// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Payload links against a real Postgres: an organization starts with the
// defaults, a save replaces the list (an empty list stays empty rather
// than falling back to the defaults), and configuration transfer carries
// the list from one organization to another.
package payloadlinks_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/configtransfer"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/payloadlinks"
)

func newDB(t *testing.T) (*pgxpool.Pool, uuid.UUID, context.Context) {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("controlplane"),
		tcpostgres.WithUsername("controlplane"),
		tcpostgres.WithPassword("controlplane"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	pool, err := impostgres.Pool(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := impostgres.Migrate(ctx, pool, migrations.FS, migrations.Dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	orgs, err := identity.NewStore(pool).ListOrgs(ctx)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("no seeded org: %v", err)
	}
	return pool, orgs[0].Org.ID, ctx
}

func TestStoreAndTransfer(t *testing.T) {
	pool, org, ctx := newDB(t)
	s := payloadlinks.NewStore(pool)

	got, err := s.Get(ctx, org)
	if err != nil || len(got) != 1 || got[0].Attribute != payloadlinks.DefaultAttribute {
		t.Fatalf("a new organization has the defaults: %+v, %v", got, err)
	}

	// Never saved: the export leaves the section out, so a target keeps
	// its own list.
	b := exportBundle(t, pool, ctx, org)
	if b.Sections.PayloadLinks != nil {
		t.Fatalf("an untouched list was exported: %+v", *b.Sections.PayloadLinks)
	}

	want := []payloadlinks.Attribute{
		{Attribute: "archive.path", Label: "Archived file", BaseURL: "https://archive.example.com/files"},
		{Attribute: payloadlinks.DefaultAttribute, Label: "Payload"},
	}
	if err := s.Put(ctx, org, want); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, org); len(got) != 2 || got[0] != want[0] {
		t.Fatalf("saved %+v, read %+v", want, got)
	}

	b = exportBundle(t, pool, ctx, org)
	if b.Sections.PayloadLinks == nil || len(*b.Sections.PayloadLinks) != 2 || (*b.Sections.PayloadLinks)[0].BaseURL != want[0].BaseURL {
		t.Fatalf("export: %+v", b.Sections.PayloadLinks)
	}

	// Into a second organization.
	other := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, slug, name) VALUES ($1, 'other', 'Other')`, other); err != nil {
		t.Fatal(err)
	}
	// Only this section: the rest of the seeded organization's bundle is
	// the other sections' tests to carry.
	only := &configtransfer.Bundle{FormatVersion: configtransfer.FormatVersion}
	only.Sections.PayloadLinks = b.Sections.PayloadLinks
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rep := &configtransfer.Report{}
	if err := configtransfer.Import(ctx, tx, other.String(), only, configtransfer.Options{Mode: configtransfer.ModeStrict}, rep); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, other); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("imported %+v", got)
	}

	// Removing every attribute is a choice, not a return to the defaults.
	if err := s.Put(ctx, org, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, org); len(got) != 0 {
		t.Fatalf("an emptied list came back as %+v", got)
	}

	// A bundle with an invalid base URL fails the import as a whole.
	bad := &configtransfer.Bundle{FormatVersion: configtransfer.FormatVersion}
	bad.Sections.PayloadLinks = &[]configtransfer.PayloadLink{{Attribute: "x", BaseURL: "javascript:alert(1)"}}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := configtransfer.Import(ctx, tx, other.String(), bad, configtransfer.Options{Mode: configtransfer.ModeStrict}, &configtransfer.Report{}); err == nil {
		t.Fatal("an invalid base URL was imported")
	}
}

func exportBundle(t *testing.T, pool *pgxpool.Pool, ctx context.Context, org uuid.UUID) *configtransfer.Bundle {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	b, err := configtransfer.Export(ctx, tx, org.String(), "test")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
