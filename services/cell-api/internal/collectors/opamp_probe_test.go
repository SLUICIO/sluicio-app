// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build opampprobe

// Real collectors against the cell's OpAMP endpoint and registry.
//
// Three pinned collectors connect over TLS, with the CA mounted and
// verified (no insecure_skip_verify):
//
//   - two parallel extension-type collectors in one team, delivering
//     different data (CPU and memory metrics) to an OTLP sink, one with a
//     configured instance_uid and a resource stamp, one with neither;
//   - a supervisor-type collector in another team, started from a local
//     base configuration.
//
// Then the OpAMP server is stopped: the running collectors must keep
// delivering data, and a supervisor started from scratch during the outage
// is observed. The server comes back on the same address and the agents
// must reconnect.
//
// Needs Podman or Docker (the Makefile's test-integration recipe shows the
// Podman socket variables) and pulls the pinned images:
//
//	go test -tags opampprobe -count=1 -v -timeout 15m ./services/cell-api/internal/collectors/ -run TestRealCollectors
package collectors_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/opamp"
)

// Pinned versions. OpAMP is Beta and the supervisor and extension are
// Alpha, so the probe runs against exact releases; opamp-go is pinned in
// go.mod (v0.25.0).
const (
	collectorImage  = "docker.io/otel/opentelemetry-collector-contrib:0.166.0"
	supervisorImage = "docker.io/otel/opentelemetry-collector-opampsupervisor:0.166.0"
)

// ── TLS: a CA and a server certificate for the name containers use ──────

func probeTLS(t *testing.T, dir string) *tls.Config {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "probe CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "host.containers.internal"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		DNSNames:    []string{"host.containers.internal", "host.docker.internal", "localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644); err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
}

// ── OTLP sink: stands in for cell-ingest, records who sent what ─────────

type batch struct {
	at       time.Time
	resource map[string]string
	metrics  []string
}

type sink struct {
	mu      sync.Mutex
	batches []batch
}

func (s *sink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		body = zr
	}
	raw, _ := io.ReadAll(body)
	if r.URL.Path == "/v1/metrics" {
		var req colmetrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(raw, &req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.mu.Lock()
		for _, rm := range req.ResourceMetrics {
			b := batch{at: time.Now(), resource: map[string]string{}}
			for _, kv := range rm.GetResource().GetAttributes() {
				b.resource[kv.Key] = kv.GetValue().GetStringValue()
			}
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					b.metrics = append(b.metrics, m.Name)
				}
			}
			s.batches = append(s.batches, b)
		}
		s.mu.Unlock()
	}
	out, _ := proto.Marshal(&colmetrics.ExportMetricsServiceResponse{})
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(out)
}

// count returns how many batches after since match.
func (s *sink) count(since time.Time, match func(batch) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.batches {
		if b.at.After(since) && match(b) {
			n++
		}
	}
	return n
}

func (s *sink) find(match func(batch) bool) (batch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.batches {
		if match(b) {
			return b, true
		}
	}
	return batch{}, false
}

func hasMetricPrefix(prefix string) func(batch) bool {
	return func(b batch) bool {
		for _, m := range b.metrics {
			if strings.HasPrefix(m, prefix) {
				return true
			}
		}
		return false
	}
}

// ── The OpAMP endpoint, stoppable and restartable on one address ────────

type endpoint struct {
	t    *testing.T
	addr string
	srv  *http.Server
	op   *opamp.Server
	tls  *tls.Config
}

func (e *endpoint) start() {
	e.t.Helper()
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ { // the port may linger briefly after a stop
		if ln, err = net.Listen("tcp", e.addr); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		e.t.Fatalf("listen %s: %v", e.addr, err)
	}
	e.addr = ln.Addr().String()
	mux := http.NewServeMux()
	mux.Handle(opamp.Path, e.op)
	e.srv = &http.Server{Handler: mux, ConnContext: e.op.ConnContext, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = e.srv.Serve(tls.NewListener(ln, e.tls)) }()
}

