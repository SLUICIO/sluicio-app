// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package license

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func termToken(t *testing.T, m *Manager, priv []byte, customer string, months int) string {
	t.Helper()
	return signToken(t, priv, Claims{
		Customer:     customer,
		Plan:         "enterprise",
		Entitlements: []string{"sso"},
		NotBefore:    time.Now().Add(-time.Hour).Unix(),
		ExpiresAt:    time.Now().AddDate(0, months, 0).Unix(),
	})
}

// The point of the whole thing: a renewed key takes effect without a
// restart. A quarterly contract means four new keys a year, and each one
// used to cost a restart of the cell on the customer's own box.
func TestRenewalTakesEffectWithoutARestart(t *testing.T) {
	m, priv := newTestManager(t)
	path := filepath.Join(t.TempDir(), "license.key")
	if err := os.WriteFile(path, []byte(termToken(t, m, priv, "Acme Q1", 3)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("SLUICIO_LICENSE_FILE", path)
	if err := m.LoadFromEnv(); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if got := m.Status().Customer; got != "Acme Q1" {
		t.Fatalf("initial customer = %q", got)
	}

	if err := os.WriteFile(path, []byte(termToken(t, m, priv, "Acme Q2", 6)), 0o600); err != nil {
		t.Fatalf("renew: %v", err)
	}
	changed, err := m.ReloadFromFile()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !changed {
		t.Fatal("reload reported no change after the file was replaced")
	}
	if got := m.Status().Customer; got != "Acme Q2" {
		t.Fatalf("customer after renewal = %q, want the new term", got)
	}
}

// An unchanged file must not re-verify on every tick, and must not report
// a change - a log line a minute would bury the one that matters.
func TestUnchangedFileIsNotAReload(t *testing.T) {
	m, priv := newTestManager(t)
	path := filepath.Join(t.TempDir(), "license.key")
	os.WriteFile(path, []byte(termToken(t, m, priv, "Acme", 12)), 0o600)
	t.Setenv("SLUICIO_LICENSE_FILE", path)
	if err := m.LoadFromEnv(); err != nil {
		t.Fatalf("load: %v", err)
	}
	for i := 0; i < 3; i++ {
		changed, err := m.ReloadFromFile()
		if err != nil || changed {
			t.Fatalf("tick %d: changed=%v err=%v, want no change", i, changed, err)
		}
	}
}

// The delivery is a copy or an rsync, so the file is briefly absent,
// briefly empty, or briefly half-written. None of those means the customer
// stopped being entitled, and disabling their Enterprise features over one
// would be an outage we caused ourselves.
func TestATransientBadFileKeepsTheLicenseInForce(t *testing.T) {
	m, priv := newTestManager(t)
	path := filepath.Join(t.TempDir(), "license.key")
	os.WriteFile(path, []byte(termToken(t, m, priv, "Acme", 12)), 0o600)
	t.Setenv("SLUICIO_LICENSE_FILE", path)
	if err := m.LoadFromEnv(); err != nil {
		t.Fatalf("load: %v", err)
	}

	for _, bad := range []struct {
		name  string
		write func()
	}{
		{"empty", func() { os.WriteFile(path, nil, 0o600) }},
		{"half written", func() { os.WriteFile(path, []byte("sluicio_lic_eyJ"), 0o600) }},
		{"garbage", func() { os.WriteFile(path, []byte("not a token"), 0o600) }},
		{"missing", func() { os.Remove(path) }},
	} {
		bad.write()
		changed, err := m.ReloadFromFile()
		if err == nil {
			t.Errorf("%s: expected an error", bad.name)
		}
		if changed {
			t.Errorf("%s: reported a change", bad.name)
		}
		if !m.Entitled("sso") {
			t.Fatalf("%s: dropped the license that was in force", bad.name)
		}
	}
}

// A token signed by the wrong key must not take over from a good one -
// otherwise anyone who can write the file can also revoke the license.
func TestAForeignTokenDoesNotReplaceAGoodOne(t *testing.T) {
	m, priv := newTestManager(t)
	path := filepath.Join(t.TempDir(), "license.key")
	os.WriteFile(path, []byte(termToken(t, m, priv, "Acme", 12)), 0o600)
	t.Setenv("SLUICIO_LICENSE_FILE", path)
	if err := m.LoadFromEnv(); err != nil {
		t.Fatalf("load: %v", err)
	}

	_, otherPriv := newTestManager(t)
	os.WriteFile(path, []byte(termToken(t, m, otherPriv, "Impostor", 12)), 0o600)

	if _, err := m.ReloadFromFile(); err == nil {
		t.Fatal("a token from another keypair was accepted")
	}
	if got := m.Status().Customer; got != "Acme" {
		t.Fatalf("customer = %q, want the original", got)
	}
	// And the rejected token must not be remembered as current, or the
	// retry would report "unchanged" forever.
	if _, err := m.ReloadFromFile(); err == nil {
		t.Fatal("the rejected token was remembered as current")
	}
}

// An inline SLUICIO_LICENSE_KEY cannot change without a restart, so there
// is nothing to watch - and watching the file anyway would silently
// override the variable that takes precedence.
func TestInlineKeyIsNotWatched(t *testing.T) {
	m, priv := newTestManager(t)
	path := filepath.Join(t.TempDir(), "license.key")
	os.WriteFile(path, []byte(termToken(t, m, priv, "FromFile", 12)), 0o600)
	t.Setenv("SLUICIO_LICENSE_KEY", termToken(t, m, priv, "FromEnv", 12))
	t.Setenv("SLUICIO_LICENSE_FILE", path)
	if err := m.LoadFromEnv(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := m.Status().Customer; got != "FromEnv" {
		t.Fatalf("inline key did not win: %q", got)
	}
	if m.FilePath() != "" {
		t.Fatalf("watching %q even though the inline key won", m.FilePath())
	}
	changed, err := m.ReloadFromFile()
	if changed || err != nil {
		t.Fatalf("reload did something: changed=%v err=%v", changed, err)
	}
}

// The watcher stops with its context rather than outliving the process it
// belongs to.
func TestWatchFileStopsWithTheContext(t *testing.T) {
	m, priv := newTestManager(t)
	path := filepath.Join(t.TempDir(), "license.key")
	os.WriteFile(path, []byte(termToken(t, m, priv, "Acme", 12)), 0o600)
	t.Setenv("SLUICIO_LICENSE_FILE", path)
	if err := m.LoadFromEnv(); err != nil {
		t.Fatalf("load: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.WatchFile(ctx, 10*time.Millisecond, nil)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WatchFile did not return when its context was cancelled")
	}
}

// End to end through the watcher: replace the file, and the new term is in
// force without anything restarting.
func TestWatchFilePicksUpARenewal(t *testing.T) {
	m, priv := newTestManager(t)
	path := filepath.Join(t.TempDir(), "license.key")
	os.WriteFile(path, []byte(termToken(t, m, priv, "Acme Q1", 3)), 0o600)
	t.Setenv("SLUICIO_LICENSE_FILE", path)
	if err := m.LoadFromEnv(); err != nil {
		t.Fatalf("load: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reloaded := make(chan struct{}, 1)
	go m.WatchFile(ctx, 10*time.Millisecond, func(changed bool, err error) {
		if changed && err == nil {
			select {
			case reloaded <- struct{}{}:
			default:
			}
		}
	})

	os.WriteFile(path, []byte(termToken(t, m, priv, "Acme Q2", 6)), 0o600)
	select {
	case <-reloaded:
	case <-time.After(3 * time.Second):
		t.Fatal("the watcher never picked up the renewed file")
	}
	if got := m.Status().Customer; got != "Acme Q2" {
		t.Fatalf("customer = %q, want the renewed term", got)
	}
}

// A file that stays broken must keep being retried - that is how fixing it
// takes effect without a restart - but must not report the same problem
// every tick. Found by watching it run: an invalid token wrote the same
// warning every interval, for as long as the cell was up.
func TestARepeatedFailureIsReportedOnce(t *testing.T) {
	m, priv := newTestManager(t)
	path := filepath.Join(t.TempDir(), "license.key")
	os.WriteFile(path, []byte(termToken(t, m, priv, "Acme", 12)), 0o600)
	t.Setenv("SLUICIO_LICENSE_FILE", path)
	if err := m.LoadFromEnv(); err != nil {
		t.Fatalf("load: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var reports []string
	go m.WatchFile(ctx, 5*time.Millisecond, func(changed bool, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			reports = append(reports, "err:"+err.Error())
			return
		}
		reports = append(reports, "ok")
	})

	// Broken for many ticks — one report.
	os.WriteFile(path, []byte("not a token"), 0o600)
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	n := len(reports)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("a steady failure produced %d reports over ~30 ticks, want 1", n)
	}

	// Fixed — reported, and the retry proves a bad file recovers without
	// a restart.
	os.WriteFile(path, []byte(termToken(t, m, priv, "Acme renewed", 3)), 0o600)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.Status().Customer == "Acme renewed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := m.Status().Customer; got != "Acme renewed" {
		t.Fatalf("a broken file was never retried: customer = %q", got)
	}
}

// A license that was already invalid when the cell started must still be
// retried, or fixing a typo in the first install would need the restart
// this whole thing exists to avoid. Found live: startup recorded the token
// before verifying it, so the watcher called the bad file "unchanged" and
// never looked at it again.
func TestAFileThatWasInvalidAtStartupIsStillRetried(t *testing.T) {
	m, priv := newTestManager(t)
	path := filepath.Join(t.TempDir(), "license.key")
	os.WriteFile(path, []byte("sluicio_lic_typo.aaa"), 0o600)
	t.Setenv("SLUICIO_LICENSE_FILE", path)
	if err := m.LoadFromEnv(); err == nil {
		t.Fatal("expected the invalid startup token to be rejected")
	}
	if m.Status().Licensed {
		t.Fatal("an invalid token was accepted")
	}

	// The operator notices and drops in the right key.
	os.WriteFile(path, []byte(termToken(t, m, priv, "Acme", 12)), 0o600)
	changed, err := m.ReloadFromFile()
	if err != nil {
		t.Fatalf("reload after the fix: %v", err)
	}
	if !changed {
		t.Fatal("the corrected file was treated as unchanged")
	}
	if got := m.Status().Customer; got != "Acme" {
		t.Fatalf("customer = %q, want the corrected license", got)
	}
}
