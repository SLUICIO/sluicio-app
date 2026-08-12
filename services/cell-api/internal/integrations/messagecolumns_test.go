// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integrations

import "testing"

func TestHumanizeKey(t *testing.T) {
	cases := map[string]string{
		// Two segments: both kept. Dropping the first would give
		// "Exported", losing the only word that says what was counted.
		"documents.exported": "Documents exported",
		"archive.month_from": "Archive month from",
		// Three or more: the first is a vendor/signal namespace and
		// repeating it in a column header is noise.
		"node_red.flow.name":   "Flow name",
		"http.response.status": "Response status",
		// No namespace to drop — the whole key is the meaning.
		"count": "Count",
		// A trailing separator must not leave a dangling dot.
		"weird.": "Weird",
		// Already readable stays readable.
		"Documents exported": "Documents exported",
		"":                   "",
	}
	for in, want := range cases {
		if got := HumanizeKey(in); got != want {
			t.Errorf("HumanizeKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeMessageColumns(t *testing.T) {
	t.Run("fills an omitted label from the key", func(t *testing.T) {
		got, err := NormalizeMessageColumns([]MessageColumn{{Key: "documents.exported"}})
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Label != "Documents exported" {
			t.Errorf("label = %q", got[0].Label)
		}
	})

	t.Run("keeps a label the user chose", func(t *testing.T) {
		// The whole point of storing the label: "Docs" is a worse
		// default and a better choice, and only the user knows which.
		got, err := NormalizeMessageColumns([]MessageColumn{{Key: "documents.exported", Label: "Docs"}})
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Label != "Docs" {
			t.Errorf("label = %q", got[0].Label)
		}
	})

	t.Run("preserves order, because order is the column order", func(t *testing.T) {
		got, _ := NormalizeMessageColumns([]MessageColumn{{Key: "b"}, {Key: "a"}, {Key: "c"}})
		if got[0].Key != "b" || got[1].Key != "a" || got[2].Key != "c" {
			t.Errorf("order changed: %+v", got)
		}
	})

	t.Run("drops a repeated key, keeping the first position", func(t *testing.T) {
		got, err := NormalizeMessageColumns([]MessageColumn{
			{Key: "a", Label: "First"}, {Key: "b"}, {Key: "a", Label: "Second"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("want 2 columns, got %d: %+v", len(got), got)
		}
		if got[0].Key != "a" || got[0].Label != "First" {
			t.Errorf("kept the wrong occurrence: %+v", got[0])
		}
	})

	t.Run("trims whitespace on both sides", func(t *testing.T) {
		got, _ := NormalizeMessageColumns([]MessageColumn{{Key: "  a.b  ", Label: "  Label  "}})
		if got[0].Key != "a.b" || got[0].Label != "Label" {
			t.Errorf("not trimmed: %+v", got[0])
		}
	})

	t.Run("a whitespace-only key is an error, not a silent drop", func(t *testing.T) {
		if _, err := NormalizeMessageColumns([]MessageColumn{{Key: "   "}}); err == nil {
			t.Error("want an error for an empty key")
		}
	})

	t.Run("refuses more than the cap", func(t *testing.T) {
		in := make([]MessageColumn, MaxMessageColumns+1)
		for i := range in {
			in[i] = MessageColumn{Key: string(rune('a' + i))}
		}
		if _, err := NormalizeMessageColumns(in); err == nil {
			t.Error("want an error past the cap")
		}
	})

	t.Run("counts the cap AFTER de-duplication", func(t *testing.T) {
		// Six entries that collapse to two is a two-column list. Failing
		// here would reject a request whose stored result is legal.
		in := []MessageColumn{{Key: "a"}, {Key: "a"}, {Key: "a"}, {Key: "b"}, {Key: "b"}, {Key: "b"}}
		got, err := NormalizeMessageColumns(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("want 2, got %d", len(got))
		}
	})

	t.Run("refuses an over-long label", func(t *testing.T) {
		long := make([]rune, MaxMessageColumnLabel+1)
		for i := range long {
			long[i] = 'x'
		}
		if _, err := NormalizeMessageColumns([]MessageColumn{{Key: "a", Label: string(long)}}); err == nil {
			t.Error("want an error for an over-long label")
		}
	})

	t.Run("measures the label in runes, not bytes", func(t *testing.T) {
		// A Swedish label of legal length must not be rejected for
		// being multi-byte.
		label := ""
		for i := 0; i < MaxMessageColumnLabel; i++ {
			label += "å"
		}
		if _, err := NormalizeMessageColumns([]MessageColumn{{Key: "a", Label: label}}); err != nil {
			t.Errorf("rejected a legal label: %v", err)
		}
	})

	t.Run("an empty list is valid and means the default column set", func(t *testing.T) {
		got, err := NormalizeMessageColumns(nil)
		if err != nil || len(got) != 0 {
			t.Errorf("got %+v, %v", got, err)
		}
	})
}

func TestNormalizeMessageColumnsBuiltins(t *testing.T) {
	t.Run("an untagged entry stays an attribute", func(t *testing.T) {
		// Every row written before built-ins were configurable looks
		// like this. Reading them as attributes is what keeps them
		// working.
		got, err := NormalizeMessageColumns([]MessageColumn{{Key: "documents.exported"}})
		if err != nil {
			t.Fatal(err)
		}
		if !got[0].IsAttribute() || got[0].Kind != ColumnKindAttribute {
			t.Errorf("got %+v", got[0])
		}
	})

	t.Run("fills a built-in's label from its own name, not from the key", func(t *testing.T) {
		// HumanizeKey("msg_id") would produce "Msg id"; the built-in
		// has a name of its own and it is the one the table already uses.
		got, _ := NormalizeMessageColumns([]MessageColumn{{Kind: ColumnKindBuiltin, Key: BuiltinMsgID}})
		if got[0].Label != "msg id" {
			t.Errorf("label = %q", got[0].Label)
		}
	})

	t.Run("rejects an unknown built-in rather than dropping it", func(t *testing.T) {
		// Silently losing a column the user asked for is worse than an
		// error that names it.
		_, err := NormalizeMessageColumns([]MessageColumn{{Kind: ColumnKindBuiltin, Key: "nope"}})
		if err == nil {
			t.Error("want an error for an unknown built-in")
		}
	})

	t.Run("rejects an unknown kind", func(t *testing.T) {
		if _, err := NormalizeMessageColumns([]MessageColumn{{Kind: "magic", Key: "a"}}); err == nil {
			t.Error("want an error for an unknown kind")
		}
	})

	t.Run("a built-in and an attribute may share a key", func(t *testing.T) {
		// "service" the built-in and an attribute literally named
		// "service" are different columns that share a word.
		got, err := NormalizeMessageColumns([]MessageColumn{
			{Kind: ColumnKindBuiltin, Key: BuiltinService},
			{Kind: ColumnKindAttribute, Key: BuiltinService, Label: "Service attr"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("de-duplicated across kinds: %+v", got)
		}
	})

	t.Run("built-ins do not consume the attribute budget", func(t *testing.T) {
		// Otherwise turning a built-in back on could push a legal
		// configuration over the limit.
		in := DefaultMessageColumns()
		for i := 0; i < MaxMessageColumns; i++ {
			in = append(in, MessageColumn{Key: "attr" + string(rune('a'+i))})
		}
		if _, err := NormalizeMessageColumns(in); err != nil {
			t.Errorf("rejected %d built-ins + %d attributes: %v", len(DefaultMessageColumns()), MaxMessageColumns, err)
		}
	})

	t.Run("still refuses more attributes than the cap", func(t *testing.T) {
		in := DefaultMessageColumns()
		for i := 0; i <= MaxMessageColumns; i++ {
			in = append(in, MessageColumn{Key: "attr" + string(rune('a'+i))})
		}
		if _, err := NormalizeMessageColumns(in); err == nil {
			t.Error("want an error past the attribute cap")
		}
	})

	t.Run("a duplicate attribute does not consume the budget", func(t *testing.T) {
		// Five distinct keys plus a repeat of one collapses to five,
		// which is legal — rejecting it would refuse a request whose
		// stored result is fine.
		in := []MessageColumn{}
		for i := 0; i < MaxMessageColumns; i++ {
			in = append(in, MessageColumn{Key: "attr" + string(rune('a'+i))})
		}
		in = append(in, MessageColumn{Key: "attra"})
		got, err := NormalizeMessageColumns(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != MaxMessageColumns {
			t.Errorf("want %d, got %d", MaxMessageColumns, len(got))
		}
	})
}

func TestMessageColumnKeysSkipsBuiltins(t *testing.T) {
	// The query layer must never look up a span attribute named
	// "duration": there is none, so the column would render blank.
	keys := MessageColumnKeys(append(DefaultMessageColumns(),
		MessageColumn{Kind: ColumnKindAttribute, Key: "documents.exported"}))
	if len(keys) != 1 || keys[0] != "documents.exported" {
		t.Errorf("got %v, want only the attribute key", keys)
	}
}

func TestDefaultMessageColumnsExcludeTheFixedChrome(t *testing.T) {
	// The status pip, the timestamp and "open" are not columns. Keeping
	// them out of the list is what makes "you cannot remove them" a
	// property of the model rather than a rule to remember.
	for _, c := range DefaultMessageColumns() {
		switch c.Key {
		case "time", "timestamp", "status", "open":
			t.Errorf("%q must not be configurable", c.Key)
		}
	}
	if len(DefaultMessageColumns()) == 0 {
		t.Error("the default set must not be empty")
	}
}
