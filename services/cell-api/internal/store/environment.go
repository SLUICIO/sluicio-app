// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// EnvironmentAttrKey addresses the environment a row was produced in
// (#40). It is a pseudo-attribute: attrClauseIn compiles it to the
// environment expression rather than a map lookup, so a filter on it can
// ride in an integration's attribute groups and reach every read that
// already honours them (messages, metrics, logs, flow, checks).
//
// Namespaced so it can never collide with an attribute a service emits:
// a matcher on deployment.environment.name stays an ordinary lookup.
const EnvironmentAttrKey = "sluicio.environment"

// NoEnvironment is how telemetry without an environment reads: the empty
// value. The UI calls it "(no environment)"; it is a group like any other
// and is never dropped.
const NoEnvironment = ""

// EnvironmentConfig is the cell's environment setting.
type EnvironmentConfig struct {
	// Key, when set, is the resource attribute to read the environment
	// from instead of deployment.environment.name / deployment.environment
	// (for installations that use `env` or a namespace).
	Key string `json:"key,omitempty"`
	// Aliases map a value as sent onto its canonical name, compared case
	// insensitively: {"prd": "production", "prod": "production"}.
	Aliases map[string]string `json:"aliases,omitempty"`
}

var envConfig atomic.Pointer[EnvironmentConfig]

// SetEnvironmentConfig installs the cell's environment setting. Applied at
// read time, so changing it never rewrites data.
func SetEnvironmentConfig(c EnvironmentConfig) {
	cp := EnvironmentConfig{Key: strings.TrimSpace(c.Key), Aliases: map[string]string{}}
	for k, v := range c.Aliases {
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		if k != "" && v != "" {
			cp.Aliases[k] = v
		}
	}
	envConfig.Store(&cp)
}

// CurrentEnvironmentConfig returns the installed setting (zero value when
// none was set).
func CurrentEnvironmentConfig() EnvironmentConfig {
	if c := envConfig.Load(); c != nil {
		return *c
	}
	return EnvironmentConfig{}
}

// sqlString quotes s as a ClickHouse string literal. The environment
// expression is spliced into many queries as text, and its key and
// aliases come from an operator setting, so they are escaped rather than
// trusted.
func sqlString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// EnvironmentExpr is the SQL for a row's environment on any signal table:
// the materialized Environment column, or the configured key, with
// aliases mapped onto canonical names.
func EnvironmentExpr() string {
	c := CurrentEnvironmentConfig()
	raw := "Environment"
	if c.Key != "" {
		raw = "ResourceAttributes[" + sqlString(c.Key) + "]"
	}
	if len(c.Aliases) == 0 {
		return raw
	}
	froms := make([]string, 0, len(c.Aliases))
	for k := range c.Aliases {
		froms = append(froms, k)
	}
	sort.Strings(froms)
	fromLits := make([]string, len(froms))
	toLits := make([]string, len(froms))
	for i, k := range froms {
		fromLits[i] = sqlString(k)
		toLits[i] = sqlString(c.Aliases[k])
	}
	return "transform(lower(" + raw + "), [" + strings.Join(fromLits, ", ") + "], [" +
		strings.Join(toLits, ", ") + "], " + raw + ")"
}

// CanonicalEnvironment maps a raw value through the aliases, the same way
// EnvironmentExpr does in SQL.
func CanonicalEnvironment(v string) string {
	if to, ok := CurrentEnvironmentConfig().Aliases[strings.ToLower(v)]; ok {
		return to
	}
	return v
}

