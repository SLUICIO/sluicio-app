// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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

// PlainSecrets lists the paths in a configuration as written that hold a
// secret in plain text. Paths only, never values. Nil when there is none,
// or body is not YAML.
//
// The rule is OTelFlow's (its validator warns about the same values), and
// narrower than the mask on purpose: the mask may hide too much, a notice
// to a team must not accuse a value that is none. What counts is decided
// by the value's own key (password, token, api_key, authorization, ...,
// but not token_url, client_secret_file or a whole block below auth), a
// header given as a name/value pair, a TLS key, a PEM private key under
// any key, and a password or secret-named query parameter in a URL. A
// value with a ${...} reference in it, a number, a boolean and a
// placeholder ([REDACTED], asterisks) never count. A component's ID is a
// name, not a value.
func PlainSecrets(body string) []string {
	var doc yaml.Node
	if yaml.Unmarshal([]byte(body), &doc) != nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	var out []string
	var walk func(n *yaml.Node, path string, secret, inTLS bool)
	walk = func(n *yaml.Node, path string, secret, inTLS bool) {
		switch n.Kind {
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, path+"["+strconv.Itoa(i)+"]", secret, false)
			}
		case yaml.MappingNode:
			// A pair spelled {name: authorization, value: ...}.
			named := false
			for i := 0; i+1 < len(n.Content); i += 2 {
				if k := n.Content[i].Value; (k == "name" || k == "key") && n.Content[i+1].Kind == yaml.ScalarNode && plainSecretKey(n.Content[i+1].Value) {
					named = true
				}
			}
			// A key's secrecy covers its value or a list of values, never a
			// whole map below it: under client_auth the username is no
			// secret, the password is, by its own key.
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i].Value
				walk(n.Content[i+1], path+"."+k, plainSecretKey(k) || (named && k == "value") || (inTLS && k == "key"), tlsBlock(k))
			}
		case yaml.ScalarNode:
			v := strings.TrimSpace(n.Value)
			if v == "" || strings.Contains(v, "${") || placeholder(v) || n.Tag == "!!bool" || n.Tag == "!!null" || n.Tag == "!!int" || n.Tag == "!!float" {
				return
			}
			if secret || (strings.Contains(v, "-----BEGIN") && strings.Contains(v, "PRIVATE KEY")) || urlCredential(v) {
				out = append(out, path)
			}
		}
	}
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		key, val := top.Content[i].Value, top.Content[i+1]
		switch {
		case componentSections[key] && val.Kind == yaml.MappingNode:
			for j := 0; j+1 < len(val.Content); j += 2 {
				walk(val.Content[j+1], key+"."+val.Content[j].Value, false, false)
			}
		case key == "service":
			if t := mapGet(val, "telemetry"); t != nil {
				walk(t, "service.telemetry", false, false)
			}
		}
	}
	return out
}

// notSecretSuffixes end keys that name something about a secret rather
// than the secret: token_url, client_secret_file, token_expiry.
var notSecretSuffixes = []string{"url", "uri", "endpoint", "file", "path", "expiry", "timeout", "interval", "buffer", "duration", "ttl"}

// plainSecretKey is the narrower test PlainSecrets uses: sensitiveKey,
// without "auth" (a block holding a mechanism and a user name as much as
// a password), the authenticator reference and keys about a secret.
func plainSecretKey(k string) bool {
	n := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(k))
	if n == "auth" || n == "authenticator" {
		return false
	}
	for _, s := range notSecretSuffixes {
		if strings.HasSuffix(n, s) {
			return false
		}
	}
	return sensitiveKey(k)
}

// placeholder is a value a tool put in place of a secret.
func placeholder(v string) bool {
	return v == collectorRedacted || strings.Trim(v, "*") == ""
}

// urlCredential says whether a URL carries a password or a secret-named
// query parameter in plain text.
func urlCredential(v string) bool {
	if !strings.Contains(v, "://") {
		return false
	}
	u, err := url.Parse(v)
	if err != nil {
		return false
	}
	if u.User != nil {
		if p, ok := u.User.Password(); ok && !placeholder(p) {
			return true
		}
	}
	for k, vals := range u.Query() {
		if sensitiveKey(k) || k == "sig" || k == "code" {
			for _, q := range vals {
				if q != "" && !placeholder(q) {
					return true
				}
			}
		}
	}
	return false
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
