// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PayloadLinkSpan is one span that carries at least one of the link
// attributes asked for, with those attributes' values.
type PayloadLinkSpan struct {
	TraceID     string
	SpanID      string
	ServiceName string
	SpanName    string
	Role        string
	Time        time.Time
	Values      map[string]string
}

// payloadLinkRowCap bounds one read. A page is at most a thousand
// messages and each lists at most a few dozen links, so a read that hits
// this is a message touching thousands of files, not a page of normal ones.
const payloadLinkRowCap = 50000

// PayloadLinkSpans reads every span of the given traces that carries one
// of the link attributes, from `from` on (the earliest message of the
// page, so the TraceId bloom filter and the time key both prune). services,
// when non-empty, limits the read to those services: a reader who sees
// part of the estate, or an integration's slice. roleExpr is the effective
// io.role (facet mappings applied, see facetmappings.RoleExprByService);
// empty reads the raw attribute.
func (s *Store) PayloadLinkSpans(ctx context.Context, traceIDs, keys, services []string, from time.Time, roleExpr string, roleArgs []any) ([]PayloadLinkSpan, error) {
	if len(traceIDs) == 0 || len(keys) == 0 {
		return nil, nil
	}
	if roleExpr == "" {
		roleExpr, roleArgs = "SpanAttributes['io.role']", nil
	}
	keyPH := make([]string, len(keys))
	args := make([]any, 0, len(roleArgs)+2*len(keys)+len(traceIDs)+len(services)+2)
	args = append(args, roleArgs...)
	for i, k := range keys {
		keyPH[i] = "?"
		args = append(args, k)
	}
	// The keys are bound twice: once to pick the values, once to pick the
	// spans that carry any of them.
	for _, k := range keys {
		args = append(args, k)
	}
	args = append(args, from)
	tidPH := make([]string, len(traceIDs))
	for i, t := range traceIDs {
		tidPH[i] = "?"
		args = append(args, t)
	}
	svcSQL := ""
	if len(services) > 0 {
		ph := make([]string, len(services))
		for i, n := range services {
			ph[i] = "?"
			args = append(args, n)
		}
		svcSQL = " AND ServiceName IN (" + strings.Join(ph, ",") + ")"
	}
	args = append(args, payloadLinkRowCap)
	keyList := "[" + strings.Join(keyPH, ",") + "]"
	q := `
		SELECT TraceId, SpanId, ServiceName, SpanName, ` + roleExpr + `, Timestamp,
		       mapFilter((k, v) -> has(` + keyList + `, k) AND v != '', SpanAttributes)
		FROM traces
		WHERE hasAny(mapKeys(SpanAttributes), ` + keyList + `)
		  AND Timestamp >= ?
		  AND TraceId IN (` + strings.Join(tidPH, ",") + `)` + svcSQL + `
		ORDER BY TraceId, Timestamp
		LIMIT ?`
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("payload link spans: %w", err)
	}
	defer rows.Close()
	var out []PayloadLinkSpan
	for rows.Next() {
		var sp PayloadLinkSpan
		if err := rows.Scan(&sp.TraceID, &sp.SpanID, &sp.ServiceName, &sp.SpanName, &sp.Role, &sp.Time, &sp.Values); err != nil {
			return nil, fmt.Errorf("payload link spans: scan: %w", err)
		}
		if len(sp.Values) > 0 {
			out = append(out, sp)
		}
	}
	return out, rows.Err()
}
