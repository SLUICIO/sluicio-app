// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const storedWithSecrets = `exporters:
  otlp_http/sluicio:
    endpoint: https://svc:url-pass@gw.example.test/v1?api_key=q-key&region=eu
    headers:
      authorization: Bearer real-token
      x-tenant: acme
receivers:
  host_metrics:
    scrapers:
      load: {}
`

func TestRestoreSecretsPutsStoredValuesBack(t *testing.T) {
	masked, _ := MaskConfig(storedWithSecrets)
	// The user edits the masked text: a scraper added, secrets untouched.
	edited := strings.Replace(masked, "      load: {}\n", "      load: {}\n      memory: {}\n", 1)
	got, err := RestoreSecrets(edited, storedWithSecrets)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Bearer real-token", "svc:url-pass@", "api_key=q-key", "memory: {}", "x-tenant: acme"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, Masked) {
		t.Errorf("a masked value survived:\n%s", got)
	}
}

func TestRestoreSecretsRefusesWhatItCannotTrace(t *testing.T) {
	masked, _ := MaskConfig(storedWithSecrets)
	cases := map[string]string{
		// A masked value moved to a new place.
		"moved": strings.Replace(masked, "x-tenant: acme", "x-other: '"+Masked+"'", 1),
		// Half of a masked URL edited.
		"partial": strings.Replace(masked, "gw.example.test", "gw2.example.test", 1),
	}
	for name, edited := range cases {
		if _, err := RestoreSecrets(edited, storedWithSecrets); !errors.As(err, &ErrMaskedValue{}) {
			t.Errorf("%s: err = %v, want ErrMaskedValue", name, err)
		}
	}
	// A secret typed in full replaces the stored one.
	typed := strings.Replace(masked, "authorization: '"+Masked+"'", "authorization: Bearer new-token", 1)
	got, err := RestoreSecrets(typed, storedWithSecrets)
	if err != nil || !strings.Contains(got, "Bearer new-token") || strings.Contains(got, "real-token") {
		t.Fatalf("typed secret: %v\n%s", err, got)
	}
}

func TestRestoreSecretsFollowsListItemsByName(t *testing.T) {
	stored := "exporters:\n  e:\n    headers:\n      - name: authorization\n        value: Bearer s1\n      - name: x-tenant\n        value: acme\n"
	masked, _ := MaskConfig(stored)
	// Reordered list: the secret follows its name, not its position.
	reordered := "exporters:\n  e:\n    headers:\n      - name: x-tenant\n        value: acme\n      - name: authorization\n        value: '" + Masked + "'\n"
	_ = masked
	got, err := RestoreSecrets(reordered, stored)
	if err != nil || !strings.Contains(got, "Bearer s1") {
		t.Fatalf("reordered: %v\n%s", err, got)
	}
}

func TestPrepareRemoteKeepsTheSupervisorsLink(t *testing.T) {
	raw := `extensions:
  opamp:
    server:
      ws:
        endpoint: ws://127.0.0.1/v1/opamp
  health_check: {}
receivers:
  host_metrics: {}
service:
  extensions: [health_check]
  pipelines:
    metrics:
      receivers: [host_metrics]
`
	got, err := PrepareRemote(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "ws://127.0.0.1") {
		t.Errorf("the supervisor's own extension settings were sent:\n%s", got)
	}
	var parsed struct {
		Extensions map[string]any `yaml:"extensions"`
		Service    struct {
			Extensions []string `yaml:"extensions"`
		} `yaml:"service"`
	}
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed.Extensions["health_check"]; !ok || strings.Join(parsed.Service.Extensions, ",") != "health_check,opamp" {
		t.Errorf("extensions list or other extensions lost:\n%s", got)
	}
	if _, err := PrepareRemote("- just\n- a list\n"); err == nil {
		t.Error("a non-mapping configuration was accepted")
	}
}
