// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
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
	cond := serviceIs(service) +
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

// serviceIs is the OTTL condition for one service's data.
func serviceIs(service string) string {
	return `resource.attributes["service.name"] == ` + ottlString(service)
}

// DeleteSpanAttribute removes one attribute from a service's spans.
func DeleteSpanAttribute(config, transformType, service, key string, sluicio SluicioExporter) (FilterChange, error) {
	return AddSpanStatement(config, transformType, "delete_key(attributes, "+ottlString(key)+") where "+serviceIs(service), sluicio)
}

// DeleteSpanAttributePrefix removes every attribute whose key starts with
// prefix from a service's spans (echoed request headers).
func DeleteSpanAttributePrefix(config, transformType, service, prefix string, sluicio SluicioExporter) (FilterChange, error) {
	re := "^" + regexp.QuoteMeta(prefix)
	return AddSpanStatement(config, transformType, "delete_matching_keys(attributes, "+ottlString(re)+") where "+serviceIs(service), sluicio)
}

// HashSpanAttribute replaces one attribute's value on a service's spans
// with its SHA-256, so equal values still match while the readable value
// is not stored. Pseudonymous, not anonymous: a value from a small or
// guessable set (a national ID number, an email address) can be found
// again by hashing candidates.
func HashSpanAttribute(config, transformType, service, key string, sluicio SluicioExporter) (FilterChange, error) {
	a := "attributes[" + ottlString(key) + "]"
	return AddSpanStatement(config, transformType, "set("+a+", SHA256("+a+")) where "+serviceIs(service)+" and "+a+" != nil", sluicio)
}

// AddFilter adds condition to the Advisor's filter processor (filterType
// is the version's name for it) for sig, and the processor to every
// pipeline of sig that sends only to Sluicio. config is the
// configuration as written; a masked one stays masked. Nothing else in
// it changes.
func AddFilter(config, filterType string, sig FilterSignal, condition string, sluicio SluicioExporter) (FilterChange, error) {
	return applyProcessor(config, filterType+"/"+AdvisorProcessorName, sig.Pipeline, sluicio, func(proc *yaml.Node) bool {
		ensureErrorMode(proc)
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
		return addUnique(conds, condition)
	})
}

// AddSpanStatement adds an OTTL statement in the span context to the
// Advisor's transform processor (transformType is the version's name for
// it), and the processor to every traces pipeline that sends only to
// Sluicio. A statement only ever changes attributes: spans are never
// dropped, so an integration's messages stay complete.
func AddSpanStatement(config, transformType, statement string, sluicio SluicioExporter) (FilterChange, error) {
	return applyProcessor(config, transformType+"/"+AdvisorProcessorName, "traces", sluicio, func(proc *yaml.Node) bool {
		ensureErrorMode(proc)
		groups := mapGet(proc, "trace_statements")
		if groups == nil || groups.Kind != yaml.SequenceNode {
			groups = &yaml.Node{Kind: yaml.SequenceNode}
			mapSet(proc, "trace_statements", groups)
		}
		var span *yaml.Node
		for _, g := range groups.Content {
			if c := mapGet(g, "context"); c != nil && c.Value == "span" {
				span = g
			}
		}
		if span == nil {
			span = &yaml.Node{Kind: yaml.MappingNode}
			setKey(span, "context", scalar("span"))
			setKey(span, "statements", &yaml.Node{Kind: yaml.SequenceNode})
			groups.Content = append(groups.Content, span)
		}
		stmts := mapGet(span, "statements")
		if stmts == nil || stmts.Kind != yaml.SequenceNode {
			stmts = &yaml.Node{Kind: yaml.SequenceNode}
			mapSet(span, "statements", stmts)
		}
		return addUnique(stmts, statement)
	})
}

// ensureErrorMode starts a filter or transform processor Sluicio adds
// with error_mode: ignore, so a statement that fails on one record does
// not stop the pipeline.
func ensureErrorMode(proc *yaml.Node) {
	if mapGet(proc, "error_mode") == nil {
		proc.Content = append([]*yaml.Node{scalar("error_mode"), scalar("ignore")}, proc.Content...)
	}
}

// CollectorStampKey is the resource attribute a stamp adds; cell-ingest
// moves it into a column of its own (see services/cell-ingest).
const CollectorStampKey = "sluicio.collector.id"

