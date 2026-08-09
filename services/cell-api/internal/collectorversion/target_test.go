// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The failure this guards against is YAML that does not start, pasted
// into production by someone who trusted us. The boundary at v0.146.0 is
// therefore tested from both sides.

package collectorversion

import "testing"

func TestOTLPHTTPExporterAtTheRenameBoundary(t *testing.T) {
	// The exact case that shipped broken: `otlphttp` was removed in
	// v0.146.0, and `otlp_http` does not exist before it. There is no
	// spelling that works on both.
	cases := []struct {
		version string
		want    string
	}{
		{"0.140.0", "otlphttp"},
		{"0.145.9", "otlphttp"},
		{"0.146.0", "otlp_http"}, // the boundary itself
		{"0.157.0", "otlp_http"},
		{"1.0.0", "otlp_http"},
	}
	for _, c := range cases {
		got, err := Name(OTLPHTTPExporter, Target{Version: c.version})
		if err != nil {
			t.Fatalf("%s: %v", c.version, err)
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.version, got, c.want)
		}
	}
}

func TestProcessorsWeEmitHaveNotBeenRenamed(t *testing.T) {
	// Verified against the component registry on 0.80.0 and 0.157.0.
	// If this ever fails, the table is stale and a snippet is wrong.
	for _, c := range []Component{FilterProcessor, TransformProcessor} {
		old, _ := Name(c, Target{Version: "0.80.0"})
		cur, _ := Name(c, Target{Version: Newest})
		if old != cur {
			t.Errorf("%s: %q vs %q — table says renamed, registry said stable", c, old, cur)
		}
	}
}

func TestAnUnknownComponentErrorsRatherThanGuessing(t *testing.T) {
	// A wrong name produces YAML that will not start. Refusing is the
	// only safe answer; a plausible guess is the dangerous one.
	if _, err := Name(Component("something_new"), Default()); err == nil {
		t.Fatal("expected an error for an unrecorded component")
	}
}

func TestResolveLadder(t *testing.T) {
	org := &Target{Version: "0.140.0", Distribution: DistributionCore}
	svc := &Target{Version: "0.157.0"}

	if got := Resolve(nil, nil); got.Version != Newest || got.Distribution != DistributionContrib {
		t.Errorf("unset should be newest contrib, got %+v", got)
	}
	if got := Resolve(org, nil); got.Version != "0.140.0" || got.Distribution != DistributionCore {
		t.Errorf("org default should win over built-in, got %+v", got)
	}
	// The service pins only a version. It must not silently reset the
	// distribution the org chose.
	got := Resolve(org, svc)
	if got.Version != "0.157.0" {
		t.Errorf("service override should win, got %q", got.Version)
	}
	if got.Distribution != DistributionCore {
		t.Errorf("a partial override must not drop the org's distribution, got %q", got.Distribution)
	}
}

func TestBlankSettingsAreIgnoredNotApplied(t *testing.T) {
	// An empty string in the database means "not set", not "set to
	// nothing". Applying it would produce a config targeting version "".
	got := Resolve(&Target{Version: "  ", Distribution: "  "}, nil)
	if got.Version != Newest || got.Distribution != DistributionContrib {
		t.Errorf("got %+v, want the defaults", got)
	}
}

func TestAtLeastComparesNumerically(t *testing.T) {
	// String comparison would put 0.9.0 above 0.146.0, which is exactly
	// the range where the rename lives.
	if AtLeast("0.9.0", "0.146.0") {
		t.Error("0.9.0 must not count as newer than 0.146.0")
	}
	if !AtLeast("0.146.0", "0.146.0") {
		t.Error("a version is at least itself")
	}
	if !AtLeast("v0.157.0", "0.146.0") {
		t.Error("a leading v should be tolerated; people paste it")
	}
	if !AtLeast("0.146", "0.146.0") {
		t.Error("a two-part version should be usable")
	}
}

func TestAnUnreadableVersionIsNotTreatedAsNew(t *testing.T) {
	// Guessing "new" on garbage would emit the newest syntax to someone
	// whose setting we could not read.
	if AtLeast("not-a-version", "0.1.0") {
		t.Error("unparseable must not compare as newer")
	}
	if Valid("latest") {
		t.Error("`latest` is not a version we can reason about")
	}
	// And the generated name falls back to the older, wider-compatible
	// spelling rather than the newest.
	got, err := Name(OTLPHTTPExporter, Target{Version: "garbage"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "otlphttp" {
		t.Errorf("got %q; an unreadable version should not select the newest name", got)
	}
}

func TestNewerThanKnownIsALimitOfOursNotAFault(t *testing.T) {
	// A cell pinned to an old release must say "newer than I can check",
	// never "invalid". Treating unknown as invalid makes a stale cell
	// withhold every suggestion.
	if !NewerThanKnown("0.999.0") {
		t.Error("a far-future version should be flagged as beyond this build")
	}
	if NewerThanKnown(Newest) {
		t.Error("the newest known version is not beyond this build")
	}
	if NewerThanKnown("0.100.0") {
		t.Error("an older version is not beyond this build")
	}
}

func TestRenamedAcrossDetectsTheBoundary(t *testing.T) {
	oldT := Target{Version: "0.140.0"}
	newT := Target{Version: "0.157.0"}
	if !RenamedAcross(OTLPHTTPExporter, oldT, newT) {
		t.Error("the exporter rename should be detected across the boundary")
	}
	if RenamedAcross(FilterProcessor, oldT, newT) {
		t.Error("filter has not been renamed")
	}
}
