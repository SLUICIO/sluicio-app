// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Integrations from a partitioning attribute (issue #18).
//
// The call-graph candidates (candidates.go) cannot see one service that
// is many integrations: Node-RED, Camel, Boomi and every engine that runs
// many flows in one process report one service.name, so topology sees
// one node and finds nothing. The flows are told apart on the spans.
//
// What makes an attribute a PARTITION of a service, rather than an
// identifier or a dimension, is measured, not guessed:
//
//   - it is on (nearly) every trace of the service: a key on a subset
//     describes the subset;
//   - it takes a handful of values, at least two (one is a constant) and
//     at most MaxPartitionValues (an order id is not a flow);
//   - each trace carries ONE value. This is the test that separates a
//     flow id from a node id or an HTTP method: every run of a flow is
//     one flow, but it calls many nodes and many methods.
//
// That still admits properties of the MESSAGE that are one per trace:
// its priority, its trading partner, its document type. Measured on a
// demo estate they outnumbered the flows, so two more tests apply:
//
//   - a property of the message travels with it, so the same key splits
//     several services the message passes through. A runtime's flow id
//     is specific to that runtime. A key with the partition shape on
//     TravellingServices or more services is left out, unless its name
//     says it is a flow;
//   - a set of values that are all numbers is a quantity (a line count),
//     and true/false is a flag, not a set of flows.
//
// And names count: a key that says flow, route, integration, pipeline
// and the like ranks first. What remains, flow identity or not, is left
// to the reviewer or to an agent reading the values, which is the
// division of labour #10 settled on: deterministic candidates, judgement
// on top. A short list of keys that are dimensions by definition is left
// out up front.
//
// The field name is never pinned: node_red.flow.id is one instance of the
// shape, and the mechanism works on any attribute that has it.

package proposals

import (
	"sort"
	"strings"
	"time"
)

// AttrPartitionStats is what the store measured about one span attribute
// of one service over a window.
type AttrPartitionStats struct {
	Service string
	Key     string
	// TracesWithKey of TracesTotal of the service's traces carry the key.
	TracesWithKey uint64
	TracesTotal   uint64
	// Distinct values of the key.
	DistinctValues uint64
	// TraceValuePairs is the number of distinct (trace, value) pairs; a
	// partition has about one per trace.
	TraceValuePairs uint64
	// NumericValues of DistinctValues parse as numbers; BooleanValues
	// are true or false.
	NumericValues uint64
	BooleanValues uint64
}

// PartitionOptions bounds what is worth proposing.
type PartitionOptions struct {
	// MinPresence is the share of the service's traces that must carry
	// the key.
	MinPresence float64
	// MinValues and MaxValues bound the number of distinct values.
	MinValues, MaxValues uint64
	// MaxValuesPerTrace is how many values a trace may carry on average
	// and still count as being in one partition. A little over one
	// tolerates the odd trace that crossed two flows.
	MaxValuesPerTrace float64
	// MinTraces is the least traffic worth reading a pattern into.
	MinTraces uint64
}

// MaxPartitionValues: a flow catalogue is tens of entries, not thousands.
const MaxPartitionValues = 50

// DefaultPartitionOptions are conservative: a missed suggestion costs
// nothing, a wrong one costs the reviewer's trust in all of them.
func DefaultPartitionOptions() PartitionOptions {
	return PartitionOptions{
		MinPresence:       0.9,
		MinValues:         2,
		MaxValues:         MaxPartitionValues,
		MaxValuesPerTrace: 1.1,
		MinTraces:         20,
	}
}

// dimensionKeys are span attributes that describe HOW a call was made,
// never WHICH flow it belongs to, however neatly they split a service.
var dimensionKeys = map[string]bool{
	"http.method": true, "http.request.method": true, "http.status_code": true,
	"http.response.status_code": true, "http.flavor": true, "http.scheme": true,
	"network.protocol.name": true, "network.protocol.version": true, "network.transport": true,
	"net.transport": true, "rpc.system": true, "db.system": true, "messaging.system": true,
	"messaging.operation": true, "messaging.operation.type": true, "messaging.operation.name": true,
	"otel.status_code": true, "otel.scope.name": true, "error": true, "error.type": true,
	"span.kind": true, "component": true, "thread.name": true, "thread.id": true,
}

