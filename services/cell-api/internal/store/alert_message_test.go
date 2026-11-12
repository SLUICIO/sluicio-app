// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"strings"
	"testing"
	"time"
)

// The triggering message is read the way the rule reads it: the same
// scope, the same filter, failing spans only for a failed-trace rule.
func TestLatestTriggeringSQLFollowsTheRule(t *testing.T) {
	q := TriggerQuery{
		Services: []string{"a", "b"}, From: time.Unix(0, 0), To: time.Unix(60, 0),
		Attrs:      []LogAttrFilter{{Key: "documents.failed", Op: AttrOpGt, Value: "0"}},
		Groups:     [][]LogAttrFilter{{{Key: "flow", Op: AttrOpEq, Value: "orders"}}},
		ErrorsOnly: true,
		Keys:       []string{"documents.failed", "service.name"},
	}
	sql, args := buildLatestTriggeringSQL("traces", "SpanAttributes", "TraceId", q)
	for _, want := range []string{"StatusCode = 'Error'", "ServiceName IN (?,?)", "ORDER BY Timestamp DESC", "LIMIT 1"} {
		if !strings.Contains(sql, want) {
			t.Errorf("statement lacks %q:\n%s", want, sql)
		}
	}
	if strings.Count(sql, "?") != len(args) {
		t.Errorf("%d placeholders, %d binds", strings.Count(sql, "?"), len(args))
	}
	// service.name is read from the column, as the predicate reads it.
	if !strings.Contains(sql, ", ServiceName]") {
		t.Errorf("service.name is not read from the ServiceName column:\n%s", sql)
	}

	logs, _ := buildLatestTriggeringSQL("logs", "LogAttributes", "TraceId", q)
	if strings.Contains(logs, "StatusCode") {
		t.Error("a log rule has no span status to filter on")
	}
}
