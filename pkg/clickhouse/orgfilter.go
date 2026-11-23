// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package clickhouse

import (
	"context"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// orgFilteredTables carry OrganizationId and must be tenant-scoped on
// every read.
var orgFilteredTables = []string{"traces", "logs", "metrics"}

// WithOrgFilter returns a context that constrains EVERY ClickHouse read
// of the telemetry tables (traces/logs/metrics) to a single organization,
// using the server-side `additional_table_filters` setting. ClickHouse
// applies the filter to every query run with the returned context —
// including subqueries and JOINs — so individual query bodies need no
// WHERE clause and there's no way to "forget" one. This is the central
// tenant-isolation boundary for telemetry reads (Phase 2 of multi-tenancy).
//
// org == "" returns ctx unchanged (no filter) — e.g. unauthenticated
// paths or a single-tenant cell that hasn't set an org. Request handlers
// get the filter via middleware (the authenticated principal's org);
// background loops wrap their context with their configured org.
func WithOrgFilter(ctx context.Context, org string) context.Context {
	return WithTableFilter(ctx, org, "")
}

// WithTableFilter is WithOrgFilter plus a predicate of the caller's,
// AND-ed onto the organization on every read of the telemetry tables:
// a request read in some environments narrows every read it makes, so
// no reader can be forgotten (#40). The setting holds one filter per
// table, so the organization and the predicate travel together; setting
// them one after the other would replace the first.
//
// extra must be valid SQL over columns every telemetry table has. Both
// empty returns ctx unchanged.
func WithTableFilter(ctx context.Context, org, extra string) context.Context {
	return WithTableFilters(ctx, org, func(string) string { return extra })
}

// WithTableFilters is WithTableFilter with a predicate per table, for a
// condition whose SQL depends on the table: the environment is read from
// SpanAttributes, LogAttributes or MetricAttributes as well as the
// resource. extra("") is never asked; an empty answer adds nothing for
// that table.
func WithTableFilters(ctx context.Context, org string, extra func(table string) string) context.Context {
	parts := make([]string, 0, len(orgFilteredTables))
	for _, t := range orgFilteredTables {
		var conds []string
		if org != "" {
			conds = append(conds, "OrganizationId = '"+strings.ReplaceAll(org, "'", "''")+"'")
		}
		if x := extra(t); x != "" {
			conds = append(conds, "("+x+")")
		}
		if len(conds) == 0 {
			continue
		}
		// Each table's filter is a single-quoted string inside the map; its
		// quotes are doubled to escape them there. org is a UUID, but it is
		// escaped defensively like the predicate.
		inner := strings.Join(conds, " AND ")
		quoted := "'" + strings.NewReplacer(`\`, `\\`, "'", "''").Replace(inner) + "'"
		parts = append(parts, fmt.Sprintf("'%s':%s", t, quoted))
	}
	if len(parts) == 0 {
		return ctx
	}
	val := "{" + strings.Join(parts, ",") + "}"
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"additional_table_filters": val,
	}))
}
