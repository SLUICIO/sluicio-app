// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// AdvisorProcessorName is the instance name of the filter processor an
// Advisor suggestion adds. One per collector: a second suggestion adds
// its condition to the same processor, so the configuration says in one
// place what Sluicio advised dropping.
const AdvisorProcessorName = "sluicio-advisor"

// PipelineSkip is a pipeline a change was not applied to, and why.
type PipelineSkip struct {
	Pipeline string `json:"pipeline"`
	Reason   string `json:"reason"`
}

// FilterChange is an Advisor suggestion applied to one configuration.
type FilterChange struct {
	Config string `json:"config"`
	// Changed are the pipelines of the signal the filter was added to.
	Changed []string `json:"changed"`
	// Skipped are the pipelines of the signal left alone: they do not
	// send to Sluicio, or they also send somewhere else, where dropping
	// the data would take it from a backend that may well use it.
	Skipped []PipelineSkip `json:"skipped"`
	// AlreadyDropped: the configuration drops this already.
	AlreadyDropped bool `json:"already_dropped"`
}

// FilterSignal is a signal the filter processor can drop from: the
// pipeline type, the processor's section for it and the OTTL context its
// conditions are written in.
type FilterSignal struct {
	Pipeline, Section, Context string
}

var (
	MetricsSignal = FilterSignal{Pipeline: "metrics", Section: "metrics", Context: "metric"}
	LogsSignal    = FilterSignal{Pipeline: "logs", Section: "logs", Context: "log_record"}
)

// ottlString quotes s as an OTTL string literal.
func ottlString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// DropMetric drops one metric from every metrics pipeline that sends only
// to Sluicio (filterType is the version's name for the filter processor).
func DropMetric(config, filterType, metric string, sluicio SluicioExporter) (FilterChange, error) {
	return AddFilter(config, filterType, MetricsSignal, "name == "+ottlString(metric), sluicio)
}

// DropLogsBelow drops one service's log records below severity (an OTTL
// severity enum, SEVERITY_NUMBER_WARN) from every logs pipeline that
// sends only to Sluicio. Records without a severity are kept: a floor
// cannot tell their errors from their noise.
func DropLogsBelow(config, filterType, service, severity string, sluicio SluicioExporter) (FilterChange, error) {
	cond := `resource.attributes["service.name"] == ` + ottlString(service) +
		" and severity_number > SEVERITY_NUMBER_UNSPECIFIED and severity_number < " + severity
	return AddFilter(config, filterType, LogsSignal, cond, sluicio)
}

// SluicioExporter tells an exporter that sends to this cell: an OTLP
// exporter whose endpoint is the cell's ingest host, or, as the docs and
// OTelFlow name it, one whose instance name says sluicio
// (otlp_http/sluicio).
type SluicioExporter struct {
	// IngestHosts are the hosts this cell ingests on (from
	// SLUICIO_INGEST_URL), lower case. Empty: recognised by name only.
	IngestHosts []string
}

