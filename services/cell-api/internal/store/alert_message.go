// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TriggeringRecord is the latest span or log record in an alert rule's
// scope that met the rule's own filter: the message a notification is
// about. Values holds what that record carried for each of the filter's
// keys, so "documents.failed > 0" arrives as documents.failed = 3.
type TriggeringRecord struct {
	Timestamp   time.Time
	ServiceName string
	TraceID     string
	Environment string
	Values      map[string]string
}

// TriggerQuery is one lookup: the rule's scope (services, the bound
// integration's conditions), its filter (attrs) and the keys to read.
// ErrorsOnly keeps failing spans, as a failed-trace rule counts them.
type TriggerQuery struct {
	Services   []string
	From, To   time.Time
	Attrs      []LogAttrFilter
	Groups     [][]LogAttrFilter
	ErrorsOnly bool
	Keys       []string
}

// recordValueExpr reads key from a row the way the rule's predicate reads
// it: the indexed columns for service.name and span.name, else the row's
// own attributes before its resource's.
func recordValueExpr(primaryMap, key string) string {
	switch key {
	case serviceNameAttrKey:
		return "ServiceName"
	case spanNameAttrKey:
		if primaryMap == "SpanAttributes" {
			return "SpanName"
		}
	}
	return attrEffectiveExprIn(primaryMap, key)
}

// LatestTriggeringSpan returns the newest span in scope that meets the
// rule's filter, with the filter's keys read from it.
func (s *Store) LatestTriggeringSpan(ctx context.Context, q TriggerQuery) (TriggeringRecord, bool, error) {
	return s.latestTriggering(ctx, "traces", "SpanAttributes", "TraceId", q)
}

// LatestTriggeringLog is LatestTriggeringSpan for a log rule.
func (s *Store) LatestTriggeringLog(ctx context.Context, q TriggerQuery) (TriggeringRecord, bool, error) {
	return s.latestTriggering(ctx, "logs", "LogAttributes", "TraceId", q)
}

func (s *Store) latestTriggering(ctx context.Context, table, primaryMap, traceCol string, q TriggerQuery) (TriggeringRecord, bool, error) {
	if len(q.Services) == 0 || len(q.Keys) == 0 {
		return TriggeringRecord{}, false, nil
	}
	sql, args := buildLatestTriggeringSQL(table, primaryMap, traceCol, q)
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return TriggeringRecord{}, false, fmt.Errorf("latest triggering %s: %w", table, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return TriggeringRecord{}, false, rows.Err()
	}
	var r TriggeringRecord
	var values []string
	if err := rows.Scan(&r.Timestamp, &r.ServiceName, &r.TraceID, &r.Environment, &values); err != nil {
		return TriggeringRecord{}, false, fmt.Errorf("latest triggering %s: %w", table, err)
	}
	r.Values = make(map[string]string, len(q.Keys))
	for i, k := range q.Keys {
		if i < len(values) && values[i] != "" {
			r.Values[k] = values[i]
		}
	}
	r.Environment = CanonicalEnvironment(r.Environment)
	return r, true, nil
}

func buildLatestTriggeringSQL(table, primaryMap, traceCol string, q TriggerQuery) (string, []any) {
	exprs := make([]string, len(q.Keys))
	for i, k := range q.Keys {
		exprs[i] = recordValueExpr(primaryMap, k)
	}
	args := []any{q.From, q.To}
	where := []string{"Timestamp >= ? AND Timestamp <= ?",
		"ServiceName IN (" + strings.TrimSuffix(strings.Repeat("?,", len(q.Services)), ",") + ")"}
	for _, n := range q.Services {
		args = append(args, n)
	}
	if q.ErrorsOnly && table == "traces" {
		where = append(where, "StatusCode = 'Error'")
	}
	for _, f := range q.Attrs {
		c, a := attrClauseIn(primaryMap, f)
		where = append(where, c)
		args = append(args, a...)
	}
	if clause, a := attrGroupsClause(primaryMap, q.Groups, q.From, q.To); clause != "" {
		where = append(where, clause)
		args = append(args, a...)
	}
	sql := fmt.Sprintf(`
		SELECT Timestamp, ServiceName, %s, %s, [%s]
		FROM %s
		WHERE %s
		ORDER BY Timestamp DESC
		LIMIT 1`, traceCol, EnvironmentExpr(), strings.Join(exprs, ", "), table, strings.Join(where, " AND "))
	return sql, args
}
