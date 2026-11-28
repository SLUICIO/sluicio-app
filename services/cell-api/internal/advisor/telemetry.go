// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The Telemetry Advisor (design §3): join what the cell ingests against
// what anything consumes, and suggest collector changes for the
// difference.
//
// The guardrails are the feature. An advisor that suggests dropping
// something load-bearing is worse than no advisor, because the first
// time it happens the operator stops trusting every other suggestion
// too — and they are right to. So each evaluator refuses in four
// directions before it proposes anything:
//
//  1. ANY demand kills it. One human view in the window is enough; we
//     do not weigh "only viewed twice" against bytes saved, because a
//     rarely-consulted signal is exactly the kind you miss when it is
//     gone.
//  2. MECHANICAL demand vetoes regardless of the window. Config is
//     demand for as long as it exists — a metric behind a paused rule,
//     an attribute behind a facet mapping or an integration matcher.
//  3. QUARANTINE. Telemetry first seen inside the observation window is
//     never judged: it has had no chance to be consumed, and "nobody
//     looked at your new metric in the four days it has existed" is an
//     accusation the data cannot support.
//  4. VOLUME FLOOR. A finding that saves nothing measurable is noise
//     wearing the costume of advice, and the cost of reading it falls
//     on a human every night.
//
// One more rule sits above all four, and it is a product promise rather
// than a heuristic: nothing here ever proposes sampling or dropping
// integration MESSAGES. Completeness is what Sluicio sells. The savings
// live in attributes, logs, metrics and noise. The one span finding (T7)
// is a span nothing can miss: an internal step inside one service, never
// a root, never a call to another service, never anyone's parent,
// carrying nothing Sluicio reads; its trace keeps every span that makes
// it a message, and error spans are always kept.
package advisor

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectorversion"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/servicetypes"
)

// Thresholds are the volume floors below which a finding is not worth
// an operator's attention. Deliberately blunt round numbers: they exist
// to filter noise, and pretending to more precision than that would
// invite tuning them forever.
const (
	// minMetricRowsPerDay: below this a metric costs nothing worth a
	// config change.
	minMetricRowsPerDay = 100
	// minLogRowsPerDay for a severity-floor suggestion.
	minLogRowsPerDay = 1_000
	// minAttrBytesPerDay before deleting an attribute is worth doing.
	minAttrBytesPerDay = 100_000
	// attrPresenceFloor: an attribute must be near-universal on a
	// service's spans before it counts as structural cost rather than a
	// conditional debug field.
	attrPresenceFloor = 0.5
	// highCardinalityValues: distinct values above which an attribute is
	// worth flagging for review (it inflates storage and index size out
	// of proportion to what it can be used for).
	highCardinalityValues = 1_000
)

// echoPrefixes are attribute families that mirror request/response data
// into spans. They are individually small, collectively enormous, and
// almost never read — the KrakenD `report_headers` case in the design.
var echoPrefixes = []string{
	"http.request.header.",
	"http.response.header.",
	"rpc.request.metadata.",
	"rpc.response.metadata.",
}

// piiPatterns match VALUES that look like personal data. Deliberately
// conservative: a false positive here sends someone to read a
// compliance finding that is not one, which spends the credibility the
// advisor needs for its real findings.
var piiPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"email address", regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[A-Za-z]{2,}$`)},
	{"IBAN", regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}$`)},
	// Swedish personnummer, the shape this product's customers actually
	// handle: YYYYMMDD-NNNN or YYMMDD-NNNN.
	{"national ID number", regexp.MustCompile(`^(19|20)?[0-9]{6}[-+][0-9]{4}$`)},
}

// TelemetryInput is everything an evaluation needs that it cannot query
// for itself.
type TelemetryInput struct {
	OrgID  uuid.UUID
	Conn   driver.Conn
	Demand *DemandSet
	// From/To bound the observation window; anything first seen after
	// From is quarantined.
	From, To time.Time
	// IntegrationServices are services that belong to an integration.
	// Their spans are never proposed for sampling or dropping — the
	// completeness promise — though their ATTRIBUTES still can be.
	IntegrationServices map[string]bool
	// Target resolves which collector a suggestion's snippet is written
	// for (issue #16). Per service, because a snippet always targets one
	// service's pipeline and an estate can run different collectors on
	// different hosts; the empty string asks for the org default, which
	// is what a metric-scoped suggestion needs.
	//
	// Optional. Nil resolves to the newest version this build knows,
	// which is what an unconfigured cell should emit.
	Target func(service string) collectorversion.Target
	// Reserved is what the org's configuration reads in code (see
	// Reserved), beyond the keys every cell reserves.
	Reserved Reserved
}

