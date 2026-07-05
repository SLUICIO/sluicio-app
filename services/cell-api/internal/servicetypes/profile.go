// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package servicetypes implements service-facet classification and the
// per-facet metric "widgets" that the UI renders on a service's
// dashboard.
//
// A service is described by its I/O profile: which kinds of inputs it
// reads from (file / queue / stream / http) and which kinds of outputs
// it writes to (file / queue / stream / http / db / email). Each facet matches one
// of those I/O roles and contributes its own set of widgets. A single
// service may match multiple facets — a file-input + queue-output
// bridge, a queue-input + db-output sink, etc. — and the dashboard
// stacks the widget sets from every matching facet, in declaration
// order, plus a `core` facet that's always present.
//
// Detection is driven by the `io.role` (input|output) and `io.kind`
// (file|queue|http|db|email) span attributes that producers should
// emit on every span that crosses a system boundary.
package servicetypes

// ServiceProfile summarizes what a service has been emitting recently.
// Used by ServiceFacet.MatchFn to classify the service.
type ServiceProfile struct {
	ServiceName      string
	SpanKinds        map[string]bool
	ResourceAttrKeys map[string]bool
	SpanAttrKeys     map[string]bool
	// IOFacets records the distinct (io.kind, io.role) pairs the
	// service has emitted, keyed as "<kind>:<role>". So a service that
	// reads files and writes to a queue would have:
	//   {"file:input": true, "queue:output": true}
	IOFacets map[string]bool
}

// HasSpanKind reports whether the service has emitted any spans of the
// given kind in the sample.
func (p ServiceProfile) HasSpanKind(kind string) bool {
	return p.SpanKinds[kind]
}

// HasSpanAttribute reports whether the service has emitted any span
// carrying the given attribute key.
func (p ServiceProfile) HasSpanAttribute(key string) bool {
	return p.SpanAttrKeys[key]
}

// HasResourceAttribute reports whether the service's resource has the
// given attribute key.
func (p ServiceProfile) HasResourceAttribute(key string) bool {
	return p.ResourceAttrKeys[key]
}

// HasAttribute checks both span and resource attribute keys.
func (p ServiceProfile) HasAttribute(key string) bool {
	return p.SpanAttrKeys[key] || p.ResourceAttrKeys[key]
}

// HasIO reports whether the service has emitted any span with the
// given io.kind/io.role pair.
func (p ServiceProfile) HasIO(kind, role string) bool {
	return p.IOFacets[kind+":"+role]
}
