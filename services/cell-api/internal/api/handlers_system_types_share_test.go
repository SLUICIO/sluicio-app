// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The shareable system-type document: YAML/JSON round-trip fidelity
// and the strict validation that guards import (files arrive from the
// internet — that's the point of the feature).
package api

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func sampleDoc() systemTypeDoc {
	return systemTypeDoc{
		Format:         SystemTypeDocFormat,
		Key:            "mosquitto",
		Label:          "Eclipse Mosquitto",
		IsSystem:       true,
		DetectPrefixes: []string{"mosquitto."},
		Checks: []docCheck{
			{
				Name: "Broker load", Signal: "metric", Metric: "mosquitto.messages.received",
				Agg: "increase", Op: ">", Threshold: 100000, Severity: "warning", Unit: "msgs",
				Attrs: []docAttrFilter{{Key: "listener", Op: "eq", Value: "1883"}},
			},
			{Name: "Failed traces", Signal: "trace_error", TraceThreshold: 3, WindowSeconds: 600, Severity: "critical"},
			{Name: "Error logs", Signal: "log", MinSeverity: 17, LogThreshold: 5},
		},
	}
}

func TestSystemTypeDocRoundTrip(t *testing.T) {
	doc := sampleDoc()

	// YAML round-trip preserves every field.
	y, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("yaml marshal: %v", err)
	}
	// The shared file must use snake_case keys (the whole point of the
	// dual tags) — a stranger hand-editing the file sees the documented
	// names.
	for _, want := range []string{"detect_prefixes:", "trace_threshold:", "window_seconds:", "min_severity:", "split_by"} {
		if want == "split_by" {
			continue // only present when set
		}
		if !strings.Contains(string(y), want) {
			t.Fatalf("yaml missing snake_case key %q:\n%s", want, y)
		}
	}
	var back systemTypeDoc
	if err := yaml.Unmarshal(y, &back); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	jA, _ := json.Marshal(doc)
	jB, _ := json.Marshal(back)
	if string(jA) != string(jB) {
		t.Fatalf("yaml round-trip drifted:\n%s\nvs\n%s", jA, jB)
	}

	// JSON round-trip too (import accepts either).
	var backJSON systemTypeDoc
	if err := json.Unmarshal(jA, &backJSON); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if jC, _ := json.Marshal(backJSON); string(jC) != string(jA) {
		t.Fatalf("json round-trip drifted")
	}

	// Check conversion to/from the stored shape loses nothing.
	stored := docToCheck(doc.Checks[0])
	gotJSON, _ := json.Marshal(checkToDoc(stored))
	wantJSON, _ := json.Marshal(doc.Checks[0])
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("check conversion drifted:\n%s\nvs\n%s", gotJSON, wantJSON)
	}
	if stored.Attrs[0].Key != "listener" || stored.Metric != "mosquitto.messages.received" {
		t.Fatalf("stored check wrong: %+v", stored)
	}
}

func TestSystemTypeDocValidation(t *testing.T) {
	ok := func(mutate func(*systemTypeDoc)) error {
		d := sampleDoc()
		mutate(&d)
		return validateSystemTypeDoc(&d)
	}
	if err := ok(func(*systemTypeDoc) {}); err != nil {
		t.Fatalf("sample doc must validate: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*systemTypeDoc)
	}{
		{"missing format", func(d *systemTypeDoc) { d.Format = "" }},
		{"future format", func(d *systemTypeDoc) { d.Format = "sluicio/system-type/v9" }},
		{"bad key", func(d *systemTypeDoc) { d.Key = "Bad Key!" }},
		{"empty label", func(d *systemTypeDoc) { d.Label = "  " }},
		{"unknown signal", func(d *systemTypeDoc) { d.Checks[0].Signal = "spans" }},
		{"unknown severity", func(d *systemTypeDoc) { d.Checks[0].Severity = "meltdown" }},
		{"metric check without metric", func(d *systemTypeDoc) { d.Checks[0].Metric = "" }},
		{"nameless check", func(d *systemTypeDoc) { d.Checks[1].Name = "" }},
		{"attr without key", func(d *systemTypeDoc) { d.Checks[0].Attrs[0].Key = "" }},
		{"too many checks", func(d *systemTypeDoc) {
			d.Checks = make([]docCheck, 51)
			for i := range d.Checks {
				d.Checks[i] = docCheck{Name: "c", Signal: "log"}
			}
		}},
	}
	for _, tc := range cases {
		if err := ok(tc.mutate); err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
	}

	// Key + label are normalised in place.
	d := sampleDoc()
	d.Key = "  MosQuitto  "
	if err := validateSystemTypeDoc(&d); err != nil {
		t.Fatalf("normalisable key rejected: %v", err)
	}
	if d.Key != "mosquitto" {
		t.Fatalf("key not normalised: %q", d.Key)
	}
}
