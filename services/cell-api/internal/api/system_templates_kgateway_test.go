// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The two kgateway types against what kgateway v2.5.2 actually emitted
// (testdata/kgateway-2.5.2.txt). A check naming a metric, a label or a
// response flag the gateway does not produce is armed and can never
// fire, and nothing else would notice.
package api

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"
)

type kgatewayCapture struct {
	// metrics[process][name] = label keys on that metric.
	metrics map[string]map[string]map[string]bool
	spans   []capturedSpan
}

type capturedSpan struct {
	name, status, flags string
	failed              bool
}

func loadKgatewayCapture(t *testing.T) kgatewayCapture {
	t.Helper()
	f, err := os.Open("testdata/kgateway-2.5.2.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := kgatewayCapture{metrics: map[string]map[string]map[string]bool{}}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		switch fields[0] {
		case "metric":
			proc, name := fields[1], fields[2]
			if c.metrics[proc] == nil {
				c.metrics[proc] = map[string]map[string]bool{}
			}
			labels := map[string]bool{}
			if fields[4] != "-" {
				for _, l := range strings.Split(fields[4], ",") {
					labels[l] = true
				}
			}
			c.metrics[proc][name] = labels
		case "span":
			c.spans = append(c.spans, capturedSpan{name: fields[1], status: fields[2], flags: fields[3], failed: fields[4] == "error"})
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return c
}

func builtInTemplate(t *testing.T, kind string) monitoringTemplate {
	t.Helper()
	for _, tm := range monitoringTemplates {
		if tm.Kind == kind {
			return tm
		}
	}
	t.Fatalf("no built-in type %q", kind)
	return monitoringTemplate{}
}

func (c kgatewayCapture) names(proc string) []string {
	out := make([]string, 0, len(c.metrics[proc]))
	for n := range c.metrics[proc] {
		out = append(out, n)
	}
	return out
}

// Each metric check reads a metric its own process emits, and splits
// and filters on labels that metric carries.
func TestKgatewayChecksReadWhatTheProcessEmits(t *testing.T) {
	c := loadKgatewayCapture(t)
	for kind, proc := range map[string]string{"kgateway": "proxy", "kgateway-controller": "controller"} {
		for _, chk := range builtInTemplate(t, kind).Checks {
			if chk.Signal != "" && chk.Signal != "metric" {
				continue
			}
			labels, ok := c.metrics[proc][chk.Metric]
			if !ok {
				t.Errorf("%s / %q: the %s does not emit %s", kind, chk.Name, proc, chk.Metric)
				continue
			}
			if chk.SplitBy != "" && !labels[chk.SplitBy] {
				t.Errorf("%s / %q: %s has no label %q to split by", kind, chk.Name, chk.Metric, chk.SplitBy)
			}
			for _, a := range chk.Attrs {
				if !labels[a.Key] {
					t.Errorf("%s / %q: %s has no label %q to filter on", kind, chk.Name, chk.Metric, a.Key)
				}
			}
		}
	}
}

// The control plane is recognised by its own metrics and the proxy is
// not mistaken for it. The proxy type itself detects on nothing: its
// telemetry is plain Envoy, and claiming envoy_ would name every other
// Envoy-based gateway "kgateway".
func TestKgatewayDetection(t *testing.T) {
	c := loadKgatewayCapture(t)
	ctrl := builtInTemplate(t, "kgateway-controller")
	if !metricsMatchPrefixes(c.names("controller"), ctrl.DetectPrefixes) {
		t.Error("the control plane's metrics do not detect kgateway-controller")
	}
	if metricsMatchPrefixes(c.names("proxy"), ctrl.DetectPrefixes) {
		t.Error("the proxy's metrics detect kgateway-controller")
	}
	proxy := builtInTemplate(t, "kgateway")
	if len(proxy.DetectPrefixes) != 0 || len(proxy.DetectSpanAttrs) != 0 {
		t.Errorf("kgateway detects on %v / %v; it is picked by hand", proxy.DetectPrefixes, proxy.DetectSpanAttrs)
	}
	for _, tm := range monitoringTemplates {
		if tm.Kind != "kgateway-controller" && metricsMatchPrefixes(c.names("controller"), tm.DetectPrefixes) {
			t.Errorf("the control plane's metrics also detect %q", tm.Kind)
		}
	}
}

// The trace checks fire on the spans that mean what they say: Envoy
// marks a 5xx failed and leaves a 4xx alone, and the missing-backend
// check matches the NC span and no other.
func TestKgatewayTraceChecksMatchTheSpans(t *testing.T) {
	c := loadKgatewayCapture(t)
	for _, s := range c.spans {
		wantFailed := strings.HasPrefix(s.status, "5")
		if s.failed != wantFailed {
			t.Errorf("span %s %s %s: failed=%v, want %v", s.name, s.status, s.flags, s.failed, wantFailed)
		}
	}
	var nc *systemCheck
	proxy := builtInTemplate(t, "kgateway")
	for i := range proxy.Checks {
		if proxy.Checks[i].Name == "Route points at a missing backend" {
			nc = &proxy.Checks[i]
		}
	}
	if nc == nil || len(nc.Attrs) != 1 || nc.Attrs[0].Key != "response_flags" || nc.Attrs[0].Op != "matches" {
		t.Fatalf("missing-backend check = %+v, want one response_flags matches filter", nc)
	}
	re := regexp.MustCompile(nc.Attrs[0].Value)
	matched := 0
	for _, s := range c.spans {
		if !s.failed || !re.MatchString(s.flags) {
			continue
		}
		matched++
		if s.flags != "NC" {
			t.Errorf("missing-backend check matches a %s span (%s)", s.flags, s.status)
		}
	}
	if matched == 0 {
		t.Error("missing-backend check matches no captured span")
	}
	for _, combined := range []string{"NC", "UF,NC", "NC,URX"} {
		if !re.MatchString(combined) {
			t.Errorf("missing-backend check misses response_flags %q", combined)
		}
	}
	if re.MatchString("UNC") {
		t.Error(`missing-backend check matches inside another flag ("UNC")`)
	}
}

// Both types survive the share format, which is how they reach the
// community repo and other cells.
func TestKgatewayTypesExportCleanly(t *testing.T) {
	for _, kind := range []string{"kgateway", "kgateway-controller"} {
		tm := builtInTemplate(t, kind)
		doc := systemTypeDoc{Format: SystemTypeDocFormat, Key: tm.Kind, Label: tm.Label, IsSystem: tm.System, DetectPrefixes: tm.DetectPrefixes}
		for _, chk := range tm.Checks {
			doc.Checks = append(doc.Checks, checkToDoc(systemCheckToCustom(chk)))
		}
		if err := validateSystemTypeDoc(&doc); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
	}
}
