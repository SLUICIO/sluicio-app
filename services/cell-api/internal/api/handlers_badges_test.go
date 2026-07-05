// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"strings"
	"testing"
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
