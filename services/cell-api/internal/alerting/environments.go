// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Checks per environment (issue #40).
//
// A check bound to an integration is evaluated once per environment of
// that integration (or once per listed environment), each with its own
// alert instance, so test failing does not page as production and an
// empty production queue does not hide a backlog in test. The
// environment travels in the evaluation's context (envctx): the store
// adapters narrow the integration's slice to it, the instance helpers
// below key and label the instance by it, and routing reads it to pick
// the profile.
//
// An integration with no environments other than "(no environment)" is
// evaluated exactly as before: once, with the "all" instance. Nothing
// changes for an installation that sends no environment.

package alerting

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/envctx"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/envrole"
)

// envFingerprintPrefix prefixes a per-environment instance's fingerprint.
const envFingerprintPrefix = "env:"

// legacyFingerprint is the instance of a check evaluated over all of its
// scope at once.
const legacyFingerprint = "all"

// EnvironmentFingerprint is the fingerprint of a rule's instance for one
// environment.
func EnvironmentFingerprint(env string) string { return envFingerprintPrefix + env }

// EnvironmentLabel is the instance label naming its environment.
const EnvironmentLabel = "environment"

// EnvironmentLister says which environments an integration has: the ones
// its own slice had telemetry from recently, plus the ones it declares.
type EnvironmentLister interface {
	IntegrationEnvironments(ctx context.Context, integrationID uuid.UUID) ([]string, error)
}

// SetEnvironmentLister wires per-environment evaluation. Unset, every
// check is evaluated once over its whole scope, as before.
func (e *Engine) SetEnvironmentLister(l EnvironmentLister) { e.envLister = l }

// perEnvironment runs eval for each environment rule is about, or once
// without one when it is not about environments at all.
func (e *Engine) perEnvironment(ctx context.Context, rule AlertRule, eval func(context.Context, AlertRule)) {
	if rule.IntegrationID == nil || e.envLister == nil {
		eval(ctx, rule)
		return
	}
	envs, perEnv, err := e.environmentsFor(ctx, rule)
	if err != nil {
		// Skipped rather than evaluated over everything: a whole-scope
		// evaluation would open the "all" instance beside the
		// per-environment ones, and page twice for one problem.
		e.log.Warn("alert eval: environments not resolved; skipping this tick", "rule", rule.ID, "err", err)
		return
	}
	if !perEnv {
		eval(ctx, rule)
		return
	}
	e.retireLegacyInstance(ctx, rule)
	for _, env := range envs {
		eval(envctx.With(ctx, env), rule)
	}
}

// environmentsFor decides which environments a rule is evaluated in.
// perEnv is false when the integration has no environments to speak of,
// and the rule is then evaluated the old way.
func (e *Engine) environmentsFor(ctx context.Context, rule AlertRule) (envs []string, perEnv bool, err error) {
	firing, err := e.store.FiringEnvironments(ctx, rule.ID)
	if err != nil {
		return nil, false, err
	}
	set := map[string]bool{}
	// An environment with an open instance is evaluated until the
	// instance resolves, whatever else changed: otherwise it would stay
	// firing forever once its environment stopped sending.
	for _, env := range firing {
		set[env] = true
	}
	if rule.EnvironmentMode == EnvironmentOnly {
		for _, env := range rule.Environments {
			if env != envrole.ProductionToken {
				set[env] = true
			}
		}
		// "Production environments": the integration's environments
		// with the production role now, so prod-us joins the check the
		// moment it sends, and a role changed in the settings applies at
		// the next tick.
		if envrole.HasToken(rule.Environments) {
			have, err := e.envLister.IntegrationEnvironments(ctx, *rule.IntegrationID)
			if err != nil {
				return nil, false, err
			}
			for _, env := range have {
				if envrole.IsProduction(env) {
					set[env] = true
				}
			}
		}
	} else {
		have, err := e.envLister.IntegrationEnvironments(ctx, *rule.IntegrationID)
		if err != nil {
			return nil, false, err
		}
		for _, env := range have {
			set[env] = true
		}
		// Nothing but "(no environment)" and nothing open: the
		// integration has no environments, and the rule stays as it was.
		if len(firing) == 0 && (len(set) == 0 || (len(set) == 1 && set[""])) {
			return nil, false, nil
		}
	}
	for env := range set {
		envs = append(envs, env)
	}
	sort.Strings(envs)
	return envs, true, nil
}

// retireLegacyInstance resolves, without notifying, a rule's "all"
// instance once the rule is evaluated per environment: the
// per-environment instances replace it, and announcing a recovery that
// did not happen would be noise.
func (e *Engine) retireLegacyInstance(ctx context.Context, rule AlertRule) {
	active, err := e.store.ActiveInstanceByFingerprint(ctx, rule.ID, legacyFingerprint)
	if err != nil || active == nil {
		return
	}
	if err := e.store.ResolveInstance(ctx, active.ID, active.Summary+" (now evaluated per environment)"); err != nil {
		e.log.Warn("alert eval: retire whole-scope instance failed", "rule", rule.ID, "err", err)
		return
	}
	e.log.Info("alert instance moved to per-environment evaluation", "rule", rule.Name, "instance", active.ID)
}

// activeInstance is the rule's firing instance for the environment ctx
// is about, or its whole-scope instance.
func (e *Engine) activeInstance(ctx context.Context, rule AlertRule) (*AlertInstance, error) {
	if env, ok := envctx.From(ctx); ok {
		return e.store.ActiveInstanceByFingerprint(ctx, rule.ID, EnvironmentFingerprint(env))
	}
	return e.store.ActiveInstanceByFingerprint(ctx, rule.ID, legacyFingerprint)
}

// openInstance opens the rule's instance for ctx's environment, labelled
// and summarised with it.
func (e *Engine) openInstance(ctx context.Context, rule AlertRule, labels map[string]string, summary string) (AlertInstance, error) {
	fp := legacyFingerprint
	if env, ok := envctx.From(ctx); ok {
		fp = EnvironmentFingerprint(env)
	}
	return e.store.OpenInstance(ctx, rule.ID, fp, withEnvironmentLabel(ctx, labels), environmentSummary(ctx, summary))
}

// refreshInstance rewrites a firing instance's labels and summary.
func (e *Engine) refreshInstance(ctx context.Context, id uuid.UUID, labels map[string]string, summary string) error {
	return e.store.RefreshInstance(ctx, id, withEnvironmentLabel(ctx, labels), environmentSummary(ctx, summary))
}

func withEnvironmentLabel(ctx context.Context, labels map[string]string) map[string]string {
	env, ok := envctx.From(ctx)
	if !ok {
		return labels
	}
	if labels == nil {
		labels = map[string]string{}
	}
	labels[EnvironmentLabel] = env
	return labels
}

// environmentSummary names the environment in a summary, so a page reads
// "… in production" without opening anything.
func environmentSummary(ctx context.Context, summary string) string {
	env, ok := envctx.From(ctx)
	if !ok {
		return summary
	}
	return summary + " [" + envctx.Display(env) + "]"
}
