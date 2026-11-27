// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package opamp

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/open-telemetry/opamp-go/client"
	clienttypes "github.com/open-telemetry/opamp-go/client/types"
	"github.com/open-telemetry/opamp-go/protobufs"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
)

// fakeRegistry stands in for Postgres: two teams, one token each.
type fakeRegistry struct {
	mu           sync.Mutex
	tokens       map[string]collectors.TokenRef
	observations []collectors.Observation
	configs      map[uuid.UUID][]collectors.ConfigFile
	ids          map[string]uuid.UUID
	disconnected []string
	// One waiting deployment for any collector, and the statuses reported.
	pending  *collectors.Deployment
	body     string
	statuses []collectors.DeploymentStatus
	sent     int
}

func (f *fakeRegistry) NextToDeliver(context.Context, uuid.UUID) (*collectors.Deployment, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending == nil {
		return nil, "", nil
	}
	d := *f.pending
	return &d, f.body, nil
}

func (f *fakeRegistry) MarkSent(context.Context, uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent++
	if f.pending != nil {
		f.pending.Status = collectors.DeploymentSent
	}
	return nil
}

func (f *fakeRegistry) ReportStatus(_ context.Context, _ uuid.UUID, hash string, status collectors.DeploymentStatus, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending != nil && hash == f.pending.Hash {
		f.statuses = append(f.statuses, status)
		if status == collectors.DeploymentApplied || status == collectors.DeploymentFailed {
			f.pending = nil
		}
	}
	return nil
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{tokens: map[string]collectors.TokenRef{}, configs: map[uuid.UUID][]collectors.ConfigFile{}, ids: map[string]uuid.UUID{}}
}

func (f *fakeRegistry) Authenticate(_ context.Context, presented string) (collectors.TokenRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ref, ok := f.tokens[presented]; ok {
		return ref, nil
	}
	return collectors.TokenRef{}, collectors.ErrUnauthenticated
}

func (f *fakeRegistry) revoke(token string) {
	f.mu.Lock()
	delete(f.tokens, token)
	f.mu.Unlock()
}

func (f *fakeRegistry) TouchToken(context.Context, uuid.UUID) error { return nil }

func (f *fakeRegistry) Observe(_ context.Context, o collectors.Observation) (uuid.UUID, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observations = append(f.observations, o)
	// Keyed by team and instance, as the unique constraint is.
	key := o.GroupID.String() + "/" + o.InstanceUID
	id, ok := f.ids[key]
	if !ok {
		id = uuid.New()
		f.ids[key] = id
	}
	return id, !ok, nil
}

func (f *fakeRegistry) RecordConfig(_ context.Context, id uuid.UUID, files []collectors.ConfigFile) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configs[id] = files
	return true, nil
}

func (f *fakeRegistry) Disconnected(_ context.Context, _ uuid.UUID, uid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnected = append(f.disconnected, uid)
	return nil
}

func (f *fakeRegistry) snapshot() ([]collectors.Observation, map[string]uuid.UUID, map[uuid.UUID][]collectors.ConfigFile, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cfg := map[uuid.UUID][]collectors.ConfigFile{}
	for k, v := range f.configs {
		cfg[k] = v
	}
	ids := map[string]uuid.UUID{}
	for k, v := range f.ids {
		ids[k] = v
	}
	return append([]collectors.Observation(nil), f.observations...), ids, cfg, append([]string(nil), f.disconnected...)
}