// Reserved is telemetry Sluicio reads in code rather than through a
// query, so the demand ledger cannot show it, per org: the prefixes
// that detect a system type, by span attribute and by metric name, and
// the attribute the cell reads the environment from.
type Reserved struct {
	AttrPrefixes   []string
	MetricPrefixes []string
	// FlowAttrs are, per service, the span attributes that split it into
	// flows, as the integration candidates propose them (issue #18). The
	// proposal reads them to suggest integrations, so the attribute that
	// would define one is never suggested for deletion.
	FlowAttrs map[string][]string
	// SpanName reports whether something in Sluicio selects spans by this
	// name with a pattern or a negation (a span.name matcher that is not a
	// plain equals), which no single demand key can say. Nil: nothing does.
	SpanName func(name string) bool
}

// targetFor is Target with the nil case folded in, so call sites read as
// one expression.
func (in TelemetryInput) targetFor(service string) collectorversion.Target {
	if in.Target == nil {
		return collectorversion.Default()
	}
	return in.Target(service)
}

// withSnippet attaches a generated snippet to a suggestion, or records
// why there is none.
//
// The rule this enforces is the one the issue turns on: a snippet that
// cannot be expressed for the target collector is NOT rendered with a
// warning beside it. YAML that does not start is not improved by a
// caveat — the reader is pasting into production at the moment they
// would have to weigh it, and the cost of our uncertainty lands on
// them.
//
// The FINDING still stands. The cost it describes is real whether or
// not we can write the fix, and withholding the whole suggestion would
// hide a real bill because of a limitation of ours. So the suggestion
// is kept and says plainly that it cannot be expressed here.
func (in TelemetryInput) withSnippet(s Suggestion, service string, gen func(collectorversion.Target) (string, error)) Suggestion {
	t := in.targetFor(service)
	s.SnippetTarget = t.Version
	snippet, err := gen(t)
	if err != nil {
		s.SnippetUnavailable = fmt.Sprintf(
			"Sluicio cannot write this change for collector %s: %v. "+
				"The finding stands; the configuration for it has to be made by hand.",
			t.Version, err)
		return s
	}
	s.Snippet = snippet
	return s
}

// EvaluateTelemetry runs T1–T6 and returns the findings, most valuable
// first is left to the caller (weight carries the ranking).
func EvaluateTelemetry(ctx context.Context, in TelemetryInput) ([]Suggestion, error) {
	// The absence of demand is only evidence if we were watching for the
	// whole window. See DemandSet.Mature — without this a cell whose
	// ledger is younger than the window would be told to delete the
	// telemetry it uses most.
	if !in.Demand.Mature(in.From) {
		return nil, nil
	}
	var out []Suggestion

	metrics, err := MetricsSupply(ctx, in.Conn, in.OrgID, in.From, in.To)
	if err != nil {
		return nil, fmt.Errorf("advisor: metric supply: %w", err)
	}
	out = append(out, evalUnusedMetrics(in, metrics)...)

	streams, err := LogStreamsSupply(ctx, in.Conn, in.OrgID, in.From, in.To)
	if err != nil {
		return nil, fmt.Errorf("advisor: log supply: %w", err)
	}
	out = append(out, evalUnviewedLogs(in, streams)...)

	attrs, err := SpanAttrsSupply(ctx, in.Conn, in.OrgID, in.From, in.To, 0)
	if err != nil {
		return nil, fmt.Errorf("advisor: span attribute supply: %w", err)
	}
	out = append(out, evalSpanAttributes(in, attrs)...)
	out = append(out, evalPIIAttributes(ctx, in, attrs)...)

	names, err := SpanNamesSupply(ctx, in.Conn, in.OrgID, in.From, in.To, 0)
	if err != nil {
		return nil, fmt.Errorf("advisor: span name supply: %w", err)
	}
	out = append(out, evalInternalSpans(in, names)...)

	return out, nil
}

// --- T7: internal spans nothing reads -----------------------------------

