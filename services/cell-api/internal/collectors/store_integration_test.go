// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Collector registry against a real Postgres: token authentication and
// team assignment, the per-team instance UID, encryption at rest, the
// read scope, and the rule that no collector is ever left without a team.
//
//	go test -tags integration ./services/cell-api/internal/collectors/...
package collectors_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
)

func TestTokenLifecycle(t *testing.T) {
	pool, ctx := newDB(t)
	org := seedOrg(t, ctx, pool, "acme")
	other := seedOrg(t, ctx, pool, "other")
	team := seedTeam(t, ctx, pool, org, "payments")
	foreign := seedTeam(t, ctx, pool, other, "theirs")
	s := collectors.New(pool, newKeys(t))

	full, tok, err := s.CreateToken(ctx, org, team, "prod hosts", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(full, "sct_") || tok.Prefix != full[:12] {
		t.Fatalf("token %q, prefix %q", full, tok.Prefix)
	}
	var stored int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM collector_tokens WHERE token_hash = $1 OR prefix = $1 OR name = $1`, full).Scan(&stored)
	if stored != 0 {
		t.Fatal("the plaintext token is stored")
	}

	ref, err := s.Authenticate(ctx, full)
	if err != nil || ref.GroupID != team || ref.OrgID != org || ref.TokenID != tok.ID {
		t.Fatalf("Authenticate = %+v, %v", ref, err)
	}
	for _, bad := range []string{"", "sct_nope", full + "x", strings.TrimPrefix(full, "sct_")} {
		if _, err := s.Authenticate(ctx, bad); !errors.Is(err, collectors.ErrUnauthenticated) {
			t.Errorf("Authenticate(%q) err = %v", bad, err)
		}
	}

	// A team of another organization cannot be given a token from here.
	if _, _, err := s.CreateToken(ctx, org, foreign, "x", nil); !errors.Is(err, collectors.ErrNotFound) {
		t.Fatalf("token for another org's team: %v", err)
	}

	// Regenerate: the old secret stops at once, the new one lands in the same team.
	newFull, _, err := s.RegenerateToken(ctx, org, team, tok.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, full); !errors.Is(err, collectors.ErrUnauthenticated) {
		t.Fatal("the old secret still authenticates after regenerate")
	}
	if ref, err := s.Authenticate(ctx, newFull); err != nil || ref.GroupID != team {
		t.Fatalf("regenerated token: %+v %v", ref, err)
	}

	// Two live tokens at once: rotation without an outage.
	second, _, _ := s.CreateToken(ctx, org, team, "rotation", nil)
	if _, err := s.Authenticate(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, newFull); err != nil {
		t.Fatal("creating a second token must not invalidate the first")
	}

	// Revoke, scoped to the team: another team's id does not reach it.
	if err := s.RevokeToken(ctx, org, foreign, tok.ID); !errors.Is(err, collectors.ErrNotFound) {
		t.Fatalf("revoke through another team: %v", err)
	}
	if err := s.RevokeToken(ctx, org, team, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, newFull); !errors.Is(err, collectors.ErrUnauthenticated) {
		t.Fatal("a revoked token still authenticates")
	}
	if list, _ := s.ListTokens(ctx, org, team); len(list) != 1 || list[0].Name != "rotation" {
		t.Fatalf("live tokens = %+v", list)
	}
}

func TestInstanceUIDIsPerTeam(t *testing.T) {
	pool, ctx := newDB(t)
	org := seedOrg(t, ctx, pool, "acme")
	a, b := seedTeam(t, ctx, pool, org, "a"), seedTeam(t, ctx, pool, org, "b")
	s := collectors.New(pool, newKeys(t))
	_, tokA, _ := s.CreateToken(ctx, org, a, "a", nil)
	_, tokB, _ := s.CreateToken(ctx, org, b, "b", nil)

	caps := uint64(1 | 2 | 4)
	uid := uuid.NewString()
	idA, created, err := s.Observe(ctx, collectors.Observation{OrgID: org, GroupID: a, TokenID: tokA.ID, InstanceUID: uid, Transport: "websocket", Capabilities: &caps,
		Description: map[string]string{"host.name": "h1"}})
	if err != nil || !created {
		t.Fatalf("first observe: %v created=%v", err, created)
	}
	again, created, _ := s.Observe(ctx, collectors.Observation{OrgID: org, GroupID: a, TokenID: tokA.ID, InstanceUID: uid, Transport: "websocket"})
	if again != idA || created {
		t.Fatal("the same agent in the same team must be the same collector")
	}
	idB, created, _ := s.Observe(ctx, collectors.Observation{OrgID: org, GroupID: b, TokenID: tokB.ID, InstanceUID: uid, Transport: "http"})
	if idB == idA || !created {
		t.Fatal("the same instance UID with another team's token must be a new collector in that team")
	}

	all, _ := s.List(ctx, collectors.Scope{OrgID: org, All: true})
	if len(all) != 2 {
		t.Fatalf("collectors = %d, want 2", len(all))
	}
	for _, c := range all {
		if c.ID == idA {
			// A message without capabilities or description leaves them.
			if c.Kind != collectors.KindSupervisor || c.Description["host.name"] != "h1" {
				t.Fatalf("partial report overwrote stored state: %+v", c)
			}
			if !c.Online {
				t.Fatal("a connected websocket agent reads offline")
			}
		}
	}
	if err := s.Disconnected(ctx, a, uid); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Get(ctx, collectors.Scope{OrgID: org, All: true}, idA); c.Online {
		t.Fatal("a closed websocket agent reads online")
	}
}

func TestScopeFailsClosed(t *testing.T) {
	pool, ctx := newDB(t)
	org := seedOrg(t, ctx, pool, "acme")
	other := seedOrg(t, ctx, pool, "other")
	a, b := seedTeam(t, ctx, pool, org, "a"), seedTeam(t, ctx, pool, org, "b")
	x := seedTeam(t, ctx, pool, other, "x")
	s := collectors.New(pool, newKeys(t))
	ids := map[uuid.UUID]uuid.UUID{}
	for _, tc := range []struct{ org, team uuid.UUID }{{org, a}, {org, b}, {other, x}} {
		_, tok, _ := s.CreateToken(ctx, tc.org, tc.team, "t", nil)
		id, _, err := s.Observe(ctx, collectors.Observation{OrgID: tc.org, GroupID: tc.team, TokenID: tok.ID, InstanceUID: uuid.NewString(), Transport: "websocket"})
		if err != nil {
			t.Fatal(err)
		}
		ids[tc.team] = id
	}

	onlyA, _ := s.List(ctx, collectors.Scope{OrgID: org, Groups: []uuid.UUID{a}})
	if len(onlyA) != 1 || onlyA[0].GroupID != a {
		t.Fatalf("team a sees %+v", onlyA)
	}
	if none, _ := s.List(ctx, collectors.Scope{OrgID: org}); len(none) != 0 {
		t.Fatal("an empty scope must read nothing")
	}
	if _, err := s.Get(ctx, collectors.Scope{OrgID: org, Groups: []uuid.UUID{a}}, ids[b]); !errors.Is(err, collectors.ErrNotFound) {
		t.Fatal("team a reads team b's collector")
	}
	if all, _ := s.List(ctx, collectors.Scope{OrgID: org, All: true}); len(all) != 2 {
		t.Fatalf("org admin sees %d collectors, want 2 (never another org's)", len(all))
	}
	// A team id of another org in the scope does not open that org.
	if leak, _ := s.List(ctx, collectors.Scope{OrgID: org, Groups: []uuid.UUID{x}}); len(leak) != 0 {
		t.Fatal("another organization's team read through the scope")
	}
}

func TestConfigEncryptedAtRest(t *testing.T) {
	pool, ctx := newDB(t)
	org := seedOrg(t, ctx, pool, "acme")
	team := seedTeam(t, ctx, pool, org, "a")
	s := collectors.New(pool, newKeys(t))
	_, tok, _ := s.CreateToken(ctx, org, team, "t", nil)
	id, _, _ := s.Observe(ctx, collectors.Observation{OrgID: org, GroupID: team, TokenID: tok.ID, InstanceUID: uuid.NewString(), Transport: "websocket"})

	v1 := []collectors.ConfigFile{{ContentType: "text/yaml", Body: "exporters:\n  otlp_http:\n    endpoint: https://internal-gw.corp.example:4318\n    headers:\n      authorization: Bearer topsecret-123\n"}}
	if changed, err := s.RecordConfig(ctx, id, v1); err != nil || !changed {
		t.Fatalf("first config: %v %v", changed, err)
	}
	if changed, _ := s.RecordConfig(ctx, id, v1); changed {
		t.Fatal("an unchanged configuration was stored again")
	}
	v2 := []collectors.ConfigFile{{ContentType: "text/yaml", Body: strings.Replace(v1[0].Body, "4318", "4319", 1)}}
	if changed, _ := s.RecordConfig(ctx, id, v2); !changed {
		t.Fatal("a changed configuration was not stored")
	}

	// What a database reader sees.
	rows, err := pool.Query(ctx, `SELECT config_hash, config_enc FROM collector_configs`)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		var hash, enc string
		_ = rows.Scan(&hash, &enc)
		n++
		for _, leak := range []string{"topsecret", "internal-gw", "exporters", "authorization", "otlp_http"} {
			if strings.Contains(hash+enc, leak) {
				t.Fatalf("stored row leaks %q", leak)
			}
		}
	}
	rows.Close()
	if n != 2 {
		t.Fatalf("history rows = %d, want 2", n)
	}

	hist, _ := s.History(ctx, id, 10)
	if len(hist) != 2 {
		t.Fatalf("history = %+v", hist)
	}
	latest, files, err := s.MaskedConfig(ctx, id, nil)
	if err != nil || latest.ID != hist[0].ID {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
	if strings.Contains(files[0].Body, "topsecret") || !strings.Contains(files[0].Body, "4319") {
		t.Fatalf("masked latest:\n%s", files[0].Body)
	}
	oldest, files, _ := s.MaskedConfig(ctx, id, &hist[1].ID)
	if oldest.ID != hist[1].ID || !strings.Contains(files[0].Body, "4318") {
		t.Fatalf("older version: %+v\n%s", oldest, files[0].Body)
	}

	// Ciphertext moved onto another collector's row does not open there.
	id2, _, _ := s.Observe(ctx, collectors.Observation{OrgID: org, GroupID: team, TokenID: tok.ID, InstanceUID: uuid.NewString(), Transport: "websocket"})
	if _, err := pool.Exec(ctx, `INSERT INTO collector_configs (collector_id, config_hash, config_enc)
		SELECT $1, config_hash, config_enc FROM collector_configs WHERE collector_id = $2 LIMIT 1`, id2, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.MaskedConfig(ctx, id2, nil); err == nil {
		t.Fatal("a configuration copied to another collector opened")
	}

	// Another cell's key reads nothing.
	if _, _, err := collectors.New(pool, newKeys(t)).MaskedConfig(ctx, id, nil); err == nil {
		t.Fatal("another key opened the configuration")
	}
}

func TestNoKeyStoresNoConfig(t *testing.T) {
	pool, ctx := newDB(t)
	org := seedOrg(t, ctx, pool, "acme")
	team := seedTeam(t, ctx, pool, org, "a")
	s := collectors.New(pool, nil)
	_, tok, _ := s.CreateToken(ctx, org, team, "t", nil)
	id, _, err := s.Observe(ctx, collectors.Observation{OrgID: org, GroupID: team, TokenID: tok.ID, InstanceUID: uuid.NewString(), Transport: "websocket"})
	if err != nil {
		t.Fatal("a cell without a key must still register collectors")
	}
	if _, err := s.RecordConfig(ctx, id, []collectors.ConfigFile{{Body: "a: 1"}}); !errors.Is(err, collectors.ErrNoKey) {
		t.Fatalf("RecordConfig without key: %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM collector_configs`).Scan(&n)
	if n != 0 {
		t.Fatal("a configuration was stored without a key")
	}
}

func TestNoTeamlessCollectors(t *testing.T) {
	pool, ctx := newDB(t)
	org := seedOrg(t, ctx, pool, "acme")
	team := seedTeam(t, ctx, pool, org, "a")
	empty := seedTeam(t, ctx, pool, org, "empty")
	s := collectors.New(pool, newKeys(t))
	_, tok, _ := s.CreateToken(ctx, org, team, "t", nil)
	_, _, _ = s.CreateToken(ctx, org, empty, "t", nil)
	if _, _, err := s.Observe(ctx, collectors.Observation{OrgID: org, GroupID: team, TokenID: tok.ID, InstanceUID: "x", Transport: "websocket"}); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, team); err == nil {
		t.Fatal("a team that owns collectors was deleted")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, empty); err != nil {
		t.Fatalf("a team without collectors could not be deleted: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO collectors (organization_id, group_id, instance_uid) VALUES ($1, NULL, 'y')`, org); err == nil {
		t.Fatal("a collector without a team was accepted")
	}
	// Deleting the whole organization still works and takes everything.
	if _, err := pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, org); err != nil {
		t.Fatalf("org delete blocked: %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM collectors) + (SELECT count(*) FROM collector_tokens)`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows left after the org was deleted", n)
	}
}
