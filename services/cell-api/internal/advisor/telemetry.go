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
// live in attributes, logs, metrics and noise — never in spans.
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

	return out, nil
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
	return out
}

// --- T2: unviewed log stream -------------------------------------------

// bandFloor maps a severity band to the floor an operator would set to
// drop it: dropping DEBUG means flooring at INFO, and so on.
func bandFloor(band int32) (name, floor string) {
	switch {
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
		if isReservedAttr(a.Key) {
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
		if isReservedAttr(a.Key) || a.SpansWithKey < 10 {
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
			Loss: "Hashing keeps correlation working (the same value still matches itself) while the " +
				"original stops being retained. If the readable value is needed for support, this is a " +
				"retention decision rather than a collector one.",
			// Compliance findings rank above cost ones: a big number
			// should not push a personal-data finding down the page.
			Weight:   int64(a.EstBytesPerDay) + 1<<40,
			Evidence: piiEvidence(kind, hits, len(values), a.SpansWithKey),
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
func piiEvidence(kind string, hits, sampled int, spans uint64) map[string]any {
	return map[string]any{
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
var reservedAttrPrefixes = []string{
	"io.", "service.", "integration.", "messaging.", "db.", "rpc.system",
	"http.request.method", "http.response.status_code", "http.route",
	"error.", "exception.", "peer.service", "file.", "transfer.",
}

func isReservedAttr(key string) bool {
	for _, p := range reservedAttrPrefixes {
		if strings.HasPrefix(key, p) {
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
