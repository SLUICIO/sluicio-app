// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Message columns: the column set of an integration's message list
// (issue #23).
//
// One ordered list holds both kinds of column — the built-in facts
// (message id, service, step, duration) and promoted span attributes —
// because from the reader's side they are the same thing: cells in a
// row, left to right. Splitting them into "the real columns" and "the
// extra ones" would mean two orderings that have to agree, and a user
// who wants their document count before the service name could not say
// so.
//
// The rules here are about keeping the table usable rather than
// correct: there is no wrong attribute to promote. What there IS is a
// table that runs out of width, a label that says nothing, a key that
// silently appears twice, and a list that removes the affordance you
// need to open a row.

package integrations

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// MaxMessageColumns caps how many ATTRIBUTES an integration can
// promote. Built-ins are not counted: they are a fixed, known set, and
// counting them would mean turning a built-in back on could push a
// legal configuration over the limit.
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

// Column kinds. An entry with no kind is an attribute: that is what
// every row written before built-ins were configurable contains, and
// reading them as attributes is what makes those rows keep working.
const (
	ColumnKindAttribute = "attribute"
	ColumnKindBuiltin   = "builtin"
)

// Built-in column ids. These are the facts the list can render without
// asking the telemetry for anything extra.
const (
	BuiltinMsgID    = "msg_id"
	BuiltinService  = "service"
	BuiltinStep     = "step"
	BuiltinDuration = "duration"
)

// builtinLabels is the default heading for each built-in, and doubles
// as the allow-list: an unknown built-in id is rejected rather than
// dropped, because a typo that silently loses a column is worse than an
// error that says which one.
var builtinLabels = map[string]string{
	BuiltinMsgID:    "msg id",
	BuiltinService:  "service",
	BuiltinStep:     "step",
	BuiltinDuration: "duration",
}

// MessageColumn is one column of the message list: either a built-in
// fact or a promoted span attribute.
type MessageColumn struct {
	// Kind is ColumnKindBuiltin or ColumnKindAttribute. Empty reads as
	// attribute, for rows written before built-ins were configurable.
	Kind string `json:"kind,omitempty"`
	// Key is the span (or resource) attribute name for an attribute
	// column, verbatim as it appears in the telemetry; for a built-in
	// it is the built-in's id.
	Key string `json:"key"`
	// Label is what the column header reads. Never derived at render
	// time — see the migration for why.
	Label string `json:"label"`
}

// IsAttribute reports whether this column reads from the telemetry.
// Empty Kind counts as an attribute; see the Kind field.
func (c MessageColumn) IsAttribute() bool { return c.Kind != ColumnKindBuiltin }

// DefaultMessageColumns is the column set an integration has before
// anyone configures one — today's list, with service and step split
// apart so either can be dropped on its own.
//
// The status pip, the timestamp and the "open" affordance are NOT here.
// They are not configurable: a message list with no time cannot be
// read, and one with no way in cannot be used. Leaving them out of the
// list is what makes that guarantee structural rather than a rule
// somebody has to remember to enforce.
func DefaultMessageColumns() []MessageColumn {
	return []MessageColumn{
		{Kind: ColumnKindBuiltin, Key: BuiltinMsgID, Label: builtinLabels[BuiltinMsgID]},
		{Kind: ColumnKindBuiltin, Key: BuiltinService, Label: builtinLabels[BuiltinService]},
		{Kind: ColumnKindBuiltin, Key: BuiltinStep, Label: builtinLabels[BuiltinStep]},
		{Kind: ColumnKindBuiltin, Key: BuiltinDuration, Label: builtinLabels[BuiltinDuration]},
	}
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
// omits the label is asking for the default, and the built-in's name or
// HumanizeKey is a better answer than an error.
//
// De-duplication is per (kind, key): an attribute literally named
// "service" and the built-in service column are different columns that
// happen to share a word.
func NormalizeMessageColumns(in []MessageColumn) ([]MessageColumn, error) {
	out := make([]MessageColumn, 0, len(in))
	seen := make(map[string]bool, len(in))
	attrs := 0
	for _, c := range in {
		key := strings.TrimSpace(c.Key)
		if key == "" {
			return nil, fmt.Errorf("%w: a column key is required", ErrInvalidMessageColumns)
		}
		kind := ColumnKindAttribute
		if c.Kind == ColumnKindBuiltin {
			kind = ColumnKindBuiltin
		} else if c.Kind != "" && c.Kind != ColumnKindAttribute {
			return nil, fmt.Errorf("%w: unknown column kind %q", ErrInvalidMessageColumns, c.Kind)
		}

		defaultLabel := ""
		if kind == ColumnKindBuiltin {
			l, ok := builtinLabels[key]
			if !ok {
				// Not dropped: a typo that silently loses a column is
				// worse than an error naming it.
				return nil, fmt.Errorf("%w: unknown built-in column %q", ErrInvalidMessageColumns, key)
			}
			defaultLabel = l
		} else {
			defaultLabel = HumanizeKey(key)
			attrs++
		}

		if seen[kind+"\x00"+key] {
			if kind == ColumnKindAttribute {
				attrs-- // a duplicate does not consume the budget
			}
			continue
		}
		seen[kind+"\x00"+key] = true

		label := strings.TrimSpace(c.Label)
		if label == "" {
			label = defaultLabel
		}
		if len([]rune(label)) > MaxMessageColumnLabel {
			return nil, fmt.Errorf("%w: %q is longer than %d characters", ErrInvalidMessageColumns, label, MaxMessageColumnLabel)
		}
		out = append(out, MessageColumn{Kind: kind, Key: key, Label: label})
	}
	if attrs > MaxMessageColumns {
		return nil, fmt.Errorf("%w: at most %d attribute columns", ErrInvalidMessageColumns, MaxMessageColumns)
	}
	return out, nil
}

// MessageColumnKeys is the ATTRIBUTE key list in column order, for the
// query layer. Built-ins are skipped: they are rendered from fields the
// row already carries and must not become promoted-column lookups —
// asking ClickHouse for an attribute named "duration" would return
// nothing and blank the column.
func MessageColumnKeys(cols []MessageColumn) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		if c.IsAttribute() {
			out = append(out, c.Key)
		}
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