// stop takes the endpoint down hard: listener and every open WebSocket.
func (e *endpoint) stop() { _ = e.srv.Close() }

func (e *endpoint) port() string { _, p, _ := net.SplitHostPort(e.addr); return p }

// ── Containers ───────────────────────────────────────────────────────────

func startContainer(t *testing.T, ctx context.Context, name string, req testcontainers.ContainerRequest) testcontainers.Container {
	t.Helper()
	req.Name = name + "-" + uuid.NewString()[:8]
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	t.Cleanup(func() {
		if t.Failed() || testing.Verbose() {
			if rc, err := c.Logs(context.Background()); err == nil {
				out, _ := io.ReadAll(rc)
				lines := strings.Split(strings.TrimSpace(string(out)), "\n")
				if len(lines) > 25 {
					lines = lines[len(lines)-25:]
				}
				t.Logf("--- %s log tail ---\n%s", name, strings.Join(lines, "\n"))
			}
		}
		_ = testcontainers.TerminateContainer(c)
	})
	return c
}

func file(body, path string) testcontainers.ContainerFile {
	return testcontainers.ContainerFile{Reader: strings.NewReader(body), ContainerFilePath: path, FileMode: 0o644}
}

// extensionConfig is a collector with the OpAMP extension. stamp adds the
// configured instance UID and a resource processor that writes it onto
// every metric, the "stamp" the design document proposes.
func extensionConfig(opampPort, sinkPort, scraper string, stamp bool) string {
	uid, proc, procDef := "", "", ""
	if stamp {
		uid = "    instance_uid: ${env:INSTANCE_UID}\n"
		proc = "      processors: [resource/stamp]\n"
		procDef = "processors:\n  resource/stamp:\n    attributes:\n      - key: sluicio.collector.instance_uid\n        value: ${env:INSTANCE_UID}\n        action: insert\n"
	}
	return fmt.Sprintf(`extensions:
  opamp:
    server:
      ws:
        endpoint: wss://host.containers.internal:%[1]s/api/v1/opamp
        headers:
          Authorization: "Bearer ${env:SLUICIO_COLLECTOR_TOKEN}"
        tls:
          ca_file: /certs/ca.pem
%[4]s    reports_raw_config: true
    capabilities:
      reports_effective_config: true
      reports_health: true
receivers:
  host_metrics:
    collection_interval: 2s
    scrapers:
      %[3]s: {}
%[6]sexporters:
  otlp_http/sluicio:
    endpoint: http://host.containers.internal:%[2]s
    compression: gzip
    headers:
      x-probe-secret: literal-secret-in-config
service:
  extensions: [opamp]
  telemetry:
    metrics:
      readers:
        - periodic:
            interval: 3000
            exporter:
              otlp:
                protocol: http/protobuf
                endpoint: http://host.containers.internal:%[2]s/v1/metrics
  pipelines:
    metrics:
      receivers: [host_metrics]
%[5]s      exporters: [otlp_http/sluicio]
`, opampPort, sinkPort, scraper, uid, proc, procDef)
}

func supervisorConfig(opampPort, token string) string {
	return fmt.Sprintf(`server:
  endpoint: wss://host.containers.internal:%s/api/v1/opamp
  headers:
    Authorization: ["Bearer %s"]
  tls:
    ca_file: /certs/ca.pem
capabilities:
  accepts_remote_config: true
  reports_effective_config: true
  reports_health: true
agent:
  executable: /otelcol-contrib
  config_files: [/etc/otel/base.yaml]
storage:
  directory: /data
`, opampPort, token)
}

func baseConfig(sinkPort, scraper string) string {
	return fmt.Sprintf(`receivers:
  host_metrics:
    collection_interval: 2s
    scrapers:
      %[2]s: {}
exporters:
  otlp_http/sluicio:
    endpoint: http://host.containers.internal:%[1]s
service:
  pipelines:
    metrics:
      receivers: [host_metrics]
      exporters: [otlp_http/sluicio]
`, sinkPort, scraper)
}

