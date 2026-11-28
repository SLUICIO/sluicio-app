// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const scrapeBase = `receivers:
  prometheus:
    config:
      scrape_configs:
        - job_name: app
          static_configs:
            - targets: ["app:9100"]
        - job_name: db
          metric_relabel_configs:
            - source_labels: [__name__]
              regex: old_(.*)
              target_label: __name__
              replacement: new_$$1
          static_configs:
            - targets: ["db:9187"]
  prometheus/shared:
    config:
      scrape_configs:
        - job_name: shared
          static_configs:
            - targets: ["x:1"]
  prometheus/files:
    config:
      scrape_config_files: [/etc/scrape.yaml]
  otlp:
    protocols:
      grpc: {}
exporters:
  otlp_http/sluicio:
    endpoint: https://ingest.example.test
  prometheus_remote_write:
    endpoint: https://prom.internal/api/v1/write
service:
  pipelines:
    metrics:
      receivers: [prometheus]
      exporters: [otlp_http/sluicio]
    metrics/shared:
      receivers: [prometheus/shared]
      exporters: [otlp_http/sluicio]
    metrics/shared-prom:
      receivers: [prometheus/shared]
      exporters: [prometheus_remote_write]
    metrics/files:
      receivers: [prometheus/files]
      exporters: [otlp_http/sluicio]
    metrics/mixed:
      receivers: [prometheus, otlp]
      exporters: [otlp_http/sluicio]
`

func TestDropMetricInScrapeJob(t *testing.T) {
	got, err := DropMetric(scrapeBase, "filter", "http_requests_total", SluicioExporter{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "metrics,metrics/files,metrics/mixed,metrics/shared"; strings.Join(got.Changed, ",") != want {
		t.Fatalf("changed %v, want %s", got.Changed, want)
	}
	if len(got.ScrapeJobs) != 2 || got.ScrapeJobs[0] != (ScrapeJob{"prometheus", "app"}) || got.ScrapeJobs[1] != (ScrapeJob{"prometheus", "db"}) {
		t.Fatalf("scrape jobs %v", got.ScrapeJobs)
	}

	var doc struct {
		Receivers map[string]struct {
			Config struct {
				ScrapeConfigs []struct {
					JobName string           `yaml:"job_name"`
					Rules   []map[string]any `yaml:"metric_relabel_configs"`
				} `yaml:"scrape_configs"`
			} `yaml:"config"`
		} `yaml:"receivers"`
		Service struct {
			Pipelines map[string]struct {
				Processors []string `yaml:"processors"`
			} `yaml:"pipelines"`
		} `yaml:"service"`
	}
	if err := yaml.Unmarshal([]byte(got.Config), &doc); err != nil {
		t.Fatal(err)
	}
	jobs := doc.Receivers["prometheus"].Config.ScrapeConfigs
	if len(jobs[0].Rules) != 1 || jobs[0].Rules[0]["regex"] != "http_requests_total" || jobs[0].Rules[0]["action"] != "drop" {
		t.Fatalf("app job rules: %v", jobs[0].Rules)
	}
	// Last, after the rename the job already has.
	if len(jobs[1].Rules) != 2 || jobs[1].Rules[1]["action"] != "drop" {
		t.Fatalf("db job rules: %v", jobs[1].Rules)
	}
	// prometheus/shared also feeds a pipeline to another backend, and
	// prometheus/files keeps its jobs elsewhere: neither is changed.
	if r := doc.Receivers["prometheus/shared"].Config.ScrapeConfigs[0].Rules; r != nil {
		t.Fatalf("shared receiver changed: %v", r)
	}
	procs := func(p string) string { return strings.Join(doc.Service.Pipelines[p].Processors, ",") }
	// Covered by the scrape jobs: no filter. The rest: the filter.
	if procs("metrics") != "" || procs("metrics/shared") != "filter/sluicio-advisor" || procs("metrics/files") != "filter/sluicio-advisor" || procs("metrics/mixed") != "filter/sluicio-advisor" {
		t.Fatalf("processors: %+v", doc.Service.Pipelines)
	}
	if !strings.Contains(got.Config, "new_$$1") {
		t.Fatalf("an escaped $ was lost:\n%s", got.Config)
	}

	// Again: nothing to change.
	same, err := DropMetric(got.Config, "filter", "http_requests_total", SluicioExporter{})
	if err != nil || !same.AlreadyDropped || same.Config != "" || len(same.ScrapeJobs) != 0 {
		t.Fatalf("again: %+v %v", same, err)
	}
}

func TestDropMetricOnlyInScrapeJobs(t *testing.T) {
	cfg := `receivers:
  prometheus:
    config:
      scrape_configs:
        - job_name: app
          static_configs:
            - targets: ["app:9100"]
exporters:
  otlp_http/sluicio:
    endpoint: https://ingest.example.test
service:
  pipelines:
    metrics:
      receivers: [prometheus]
      exporters: [otlp_http/sluicio]
`
	got, err := DropMetric(cfg, "filter", "go.gc.duration", SluicioExporter{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Config, "sluicio-advisor") {
		t.Fatalf("a filter processor was added although the scrape job drops it:\n%s", got.Config)
	}
	// The name is a regex there: the dot is escaped.
	if !strings.Contains(got.Config, `regex: 'go\.gc\.duration'`) {
		t.Fatalf("rule:\n%s", got.Config)
	}
	if d := strings.Count(got.Config, "\n") - strings.Count(cfg, "\n"); d != 4 {
		t.Fatalf("%d lines added, want 4:\n%s", d, got.Config)
	}
}
