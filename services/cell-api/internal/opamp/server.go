// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package opamp is the cell's OpAMP endpoint: the server side of the
// protocol a customer's collectors (the OpAMP extension, or the supervisor
// running a collector) connect to. The agent always opens the connection,
// over WebSocket or plain HTTP polling, behind the same TLS-terminating
// proxy as the rest of /api.
//
// Stage 1 only listens. It authenticates the team token, registers the
// agent in its team and stores what the agent reports. It offers no
// remote configuration, so nothing a collector runs is changed from here.
package opamp

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	opamptypes "github.com/open-telemetry/opamp-go/client/types"
	"github.com/open-telemetry/opamp-go/protobufs"
	opampserver "github.com/open-telemetry/opamp-go/server"
	"github.com/open-telemetry/opamp-go/server/types"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
)

// Path is where the endpoint is mounted. Under /api so every deployment's
// proxy (Caddy, the Helm Ingress, the OpenShift Route, the portal's edge)
// already routes it to cell-api; WebSocket upgrades pass through them.
const Path = "/api/v1/opamp"

// maxMessageSize bounds one OpAMP message. A configuration is kilobytes;
// the library default of 64 MiB is far more than a collector needs.
const maxMessageSize = 16 << 20

// serverCapabilities: the server takes status and the effective
// configuration, and offers remote configuration to the agents that
// accept it (the supervisor type). Nothing is offered unless someone
// with the rights to change the collector sends a configuration.
const serverCapabilities = uint64(protobufs.ServerCapabilities_ServerCapabilities_AcceptsStatus |
	protobufs.ServerCapabilities_ServerCapabilities_AcceptsEffectiveConfig |
	protobufs.ServerCapabilities_ServerCapabilities_OffersRemoteConfig)

// Registry is the part of collectors.Store the server writes to, so the
// protocol handling can be tested without Postgres.
type Registry interface {
	Authenticate(ctx context.Context, presented string) (collectors.TokenRef, error)
	TouchToken(ctx context.Context, id uuid.UUID) error
	Observe(ctx context.Context, o collectors.Observation) (uuid.UUID, bool, error)
	RecordConfig(ctx context.Context, collectorID uuid.UUID, files []collectors.ConfigFile) (bool, error)
	Disconnected(ctx context.Context, groupID uuid.UUID, instanceUID string) error
	NextToDeliver(ctx context.Context, collectorID uuid.UUID) (*collectors.Deployment, string, error)
	MarkSent(ctx context.Context, id uuid.UUID) error
	ReportStatus(ctx context.Context, collectorID uuid.UUID, hash string, status collectors.DeploymentStatus, errMsg string) error
}

// Server holds the live connections.
type Server struct {
	reg    Registry
	logger *slog.Logger

	handler opampserver.HTTPHandlerFunc
	// ConnContext must be set on the http.Server: plain-HTTP OpAMP finds
	// its connection through it.
	ConnContext opampserver.ConnContext

	mu sync.Mutex
	// live holds the open WebSocket connections and the token each one
	// authenticated with, so revoking a token closes them.
	live map[types.Connection]*connState
	// fullState remembers which agents have sent their description since
	// this process started; one that has not is asked for its full state.
	fullState map[string]bool
	// touched throttles last_used_at writes; HTTP agents authenticate on
	// every poll.
	touched map[uuid.UUID]time.Time
}

type connState struct {
	ref        collectors.TokenRef
	transport  string
	remoteAddr string
	// instanceUID is the agent seen on this connection, so a WebSocket
	// close can mark it offline.
	instanceUID string
	// lastMsg and heartbeat feed the reaper. Written by the connection's
	// own message callback, read by the reaper: guarded by Server.mu.
	lastMsg   time.Time
	heartbeat bool
	// collectorID, acceptsRemote and sentHash serve delivery: which
	// registry row this agent is, whether it takes a configuration, and
	// what was last delivered on this connection, so a configuration
	// is not sent again with every heartbeat.
	collectorID   uuid.UUID
	acceptsRemote bool
	sentHash      string
	instanceBytes []byte
}