// WithEnvironment narrows an integration's attribute groups to one
// environment by AND-ing the environment onto every group. Every group,
// because a group is one alternative way to belong to the integration,
// and each must be read in the chosen environment. With no groups the
// result is the environment alone. The input is not modified.
func WithEnvironment(groups [][]LogAttrFilter, env string) [][]LogAttrFilter {
	envFilter := LogAttrFilter{Key: EnvironmentAttrKey, Op: AttrOpEq, Value: env}
	if len(groups) == 0 {
		return [][]LogAttrFilter{{envFilter}}
	}
	out := make([][]LogAttrFilter, len(groups))
	for i, g := range groups {
		ng := make([]LogAttrFilter, 0, len(g)+1)
		ng = append(ng, g...)
		f := envFilter
		// Carry the group's membership tags, so "one trace through every
		// service" still asks for one group per member.
		if len(g) > 0 {
			f.RequireAllGroups = g[0].RequireAllGroups
			f.Member = g[0].Member
		}
		out[i] = append(ng, f)
	}
	return out
}

// EnvironmentVolume is what one environment of a slice holds in a window.
type EnvironmentVolume struct {
	Environment string
	Messages    uint64 // distinct traces
	ErrorTraces uint64 // distinct traces with an erroring span
	LogRecords  uint64
	MetricRows  uint64
}

// EnvironmentsIn returns the environments a slice (services narrowed by
// an integration's groups) has telemetry from in [from, to], with what
// each holds. "(no environment)" is the empty name and is included like
// any other: a service that never set the attribute must not vanish.
func (s *Store) EnvironmentsIn(ctx context.Context, services []string, groups [][]LogAttrFilter, from, to time.Time) ([]EnvironmentVolume, error) {
	if len(services) == 0 {
		return []EnvironmentVolume{}, nil
	}
	env := EnvironmentExpr()
	byEnv := map[string]*EnvironmentVolume{}
	get := func(e string) *EnvironmentVolume {
		v, ok := byEnv[e]
		if !ok {
			v = &EnvironmentVolume{Environment: e}
			byEnv[e] = v
		}
		return v
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(services)), ",")
	base := func(primary string) (string, []any) {
		args := []any{from, to}
		for _, n := range services {
			args = append(args, n)
		}
		where := "Timestamp >= ? AND Timestamp <= ? AND ServiceName IN (" + in + ")"
		if clause, cargs := attrGroupsClause(primary, groups, from, to); clause != "" {
			where += " AND " + clause
			args = append(args, cargs...)
		}
		return where, args
	}

	where, args := base("SpanAttributes")
	rows, err := s.conn.Query(ctx, `
		SELECT `+env+` AS env,
		       toUInt64(uniqExact(TraceId)),
		       toUInt64(uniqExactIf(TraceId, StatusCode = 'Error'))
		FROM traces WHERE `+where+` GROUP BY env`, args...)
	if err != nil {
		return nil, fmt.Errorf("environments: traces: %w", err)
	}
	for rows.Next() {
		var e string
		var msgs, errs uint64
		if err := rows.Scan(&e, &msgs, &errs); err != nil {
			rows.Close()
			return nil, err
		}
		v := get(e)
		v.Messages, v.ErrorTraces = msgs, errs
	}
	rows.Close()

	for _, t := range []struct {
		table, primary string
		set            func(*EnvironmentVolume, uint64)
	}{
		{"logs", "LogAttributes", func(v *EnvironmentVolume, n uint64) { v.LogRecords = n }},
		{"metrics", "MetricAttributes", func(v *EnvironmentVolume, n uint64) { v.MetricRows = n }},
	} {
		where, args := base(t.primary)
		rows, err := s.conn.Query(ctx, `SELECT `+env+` AS env, toUInt64(count()) FROM `+t.table+` WHERE `+where+` GROUP BY env`, args...)
		if err != nil {
			return nil, fmt.Errorf("environments: %s: %w", t.table, err)
		}
		for rows.Next() {
			var e string
			var n uint64
			if err := rows.Scan(&e, &n); err != nil {
				rows.Close()
				return nil, err
			}
			t.set(get(e), n)
		}
		rows.Close()
	}

	out := make([]EnvironmentVolume, 0, len(byEnv))
	for _, v := range byEnv {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Environment < out[j].Environment })
	return out, nil
}
