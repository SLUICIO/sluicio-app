// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/open-telemetry/opamp-go/protobufs"
)

func testKeys(t *testing.T) *Keys {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	k, err := ParseKeys(base64.StdEncoding.EncodeToString(b))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// The capability sets below are what the two collector types report.
// The opampprobe test asserts them against real binaries; these pin the
// rule itself.
func TestDetectKind(t *testing.T) {
	const (
		status    = uint64(protobufs.AgentCapabilities_AgentCapabilities_ReportsStatus)
		remote    = uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig)
		effective = uint64(protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig)
		health    = uint64(protobufs.AgentCapabilities_AgentCapabilities_ReportsHealth)
		restart   = uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRestartCommand)
	)
	cases := []struct {
		name string
		caps uint64
		want Kind
	}{
		{"extension: reports only", status | effective | health, KindExtension},
		{"supervisor accepting remote config", status | remote | effective | health | restart, KindSupervisor},
		{"supervisor without accepts_remote_config is read only", status | effective | health | restart, KindExtension},
		{"nothing reported yet", 0, KindExtension},
	}
	for _, c := range cases {
		if got := DetectKind(c.caps); got != c.want {
			t.Errorf("%s: DetectKind(%b) = %s, want %s", c.name, c.caps, got, c.want)
		}
	}
	if got := CapabilityNames(status | remote); strings.Join(got, ",") != "reports_status,accepts_remote_config" {
		t.Errorf("CapabilityNames = %v", got)
	}
}

func TestSealNeverStoresPlaintext(t *testing.T) {
	k := testKeys(t)
	id := uuid.New()
	files := []ConfigFile{{Body: "exporters:\n  otlp_http:\n    headers:\n      authorization: Bearer hunter2-secret\n"}}
	hash, sealed, err := k.Seal(id, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"hunter2", "authorization", "otlp_http", "exporters"} {
		if strings.Contains(sealed, leak) || strings.Contains(hash, leak) {
			t.Fatalf("stored values leak %q", leak)
		}
	}
	got, err := k.Open(id, hash, sealed)
	if err != nil || len(got) != 1 || got[0].Body != files[0].Body {
		t.Fatalf("round trip: %v %v", got, err)
	}
}

func TestSealIsBoundToCollectorAndHash(t *testing.T) {
	k := testKeys(t)
	a, b := uuid.New(), uuid.New()
	hash, sealed, _ := k.Seal(a, []ConfigFile{{Body: "receivers: {}"}})
	if _, err := k.Open(b, hash, sealed); err == nil {
		t.Fatal("ciphertext copied to another collector must not open")
	}
	otherHash := k.Hash([]ConfigFile{{Body: "receivers: {otlp: {}}"}})
	if _, err := k.Open(a, otherHash, sealed); err == nil {
		t.Fatal("ciphertext paired with another hash must not open")
	}
	if _, err := k.Open(a, hash, `[{"body":"plain"}]`); err == nil {
		t.Fatal("a plaintext row must not be shown as a stored configuration")
	}
}

func TestOpenWithAnotherCellsKeyFails(t *testing.T) {
	id := uuid.New()
	hash, sealed, _ := testKeys(t).Seal(id, []ConfigFile{{Body: "a: 1"}})
	if _, err := testKeys(t).Open(id, hash, sealed); err == nil {
		t.Fatal("another key must not open the configuration")
	}
}

func TestHash(t *testing.T) {
	k := testKeys(t)
	one := []ConfigFile{{Name: "a", Body: "x: 1"}, {Name: "b", Body: "y: 2"}}
	swapped := []ConfigFile{one[1], one[0]}
	if k.Hash(one) != k.Hash(swapped) {
		t.Fatal("file order must not change the hash")
	}
	if k.Hash(one) == k.Hash([]ConfigFile{{Name: "a", Body: "x: 2"}, one[1]}) {
		t.Fatal("a changed body must change the hash")
	}
	if k.Hash(one) == testKeys(t).Hash(one) {
		t.Fatal("the hash must be keyed: another cell's key gives another hash")
	}
}

func TestParseKeysRejectsBadKey(t *testing.T) {
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := ParseKeys(bad); err == nil {
			t.Errorf("ParseKeys(%q) accepted", bad)
		}
	}
}

func TestMaskConfig(t *testing.T) {
	in := `receivers:
  otlp:
    protocols:
      http:
        endpoint: 0.0.0.0:4318
  prometheus:
    config:
      scrape_configs:
        - job_name: app
          basic_auth:
            username: scraper
            password: s3cr3t-pass
exporters:
  otlp_http/sluicio:
    endpoint: https://ingest.example.test
    headers:
      Authorization: Bearer sk-live-123
      x-api-key: abc-key-456
  otlp_http/env:
    headers:
      authorization: ${env:SLUICIO_TOKEN}
  otlp_http/url:
    endpoint: https://user:urlpass@collector.internal:4318
extensions:
  bearertokenauth:
    token: tok-789
  oauth2client:
    client_id: my-client
    client_secret: [part-one, part-two]
  opamp:
    server:
      ws:
        endpoint: wss://cell.example.test/api/v1/opamp
service:
  extensions: [opamp]
`
	out, ok := MaskConfig(in)
	if !ok {
		t.Fatal("valid YAML reported as unparseable")
	}
	for _, secret := range []string{"s3cr3t-pass", "sk-live-123", "abc-key-456", "urlpass", "tok-789", "part-one", "part-two"} {
		if strings.Contains(out, secret) {
			t.Errorf("masked output still contains %q:\n%s", secret, out)
		}
	}
	for _, kept := range []string{"0.0.0.0:4318", "scraper", "https://ingest.example.test", "${env:SLUICIO_TOKEN}", "my-client", "collector.internal:4318", "wss://cell.example.test/api/v1/opamp", "job_name: app"} {
		if !strings.Contains(out, kept) {
			t.Errorf("masked output lost %q:\n%s", kept, out)
		}
	}
}

