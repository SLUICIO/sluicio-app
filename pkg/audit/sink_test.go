// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The sink must deliver every recorded entry as signed JSON, and a sink
// failure must never surface to the caller — the local write is the
// source of truth.
func TestForwardingRecorderDeliversSignedEvents(t *testing.T) {
	type got struct {
		body []byte
		sig  string
	}
	received := make(chan got, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received <- got{body: b, sig: r.Header.Get("X-Sluicio-Signature")}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	const secret = "test-secret"
	f := NewForwarding(Noop{}, srv.URL, secret, slog.New(slog.NewTextHandler(io.Discard, nil)))

	entry := Entry{
		OrgID:      uuid.New(),
		ActorName:  "Tester",
		ActorEmail: "t@example.com",
		Action:     "member.added",
		TargetType: "user",
		TargetID:   "abc",
		Metadata:   map[string]any{"role": "admin"},
		IP:         "10.0.0.1",
	}
	if err := f.Record(context.Background(), entry); err != nil {
		t.Fatalf("Record: %v", err)
	}

	select {
	case g := <-received:
		var ev sinkEvent
		if err := json.Unmarshal(g.body, &ev); err != nil {
			t.Fatalf("sink body not JSON: %v", err)
		}
		if ev.Action != "member.added" || ev.ActorEmail != "t@example.com" {
			t.Errorf("event fields lost in transit: %+v", ev)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(g.body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if g.sig != want {
			t.Errorf("signature mismatch: got %q want %q", g.sig, want)
		}
		if !strings.HasPrefix(g.sig, "sha256=") {
			t.Errorf("signature missing scheme prefix: %q", g.sig)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sink never received the event")
	}
}

// A dead sink must not fail Record — best-effort by design.
func TestForwardingRecorderSwallowsSinkFailure(t *testing.T) {
	f := NewForwarding(Noop{}, "http://127.0.0.1:1", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := f.Record(context.Background(), Entry{OrgID: uuid.New(), Action: "x"}); err != nil {
		t.Fatalf("Record must not surface sink errors, got: %v", err)
	}
}
