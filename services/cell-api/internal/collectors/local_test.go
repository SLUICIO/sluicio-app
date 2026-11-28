// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"reflect"
	"testing"
)

func TestKeptByLocal(t *testing.T) {
	// The supervisor's merge: a scraper and an exporter from the local
	// files, a scraper Sluicio sent, the supervisor's own extension.
	current := `receivers:
  host_metrics:
    scrapers:
      load: {}
      memory: {}
exporters:
  debug: {}
  otlp_http/sluicio:
    endpoint: https://ingest.example.test
extensions:
  opamp:
    instance_uid: x
service:
  pipelines:
    metrics:
      receivers: [host_metrics]
      exporters: [otlp_http/sluicio]
`
	sent := "receivers:\n  host_metrics:\n    scrapers:\n      memory: {}\n"
	// The edit removes both scrapers, the debug exporter and the opamp
	// extension.
	change := `receivers:
  host_metrics:
    scrapers: {}
exporters:
  otlp_http/sluicio:
    endpoint: https://ingest.example.test
service:
  pipelines:
    metrics:
      receivers: [host_metrics]
      exporters: [otlp_http/sluicio]
`
	got, err := KeptByLocal(current, sent, change)
	if err != nil {
		t.Fatal(err)
	}
	// memory was sent, so removing it takes effect; load and debug come
	// from the local files and stay; the supervisor's extension is not
	// the user's to remove.
	want := []string{"exporters.debug", "receivers.host_metrics.scrapers.load"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kept by local:\n got %v\nwant %v", got, want)
	}
	// Nothing ever sent: every removal comes from the local files.
	got, _ = KeptByLocal(current, "", change)
	if len(got) != 3 {
		t.Fatalf("nothing sent before: %v", got)
	}
	// Nothing removed: nothing to warn of.
	if got, _ := KeptByLocal(current, sent, current); len(got) != 0 {
		t.Fatalf("unchanged: %v", got)
	}
}
