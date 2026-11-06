// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	imclickhouse "github.com/sluicio/sluicio-app/pkg/clickhouse"
)

func TestTheStorageGateRefusesAtTheLimitAndReopensBelowIt(t *testing.T) {
	limit := imclickhouse.StorageLimitFromGB(1)
	limit.Action = imclickhouse.StoragePause
	g := NewStorageGate(nil, limit, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

// fakeStore is a telemetry database of whole days, oldest first.
type fakeStore struct {
	days    []uint64 // bytes per day; the last one is today
	deleted []string
}

func (f *fakeStore) measure(context.Context) (uint64, error) {
	var n uint64
	for _, d := range f.days {
		n += d
	}
	return n, nil
}

func (f *fakeStore) deleteOldest(context.Context) (string, error) {
	if len(f.days) <= 1 { // only today left
		return "", nil
	}
	f.days = f.days[1:]
	day := []string{"20261101", "20261102", "20261103", "20261104"}[len(f.deleted)]
	f.deleted = append(f.deleted, day)
	return day, nil
}

func gateOn(f *fakeStore, action imclickhouse.StorageAction) *StorageGate {
	limit := imclickhouse.StorageLimitFromGB(1)
	limit.Action = action
	g := NewStorageGate(nil, limit, slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.measureFn, g.deleteOldestFn = f.measure, f.deleteOldest
	return g
}

// The acceptable use policy's default: a full instance makes room by
// deleting its oldest telemetry and keeps collecting.
func TestAFullInstanceDeletesItsOldestDaysAndKeepsCollecting(t *testing.T) {
	f := &fakeStore{days: []uint64{400_000_000, 300_000_000, 200_000_000, 200_000_000}} // 1.1 GB
	g := gateOn(f, imclickhouse.StorageDeleteOldest)
	g.measure(context.Background())
	if len(f.deleted) != 1 || f.deleted[0] != "20261101" {
		t.Fatalf("deleted %v, want only the oldest day", f.deleted)
	}
	if g.stopped.Load() {
		t.Error("collection paused although deleting the oldest day made room")
	}
}

// When today's data alone fills the limit there is nothing older to
// delete, and the disk is protected by refusing instead.
func TestAnInstanceFullOfTodaysDataRefusesTelemetry(t *testing.T) {
	f := &fakeStore{days: []uint64{100_000_000, 1_200_000_000}}
	g := gateOn(f, imclickhouse.StorageDeleteOldest)
	g.measure(context.Background())
	if len(f.deleted) != 1 {
		t.Fatalf("deleted %v, want the one earlier day", f.deleted)
	}
	if !g.stopped.Load() {
		t.Error("today's data alone is over the limit, but telemetry is still accepted")
	}
}

func TestAPausingInstanceDeletesNothing(t *testing.T) {
	f := &fakeStore{days: []uint64{600_000_000, 600_000_000}}
	g := gateOn(f, imclickhouse.StoragePause)
	g.measure(context.Background())
	if len(f.deleted) != 0 {
		t.Errorf("a pausing instance deleted %v", f.deleted)
	}
	if !g.stopped.Load() {
		t.Error("a pausing instance at its limit still accepts telemetry")
	}
}

func TestAFailedDeleteLeavesTheGateToRefuse(t *testing.T) {
	f := &fakeStore{days: []uint64{600_000_000, 600_000_000}}
	g := gateOn(f, imclickhouse.StorageDeleteOldest)
	g.deleteOldestFn = func(context.Context) (string, error) { return "", errors.New("boom") }
	g.measure(context.Background())
	if !g.stopped.Load() {
		t.Error("deleting failed and the instance is over its limit, but telemetry is still accepted")
	}
}
