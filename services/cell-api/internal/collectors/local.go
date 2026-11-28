// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

// KeptByLocal lists the settings a change removes that the collector will
// keep anyway because its local configuration files set them.
//
// The supervisor merges what is sent over its local files, so a change
// can add and override but not remove what the local files set. Sluicio
// cannot read those files, but it can tell: current is the configuration
// as written the collector reports (its merge), sent the last
// configuration Sluicio sent it ("" if none). A map key that the change
// removes from current and that Sluicio never sent can only have come
// from the local files (or the supervisor's own OpAMP extension, which is
// left out), so it stays. Paths only, never values.
func KeptByLocal(current, sent, change string) ([]string, error) {
	var c, s, n any
	if err := yaml.Unmarshal([]byte(current), &c); err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal([]byte(change), &n); err != nil {
		return nil, err
	}
	_ = yaml.Unmarshal([]byte(sent), &s)
	out := []string{}
	removedKeys(&out, "", c, n, s)
	sort.Strings(out)
	return out, nil
}

func removedKeys(out *[]string, path string, current, change, sent any) {
	cm, ok := current.(map[string]any)
	if !ok {
		return // a value or a list: a change replaces it, no removal to warn of
	}
	nm, _ := change.(map[string]any)
	sm, _ := sent.(map[string]any)
	for k, cv := range cm {
		p := k
		if path != "" {
			p = path + "." + k
		}
		if p == "extensions.opamp" {
			continue // the supervisor's own, merged in by it
		}
		var sv any
		if sm != nil {
			sv = sm[k]
		}
		nv, kept := nm[k]
		if !kept {
			if _, isMap := cv.(map[string]any); isMap && len(cv.(map[string]any)) > 0 {
				// A whole section removed: report what is in it, key by key,
				// so the supervisor's own extension is still left out.
				removedKeys(out, p, cv, map[string]any{}, sv)
			} else if sv == nil {
				*out = append(*out, p)
			}
			continue
		}
		removedKeys(out, p, cv, nv, sv)
	}
}

// LastSent is the configuration the collector last applied from Sluicio,
// "" when it never applied one.
func (s *Store) LastSent(ctx context.Context, collectorID uuid.UUID) (string, error) {
	if s.keys == nil {
		return "", ErrNoKey
	}
	var hash, sealed string
	err := s.pool.QueryRow(ctx, `SELECT config_hash, config_enc FROM collector_deployments
		WHERE collector_id = $1 AND status = 'applied' ORDER BY finished_at DESC NULLS LAST, created_at DESC LIMIT 1`,
		collectorID).Scan(&hash, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	files, err := s.keys.Open(collectorID, hash, sealed)
	if err != nil || len(files) != 1 {
		return "", err
	}
	return strings.TrimSpace(files[0].Body), nil
}