// staleAfter is how long a WebSocket agent that reports heartbeats may be
// silent before its connection is closed. Agents send one every 30s by
// default; three missed means the agent is gone, even when no TCP close
// ever arrives (a network that drops silently, a proxy that keeps its own
// half of the connection open). Without this such a collector would read
// online for as long as cell-api runs.
const staleAfter = 90 * time.Second

// New attaches an OpAMP server.
func New(reg Registry, logger *slog.Logger) (*Server, error) {
	s := &Server{
		reg:       reg,
		logger:    logger,
		live:      map[types.Connection]*connState{},
		fullState: map[string]bool{},
		touched:   map[uuid.UUID]time.Time{},
	}
	srv := opampserver.New(&slogAdapter{logger})
	handler, connCtx, err := srv.Attach(opampserver.Settings{
		Callbacks:         types.Callbacks{OnConnecting: s.onConnecting},
		EnableCompression: true,
		MaxMessageSize:    maxMessageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("opamp: attach: %w", err)
	}
	s.handler, s.ConnContext = handler, connCtx
	return s, nil
}

// ServeHTTP is the endpoint.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler(w, r) }

// bearer reads "Authorization: Bearer <token>".
func bearer(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// onConnecting authenticates. The token decides the team; nothing the
// agent says about itself does. It runs once per WebSocket and on every
// plain-HTTP poll, so a revoked token stops a polling agent at its next
// request.
func (s *Server) onConnecting(r *http.Request) types.ConnectionResponse {
	ref, err := s.reg.Authenticate(r.Context(), bearer(r))
	if err != nil {
		if errors.Is(err, collectors.ErrUnauthenticated) {
			return types.ConnectionResponse{Accept: false, HTTPStatusCode: http.StatusUnauthorized}
		}
		s.logger.Warn("opamp: token lookup failed", "err", err)
		return types.ConnectionResponse{Accept: false, HTTPStatusCode: http.StatusServiceUnavailable,
			HTTPResponseHeader: map[string]string{"Retry-After": "30"}}
	}
	s.touch(ref.TokenID)
	st := &connState{ref: ref, transport: "websocket", remoteAddr: httpserver.ClientIP(r)}
	if r.Header.Get("Content-Type") == "application/x-protobuf" {
		st.transport = "http"
	}
	return types.ConnectionResponse{
		Accept: true,
		ConnectionCallbacks: types.ConnectionCallbacks{
			OnConnected: func(_ context.Context, conn types.Connection) {
				if st.transport == "websocket" {
					s.mu.Lock()
					st.lastMsg = time.Now()
					s.live[conn] = st
					s.mu.Unlock()
				}
			},
			OnMessage: func(ctx context.Context, _ types.Connection, msg *protobufs.AgentToServer) *protobufs.ServerToAgent {
				return s.onMessage(ctx, st, msg)
			},
			OnConnectionClose: func(conn types.Connection) {
				if st.transport != "websocket" {
					return
				}
				s.mu.Lock()
				delete(s.live, conn)
				uid := st.instanceUID
				s.mu.Unlock()
				if uid != "" {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := s.reg.Disconnected(ctx, st.ref.GroupID, uid); err != nil {
						s.logger.Warn("opamp: mark disconnected failed", "err", err)
					}
				}
			},
		},
	}
}

func (s *Server) touch(tokenID uuid.UUID) {
	s.mu.Lock()
	last, ok := s.touched[tokenID]
	due := !ok || time.Since(last) > time.Minute
	if due {
		s.touched[tokenID] = time.Now()
	}
	s.mu.Unlock()
	if due {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.reg.TouchToken(ctx, tokenID); err != nil {
			s.logger.Warn("opamp: touch token failed", "err", err)
		}
	}
}

// DisconnectToken closes every live WebSocket that authenticated with the
// token, so revoking or regenerating it takes effect at once rather than
// at the agent's next reconnect.
func (s *Server) DisconnectToken(tokenID uuid.UUID) int {
	s.mu.Lock()
	var conns []types.Connection
	for c, st := range s.live {
		if st.ref.TokenID == tokenID {
			conns = append(conns, c)
		}
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Disconnect()
	}
	return len(conns)
}

// remoteConfigFor returns the configuration waiting for this agent, if it
// takes one and does not have it yet, and records the delivery.
func (s *Server) remoteConfigFor(ctx context.Context, st *connState, agentHas string) *protobufs.AgentRemoteConfig {
	s.mu.Lock()
	accepts, id, sent := st.acceptsRemote, st.collectorID, st.sentHash
	s.mu.Unlock()
	if !accepts || id == uuid.Nil {
		return nil
	}
	d, body, err := s.reg.NextToDeliver(ctx, id)
	if err != nil {
		s.logger.Warn("opamp: read pending configuration failed", "err", err)
		return nil
	}
	if d == nil || agentHas == d.Hash || (d.Status != collectors.DeploymentPending && sent == d.Hash) {
		return nil
	}
	if err := s.reg.MarkSent(ctx, d.ID); err != nil {
		s.logger.Warn("opamp: mark sent failed", "err", err)
	}
	s.mu.Lock()
	st.sentHash = d.Hash
	s.mu.Unlock()
	return &protobufs.AgentRemoteConfig{
		Config: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigObject{
			"": {Body: []byte(body), ContentType: "text/yaml"},
		}},
		ConfigHash: []byte(d.Hash),
	}
}

// Deliver sends the waiting configuration to a collector connected over
// WebSocket now, rather than at its next message. An agent on plain HTTP
// gets it at its next poll. Returns whether it was sent.
func (s *Server) Deliver(ctx context.Context, collectorID uuid.UUID) bool {
	s.mu.Lock()
	var conn types.Connection
	var st *connState
	for c, x := range s.live {
		if x.collectorID == collectorID && x.acceptsRemote {
			conn, st = c, x
			break
		}
	}
	s.mu.Unlock()
	if conn == nil {
		return false
	}
	rc := s.remoteConfigFor(ctx, st, "")
	if rc == nil {
		return false
	}
	s.mu.Lock()
	instance := st.instanceBytes
	s.mu.Unlock()
	msg := &protobufs.ServerToAgent{InstanceUid: instance, Capabilities: serverCapabilities, RemoteConfig: rc}
	if err := conn.Send(ctx, msg); err != nil {
		s.logger.Warn("opamp: deliver configuration failed", "err", err)
		return false
	}
	return true
}

func deploymentStatus(s protobufs.RemoteConfigStatuses) (collectors.DeploymentStatus, bool) {
	switch s {
	case protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLYING:
		return collectors.DeploymentApplying, true
	case protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLIED:
		return collectors.DeploymentApplied, true
	case protobufs.RemoteConfigStatuses_RemoteConfigStatuses_FAILED:
		return collectors.DeploymentFailed, true
	}
	return "", false
}

// Run closes stale WebSocket connections until ctx ends. See staleAfter.
func (s *Server) Run(ctx context.Context) {
	t := time.NewTicker(staleAfter / 3)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if n := s.reap(now); n > 0 {
				s.logger.Info("opamp: closed silent agent connections", "count", n)
			}
		}
	}
}

