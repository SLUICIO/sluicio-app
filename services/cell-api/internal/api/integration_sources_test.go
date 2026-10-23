// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

func TestWithSources(t *testing.T) {
	m := 0
	members := []string{"orders-api"}
	memberGroups := [][]store.LogAttrFilter{{{Key: "flow", Op: store.AttrOpEq, Value: "x", RequireAllGroups: true, Member: &m}}}

	// Without sources nothing changes.
	if s, g := WithSources(members, memberGroups, nil, integrations.SourceMetrics); len(s) != 1 || len(g) != 1 || len(g[0]) != 1 {
		t.Fatalf("no sources: %v %v", s, g)
	}

	src := []ResolvedSource{{
		Services:    []string{"rabbitmq-prod"},
		Conditions:  []integrations.SourceCondition{{Attribute: "rabbitmq.queue.name", Operator: integrations.OperatorEquals, Value: "orders"}},
		MetricNames: []string{"rabbitmq.queue.*"},
	}}
	svcs, groups := WithSources(members, memberGroups, src, integrations.SourceMetrics)
	if len(svcs) != 2 || svcs[1] != "rabbitmq-prod" {
		t.Errorf("services: %v", svcs)
	}
	if len(groups) != 2 {
		t.Fatalf("groups: %+v", groups)
	}
	// The member group is pinned to the members, keeping its tags, so
	// it never reads the broker.
	pin := groups[0][1]
	if pin.Key != "service.name" || pin.Value != "^(?:orders-api)$" || !pin.RequireAllGroups || pin.Member != &m || pin.Source {
		t.Errorf("member pin: %+v", pin)
	}
	if len(memberGroups[0]) != 1 {
		t.Error("input modified")
	}
	sg := groups[1]
	if len(sg) != 3 || sg[0].Value != "^(?:rabbitmq-prod)$" || sg[1].Value != "orders" || sg[2].Key != store.MetricNameAttrKey || sg[2].Value != `^(?:rabbitmq\.queue\..*)$` {
		t.Errorf("source group: %+v", sg)
	}
	for _, f := range sg {
		if !f.Source {
			t.Errorf("unmarked source filter: %+v", f)
		}
	}

	// A log source takes no metric names, and members without
	// conditions still get pinned.
	_, lg := WithSources(members, nil, src, integrations.SourceLogs)
	if len(lg) != 2 || len(lg[0]) != 1 || len(lg[1]) != 2 {
		t.Errorf("logs: %+v", lg)
	}
}

func TestResolveSources(t *testing.T) {
	sys := uuid.New()
	sources := []integrations.Source{
		{Signal: integrations.SourceMetrics, OriginKind: integrations.OriginService, Origin: "rabbitmq-prod"},
		{Signal: integrations.SourceMetrics, OriginKind: integrations.OriginSystem, Origin: sys.String()},
		{Signal: integrations.SourceLogs, OriginKind: integrations.OriginService, Origin: "rabbitmq-prod"},
		{Signal: integrations.SourceMetrics, OriginKind: integrations.OriginSystem, Origin: uuid.NewString()}, // no members
	}
	members := func(_ context.Context, id uuid.UUID) ([]string, error) {
		if id == sys {
			return []string{"broker-1", "broker-2"}, nil
		}
		return nil, nil
	}
	got, err := ResolveSources(context.Background(), sources, integrations.SourceMetrics, members)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Services[0] != "rabbitmq-prod" || len(got[1].Services) != 2 {
		t.Errorf("resolved: %+v", got)
	}
}
