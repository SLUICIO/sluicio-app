// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package clickhouse

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// switchedOffLogs are the system log tables deploy/clickhouse/sluicio-logs.xml
// turns off: they only help debug ClickHouse itself, and at its defaults
// they grow by gigabytes a week.
var switchedOffLogs = []string{
	"text_log", "trace_log", "metric_log", "asynchronous_metric_log", "processors_profile_log",
	"query_thread_log", "query_views_log", "opentelemetry_span_log", "session_log",
}

// systemLogName is what DropIdleSystemLogs may drop: a switched-off log, or
// a copy ClickHouse set aside when a log's settings changed (query_log_0).
// The name goes into the statement, so it is checked to be one of these.
var systemLogName = regexp.MustCompile(`^(` + strings.Join(switchedOffLogs, "|") + `|[a-z_]+_log_[0-9]+)$`)

// DropIdleSystemLogs drops ClickHouse's own log tables that nothing has
// written to for idle, and returns their names.
//
// Turning a log off in the configuration does not delete its table, and
// changing a log's settings renames the old table aside, so without this
// the gigabytes they hold stay on disk for good. They are not telemetry:
// the usage page and the storage limit do not count them. Only a table
// nothing writes to any more is dropped, so a log that is still on, as on
// an installation without the configuration, is left alone.
func DropIdleSystemLogs(ctx context.Context, conn driver.Conn, idle time.Duration) ([]string, error) {
	rows, err := conn.Query(ctx, `
		SELECT t.name
		FROM system.tables AS t
		LEFT JOIN (
			SELECT table, max(modification_time) AS last
			FROM system.parts WHERE database = 'system' AND active GROUP BY table
		) AS p ON p.table = t.name
		WHERE t.database = 'system'
		  AND match(t.name, ?)
		  AND if(p.last = toDateTime(0), t.metadata_modification_time, p.last) < now() - toIntervalSecond(?)`,
		systemLogName.String(), int64(idle.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("find idle system logs: %w", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var dropped []string
	for _, n := range names {
		if !systemLogName.MatchString(n) {
			continue
		}
		if err := conn.Exec(ctx, "DROP TABLE IF EXISTS system."+n+" SYNC"); err != nil {
			return dropped, fmt.Errorf("drop system.%s: %w", n, err)
		}
		dropped = append(dropped, n)
	}
	return dropped, nil
}