// reap disconnects every heartbeat-reporting WebSocket agent silent for
// longer than staleAfter. Closing the connection runs OnConnectionClose,
// which marks the collector offline. Agents that do not report heartbeats
// are left alone: their silence says nothing.
func (s *Server) reap(now time.Time) int {
	s.mu.Lock()
	var stale []types.Connection
	for c, st := range s.live {
		if st.heartbeat && now.Sub(st.lastMsg) > staleAfter {
			stale = append(stale, c)
		}
	}
	s.mu.Unlock()
	for _, c := range stale {
		_ = c.Disconnect()
	}
	return len(stale)
}

// FormatInstanceUID renders the agent's 16-byte instance UID the way the
// collector and supervisor print it (a UUID); anything else as hex.
func FormatInstanceUID(b []byte) string {
	if len(b) == 16 {
		return uuid.UUID(b).String()
	}
	return hex.EncodeToString(b)
}

func (s *Server) onMessage(ctx context.Context, st *connState, msg *protobufs.AgentToServer) *protobufs.ServerToAgent {
	resp := &protobufs.ServerToAgent{InstanceUid: msg.InstanceUid, Capabilities: serverCapabilities}
	if len(msg.InstanceUid) == 0 || len(msg.InstanceUid) > 64 {
		resp.ErrorResponse = &protobufs.ServerErrorResponse{
			Type:         protobufs.ServerErrorResponseType_ServerErrorResponseType_BadRequest,
			ErrorMessage: "instance_uid must be set",
		}
		return resp
	}
	uid := FormatInstanceUID(msg.InstanceUid)
	s.mu.Lock()
	st.instanceUID = uid
	st.lastMsg = time.Now()
	if msg.Capabilities != 0 {
		st.heartbeat = msg.Capabilities&uint64(protobufs.AgentCapabilities_AgentCapabilities_ReportsHeartbeat) != 0
		st.acceptsRemote = msg.Capabilities&uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig) != 0
	}
	s.mu.Unlock()

	obs := collectors.Observation{
		OrgID:       st.ref.OrgID,
		GroupID:     st.ref.GroupID,
		TokenID:     st.ref.TokenID,
		InstanceUID: uid,
		Transport:   st.transport,
		RemoteAddr:  st.remoteAddr,
	}
	if msg.Capabilities != 0 {
		c := msg.Capabilities
		obs.Capabilities = &c
	}
	if msg.AgentDescription != nil {
		obs.Description = describe(msg.AgentDescription)
	}
	if msg.Health != nil {
		h := health(msg.Health, 0)
		obs.Health = &h
	}
	id, _, err := s.reg.Observe(ctx, obs)
	if err != nil {
		s.logger.Error("opamp: register agent failed", "err", err)
		resp.ErrorResponse = &protobufs.ServerErrorResponse{
			Type:    protobufs.ServerErrorResponseType_ServerErrorResponseType_Unavailable,
			Details: &protobufs.ServerErrorResponse_RetryInfo{RetryInfo: &protobufs.RetryInfo{RetryAfterNanoseconds: uint64(30 * time.Second)}},
		}
		return resp
	}

	s.mu.Lock()
	st.collectorID = id
	st.instanceBytes = msg.InstanceUid
	s.mu.Unlock()

	// What the agent says about a configuration it was sent.
	agentHas := ""
	if rs := msg.RemoteConfigStatus; rs != nil && len(rs.LastRemoteConfigHash) > 0 {
		agentHas = string(rs.LastRemoteConfigHash)
		if status, ok := deploymentStatus(rs.Status); ok {
			if err := s.reg.ReportStatus(ctx, id, agentHas, status, rs.ErrorMessage); err != nil {
				s.logger.Warn("opamp: record remote config status failed", "err", err)
			}
		}
	}

	if ec := msg.EffectiveConfig; ec != nil && ec.ConfigMap != nil {
		if _, err := s.reg.RecordConfig(ctx, id, files(ec.ConfigMap)); err != nil && !errors.Is(err, collectors.ErrNoKey) {
			s.logger.Error("opamp: store effective config failed", "err", err)
		}
	}
	if msg.AgentDisconnect != nil && st.transport == "websocket" {
		if err := s.reg.Disconnected(ctx, st.ref.GroupID, uid); err != nil {
			s.logger.Warn("opamp: mark disconnected failed", "err", err)
		}
	}

	if rc := s.remoteConfigFor(ctx, st, agentHas); rc != nil {
		resp.RemoteConfig = rc
	}

	// Agents send only what changed since their last message. An agent
	// this process has not heard describe itself (new, or reconnecting
	// after a restart of cell-api) is asked for everything.
	key := st.ref.GroupID.String() + "/" + uid
	s.mu.Lock()
	if msg.AgentDescription != nil {
		s.fullState[key] = true
	}
	known := s.fullState[key]
	s.mu.Unlock()
	if !known {
		resp.Flags = uint64(protobufs.ServerToAgentFlags_ServerToAgentFlags_ReportFullState)
	}
	return resp
}