// evalInternalSpans finds span names a service could stop sending without
// anything in Sluicio missing them. Every condition is read from the data
// or from Sluicio's own configuration, and every one of them errs towards
// not suggesting:
//
//   - always an Internal span whose parent is a span of the same service:
//     never a trace's root (dropping it could drop a message), never a
//     call between services (topology and a message's path are built from
//     those), never the earliest span of its service in the trace (an
//     ungated completion rule starts its clock there);
//   - never the parent of another span, so no span is left without its
//     parent and no step in the waterfall loses its place;
//   - no span links (the hand-offs that chain traces);
//   - no demand on the span name (a trace-completion step or start span)
//     or on any key it carries (a matcher, a check, a facet, a search),
//     and none on the service's traces as a whole: any check on them,
//     latency ones included (a quantile over every span would shift);
//   - the usual quarantine and volume floor.
//
// The filter keeps error spans of the name whatever the rest says.
func evalInternalSpans(in TelemetryInput, names []SpanNameSupply) []Suggestion {
	out := []Suggestion{}
	for _, n := range names {
		if n.Spans == 0 || n.NotInternal > 0 || n.Foreign > 0 || n.Parents > 0 || n.Linked > 0 {
			continue
		}
		if in.quarantined(n.FirstSeen) {
			continue
		}
		if in.Demand.Mechanical("trace", n.Service, n.Name) || in.Demand.ConsumedSince("trace", n.Service, n.Name, in.From) {
			continue
		}
		if in.Reserved.SpanName != nil && in.Reserved.SpanName(n.Name) {
			continue
		}
		read := false
		for _, k := range n.Keys {
			if in.reservedAttrOn(n.Service, k) || in.Demand.Mechanical("trace", n.Service, k) || in.Demand.ConsumedSince("trace", n.Service, k, in.From) {
				read = true
				break
			}
		}
		if read || n.EstBytesPerDay < minAttrBytesPerDay {
			continue
		}
		out = append(out, in.withSnippet(Suggestion{
			Fingerprint: fmt.Sprintf("T7|span|%s|%s", n.Service, n.Name),
			Class:       "T7",
			Advisor:     "telemetry",
			ScopeKind:   "span",
			ScopeID:     n.Service + "." + n.Name,
			Title:       fmt.Sprintf("%s's internal %q spans are read by nothing", n.Service, n.Name),
			Loss: fmt.Sprintf("These steps no longer appear inside %s's messages in the waterfall, and the span counts "+
				"and span-level error rate of %s include fewer successful spans. The messages, their path between "+
				"services, their duration and every check are untouched; failed %q spans are always kept.",
				n.Service, n.Service, n.Name),
			Weight: int64(n.EstBytesPerDay),
			Evidence: map[string]any{
				"spans_in_sample":   n.Spans,
				"kind":              "internal, inside the service, no children, no links",
				"est_bytes_per_day": n.EstBytesPerDay,
				"first_seen":        n.FirstSeen.Format(time.RFC3339),
				"demand":            lastSeenPhrase(in.Demand.LastConsumed("trace", n.Service, n.Name), in.To),
			},
		}, n.Service, func(t collectorversion.Target) (string, error) { return snippetDropInternalSpans(n.Service, n.Name, t) }))
	}
	return out
}

// quarantined reports whether telemetry is too young to judge.
func (in TelemetryInput) quarantined(firstSeen time.Time) bool {
	return firstSeen.IsZero() || firstSeen.After(in.From)
}

func days(d time.Duration) int { return int(d.Hours() / 24) }

// lastSeenPhrase renders the demand evidence an operator actually reads.
func lastSeenPhrase(last time.Time, now time.Time) string {
	if last.IsZero() {
		return "never consumed"
	}
	return fmt.Sprintf("last consumed %d days ago", days(now.Sub(last)))
}

// --- T1: unused metric -------------------------------------------------