// The collector reports headers as a list of name/value pairs; the
// secret is the value next to a sensitive name.
func TestMaskConfigNamedPairs(t *testing.T) {
	in := `exporters:
  otlp_http:
    headers:
      - name: authorization
        value: Bearer pair-secret
      - name: x-tenant
        value: acme
`
	out, ok := MaskConfig(in)
	if !ok || strings.Contains(out, "pair-secret") || !strings.Contains(out, "acme") || !strings.Contains(out, "authorization") {
		t.Fatalf("named pairs masked wrongly:\n%s", out)
	}
}

func TestMaskConfigFailsClosed(t *testing.T) {
	if out, ok := MaskConfig("key: [unterminated\n  password: x"); ok || out != "" {
		t.Fatalf("unparseable body must be hidden, got ok=%v %q", ok, out)
	}
}

func TestMaskText(t *testing.T) {
	got := MaskText(`Post "https://alice:pw@host:4318/v1/traces": dial tcp: refused`)
	if strings.Contains(got, "pw") || strings.Contains(got, "alice") || !strings.Contains(got, "host:4318") {
		t.Fatalf("MaskText = %q", got)
	}
}

// The test plan's masking matrix: every form a secret takes in a collector
// configuration, and the references that must stay readable.
func TestMaskMatrix(t *testing.T) {
	in := `extensions:
  basicauth/client:
    client_auth:
      username: ingest
      password: m-basic-pass
  oauth2client:
    client_id: app
    client_secret: m-client-secret
  bearertokenauth:
    token: m-bearer-token
exporters:
  otlp_http/headers:
    endpoint: https://gw.example.test:4318
    headers:
      x-api-key: m-header-key
  otlp_http/list:
    headers:
      - name: Authorization
        value: Bearer m-list-bearer
      - name: x-tenant
        value: acme
  otlp_http/query:
    endpoint: https://gw.example.test/v1?api_key=m-query-key&region=eu
  otlp_http/userinfo:
    endpoint: https://svc:m-url-pass@gw.example.test
  otlp_http/env:
    headers:
      authorization: ${env:SLUICIO_TOKEN}
    tls:
      key_pem: |
        -----BEGIN PRIVATE KEY-----
        m-pem-body
        -----END PRIVATE KEY-----
      cert_file: /certs/client.pem
receivers:
  sqlquery:
    datasource: postgres://reader:m-dsn-pass@db.internal:5432/app
  something:
    blob: |
      -----BEGIN RSA PRIVATE KEY-----
      m-unnamed-pem
      -----END RSA PRIVATE KEY-----
`
	out, ok := MaskConfig(in)
	if !ok {
		t.Fatal("valid YAML reported as unparseable")
	}
	for _, secret := range []string{"m-basic-pass", "m-client-secret", "m-bearer-token", "m-header-key", "m-list-bearer",
		"m-query-key", "m-url-pass", "m-pem-body", "m-dsn-pass", "m-unnamed-pem"} {
		if strings.Contains(out, secret) {
			t.Errorf("not masked: %s", secret)
		}
	}
	for _, kept := range []string{"username: ingest", "client_id: app", "${env:SLUICIO_TOKEN}", "region=eu", "x-tenant", "acme",
		"/certs/client.pem", "gw.example.test", "db.internal:5432"} {
		if !strings.Contains(out, kept) {
			t.Errorf("lost: %s", kept)
		}
	}
}

// A component ID is a name, not a secret: "otlp_http/secrets" or
// "bearertokenauth" must not mask every setting of the component.
func TestMaskKeepsComponentsWithSecretLookingIDs(t *testing.T) {
	in := `extensions:
  bearertokenauth:
    scheme: Bearer
    token: c-ext-token
exporters:
  otlp_http/secrets:
    endpoint: https://gw.example.test
    timeout: 30s
    headers:
      x-api-key: c-header-key
`
	out, ok := MaskConfig(in)
	if !ok {
		t.Fatal("unparseable")
	}
	for _, kept := range []string{"scheme: Bearer", "endpoint: https://gw.example.test", "timeout: 30s", "otlp_http/secrets", "bearertokenauth"} {
		if !strings.Contains(out, kept) {
			t.Errorf("lost %q:\n%s", kept, out)
		}
	}
	for _, secret := range []string{"c-ext-token", "c-header-key"} {
		if strings.Contains(out, secret) {
			t.Errorf("not masked: %s", secret)
		}
	}
}