// startServer serves the endpoint over TLS, as every deployment does.
func startServer(t *testing.T, reg Registry) (*Server, string) {
	t.Helper()
	s, err := New(reg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(Path, s)
	ts := httptest.NewUnstartedServer(mux)
	ts.Config.ConnContext = s.ConnContext
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return s, ts.URL
}

type agent struct {
	c         client.OpAMPClient
	connected chan struct{}
	failed    chan error
}

const agentCaps = protobufs.AgentCapabilities_AgentCapabilities_ReportsStatus |
	protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig |
	protobufs.AgentCapabilities_AgentCapabilities_ReportsHealth

func startAgentCaps(t *testing.T, url, token string, caps protobufs.AgentCapabilities) *agent {
	t.Helper()
	return startAgentWith(t, url, token, clienttypes.InstanceUid(uuid.New()), true, "a: 1", caps)
}

func startAgent(t *testing.T, url, token string, uid clienttypes.InstanceUid, ws bool, config string) *agent {
	t.Helper()
	return startAgentWith(t, url, token, uid, ws, config, agentCaps)
}

func startAgentWith(t *testing.T, url, token string, uid clienttypes.InstanceUid, ws bool, config string, caps protobufs.AgentCapabilities) *agent {
	t.Helper()
	a := &agent{connected: make(chan struct{}, 8), failed: make(chan error, 64)}
	if ws {
		a.c = client.NewWebSocket(nil)
		url = strings.Replace(url, "https://", "wss://", 1)
	} else {
		hc := client.NewHTTP(nil)
		hc.SetPollingInterval(100 * time.Millisecond)
		a.c = hc
	}
	_ = a.c.SetAgentDescription(&protobufs.AgentDescription{
		IdentifyingAttributes: []*protobufs.KeyValue{
			{Key: "service.name", Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: "otelcol-contrib"}}},
		},
		NonIdentifyingAttributes: []*protobufs.KeyValue{
			{Key: "host.name", Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: "host-a"}}},
		},
	})
	_ = a.c.SetHealth(&protobufs.ComponentHealth{Healthy: true, LastError: "Post https://u:p@x/v1: refused"})
	err := a.c.Start(context.Background(), clienttypes.StartSettings{
		OpAMPServerURL: url + Path,
		Header:         http.Header{"Authorization": []string{"Bearer " + token}},
		TLSConfig:      &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // httptest's self-signed certificate
		InstanceUid:    uid,
		Capabilities:   caps,
		Callbacks: clienttypes.Callbacks{
			OnConnect:       func(context.Context) { a.connected <- struct{}{} },
			OnConnectFailed: func(_ context.Context, err error) { a.failed <- err },
			GetEffectiveConfig: func(context.Context) (*protobufs.EffectiveConfig, error) {
				return &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigObject{
					"": {Body: []byte(config), ContentType: "text/yaml"},
				}}}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.c.Stop(context.Background()) })
	return a
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestTokenAssignsTeam(t *testing.T) {
	reg := newFakeRegistry()
	teamA, teamB := uuid.New(), uuid.New()
	reg.tokens["sct_a"] = collectors.TokenRef{TokenID: uuid.New(), OrgID: uuid.New(), GroupID: teamA}
	reg.tokens["sct_b"] = collectors.TokenRef{TokenID: uuid.New(), OrgID: uuid.New(), GroupID: teamB}
	_, url := startServer(t, reg)

	// The same instance UID under two teams' tokens: two agents, one per
	// team, each with its own configuration.
	uid := clienttypes.InstanceUid(uuid.New())
	startAgent(t, url, "sct_a", uid, true, "receivers: {otlp: {}}")
	startAgent(t, url, "sct_b", uid, false, "receivers: {hostmetrics: {}}")

	keyA, keyB := teamA.String()+"/"+uuid.UUID(uid).String(), teamB.String()+"/"+uuid.UUID(uid).String()
	waitFor(t, "both agents' configurations", func() bool {
		_, ids, cfg, _ := reg.snapshot()
		return len(cfg[ids[keyA]]) == 1 && len(cfg[ids[keyB]]) == 1
	})
	obs, ids, cfg, _ := reg.snapshot()
	if ids[keyA] == ids[keyB] {
		t.Fatal("the same instance UID under two teams must be two collectors")
	}
	if !strings.Contains(cfg[ids[keyA]][0].Body, "otlp") || !strings.Contains(cfg[ids[keyB]][0].Body, "hostmetrics") {
		t.Fatalf("configurations crossed teams: %+v", cfg)
	}
	var sawWS, sawHTTP bool
	for _, o := range obs {
		if o.GroupID != teamA && o.GroupID != teamB {
			t.Fatalf("observation in a team no token names: %v", o.GroupID)
		}
		if o.GroupID == teamA && o.Transport == "websocket" {
			sawWS = true
		}
		if o.GroupID == teamB && o.Transport == "http" {
			sawHTTP = true
		}
		if o.Health != nil && strings.Contains(o.Health.LastError, "u:p@") {
			t.Fatalf("health error stored with credentials: %q", o.Health.LastError)
		}
		if o.Description != nil && o.Description["host.name"] != "host-a" {
			t.Fatalf("description = %v", o.Description)
		}
		if o.Capabilities != nil && collectors.DetectKind(*o.Capabilities) != collectors.KindExtension {
			t.Fatal("an agent without accepts_remote_config must read as the extension type")
		}
	}
	if !sawWS || !sawHTTP {
		t.Fatalf("transports: websocket=%v http=%v", sawWS, sawHTTP)
	}
}