// dimensionPrefixes are namespaces of the same kind.
var dimensionPrefixes = []string{"code.", "exception.", "otel.", "thread.", "user_agent.", "server.", "client.", "net.peer.", "net.host."}

// IsDimensionKey reports whether a key is a dimension by definition.
func IsDimensionKey(key string) bool {
	if dimensionKeys[key] {
		return true
	}
	for _, p := range dimensionPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// TravellingServices: a key with the partition shape on this many
// services is a property of the message travelling through them.
const TravellingServices = 3

// flowWords are what a flow identity tends to be called.
var flowWords = []string{"flow", "route", "integration", "pipeline", "workflow", "process", "dag", "job", "interface", "scenario", "message_type", "messagetype"}

// NamesAFlow reports whether a key's name says it identifies a flow.
func NamesAFlow(key string) bool {
	k := strings.ToLower(key)
	for _, w := range flowWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// PartitionCandidate is one attribute that splits one service into flows.
type PartitionCandidate struct {
	Service  string
	Key      string
	Presence float64 // share of the service's traces carrying the key
	Values   uint64
	PerTrace float64 // values per trace, ~1 for a partition
	Traces   uint64  // the service's traces in the window
	// NamedAsFlow: the key's name says flow, route, integration, ...
	NamedAsFlow bool
}

// FindPartitions picks the attributes that partition their service,
// strongest first: a key named like a flow, then the share of traffic
// explained, then fewer values (a coarser split is the more likely flow
// identity). At most perService per service and limit in all, applied
// after ranking so the strongest survive wherever they are.
func FindPartitions(stats []AttrPartitionStats, opt PartitionOptions, perService, limit int) []PartitionCandidate {
	var shaped []PartitionCandidate
	servicesPerKey := map[string]int{}
	for _, s := range stats {
		if s.TracesTotal < opt.MinTraces || s.TracesWithKey == 0 || IsDimensionKey(s.Key) {
			continue
		}
		presence := float64(s.TracesWithKey) / float64(s.TracesTotal)
		perTrace := float64(s.TraceValuePairs) / float64(s.TracesWithKey)
		if presence < opt.MinPresence || s.DistinctValues < opt.MinValues || s.DistinctValues > opt.MaxValues || perTrace > opt.MaxValuesPerTrace {
			continue
		}
		if s.NumericValues == s.DistinctValues || s.BooleanValues == s.DistinctValues {
			continue // a quantity or a yes/no, not a set of flows
		}
		servicesPerKey[s.Key]++
		shaped = append(shaped, PartitionCandidate{
			Service: s.Service, Key: s.Key, Presence: presence,
			Values: s.DistinctValues, PerTrace: perTrace, Traces: s.TracesTotal,
			NamedAsFlow: NamesAFlow(s.Key),
		})
	}
	var out []PartitionCandidate
	for _, c := range shaped {
		if servicesPerKey[c.Key] >= TravellingServices && !c.NamedAsFlow {
			continue // a property of the message, travelling with it
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].NamedAsFlow != out[j].NamedAsFlow {
			return out[i].NamedAsFlow
		}
		if out[i].Presence != out[j].Presence {
			return out[i].Presence > out[j].Presence
		}
		if out[i].Values != out[j].Values {
			return out[i].Values < out[j].Values
		}
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Key < out[j].Key
	})
	kept := []PartitionCandidate{}
	count := map[string]int{}
	for _, c := range out {
		if perService > 0 && count[c.Service] >= perService {
			continue
		}
		if limit > 0 && len(kept) >= limit {
			break
		}
		count[c.Service]++
		kept = append(kept, c)
	}
	return kept
}

// PartitionValue is one value of a partitioning attribute: one proposed
// integration.
type PartitionValue struct {
	Value       string
	Traces      uint64
	ErrorTraces uint64
	FirstSeen   time.Time
	LastSeen    time.Time
}
