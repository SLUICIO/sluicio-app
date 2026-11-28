// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package advisor

import "testing"

// Keys Sluicio reads in code are never suggested for deletion: the
// built-in facets' attributes, the older 5xx key, the environment, and
// what the org's system types detect by.
func TestReservedAttr(t *testing.T) {
	in := TelemetryInput{Reserved: Reserved{AttrPrefixes: []string{"node_red.", "env.tier"}}}
	for _, k := range []string{"net.peer.name", "email.subject", "http.status_code", "deployment.environment.name", "node_red.flow", "env.tier"} {
		if !in.reservedAttr(k) {
			t.Errorf("%s not reserved", k)
		}
	}
	for _, k := range []string{"order.id", "customer.tier", "node_redx"} {
		if in.reservedAttr(k) {
			t.Errorf("%s reserved", k)
		}
	}
}

// When every metric a system type is detected by would go, one stays.
func TestKeepDetectable(t *testing.T) {
	metrics := []MetricSupply{
		{Name: "rabbitmq_queue_messages", Services: []string{"a", "b"}, EstBytesPerDay: 900},
		{Name: "rabbitmq_channels", Services: []string{"a", "b"}, EstBytesPerDay: 100},
		{Name: "rabbitmq_connections", Services: []string{"a"}, EstBytesPerDay: 10},
		{Name: "kafka.lag", Services: []string{"k"}},
		{Name: "kafka.offset", Services: []string{"k"}},
		{Name: "orders.created"},
	}
	sug := func(names ...string) []Suggestion {
		out := []Suggestion{}
		for _, n := range names {
			out = append(out, Suggestion{ScopeID: n})
		}
		return out
	}
	ids := func(s []Suggestion) map[string]bool {
		m := map[string]bool{}
		for _, x := range s {
			m[x.ScopeID] = true
		}
		return m
	}
	prefixes := []string{"rabbitmq", "kafka."}

	// Every rabbitmq metric suggested: the one most services emit, then
	// the cheapest, stays. Kafka keeps one too; orders is unaffected.
	got := ids(keepDetectable(sug("rabbitmq_queue_messages", "rabbitmq_channels", "rabbitmq_connections", "kafka.lag", "kafka.offset", "orders.created"), metrics, prefixes))
	if got["rabbitmq_channels"] || !got["rabbitmq_queue_messages"] || !got["rabbitmq_connections"] || !got["orders.created"] {
		t.Fatalf("rabbitmq: %v", got)
	}
	if got["kafka.lag"] && got["kafka.offset"] {
		t.Fatalf("kafka lost every metric: %v", got)
	}
	// One kafka metric is still read: nothing to keep.
	got = ids(keepDetectable(sug("kafka.lag"), metrics, prefixes))
	if !got["kafka.lag"] {
		t.Fatalf("kept a metric although another one detects the type: %v", got)
	}
}

// An attribute that splits a service into flows is reserved on that
// service only.
func TestReservedFlowAttr(t *testing.T) {
	in := TelemetryInput{Reserved: Reserved{FlowAttrs: map[string][]string{"broker": {"flow.name"}}}}
	if !in.reservedAttrOn("broker", "flow.name") {
		t.Error("flow.name on broker not reserved")
	}
	if in.reservedAttrOn("other", "flow.name") {
		t.Error("flow.name reserved on a service it does not split")
	}
	if !in.reservedAttrOn("other", "http.status_code") {
		t.Error("the global reserved keys still apply")
	}
}
