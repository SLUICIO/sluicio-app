// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Which collector a generated snippet is FOR (issue #16).
//
// Sluicio hands users OpenTelemetry Collector YAML in two places: the
// onboarding config shown with a new ingest key, and every Telemetry
// Advisor suggestion. Collector configuration is not version-stable, so
// YAML written without knowing the target is YAML that is right for
// some customers and refuses to start for others.
//
// This was not hypothetical. `otlphttp` was REMOVED in v0.146.0 and
// renamed `otlp_http`, so the first configuration a new customer ever
// pasted was a hard error on any current collector.
//
// # Why this does not need the whole component registry
//
// Validating a user's arbitrary config needs to know every component
// that has ever existed. That is #3's problem.
//
// Validating OUR OWN generated snippets needs to know the
// version-sensitivity of the components WE emit, which is a far smaller
// set. Measured against the registry, everything Sluicio generates
// today is: the `filter` and `transform` processors, both stable from
// v0.80.0 through v0.157.0, and the OTLP/HTTP exporter, which is the
// one that moved.
//
// So the knowledge below is a table of the renames that affect what we
// write, not a mirror of the collector ecosystem. Adding the next
// rename is a data change. If we ever emit a component whose history we
// have not recorded, the honest answer is to say we cannot express the
// suggestion for that version rather than to guess.
//
// # Detection was ruled out
//
// Checked against real production traffic: a trace forwarded through a
// collector carries `telemetry.sdk.*` for the application's
// instrumentation, `process.runtime.*` for its runtime and `host.*` for
// the machine. Nothing identifies the collector. It forwards spans
// without stamping itself onto them, which is correct of it and leaves
// no signal. So the version is configured, and defaults to the newest
// we know rather than being guessed from telemetry.

package collectorversion

import (
	"fmt"
	"strconv"
	"strings"
)

// Distribution is which collector build a target runs.
//
// It matters as much as the version: a component present in contrib may
// be absent from core, so a version number alone answers only half the
// question.
type Distribution string

const (
	DistributionContrib Distribution = "contrib"
	DistributionCore    Distribution = "core"
)

// Newest is the newest collector release this build of Sluicio knows
// about, and the default when nothing is configured.
//
// Newest rather than oldest, deliberately: an unset value most often
// means a fresh install, and a fresh install runs a current collector.
// Defaulting to the oldest supported would emit deliberately dated
// syntax for the majority in order to protect a minority who have not
// configured anything.
//
// Because the registry ships INSIDE the product, this constant is also
// the edge of what we can check. A cell pinned to an old release knows
// exactly as much about the collector world as the release it shipped
// in, so a target NEWER than this must read as "newer than this Sluicio
// release can check" and never as "invalid". Treating unknown as
// invalid would make an out-of-date cell silently withhold every
// suggestion, which is the same trust failure this feature exists to
// prevent, arriving from the other direction.
const Newest = "0.157.0"

// Target is the collector a snippet is generated for.
type Target struct {
	Version      string       `json:"version"`
	Distribution Distribution `json:"distribution"`
}

// Default is what an unconfigured cell targets.
func Default() Target {
	return Target{Version: Newest, Distribution: DistributionContrib}
}

// Resolve picks the effective target from the settings ladder.
//
// A per-service override wins over the org default, which wins over the
// built-in newest. The service level exists because a snippet always
// targets one service's pipeline: a customer running a newer collector
// on one host than another would otherwise get correct YAML for some
// services and broken YAML for others, with no way to say so.
//
// Partial settings are honoured field by field, so an org that pins a
// version without caring about the distribution does not silently lose
// the default distribution.
func Resolve(orgDefault, serviceOverride *Target) Target {
	out := Default()
	for _, t := range []*Target{orgDefault, serviceOverride} {
		if t == nil {
			continue
		}
		if v := strings.TrimSpace(t.Version); v != "" {
			out.Version = v
		}
		if d := Distribution(strings.TrimSpace(string(t.Distribution))); d != "" {
			out.Distribution = d
		}
	}
	return out
}