// supervisorWithCollector is the published supervisor image plus the
// collector binary of the same release. The published supervisor image
// carries no collector (the Dockerfile in the contrib repository does, the
// released image does not), so a customer builds the same thing.
func supervisorWithCollector(t *testing.T) testcontainers.FromDockerfile {
	dir := t.TempDir()
	df := fmt.Sprintf("FROM %s AS col\nFROM %s\nCOPY --from=col /otelcol-contrib /otelcol-contrib\n", collectorImage, supervisorImage)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(df), 0o644); err != nil {
		t.Fatal(err)
	}
	return testcontainers.FromDockerfile{Context: dir, Dockerfile: "Dockerfile", Repo: "sluicio-probe-supervisor", Tag: "0.166.0", KeepImage: true}
}

func waitUntil(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}

func TestRealCollectors(t *testing.T) {
	pool, ctx := newDB(t)
	keys := newKeys(t)
	store := collectors.New(pool, keys)
	org := seedOrg(t, ctx, pool, "acme")
	teamA := seedTeam(t, ctx, pool, org, "platform")
	teamB := seedTeam(t, ctx, pool, org, "payments")
	tokenA, _, _ := store.CreateToken(ctx, org, teamA, "probe", nil)
	tokenB, _, _ := store.CreateToken(ctx, org, teamB, "probe", nil)
	all := collectors.Scope{OrgID: org, All: true}

	certDir := t.TempDir()
	tlsCfg := probeTLS(t, certDir)
	caPEM, _ := os.ReadFile(filepath.Join(certDir, "ca.pem"))

	op, err := opamp.New(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ep := &endpoint{t: t, addr: "127.0.0.1:0", op: op, tls: tlsCfg}
	ep.start()

	sk := &sink{}
	sinkLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sinkSrv := &http.Server{Handler: sk, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = sinkSrv.Serve(sinkLn) }()
	t.Cleanup(func() { _ = sinkSrv.Close() })
	_, sinkPort, _ := net.SplitHostPort(sinkLn.Addr().String())

	// A ContainerFile's reader is consumed by the first container that
	// copies it, so each container gets its own.
	certs := func() testcontainers.ContainerFile { return file(string(caPEM), "/certs/ca.pem") }
	uidA := uuid.NewString()

	// Two parallel extension-type collectors in one team: A (CPU, stamped,
	// configured instance_uid) and B (memory, no stamp, default uid).
	startContainer(t, ctx, "ext-a", testcontainers.ContainerRequest{
		Image: collectorImage,
		Env:   map[string]string{"SLUICIO_COLLECTOR_TOKEN": tokenA, "INSTANCE_UID": uidA},
		Files: []testcontainers.ContainerFile{certs(), file(extensionConfig(ep.port(), sinkPort, "cpu", true), "/etc/otelcol-contrib/config.yaml")},
	})
	startContainer(t, ctx, "ext-b", testcontainers.ContainerRequest{
		Image: collectorImage,
		Env:   map[string]string{"SLUICIO_COLLECTOR_TOKEN": tokenA},
		Files: []testcontainers.ContainerFile{certs(), file(extensionConfig(ep.port(), sinkPort, "memory", false), "/etc/otelcol-contrib/config.yaml")},
	})
	// A supervisor-type collector in the other team, from a base config.
	supImage := supervisorWithCollector(t)
	startContainer(t, ctx, "supervisor", testcontainers.ContainerRequest{
		FromDockerfile: supImage,
		Cmd:            []string{"--config", "/etc/otel/supervisor.yaml"},
		Tmpfs:          map[string]string{"/data": "rw,mode=1777"},
		Files: []testcontainers.ContainerFile{certs(),
			file(supervisorConfig(ep.port(), tokenB), "/etc/otel/supervisor.yaml"),
			file(baseConfig(sinkPort, "load"), "/etc/otel/base.yaml")},
	})

	// ── Phase 1: registration, type, stored configuration ──────────────
	var list []collectors.Collector
	waitUntil(t, "three collectors with a stored configuration", 3*time.Minute, func() bool {
		list, _ = store.List(ctx, all)
		withCfg := 0
		for _, c := range list {
			if c.ConfigHash != "" && len(c.Description) > 0 {
				withCfg++
			}
		}
		return withCfg == 3
	})
	var colA, colB, sup collectors.Collector
	for _, c := range list {
		t.Logf("registered: team=%s kind=%s uid=%s online=%v transport=%s caps=%v", c.GroupName, c.Kind, c.InstanceUID, c.Online, c.Transport, c.Capabilities)
		t.Logf("  description: %v", c.Description)
		switch {
		case c.GroupID == teamB:
			sup = c
		case c.InstanceUID == uidA:
			colA = c
		default:
			colB = c
		}
	}
	if colA.ID == uuid.Nil || colB.ID == uuid.Nil || sup.ID == uuid.Nil {
		t.Fatalf("could not tell the three collectors apart: %+v", list)
	}
	if colA.GroupID != teamA || colB.GroupID != teamA {
		t.Fatal("the extension collectors are not in the team whose token they used")
	}
	if colA.Kind != collectors.KindExtension || colB.Kind != collectors.KindExtension {
		t.Fatalf("extension collectors detected as %s / %s", colA.Kind, colB.Kind)
	}
	if sup.Kind != collectors.KindSupervisor {
		t.Fatalf("supervisor detected as %s (caps %v)", sup.Kind, sup.Capabilities)
	}
	for _, c := range []collectors.Collector{colA, colB, sup} {
		if !c.Online || c.Transport != "websocket" {
			t.Fatalf("%s: online=%v transport=%s", c.InstanceUID, c.Online, c.Transport)
		}
	}

	// The reported configuration: what the collector sends, and what is shown.
	for _, c := range []collectors.Collector{colA, colB, sup} {
		var hash, enc string
		if err := pool.QueryRow(ctx, `SELECT config_hash, config_enc FROM collector_configs WHERE collector_id = $1 ORDER BY received_at DESC LIMIT 1`, c.ID).Scan(&hash, &enc); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(enc, "host_metrics") || strings.Contains(enc, "literal-secret") {
			t.Fatal("configuration stored in the clear")
		}
		plain, err := keys.Open(c.ID, hash, enc)
		if err != nil {
			t.Fatal(err)
		}
		_, shown, _ := store.MaskedConfig(ctx, c.ID, nil)
		for i, f := range plain {
			reportsSecret := strings.Contains(f.Body, "literal-secret-in-config")
			reportsToken := strings.Contains(f.Body, tokenA) || strings.Contains(f.Body, tokenB)
			t.Logf("%s file %q (%s, %d bytes): literal header value reported in clear=%v, OpAMP token reported in clear=%v",
				c.Kind, f.Name, f.ContentType, len(f.Body), reportsSecret, reportsToken)
			if strings.Contains(shown[i].Body, "literal-secret-in-config") || strings.Contains(shown[i].Body, tokenA) || strings.Contains(shown[i].Body, tokenB) {
				t.Fatalf("masked configuration still shows a secret:\n%s", shown[i].Body)
			}
		}
		// The extension reports the configuration as written beside the
		// effective one, scrapers and all.
		if c.Kind == collectors.KindExtension {
			var raw string
			for _, f := range plain {
				if f.Name == collectors.RawConfigFile {
					raw = f.Body
				}
			}
			if !strings.Contains(raw, "scrapers") {
				t.Fatalf("%s: no raw configuration with its scrapers (files: %d)", c.InstanceUID, len(plain))
			}
		}
		// Each collector's history holds its own configuration: only A has
		// the stamp processor. (The scraper names are no use here: the
		// extension's effective config omits host_metrics' scrapers.)
		if len(plain) > 0 {
			stamped := strings.Contains(plain[0].Body, "resource/stamp")
			if (c.ID == colA.ID) != stamped {
				t.Fatalf("collector %s holds another collector's configuration:\n%s", c.InstanceUID, plain[0].Body)
			}
			t.Logf("%s reports host_metrics scrapers in its effective config: %v", c.InstanceUID, strings.Contains(plain[0].Body, "scrapers"))
		}
		if c.ID == sup.ID {
			t.Logf("supervisor effective config:\n%s", shown[0].Body)
		}
	}

	// ── The design document's case: two parallel collectors, different data ──
	waitUntil(t, "metrics from both extension collectors", time.Minute, func() bool {
		return sk.count(time.Time{}, hasMetricPrefix("system.cpu")) > 0 && sk.count(time.Time{}, hasMetricPrefix("system.memory")) > 0
	})
	stampedA := func(b batch) bool { return b.resource["sluicio.collector.instance_uid"] == uidA }
	if n := sk.count(time.Time{}, func(b batch) bool { return hasMetricPrefix("system.memory")(b) && stampedA(b) }); n != 0 {
		t.Fatal("collector B's data carries collector A's stamp")
	}
	if sk.count(time.Time{}, func(b batch) bool { return hasMetricPrefix("system.cpu")(b) && stampedA(b) }) == 0 {
		t.Fatal("collector A's data does not carry its stamp")
	}
	// Own telemetry: which resource identifies each collector, and does it
	// match what the agent reported over OpAMP?
	for _, c := range []collectors.Collector{colA, colB} {
		sid := c.Description["service.instance.id"]
		own, ok := sk.find(func(b batch) bool { return hasMetricPrefix("otelcol")(b) && b.resource["service.instance.id"] == sid })
		t.Logf("collector uid=%s: OpAMP instance_uid == service.instance.id? %v (service.instance.id=%s); own metrics found under that service.instance.id: %v",
			c.InstanceUID, c.InstanceUID == sid, sid, ok)
		if ok {
			t.Logf("  own-metric resource: %v", own.resource)
		}
	}

	// ── Phase 2: the OpAMP server goes away ──────────────────────────────
	ep.stop()
	down := time.Now()
	t.Logf("OpAMP server stopped at %s", down.Format(time.RFC3339))

	// A supervisor started from scratch while the server is unreachable:
	// what does it do before it has ever heard from the server?
	startContainer(t, ctx, "supervisor-fresh", testcontainers.ContainerRequest{
		FromDockerfile: supImage,
		Cmd:            []string{"--config", "/etc/otel/supervisor.yaml"},
		Tmpfs:          map[string]string{"/data": "rw,mode=1777"},
		Files: []testcontainers.ContainerFile{certs(),
			file(supervisorConfig(ep.port(), tokenB), "/etc/otel/supervisor.yaml"),
			file(baseConfig(sinkPort, "paging"), "/etc/otel/base.yaml")},
	})

	time.Sleep(25 * time.Second)
	a := sk.count(down.Add(2*time.Second), func(b batch) bool { return hasMetricPrefix("system.cpu")(b) && stampedA(b) })
	b := sk.count(down.Add(2*time.Second), hasMetricPrefix("system.memory"))
	s := sk.count(down.Add(2*time.Second), hasMetricPrefix("system.cpu.load_average"))
	fresh := sk.count(down, hasMetricPrefix("system.paging"))
	t.Logf("during the outage: batches from A=%d B=%d supervisor=%d; fresh supervisor's collector=%d", a, b, s, fresh)
	if a == 0 || b == 0 || s == 0 {
		t.Fatalf("a collector stopped delivering data while the OpAMP server was down (A=%d B=%d supervisor=%d)", a, b, s)
	}

	// ── Phase 3: the server comes back on the same address ───────────────
	ep.start()
	up := time.Now()
	waitUntil(t, "the three collectors back online", 3*time.Minute, func() bool {
		n := 0
		for _, id := range []uuid.UUID{colA.ID, colB.ID, sup.ID} {
			if c, err := store.Get(ctx, all, id); err == nil && c.Online && c.LastSeenAt.After(up) {
				n++
			}
		}
		return n == 3
	})
	t.Logf("all three reconnected within %s", time.Since(up).Round(time.Second))
	waitUntil(t, "the fresh supervisor to register", 2*time.Minute, func() bool {
		l, _ := store.List(ctx, collectors.Scope{OrgID: org, Groups: []uuid.UUID{teamB}})
		return len(l) == 2
	})
	t.Logf("fresh supervisor registered %s after the server came back; its collector's batches before then: %d",
		time.Since(up).Round(time.Second), fresh)

	// Reconnecting must not fork the registry: same rows, history unchanged
	// unless the configuration changed.
	after, _ := store.List(ctx, all)
	if len(after) != 4 {
		t.Fatalf("collectors after reconnect = %d, want 4 (3 + the fresh supervisor)", len(after))
	}
	for _, c := range []collectors.Collector{colA, colB, sup} {
		h, _ := store.History(ctx, c.ID, 10)
		t.Logf("history of %s (%s): %d version(s)", c.InstanceUID, c.Kind, len(h))
		// What changed between versions, line by line, so a version that
		// differs only by noise is visible as such.
		for i := 0; i+1 < len(h); i++ {
			_, newer, _ := store.MaskedConfig(ctx, c.ID, &h[i].ID)
			_, older, _ := store.MaskedConfig(ctx, c.ID, &h[i+1].ID)
			if len(newer) == 0 || len(older) == 0 {
				continue
			}
			was := map[string]bool{}
			for _, l := range strings.Split(older[0].Body, "\n") {
				was[l] = true
			}
			now := map[string]bool{}
			for _, l := range strings.Split(newer[0].Body, "\n") {
				now[l] = true
				if !was[l] {
					t.Logf("  + %s", l)
				}
			}
			for l := range was {
				if !now[l] {
					t.Logf("  - %s", l)
				}
			}
		}
	}
	var buf bytes.Buffer
	for _, c := range after {
		fmt.Fprintf(&buf, "%s %s %s online=%v\n", c.GroupName, c.Kind, c.InstanceUID, c.Online)
	}
	t.Logf("final registry:\n%s", buf.String())
}

// Stage 2 against a real supervisor 0.166.0: a configuration edited on its
// masked form is sent, applied, and the collector delivers what the new
// configuration says; a configuration the collector cannot run is
// reported failed, with the reason, and the collector keeps delivering.
func TestRealSupervisorPush(t *testing.T) {
	pool, ctx := newDB(t)
	keys := newKeys(t)
	store := collectors.New(pool, keys)
	org := seedOrg(t, ctx, pool, "acme")
	team := seedTeam(t, ctx, pool, org, "platform")
	token, _, _ := store.CreateToken(ctx, org, team, "probe", nil)

	certDir := t.TempDir()
	tlsCfg := probeTLS(t, certDir)
	caPEM, _ := os.ReadFile(filepath.Join(certDir, "ca.pem"))
	op, err := opamp.New(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ep := &endpoint{t: t, addr: "127.0.0.1:0", op: op, tls: tlsCfg}
	ep.start()
	sk := &sink{}
	sinkLn, _ := net.Listen("tcp", "127.0.0.1:0")
	sinkSrv := &http.Server{Handler: sk, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = sinkSrv.Serve(sinkLn) }()
	t.Cleanup(func() { _ = sinkSrv.Close() })
	_, sinkPort, _ := net.SplitHostPort(sinkLn.Addr().String())

	base := strings.Replace(baseConfig(sinkPort, "load"), "service:\n", "extensions:\n  opamp:\n    reports_raw_config: true\nservice:\n", 1)
	startContainer(t, ctx, "supervisor-push", testcontainers.ContainerRequest{
		FromDockerfile: supervisorWithCollector(t),
		Cmd:            []string{"--config", "/etc/otel/supervisor.yaml"},
		Tmpfs:          map[string]string{"/data": "rw,mode=1777"},
		Files: []testcontainers.ContainerFile{
			file(string(caPEM), "/certs/ca.pem"),
			// The recommended guards for a pushed configuration: check it
			// before restarting the collector, and go back to the last
			// working one if it still does not start.
			file(strings.Replace(supervisorConfig(ep.port(), token), "agent:\n", "agent:\n  validate_config: true\n  automatic_config_rollback: true\n", 1), "/etc/otel/supervisor.yaml"),
			file(base, "/etc/otel/base.yaml"),
		},
	})

	all := collectors.Scope{OrgID: org, All: true}
	var sup collectors.Collector
	waitUntil(t, "the supervisor with its configuration as written", 3*time.Minute, func() bool {
		l, _ := store.List(ctx, all)
		if len(l) != 1 || l[0].ConfigHash == "" {
			return false
		}
		sup = l[0]
		raw, err := store.LatestRaw(ctx, sup.ID)
		return err == nil && strings.Contains(raw, "load")
	})
	if sup.Kind != collectors.KindSupervisor {
		t.Fatalf("kind %s", sup.Kind)
	}

	send := func(edit func(string) string) collectors.Deployment {
		t.Helper()
		raw, _ := store.LatestRaw(ctx, sup.ID)
		masked, _ := collectors.MaskConfig(raw)
		restored, err := collectors.RestoreSecrets(edit(masked), raw, "")
		if err != nil {
			t.Fatal(err)
		}
		payload, err := collectors.PrepareRemote(restored)
		if err != nil {
			t.Fatal(err)
		}
		d, err := store.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: sup.ID, Body: payload, Via: "probe"})
		if err != nil {
			t.Fatal(err)
		}
		if !op.Deliver(ctx, sup.ID) {
			t.Log("not delivered at once; waiting for the next message")
		}
		return d
	}
	status := func(id uuid.UUID) collectors.Deployment {
		l, _ := store.Deployments(ctx, sup.ID, 10)
		for _, d := range l {
			if d.ID == id {
				return d
			}
		}
		return collectors.Deployment{}
	}

	// 1. A change: CPU metrics instead of load.
	good := send(func(m string) string { return strings.Replace(m, "load: {}", "cpu: {}", 1) })
	waitUntil(t, "the change applied", 2*time.Minute, func() bool { return status(good.ID).Status == collectors.DeploymentApplied })
	applied := time.Now()
	waitUntil(t, "CPU metrics from the new configuration", time.Minute, func() bool {
		return sk.count(applied, hasMetricPrefix("system.cpu")) > 0
	})
	raw, _ := store.LatestRaw(ctx, sup.ID)
	t.Logf("applied in %s; raw now has cpu=%v load=%v", status(good.ID).FinishedAt.Sub(good.CreatedAt).Round(time.Millisecond),
		strings.Contains(raw, "cpu: {}"), strings.Contains(raw, "load: {}"))

	// 2. A configuration the collector cannot run.
	bad := send(func(m string) string {
		return strings.Replace(m, "receivers:\n", "receivers:\n  no_such_receiver: {}\n", 1)
	})
	waitUntil(t, "the bad configuration reported", 2*time.Minute, func() bool {
		s := status(bad.ID).Status
		return s == collectors.DeploymentFailed || s == collectors.DeploymentApplied
	})
	got := status(bad.ID)
	t.Logf("bad configuration: status=%s error=%q", got.Status, got.ErrorMessage)
	if got.Status != collectors.DeploymentFailed || got.ErrorMessage == "" {
		t.Fatalf("a configuration the collector cannot run was reported %s (%q)", got.Status, got.ErrorMessage)
	}
	after := time.Now()
	waitUntil(t, "the collector still delivering after the failed change", time.Minute, func() bool {
		return sk.count(after, hasMetricPrefix("system.cpu")) > 0
	})
}
