// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Finding groups of services that look like one integration (issue #10).
//
// Services belonging to no integration sit in the `(no integration)`
// bucket until somebody gets round to them, monitored only by the
// built-in error signal, and nobody notices because silence looks like
// health. This proposes groupings for a human to approve.
//
// # Why topology rather than naming
//
// The obvious signal is the service name, and it was measured against a
// real cell's 17 unassigned services before being rejected:
//
//   - leading-token clustering covered 2 of 17;
//   - trailing-token clustering produced three clusters and all three
//     were wrong (`carrier-dispatcher` with `notification-dispatcher`,
//     shipping with email);
//   - the groupings a human would make shared no lexical structure at
//     all.
//
// Where a rigid naming scheme does exist, the customer knows it and can
// add one prefix matcher by hand in seconds. So naming has little value
// where it works and negative value where it does not, and a
// confidently wrong suggestion costs a reviewer more than no suggestion.
//
// The call graph is the signal we already compute and were not using.
// Services in one business flow talk to each other regardless of what
// they are called.
//
// # What this deliberately does not do
//
// It does not name the group, choose the matcher shape, or decide which
// straggler belongs. Those are judgement, and judgement is the agent's
// job (or a human's). This produces candidates: deterministic, derived
// from observed calls, and identical on every run given the same graph.
// That also means a cell with no agent attached still gets suggestions.

package proposals

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
)

// Edge is one observed service-to-service call.
type Edge struct {
	Source string
	Target string
	// Traces is how many distinct traces crossed this hop. Used as a
	// confidence floor: two services that called each other once are not
	// an integration, they are a coincidence.
	Traces uint64
}

// Cluster is a proposed grouping of services.
type Cluster struct {
	// Services, sorted, so the same cluster produces the same dedup key
	// on every run.
	Services []string
	// InternalTraces is the total traffic on edges INSIDE the cluster.
	// Carried so a reviewer sees the evidence rather than a bare list,
	// and so an agent can cite what it observed.
	InternalTraces uint64
}

// ClusterOptions bounds what is worth proposing.
type ClusterOptions struct {
	// MinEdgeTraces is the traffic an edge needs before it counts as a
	// real relationship. Below this, one stray call would fuse two
	// unrelated flows into a single suggestion, and a wrong grouping is
	// more expensive to review than no grouping.
	MinEdgeTraces uint64
	// MinServices is the smallest group worth proposing. Two services
	// that call each other are usually a caller and its database, not a
	// business flow.
	MinServices int
	// MaxServices caps a cluster. A component spanning half the estate
	// is not an integration, it is the estate: some hub service (an auth
	// sidecar, a shared gateway) has chained everything together, and
	// proposing it would be confidently useless.
	MaxServices int
}

// DefaultClusterOptions are deliberately conservative. The cost of a
// wrong suggestion is a reviewer's trust; the cost of a missed one is
// that things stay as they are today.
func DefaultClusterOptions() ClusterOptions {
	return ClusterOptions{MinEdgeTraces: 5, MinServices: 2, MaxServices: 12}
}

