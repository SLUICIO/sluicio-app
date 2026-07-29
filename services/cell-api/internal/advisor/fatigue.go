// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The Alert Fatigue Advisor (design §4): join firing history against
// operator interaction, and suggest tuning rules nobody acts on.
//
// The reasoning is the same as the telemetry side — an unconsumed signal
// is a cost — but the failure mode is far worse, so the advisor is more
// careful here. Telemetry nobody reads costs money. An alert nobody acts
// on costs attention, and attention is what makes the NEXT alert work.
// A page ignored for the thirtieth time is why the thirty-first gets
// ignored too.
//
// Two rules govern everything below.
//
// **F1 puts no number in the action.** The advisor reports what it
// observed — fired 214 times, engaged with twice — and stops. It does
// not propose a threshold. A suggested number in a one-click button is
// how somebody silences a real alert on our recommendation, and the
// first time that happens they are right to distrust every other
// suggestion we make. Choosing the number is the operator's job; giving
// them the evidence to choose well is ours.
//
// **A percentile is the wrong statistic for message-shaped rules.**
// Quantiles describe distributions — latency, queue depth. For anything
// counted per integration message (message counts, error counts,
// completeness) every message matters, and a p95 silently frames away
// the tail that is the entire reason the rule exists. Where evidence is
// shown for a message-shaped rule it is counts and frequencies, never a
// quantile.
package advisor

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// minFiringsForFatigue: below this, a rule firing unattended is not
	// yet a pattern. Ten times in a month is.
	minFiringsForFatigue = 10
	// wallpaperDays: an instance firing continuously this long has
	// stopped being an alert and become a description of the system.
	wallpaperDays = 14
	// minFlapCyclesPerDay before a rule counts as flapping.
	minFlapCyclesPerDay = 4.0
	// duplicateOverlap: the share of one rule's firings that must
	// coincide with another's before they are called duplicates.
	duplicateOverlap = 0.9
)

// RuleStats is one rule's behaviour over the window.
type RuleStats struct {
	RuleID    uuid.UUID
	Name      string
	Severity  string
	Enabled   bool
	Signal    string
	Service   string
	Firings   int
	Handled   int
	OpenNow   bool
	LongestUp time.Duration
	MeanUp    time.Duration
	HasRoute  bool
	FirstFire time.Time
	LastFire  time.Time
}

// FatigueInput is what an alerting evaluation needs.
type FatigueInput struct {
	OrgID uuid.UUID
	Pool  *pgxpool.Pool
	// Demand answers "did anyone click through from a notification" —
	// deep links record demand under the alert signal (design §4).
	Demand   *DemandSet
	From, To time.Time
}

