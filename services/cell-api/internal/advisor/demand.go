// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Reading the demand ledger (design §2) for the guardrail every
// suggestion has to clear: has anyone, or anything, actually consumed
// this telemetry?
//
// The whole org's ledger for the window is loaded once per run and
// answered from memory. At daily grain that is a few thousand rows, and
// the alternative — a query per candidate — would make the guardrail
// the expensive part of an evaluation whose entire job is to say "no"
// to most candidates.
//
// Two lookup rules encode the design's guardrails, and both are
// deliberately generous toward NOT suggesting:
//
//   - A key consumed at org scope counts as consumed for every service.
//     The metrics explorer records demand with no service attached (you
//     chart a metric, not a metric-on-a-service), so requiring an exact
//     service match would call charted metrics unused.
//   - Mechanical demand ignores the window entirely. A metric referenced
//     by a paused alert rule is in use — the rule exists, someone meant
//     it, and the sweep will re-record it tomorrow. Judging config by a
//     30-day activity window would propose deleting the telemetry behind
//     every seasonal alert.
package advisor

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// DemandSet is one org's consumption over the observation window.
type DemandSet struct {
	// Earliest is the oldest day the ledger holds for this org, zero if
	// it holds nothing. It decides whether the ledger is old enough to
	// be believed — see Mature.
	Earliest time.Time
	// human[signal|service|key] — someone looked.
	human map[string]time.Time
	// mechanical[signal|service|key] — config references it.
	mechanical map[string]time.Time
}

func demandKey(signal, service, key string) string {
	return signal + "|" + service + "|" + key
}

// LoadDemand reads the ledger for one org since `since`.
func LoadDemand(ctx context.Context, conn driver.Conn, orgID uuid.UUID, since time.Time) (*DemandSet, error) {
	d := &DemandSet{human: map[string]time.Time{}, mechanical: map[string]time.Time{}}
	if conn == nil {
		return d, nil
	}
	rows, err := conn.Query(ctx, `
		SELECT Signal, ServiceName, Key, ConsumerKind, max(Day) AS lastDay
		FROM telemetry_demand
		WHERE OrganizationId = ? AND Day >= ?
		GROUP BY Signal, ServiceName, Key, ConsumerKind`,
		orgID.String(), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var signal, service, key, kind string
		var day time.Time
		if err := rows.Scan(&signal, &service, &key, &kind, &day); err != nil {
			return nil, err
		}
		target := d.mechanical
		if kind == "human" {
			target = d.human
		}
		k := demandKey(signal, service, key)
		if day.After(target[k]) {
			target[k] = day
		}
		if d.Earliest.IsZero() || day.Before(d.Earliest) {
			d.Earliest = day
		}
	}
	return d, rows.Err()
}

// Mature reports whether the ledger has been recording since `from` —
// i.e. long enough to cover the whole observation window.
//
// This is the difference between an advisor that is trustworthy on its
// first day and one that is systematically wrong for a month. The
// ABSENCE of demand is only evidence if we were watching: on a cell
// where the ledger started last Tuesday, a metric somebody charted
// three weeks ago has no recorded demand, and every evaluator would
// read that as "nobody uses this" and propose deleting it.
//
// Mechanical demand is exempt from the problem — the sweep re-records
// config every day, so it is current regardless of history — but every
// class that turns on "nobody LOOKED" is unsafe until the ledger spans
// the window. So the advisors stay silent and say why, rather than
// spending their credibility on a month of confident false positives.
func (d *DemandSet) Mature(from time.Time) bool {
	return !d.Earliest.IsZero() && !d.Earliest.After(from)
}

// ConsumedSince reports whether anything consumed this key at or after
// `cutoff` — the guardrail "any demand = no suggestion", scoped to the
// observation window.
//
// The ledger is loaded over a much longer horizon than the window on
// purpose. Demand older than the window does not stop a suggestion, but
// it is the single most persuasive thing the evidence block can say:
// "you last looked at this 47 days ago" lands very differently from
// "no demand", and a reader can weigh it themselves.
func (d *DemandSet) ConsumedSince(signal, service, key string, cutoff time.Time) bool {
	last := d.LastConsumed(signal, service, key)
	return !last.IsZero() && !last.Before(cutoff)
}

// Mechanical reports whether org CONFIG references this key. Vetoes a
// suggestion regardless of the window — see the file comment.
func (d *DemandSet) Mechanical(signal, service, key string) bool {
	for _, k := range lookupKeys(signal, service, key) {
		if !d.mechanical[k].IsZero() {
			return true
		}
	}
	return false
}

// LastConsumed is the most recent day anything consumed this key, zero
// if never. Rendered as evidence ("last looked at 47 days ago"), which
// is usually more persuasive than the volume.
func (d *DemandSet) LastConsumed(signal, service, key string) time.Time {
	var best time.Time
	for _, k := range lookupKeys(signal, service, key) {
		if t := d.human[k]; t.After(best) {
			best = t
		}
		if t := d.mechanical[k]; t.After(best) {
			best = t
		}
	}
	return best
}

// lookupKeys expands one (signal, service, key) into the ledger entries
// that count as demand for it.
func lookupKeys(signal, service, key string) []string {
	out := []string{demandKey(signal, service, key)}
	if service != "" {
		// Org-scoped demand for the same key: charting a metric records
		// no service, and that is still demand for it everywhere.
		out = append(out, demandKey(signal, "", key))
	}
	if key != "" {
		// Whole-signal demand for this service: someone opened its logs
		// without filtering on a key. That is consumption of the stream,
		// which is what T2 asks about.
		out = append(out, demandKey(signal, service, ""))
	}
	return out
}

// HumanTouchedServiceSince reports whether a person looked at this
// service's signal at all since cutoff, regardless of key.
func (d *DemandSet) HumanTouchedServiceSince(signal, service string, cutoff time.Time) bool {
	t := d.human[demandKey(signal, service, "")]
	return !t.IsZero() && !t.Before(cutoff)
}