// Is reports whether the exporter with this id and settings sends to the
// cell.
func (s SluicioExporter) Is(id string, settings *yaml.Node) bool {
	typ, name, _ := strings.Cut(id, "/")
	if !strings.HasPrefix(typ, "otlp") {
		return false
	}
	if strings.Contains(strings.ToLower(name), "sluicio") {
		return true
	}
	if settings == nil || len(s.IngestHosts) == 0 {
		return false
	}
	ep := mapGet(settings, "endpoint")
	if ep == nil || ep.Kind != yaml.ScalarNode {
		return false
	}
	raw := ep.Value
	if !strings.Contains(raw, "://") {
		raw = "//" + raw // the gRPC exporter takes host:port
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range s.IngestHosts {
		if host == h {
			return true
		}
	}
	return false
}

// AddFilter adds condition to the Advisor's filter processor (filterType
// is the version's name for it) for sig, and the processor to every
// pipeline of sig that sends only to Sluicio. config is the
// configuration as written; a masked one stays masked. Nothing else in
// it changes.
func AddFilter(config, filterType string, sig FilterSignal, condition string, sluicio SluicioExporter) (FilterChange, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(config), &doc); err != nil {
		return FilterChange{}, fmt.Errorf("the configuration is not YAML: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return FilterChange{}, errors.New("the configuration is not a YAML map")
	}
	root := doc.Content[0]
	pipelines := mapGet(mapGet(root, "service"), "pipelines")
	if pipelines == nil || pipelines.Kind != yaml.MappingNode {
		return FilterChange{}, errors.New("the configuration has no service pipelines")
	}
	exporters := mapGet(root, "exporters")
	procID := filterType + "/" + AdvisorProcessorName

	out := FilterChange{Changed: []string{}, Skipped: []PipelineSkip{}}
	var eligible []*yaml.Node
	for i := 0; i+1 < len(pipelines.Content); i += 2 {
		name, p := pipelines.Content[i].Value, pipelines.Content[i+1]
		if name != sig.Pipeline && !strings.HasPrefix(name, sig.Pipeline+"/") {
			continue
		}
		toSluicio, elsewhere := 0, []string{}
		for _, e := range seqValues(mapGet(p, "exporters")) {
			if sluicio.Is(e, mapGet(exporters, e)) {
				toSluicio++
			} else {
				elsewhere = append(elsewhere, e)
			}
		}
		switch {
		case toSluicio == 0:
			out.Skipped = append(out.Skipped, PipelineSkip{name, "does not send to Sluicio"})
		case len(elsewhere) > 0:
			out.Skipped = append(out.Skipped, PipelineSkip{name, "also sends to " + strings.Join(elsewhere, ", ") + ", which would lose the data too"})
		default:
			eligible = append(eligible, p)
			out.Changed = append(out.Changed, name)
		}
	}
	if len(eligible) == 0 {
		return out, nil
	}

	// The processor: one per collector, one condition per metric.
	processors := mapGet(root, "processors")
	if processors == nil {
		processors = &yaml.Node{Kind: yaml.MappingNode}
		insertBefore(root, "service", "processors", processors)
	}
	proc := mapGet(processors, procID)
	if proc == nil || proc.Kind != yaml.MappingNode {
		proc = &yaml.Node{Kind: yaml.MappingNode}
		setKey(proc, "error_mode", scalar("ignore"))
		mapSet(processors, procID, proc)
	}
	section := mapGet(proc, sig.Section)
	if section == nil {
		section = &yaml.Node{Kind: yaml.MappingNode}
		setKey(proc, sig.Section, section)
	}
	conds := mapGet(section, sig.Context)
	if conds == nil || conds.Kind != yaml.SequenceNode {
		conds = &yaml.Node{Kind: yaml.SequenceNode}
		mapSet(section, sig.Context, conds)
	}
	have := false
	for _, c := range conds.Content {
		if c.Value == condition {
			have = true
		}
	}
	if !have {
		conds.Content = append(conds.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: condition, Style: yaml.SingleQuotedStyle})
	}

	// Into each pipeline, early: dropped before anything spends work on
	// it, but after a memory_limiter, which belongs first.
	added := false
	for _, p := range eligible {
		list := mapGet(p, "processors")
		if list == nil || list.Kind != yaml.SequenceNode {
			list = &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
			insertBefore(p, "exporters", "processors", list)
		}
		if contains(seqValues(list), procID) {
			continue
		}
		at := 0
		if len(list.Content) > 0 && strings.HasPrefix(list.Content[0].Value, "memory_limiter") {
			at = 1
		}
		item := scalar(procID)
		list.Content = append(list.Content[:at], append([]*yaml.Node{item}, list.Content[at:]...)...)
		added = true
	}
	if have && !added {
		out.AlreadyDropped = true
		out.Changed = []string{}
		return out, nil
	}
	s, err := encode(&doc)
	if err != nil {
		return FilterChange{}, err
	}
	out.Config = s
	sort.Strings(out.Changed)
	return out, nil
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v} }

func seqValues(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(n.Content))
	for _, c := range n.Content {
		out = append(out, c.Value)
	}
	return out
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// setKey appends key: v to a map.
func setKey(m *yaml.Node, key string, v *yaml.Node) {
	m.Content = append(m.Content, scalar(key), v)
}

// mapSet replaces key's value, or appends it.
func mapSet(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	setKey(m, key, v)
}

// insertBefore adds key: v before the key named before, or at the end.
func insertBefore(m *yaml.Node, before, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == before {
			m.Content = append(m.Content[:i], append([]*yaml.Node{scalar(key), v}, m.Content[i:]...)...)
			return
		}
	}
	setKey(m, key, v)
}
