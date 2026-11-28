// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"strings"
	"testing"
)

func TestPlainSecrets(t *testing.T) {
	cfg := `extensions:
  basicauth/out:
    client_auth:
      username: sluicio
      password: hunter2
  bearertokenauth:
    token: ${env:SLUICIO_TOKEN}
  basicauth/in:
    htpasswd:
      inline: "[REDACTED]"
exporters:
  otlp_http/sluicio:
    endpoint: https://ingest.example.test
    headers:
      Authorization: Bearer ${env:SLUICIO_TOKEN}
      x-api-key: abc123
  otlp_http/secrets:
    endpoint: https://user:pw@backend.example.test/v1
    auth:
      authenticator: basicauth/out
  prometheus_remote_write:
    endpoint: https://prom.example.test/write?api_key=k1
processors:
  resource:
    attributes:
      - key: service.name
        value: checkout
        action: insert
      - key: authorization
        value: literal
        action: upsert
receivers:
  otlp:
    protocols:
      grpc:
        tls:
          key_file: /etc/tls/key.pem
service:
  pipelines: {}
`
	got := strings.Join(PlainSecrets(cfg), "\n")
	want := strings.Join([]string{
		"extensions.basicauth/out.client_auth.password",
		"exporters.otlp_http/sluicio.headers.x-api-key",
		"exporters.otlp_http/secrets.endpoint",
		"exporters.prometheus_remote_write.endpoint",
		"processors.resource.attributes[1].value",
	}, "\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// References, an attribute named key, a component id that says
	// "secrets" and a key file are not secrets in plain text.
	// Nor is what the collector reports in place of an opaque value.
	for _, no := range []string{"bearertokenauth.token", "Authorization", "service.name", "key_file", "htpasswd", "authenticator"} {
		if strings.Contains(got, no) {
			t.Fatalf("%s reported", no)
		}
	}
	if PlainSecrets("a: [") != nil || PlainSecrets("receivers: {}\n") != nil {
		t.Fatal("a configuration without secrets reported some")
	}
}