// LoadRuleStats aggregates instance history per rule.
func LoadRuleStats(ctx context.Context, in FatigueInput) ([]RuleStats, error) {
	rows, err := in.Pool.Query(ctx, `
		SELECT r.id, r.name, r.severity::text, r.enabled, r.signal,
		       COALESCE(r.service_name, ''),
		       count(i.id)                                   AS firings,
		       count(i.handled_at)                           AS handled,
		       bool_or(i.ended_at IS NULL)                   AS open_now,
		       COALESCE(EXTRACT(EPOCH FROM max(COALESCE(i.ended_at, now()) - i.started_at)), 0) AS longest_s,
		       COALESCE(EXTRACT(EPOCH FROM avg(COALESCE(i.ended_at, now()) - i.started_at)), 0) AS mean_s,
		       EXISTS (SELECT 1 FROM alert_rule_routes ar WHERE ar.alert_rule_id = r.id) AS has_route,
		       min(i.started_at) AS first_fire,
		       max(i.started_at) AS last_fire
		FROM alert_rules r
		LEFT JOIN alert_instances i
		       ON i.alert_rule_id = r.id AND i.started_at >= $2 AND i.started_at < $3
		WHERE r.organization_id = $1
		GROUP BY r.id, r.name, r.severity, r.enabled, r.signal, r.service_name`,
		in.OrgID, in.From, in.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuleStats{}
	for rows.Next() {
		var s RuleStats
		var longestS, meanS float64
		var first, last *time.Time
		if err := rows.Scan(&s.RuleID, &s.Name, &s.Severity, &s.Enabled, &s.Signal, &s.Service,
			&s.Firings, &s.Handled, &s.OpenNow, &longestS, &meanS, &s.HasRoute, &first, &last); err != nil {
			return nil, err
		}
		s.LongestUp = time.Duration(longestS) * time.Second
		s.MeanUp = time.Duration(meanS) * time.Second
		if first != nil {
			s.FirstFire = *first
		}
		if last != nil {
			s.LastFire = *last
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// EvaluateFatigue runs F1–F5.
func EvaluateFatigue(ctx context.Context, in FatigueInput) ([]Suggestion, error) {
	stats, err := LoadRuleStats(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("advisor: rule stats: %w", err)
	}
	out := []Suggestion{}
	windowDays := in.To.Sub(in.From).Hours() / 24
	if windowDays < 1 {
		windowDays = 1
	}

	for _, s := range stats {
		// engaged: somebody acknowledged an instance, or followed a
		// notification's deep link into the UI.
		engaged := s.Handled > 0 || in.Demand.ConsumedSince("alert", "", s.RuleID.String(), in.From)

		// --- F2: wallpaper ------------------------------------------
		// Checked before F1: a rule that has been firing continuously
		// for a fortnight is a state, and telling someone to "tune the
		// threshold" of a permanently-true condition misses the point.
		if s.OpenNow && s.LongestUp >= wallpaperDays*24*time.Hour {
			out = append(out, Suggestion{
				Fingerprint: "F2|rule|" + s.RuleID.String(),
				Class:       "F2",
				Advisor:     "alerting",
				ScopeKind:   "rule",
				ScopeID:     s.RuleID.String(),
				Title:       fmt.Sprintf("%q has been firing continuously for %d days", s.Name, days(s.LongestUp)),
				Loss: "This is describing a state, not reporting an event. Either the underlying condition " +
					"is acceptable and the rule should change, or it is not and something needs fixing — " +
					"but as it stands the alert carries no information, because it is always on.",
				Weight: int64(days(s.LongestUp)),
				Evidence: map[string]any{
					"continuous_days": days(s.LongestUp),
					"firings":         s.Firings,
					"acknowledged":    s.Handled,
					"severity":        s.Severity,
				},
			})
			continue
		}

		// --- F3: flapper --------------------------------------------
		cyclesPerDay := float64(s.Firings) / windowDays
		if s.Firings >= minFiringsForFatigue && cyclesPerDay >= minFlapCyclesPerDay && s.MeanUp < time.Hour {
			out = append(out, Suggestion{
				Fingerprint: "F3|rule|" + s.RuleID.String(),
				Class:       "F3",
				Advisor:     "alerting",
				ScopeKind:   "rule",
				ScopeID:     s.RuleID.String(),
				Title: fmt.Sprintf("%q fires and resolves %.0f times a day, averaging %s",
					s.Name, cyclesPerDay, humanDuration(s.MeanUp)),
				Loss: fmt.Sprintf("A condition that clears on its own within %s is usually noise around a "+
					"boundary rather than a distinct incident each time. Widening the sustain window past "+
					"the typical episode would collapse these into one alert — but a genuinely brief "+
					"outage would then take that much longer to reach anyone.", humanDuration(s.MeanUp)),
				Weight: int64(s.Firings),
				Evidence: map[string]any{
					"firings":               s.Firings,
					"cycles_per_day":        fmt.Sprintf("%.1f", cyclesPerDay),
					"mean_episode":          humanDuration(s.MeanUp),
					"longest_episode":       humanDuration(s.LongestUp),
					"acknowledged":          s.Handled,
					"observed_not_proposed": "the sustain window to set is yours to choose",
				},
			})
			continue
		}

		// --- F1: ignored rule ---------------------------------------
		if s.Firings >= minFiringsForFatigue && !engaged {
			out = append(out, Suggestion{
				Fingerprint: "F1|rule|" + s.RuleID.String(),
				Class:       "F1",
				Advisor:     "alerting",
				ScopeKind:   "rule",
				ScopeID:     s.RuleID.String(),
				Title:       fmt.Sprintf("%q fired %d times and nobody engaged with it", s.Name, s.Firings),
				Loss: "Before changing it: an alert nobody acknowledges is not necessarily an alert nobody " +
					"acts on — a team may fix the cause and let it resolve itself. Check whether that is " +
					"what is happening before you raise the threshold.",
				Weight: int64(s.Firings),
				Evidence: map[string]any{
					"firings":         s.Firings,
					"acknowledged":    s.Handled,
					"deep_link_opens": 0,
					"severity":        s.Severity,
					"mean_episode":    humanDuration(s.MeanUp),
					// Stated explicitly so the UI has something honest to
					// render where a "recommended value" would otherwise
					// go — see the file comment on F1.
					"suggested_threshold": "none — the advisor deliberately proposes no number",
				},
			})
			continue
		}

		// --- F4: channel-less ---------------------------------------
		// Only meaningful for a rule that actually fires: a quiet rule
		// with no route is a rule waiting to matter, not a mistake.
		if s.Enabled && !s.HasRoute && s.Firings >= minFiringsForFatigue {
			out = append(out, Suggestion{
				Fingerprint: "F4|rule|" + s.RuleID.String(),
				Class:       "F4",
				Advisor:     "alerting",
				ScopeKind:   "rule",
				ScopeID:     s.RuleID.String(),
				Title:       fmt.Sprintf("%q fired %d times and reaches nobody", s.Name, s.Firings),
				Loss: "The rule is enabled and firing but has no notification channel, so it is only ever " +
					"seen by someone already looking at Sluicio. Either route it somewhere, or the check " +
					"is decorative.",
				Weight: int64(s.Firings),
				Evidence: map[string]any{
					"firings":  s.Firings,
					"channels": 0,
					"enabled":  true,
				},
			})
		}
	}

	dupes, err := evalDuplicateRules(ctx, in, stats)
	if err != nil {
		return nil, err
	}
	return append(out, dupes...), nil
}

// --- F5: duplicate rules -----------------------------------------------

// evalDuplicateRules finds pairs that fire together often enough to be
// two names for one condition.
//
// Co-firing is compared at MINUTE grain rather than by exact instant:
// two rules evaluating on different cycles will never agree to the
// second, and requiring that would find nothing. It is also directional
// — the overlap is measured against the rule with FEWER firings, so a
// narrow rule fully contained inside a broad one is caught, which is the
// usual shape of an accidental duplicate.
func evalDuplicateRules(ctx context.Context, in FatigueInput, stats []RuleStats) ([]Suggestion, error) {
	byID := map[uuid.UUID]RuleStats{}
	for _, s := range stats {
		if s.Firings >= minFiringsForFatigue {
			byID[s.RuleID] = s
		}
	}
	if len(byID) < 2 {
		return nil, nil
	}
	rows, err := in.Pool.Query(ctx, `
		WITH fires AS (
			SELECT i.alert_rule_id AS rule_id, date_trunc('minute', i.started_at) AS minute
			FROM alert_instances i
			JOIN alert_rules r ON r.id = i.alert_rule_id
			WHERE r.organization_id = $1 AND i.started_at >= $2 AND i.started_at < $3
			GROUP BY 1, 2
		)
		SELECT a.rule_id, b.rule_id, count(*) AS shared
		FROM fires a
		JOIN fires b ON a.minute = b.minute AND a.rule_id < b.rule_id
		GROUP BY a.rule_id, b.rule_id
		HAVING count(*) >= $4`, in.OrgID, in.From, in.To, minFiringsForFatigue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Suggestion{}
	for rows.Next() {
		var aID, bID uuid.UUID
		var shared int
		if err := rows.Scan(&aID, &bID, &shared); err != nil {
			return nil, err
		}
		a, aok := byID[aID]
		b, bok := byID[bID]
		if !aok || !bok {
			continue
		}
		smaller := a.Firings
		if b.Firings < smaller {
			smaller = b.Firings
		}
		if smaller == 0 || float64(shared)/float64(smaller) < duplicateOverlap {
			continue
		}
		out = append(out, Suggestion{
			// Sorted pair, so the fingerprint is stable whichever rule
			// the query happens to name first.
			Fingerprint: fmt.Sprintf("F5|rule-pair|%s|%s", aID, bID),
			Class:       "F5",
			Advisor:     "alerting",
			ScopeKind:   "rule",
			ScopeID:     aID.String(),
			Title:       fmt.Sprintf("%q and %q almost always fire together", a.Name, b.Name),
			Loss: "Merging them halves the noise, but check they are not deliberately separate — two rules " +
				"on one condition at different severities, or routed to different teams, look identical " +
				"here and are not duplicates.",
			Weight: int64(shared),
			Evidence: map[string]any{
				"shared_minutes":   shared,
				"a_name":           a.Name,
				"a_firings":        a.Firings,
				"b_name":           b.Name,
				"b_firings":        b.Firings,
				"overlap_of_rarer": fmt.Sprintf("%.0f%%", 100*float64(shared)/float64(smaller)),
			},
		})
	}
	return out, rows.Err()
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", days(d))
	case d >= time.Hour:
		return fmt.Sprintf("%.0fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0fm", d.Minutes())
	default:
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
}
