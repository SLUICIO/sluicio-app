// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The guardrails, not the findings.
//
// Getting a suggestion wrong in the permissive direction is the failure
// that ends the feature: an operator who drops something load-bearing on
// our advice stops trusting every later suggestion, and is right to. So
// what is pinned here is mostly the REFUSALS — the cases where the
// evaluator has a plausible-looking candidate and declines it anyway.
//
// The one thing these tests cannot check is whether a threshold is well
// chosen. They check that the guardrails are wired in at all, which is
// the part that silently rots when someone refactors the evaluator.

package advisor

import (
	"strings"
	"testing"
	"time"
)

func testWindow() (from, to time.Time) {
	to = time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	return to.Add(-ObservationWindow), to
}

// demandWith builds a ledger containing exactly the given entries.
func demandWith(human, mechanical map[string]time.Time) *DemandSet {
	d := &DemandSet{human: map[string]time.Time{}, mechanical: map[string]time.Time{}}
	for k, v := range human {
		d.human[k] = v
	}
	for k, v := range mechanical {
		d.mechanical[k] = v
	}
	return d
}

func TestUnusedMetricNeedsAllGuardrailsToPass(t *testing.T) {
	from, to := testWindow()
	old := from.Add(-90 * 24 * time.Hour) // safely outside quarantine
	busy := MetricSupply{
		Name: "queue.depth", Rows: 100_000, Series: 4,
		Services: []string{"orders-api"}, FirstSeen: old,
		EstBytesPerDay: 500_000,
	}
	base := TelemetryInput{From: from, To: to, Demand: demandWith(nil, nil)}

	if got := evalUnusedMetrics(base, []MetricSupply{busy}); len(got) != 1 {
		t.Fatalf("an old, high-volume, unconsumed metric should be suggested; got %d findings", len(got))
	}

	for _, tc := range []struct {
		name   string
		in     TelemetryInput
		metric MetricSupply
	}{
		{
			// One person charting it once is enough. We do not weigh
			// "only viewed twice" against bytes — a rarely consulted
			// signal is exactly the kind you miss when it is gone.
			name: "a single human view vetoes it",
			in: TelemetryInput{From: from, To: to, Demand: demandWith(
				map[string]time.Time{"metric||queue.depth": from.Add(24 * time.Hour)}, nil)},
			metric: busy,
		},
		{
			// Config is demand for as long as it exists, window or not.
			// A metric behind a paused seasonal rule is still in use.
			name: "an old mechanical reference vetoes it regardless of the window",
			in: TelemetryInput{From: from, To: to, Demand: demandWith(nil,
				map[string]time.Time{"metric||queue.depth": old})},
			metric: busy,
		},
		{
			name: "demand recorded against one of its services vetoes it",
			in: TelemetryInput{From: from, To: to, Demand: demandWith(
				map[string]time.Time{"metric|orders-api|queue.depth": from.Add(time.Hour)}, nil)},
			metric: busy,
		},
		{
			// Quarantine: it has not had a fair chance to be consumed.
			name:   "telemetry younger than the window is never judged",
			in:     base,
			metric: MetricSupply{Name: "queue.depth", Rows: 100_000, FirstSeen: to.Add(-24 * time.Hour), EstBytesPerDay: 500_000},
		},
		{
			name:   "a metric too small to matter is not worth an operator's attention",
			in:     base,
			metric: MetricSupply{Name: "queue.depth", Rows: 5, FirstSeen: old, EstBytesPerDay: 10},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalUnusedMetrics(tc.in, []MetricSupply{tc.metric}); len(got) != 0 {
				t.Errorf("expected no suggestion, got %d: %s", len(got), got[0].Title)
			}
		})
	}
}

func TestWarningsAndErrorsAreNeverProposedForDropping(t *testing.T) {
	// The records someone reaches for during an incident are exactly the
	// ones nobody "consumes" beforehand. Judging them by demand would
	// delete the logs you need on your worst day.
	from, to := testWindow()
	old := from.Add(-90 * 24 * time.Hour)
	in := TelemetryInput{From: from, To: to, Demand: demandWith(nil, nil)}

	for _, band := range []int32{13, 17, 21} { // warn, error, fatal
		got := evalUnviewedLogs(in, []LogStreamSupply{{
			Service: "orders-api", SeverityNumber: band, Rows: 5_000_000,
			FirstSeen: old, EstBytesPerDay: 900_000_000,
		}})
		if len(got) != 0 {
			t.Errorf("severity band %d was proposed for dropping: %s", band, got[0].Title)
		}
	}
	// …while debug on the same service is fair game.
	got := evalUnviewedLogs(in, []LogStreamSupply{{
		Service: "orders-api", SeverityNumber: 5, Rows: 5_000_000,
		FirstSeen: old, EstBytesPerDay: 900_000_000,
	}})
	if len(got) != 1 {
		t.Fatalf("an unread DEBUG stream should be suggested; got %d", len(got))
	}
	if got[0].Loss == "" {
		t.Error("every suggestion must state what is lost, not only what is saved")
	}
}

func TestReservedAttributesAreNeverSuggested(t *testing.T) {
	// These are read by CODE, not by queries, so the demand ledger is
	// silent about them — and deleting one breaks the product rather
	// than saving money.
	for _, key := range []string{
		"io.kind", "io.role", "service.name", "integration.id",
		"messaging.destination.name", "error.type", "http.route",
	} {
		if !isReservedAttr(key) {
			t.Errorf("%q is load-bearing for facets/classification but is not reserved", key)
		}
	}
	// A genuinely incidental key is not reserved.
	for _, key := range []string{"debug.session", "internal.build_tag"} {
		if isReservedAttr(key) {
			t.Errorf("%q should not be reserved — over-reserving makes the advisor useless", key)
		}
	}
}

