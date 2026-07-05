// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Integration test for the identity Store against a real Postgres,
// spun up with testcontainers-go. Unlike the pure-logic unit tests,
// this exercises the actual SQL + the embedded migrations (including
// the 0017 admin/org seed and the 0023 email rebrand), so it catches
// schema/query drift the unit layer can't.
//
// Build-tagged `integration` so the fast `go test ./...` run never
// needs Docker/Podman. Run it with:
//
//	go test -tags integration ./services/cell-api/internal/identity/...
//
// or `make test-integration`.
package identity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/integration-monitor/integration-monitor/pkg/postgres"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/migrations"
)

const seedAdminEmail = "admin@sluicio.local"

// newMigratedStore brings up a throwaway Postgres, applies the cell-api
// migrations, and returns a Store wired to it. The container is torn
// down via t.Cleanup.
func newMigratedStore(t *testing.T) (*identity.Store, context.Context) {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("controlplane"),
		tcpostgres.WithUsername("controlplane"),
		tcpostgres.WithPassword("controlplane"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(pg); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := impostgres.Pool(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := impostgres.Migrate(ctx, pool, migrations.FS, migrations.Dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return identity.NewStore(pool), ctx
}

// TestStoreSeedAndAuth walks the real bootstrap → authenticate path the
// cell-api relies on at startup.
func TestStoreSeedAndAuth(t *testing.T) {
	store, ctx := newMigratedStore(t)

	// The 0017 seed (rebranded by 0023) must leave a usable admin row.
	admin, err := store.GetUserByEmail(ctx, seedAdminEmail)
	if err != nil {
		t.Fatalf("seeded admin not found: %v", err)
	}
	if admin.PasswordHash != "" {
		t.Fatalf("freshly migrated admin should have no password yet, got a hash")
	}

	// Before bootstrap, login must fail closed (no local password set).
	if _, err := store.AuthenticatePassword(ctx, seedAdminEmail, "admin"); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("auth before bootstrap: want ErrInvalidCredentials, got %v", err)
	}

	// Bootstrap is what cell-api's main() runs on first boot.
	if err := store.BootstrapSeedAdminPassword(ctx, seedAdminEmail, "admin"); err != nil {
		t.Fatalf("bootstrap seed password: %v", err)
	}

	// Now the correct password authenticates and resolves the same user.
	got, err := store.AuthenticatePassword(ctx, seedAdminEmail, "admin")
	if err != nil {
		t.Fatalf("auth after bootstrap: %v", err)
	}
	if got.ID != admin.ID {
		t.Fatalf("authenticated user id = %s, want %s", got.ID, admin.ID)
	}

	// A wrong password is still rejected.
	if _, err := store.AuthenticatePassword(ctx, seedAdminEmail, "wrong"); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("auth with wrong password: want ErrInvalidCredentials, got %v", err)
	}

	// Bootstrap is idempotent — a second call must not error or re-hash
	// (the WHERE password_hash='' guard means it's a no-op now).
	if err := store.BootstrapSeedAdminPassword(ctx, seedAdminEmail, "different"); err != nil {
		t.Fatalf("second bootstrap should be a no-op, got: %v", err)
	}
	if _, err := store.AuthenticatePassword(ctx, seedAdminEmail, "admin"); err != nil {
		t.Fatalf("original password should still work after no-op bootstrap: %v", err)
	}
}

// TestStoreSeedMembership checks the seeded admin belongs to the seeded
// org as an admin — the membership the SPA reads on login.
func TestStoreSeedMembership(t *testing.T) {
	store, ctx := newMigratedStore(t)

	admin, err := store.GetUserByEmail(ctx, seedAdminEmail)
	if err != nil {
		t.Fatalf("seeded admin not found: %v", err)
	}

	mems, err := store.ListMemberships(ctx, admin.ID)
	if err != nil {
		t.Fatalf("list memberships: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("seeded admin memberships = %d, want 1", len(mems))
	}
	if mems[0].Role != identity.RoleAdmin {
		t.Fatalf("seeded membership role = %q, want %q", mems[0].Role, identity.RoleAdmin)
	}
}
