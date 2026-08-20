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

// MessageFilter is one attribute an integration may be filtered by.
type MessageFilter struct {
	// Key is the span (or resource) attribute name, verbatim as it
	// appears in the telemetry. This is what the query uses.
	Key string `json:"key"`
	// Label is what the reader sees. Never the query, never the stored
	// filter — see the file comment.
	Label string `json:"label"`
}

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
		seen[key] = true
		label := strings.TrimSpace(f.Label)
		if label == "" {
			label = HumanizeKey(key)
		}
		if len(label) > MaxMessageColumnLabel {
			label = label[:MaxMessageColumnLabel]
		}
		out = append(out, MessageFilter{Key: key, Label: label})
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
	if len(filters) == 0 {
		return true
	}
	key = strings.TrimSpace(key)
	for _, f := range filters {
		if f.Key == key {
			return true
		}
	}
	return false
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