// FindClusters returns connected components of the call graph, limited
// to services that belong to no integration.
//
// unassigned is the candidate set: edges touching anything outside it
// are ignored entirely rather than pulling assigned services in. A
// service already in an integration is not evidence about services that
// are not, and including it would propose overlapping memberships.
func FindClusters(unassigned []string, edges []Edge, opt ClusterOptions) []Cluster {
	if opt.MinServices <= 0 {
		opt.MinServices = 2
	}

	candidate := make(map[string]bool, len(unassigned))
	for _, s := range unassigned {
		candidate[s] = true
	}

	// Union-find over the candidate set.
	parent := make(map[string]string, len(unassigned))
	for _, s := range unassigned {
		parent[s] = s
	}
	var find func(string) string
	find = func(x string) string {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	// Traffic on the edges that actually joined things, kept per edge so
	// it can be summed per cluster after the components settle.
	kept := make([]Edge, 0, len(edges))
	for _, e := range edges {
		if e.Source == e.Target {
			continue
		}
		if !candidate[e.Source] || !candidate[e.Target] {
			continue
		}
		if e.Traces < opt.MinEdgeTraces {
			continue
		}
		union(e.Source, e.Target)
		kept = append(kept, e)
	}

	groups := map[string][]string{}
	for _, s := range unassigned {
		root := find(s)
		groups[root] = append(groups[root], s)
	}

	traffic := map[string]uint64{}
	for _, e := range kept {
		traffic[find(e.Source)] += e.Traces
	}

	out := []Cluster{}
	for root, members := range groups {
		if len(members) < opt.MinServices {
			continue
		}
		if opt.MaxServices > 0 && len(members) > opt.MaxServices {
			// Reported by the caller as skipped rather than silently
			// dropped: a hub that fused the estate is itself worth
			// knowing about.
			continue
		}
		sort.Strings(members)
		out = append(out, Cluster{Services: members, InternalTraces: traffic[root]})
	}

	// Busiest first: the cluster carrying the most traffic is the one
	// whose absence from monitoring matters most.
	sort.Slice(out, func(i, j int) bool {
		if out[i].InternalTraces != out[j].InternalTraces {
			return out[i].InternalTraces > out[j].InternalTraces
		}
		return out[i].Services[0] < out[j].Services[0]
	})
	return out
}

// OversizedClusters returns the components that were rejected for being
// too large, so the caller can say what it skipped.
//
// A silent cap reads as "there was nothing else to find", which is the
// opposite of the truth: an oversized component usually means a hub
// service has chained unrelated flows together, and that is a finding.
func OversizedClusters(unassigned []string, edges []Edge, opt ClusterOptions) []Cluster {
	wide := opt
	wide.MaxServices = 0
	all := FindClusters(unassigned, edges, wide)
	out := []Cluster{}
	for _, c := range all {
		if opt.MaxServices > 0 && len(c.Services) > opt.MaxServices {
			out = append(out, c)
		}
	}
	return out
}

// DedupKey identifies a proposed cluster independently of when it was
// proposed.
//
// Creates never supersede — there is no target to key on — so without
// this a re-proposing agent floods the inbox with the same suggestion
// every run. The key is derived from the sorted member list, so the
// same grouping is recognised even if the agent names it differently on
// a later pass.
//
// Names are LENGTH-PREFIXED before hashing rather than joined by a
// separator. A separator is forgeable: a service literally named
// "a\x1fb" would otherwise produce the same key as the two services "a"
// and "b", and two different groupings sharing one key means one of
// them silently never reaches the inbox. Service names come from
// customer telemetry and are not ours to make assumptions about.
//
// Hashed rather than concatenated so the result is a fixed size, usable
// as a unique index without worrying about how many services a cluster
// has.
func DedupKey(services []string) string {
	s := append([]string(nil), services...)
	sort.Strings(s)
	h := sha256.New()
	for _, name := range s {
		h.Write([]byte(strconv.Itoa(len(name))))
		h.Write([]byte(":"))
		h.Write([]byte(name))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ── Drift for a create ───────────────────────────────────────────────

// CreateDrift is why a create proposal can no longer be applied as
// filed.
//
// The existing CheckDrift compares each change's Before against the
// target's current value, which needs a target. A create has none, so
// "the target changed" is not the question. What can go stale instead
// is the WORLD the suggestion was made about:
//
//   - the slug it wants is now taken;
//   - services it would capture have been claimed by another
//     integration meanwhile;
//   - services it names have stopped reporting, so approving would
//     create an integration that watches nothing.
//
// Reported as a list rather than a boolean because they call for
// different responses. A taken slug is a rename. Claimed services mean
// somebody already did this by hand and the suggestion is redundant.
// Services that went quiet mean the evidence has expired and the
// grouping should be re-derived rather than approved on trust.
type CreateDrift struct {
	SlugTaken bool `json:"slug_taken,omitempty"`
	// ClaimedServices are members that now belong to some integration.
	ClaimedServices []string `json:"claimed_services,omitempty"`
	// MissingServices are members that no longer report at all.
	MissingServices []string `json:"missing_services,omitempty"`
}

// Any reports whether anything drifted.
func (d CreateDrift) Any() bool {
	return d.SlugTaken || len(d.ClaimedServices) > 0 || len(d.MissingServices) > 0
}

// Blocking reports whether the drift must stop an approval outright.
//
// A taken slug blocks: applying would fail or, worse, silently attach to
// somebody else's integration.
//
// Claimed or missing services do NOT block. They make the suggestion
// weaker, not impossible, and the reviewer is the right person to judge
// that: an integration over four of the five services originally
// proposed is usually still the integration they wanted. Blocking on it
// would turn a useful suggestion into a dead row every time somebody
// tidied one service in the meantime.
func (d CreateDrift) Blocking() bool { return d.SlugTaken }

// CheckCreateDrift compares a proposed integration against the world as
// it is now.
//
// slugExists, assigned and reporting are supplied by the caller rather
// than queried here, so this stays testable without a database and so
// the caller controls the window "still reporting" is measured over.
func CheckCreateDrift(services []string, slugExists bool, assigned map[string]bool, reporting map[string]bool) CreateDrift {
	d := CreateDrift{SlugTaken: slugExists}
	for _, s := range services {
		if assigned[s] {
			d.ClaimedServices = append(d.ClaimedServices, s)
		}
		// Checked only when the caller supplied a reporting set at all;
		// an empty map means "not established", and treating that as
		// "everything is missing" would block every approval on a cell
		// whose catalog had not loaded.
		if len(reporting) > 0 && !reporting[s] {
			d.MissingServices = append(d.MissingServices, s)
		}
	}
	sort.Strings(d.ClaimedServices)
	sort.Strings(d.MissingServices)
	return d
}
