// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package audit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ForwardingRecorder decorates a Recorder: every successfully recorded
// entry is also POSTed to an external sink (SIEM HTTP collector, object
// store gateway, syslog bridge — anything that accepts JSON over HTTPS).
//
// This is the "write-once" leg of tamper evidence: a copy that leaves the
// cell before anyone with database access can touch it. It is strictly
// opt-in — wired only when the operator sets SLUICIO_AUDIT_SINK_URL on
// cell-api — and deliberately NOT configurable at runtime through the API:
// a runtime knob would let a compromised admin account redirect or drain
// the security log. Deployment config is the trust boundary here.
//
// Delivery is asynchronous and best-effort: a slow or down sink never
// blocks or fails the action being audited. The local Postgres chain
// remains the source of truth; the sink is the off-box witness.
type ForwardingRecorder struct {
	Recorder
	url    string
	secret string
	client *http.Client
	logger *slog.Logger
	queue  chan sinkEvent
}

// sinkEvent is the wire shape shipped to the sink. RecordedAt is stamped
// at enqueue time (the store assigns the exact occurred_at internally;
// the two are milliseconds apart).
type sinkEvent struct {
	RecordedAt  time.Time      `json:"recorded_at"`
	OrgID       uuid.UUID      `json:"org_id"`
	ActorUserID *uuid.UUID     `json:"actor_user_id,omitempty"`
	ActorName   string         `json:"actor_name,omitempty"`
	ActorEmail  string         `json:"actor_email,omitempty"`
	Action      string         `json:"action"`
	TargetType  string         `json:"target_type,omitempty"`
	TargetID    string         `json:"target_id,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	IP          string         `json:"ip,omitempty"`
}

// NewForwarding wraps inner with sink delivery to url. secret, when
// non-empty, signs each request body with HMAC-SHA256 into the
// X-Sluicio-Signature header so the receiver can authenticate the sender.
func NewForwarding(inner Recorder, url, secret string, logger *slog.Logger) *ForwardingRecorder {
	f := &ForwardingRecorder{
		Recorder: inner,
		url:      url,
		secret:   secret,
		client:   &http.Client{Timeout: 10 * time.Second},
		logger:   logger,
		// Bounded queue: an unreachable sink drops events (with a warning)
		// rather than growing memory without limit. 256 in-flight admin
		// actions is a large burst for this domain.
		queue: make(chan sinkEvent, 256),
	}
	go f.deliver()
	return f
}

// Record persists locally first; only a successful local write is
// forwarded (the sink mirrors the log, it doesn't replace it).
func (f *ForwardingRecorder) Record(ctx context.Context, e Entry) error {
	if err := f.Recorder.Record(ctx, e); err != nil {
		return err
	}
	ev := sinkEvent{
		RecordedAt:  time.Now().UTC(),
		OrgID:       e.OrgID,
		ActorUserID: e.ActorUserID,
		ActorName:   e.ActorName,
		ActorEmail:  e.ActorEmail,
		Action:      e.Action,
		TargetType:  e.TargetType,
		TargetID:    e.TargetID,
		Metadata:    e.Metadata,
		IP:          e.IP,
	}
	select {
	case f.queue <- ev:
	default:
		f.logger.Warn("audit sink queue full — event not forwarded", "action", e.Action)
	}
	return nil
}

// deliver drains the queue for the life of the process. One retry after a
// short pause covers transient blips; anything longer is logged and
// dropped — the local log still has the entry.
func (f *ForwardingRecorder) deliver() {
	for ev := range f.queue {
		if err := f.post(ev); err != nil {
			time.Sleep(5 * time.Second)
			if err := f.post(ev); err != nil {
				f.logger.Warn("audit sink delivery failed", "action", ev.Action, "err", err)
			}
		}
	}
}

func (f *ForwardingRecorder) post(ev sinkEvent) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sluicio-audit-sink")
	if f.secret != "" {
		mac := hmac.New(sha256.New, []byte(f.secret))
		mac.Write(body)
		req.Header.Set("X-Sluicio-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	res, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return &sinkStatusError{status: res.StatusCode}
	}
	return nil
}

type sinkStatusError struct{ status int }

func (e *sinkStatusError) Error() string {
	return "sink returned HTTP " + http.StatusText(e.status)
}
