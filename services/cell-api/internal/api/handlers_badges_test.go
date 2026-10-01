// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"strings"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
)

func TestBadgeColorMessage(t *testing.T) {
	cases := map[string]struct{ color, msg string }{
		"ok":        {"#3fb950", "healthy"},
		"errors":    {"#d29922", "errors"},
		"unhealthy": {"#e5534b", "unhealthy"},
		"quiet":     {"#8b949e", "no data"},
		"":          {"#8b949e", "no data"}, // unknown → grey/no-data
	}
	for status, want := range cases {
		if got := badgeColor(status); got != want.color {
			t.Errorf("badgeColor(%q) = %q, want %q", status, got, want.color)
		}
		if got := badgeMessage(status); got != want.msg {
			t.Errorf("badgeMessage(%q) = %q, want %q", status, got, want.msg)
		}
	}
}

func TestRenderBadge(t *testing.T) {
	svg := string(renderBadge("Order Sync", "healthy", "#3fb950"))
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("not a self-contained svg: %.40s…", svg)
	}
	for _, want := range []string{"Order Sync", "healthy", "#3fb950", `role="img"`, "<title>"} {
		if !strings.Contains(svg, want) {
			t.Errorf("svg missing %q", want)
		}
	}
}

func TestRenderBadgeEscapesLabel(t *testing.T) {
	// A crafted name must not break out of the SVG markup.
	svg := string(renderBadge(`a"><script>x`, "healthy", "#3fb950"))
	if strings.Contains(svg, "<script>") {
		t.Fatalf("label not escaped: %s", svg)
	}
	if !strings.Contains(svg, "&lt;script&gt;") {
		t.Errorf("expected escaped label in %s", svg)
	}
}

func TestBadgeTruncate(t *testing.T) {
	long := strings.Repeat("x", 60)
	got := badgeTruncate(long, 40)
	if len([]rune(got)) != 40 {
		t.Errorf("truncate len = %d, want 40", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis: %q", got)
	}
	if badgeTruncate("short", 40) != "short" {
		t.Errorf("short string should be unchanged")
	}
}

// Two integrations sharing one service: a RabbitMQ broker with one
// integration per queue. The badge used to be read off the broker, so
// every queue wore the badge of whichever sibling was failing. It is now
// read off the integration: its own checks, the member checks that speak
// for it, and the error traces in its own slice.
func TestIntegrationBadgeReadsTheIntegration(t *testing.T) {
	cases := []struct {
		name        string
		health      integrationHealth
		errorTraces uint64
		want        string
	}{
		{"quiet slice, nothing firing", integrationHealth{Slice: true}, 0, "ok"},
		{"errors in its own slice", integrationHealth{Slice: true}, 3, "errors"},
		{"its own check fires", integrationHealth{Slice: true, IntegrationFiring: true}, 0, "unhealthy"},
		// A sibling queue's backlog check bound to the shared broker is
		// span-level: it speaks for the queue it read, not for this one.
		{"sibling's span-level check on the shared broker", integrationHealth{Slice: true, MemberFiring: alerting.ServiceFiring{Any: true}}, 0, "ok"},
		// A check describing the broker process does speak for every
		// queue on it: a dead broker breaks them all.
		{"broker process check fires", integrationHealth{Slice: true, MemberFiring: alerting.ServiceFiring{Any: true, Process: true}}, 0, "unhealthy"},
		// A service-only integration IS its members' traffic, so any
		// member check speaks for it, as it always did.
		{"service-only, member check fires", integrationHealth{MemberFiring: alerting.ServiceFiring{Any: true}}, 0, "unhealthy"},
		{"service-only, error traces", integrationHealth{}, 1, "errors"},
		{"unhealthy outranks errors", integrationHealth{IntegrationFiring: true}, 5, "unhealthy"},
	}
	for _, c := range cases {
		if got := integrationBadgeFold(c.health, c.errorTraces); got != c.want {
			t.Errorf("%s: integrationBadgeFold = %q, want %q", c.name, got, c.want)
		}
	}
}

// The badge must agree with the integration list about "unhealthy": the
// same fold decides both, so a badge cannot read red while the list
// reads the integration as fine, or the other way round.
func TestIntegrationBadgeAgreesWithTheListOnUnhealthy(t *testing.T) {
	for _, slice := range []bool{false, true} {
		for _, integ := range []bool{false, true} {
			for _, f := range []alerting.ServiceFiring{{}, {Any: true}, {Any: true, Process: true}} {
				h := integrationHealth{Slice: slice, Active: true, MemberFiring: f, IntegrationFiring: integ}
				list := integrationRollupStatus(h) == "unhealthy"
				badge := integrationBadgeFold(h, 0) == "unhealthy"
				if list != badge {
					t.Errorf("slice=%v integration=%v member=%+v: list unhealthy=%v, badge unhealthy=%v", slice, integ, f, list, badge)
				}
			}
		}
	}
}