// parse turns "0.146.0" into comparable numbers. A leading "v" is
// tolerated because people paste it.
func parse(v string) (major, minor, patch int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) < 2 {
		return 0, 0, 0, false
	}
	nums := make([]int, 3)
	for i := 0; i < 3; i++ {
		if i >= len(parts) {
			continue
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return 0, 0, 0, false
		}
		nums[i] = n
	}
	return nums[0], nums[1], nums[2], true
}

// AtLeast reports whether version v is >= min. Unparseable versions
// report false: an unreadable version is not evidence of being new.
func AtLeast(v, min string) bool {
	vMaj, vMin, vPatch, ok := parse(v)
	if !ok {
		return false
	}
	mMaj, mMin, mPatch, ok := parse(min)
	if !ok {
		return false
	}
	if vMaj != mMaj {
		return vMaj > mMaj
	}
	if vMin != mMin {
		return vMin > mMin
	}
	return vPatch >= mPatch
}

// Valid reports whether a version string is well-formed. Used to reject
// a setting at the point it is saved rather than discovering it when a
// snippet is generated.
func Valid(v string) bool {
	_, _, _, ok := parse(v)
	return ok
}

// NewerThanKnown reports whether a target is beyond what this build can
// check. Callers must present this as a limit of the Sluicio release,
// never as a problem with the target.
func NewerThanKnown(v string) bool {
	return Valid(v) && AtLeast(v, Newest) && v != Newest
}

// ── Component naming ─────────────────────────────────────────────────

// ComponentKind is which section of a config a component lives in.
type ComponentKind string

const (
	KindExporter  ComponentKind = "exporter"
	KindProcessor ComponentKind = "processor"
	KindReceiver  ComponentKind = "receiver"
)

// Component is one thing Sluicio emits, referred to by a stable
// internal name so call sites never spell a version-specific string.
type Component string

const (
	// OTLPHTTPExporter carries telemetry to a Sluicio cell.
	OTLPHTTPExporter Component = "otlp_http_exporter"
	// FilterProcessor drops matching telemetry.
	FilterProcessor Component = "filter_processor"
	// TransformProcessor edits attributes in place.
	TransformProcessor Component = "transform_processor"
)

// naming records how a component is spelled across versions.
//
// SinceName applies from ChangedIn onward; BeforeName applies below it.
// A component with no ChangedIn has never been renamed in the range we
// support, which is the case for both processors we emit.
type naming struct {
	Kind       ComponentKind
	BeforeName string
	SinceName  string
	// ChangedIn is the first version using SinceName. Empty means the
	// name has never changed.
	ChangedIn string
}

var names = map[Component]naming{
	OTLPHTTPExporter: {
		Kind:       KindExporter,
		BeforeName: "otlphttp",
		SinceName:  "otlp_http",
		ChangedIn:  "0.146.0",
	},
	// Verified against the registry on both 0.80.0 and 0.157.0.
	FilterProcessor:    {Kind: KindProcessor, BeforeName: "filter", SinceName: "filter"},
	TransformProcessor: {Kind: KindProcessor, BeforeName: "transform", SinceName: "transform"},
}

// Name returns how a component is spelled for a target.
//
// Errors on a component we have no record of rather than guessing. A
// wrong name produces YAML that will not start, and a customer pasting
// it into production is the worst place to discover our uncertainty.
func Name(c Component, t Target) (string, error) {
	n, ok := names[c]
	if !ok {
		return "", fmt.Errorf("collectorversion: no naming recorded for %q", c)
	}
	if n.ChangedIn == "" || AtLeast(t.Version, n.ChangedIn) {
		return n.SinceName, nil
	}
	return n.BeforeName, nil
}

// RenamedAcross reports whether a component's spelling differs between
// two versions, so a UI can warn that a stored snippet no longer
// matches the current target.
func RenamedAcross(c Component, a, b Target) bool {
	na, errA := Name(c, a)
	nb, errB := Name(c, b)
	if errA != nil || errB != nil {
		return false
	}
	return na != nb
}
