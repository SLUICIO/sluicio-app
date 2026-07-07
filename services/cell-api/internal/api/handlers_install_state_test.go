// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
)

// The public install-state endpoint only advertises a login pre-fill when
// SLUICIO_LOGIN_PREFILL_EMAIL is set (demo cells); on every normal install
// the key must be absent entirely.
func TestInstallStatePrefill(t *testing.T) {
	h := &Handlers{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	call := func() map[string]json.RawMessage {
		t.Helper()
		rec := httptest.NewRecorder()
		h.installState(rec, httptest.NewRequest("GET", "/api/v1/auth/install-state", nil))
		if rec.Code != 200 {
			t.Fatalf("status = %d", rec.Code)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("bad JSON: %v", err)
		}
		return body
	}

	t.Run("no env → no prefill key", func(t *testing.T) {
		t.Setenv("SLUICIO_LOGIN_PREFILL_EMAIL", "")
		if _, ok := call()["prefill"]; ok {
			t.Fatal("prefill present without SLUICIO_LOGIN_PREFILL_EMAIL")
		}
	})

	t.Run("env set → prefill advertised", func(t *testing.T) {
		t.Setenv("SLUICIO_LOGIN_PREFILL_EMAIL", "demo@sluicio.com")
		t.Setenv("SLUICIO_LOGIN_PREFILL_PASSWORD", "demodemo")
		var got struct{ Email, Password string }
		if err := json.Unmarshal(call()["prefill"], &got); err != nil {
			t.Fatalf("prefill missing/bad: %v", err)
		}
		if got.Email != "demo@sluicio.com" || got.Password != "demodemo" {
			t.Fatalf("prefill = %+v", got)
		}
	})
}