func TestRejectsMissingAndUnknownTokens(t *testing.T) {
	reg := newFakeRegistry()
	_, url := startServer(t, reg)
	for _, token := range []string{"", "sct_unknown", "con_pat_not-a-collector-token"} {
		for _, ws := range []bool{true, false} {
			a := startAgent(t, url, token, clienttypes.InstanceUid(uuid.New()), ws, "a: 1")
			// The WebSocket client reports the refused upgrade; the HTTP
			// client only logs a 401, so for it "never connected" is the
			// observable outcome.
			wait := time.After(1500 * time.Millisecond)
			if ws {
				wait = time.After(10 * time.Second)
			}
			select {
			case <-a.failed:
			case <-a.connected:
				t.Fatalf("token %q (ws=%v) was accepted", token, ws)
			case <-wait:
				if ws {
					t.Fatalf("token %q: no failure reported", token)
				}
			}
		}
	}
	// Plain request: 401, not a hint about why.
	resp, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}). //nolint:gosec // test certificate
																Post(url+Path, "application/x-protobuf", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if obs, _, _, _ := reg.snapshot(); len(obs) != 0 {
		t.Fatalf("an unauthenticated agent was registered: %+v", obs)
	}
}

func TestRevokeDisconnectsAndKeepsOut(t *testing.T) {
	reg := newFakeRegistry()
	tokenID := uuid.New()
	reg.tokens["sct_a"] = collectors.TokenRef{TokenID: tokenID, OrgID: uuid.New(), GroupID: uuid.New()}
	s, url := startServer(t, reg)
	uid := clienttypes.InstanceUid(uuid.New())
	a := startAgent(t, url, "sct_a", uid, true, "a: 1")
	<-a.connected
	waitFor(t, "registration", func() bool { o, _, _, _ := reg.snapshot(); return len(o) > 0 })

	reg.revoke("sct_a")
	if n := s.DisconnectToken(tokenID); n != 1 {
		t.Fatalf("DisconnectToken closed %d connections, want 1", n)
	}
	waitFor(t, "marked offline", func() bool { _, _, _, d := reg.snapshot(); return len(d) > 0 })
	// The agent reconnects on its own and is refused from now on.
	select {
	case <-a.failed:
	case <-time.After(20 * time.Second):
		t.Fatal("reconnect with a revoked token was not refused")
	}
}