func TestSpanAttributeSuggestionsRespectPresenceAndDemand(t *testing.T) {
	from, to := testWindow()
	old := from.Add(-90 * 24 * time.Hour)
	heavy := SpanAttrSupply{
		Service: "orders-api", Key: "debug.session",
		SpansWithKey: 900, SpansTotal: 1000, DistinctValues: 12,
		AvgValueBytes: 40, FirstSeen: old, EstBytesPerDay: 5_000_000,
	}
	in := TelemetryInput{From: from, To: to, Demand: demandWith(nil, nil)}

	got := evalSpanAttributes(in, []SpanAttrSupply{heavy})
	if len(got) != 1 || got[0].Class != "T3" {
		t.Fatalf("a structural, sizeable, unread attribute should be a T3; got %+v", got)
	}
	if got[0].Snippet == "" {
		t.Error("a telemetry suggestion without a snippet is a complaint, not advice")
	}

	// Present on a handful of spans: a conditional debug flag, not
	// structural cost.
	rare := heavy
	rare.SpansWithKey = 20
	if got := evalSpanAttributes(in, []SpanAttrSupply{rare}); len(got) != 0 {
		t.Errorf("an attribute on 2%% of spans is not dead weight: %s", got[0].Title)
	}

	// A facet mapping referencing it is mechanical demand, and vetoes.
	withFacet := TelemetryInput{From: from, To: to, Demand: demandWith(nil,
		map[string]time.Time{"trace|orders-api|debug.session": old})}
	if got := evalSpanAttributes(withFacet, []SpanAttrSupply{heavy}); len(got) != 0 {
		t.Errorf("an attribute referenced by config must never be suggested: %s", got[0].Title)
	}
}

func TestHighCardinalityIsFlaggedForReviewNotDeletion(t *testing.T) {
	// A high-cardinality attribute is often the USEFUL one — an order id.
	// The honest framing is "this is expensive, is it worth it", never
	// "delete this".
	from, to := testWindow()
	got := evalSpanAttributes(
		TelemetryInput{From: from, To: to, Demand: demandWith(nil, nil)},
		[]SpanAttrSupply{{
			Service: "orders-api", Key: "order.correlation",
			SpansWithKey: 990, SpansTotal: 1000, DistinctValues: 50_000,
			AvgValueBytes: 36, FirstSeen: from.Add(-90 * 24 * time.Hour),
			EstBytesPerDay: 8_000_000,
		}})
	if len(got) != 1 || got[0].Class != "T4" {
		t.Fatalf("expected a T4 review finding, got %+v", got)
	}
	if got[0].Evidence["review_required"] != true {
		t.Error("a high-cardinality finding must be marked for review")
	}
}

func TestEchoedHeadersCollapseIntoOneFinding(t *testing.T) {
	// Forty findings that are one decision is a wall, not advice.
	from, to := testWindow()
	old := from.Add(-90 * 24 * time.Hour)
	var attrs []SpanAttrSupply
	for _, h := range []string{"accept", "user-agent", "x-forwarded-for", "cookie"} {
		attrs = append(attrs, SpanAttrSupply{
			Service: "gateway", Key: "http.request.header." + h,
			SpansWithKey: 1000, SpansTotal: 1000, DistinctValues: 5,
			AvgValueBytes: 60, FirstSeen: old, EstBytesPerDay: 1_000_000,
		})
	}
	got := evalSpanAttributes(TelemetryInput{From: from, To: to, Demand: demandWith(nil, nil)}, attrs)
	if len(got) != 1 {
		t.Fatalf("four header keys should collapse to one finding, got %d", len(got))
	}
	if got[0].Class != "T5" {
		t.Fatalf("expected T5, got %s", got[0].Class)
	}
	if n, _ := got[0].Evidence["keys"].(int); n != 4 {
		t.Errorf("the finding should count all 4 keys, said %v", got[0].Evidence["keys"])
	}
	if got[0].Weight != 4_000_000 {
		t.Errorf("weight should sum the family's cost, got %d", got[0].Weight)
	}
}

func TestPIIClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   string
	}{
		{"emails", []string{"a@b.se", "c@d.com", "e@f.io"}, "email address"},
		{"personnummer", []string{"19850101-1234", "20001231-9999"}, "national ID number"},
		{"IBANs", []string{"SE3550000000054910000003", "DE89370400440532013000"}, "IBAN"},
		{"ordinary ids are not personal data", []string{"ord_12345", "ord_99", "abc"}, ""},
		{"free text is not personal data", []string{"queue drained", "retry scheduled"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := classifyPII(tc.values)
			if got != tc.want {
				t.Errorf("classifyPII(%v) = %q, want %q", tc.values, got, tc.want)
			}
		})
	}
}

// The compliance finding must describe personal data without carrying
// any. Copying a matched value into the suggestions table would create a
// second copy of the problem being reported, in a table with none of the
// retention controls of the first.
func TestPIIEvidenceCarriesNoSampledValue(t *testing.T) {
	ev := piiEvidence("email address", 40, 50, 12_000)
	for k, v := range ev {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if strings.ContainsAny(s, "@") {
			t.Errorf("evidence %q leaks something value-shaped: %q", k, s)
		}
	}
	if ev["sampled_values_matching"] != "40 of 50" {
		t.Errorf("proportion = %v, want \"40 of 50\"", ev["sampled_values_matching"])
	}
	if ev["compliance"] != true {
		t.Error("a PII finding must be flagged as compliance so the UI ranks it above cost findings")
	}
}
