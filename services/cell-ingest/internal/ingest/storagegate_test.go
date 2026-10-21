// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	imclickhouse "github.com/sluicio/sluicio-app/pkg/clickhouse"
)

func TestTheStorageGateRefusesAtTheLimitAndReopensBelowIt(t *testing.T) {
	g := NewStorageGate(nil, imclickhouse.StorageLimitFromGB(1), slog.New(slog.NewTextHandler(io.Discard, nil)))
	stored := 0
	h := g.Wrap("logs", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { stored++ }))
	send := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/logs", nil))
		return rec
	}

	// Before any measurement the gate is open.
	if rec := send(); rec.Code != http.StatusOK || stored != 1 {
		t.Fatalf("unmeasured gate refused: %d", rec.Code)
	}
	g.observe(1_000_000_000)
	rec := send()
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "300" || stored != 1 {
		t.Fatalf("at the limit: want 429 with Retry-After 300 and nothing stored, got %d %q stored=%d", rec.Code, rec.Header().Get("Retry-After"), stored)
	}
	if GetCounters().Logs.StorageRefused == 0 {
		t.Error("the refusal was not counted")
	}
	// Just under the limit is not enough to reopen: it would flap.
	g.observe(990_000_000)
	if send().Code != http.StatusTooManyRequests {
		t.Error("reopened at 99% of the limit")
	}
	g.observe(970_000_000)
	if rec := send(); rec.Code != http.StatusOK || stored != 2 {
		t.Errorf("below the resume mark: want accepted, got %d", rec.Code)
	}
}

func TestNoLimitMeansNoGate(t *testing.T) {
	g := NewStorageGate(nil, imclickhouse.StorageLimit{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.observe(1 << 62)
	called := false
	g.Wrap("traces", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/traces", nil))
	if !called {
		t.Error("an instance with no storage limit refused telemetry")
	}
}
