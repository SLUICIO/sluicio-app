// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

// Drift: whether a collector still runs what was last rolled out to it,
// and nothing else changed since.
//
// The configuration as written that a supervisor reports is its merge of
// its local files, its OpAMP extension and the configuration sent last,
// in that order, maps merged and lists replaced. Two things are checked
// against it:
//
//   - Everything sent is still there with the same value, a list exactly
//     as sent (missing, changed). The supervisor merges what was sent
//     last, so this only fails when something on the host goes around it.
//   - Nothing changed since the rollout: the configuration now against the
//     one the collector reported right after the last deployment reached
//     it (added, removed, changed). This is the drift that happens: the
//     local files edited by hand or by another pipeline, the collector's
//     configuration changed outside Sluicio.
//
// Settings from the local files that were there at the rollout are not
// drift; they were always allowed. Only paths are kept, never values,
// which may be secrets.
//
// A change made on the host that is meant to stay is accepted (see
// AcceptDrift): the configuration reported then becomes the baseline,
// and drift is only what changed after it, until the next rollout from
// Sluicio sets a new one. Nothing is sent to the collector.

// DriftPath is one place where the collector differs from what was sent.
type DriftPath struct {
	Path string `json:"path"`
	// Kind is "missing" (sent, not in the collector's configuration),
	// "changed" (a different value than sent, or than at the rollout),
	// "added" or "removed" (since the rollout, outside what was sent).
	Kind string `json:"kind"`
}

// Drift is the result of the last check.
type Drift struct {
	// DeploymentID is the deployment compared with: the last one the
	// collector applied.
	DeploymentID uuid.UUID   `json:"deployment_id"`
	Paths        []DriftPath `json:"paths"`
	CheckedAt    time.Time   `json:"checked_at"`
	// AcceptedAt and AcceptedBy: the baseline is a configuration someone
	// accepted after the rollout, not the rollout itself.
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	AcceptedBy string     `json:"accepted_by,omitempty"`
}

// ErrNothingToAccept: the collector has no drift result to accept (not
// applicable, or no configuration reported).
var ErrNothingToAccept = errors.New("collectors: nothing to accept")

