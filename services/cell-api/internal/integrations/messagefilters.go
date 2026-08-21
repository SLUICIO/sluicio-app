// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Which attributes an integration may be filtered by, and what they are
// called (issue #31).
//
// This is the filter-side twin of messagecolumns.go. That one decides
// which attributes appear as COLUMNS; this one decides which may be used
// as FILTER FIELDS. Same shape, same label handling, configured on the
// same screen by the same person about the same attributes — because the
// label a reader sees in a column header should be the label they see in
// the filter that narrows it.
//
// # Empty means unrestricted
//
// An integration nobody has configured offers every attribute, which is
// today's behaviour. The restriction exists only where somebody made
// one, so nothing changes on upgrade and nothing narrows silently.
//
// # A non-empty list is enforced, not merely offered
//
// The picker shows exactly these fields, AND a search against the
// integration naming a field outside the list is rejected. Curating only
// the UI would leave the promise hollow for anyone calling the API, and
// "these are the fields you may use" would not be true.
//
// The cost is that a saved view or bookmarked link using a field later
// removed stops working. That is the right failure: it is loud, it names
// the field, and it is what the editor asked for. Quietly returning
// unfiltered results would be worse — a filter that appears to apply and
// does not is how somebody reads the wrong customer's messages and never
// finds out.
//
// # Labels are display only
//
// The stored filter, the API payload and the audit trail keep the real
// attribute key. A label that leaked into the data model would make
// `KundId` unsearchable the day somebody renames it.

package integrations

import (
	"fmt"
	"strings"
)

// MaxMessageFilters caps how many attributes an integration can expose
// as filter fields.
//
// Higher than the column cap because a filter list is a menu rather than
// a row of headers: it costs vertical space in a dropdown, not
// horizontal space in a table, and the whole point of the feature is
// picking a usable handful out of a hundred.
const MaxMessageFilters = 20

// Filter field kinds, mirroring the column kinds. An entry with no kind
// reads as an attribute, which is what every row written before the
// standard fields were configurable contains.
const (
	FilterKindAttribute = "attribute"
	FilterKindBuiltin   = "builtin"
)

// Built-in filter fields — the ones that are part of the product's own
// vocabulary rather than the customer's telemetry.
//
// Deliberately NOT the whole Field enum. `time` is the window picker,
// `integration` is the page you are on, and `payload` is the attribute
// kind itself; none of the three is something an editor curates here.
const (
	FilterStatus    = "status"
	FilterService   = "service"
	FilterErrorType = "errorType"
	FilterTraceID   = "traceId"
	FilterSpanID    = "spanId"
)

// builtinFilterLabels is the default label for each built-in field, and
// doubles as the allow-list: an unknown id is rejected rather than
// dropped, because a typo that silently loses a field is worse than an
// error naming it.
var builtinFilterLabels = map[string]string{
	FilterStatus:    "status",
	FilterService:   "service",
	FilterErrorType: "error type",
	FilterTraceID:   "trace ID",
	FilterSpanID:    "span ID",
}

// TechnicalFilterFields are the built-ins that a reader without a
// technical background has no use for, and that are grouped apart in the
// picker even when they are offered.
//
// What sets them apart is not that they are technical but that they are
// LOOKUP fields rather than BROWSE fields: nobody explores by trace id,
// you arrive already holding one from a support ticket or a link. A
// field only usable once you know the answer does not belong beside the
// fields you search with.
//
// They stay available because removing them outright would hurt the
// person working a customer's report at an awkward hour, who is exactly
// the one with an id in hand.
var TechnicalFilterFields = map[string]bool{
	FilterService:   true,
	FilterErrorType: true,
	FilterTraceID:   true,
	FilterSpanID:    true,
}

// MessageFilter is one field an integration may be filtered by: either a
// built-in of the product's own vocabulary, or a span attribute.
type MessageFilter struct {
	// Kind is FilterKindBuiltin or FilterKindAttribute. Empty reads as
	// attribute, for rows written before built-ins were configurable.
	Kind string `json:"kind,omitempty"`
	// Key is the span (or resource) attribute name for an attribute
	// field, verbatim as it appears in the telemetry; for a built-in it
	// is the built-in's id. This is what the query uses.
	Key string `json:"key"`
	// Label is what the reader sees. Never the query, never the stored
	// filter — see the file comment.
	Label string `json:"label"`
}

// IsBuiltin reports whether this entry names a standard field.
func (f MessageFilter) IsBuiltin() bool { return f.Kind == FilterKindBuiltin }

