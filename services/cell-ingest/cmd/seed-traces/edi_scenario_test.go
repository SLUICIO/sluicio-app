// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The EDI gateway scenario exists to model one specific shape: three
// integrations behind one entry point, where the message type — not the
// root service, not the names, not the dependency graph — is what
// separates them. Its own doc comment makes that claim, and a grouping
// feature will be measured against it.
//
// A seed whose shape has quietly drifted is worse than no seed, because
// the analysis it feeds still produces confident numbers. So the
// properties are asserted rather than described:
//
//   - the three downstream flows stay disjoint (nothing to separate
//     otherwise),
//   - every trace still roots at the shared gateway (the property that
//     makes entry-point grouping fail),
//   - the failing branch actually fails (a dead branch would make the
//     "one flow degrades behind two healthy ones" demo silently untrue).

package main

import (
	mrand "math/rand"
	"testing"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// ediTrace flattens one generated trace into the facts under test.
type ediTrace struct {
	messageType string
	services    map[string]bool
	downstream  []string // everything except the shared gateway
	rootService string
	failed      bool
}

func generateEDITrace(rng *mrand.Rand) ediTrace {
	out := ediTrace{services: map[string]bool{}}
	for _, ss := range ediGatewayScenario(rng) {
		out.services[ss.serviceName] = true
		if ss.serviceName != "b2b-gateway" {
			out.downstream = append(out.downstream, ss.serviceName)
		}
		for _, sp := range ss.spans {
			if len(sp.ParentSpanId) == 0 {
				out.rootService = ss.serviceName
			}
			if sp.Status.GetCode() == tracepb.Status_STATUS_CODE_ERROR {
				out.failed = true
			}
			for _, a := range sp.Attributes {
				if a.Key == "edi.message_type" {
					out.messageType = a.Value.GetStringValue()
				}
			}
		}
	}
	return out
}

func TestEDIScenarioKeepsItsShape(t *testing.T) {
	rng := mrand.New(mrand.NewSource(1))
	const n = 4000

	sinksByType := map[string]map[string]bool{}
	typesByService := map[string]map[string]bool{}
	totalByType := map[string]int{}
	failedByType := map[string]int{}

	for i := 0; i < n; i++ {
		tr := generateEDITrace(rng)

		if tr.rootService != "b2b-gateway" {
			t.Fatalf("trace rooted at %q; every flow must enter through the shared gateway, "+
				"or the scenario stops modelling the case it exists for", tr.rootService)
		}
		if tr.messageType == "" {
			t.Fatal("trace carries no edi.message_type — the only signal that separates the flows")
		}

		totalByType[tr.messageType]++
		if tr.failed {
			failedByType[tr.messageType]++
		}
		if sinksByType[tr.messageType] == nil {
			sinksByType[tr.messageType] = map[string]bool{}
		}
		for _, svc := range tr.downstream {
			sinksByType[tr.messageType][svc] = true
			if typesByService[svc] == nil {
				typesByService[svc] = map[string]bool{}
			}
			typesByService[svc][tr.messageType] = true
		}
	}

	if len(totalByType) < 3 {
		t.Fatalf("only %d message types generated in %d traces; want 3", len(totalByType), n)
	}

	// Disjointness is the whole point: a downstream service shared
	// between two message types would make the service set stop
	// separating them, which is the one thing that still does.
	for svc, types := range typesByService {
		if len(types) > 1 {
			t.Errorf("%s serves %v — the flows must stay disjoint downstream, "+
				"or nothing distinguishes them", svc, keysOf(types))
		}
	}

	for mt, total := range totalByType {
		rate := float64(failedByType[mt]) / float64(total)
		t.Logf("%s: %d traces, %d failed (%.1f%%), downstream %v",
			mt, total, failedByType[mt], 100*rate, keysOf(sinksByType[mt]))
		if failedByType[mt] == 0 {
			t.Errorf("%s never failed across %d traces — a dead failure branch makes the "+
				"'one flow degrades behind healthy ones' case untrue in the demo", mt, total)
		}
	}

	// The invoice branch is meant to be the conspicuously unhealthy one.
	// If it stops being the worst, the estate loses the contrast the
	// scenario was built to show.
	worst, worstRate := "", 0.0
	for mt, total := range totalByType {
		if r := float64(failedByType[mt]) / float64(total); r > worstRate {
			worst, worstRate = mt, r
		}
	}
	if worst != "INVOIC" {
		t.Errorf("the worst-performing flow is %s at %.1f%%, not INVOIC — the scenario's "+
			"point is that ONE flow behind the shared gateway is unhealthy", worst, 100*worstRate)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
