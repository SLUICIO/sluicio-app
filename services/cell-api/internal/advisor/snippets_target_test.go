// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Issue #16: what a snippet is written FOR.
//
// The failure this guards against does not look like a bug in review. A
// snippet with a stale component name is valid YAML, reads correctly,
// passes every test that checks what it says — and the collector
// refuses to start. `otlphttp` was removed in v0.146.0 and that is
// exactly how it reached customers.
//
// So the properties pinned here are about provenance rather than text:
// that names come from the version table rather than a literal, and
// that a name we cannot resolve produces a refusal rather than a guess.

package advisor

import (
	"errors"
	"strings"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectorversion"
)

func oldTarget() collectorversion.Target {
	return collectorversion.Target{Version: "0.80.0", Distribution: collectorversion.DistributionContrib}
}

func newTarget() collectorversion.Target {
	return collectorversion.Target{Version: "0.157.0", Distribution: collectorversion.DistributionContrib}
}

// The processors we emit have been stable across the whole supported
// range. This is not a tautology worth skipping: it is the claim the
// snippets rest on, and the day it stops being true this test is what
// says so rather than a customer's collector.
func TestProcessorNamesAreStableAcrossTheSupportedRange(t *testing.T) {
	for _, c := range []collectorversion.Component{
		collectorversion.FilterProcessor,
		collectorversion.TransformProcessor,
	} {
		if collectorversion.RenamedAcross(c, oldTarget(), newTarget()) {
			t.Errorf("%s is renamed between 0.80.0 and 0.157.0, but the snippets assume one spelling", c)
		}
	}
}

// The exporter is the one that moved, and it is why this feature
// exists. If this ever stops holding, the naming table has lost the
// rename that caused the original breakage.
func TestTheExporterRenameIsStillRecorded(t *testing.T) {
	before, err := collectorversion.Name(collectorversion.OTLPHTTPExporter, oldTarget())
	if err != nil {
		t.Fatalf("no name for the OTLP/HTTP exporter on 0.80.0: %v", err)
	}
	after, err := collectorversion.Name(collectorversion.OTLPHTTPExporter, newTarget())
	if err != nil {
		t.Fatalf("no name for the OTLP/HTTP exporter on 0.157.0: %v", err)
	}
	if before != "otlphttp" || after != "otlp_http" {
		t.Errorf("exporter spellings are %q -> %q, want otlphttp -> otlp_http", before, after)
	}
}

func TestEverySnippetNamesItsProcessorFromTheTable(t *testing.T) {
	filter, _ := collectorversion.Name(collectorversion.FilterProcessor, newTarget())
	transform, _ := collectorversion.Name(collectorversion.TransformProcessor, newTarget())

	cases := []struct {
		name string
		gen  func() (string, error)
		want string
	}{
		{"drop metric", func() (string, error) { return snippetDropMetric("queue.depth", newTarget()) }, filter},
		{"log floor", func() (string, error) { return snippetLogFloor("svc", "warn", newTarget()) }, filter},
		{"delete attr", func() (string, error) { return snippetDeleteSpanAttr("svc", "a.b", newTarget()) }, transform},
		{"delete pattern", func() (string, error) { return snippetDeleteAttrPattern("svc", "http.request.header.", newTarget()) }, transform},
		{"redact", func() (string, error) { return snippetRedactAttr("svc", "user.email", newTarget()) }, transform},
	}
	for _, c := range cases {
		out, err := c.gen()
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		// Both the processors block and the pipeline reference have to
		// carry the resolved name. Renaming one and not the other is a
		// config that parses and then does nothing, which is worse than
		// one that fails loudly.
		if got := strings.Count(out, c.want+"/sluicio-advisor"); got != 2 {
			t.Errorf("%s: %q appears %d times, want 2:\n%s", c.name, c.want+"/sluicio-advisor", got, out)
		}
	}
}

// The rule the issue turns on: no snippet is better than a broken one,
// but neither is a reason to hide the finding.
func TestAnInexpressibleChangeKeepsTheFindingAndDropsTheYAML(t *testing.T) {
	in := TelemetryInput{Target: func(string) collectorversion.Target { return newTarget() }}
	got := in.withSnippet(
		Suggestion{Title: "Nothing reads the queue.depth metric", Weight: 1234},
		"svc",
		func(collectorversion.Target) (string, error) {
			return "", errors.New("no naming recorded for \"some_future_processor\"")
		},
	)

	if got.Snippet != "" {
		t.Errorf("a snippet was emitted for a component we cannot name: %q", got.Snippet)
	}
	if got.SnippetUnavailable == "" {
		t.Error("no reason given for the missing snippet; the reader is left guessing")
	}
	if !strings.Contains(got.SnippetUnavailable, "0.157.0") {
		t.Errorf("the reason does not name the target it could not be written for: %q", got.SnippetUnavailable)
	}
	// The finding survives. The cost it describes is real whether or not
	// we can write the fix, and dropping it would hide a real bill
	// because of a limitation of ours.
	if got.Title == "" || got.Weight != 1234 {
		t.Error("the finding itself was lost along with its snippet")
	}
}

func TestSnippetRecordsTheTargetItWasWrittenFor(t *testing.T) {
	// Stored on the row rather than derived later: on an accepted
	// suggestion the snippet is the audit trail of what was advised.
	in := TelemetryInput{Target: func(string) collectorversion.Target { return oldTarget() }}
	got := in.withSnippet(Suggestion{}, "svc", func(t collectorversion.Target) (string, error) {
		return snippetDeleteSpanAttr("svc", "a.b", t)
	})
	if got.SnippetTarget != "0.80.0" {
		t.Errorf("snippet target recorded as %q, want 0.80.0", got.SnippetTarget)
	}
	if got.Snippet == "" {
		t.Error("no snippet produced for a target we fully support")
	}
}

func TestAnUnsetTargetFallsBackToNewestRatherThanEmpty(t *testing.T) {
	// A nil resolver is the unconfigured cell. It must produce the
	// newest known version, not the zero value: an empty version string
	// would silently select the older spelling everywhere.
	in := TelemetryInput{}
	got := in.withSnippet(Suggestion{}, "svc", func(t collectorversion.Target) (string, error) {
		return snippetDropMetric("m", t)
	})
	if got.SnippetTarget != collectorversion.Newest {
		t.Errorf("unset target resolved to %q, want %s", got.SnippetTarget, collectorversion.Newest)
	}
}