// NormalizeMessageFilters trims, de-duplicates and caps a filter list,
// filling in a humanised label where none was given.
//
// Rejects rather than truncates when over the cap: silently dropping the
// twenty-first field would leave an editor believing they had exposed
// something they had not, and the first they would hear of it is a
// colleague unable to find a filter.
func NormalizeMessageFilters(in []MessageFilter) ([]MessageFilter, error) {
	out := make([]MessageFilter, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, f := range in {
		key := strings.TrimSpace(f.Key)
		if key == "" {
			continue
		}
		if seen[key] {
			continue
		}
		kind := strings.TrimSpace(f.Kind)
		if kind == "" {
			kind = FilterKindAttribute
		}
		if kind != FilterKindAttribute && kind != FilterKindBuiltin {
			return nil, fmt.Errorf("unknown filter kind %q", kind)
		}
		if kind == FilterKindBuiltin {
			if _, ok := builtinFilterLabels[key]; !ok {
				return nil, fmt.Errorf("unknown standard field %q", key)
			}
		}
		seen[key] = true
		label := strings.TrimSpace(f.Label)
		if label == "" {
			if kind == FilterKindBuiltin {
				label = builtinFilterLabels[key]
			} else {
				label = HumanizeKey(key)
			}
		}
		if len(label) > MaxMessageColumnLabel {
			label = label[:MaxMessageColumnLabel]
		}
		out = append(out, MessageFilter{Kind: kind, Key: key, Label: label})
	}
	if len(out) > MaxMessageFilters {
		return nil, fmt.Errorf("at most %d filter fields per integration", MaxMessageFilters)
	}
	return out, nil
}

// MessageFilterAllowed reports whether an attribute key may be used as a
// filter field for this integration.
//
// An empty list allows everything, which is what an unconfigured
// integration means. Callers must pass the integration's own list; a nil
// list from a lookup failure therefore reads as unrestricted, which is
// the safe direction for a feature that is about tidiness rather than
// access control. Nothing here is a security boundary: RBAC decides what
// a caller may read, this decides what they may narrow by.
func MessageFilterAllowed(filters []MessageFilter, key string) bool {
	return kindAllowed(AttributeFilters(filters), key)
}

// BuiltinFilterAllowed reports whether a standard field may be used.
//
// Each KIND is governed by its own entries, which is the rule that keeps
// this safe to extend. A list naming only attributes leaves the standard
// fields unrestricted, so every integration configured before the
// standard fields became curatable keeps its status filter. Reading the
// list as one complete allow-list would have silently removed it from
// all of them.
func BuiltinFilterAllowed(filters []MessageFilter, key string) bool {
	return kindAllowed(BuiltinFilters(filters), key)
}

func kindAllowed(ofKind []MessageFilter, key string) bool {
	if len(ofKind) == 0 {
		return true
	}
	key = strings.TrimSpace(key)
	for _, f := range ofKind {
		if f.Key == key {
			return true
		}
	}
	return false
}

// AttributeFilters returns only the attribute entries, for the callers
// that narrow an attribute-key catalogue.
func AttributeFilters(filters []MessageFilter) []MessageFilter {
	out := make([]MessageFilter, 0, len(filters))
	for _, f := range filters {
		if !f.IsBuiltin() {
			out = append(out, f)
		}
	}
	return out
}

// BuiltinFilters returns only the standard-field entries.
func BuiltinFilters(filters []MessageFilter) []MessageFilter {
	out := make([]MessageFilter, 0, len(filters))
	for _, f := range filters {
		if f.IsBuiltin() {
			out = append(out, f)
		}
	}
	return out
}

// AllBuiltinFilters is every standard field an editor can offer, with
// its default label — the vocabulary the editor picks from.
func AllBuiltinFilters() []MessageFilter {
	order := []string{FilterStatus, FilterService, FilterErrorType, FilterTraceID, FilterSpanID}
	out := make([]MessageFilter, 0, len(order))
	for _, k := range order {
		out = append(out, MessageFilter{Kind: FilterKindBuiltin, Key: k, Label: builtinFilterLabels[k]})
	}
	return out
}

// MessageFilterKeys returns just the keys, for a caller building a
// picker or validating a request.
func MessageFilterKeys(filters []MessageFilter) []string {
	out := make([]string, 0, len(filters))
	for _, f := range filters {
		out = append(out, f.Key)
	}
	return out
}