func evalUnusedMetrics(in TelemetryInput, metrics []MetricSupply) []Suggestion {
	out := []Suggestion{}
	for _, m := range metrics {
		if in.quarantined(m.FirstSeen) {
			continue
		}
		if in.Demand.ConsumedSince("metric", "", m.Name, in.From) || in.Demand.Mechanical("metric", "", m.Name) {
			continue
		}
		// Also check per-service demand: a metric charted while scoped
		// to one service records against that service.
		consumed := false
		for _, svc := range m.Services {
			if in.Demand.ConsumedSince("metric", svc, m.Name, in.From) || in.Demand.Mechanical("metric", svc, m.Name) {
				consumed = true
				break
			}
		}
		if consumed {
			continue
		}
		perDayRows := perDay(m.Rows, in.From, in.To)
		if perDayRows < minMetricRowsPerDay {
			continue
		}
		out = append(out, in.withSnippet(Suggestion{
			Fingerprint: "T1|metric|" + m.Name,
			Class:       "T1",
			Advisor:     "telemetry",
			ScopeKind:   "metric",
			ScopeID:     m.Name,
			Title:       fmt.Sprintf("Nothing reads the %q metric", m.Name),
			Loss: fmt.Sprintf("You lose the ability to chart %s or alert on it without collecting it again. "+
				"Existing data is untouched and stays for its retention period.", m.Name),
			Weight: int64(m.EstBytesPerDay),
			Evidence: map[string]any{
				"rows_per_day":      perDayRows,
				"series":            m.Series,
				"est_bytes_per_day": m.EstBytesPerDay,
				"services":          m.Services,
				"first_seen":        m.FirstSeen.Format(time.RFC3339),
				"demand":            lastSeenPhrase(in.Demand.LastConsumed("metric", "", m.Name), in.To),
			},
		}, "", func(t collectorversion.Target) (string, error) { return snippetDropMetric(m.Name, t) }))
	}
	return keepDetectable(out, metrics, in.Reserved.MetricPrefixes)
}

// keepDetectable keeps a system type detectable by its metrics: when
// every metric with one of its detection prefixes would be dropped, the
// one most services emit (then the cheapest) is not suggested. One hit
// is enough to detect the type, so the rest can still go.
func keepDetectable(out []Suggestion, metrics []MetricSupply, prefixes []string) []Suggestion {
	if len(prefixes) == 0 || len(out) == 0 {
		return out
	}
	suggested := map[string]bool{}
	for _, s := range out {
		suggested[s.ScopeID] = true
	}
	keep := map[string]bool{}
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		var best *MetricSupply
		all := true
		for i := range metrics {
			m := &metrics[i]
			if !strings.HasPrefix(m.Name, p) {
				continue
			}
			if !suggested[m.Name] {
				all = false
				break
			}
			if best == nil || len(m.Services) > len(best.Services) ||
				(len(m.Services) == len(best.Services) && m.EstBytesPerDay < best.EstBytesPerDay) {
				best = m
			}
		}
		if all && best != nil {
			keep[best.Name] = true
		}
	}
	if len(keep) == 0 {
		return out
	}
	kept := out[:0]
	for _, s := range out {
		if !keep[s.ScopeID] {
			kept = append(kept, s)
		}
	}
	return kept
}

// --- T2: unviewed log stream -------------------------------------------

// bandFloor maps a severity band to the floor an operator would set to
// drop it: dropping DEBUG means flooring at INFO, and so on.
//
// Band 0 is records without a severity (a filelog receiver with no
// severity parser sends every line that way). It is never proposed: a
// floor cannot tell its errors from its noise, and an unlabelled error
// line is the worst one to lose.
func bandFloor(band int32) (name, floor string) {
	switch {
	case band <= 0:
		return "", ""
	case band <= 4:
		return "TRACE", "info"
	case band <= 8:
		return "DEBUG", "info"
	case band <= 12:
		return "INFO", "warn"
	default:
		return "", "" // warn and above are never proposed for dropping
	}
}

// FloorForBand is the severity a T2 suggestion's records are dropped
// below, from the band its evidence names: TRACE and DEBUG below INFO,
// INFO below WARN. ok is false for any other band, which is never
// proposed.
func FloorForBand(band string) (floor string, ok bool) {
	switch band {
	case "TRACE", "DEBUG":
		return "INFO", true
	case "INFO":
		return "WARN", true
	}
	return "", false
}