// AcceptDrift takes the configuration the collector reports now as the
// baseline drift is measured from, and checks again (in sync, unless it
// changes again). byName is who accepted it.
func (s *Store) AcceptDrift(ctx context.Context, collectorID uuid.UUID, byName string) (*Drift, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE collectors SET drift_accepted_config_id = (
		           SELECT id FROM collector_configs WHERE collector_id = $1 ORDER BY received_at DESC, id DESC LIMIT 1),
		       drift_accepted_at = now(), drift_accepted_by_name = $2
		WHERE id = $1 AND drift IS NOT NULL`, collectorID, byName)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNothingToAccept
	}
	if err := s.CheckDrift(ctx, collectorID); err != nil {
		return nil, err
	}
	var d *Drift
	err = s.pool.QueryRow(ctx, `SELECT drift FROM collectors WHERE id = $1`, collectorID).Scan(&d)
	return d, err
}

// maxDriftPaths caps what is kept: past a few dozen differences, the
// list stops telling anyone more than "most of it".
const maxDriftPaths = 50

// DiffSent lists where reported (the configuration as written) differs
// from sent (what was last rolled out). Both are YAML.
func DiffSent(sent, reported string) ([]DriftPath, error) {
	var s, r any
	if err := yaml.Unmarshal([]byte(sent), &s); err != nil {
		return nil, fmt.Errorf("the configuration sent is not YAML: %w", err)
	}
	if err := yaml.Unmarshal([]byte(reported), &r); err != nil {
		return nil, fmt.Errorf("the reported configuration is not YAML: %w", err)
	}
	out := []DriftPath{}
	if s == nil {
		// Nothing was sent (back to the local files): nothing to check.
		return out, nil
	}
	diffInto(&out, "", s, r)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if len(out) > maxDriftPaths {
		out = out[:maxDriftPaths]
	}
	return out, nil
}

// DiffSince lists what changed between before (the configuration the
// collector reported right after the rollout) and now. Both are YAML.
func DiffSince(before, now string) ([]DriftPath, error) {
	var b, n any
	if err := yaml.Unmarshal([]byte(before), &b); err != nil {
		return nil, fmt.Errorf("the earlier configuration is not YAML: %w", err)
	}
	if err := yaml.Unmarshal([]byte(now), &n); err != nil {
		return nil, fmt.Errorf("the reported configuration is not YAML: %w", err)
	}
	out := []DriftPath{}
	changesInto(&out, "", b, n)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func changesInto(out *[]DriftPath, path string, before, now any) {
	bm, bok := before.(map[string]any)
	nm, nok := now.(map[string]any)
	if !bok || !nok {
		if !reflect.DeepEqual(before, now) {
			*out = append(*out, DriftPath{Path: path, Kind: "changed"})
		}
		return
	}
	join := func(k string) string {
		if path == "" {
			return k
		}
		return path + "." + k
	}
	for k, bv := range bm {
		nv, ok := nm[k]
		if !ok {
			*out = append(*out, DriftPath{Path: join(k), Kind: "removed"})
			continue
		}
		changesInto(out, join(k), bv, nv)
	}
	for k := range nm {
		if _, ok := bm[k]; !ok {
			*out = append(*out, DriftPath{Path: join(k), Kind: "added"})
		}
	}
}

// mergeDrift joins both checks, one entry per path, sorted and capped.
func mergeDrift(a, b []DriftPath) []DriftPath {
	seen := map[string]bool{}
	out := []DriftPath{}
	for _, list := range [][]DriftPath{a, b} {
		for _, p := range list {
			if !seen[p.Path] {
				seen[p.Path] = true
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if len(out) > maxDriftPaths {
		out = out[:maxDriftPaths]
	}
	return out
}

func diffInto(out *[]DriftPath, path string, sent, reported any) {
	sm, ok := sent.(map[string]any)
	if !ok {
		// A scalar or a list: the collector must have exactly this.
		if !reflect.DeepEqual(sent, reported) {
			*out = append(*out, DriftPath{Path: path, Kind: "changed"})
		}
		return
	}
	rm, ok := reported.(map[string]any)
	if !ok {
		if reported == nil {
			if len(sm) > 0 || path == "" {
				*out = append(*out, DriftPath{Path: path, Kind: "missing"})
			}
			return
		}
		*out = append(*out, DriftPath{Path: path, Kind: "changed"})
		return
	}
	for k, sv := range sm {
		p := k
		if path != "" {
			p = path + "." + k
		}
		rv, present := rm[k]
		if !present {
			*out = append(*out, DriftPath{Path: p, Kind: "missing"})
			continue
		}
		// An empty map sent ({}) is satisfied by the key being there,
		// whatever the local files add under it.
		if m, isMap := sv.(map[string]any); isMap && len(m) == 0 {
			continue
		}
		diffInto(out, p, sv, rv)
	}
}

// CheckDrift compares the collector's newest configuration as written with
// the last deployment it applied, and stores the result on the collector.
// Nothing is changed while a deployment is on its way, or before the
// collector has reported its configuration since the last one reached it.
// A supervisor (0.166.0) reports the merged configuration, which already
// includes what was sent, as soon as it receives it, seconds before it
// reports the deployment applied; so the check runs again when "applied"
// arrives, against the configuration reported since the deployment was
// sent. Comparing one reported before that would show drift that is not
// there. Not applicable (NULL): no deployment applied,
// or no configuration as written.
func (s *Store) CheckDrift(ctx context.Context, collectorID uuid.UUID) error {
	if s.keys == nil {
		return nil
	}
	var inFlight bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM collector_deployments
		WHERE collector_id = $1 AND status IN ('pending', 'sent', 'applying'))`, collectorID).Scan(&inFlight); err != nil {
		return err
	}
	if inFlight {
		return nil
	}
	var depID uuid.UUID
	var depHash, depSealed string
	var reached *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id, config_hash, config_enc, COALESCE(sent_at, finished_at) FROM collector_deployments
		WHERE collector_id = $1 AND status = 'applied'
		ORDER BY finished_at DESC NULLS LAST, created_at DESC LIMIT 1`, collectorID).Scan(&depID, &depHash, &depSealed, &reached)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.setDrift(ctx, collectorID, nil)
	}
	if err != nil {
		return err
	}
	var received time.Time
	var cfgHash, cfgSealed string
	err = s.pool.QueryRow(ctx, `SELECT config_hash, config_enc, received_at FROM collector_configs WHERE collector_id = $1
		ORDER BY received_at DESC, id DESC LIMIT 1`, collectorID).Scan(&cfgHash, &cfgSealed, &received)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.setDrift(ctx, collectorID, nil)
	}
	if err != nil {
		return err
	}
	if reached != nil && received.Before(*reached) {
		return nil // the collector has not reported since it got it
	}
	raw, err := s.rawOf(collectorID, cfgHash, cfgSealed)
	if err != nil {
		return err
	}
	if raw == "" {
		return s.setDrift(ctx, collectorID, nil)
	}
	// The baseline: a configuration accepted since the rollout, else the
	// first one the collector reported after the deployment reached it.
	baseline := raw
	var accepted struct {
		hash, sealed, by string
		at, received     *time.Time
	}
	_ = s.pool.QueryRow(ctx, `
		SELECT cfg.config_hash, cfg.config_enc, cfg.received_at, c.drift_accepted_at, c.drift_accepted_by_name
		FROM collectors c JOIN collector_configs cfg ON cfg.id = c.drift_accepted_config_id
		WHERE c.id = $1`, collectorID).Scan(&accepted.hash, &accepted.sealed, &accepted.received, &accepted.at, &accepted.by)
	if accepted.received != nil && (reached == nil || !accepted.received.Before(*reached)) {
		b, err := s.rawOf(collectorID, accepted.hash, accepted.sealed)
		if err != nil {
			return err
		}
		since, err := DiffSince(b, raw)
		if err != nil {
			return err
		}
		return s.setDrift(ctx, collectorID, &Drift{DeploymentID: depID, Paths: mergeDrift(nil, since),
			CheckedAt: time.Now().UTC(), AcceptedAt: accepted.at, AcceptedBy: accepted.by})
	}
	if reached != nil {
		var bHash, bSealed string
		err := s.pool.QueryRow(ctx, `SELECT config_hash, config_enc FROM collector_configs
			WHERE collector_id = $1 AND received_at >= $2 ORDER BY received_at, id LIMIT 1`, collectorID, *reached).Scan(&bHash, &bSealed)
		if err == nil {
			if b, err := s.rawOf(collectorID, bHash, bSealed); err == nil && b != "" {
				baseline = b
			}
		}
	}
	sent, err := s.keys.Open(collectorID, depHash, depSealed)
	if err != nil || len(sent) != 1 {
		return fmt.Errorf("collectors: open deployment: %w", err)
	}
	missing, err := DiffSent(sent[0].Body, raw)
	if err != nil {
		return err
	}
	since, err := DiffSince(baseline, raw)
	if err != nil {
		return err
	}
	return s.setDrift(ctx, collectorID, &Drift{DeploymentID: depID, Paths: mergeDrift(missing, since), CheckedAt: time.Now().UTC()})
}

// rawOf opens a stored version and returns its configuration as written,
// or "" when it has none.
func (s *Store) rawOf(collectorID uuid.UUID, hash, sealed string) (string, error) {
	files, err := s.keys.Open(collectorID, hash, sealed)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if f.Name == RawConfigFile {
			return f.Body, nil
		}
	}
	return "", nil
}

func (s *Store) setDrift(ctx context.Context, collectorID uuid.UUID, d *Drift) error {
	var b []byte
	if d != nil {
		b, _ = json.Marshal(d)
	}
	_, err := s.pool.Exec(ctx, `UPDATE collectors SET drift = $2, drift_checked_at = now() WHERE id = $1`, collectorID, nullJSON(b))
	return err
}
