// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// MessageFacts is the message an alert is about: the newest span or log
// record in the rule's scope that met the rule's own filter, and what it
// carried for each of the filter's keys. A rule on "documents.failed > 0"
// then arrives saying documents.failed = 3 rather than only that the
// count was breached. Resolved at delivery for a firing notification;
// nil for a rule without a filter or when nothing in scope matched.
type MessageFacts struct {
	TraceID    string            `json:"trace_id,omitempty"`
	Service    string            `json:"service,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// RuleFilter is what a rule's filter asks of a message, read from its
// rule_spec: the predicates, which records they apply to, and how far
// back the rule looks.
type RuleFilter struct {
	Attrs []AttrFilter
	// Logs is true for a log rule; otherwise the filter is on spans.
	Logs bool
	// ErrorsOnly keeps failing spans, as a failed-trace rule counts them.
	ErrorsOnly bool
	Window     time.Duration
}

// Keys are the filter's attribute keys, once each, in the order written.
func (f RuleFilter) Keys() []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range f.Attrs {
		k := strings.TrimSpace(a.Key)
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// RuleFilterOf reads the filter of a rule that has one on messages: a
// log rule, a failed-trace rule or a trace-attribute rule. ok is false
// for the rest (a metric series is not a message) and for a rule whose
// filter is empty.
func RuleFilterOf(signal, kind string, spec json.RawMessage) (RuleFilter, bool) {
	var f RuleFilter
	switch {
	case signal == SignalLog:
		var s LogRuleSpec
		if json.Unmarshal(spec, &s) != nil {
			return f, false
		}
		f = RuleFilter{Attrs: s.Attrs, Logs: true, Window: s.WindowDuration()}
	case kind == TraceErrorSpecKind:
		var s TraceErrorRuleSpec
		if json.Unmarshal(spec, &s) != nil {
			return f, false
		}
		f = RuleFilter{Attrs: s.Attrs, ErrorsOnly: true, Window: s.WindowDuration()}
	case kind == TraceAttributeSpecKind:
		var s TraceAttributeRuleSpec
		if json.Unmarshal(spec, &s) != nil {
			return f, false
		}
		f = RuleFilter{Attrs: s.Attrs, Window: s.WindowDuration()}
	default:
		return f, false
	}
	return f, len(f.Keys()) > 0
}

// factLines are the plain-text lines that say where an alert is and what
// the message carried: the integration, the telemetry environment, then
// each attribute of the triggering message. They follow the summary in
// the plain-text body, which is also what Slack and PagerDuty show
// without a template.
func (c *AlertContext) factLines() []string {
	var out []string
	switch {
	case c.Integration != nil && c.Integration.Name != "":
		out = append(out, "Integration: "+c.Integration.Name)
	case c.Service != nil && len(c.Service.Integrations) > 0:
		label := "Integration: "
		if len(c.Service.Integrations) > 1 {
			label = "Integrations: "
		}
		out = append(out, label+strings.Join(c.Service.Integrations, ", "))
	}
	if c.Alert.Environment != "" {
		out = append(out, "Environment: "+c.Alert.Environment)
	}
	if c.Message != nil {
		keys := make([]string, 0, len(c.Message.Attributes))
		for k := range c.Message.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, fmt.Sprintf("%s: %s", k, c.Message.Attributes[k]))
		}
	}
	return out
}