func evalUnviewedLogs(in TelemetryInput, streams []LogStreamSupply) []Suggestion {
	out := []Suggestion{}
	for _, s := range streams {
		bandName, floor := bandFloor(s.SeverityNumber)
		if bandName == "" {
			// Warnings and errors are never proposed for dropping. They
			// are the records someone reaches for during an incident,
			// which is exactly when nobody has been "consuming" them.
			continue
		}
		if in.quarantined(s.FirstSeen) {
			continue
		}
		if in.Demand.ConsumedSince("log", s.Service, "", in.From) || in.Demand.Mechanical("log", s.Service, "") {
			continue
		}
		perDayRows := perDay(s.Rows, in.From, in.To)
		if perDayRows < minLogRowsPerDay {
			continue
		}
		out = append(out, in.withSnippet(Suggestion{
			Fingerprint: fmt.Sprintf("T2|log|%s|%s", s.Service, bandName),
			Class:       "T2",
			Advisor:     "telemetry",
			ScopeKind:   "service",
			ScopeID:     s.Service,
			Title:       fmt.Sprintf("Nobody has opened %s's %s logs", s.Service, bandName),
			Loss: fmt.Sprintf("The Logs view for %s will show nothing below %s. "+
				"Warnings and errors keep flowing, so alerting is unaffected — but if this service is "+
				"debugged by reading its %s output, you will want that back before the next incident.",
				s.Service, strings.ToUpper(floor), bandName),
			Weight: int64(s.EstBytesPerDay),
			Evidence: map[string]any{
				"severity_band":     bandName,
				"rows_per_day":      perDayRows,
				"est_bytes_per_day": s.EstBytesPerDay,
				"first_seen":        s.FirstSeen.Format(time.RFC3339),
				"demand":            lastSeenPhrase(in.Demand.LastConsumed("log", s.Service, ""), in.To),
			},
		}, s.Service, func(t collectorversion.Target) (string, error) { return snippetLogFloor(s.Service, floor, t) }))
	}
	return out
}

// --- T3/T4/T5: span attributes -----------------------------------------

func evalSpanAttributes(in TelemetryInput, attrs []SpanAttrSupply) []Suggestion {
	out := []Suggestion{}
	// Echo families are reported once per (service, prefix) rather than
	// once per key: forty findings that are one decision is not advice,
	// it is a wall.
	echoed := map[string]*Suggestion{}

	for _, a := range attrs {
		if in.quarantined(a.FirstSeen) {
			continue
		}
		if in.Demand.ConsumedSince("trace", a.Service, a.Key, in.From) || in.Demand.Mechanical("trace", a.Service, a.Key) {
			continue
		}
		// Attributes Sluicio itself derives meaning from are never
		// proposed, whether or not anyone queried them this month.
		if in.reservedAttrOn(a.Service, a.Key) {
			continue
		}

		// T5 — header/payload echo, grouped by family.
		if prefix := echoPrefix(a.Key); prefix != "" {
			k := a.Service + "|" + prefix
			if s, ok := echoed[k]; ok {
				s.Weight += int64(a.EstBytesPerDay)
				s.Evidence["keys"] = s.Evidence["keys"].(int) + 1
				continue
			}
			sug := in.withSnippet(Suggestion{
				Fingerprint: fmt.Sprintf("T5|attr-family|%s|%s", a.Service, prefix),
				Class:       "T5",
				Advisor:     "telemetry",
				ScopeKind:   "service",
				ScopeID:     a.Service,
				Title:       fmt.Sprintf("%s echoes %s* into every span", a.Service, prefix),
				Loss: fmt.Sprintf("Request metadata under %s* stops being searchable for %s. "+
					"Nothing in Sluicio reads these today.", prefix, a.Service),
				Weight: int64(a.EstBytesPerDay),
				Evidence: map[string]any{
					"prefix":            prefix,
					"keys":              1,
					"est_bytes_per_day": a.EstBytesPerDay,
					"demand":            "no rule, facet, matcher or query references these keys",
				},
			}, a.Service, func(t collectorversion.Target) (string, error) {
				return snippetDeleteAttrPattern(a.Service, prefix, t)
			})
			echoed[k] = &sug
			continue
		}

		presence := 0.0
		if a.SpansTotal > 0 {
			presence = float64(a.SpansWithKey) / float64(a.SpansTotal)
		}

		// T4 — high cardinality. Flagged for REVIEW, never worded as
		// safe: a high-cardinality key is often the useful one (an order
		// id), and the honest framing is "this is expensive, is it
		// worth it" rather than "delete this".
		if a.DistinctValues >= highCardinalityValues && a.EstBytesPerDay >= minAttrBytesPerDay {
			out = append(out, in.withSnippet(Suggestion{
				Fingerprint: fmt.Sprintf("T4|attr|%s|%s", a.Service, a.Key),
				Class:       "T4",
				Advisor:     "telemetry",
				ScopeKind:   "attribute",
				ScopeID:     a.Service + "." + a.Key,
				Title: fmt.Sprintf("%q on %s has %s distinct values and nothing queries it",
					a.Key, a.Service, humanCount(a.DistinctValues)),
				Loss: fmt.Sprintf("Worth a look before acting: a high-cardinality attribute is often the "+
					"useful one (an order or correlation id). If %q is how you find a specific message, "+
					"keep it — this is flagged because it is expensive, not because it is useless.", a.Key),
				Weight: int64(a.EstBytesPerDay),
				Evidence: map[string]any{
					"distinct_values":   a.DistinctValues,
					"present_on_spans":  fmt.Sprintf("%.0f%%", presence*100),
					"est_bytes_per_day": a.EstBytesPerDay,
					"review_required":   true,
					"demand":            lastSeenPhrase(in.Demand.LastConsumed("trace", a.Service, a.Key), in.To),
				},
			}, a.Service, func(t collectorversion.Target) (string, error) { return snippetDeleteSpanAttr(a.Service, a.Key, t) }))
			continue
		}

		// T3 — dead weight: structural, sizeable, unread.
		if presence >= attrPresenceFloor && a.EstBytesPerDay >= minAttrBytesPerDay {
			out = append(out, in.withSnippet(Suggestion{
				Fingerprint: fmt.Sprintf("T3|attr|%s|%s", a.Service, a.Key),
				Class:       "T3",
				Advisor:     "telemetry",
				ScopeKind:   "attribute",
				ScopeID:     a.Service + "." + a.Key,
				Title:       fmt.Sprintf("%q is on every %s span and nothing reads it", a.Key, a.Service),
				Loss: fmt.Sprintf("You can no longer filter or group %s's messages by %q. "+
					"The messages themselves are untouched — this removes one field, not the record.",
					a.Service, a.Key),
				Weight: int64(a.EstBytesPerDay),
				Evidence: map[string]any{
					"present_on_spans":  fmt.Sprintf("%.0f%%", presence*100),
					"avg_value_bytes":   int(a.AvgValueBytes),
					"est_bytes_per_day": a.EstBytesPerDay,
					"demand":            lastSeenPhrase(in.Demand.LastConsumed("trace", a.Service, a.Key), in.To),
				},
			}, a.Service, func(t collectorversion.Target) (string, error) { return snippetDeleteSpanAttr(a.Service, a.Key, t) }))
		}
	}
	for _, s := range echoed {
		out = append(out, *s)
	}
	return out
}