func describe(d *protobufs.AgentDescription) map[string]string {
	out := map[string]string{}
	for _, kv := range d.NonIdentifyingAttributes {
		out[kv.Key] = anyString(kv.Value)
	}
	// Identifying attributes win on a clash: they are what names the agent.
	for _, kv := range d.IdentifyingAttributes {
		out[kv.Key] = anyString(kv.Value)
	}
	return out
}

func anyString(v *protobufs.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.Value.(type) {
	case *protobufs.AnyValue_StringValue:
		return x.StringValue
	case *protobufs.AnyValue_IntValue:
		return fmt.Sprint(x.IntValue)
	case *protobufs.AnyValue_DoubleValue:
		return fmt.Sprint(x.DoubleValue)
	case *protobufs.AnyValue_BoolValue:
		return fmt.Sprint(x.BoolValue)
	case *protobufs.AnyValue_BytesValue:
		return hex.EncodeToString(x.BytesValue)
	case *protobufs.AnyValue_ArrayValue:
		parts := make([]string, 0, len(x.ArrayValue.GetValues()))
		for _, e := range x.ArrayValue.GetValues() {
			parts = append(parts, anyString(e))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *protobufs.AnyValue_KvlistValue:
		parts := make([]string, 0, len(x.KvlistValue.GetValues()))
		for _, e := range x.KvlistValue.GetValues() {
			parts = append(parts, e.Key+"="+anyString(e.Value))
		}
		sort.Strings(parts)
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return ""
}

// health converts the agent's report. Errors are masked because they
// often quote the URL that failed, credentials included.
func health(h *protobufs.ComponentHealth, depth int) collectors.Health {
	out := collectors.Health{
		Healthy:   h.Healthy,
		Status:    h.Status,
		LastError: collectors.MaskText(h.LastError),
	}
	if h.StartTimeUnixNano > 0 {
		t := time.Unix(0, int64(h.StartTimeUnixNano)).UTC()
		out.StartedAt = &t
	}
	if depth < 4 && len(h.ComponentHealthMap) > 0 {
		out.Components = make(map[string]collectors.Health, len(h.ComponentHealthMap))
		for name, c := range h.ComponentHealthMap {
			if c != nil {
				out.Components[name] = health(c, depth+1)
			}
		}
	}
	return out
}

func files(m *protobufs.AgentConfigMap) []collectors.ConfigFile {
	out := make([]collectors.ConfigFile, 0, len(m.ConfigMap))
	for name, f := range m.ConfigMap {
		if f == nil {
			continue
		}
		out = append(out, collectors.ConfigFile{Name: name, ContentType: f.ContentType, Body: string(f.Body)})
	}
	return out
}

type slogAdapter struct{ l *slog.Logger }

func (a *slogAdapter) Debugf(ctx context.Context, format string, v ...any) {
	a.l.DebugContext(ctx, "opamp: "+fmt.Sprintf(format, v...))
}

func (a *slogAdapter) Errorf(ctx context.Context, format string, v ...any) {
	a.l.WarnContext(ctx, "opamp: "+fmt.Sprintf(format, v...))
}

var _ opamptypes.Logger = (*slogAdapter)(nil)
