// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const adviseBase = `receivers:
  host_metrics:
    scrapers:
      load: {}
processors:
  memory_limiter:
    check_interval: 1s
    limit_percentage: 80
  batch: {}
exporters:
  otlp_http/sluicio:
    endpoint: https://ingest.example.test
  prometheus_remote_write:
    endpoint: https://prom.internal/api/v1/write
  otlp/gw:
    endpoint: cell.example.test:4317
service:
  pipelines:
    metrics:
      receivers: [host_metrics]
      processors: [memory_limiter, batch]
      exporters: [otlp_http/sluicio]
    metrics/both:
      receivers: [host_metrics]
      exporters: [otlp_http/sluicio, prometheus_remote_write]
    metrics/prom:
      receivers: [host_metrics]
      exporters: [prometheus_remote_write]
    metrics/by-host:
      receivers: [host_metrics]
      exporters: [otlp/gw]
    traces:
      receivers: [host_metrics]
      exporters: [otlp_http/sluicio]
`

func TestDropMetric(t *testing.T) {
	sl := SluicioExporter{IngestHosts: []string{"cell.example.test"}}
	got, err := DropMetric(adviseBase, "filter", "system.cpu.load_average.15m", sl)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"metrics", "metrics/by-host"}; strings.Join(got.Changed, ",") != strings.Join(want, ",") {
		t.Fatalf("changed %v, want %v", got.Changed, want)
	}
	reasons := map[string]string{}
	for _, s := range got.Skipped {
		reasons[s.Pipeline] = s.Reason
	}
	if !strings.Contains(reasons["metrics/both"], "prometheus_remote_write") || reasons["metrics/prom"] != "does not send to Sluicio" {
		t.Fatalf("skipped: %v", got.Skipped)
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(got.Config), &doc); err != nil {
		t.Fatal(err)
	}
	pipes := doc["service"].(map[string]any)["pipelines"].(map[string]any)
	procs := func(p string) []any {
		v, _ := pipes[p].(map[string]any)["processors"].([]any)
		return v
	}
	if p := procs("metrics"); len(p) != 3 || p[0] != "memory_limiter" || p[1] != "filter/sluicio-advisor" {
		t.Fatalf("metrics processors: %v (memory_limiter stays first)", p)
	}
	if p := procs("metrics/by-host"); len(p) != 1 || p[0] != "filter/sluicio-advisor" {
		t.Fatalf("by-host processors: %v", p)
	}
	if procs("metrics/both") != nil || procs("traces") != nil {
		t.Fatal("a pipeline that also exports elsewhere, or a traces pipeline, was changed")
	}
	if !strings.Contains(got.Config, `- 'name == "system.cpu.load_average.15m"'`) || !strings.Contains(got.Config, "error_mode: ignore") {
		t.Fatalf("processor:\n%s", got.Config)
	}

	// A second metric joins the same processor; the pipelines are not
	// touched twice.
	again, err := DropMetric(got.Config, "filter", "system.cpu.load_average.5m", sl)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(again.Config, "filter/sluicio-advisor:") != 1 || strings.Count(again.Config, "name ==") != 2 {
		t.Fatalf("second metric:\n%s", again.Config)
	}
	if strings.Count(again.Config, "- filter/sluicio-advisor")+strings.Count(again.Config, ", filter/sluicio-advisor")+strings.Count(again.Config, "[filter/sluicio-advisor") > 2 {
		t.Fatalf("processor listed twice in a pipeline:\n%s", again.Config)
	}

	// The same metric again changes nothing.
	same, err := DropMetric(got.Config, "filter", "system.cpu.load_average.15m", sl)
	if err != nil || !same.AlreadyDropped || same.Config != "" {
		t.Fatalf("same metric again: %+v %v", same, err)
	}
}

func TestDropMetricRecognisesSluicioByNameOnly(t *testing.T) {
	got, err := DropMetric(adviseBase, "filter", "m", SluicioExporter{})
	if err != nil {
		t.Fatal(err)
	}
	// Without the cell's ingest host, otlp/gw is not known to be Sluicio.
	if strings.Join(got.Changed, ",") != "metrics" {
		t.Fatalf("changed %v", got.Changed)
	}
}

func TestDropMetricNothingToChange(t *testing.T) {
	cfg := "receivers: {otlp: {}}\nexporters: {debug: {}}\nservice:\n  pipelines:\n    metrics:\n      receivers: [otlp]\n      exporters: [debug]\n"
	got, err := DropMetric(cfg, "filter", "m", SluicioExporter{})
	if err != nil || len(got.Changed) != 0 || got.Config != "" || len(got.Skipped) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := DropMetric("a: [", "filter", "m", SluicioExporter{}); err == nil {
		t.Fatal("broken YAML accepted")
	}
}

func TestDropMetricKeepsMaskedValues(t *testing.T) {
	masked, _ := MaskConfig(adviseBase + "extensions:\n  basicauth:\n    client_auth:\n      password: hunter2\n")
	got, err := DropMetric(masked, "filter", "m", SluicioExporter{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Config, "hunter2") || !strings.Contains(got.Config, Masked) {
		t.Fatalf("masking lost:\n%s", got.Config)
	}
	// Only the advisor's lines differ from the input.
	if d := strings.Count(got.Config, "\n") - strings.Count(masked, "\n"); d != 5 {
		t.Fatalf("%d lines added, want 5 (the processor block; the pipeline entry goes in the existing line):\n%s", d, got.Config)
	}
}

func TestDropLogsBelow(t *testing.T) {
	cfg := `receivers:
  otlp:
    protocols:
      http: {}
exporters:
  otlp_http/sluicio:
    endpoint: https://ingest.example.test
  otlp/siem:
    endpoint: siem.internal:4317
service:
  pipelines:
    logs:
      receivers: [otlp]
      exporters: [otlp_http/sluicio]
    logs/siem:
      receivers: [otlp]
      exporters: [otlp/siem]
    metrics:
      receivers: [otlp]
      exporters: [otlp_http/sluicio]
`
	got, err := DropLogsBelow(cfg, "filter", `orders-"api"`, "SEVERITY_NUMBER_WARN", SluicioExporter{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Changed, ",") != "logs" || len(got.Skipped) != 1 || got.Skipped[0].Pipeline != "logs/siem" {
		t.Fatalf("changed %v skipped %v", got.Changed, got.Skipped)
	}
	want := `- 'resource.attributes["service.name"] == "orders-\"api\"" and severity_number > SEVERITY_NUMBER_UNSPECIFIED and severity_number < SEVERITY_NUMBER_WARN'`
	if !strings.Contains(got.Config, want) || !strings.Contains(got.Config, "    logs:\n      log_record:") {
		t.Fatalf("condition:\n%s", got.Config)
	}
	if strings.Contains(got.Config, "processors: [filter/sluicio-advisor]\n      exporters: [otlp/siem]") {
		t.Fatal("the SIEM pipeline would lose the logs too")
	}

	// A metric drop on the same collector shares the processor.
	both, err := DropMetric(got.Config, "filter", "m", SluicioExporter{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(both.Config, "filter/sluicio-advisor:") != 1 || !strings.Contains(both.Config, "      metric:\n") || !strings.Contains(both.Config, "      log_record:\n") {
		t.Fatalf("one processor for both:\n%s", both.Config)
	}
}