// A collector whose network drops silently never sends a TCP close. The
// reaper closes its connection once it misses its heartbeats, which marks
// it offline; an agent that never promised heartbeats is left alone.
func TestReapClosesSilentHeartbeatAgents(t *testing.T) {
	reg := newFakeRegistry()
	reg.tokens["sct_a"] = collectors.TokenRef{TokenID: uuid.New(), OrgID: uuid.New(), GroupID: uuid.New()}
	s, url := startServer(t, reg)

	beats := startAgentCaps(t, url, "sct_a", agentCaps|protobufs.AgentCapabilities_AgentCapabilities_ReportsHeartbeat)
	quiet := startAgentCaps(t, url, "sct_a", agentCaps)
	<-beats.connected
	<-quiet.connected
	waitFor(t, "both registered", func() bool { o, _, _, _ := reg.snapshot(); return len(o) >= 2 })

	if n := s.reap(time.Now()); n != 0 {
		t.Fatalf("reaped %d live agents", n)
	}
	if n := s.reap(time.Now().Add(staleAfter + time.Second)); n != 1 {
		t.Fatalf("reaped %d, want only the heartbeat agent", n)
	}
	waitFor(t, "the silent agent marked offline", func() bool { _, _, _, d := reg.snapshot(); return len(d) == 1 })
}

// A configuration waiting for a supervisor-type agent is delivered once,
// with its hash, and the agent's answer is recorded. An agent that does
// not accept remote configuration is never sent one.
func TestDeliversWaitingConfiguration(t *testing.T) {
	reg := newFakeRegistry()
	reg.tokens["sct_a"] = collectors.TokenRef{TokenID: uuid.New(), OrgID: uuid.New(), GroupID: uuid.New()}
	reg.pending = &collectors.Deployment{ID: uuid.New(), Hash: "h-1", Status: collectors.DeploymentPending}
	reg.body = "receivers: {otlp: {}}"
	s, url := startServer(t, reg)

	var mu sync.Mutex
	var got []string
	c := client.NewWebSocket(nil)
	caps := agentCaps | protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig | protobufs.AgentCapabilities_AgentCapabilities_ReportsRemoteConfig
	_ = c.SetAgentDescription(&protobufs.AgentDescription{IdentifyingAttributes: []*protobufs.KeyValue{
		{Key: "service.name", Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: "otelcol"}}}}})
	_ = c.SetHealth(&protobufs.ComponentHealth{Healthy: true})
	err := c.Start(context.Background(), clienttypes.StartSettings{
		OpAMPServerURL: strings.Replace(url, "https://", "wss://", 1) + Path,
		Header:         http.Header{"Authorization": []string{"Bearer sct_a"}},
		TLSConfig:      &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // httptest's certificate
		InstanceUid:    clienttypes.InstanceUid(uuid.New()),
		Capabilities:   caps,
		Callbacks: clienttypes.Callbacks{
			OnMessage: func(_ context.Context, m *clienttypes.MessageData) {
				if m.RemoteConfig == nil {
					return
				}
				mu.Lock()
				got = append(got, string(m.RemoteConfig.Config.ConfigMap[""].Body)+"|"+string(m.RemoteConfig.ConfigHash))
				mu.Unlock()
				_ = c.SetRemoteConfigStatus(&protobufs.RemoteConfigStatus{
					LastRemoteConfigHash: m.RemoteConfig.ConfigHash,
					Status:               protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLIED,
				})
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	waitFor(t, "applied", func() bool {
		reg.mu.Lock()
		defer reg.mu.Unlock()
		return len(reg.statuses) > 0 && reg.statuses[len(reg.statuses)-1] == collectors.DeploymentApplied
	})
	mu.Lock()
	if len(got) != 1 || got[0] != "receivers: {otlp: {}}|h-1" {
		t.Fatalf("delivered %v, want the body once with its hash", got)
	}
	mu.Unlock()

	// Nothing waiting: Deliver sends nothing.
	if s.Deliver(context.Background(), uuid.New()) {
		t.Fatal("delivered to a collector with nothing waiting")
	}

	// A read-only agent is never offered a configuration.
	reg.pending = &collectors.Deployment{ID: uuid.New(), Hash: "h-2", Status: collectors.DeploymentPending}
	ro := startAgent(t, url, "sct_a", clienttypes.InstanceUid(uuid.New()), true, "a: 1")
	<-ro.connected
	time.Sleep(300 * time.Millisecond)
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.pending == nil || reg.pending.Status != collectors.DeploymentPending {
		t.Fatal("a configuration was sent to an agent that does not accept one")
	}
}
