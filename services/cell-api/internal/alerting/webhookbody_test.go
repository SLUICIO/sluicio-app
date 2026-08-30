// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"encoding/json"
	"strings"
	"testing"
)

func render(t *testing.T, tmpl string, b map[string]any) map[string]any {
	t.Helper()
	doc, ok := RenderWebhookTemplate(tmpl, b)
	if !ok {
		t.Fatalf("render failed for %q", tmpl)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// The reason this is a JSON mapping rather than a text template. A
// summary carrying quotes and newlines must not be able to produce a
// body the receiver rejects.
func TestValuesThatWouldBreakATextTemplate(t *testing.T) {
	b := map[string]any{"alert": map[string]any{
		"summary": `Order "A-42" failed` + "\n" + `at step 3\`,
	}}
	out := render(t, `{"text_content": "$alert.summary"}`, b)
	got, _ := out["text_content"].(string)
	if got != `Order "A-42" failed`+"\n"+`at step 3\` {
		t.Fatalf("value mangled: %q", got)
	}
}

// A whole-value reference keeps its type: a receiver whose schema says
// number must not be handed a string.
func TestWholeValueReferenceKeepsType(t *testing.T) {
	b := map[string]any{"check": map[string]any{"value": 4.2, "firing": true}}
	out := render(t, `{"v": "$check.value", "f": "$check.firing"}`, b)
	if _, ok := out["v"].(float64); !ok {
		t.Fatalf("v = %T, want number", out["v"])
	}
	if _, ok := out["f"].(bool); !ok {
		t.Fatalf("f = %T, want bool", out["f"])
	}
}

func TestInlineReferenceInterpolates(t *testing.T) {
	b := map[string]any{"alert": map[string]any{"state": "firing", "severity": "critical"}}
	out := render(t, `{"s": "[${alert.state}] ${alert.severity} alert"}`, b)
	if out["s"] != "[firing] critical alert" {
		t.Fatalf("s = %v", out["s"])
	}
}

// A path with nothing behind it is null in a value position and empty
// when interpolated. Neither may produce invalid JSON.
func TestMissingPath(t *testing.T) {
	out := render(t, `{"a": "$service.nope", "b": "x$service.nope y"}`, map[string]any{})
	if out["a"] != nil {
		t.Fatalf("a = %v, want null", out["a"])
	}
	if out["b"] != "x y" {
		t.Fatalf("b = %q", out["b"])
	}
}

// Keys are the receiver's schema. Substituting them would let a template
// rename fields the receiver requires.
func TestKeysAreNotSubstituted(t *testing.T) {
	b := map[string]any{"alert": map[string]any{"state": "firing"}}
	out := render(t, `{"$alert.state": "x"}`, b)
	if _, ok := out["$alert.state"]; !ok {
		t.Fatalf("key was substituted: %v", out)
	}
}

func TestNestedAndArrays(t *testing.T) {
	b := map[string]any{"alert": map[string]any{"summary": "s"}, "rule": map[string]any{"name": "r"}}
	out := render(t, `{"recipients":[{"email":"a@b.c"}],"subject":"$rule.name","o":{"t":"$alert.summary"}}`, b)
	if out["subject"] != "r" {
		t.Fatalf("subject = %v", out["subject"])
	}
	if out["o"].(map[string]any)["t"] != "s" {
		t.Fatalf("nested not substituted: %v", out["o"])
	}
	if len(out["recipients"].([]any)) != 1 {
		t.Fatalf("array lost: %v", out["recipients"])
	}
}

// Validation runs at save time, which is the whole point: the author
// finds out now rather than from an alert that never arrived.
func TestValidation(t *testing.T) {
	t.Run("rejects non-JSON", func(t *testing.T) {
		if _, err := ValidateWebhookTemplate(`{"a": }`); err == nil {
			t.Fatal("accepted invalid JSON")
		}
	})
	t.Run("rejects empty", func(t *testing.T) {
		if _, err := ValidateWebhookTemplate("  "); err == nil {
			t.Fatal("accepted empty template")
		}
	})
	t.Run("rejects an unknown variable", func(t *testing.T) {
		_, err := ValidateWebhookTemplate(`{"s": "$alert.sumary"}`)
		if err == nil {
			t.Fatal("accepted a misspelled path")
		}
		if !strings.Contains(err.Error(), "alert.sumary") {
			t.Fatalf("error does not name the path: %v", err)
		}
	})
	t.Run("accepts known variables", func(t *testing.T) {
		paths, err := ValidateWebhookTemplate(`{"a":"$alert.state","b":"x ${alert.severity}"}`)
		if err != nil {
			t.Fatalf("rejected a valid template: %v", err)
		}
		if len(paths) != 2 {
			t.Fatalf("paths = %v, want both", paths)
		}
	})
}

// The AhaSend shape from the feature request, end to end.
func TestAhaSendShape(t *testing.T) {
	tmpl := `{
	  "from": {"email": "alerts@romait.se", "name": "Sluicio"},
	  "recipients": [{"email": "ops@example.com"}],
	  "subject": "[${alert.state}] ${alert.severity}",
	  "text_content": "$alert.summary"
	}`
	if _, err := ValidateWebhookTemplate(tmpl); err != nil {
		t.Fatalf("AhaSend template rejected: %v", err)
	}
	b := map[string]any{"alert": map[string]any{
		"state": "firing", "severity": "critical", "summary": `a "quoted" summary`,
	}}
	out := render(t, tmpl, b)
	if out["subject"] != "[firing] critical" {
		t.Fatalf("subject = %v", out["subject"])
	}
	if out["text_content"] != `a "quoted" summary` {
		t.Fatalf("text_content = %v", out["text_content"])
	}
}
