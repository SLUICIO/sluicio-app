// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package servicetypes

// ServiceFacet is one input or output role a service plays. A service
// is described by every facet that fires for it — many facets can
// stack on a single service, and the dashboard concatenates each
// facet's widgets in declaration order.
//
// In Phase 1 only built-in facets exist and they are defined in Go.
// User-defined facets (Phase 2) will store the same shape in Postgres.
type ServiceFacet struct {
	Slug        string
	Name        string
	Description string
	// MatchFn returns true if this facet should be applied to a service
	// with the given profile.
	MatchFn func(ServiceProfile) bool
	Widgets []Widget
	// KeyAttributes lists the span attribute keys the UI should
	// highlight on every span belonging to a service that has this
	// facet. For File Input this is file.name + transfer.source.host so
	// a user scanning a list of pickups can see *what* came in.
	KeyAttributes []string
	// Match documents, for the UI, what a service must emit for this
	// facet to be applied — the OTel span attributes / span kinds Sluicio
	// looks for to classify the service. Mirrors what MatchFn checks.
	Match MatchSpec
	// Custom is true for org-defined facets (from the servicefacets store),
	// false for built-in code-defined facets. Custom facets never auto-match
	// (MatchFn is nil/false) — they're applied to services via overrides.
	Custom bool
}

// AttrRequirement is one (key=value) span-attribute pair Sluicio must
// see to apply a facet.
type AttrRequirement struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// MatchSpec describes, for the UI, how Sluicio detects a facet — i.e.
// what instrumentation a producer must emit. The span attributes (all
// of them, on the same span) and/or span kinds that drive the match.
type MatchSpec struct {
	// SpanAttributes are the (key=value) pairs that must all be present
	// on the same span for the facet to fire.
	SpanAttributes []AttrRequirement `json:"span_attributes,omitempty"`
	// SpanKinds, when set, are the OTel span kinds that drive the match.
	SpanKinds []string `json:"span_kinds,omitempty"`
	// Always is true for the baseline facet applied to every service.
	Always bool `json:"always,omitempty"`
	// Note is an optional human clarification (e.g. worker's "and no I/O").
	Note string `json:"note,omitempty"`
}