// StampProcessor is the resource processor the stamp is.
const StampProcessor = "resource/sluicio-stamp"

// AddStamp makes a collector put its own id on the data it carries:
// a resource processor inserting sluicio.collector.id = id, in every
// traces, metrics and logs pipeline that sends only to Sluicio. Insert,
// so data already stamped by a collector before it on the way keeps that
// stamp (the first collector counts). A stamp with another id (a
// configuration copied from another collector) is corrected.
func AddStamp(config, id string, sluicio SluicioExporter) (FilterChange, error) {
	out := FilterChange{Changed: []string{}, Skipped: []PipelineSkip{}}
	allThere := true
	for _, pipeline := range []string{"traces", "metrics", "logs"} {
		c, err := applyProcessor(config, StampProcessor, pipeline, sluicio, func(proc *yaml.Node) bool {
			attrs := mapGet(proc, "attributes")
			if attrs == nil || attrs.Kind != yaml.SequenceNode {
				attrs = &yaml.Node{Kind: yaml.SequenceNode}
				mapSet(proc, "attributes", attrs)
			}
			for _, a := range attrs.Content {
				if k := mapGet(a, "key"); k != nil && k.Value == CollectorStampKey {
					if v := mapGet(a, "value"); v != nil && v.Value == id {
						return true
					}
					mapSet(a, "value", scalar(id))
					mapSet(a, "action", scalar("insert"))
					return false
				}
			}
			item := &yaml.Node{Kind: yaml.MappingNode}
			setKey(item, "key", scalar(CollectorStampKey))
			setKey(item, "value", &yaml.Node{Kind: yaml.ScalarNode, Value: id, Style: yaml.DoubleQuotedStyle})
			setKey(item, "action", scalar("insert"))
			attrs.Content = append(attrs.Content, item)
			return false
		})
		if err != nil {
			return FilterChange{}, err
		}
		out.Skipped = append(out.Skipped, c.Skipped...)
		switch {
		case c.Config != "":
			config, allThere = c.Config, false
			out.Changed = append(out.Changed, c.Changed...)
		case !c.AlreadyDropped && len(c.Changed) == 0:
			// no pipeline of this signal sends to Sluicio alone
		}
	}
	if allThere {
		out.AlreadyDropped = true
		return out, nil
	}
	out.Config = config
	sort.Strings(out.Changed)
	return out, nil
}

// addUnique appends v as a single-quoted item unless the list has it, and
// says whether it was there already.
func addUnique(list *yaml.Node, v string) bool {
	for _, c := range list.Content {
		if c.Value == v {
			return true
		}
	}
	list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: v, Style: yaml.SingleQuotedStyle})
	return false
}

// applyProcessor is the part every change Sluicio writes shares: find the
// pipelines of pipelineType that send to Sluicio alone, let add put the
// change into the processor procID (made empty when missing; add reports
// whether the change was there already), and put the processor into
// those pipelines.
func applyProcessor(config, procID, pipelineType string, sluicio SluicioExporter, add func(proc *yaml.Node) bool) (FilterChange, error) {
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

	out := FilterChange{Changed: []string{}, Skipped: []PipelineSkip{}}
	var eligible []*yaml.Node
	for i := 0; i+1 < len(pipelines.Content); i += 2 {
		name, p := pipelines.Content[i].Value, pipelines.Content[i+1]
		if name != pipelineType && !strings.HasPrefix(name, pipelineType+"/") {
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
			out.Skipped = append(out.Skipped, PipelineSkip{name, "also sends to " + strings.Join(elsewhere, ", ") + ", which would get the change too"})
		default:
			eligible = append(eligible, p)
			out.Changed = append(out.Changed, name)
		}
	}
	if len(eligible) == 0 {
		return out, nil
	}

	// The processor: one of each kind per collector, every change in it.
	processors := mapGet(root, "processors")
	if processors == nil {
		processors = &yaml.Node{Kind: yaml.MappingNode}
		insertBefore(root, "service", "processors", processors)
	}
	proc := mapGet(processors, procID)
	if proc == nil || proc.Kind != yaml.MappingNode {
		proc = &yaml.Node{Kind: yaml.MappingNode}
		mapSet(processors, procID, proc)
	}
	have := add(proc)

	// Into each pipeline, early: before anything spends work on the
	// data, but after a memory_limiter, which belongs first.
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
