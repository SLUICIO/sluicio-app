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
	if org == "" {
		return ctx
	}
	// Produces, per table: 'traces':'OrganizationId = ''<org>'''
	// The inner filter is a single-quoted string; its quotes are doubled
	// to escape them inside the outer single-quoted map value. org is a
	// UUID, but we escape defensively.
	inner := "OrganizationId = '" + strings.ReplaceAll(org, "'", "''") + "'"
	quoted := "'" + strings.ReplaceAll(inner, "'", "''") + "'"
	parts := make([]string, 0, len(orgFilteredTables))
	for _, t := range orgFilteredTables {
		parts = append(parts, fmt.Sprintf("'%s':%s", t, quoted))
	}
	val := "{" + strings.Join(parts, ",") + "}"
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"additional_table_filters": val,
	}))
}
