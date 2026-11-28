// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Dropping a metric where it is scraped. A Prometheus receiver's scrape
// job can drop a series before the collector turns it into OTLP, which
// is cheaper than a filter processor and is where an operator would put
// it. In collector 0.166.0 (checked) the receiver keeps the exposition
// name as the metric name, suffixes included (a counter stays
// "..._total"), so a relabel rule on __name__ matches the name Sluicio
// stores. Which job a metric comes from cannot be told from the data:
// target_info's service_name, not the job name, becomes service.name.
// The rule goes into every job of the receiver, which drops what a
// pipeline filter on the name would drop from that receiver.

// ScrapeJob is a scrape job a change went into: the receiver and the
// job's name.
type ScrapeJob struct {
	Receiver string `json:"receiver"`
	Job      string `json:"job"`
}

// DropMetric drops one metric from every metrics pipeline that sends only
// to Sluicio. Where all of a pipeline's receivers are Prometheus
// receivers whose scrape jobs can be changed, the metric is dropped in
// those jobs; any other pipeline gets the Advisor's filter processor
// (filterType is the version's name for it).
func DropMetric(config, filterType, metric string, sluicio SluicioExporter) (FilterChange, error) {
	doc, root, err := parseConfig(config)
	if err != nil {
		return FilterChange{}, err
	}
	out, eligible, err := sluicioPipelines(root, MetricsSignal.Pipeline, sluicio)
	if err != nil {
		return FilterChange{}, err
	}
	if len(eligible) == 0 {
		return out, nil
	}

	// A receiver's jobs feed every pipeline it is in, so it is changed
	// only when all of them send to Sluicio alone.
	ok := map[string]bool{}
	for _, p := range eligible {
		ok[p.name] = true
	}
	usedBy := map[string][]string{}
	pipelines := mapGet(mapGet(root, "service"), "pipelines")
	for i := 0; i+1 < len(pipelines.Content); i += 2 {
		for _, r := range seqValues(mapGet(pipelines.Content[i+1], "receivers")) {
			usedBy[r] = append(usedBy[r], pipelines.Content[i].Value)
		}
	}
	receivers := mapGet(root, "receivers")
	droppable := map[string]bool{}
	for id, names := range usedBy {
		all := true
		for _, n := range names {
			all = all && ok[n]
		}
		if all && scrapeJobs(id, mapGet(receivers, id)) != nil {
			droppable[id] = true
		}
	}

	changed := false
	var rest []namedPipeline
	for _, p := range eligible {
		recv := seqValues(mapGet(p.node, "receivers"))
		covered := len(recv) > 0
		for _, r := range recv {
			covered = covered && droppable[r]
		}
		if !covered {
			rest = append(rest, p)
		}
	}
	for id := range droppable {
		for _, job := range scrapeJobs(id, mapGet(receivers, id)) {
			if addScrapeDrop(job, metric) {
				changed = true
			}
			if n := mapGet(job, "job_name"); n != nil {
				out.ScrapeJobs = append(out.ScrapeJobs, ScrapeJob{Receiver: id, Job: n.Value})
			}
		}
	}
	sort.Slice(out.ScrapeJobs, func(i, k int) bool {
		a, b := out.ScrapeJobs[i], out.ScrapeJobs[k]
		return a.Receiver < b.Receiver || (a.Receiver == b.Receiver && a.Job < b.Job)
	})
	if len(rest) > 0 && insertProcessor(root, filterType+"/"+AdvisorProcessorName, rest, addCondition(MetricsSignal, "name == "+ottlString(metric))) {
		changed = true
	}
	if !changed {
		out.AlreadyDropped = true
		out.Changed = []string{}
		out.ScrapeJobs = nil
		return out, nil
	}
	s, err := encode(doc)
	if err != nil {
		return FilterChange{}, err
	}
	out.Config = s
	return out, nil
}

// scrapeJobs is the scrape jobs of a Prometheus receiver when they can be
// changed here: listed inline. Nil for another receiver, or when jobs
// come from scrape_config_files or a target allocator, which are not in
// the configuration Sluicio sends.
func scrapeJobs(id string, settings *yaml.Node) []*yaml.Node {
	if typ, _, _ := strings.Cut(id, "/"); typ != "prometheus" || settings == nil {
		return nil
	}
	if mapGet(settings, "target_allocator") != nil {
		return nil
	}
	cfg := mapGet(settings, "config")
	if cfg == nil || mapGet(cfg, "scrape_config_files") != nil {
		return nil
	}
	jobs := mapGet(cfg, "scrape_configs")
	if jobs == nil || jobs.Kind != yaml.SequenceNode || len(jobs.Content) == 0 {
		return nil
	}
	for _, j := range jobs.Content {
		if j.Kind != yaml.MappingNode {
			return nil
		}
	}
	return jobs.Content
}

// addScrapeDrop appends a rule dropping metric to a job's
// metric_relabel_configs, last, so it sees the name any earlier rule
// gave the series. It reports whether it added one.
func addScrapeDrop(job *yaml.Node, metric string) bool {
	regex := regexp.QuoteMeta(metric) // Prometheus anchors the regex
	rules := mapGet(job, "metric_relabel_configs")
	if rules == nil || rules.Kind != yaml.SequenceNode {
		rules = &yaml.Node{Kind: yaml.SequenceNode}
		mapSet(job, "metric_relabel_configs", rules)
	}
	for _, r := range rules.Content {
		a, re := mapGet(r, "action"), mapGet(r, "regex")
		if a != nil && a.Value == "drop" && re != nil && re.Value == regex && strings.Join(seqValues(mapGet(r, "source_labels")), ",") == "__name__" {
			return false
		}
	}
	rule := &yaml.Node{Kind: yaml.MappingNode}
	setKey(rule, "source_labels", &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Content: []*yaml.Node{scalar("__name__")}})
	setKey(rule, "regex", &yaml.Node{Kind: yaml.ScalarNode, Value: regex, Style: yaml.SingleQuotedStyle})
	setKey(rule, "action", scalar("drop"))
	rules.Content = append(rules.Content, rule)
	return true
}
