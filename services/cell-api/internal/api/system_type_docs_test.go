// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Adding a system type is supposed to include writing its docs page —
// there's a checklist — but a checklist is a reminder, not a mechanism.
// These tests make the docs decision part of the build: a new built-in
// either has a page recorded here, or it fails.
//
// The failure mode being prevented is specific. If a new type inherited
// a URL by pattern, we'd ship a confident-looking link to a page that
// doesn't exist, and an agent citing it would send a human to a 404 in
// the middle of an incident. A missing link is honest; a broken one is
// worse than nothing.

package api

import (
	"strings"
	"testing"
)

func TestEveryBuiltInTypeHasADocsDecision(t *testing.T) {
	for _, tmpl := range monitoringTemplates {
		if _, decided := systemTypeDocsPages[tmpl.Kind]; !decided {
			t.Errorf("built-in system type %q has no entry in systemTypeDocsPages — "+
				"add its docs page and record it as true, or record false if it deliberately has none",
				tmpl.Kind)
		}
	}
}

func TestDocsPagesReferenceRealBuiltIns(t *testing.T) {
	// The other direction: an entry for a type that no longer exists is a
	// link nobody will notice has gone stale.
	known := map[string]bool{}
	for _, tmpl := range monitoringTemplates {
		known[tmpl.Kind] = true
	}
	for key := range systemTypeDocsPages {
		if !known[key] {
			t.Errorf("systemTypeDocsPages has %q, which is not a built-in type — remove it or restore the type", key)
		}
	}
}

func TestDocsURLShapeAndOmission(t *testing.T) {
	// A built-in resolves to its page.
	got := docsURLForSystemType("rabbitmq")
	want := "https://docs.sluicio.com/system-types/rabbitmq/"
	if got != want {
		t.Errorf("docsURLForSystemType(rabbitmq) = %q, want %q", got, want)
	}
	if !strings.HasSuffix(got, "/") {
		t.Error("docs URLs must end in a slash — the docs site redirects otherwise")
	}

	// Custom and unknown types get nothing. This is the case that
	// matters: inventing a URL here is what produces the 404.
	for _, key := range []string{"", "acme-internal-widget", "RabbitMQ", "rabbitmq-prod"} {
		if u := docsURLForSystemType(key); u != "" {
			t.Errorf("docsURLForSystemType(%q) = %q, want empty — only types with a real page may carry a link", key, u)
		}
	}
}