// --- T6: PII-shaped values ---------------------------------------------

// evalPIIAttributes samples values and reports what LOOKS like personal
// data. Shown regardless of demand: this is a compliance finding, and an
// attribute being useful does not make retaining a national ID number
// for a year less of a problem.
func evalPIIAttributes(ctx context.Context, in TelemetryInput, attrs []SpanAttrSupply) []Suggestion {
	out := []Suggestion{}
	for _, a := range attrs {
		if in.reservedAttr(a.Key) || a.SpansWithKey < 10 {
			continue
		}
		values, err := SampleAttrValues(ctx, in.Conn, in.OrgID, a.Service, a.Key, in.From, in.To, 50)
		if err != nil || len(values) == 0 {
			continue
		}
		kind, hits := classifyPII(values)
		// A majority of sampled values must match. One email in a free
		// text field is a coincidence; most of them is a column.
		if kind == "" || hits*2 <= len(values) {
			continue
		}
		out = append(out, in.withSnippet(Suggestion{
			Fingerprint: fmt.Sprintf("T6|pii|%s|%s", a.Service, a.Key),
			Class:       "T6",
			Advisor:     "telemetry",
			ScopeKind:   "attribute",
			ScopeID:     a.Service + "." + a.Key,
			Title:       fmt.Sprintf("%q on %s carries values that look like %s", a.Key, a.Service, pluralPattern(kind)),
			Loss: fmt.Sprintf("You can no longer filter or group %s's messages by %q; the messages "+
				"themselves are untouched. If you need to match the same value twice, hash it instead: "+
				"that is pseudonymisation, not anonymisation, since values from a small or guessable set "+
				"(ID numbers, email addresses) can be found again by hashing candidates.", a.Service, a.Key),
			// Compliance findings rank above cost ones: a big number
			// should not push a personal-data finding down the page.
			Weight:   int64(a.EstBytesPerDay) + 1<<40,
			Evidence: piiEvidence(kind, hits, len(values), a.SpansWithKey, in.Demand.Mechanical("trace", a.Service, a.Key)),
		}, a.Service, func(t collectorversion.Target) (string, error) { return snippetRedactAttr(a.Service, a.Key, t) }))
	}
	return out
}

