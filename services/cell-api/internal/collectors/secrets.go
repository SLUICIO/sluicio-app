// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

// A secret written into a collector's configuration, rather than taken
// from an environment variable, sits in a file on the host and in every
// copy of it. Sluicio stores configurations encrypted and shows them
// masked whatever they hold; this tells the team where a configuration
// holds one, so it can be moved into a variable.
//
// What a collector reports cannot show every one. Checked on 0.166.0,
// extension and supervisor alike: in the configuration as written an
// opaque value (headers, passwords, tokens) reads [REDACTED] unless it is
// a bare ${env:NAME}, so a literal and "Bearer ${env:TOKEN}" look the
// same there, and neither counts. A plain string field (an endpoint with
// credentials in it, an attribute value) is reported as written. A
// configuration Sluicio sent is known as written, so the one the
// collector last applied is read as well. In the effective configuration
// every reference is resolved, so it is never read for this.

// PlainSecrets lists the paths in a configuration as written whose
// values the mask hides and that take nothing from a variable: a value
// with a ${...} reference in it (Bearer ${env:TOKEN}) is not plain text.
// Paths only, never values. Nil when there is none, or body is not YAML.
func PlainSecrets(body string) []string {
	var orig, masked yaml.Node
	if yaml.Unmarshal([]byte(body), &orig) != nil || yaml.Unmarshal([]byte(body), &masked) != nil {
		return nil
	}
	maskDocument(&masked)
	var out []string
	var walk func(a, b *yaml.Node, path string)
	walk = func(a, b *yaml.Node, path string) {
		if a.Kind != b.Kind || len(a.Content) != len(b.Content) {
			return
		}
		switch a.Kind {
		case yaml.DocumentNode:
			for i := range a.Content {
				walk(a.Content[i], b.Content[i], path)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(a.Content); i += 2 {
				p := a.Content[i].Value
				if path != "" {
					p = path + "." + p
				}
				walk(a.Content[i+1], b.Content[i+1], p)
			}
		case yaml.SequenceNode:
			for i := range a.Content {
				walk(a.Content[i], b.Content[i], path+"["+strconv.Itoa(i)+"]")
			}
		case yaml.ScalarNode:
			if a.Value != b.Value && a.Value != Masked && a.Value != collectorRedacted && !strings.Contains(a.Value, "${") {
				out = append(out, path)
			}
		}
	}
	walk(&orig, &masked, "")
	return out
}

// PlainSecretsNotice is a collector whose configuration gained secrets in
// plain text: Added are the paths that were not there before.
type PlainSecretsNotice struct {
	OrgID       uuid.UUID
	GroupID     uuid.UUID
	CollectorID uuid.UUID
	Name        string
	Added       []string
	All         []string
}

// checkPlainSecrets stores where the collector's configuration holds a
// plain-text secret, as written (raw; empty: read the newest stored
// version) and in the configuration Sluicio last saw it apply, and tells
// OnPlainSecrets about paths that are new.
func (s *Store) checkPlainSecrets(ctx context.Context, collectorID uuid.UUID, raw string) error {
	if raw == "" {
		files, err := s.latestFiles(ctx, collectorID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		for _, f := range files {
			if f.Name == RawConfigFile {
				raw = f.Body
			}
		}
	}
	applied, err := s.appliedBody(ctx, collectorID)
	if err != nil {
		return err
	}
	if raw == "" && applied == "" {
		return nil // effective only, nothing sent: nothing can be told
	}
	paths := PlainSecrets(raw)
	for _, p := range PlainSecrets(applied) {
		if !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	if paths == nil {
		paths = []string{}
	}
	b, _ := json.Marshal(paths)
	var old []byte
	var n PlainSecretsNotice
	err = s.pool.QueryRow(ctx, `
		UPDATE collectors c SET plain_secrets = $2, plain_secrets_checked_at = now(),
		       plain_secrets_at = CASE WHEN $2::jsonb = '[]'::jsonb THEN NULL
		                               ELSE COALESCE(c.plain_secrets_at, now()) END
		FROM (SELECT plain_secrets FROM collectors WHERE id = $1) o
		WHERE c.id = $1
		RETURNING o.plain_secrets, c.organization_id, c.group_id,
		          COALESCE(NULLIF(c.description->>'host.name', ''), c.instance_uid)`,
		collectorID, b).Scan(&old, &n.OrgID, &n.GroupID, &n.Name)
	if err != nil {
		return fmt.Errorf("collectors: plain secrets: %w", err)
	}
	var before []string
	_ = json.Unmarshal(old, &before)
	for _, p := range paths {
		if !slices.Contains(before, p) {
			n.Added = append(n.Added, p)
		}
	}
	if len(n.Added) > 0 && s.OnPlainSecrets != nil {
		n.CollectorID, n.All = collectorID, paths
		s.OnPlainSecrets(ctx, n)
	}
	return nil
}

// plainSecretsUnchecked says whether the collector's stored configuration
// was never checked (stored before the check existed).
func (s *Store) plainSecretsUnchecked(ctx context.Context, collectorID uuid.UUID) bool {
	var at *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT plain_secrets_checked_at FROM collectors WHERE id = $1`, collectorID).Scan(&at); err != nil {
		return false
	}
	return at == nil
}
