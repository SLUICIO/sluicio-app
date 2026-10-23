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