// piiEvidence builds the evidence block for a compliance finding.
//
// Its whole job is to describe personal data WITHOUT carrying any: a
// finding that copies a sampled email address into the suggestions
// table has created a second copy of the problem it is reporting, in a
// table with none of the retention controls of the first. So the
// evidence is a proportion and a count, and the sampled values never
// leave the process that read them.
//
// referenced says whether Sluicio's own configuration (a matcher, facet
// or rule) reads the key: a compliance finding is shown regardless, but
// hashing such a key from here would silently break what reads it.
func piiEvidence(kind string, hits, sampled int, spans uint64, referenced bool) map[string]any {
	return map[string]any{
		"referenced_by_config":    referenced,
		"pattern":                 kind,
		"sampled_values_matching": fmt.Sprintf("%d of %d", hits, sampled),
		"present_on_spans":        spans,
		"compliance":              true,
	}
}

// pluralPattern renders a pattern name for "values that look like …".
// Naive %ss produced "email addresss"; the patterns are a closed set, so
// the plural is stated rather than derived.
func pluralPattern(kind string) string {
	switch kind {
	case "email address":
		return "email addresses"
	case "national ID number":
		return "national ID numbers"
	case "IBAN":
		return "IBANs"
	}
	return kind
}

func classifyPII(values []string) (string, int) {
	best, bestHits := "", 0
	for _, p := range piiPatterns {
		hits := 0
		for _, v := range values {
			if p.re.MatchString(strings.TrimSpace(v)) {
				hits++
			}
		}
		if hits > bestHits {
			best, bestHits = p.name, hits
		}
	}
	return best, bestHits
}

// --- helpers -----------------------------------------------------------

func echoPrefix(key string) string {
	for _, p := range echoPrefixes {
		if strings.HasPrefix(key, p) {
			return p
		}
	}
	return ""
}

// reservedAttrPrefixes are attributes Sluicio derives meaning from
// directly — service facets, io classification, integration ids. They
// are consumed by code rather than by queries, so the demand ledger
// would not necessarily show them, and a suggestion to delete one would
// break the product rather than save money.
//
// http.status_code is the older name cell-ingest still reads to treat a
// 5xx as an error; deployment.environment(.name) is where the cell reads
// a row's environment from when none is configured.
var reservedAttrPrefixes = []string{
	"io.", "service.", "integration.", "messaging.", "db.", "rpc.system",
	"http.request.method", "http.response.status_code", "http.route",
	"error.", "exception.", "peer.service", "file.", "transfer.",
	"http.status_code", "deployment.environment",
}

// builtinFacetKeys are the span attributes the built-in service facets
// show and break down by (http.method, net.peer.name, email.subject...),
// taken from the facets themselves so the two cannot drift apart.
var builtinFacetKeys = func() map[string]bool {
	keys := map[string]bool{}
	for _, f := range servicetypes.Builtin() {
		for _, k := range f.KeyAttributes {
			keys[k] = true
		}
		for _, w := range f.Widgets {
			if b, ok := w.(servicetypes.BreakdownWidget); ok && b.Attribute != "" {
				keys[b.Attribute] = true
			}
		}
	}
	return keys
}()

func isReservedAttr(key string) bool {
	if builtinFacetKeys[key] {
		return true
	}
	for _, p := range reservedAttrPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// reservedAttr is isReservedAttr plus what this org's configuration
// reads in code.
func (in TelemetryInput) reservedAttr(key string) bool {
	if isReservedAttr(key) {
		return true
	}
	for _, p := range in.Reserved.AttrPrefixes {
		if p != "" && strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// reservedAttrOn is reservedAttr plus the attributes that split this
// service into flows.
func (in TelemetryInput) reservedAttrOn(service, key string) bool {
	if in.reservedAttr(key) {
		return true
	}
	for _, k := range in.Reserved.FlowAttrs[service] {
		if k == key {
			return true
		}
	}
	return false
}

func humanCount(n uint64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
