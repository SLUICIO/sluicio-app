// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integrations

import (
	"testing"

	"github.com/google/uuid"
)

func TestSourceValidate(t *testing.T) {
	ok := []Source{
		{Signal: SourceMetrics, OriginKind: OriginService, Origin: "rabbitmq", MetricNames: []string{"rabbitmq.queue.*"},
			Conditions: []SourceCondition{{Attribute: "rabbitmq.queue.name", Operator: OperatorEquals, Value: "orders"}}},
		{Signal: SourceLogs, OriginKind: OriginSystem, Origin: uuid.NewString()},
	}
	for _, s := range ok {
		if err := s.Validate(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	bad := []Source{
		{Signal: "traces", OriginKind: OriginService, Origin: "x"},
		{Signal: SourceMetrics, OriginKind: OriginService, Origin: " "},
		{Signal: SourceMetrics, OriginKind: OriginSystem, Origin: "not-a-uuid"},
		{Signal: SourceLogs, OriginKind: OriginService, Origin: "x", MetricNames: []string{"a"}},
		{Signal: SourceMetrics, OriginKind: OriginService, Origin: "x", Conditions: []SourceCondition{{Attribute: "service.name", Operator: OperatorEquals, Value: "y"}}},
		{Signal: SourceMetrics, OriginKind: OriginService, Origin: "x", Conditions: []SourceCondition{{Attribute: "q", Operator: OperatorRegex, Value: "("}}},
		{Signal: SourceMetrics, OriginKind: OriginService, Origin: "x", Conditions: []SourceCondition{{Attribute: "q", Operator: OperatorEquals, Value: ""}}},
	}
	for _, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("accepted %+v", s)
		}
	}
}

// Numeric comparisons on an attribute (http.response.status_code >= 500).
func TestSourceNumericConditions(t *testing.T) {
	ok := Source{Signal: SourceLogs, OriginKind: OriginService, Origin: "gateway",
		Conditions: []SourceCondition{{Attribute: "http.response.status_code", Operator: SourceOpGte, Value: "500"}, {Attribute: "retry", Operator: SourceOpLt, Value: "2.5"}}}
	if err := ok.Validate(); err != nil {
		t.Errorf("numeric: %v", err)
	}
	bad := Source{Signal: SourceLogs, OriginKind: OriginService, Origin: "gateway",
		Conditions: []SourceCondition{{Attribute: "http.response.status_code", Operator: SourceOpGt, Value: "five hundred"}}}
	if err := bad.Validate(); err == nil {
		t.Error("a numeric comparison with a non-number was accepted")
	}
	if !SourceOpLte.IsNumeric() || OperatorEquals.IsNumeric() {
		t.Error("IsNumeric")
	}
}