// JSONShape exposes the facet for the UI without leaking the MatchFn.
type JSONShape struct {
	Slug          string        `json:"slug"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	Widgets       []WidgetShape `json:"widgets"`
	KeyAttributes []string      `json:"key_attributes"`
	Match         MatchSpec     `json:"match"`
	// Custom is true for org-defined facets (servicefacets store), false for
	// the built-in code-defined ones. The UI gates edit/delete on it.
	Custom bool `json:"custom,omitempty"`
}

type WidgetShape struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ioMatch builds the MatchSpec for an I/O facet: the io.kind + io.role
// span attributes a producer emits on every boundary-crossing span.
func ioMatch(kind, role string) MatchSpec {
	return MatchSpec{SpanAttributes: []AttrRequirement{
		{Key: "io.kind", Value: kind},
		{Key: "io.role", Value: role},
	}}
}

func (f ServiceFacet) ToJSON() JSONShape {
	keys := f.KeyAttributes
	if keys == nil {
		keys = []string{}
	}
	out := JSONShape{
		Slug:          f.Slug,
		Name:          f.Name,
		Description:   f.Description,
		Widgets:       make([]WidgetShape, 0, len(f.Widgets)),
		KeyAttributes: keys,
		Match:         f.Match,
		Custom:        f.Custom,
	}
	for _, w := range f.Widgets {
		out.Widgets = append(out.Widgets, WidgetShape{
			Kind:        w.Kind(),
			Name:        w.Name(),
			Description: w.Description(),
		})
	}
	return out
}

// Builtin returns the ordered list of built-in facets. Order matters
// for presentation only — every matching facet is returned by the
// registry, not just the first.
//
// I/O facets come first (more specific dashboards), then worker,
// then core. core always matches, so the returned dashboard is never
// empty.
func Builtin() []ServiceFacet {
	return []ServiceFacet{
		fileInput(),
		fileOutput(),
		queueInput(),
		queueOutput(),
		streamInput(),
		streamOutput(),
		httpInput(),
		httpOutput(),
		dbOutput(),
		emailOutput(),
		worker(),
		core(),
	}
}

// --- File Input ---

func fileInput() ServiceFacet {
	filter := SpanFilter{
		AttrEquals: []SpanAttrEqual{
			{Key: "io.kind", Value: "file"},
			{Key: "io.role", Value: "input"},
		},
	}
	return ServiceFacet{
		Slug: "file-input",
		Name: "File input",
		Description: "Service receives files — pulling from an FTP / SFTP server, " +
			"reading from a network share, or watching a local inbox.",
		KeyAttributes: []string{
			"file.name",
			"transfer.source.host",
			"transfer.source.path",
			"transfer.protocol",
			"io.system",
		},
		Match:   ioMatch("file", "input"),
		MatchFn: func(p ServiceProfile) bool { return p.HasIO("file", "input") },
		Widgets: []Widget{
			CounterWidget{WName: "Files received", Subtitle: "in window", Filter: filter, Aggregation: CounterSpans},
			CounterWidget{WName: "Pickup errors", Filter: filter, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Pickup rate", WDescription: "Files received per minute.", Filter: filter},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of pickups that failed.", Filter: filter},
			LatencyWidget{WName: "Pickup duration", WDescription: "Time to fetch a file. Inflated by large transfers.", Filter: filter},
			BreakdownWidget{WName: "By source host", WDescription: "Where files came from.", Filter: filter, Source: AttrSourceSpan, Attribute: "transfer.source.host"},
			BreakdownWidget{WName: "By protocol", WDescription: "FTP / SFTP / SMB / local.", Filter: filter, Source: AttrSourceSpan, Attribute: "io.system"},
		},
	}
}

// --- File Output ---

func fileOutput() ServiceFacet {
	filter := SpanFilter{
		AttrEquals: []SpanAttrEqual{
			{Key: "io.kind", Value: "file"},
			{Key: "io.role", Value: "output"},
		},
	}
	return ServiceFacet{
		Slug: "file-output",
		Name: "File output",
		Description: "Service sends files — pushing to an FTP / SFTP partner, " +
			"writing to a network share, or dropping into a local folder.",
		KeyAttributes: []string{
			"file.name",
			"transfer.destination.host",
			"transfer.destination.path",
			"transfer.protocol",
			"io.system",
		},
		Match:   ioMatch("file", "output"),
		MatchFn: func(p ServiceProfile) bool { return p.HasIO("file", "output") },
		Widgets: []Widget{
			CounterWidget{WName: "Files sent", Subtitle: "in window", Filter: filter, Aggregation: CounterSpans},
			CounterWidget{WName: "Send errors", Filter: filter, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Send rate", WDescription: "Files sent per minute.", Filter: filter},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of sends that failed.", Filter: filter},
			LatencyWidget{WName: "Send duration", WDescription: "Time to deliver a file. Inflated by large transfers.", Filter: filter},
			BreakdownWidget{WName: "By destination host", WDescription: "Where files were delivered.", Filter: filter, Source: AttrSourceSpan, Attribute: "transfer.destination.host"},
			BreakdownWidget{WName: "By protocol", WDescription: "FTP / SFTP / SMB / local.", Filter: filter, Source: AttrSourceSpan, Attribute: "io.system"},
		},
	}
}

// --- Queue Input ---

func queueInput() ServiceFacet {
	filter := SpanFilter{
		AttrEquals: []SpanAttrEqual{
			{Key: "io.kind", Value: "queue"},
			{Key: "io.role", Value: "input"},
		},
	}
	return ServiceFacet{
		Slug:        "queue-input",
		Name:        "Queue input",
		Description: "Service consumes messages from a queue or topic.",
		KeyAttributes: []string{
			"messaging.destination.name",
			"messaging.system",
			"messaging.operation",
		},
		Match:   ioMatch("queue", "input"),
		MatchFn: func(p ServiceProfile) bool { return p.HasIO("queue", "input") },
		Widgets: []Widget{
			CounterWidget{WName: "Messages processed", Subtitle: "in window", Filter: filter, Aggregation: CounterSpans},
			CounterWidget{WName: "Processing errors", Filter: filter, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Throughput", WDescription: "Messages processed per minute.", Filter: filter},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of consume operations that failed.", Filter: filter},
			LatencyWidget{WName: "Processing latency", WDescription: "Time to handle a single message.", Filter: filter},
			BreakdownWidget{WName: "By queue / topic", WDescription: "Throughput per messaging destination.", Filter: filter, Source: AttrSourceSpan, Attribute: "messaging.destination.name"},
			BreakdownWidget{WName: "By messaging system", WDescription: "Activity grouped by Kafka / Service Bus / etc.", Filter: filter, Source: AttrSourceSpan, Attribute: "messaging.system"},
		},
	}
}

// --- Queue Output ---

func queueOutput() ServiceFacet {
	filter := SpanFilter{
		AttrEquals: []SpanAttrEqual{
			{Key: "io.kind", Value: "queue"},
			{Key: "io.role", Value: "output"},
		},
	}
	return ServiceFacet{
		Slug:        "queue-output",
		Name:        "Queue output",
		Description: "Service publishes messages to a queue or topic.",
		KeyAttributes: []string{
			"messaging.destination.name",
			"messaging.system",
		},
		Match:   ioMatch("queue", "output"),
		MatchFn: func(p ServiceProfile) bool { return p.HasIO("queue", "output") },
		Widgets: []Widget{
			CounterWidget{WName: "Messages published", Filter: filter, Aggregation: CounterSpans},
			CounterWidget{WName: "Publish errors", Filter: filter, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Publish rate", WDescription: "Messages produced per minute.", Filter: filter},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of publish operations that failed.", Filter: filter},
			BreakdownWidget{WName: "By destination", WDescription: "Publish volume per destination.", Filter: filter, Source: AttrSourceSpan, Attribute: "messaging.destination.name"},
		},
	}
}

// --- Stream Input ---
//
// Streams are the event-log cousins of queues: Kafka, Event Hubs,
// Kinesis, Pulsar. Same messaging semconv, different operational
// story — topics with partitions, offsets, and consumer groups rather
// than discrete work items, so the facet highlights those.

func streamInput() ServiceFacet {
	filter := SpanFilter{
		AttrEquals: []SpanAttrEqual{
			{Key: "io.kind", Value: "stream"},
			{Key: "io.role", Value: "input"},
		},
	}
	return ServiceFacet{
		Slug: "stream-input",
		Name: "Stream input",
		Description: "Service consumes events from a data stream — a Kafka or " +
			"Event Hubs topic, a Kinesis or Pulsar stream.",
		KeyAttributes: []string{
			"messaging.destination.name",
			"messaging.system",
			"messaging.consumer.group.name",
			"messaging.destination.partition.id",
			"messaging.kafka.message.offset",
		},
		Match:   ioMatch("stream", "input"),
		MatchFn: func(p ServiceProfile) bool { return p.HasIO("stream", "input") },
		Widgets: []Widget{
			CounterWidget{WName: "Events consumed", Subtitle: "in window", Filter: filter, Aggregation: CounterSpans},
			CounterWidget{WName: "Consume errors", Filter: filter, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Consume rate", WDescription: "Events consumed per minute.", Filter: filter},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of consume operations that failed.", Filter: filter},
			LatencyWidget{WName: "Processing latency", WDescription: "Time to handle a single event.", Filter: filter},
			BreakdownWidget{WName: "By topic / stream", WDescription: "Throughput per stream destination.", Filter: filter, Source: AttrSourceSpan, Attribute: "messaging.destination.name"},
			BreakdownWidget{WName: "By partition", WDescription: "Consumption spread across partitions — skew shows up here.", Filter: filter, Source: AttrSourceSpan, Attribute: "messaging.destination.partition.id"},
			BreakdownWidget{WName: "By streaming system", WDescription: "Kafka / Event Hubs / Kinesis / etc.", Filter: filter, Source: AttrSourceSpan, Attribute: "messaging.system"},
		},
	}
}

// --- Stream Output ---

func streamOutput() ServiceFacet {
	filter := SpanFilter{
		AttrEquals: []SpanAttrEqual{
			{Key: "io.kind", Value: "stream"},
			{Key: "io.role", Value: "output"},
		},
	}
	return ServiceFacet{
		Slug: "stream-output",
		Name: "Stream output",
		Description: "Service produces events to a data stream — a Kafka or " +
			"Event Hubs topic, a Kinesis or Pulsar stream.",
		KeyAttributes: []string{
			"messaging.destination.name",
			"messaging.system",
			"messaging.destination.partition.id",
		},
		Match:   ioMatch("stream", "output"),
		MatchFn: func(p ServiceProfile) bool { return p.HasIO("stream", "output") },
		Widgets: []Widget{
			CounterWidget{WName: "Events produced", Filter: filter, Aggregation: CounterSpans},
			CounterWidget{WName: "Produce errors", Filter: filter, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Produce rate", WDescription: "Events produced per minute.", Filter: filter},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of produce operations that failed.", Filter: filter},
			BreakdownWidget{WName: "By topic / stream", WDescription: "Produce volume per stream destination.", Filter: filter, Source: AttrSourceSpan, Attribute: "messaging.destination.name"},
			BreakdownWidget{WName: "By partition", WDescription: "Produce spread across partitions.", Filter: filter, Source: AttrSourceSpan, Attribute: "messaging.destination.partition.id"},
		},
	}
}

// --- HTTP Input ---

func httpInput() ServiceFacet {
	filter := SpanFilter{
		AttrEquals: []SpanAttrEqual{
			{Key: "io.kind", Value: "http"},
			{Key: "io.role", Value: "input"},
		},
	}
	return ServiceFacet{
		Slug:        "http-input",
		Name:        "HTTP input",
		Description: "Service handles inbound HTTP requests.",
		KeyAttributes: []string{
			"http.route",
			"http.method",
			"http.request.method",
			"http.status_code",
			"http.response.status_code",
		},
		Match:   ioMatch("http", "input"),
		MatchFn: func(p ServiceProfile) bool { return p.HasIO("http", "input") },
		Widgets: []Widget{
			CounterWidget{WName: "Requests handled", Filter: filter, Aggregation: CounterSpans},
			CounterWidget{WName: "Failed requests", Filter: filter, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Request throughput", WDescription: "Incoming requests per minute.", Filter: filter},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of requests that returned an error status.", Filter: filter},
			LatencyWidget{WName: "Response time", WDescription: "p50 / p95 / p99 response latency.", Filter: filter},
			BreakdownWidget{WName: "By route", WDescription: "Top routes by request volume.", Filter: filter, Source: AttrSourceSpan, Attribute: "http.route"},
			BreakdownWidget{WName: "By status code", WDescription: "HTTP response status distribution.", Filter: filter, Source: AttrSourceSpan, Attribute: "http.status_code"},
		},
	}
}

// --- HTTP Output ---

func httpOutput() ServiceFacet {
	filter := SpanFilter{
		AttrEquals: []SpanAttrEqual{
			{Key: "io.kind", Value: "http"},
			{Key: "io.role", Value: "output"},
		},
	}
	return ServiceFacet{
		Slug:        "http-output",
		Name:        "HTTP output",
		Description: "Service makes outbound HTTP calls to other systems.",
		KeyAttributes: []string{
			"http.url",
			"http.method",
			"http.status_code",
			"net.peer.name",
			"peer.service",
		},
		Match:   ioMatch("http", "output"),
		MatchFn: func(p ServiceProfile) bool { return p.HasIO("http", "output") },
		Widgets: []Widget{
			CounterWidget{WName: "Requests sent", Filter: filter, Aggregation: CounterSpans},
			CounterWidget{WName: "Failed requests", Filter: filter, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Call rate", WDescription: "Outbound requests per minute.", Filter: filter},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of outbound calls that returned an error status.", Filter: filter},
			LatencyWidget{WName: "Call latency", WDescription: "p50 / p95 / p99 round-trip latency.", Filter: filter},
			BreakdownWidget{WName: "By peer", WDescription: "Top downstream peers by call volume.", Filter: filter, Source: AttrSourceSpan, Attribute: "net.peer.name"},
		},
	}
}

// --- DB Output ---

func dbOutput() ServiceFacet {
	filter := SpanFilter{
		AttrEquals: []SpanAttrEqual{
			{Key: "io.kind", Value: "db"},
			{Key: "io.role", Value: "output"},
		},
	}
	return ServiceFacet{
		Slug:        "db-output",
		Name:        "Database output",
		Description: "Service writes to or reads from a database. Detected by io.kind=db spans.",
		KeyAttributes: []string{
			"db.system",
			"db.name",
			"db.operation",
			"db.sql.table",
			"net.peer.name",
		},
		Match:   ioMatch("db", "output"),
		MatchFn: func(p ServiceProfile) bool { return p.HasIO("db", "output") },
		Widgets: []Widget{
			CounterWidget{WName: "DB operations", Filter: filter, Aggregation: CounterSpans},
			CounterWidget{WName: "Failed operations", Filter: filter, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Operation rate", WDescription: "Database operations per minute.", Filter: filter},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of DB operations that failed.", Filter: filter},
			LatencyWidget{WName: "Operation latency", WDescription: "p50 / p95 / p99 DB operation latency.", Filter: filter},
			BreakdownWidget{WName: "By table", WDescription: "Top tables by activity.", Filter: filter, Source: AttrSourceSpan, Attribute: "db.sql.table"},
			BreakdownWidget{WName: "By system", WDescription: "Snowflake / Postgres / MySQL / etc.", Filter: filter, Source: AttrSourceSpan, Attribute: "db.system"},
		},
	}
}

// --- Email Output ---

func emailOutput() ServiceFacet {
	filter := SpanFilter{
		AttrEquals: []SpanAttrEqual{
			{Key: "io.kind", Value: "email"},
			{Key: "io.role", Value: "output"},
		},
	}
	return ServiceFacet{
		Slug:        "email-output",
		Name:        "Email output",
		Description: "Service sends email via SMTP or an email provider API.",
		KeyAttributes: []string{
			"email.to",
			"email.subject",
			"email.system",
			"net.peer.name",
		},
		Match:   ioMatch("email", "output"),
		MatchFn: func(p ServiceProfile) bool { return p.HasIO("email", "output") },
		Widgets: []Widget{
			CounterWidget{WName: "Emails sent", Filter: filter, Aggregation: CounterSpans},
			CounterWidget{WName: "Send failures", Filter: filter, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Send rate", WDescription: "Emails sent per minute.", Filter: filter},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of email sends that failed.", Filter: filter},
			LatencyWidget{WName: "Send latency", WDescription: "p50 / p95 / p99 send latency.", Filter: filter},
			BreakdownWidget{WName: "By recipient domain", WDescription: "Top recipient addresses.", Filter: filter, Source: AttrSourceSpan, Attribute: "email.to"},
		},
	}
}

// --- Worker ---
//
// Worker matches when the service has Internal-kind spans and no
// observed I/O facets. It's not a "fallback" — it's a real facet for
// pure background workers (audit loggers, schedulers, GC processes).
// A service with both Internal spans AND I/O activity also gets
// queue-input / file-output / etc., not Worker.

func worker() ServiceFacet {
	filter := SpanFilter{
		SpanKinds: []string{"Internal"},
	}
	return ServiceFacet{
		Slug:        "worker",
		Name:        "Worker",
		Description: "Background services with only Internal-kind work and no observed I/O boundaries.",
		Match: MatchSpec{
			SpanKinds: []string{"Internal"},
			Note:      "and no file / queue / stream / http / db / email I/O spans",
		},
		MatchFn: func(p ServiceProfile) bool {
			if !p.HasSpanKind("Internal") {
				return false
			}
			// Suppress when the service already has a real I/O role.
			for _, kind := range []string{"file", "queue", "stream", "http", "db", "email"} {
				if p.HasIO(kind, "input") || p.HasIO(kind, "output") {
					return false
				}
			}
			return true
		},
		Widgets: []Widget{
			CounterWidget{WName: "Operations", Filter: filter, Aggregation: CounterSpans},
			CounterWidget{WName: "Errors", Filter: filter, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Operation rate", WDescription: "Operations per minute.", Filter: filter},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of operations that failed.", Filter: filter},
			LatencyWidget{WName: "Operation duration", WDescription: "p50 / p95 / p99 duration.", Filter: filter},
			BreakdownWidget{WName: "Top operations", WDescription: "Most active span names.", Filter: filter, IntrinsicColumn: "SpanName"},
		},
	}
}

// --- Core ---
//
// Core is always present. It gives every service a baseline of overall
// throughput / error rate / latency widgets, computed across all the
// service's spans regardless of I/O role. The facet sections from
// file-input / queue-output / etc. then zoom in on each boundary.

func core() ServiceFacet {
	return ServiceFacet{
		Slug:        CoreSlug,
		Name:        "Overview",
		Description: "Baseline traffic and error rate across all of the service's spans, regardless of I/O role.",
		Match:       MatchSpec{Always: true},
		MatchFn:     func(_ ServiceProfile) bool { return true },
		Widgets: []Widget{
			CounterWidget{WName: "Spans", Filter: SpanFilter{}, Aggregation: CounterSpans},
			CounterWidget{WName: "Errors", Filter: SpanFilter{}, Aggregation: CounterErrors},
			ThroughputWidget{WName: "Span throughput", WDescription: "All spans per minute.", Filter: SpanFilter{}},
			ErrorRateWidget{WName: "Error rate", WDescription: "Share of spans with error status.", Filter: SpanFilter{}},
			LatencyWidget{WName: "Operation duration", WDescription: "p50 / p95 / p99 across every span.", Filter: SpanFilter{}},
		},
	}
}
