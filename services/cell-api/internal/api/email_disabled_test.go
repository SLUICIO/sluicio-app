// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Email turned off for the instance (SLUICIO_EMAIL_DISABLED): no email
// channel can be created or kept as one, no mail server can be set or
// tested, and the other channel kinds are untouched.

package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
)

func TestEmailDisabledRefusesEmailChannels(t *testing.T) {
	req := &channelRequest{
		Name: "ops", Kind: alerting.ChannelEmail,
		Config: map[string]string{"to": "ops@example.test"},
	}
	for _, p := range []channelPolicy{{EmailDisabled: true}, {EmailDisabled: true, Managed: true}} {
		err := validateChannel(req, p)
		if err == nil || !strings.Contains(err.Error(), "turned off") {
			t.Errorf("policy %+v: an email channel was accepted (err %v)", p, err)
		}
	}
	hook := &channelRequest{
		Name: "hook", Kind: alerting.ChannelWebhook,
		Config: map[string]string{"url": "https://hooks.example.test/x"},
	}
	if err := validateChannel(hook, channelPolicy{EmailDisabled: true}); err != nil {
		t.Errorf("a webhook was refused with email off: %v", err)
	}
}

func TestEmailDisabledRefusesMailSettings(t *testing.T) {
	h := &Handlers{EmailDisabled: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	rec := httptest.NewRecorder()
	h.getSMTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/cell-settings/smtp", nil))
	var got smtpResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if !got.Disabled || got.Configured || got.Host != "" {
		t.Errorf("get should say disabled and nothing else, got %+v", got)
	}

	for name, handler := range map[string]http.HandlerFunc{"patch": h.patchSMTP, "test": h.testSMTP} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"host":"smtp.example.test"}`)))
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "turned off") {
			t.Errorf("%s: want 409 turned off, got %d %s", name, rec.Code, rec.Body)
		}
	}
}
