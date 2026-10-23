// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The demo estate is what visitors of the public demo read, so its shape
// is asserted rather than described: exactly two flows, the services in
// each, who calls whom, one environment per trace, and that the failures
// it promises actually happen.

package main

import (
	mrand "math/rand"
	"testing"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// edges returns the service-to-service hops of one trace: a span whose
// parent is in another service.
func edges(trace []serviceSpans) (map[string]bool, map[string]bool, map[string]bool) {
	owner := map[string]string{}
	envs := map[string]bool{}
	services := map[string]bool{}
	for _, ss := range trace {
		services[ss.serviceName] = true
		envs[ss.environment] = true
		for _, sp := range ss.spans {
			owner[string(sp.SpanId)] = ss.serviceName
		}
	}
	hops := map[string]bool{}
	for _, ss := range trace {
		for _, sp := range ss.spans {
			if p, ok := owner[string(sp.ParentSpanId)]; ok && p != ss.serviceName {
				hops[p+">"+ss.serviceName] = true
			}
		}
	}
	return services, hops, envs
}

func TestDemoEstateShape(t *testing.T) {
	if len(demoEstateScenarios) != 2 {
		t.Fatalf("the demo is two integrations, got %d flows", len(demoEstateScenarios))
	}
	rng := mrand.New(mrand.NewSource(1))
	envSeen := map[string]bool{}
	orderFailed, malformed, erpRejected := 0, 0, 0
	for i := 0; i < 2000; i++ {
		// Order sync: one to one.
		order := orderSyncScenario(rng)
		svcs, hops, envs := edges(order)
		if len(svcs) != 2 || !hops["webshop-orders>erp-order-sync"] || len(hops) != 1 {
			t.Fatalf("order sync: services %v hops %v", svcs, hops)
		}
		if len(envs) != 1 {
			t.Fatalf("a trace runs in one environment: %v", envs)
		}
		for e := range envs {
			envSeen[e] = true
		}
		if failed(order) {
			orderFailed++
		}

		// Invoice distribution: A to B to C and D.
		inv := invoiceDistributionScenario(rng)
		svcs, hops, envs = edges(inv)
		if len(envs) != 1 {
			t.Fatalf("a trace runs in one environment: %v", envs)
		}
		switch len(svcs) {
		case 2: // malformed: stops at the router
			if !hops["invoice-intake>invoice-router"] || !failed(inv) {
				t.Fatalf("malformed invoice: %v %v", svcs, hops)
			}
			malformed++
		case 4:
			for _, h := range []string{"invoice-intake>invoice-router", "invoice-router>erp-invoice-adapter", "invoice-router>invoice-archiver"} {
				if !hops[h] {
					t.Fatalf("missing hop %s in %v", h, hops)
				}
			}
			if len(hops) != 3 {
				t.Fatalf("hops: %v", hops)
			}
			if failed(inv) {
				erpRejected++
				for _, ss := range inv {
					if ss.serviceName == "invoice-archiver" && failed([]serviceSpans{ss}) {
						t.Fatal("an ERP rejection must not fail the archive branch")
					}
				}
			}
		default:
			t.Fatalf("invoice distribution services: %v", svcs)
		}
	}
	if !envSeen["production"] || !envSeen["test"] {
		t.Errorf("environments: %v", envSeen)
	}
	if orderFailed == 0 || malformed == 0 || erpRejected == 0 {
		t.Errorf("seeded failures missing: order %d, malformed %d, rejected %d", orderFailed, malformed, erpRejected)
	}
}

// failed reports whether a trace failed, and insists the failure says
// why: some failed span carries the exception event.
func failed(trace []serviceSpans) bool {
	errored, explained := false, false
	for _, ss := range trace {
		for _, sp := range ss.spans {
			if sp.Status.GetCode() == tracepb.Status_STATUS_CODE_ERROR {
				errored = true
				for _, e := range sp.Events {
					explained = explained || e.Name == "exception"
				}
			}
		}
	}
	if errored && !explained {
		panic("a seeded failure without an exception event")
	}
	return errored
}

func TestDemoEstateRequest(t *testing.T) {
	saved := []any{demoServices, demoScenarios, standaloneSpans}
	t.Cleanup(func() {
		demoServices = saved[0].([]serviceSpec)
		demoScenarios = saved[1].([]scenarioFactory)
		standaloneSpans = saved[2].(bool)
	})
	useDemoEstate()
	req := buildRequest(mrand.New(mrand.NewSource(2)), 50)
	names := map[string]bool{}
	for _, rs := range req.ResourceSpans {
		for _, a := range rs.Resource.Attributes {
			if a.Key == "service.name" {
				names[a.Value.GetStringValue()] = true
			}
		}
	}
	for n := range names {
		found := false
		for _, s := range demoEstateServices {
			found = found || s.name == n
		}
		if !found {
			t.Errorf("service %s is not in the demo estate", n)
		}
	}
	if len(names) != 6 {
		t.Errorf("want the six demo services, got %v", names)
	}
}
