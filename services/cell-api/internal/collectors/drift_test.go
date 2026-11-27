// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"reflect"
	"testing"
)

const driftSent = `receivers:
  host_metrics:
    collection_interval: 30s
    scrapers:
      load: {}
      memory: {}
exporters:
  otlp_http/sluicio:
    endpoint: https://ingest.example.test
service:
  extensions: [opamp]
  pipelines:
    metrics:
      receivers: [host_metrics]
      exporters: [otlp_http/sluicio]
`

func TestDiffSent(t *testing.T) {
	// The supervisor's merge: what was sent, plus its own extension and
	// settings from the local files. In sync.
	merged := driftSent + `extensions:
  opamp:
    instance_uid: 01a21e66
  health_check: {}
`
	got, err := DiffSent(driftSent, merged)
	if err != nil || len(got) != 0 {
		t.Fatalf("in sync: %v %v", got, err)
	}

	// The local files dropped a scraper and changed the interval; a
	// pipeline list differs. Paths only, sorted.
	drifted := `receivers:
  host_metrics:
    collection_interval: 10s
    scrapers:
      load: {}
exporters:
  otlp_http/sluicio:
    endpoint: https://ingest.example.test
service:
  extensions: [opamp]
  pipelines:
    metrics:
      receivers: [host_metrics]
      exporters: [otlp_http/sluicio, debug]
`
	got, err = DiffSent(driftSent, drifted)
	if err != nil {
		t.Fatal(err)
	}
	want := []DriftPath{
		{Path: "receivers.host_metrics.collection_interval", Kind: "changed"},
		{Path: "receivers.host_metrics.scrapers.memory", Kind: "missing"},
		{Path: "service.pipelines.metrics.exporters", Kind: "changed"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("drift:\n got %v\nwant %v", got, want)
	}

	// Nothing sent (back to the local files): nothing to check.
	if got, _ := DiffSent("", drifted); len(got) != 0 {
		t.Fatalf("empty sent: %v", got)
	}
	// An empty map sent is satisfied by the key, whatever is under it.
	if got, _ := DiffSent("receivers:\n  otlp: {}\n", "receivers:\n  otlp:\n    protocols:\n      http: {}\n"); len(got) != 0 {
		t.Fatalf("empty map: %v", got)
	}
	if _, err := DiffSent(driftSent, "a: ["); err == nil {
		t.Fatal("broken YAML accepted")
	}
}

func TestDiffSince(t *testing.T) {
	before := "receivers:\n  host_metrics:\n    scrapers:\n      load: {}\n    collection_interval: 30s\nexporters:\n  debug: {}\n"
	now := "receivers:\n  host_metrics:\n    scrapers:\n      load: {}\n      cpu: {}\n    collection_interval: 10s\n"
	got, err := DiffSince(before, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []DriftPath{
		{Path: "exporters", Kind: "removed"},
		{Path: "receivers.host_metrics.collection_interval", Kind: "changed"},
		{Path: "receivers.host_metrics.scrapers.cpu", Kind: "added"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("since:\n got %v\nwant %v", got, want)
	}
	if got, _ := DiffSince(now, now); len(got) != 0 {
		t.Fatalf("unchanged: %v", got)
	}
	// Both checks, one entry per path.
	m := mergeDrift([]DriftPath{{Path: "a", Kind: "missing"}}, []DriftPath{{Path: "a", Kind: "removed"}, {Path: "b", Kind: "added"}})
	if len(m) != 2 || m[0].Kind != "missing" {
		t.Fatalf("merge: %v", m)
	}
}
