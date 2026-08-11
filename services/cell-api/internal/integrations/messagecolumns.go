// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Message columns: the span attributes an integration promotes into its
// message list (issue #23).
//
// The list is small, ordered, and entirely user-chosen, so the rules
// here are about keeping it usable rather than keeping it correct —
// there is no wrong attribute to promote. What there IS is a table that
// runs out of width, a label that says nothing, and a key that silently
// appears twice.

package integrations

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// MaxMessageColumns caps how many attributes an integration can promote.
//
// The limit is the table, not the query: each promoted key costs one
// aggregate expression in a GROUP BY that already reads every span of
// every result trace, which is close to free. Five columns of prose
// beside a timestamp, a trace id, a service and a duration is already
// more than a laptop shows without scrolling.
const MaxMessageColumns = 5

// MaxMessageColumnLabel bounds a label so one entry cannot squeeze the
// rest of the row out of the viewport.
const MaxMessageColumnLabel = 40

// MessageColumn promotes one attribute key to a named column.
type MessageColumn struct {
	// Key is the span (or resource) attribute name, verbatim as it
	// appears in the telemetry.
	Key string `json:"key"`
	// Label is what the column header reads. Never derived at render
	// time — see the migration for why.
	Label string `json:"label"`
}

// ErrInvalidMessageColumns wraps every rejection from
// NormalizeMessageColumns, so the API layer can tell "the caller sent a
// bad list" (400) from "the database refused" (500) with one errors.Is
// instead of matching on message text.
var ErrInvalidMessageColumns = errors.New("invalid message columns")

// NormalizeMessageColumns trims, validates and de-duplicates a proposed
// column list, preserving order.
//
// De-duplication keeps the FIRST occurrence of a key. A repeated key is
// a mistake rather than an intent — two columns reading the same value
// under different headings is never what someone meant — and keeping
// the first preserves the position the user chose earliest.
//
// An empty key is rejected; an empty label is not, because a caller that
// omits the label is asking for the default, and HumanizeKey is a better
// answer than an error.
func NormalizeMessageColumns(in []MessageColumn) ([]MessageColumn, error) {
	out := make([]MessageColumn, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, c := range in {
		key := strings.TrimSpace(c.Key)
		if key == "" {
			return nil, fmt.Errorf("%w: a column key is required", ErrInvalidMessageColumns)
		}
		if seen[key] {
			continue
		}
		seen[key] = true

		label := strings.TrimSpace(c.Label)
		if label == "" {
			label = HumanizeKey(key)
		}
		if len([]rune(label)) > MaxMessageColumnLabel {
			return nil, fmt.Errorf("%w: %q is longer than %d characters", ErrInvalidMessageColumns, label, MaxMessageColumnLabel)
		}
		out = append(out, MessageColumn{Key: key, Label: label})
	}
	if len(out) > MaxMessageColumns {
		return nil, fmt.Errorf("%w: at most %d columns", ErrInvalidMessageColumns, MaxMessageColumns)
	}
	return out, nil
}

// MessageColumnKeys is the key list in column order, for the query layer.
func MessageColumnKeys(cols []MessageColumn) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.Key)
	}
	return out
}

// HumanizeKey turns an attribute key into a first-guess column label:
// separators to spaces, sentence case, and the leading namespace
// dropped when there plainly is one.
//
//	documents.exported   → "Documents exported"
//	archive.month_from   → "Archive month from"
//	node_red.flow.name   → "Flow name"
//	http.response.status → "Response status"
//	count                → "Count"
//
// The namespace is dropped only at THREE or more dotted segments. With
// three the first is nearly always a vendor or signal namespace
// (node_red., http., messaging.) and repeating it in a column header
// inside an integration that already names the system is noise. With
// two it is just as likely to be the subject — dropping it turns
// "documents.exported" into "Exported", which has lost the word that
// mattered.
//
// There is no rule that is right for every key, because a namespace is
// sometimes the scope and sometimes the subject. This is a pre-fill the
// user can overwrite, so being occasionally clumsy is acceptable in a
// way that being wrong about a VALUE would not be.
func HumanizeKey(key string) string {
	s := strings.TrimSpace(key)
	if s == "" {
		return ""
	}
	if parts := strings.Split(s, "."); len(parts) >= 3 {
		s = strings.Join(parts[1:], ".")
	}
	s = strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return strings.TrimSpace(key)
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
