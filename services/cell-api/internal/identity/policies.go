// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Group access policies. See migration 0020 for the model rationale.
// Five policy kinds:
//
//   service       a specific service_name
//   integration   every service inside an integration (via matchers)
//   attributes    services whose resource attributes match all the kv
//                 pairs in attribute_match
//   compound      integration OR service-scope PLUS attribute filter
//   all_org       wildcard — everything in the org
//
// The resolver below answers the per-request question "what services
// can this user see?" by ORing across all policies on every group the
// user belongs to.

package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PolicyKind enumerates the five access-policy shapes.
type PolicyKind string

const (
	PolicyService     PolicyKind = "service"
	PolicyIntegration PolicyKind = "integration"
	PolicyAttributes  PolicyKind = "attributes"
	PolicyCompound    PolicyKind = "compound"
	PolicyAllOrg      PolicyKind = "all_org"
	// PolicySystem grants every service flagged is_system, optionally
	// narrowed to one system_kind (TargetSystemKind). A specific system
	// stays grantable via PolicyService.
	PolicySystem PolicyKind = "system"
	// PolicyExpression carries an arbitrary AND/OR/NOT boolean tree over
	// service-name and resource-attribute leaves (Conditions). The most
	// flexible kind; see policies_expr.go.
	PolicyExpression PolicyKind = "expression"
)

// Signal is one telemetry dimension a policy can be narrowed to.
type Signal string

const (
	SignalTraces   Signal = "traces"
	SignalLogs     Signal = "logs"
	SignalMetrics  Signal = "metrics"
	SignalMessages Signal = "messages"
)

// AllSignals is the closed signal set, in display order.
var AllSignals = []Signal{SignalTraces, SignalLogs, SignalMetrics, SignalMessages}

func validSignal(s Signal) bool {
	for _, k := range AllSignals {
		if s == k {
			return true
		}
	}
	return false
}

// AccessPolicy is one row in group_access_policies.
type AccessPolicy struct {
	ID                  uuid.UUID  `json:"id"`
	GroupID             uuid.UUID  `json:"group_id"`
	Kind                PolicyKind `json:"kind"`
	TargetServiceName   *string    `json:"target_service_name,omitempty"`
	TargetIntegrationID *uuid.UUID `json:"target_integration_id,omitempty"`
	// TargetSystemKind narrows a kind='system' policy to one system_kind;
	// TargetSystemID narrows it to one system entity. At most one is set;
	// neither means all flagged systems.
	TargetSystemKind *string           `json:"target_system_kind,omitempty"`
	TargetSystemID   *uuid.UUID        `json:"target_system_id,omitempty"`
	AttributeMatch   map[string]string `json:"attribute_match"`
	// Conditions is the boolean tree for kind='expression'; nil otherwise.
	Conditions *PolicyExpr `json:"conditions,omitempty"`
	// Signals narrows the policy to a telemetry subset; empty = all
	// signals (RBAC v2 §7). Signal-narrowed policies never grant manage.
	Signals   []Signal  `json:"signals,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// AccessPolicyInput is the write payload.
type AccessPolicyInput struct {
	Kind                PolicyKind        `json:"kind"`
	TargetServiceName   string            `json:"target_service_name"`
	TargetIntegrationID string            `json:"target_integration_id"`
	TargetSystemKind    string            `json:"target_system_kind"`
	TargetSystemID      string            `json:"target_system_id"`
	AttributeMatch      map[string]string `json:"attribute_match"`
	Conditions          *PolicyExpr       `json:"conditions,omitempty"`
	Signals             []Signal          `json:"signals,omitempty"`
}

// validatePolicyInput enforces the same shape rules as the DB
// CHECK constraint, but with friendlier error messages. Run before
// the INSERT so a bad payload returns 400 instead of 500.
func validatePolicyInput(in *AccessPolicyInput) error {
	in.TargetServiceName = strings.TrimSpace(in.TargetServiceName)
	in.TargetIntegrationID = strings.TrimSpace(in.TargetIntegrationID)
	in.TargetSystemKind = strings.ToLower(strings.TrimSpace(in.TargetSystemKind))
	if in.AttributeMatch == nil {
		in.AttributeMatch = map[string]string{}
	}
	in.TargetSystemID = strings.TrimSpace(in.TargetSystemID)
	// Signals: dedupe, validate, and normalise "all four" to nil (= all,
	// keeps such policies eligible for the Managed tier).
	if len(in.Signals) > 0 {
		seen := map[Signal]struct{}{}
		out := in.Signals[:0]
		for _, sig := range in.Signals {
			sig = Signal(strings.ToLower(strings.TrimSpace(string(sig))))
			if !validSignal(sig) {
				return fmt.Errorf("unknown signal %q (want traces/logs/metrics/messages)", sig)
			}
			if _, dup := seen[sig]; dup {
				continue
			}
			seen[sig] = struct{}{}
			out = append(out, sig)
		}
		in.Signals = out
		if len(in.Signals) == len(AllSignals) {
			in.Signals = nil
		}
	}
	// target_system_kind / target_system_id only apply to kind='system'.
	if in.Kind != PolicySystem && in.TargetSystemKind != "" {
		return errors.New("target_system_kind only applies to a system policy")
	}
	if in.Kind != PolicySystem && in.TargetSystemID != "" {
		return errors.New("target_system_id only applies to a system policy")
	}
	if in.TargetSystemKind != "" && in.TargetSystemID != "" {
		return errors.New("system policy takes target_system_kind OR target_system_id, not both")
	}
	switch in.Kind {
	case PolicyService:
		if in.TargetServiceName == "" {
			return errors.New("service policy requires target_service_name")
		}
		if in.TargetIntegrationID != "" || len(in.AttributeMatch) > 0 {
			return errors.New("service policy must not set integration/attributes")
		}
	case PolicyIntegration:
		if in.TargetIntegrationID == "" {
			return errors.New("integration policy requires target_integration_id")
		}
		if in.TargetServiceName != "" || len(in.AttributeMatch) > 0 {
			return errors.New("integration policy must not set service/attributes")
		}
	case PolicyAttributes:
		if len(in.AttributeMatch) == 0 {
			return errors.New("attributes policy requires at least one attribute_match kv")
		}
		if in.TargetServiceName != "" || in.TargetIntegrationID != "" {
			return errors.New("attributes policy must not set service/integration")
		}
	case PolicyCompound:
		if in.TargetServiceName == "" && in.TargetIntegrationID == "" {
			return errors.New("compound policy requires target_service_name or target_integration_id")
		}
		if len(in.AttributeMatch) == 0 {
			return errors.New("compound policy requires attribute_match")
		}
	case PolicyAllOrg:
		if in.TargetServiceName != "" || in.TargetIntegrationID != "" || len(in.AttributeMatch) > 0 {
			return errors.New("all_org policy must not set targets/attributes")
		}
	case PolicySystem:
		// target_system_kind is optional (empty = all systems).
		if in.TargetServiceName != "" || in.TargetIntegrationID != "" || len(in.AttributeMatch) > 0 {
			return errors.New("system policy must not set service/integration/attributes")
		}
	case PolicyExpression:
		if in.TargetServiceName != "" || in.TargetIntegrationID != "" || len(in.AttributeMatch) > 0 {
			return errors.New("expression policy must not set service/integration/attributes")
		}
		if err := ValidateExpr(in.Conditions); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid policy kind: %s", in.Kind)
	}
	return nil
}

// ── CRUD ────────────────────────────────────────────────────────────

// CreatePolicy inserts a row into group_access_policies after
// validating the kind-specific shape.
func (s *Store) CreatePolicy(ctx context.Context, groupID uuid.UUID, in AccessPolicyInput) (AccessPolicy, error) {
	if err := validatePolicyInput(&in); err != nil {
		return AccessPolicy{}, err
	}
	var (
		serviceArg     interface{}
		integrationArg interface{}
	)
	if in.TargetServiceName != "" {
		serviceArg = in.TargetServiceName
	}
	if in.TargetIntegrationID != "" {
		id, err := uuid.Parse(in.TargetIntegrationID)
		if err != nil {
			return AccessPolicy{}, fmt.Errorf("invalid target_integration_id: %v", err)
		}
		integrationArg = id
	}
	var systemKindArg interface{}
	if in.TargetSystemKind != "" {
		systemKindArg = in.TargetSystemKind
	}
	var systemIDArg interface{}
	if in.TargetSystemID != "" {
		id, err := uuid.Parse(in.TargetSystemID)
		if err != nil {
			return AccessPolicy{}, fmt.Errorf("invalid target_system_id: %v", err)
		}
		systemIDArg = id
	}
	attrJSON, err := json.Marshal(in.AttributeMatch)
	if err != nil {
		return AccessPolicy{}, fmt.Errorf("attribute_match marshal: %w", err)
	}
	var condArg interface{}
	if in.Kind == PolicyExpression && in.Conditions != nil {
		condJSON, err := json.Marshal(in.Conditions)
		if err != nil {
			return AccessPolicy{}, fmt.Errorf("conditions marshal: %w", err)
		}
		condArg = string(condJSON)
	}
	var signalsArg interface{}
	if len(in.Signals) > 0 {
		sigs := make([]string, 0, len(in.Signals))
		for _, sg := range in.Signals {
			sigs = append(sigs, string(sg))
		}
		signalsArg = sigs
	}
	const q = `
		INSERT INTO group_access_policies
		    (group_id, kind, target_service_name, target_integration_id, target_system_kind, target_system_id, attribute_match, conditions, signals)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9)
		RETURNING id, group_id, kind, target_service_name, target_integration_id, target_system_kind, target_system_id, attribute_match, conditions, signals, created_at`
	row := s.pool.QueryRow(ctx, q, groupID, in.Kind, serviceArg, integrationArg, systemKindArg, systemIDArg, string(attrJSON), condArg, signalsArg)
	return scanPolicy(row)
}

// DeletePolicy removes a single policy by id. Caller is expected to
// have verified the policy belongs to a group the caller may admin.
func (s *Store) DeletePolicy(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM group_access_policies WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("identity: delete policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPoliciesForGroup returns every policy attached to a group,
// newest first (a UI convenience — fresh edits float).
func (s *Store) ListPoliciesForGroup(ctx context.Context, groupID uuid.UUID) ([]AccessPolicy, error) {
	const q = `
		SELECT id, group_id, kind, target_service_name, target_integration_id, target_system_kind, target_system_id, attribute_match, conditions, signals, created_at
		FROM group_access_policies
		WHERE group_id = $1
		ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, groupID)
	if err != nil {
		return nil, fmt.Errorf("identity: list policies: %w", err)
	}
	defer rows.Close()
	out := make([]AccessPolicy, 0)
	for rows.Next() {
		p, err := scanPolicyRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MemberRef identifies the group-membership-bearing principal a
// visibility resolution runs for: a user or a service account
// (docs/service-account-scoping-design.md). Exactly one field is set.
// Internal callers and org-wide short-circuits never reach the
// resolver — handlers decide that BEFORE building a ref.
type MemberRef struct {
	UserID           *uuid.UUID
	ServiceAccountID *uuid.UUID
}

// UserRef builds a MemberRef for a user principal.
func UserRef(id uuid.UUID) MemberRef { return MemberRef{UserID: &id} }

// ServiceAccountRef builds a MemberRef for a scoped service account.
func ServiceAccountRef(id uuid.UUID) MemberRef { return MemberRef{ServiceAccountID: &id} }

// ListPoliciesForUser returns every policy active for one user
// across all their groups within an org. Used by the visibility
// resolver below; called once per request and cached on the
// principal-context where possible.
func (s *Store) ListPoliciesForUser(ctx context.Context, userID, orgID uuid.UUID) ([]UserPolicy, error) {
	return s.ListPoliciesForMember(ctx, UserRef(userID), orgID)
}

// ListPoliciesForMember is ListPoliciesForUser generalised over the
// two membership kinds — same join, membership column picked by ref.
func (s *Store) ListPoliciesForMember(ctx context.Context, ref MemberRef, orgID uuid.UUID) ([]UserPolicy, error) {
	memberCol, memberID := "gm.user_id", uuid.Nil
	switch {
	case ref.UserID != nil:
		memberID = *ref.UserID
	case ref.ServiceAccountID != nil:
		memberCol, memberID = "gm.service_account_id", *ref.ServiceAccountID
	default:
		return nil, fmt.Errorf("identity: empty member ref")
	}
	q := `
		SELECT p.id, p.group_id, p.kind, p.target_service_name, p.target_integration_id, p.target_system_kind, p.target_system_id, p.attribute_match, p.conditions, p.signals, p.created_at, gm.role
		FROM group_access_policies p
		JOIN groups g ON g.id = p.group_id
		JOIN group_members gm ON gm.group_id = g.id
		WHERE ` + memberCol + ` = $1 AND g.org_id = $2
		ORDER BY p.created_at DESC`
	rows, err := s.pool.Query(ctx, q, memberID, orgID)
	if err != nil {
		return nil, fmt.Errorf("identity: list user policies: %w", err)
	}
	defer rows.Close()
	out := make([]UserPolicy, 0)
	for rows.Next() {
		var up UserPolicy
		var attrBytes, condBytes []byte
		var sigs []string
		if err := rows.Scan(&up.ID, &up.GroupID, &up.Kind,
			&up.TargetServiceName, &up.TargetIntegrationID, &up.TargetSystemKind, &up.TargetSystemID,
			&attrBytes, &condBytes, &sigs, &up.CreatedAt, &up.GroupRole); err != nil {
			return nil, err
		}
		hydratePolicy(&up.AccessPolicy, attrBytes, condBytes)
		for _, sg := range sigs {
			up.Signals = append(up.Signals, Signal(sg))
		}
		out = append(out, up)
	}
	return out, rows.Err()
}

func scanPolicy(row pgx.Row) (AccessPolicy, error) {
	var p AccessPolicy
	var attrBytes, condBytes []byte
	var sigs []string
	if err := row.Scan(&p.ID, &p.GroupID, &p.Kind,
		&p.TargetServiceName, &p.TargetIntegrationID, &p.TargetSystemKind, &p.TargetSystemID, &attrBytes, &condBytes, &sigs, &p.CreatedAt); err != nil {
		return AccessPolicy{}, err
	}
	hydratePolicy(&p, attrBytes, condBytes)
	for _, sg := range sigs {
		p.Signals = append(p.Signals, Signal(sg))
	}
	return p, nil
}

func scanPolicyRows(row pgx.Rows) (AccessPolicy, error) {
	var p AccessPolicy
	var attrBytes, condBytes []byte
	var sigs []string
	if err := row.Scan(&p.ID, &p.GroupID, &p.Kind,
		&p.TargetServiceName, &p.TargetIntegrationID, &p.TargetSystemKind, &p.TargetSystemID, &attrBytes, &condBytes, &sigs, &p.CreatedAt); err != nil {
		return AccessPolicy{}, err
	}
	hydratePolicy(&p, attrBytes, condBytes)
	for _, sg := range sigs {
		p.Signals = append(p.Signals, Signal(sg))
	}
	return p, nil
}

// hydratePolicy fills the JSONB-backed fields after a row scan.
func hydratePolicy(p *AccessPolicy, attrBytes, condBytes []byte) {
	if len(attrBytes) > 0 {
		_ = json.Unmarshal(attrBytes, &p.AttributeMatch)
	}
	if p.AttributeMatch == nil {
		p.AttributeMatch = map[string]string{}
	}
	if len(condBytes) > 0 {
		var expr PolicyExpr
		if err := json.Unmarshal(condBytes, &expr); err == nil {
			p.Conditions = &expr
		}
	}
}

// ── visibility resolution ────────────────────────────────────────────

// EffectiveAccess is the resolved per-user permission set. all_org
// short-circuits everything else (the wildcard); otherwise we have
// explicit service names + a list of attribute predicates the caller
// composes into queries.
//
// AllOrg=true ⇒ user sees everything; ignore the other slices.
// AllOrg=false AND len(Services)==0 AND len(Attribute predicates)==0
//
//	⇒ user sees nothing (strict-isolation default for users with no
//	policy-bearing memberships).
type EffectiveAccess struct {
	// AllOrg is set when any policy is kind=all_org. The wildcard
	// escape hatch — used for "sees everything" groups.
	AllOrg bool
	// Services is the explicit allowlist resolved from kind=service
	// + kind=integration (the latter expands via matchers) +
	// kind=compound's target side. Stored as a set for O(1) checks.
	Services map[string]struct{}
	// AttributePredicates is the list of attribute_match maps from
	// kind=attributes + kind=compound. Each predicate is AND-internal
	// (all kv must match); predicates between themselves are OR.
	// Used by the ClickHouse query rewriter (G5).
	AttributePredicates []map[string]string
	// Expressions is the list of raw boolean trees from kind=expression
	// policies. Each is evaluated against the org's service universe in
	// ResolveVisibleServiceSet and its result UNIONed in.
	Expressions []PolicyExpr
	// CompoundPredicates is the subset of policies that constrain
	// BOTH a service/integration AND an attribute filter — the
	// caller treats these as "data inside service X where attrs
	// match". Separated out so the query rewriter can emit the
	// AND'd shape (svc=X AND attrs match) rather than just adding
	// X to the service allowlist (which would over-grant).
	CompoundPredicates []CompoundPredicate
}

// CompoundPredicate represents a kind=compound policy in resolved
// form. Exactly one of Services / IntegrationServices is non-empty
// (matchers expanded for the integration case).
type CompoundPredicate struct {
	Services       []string
	AttributeMatch map[string]string
}

// HasNoAccess reports whether the user has no policies at all —
// strict-isolation case where every read filter returns nothing.
func (e EffectiveAccess) HasNoAccess() bool {
	return !e.AllOrg && len(e.Services) == 0 && len(e.AttributePredicates) == 0 &&
		len(e.CompoundPredicates) == 0 && len(e.Expressions) == 0
}

// integrationExpander is the dependency the resolver needs to turn
// kind=integration policies into concrete service lists. We pass it
// in rather than importing the integrations package directly so
// identity stays at the bottom of the dependency graph.
type integrationExpander func(ctx context.Context, orgID, integrationID uuid.UUID) ([]string, error)

// systemExpander turns a kind=system policy into concrete member service
// names. Narrowing: systemID (one system entity) wins when non-nil, else
// systemKind ("" = all flagged systems). Passed in (like
// integrationExpander) so identity stays at the bottom of the graph.
type systemExpander func(ctx context.Context, orgID uuid.UUID, systemKind string, systemID *uuid.UUID) ([]string, error)

// serviceUniverseProvider returns every service name in the org. Needed
// to evaluate kind=expression policies — NOT complements against it, and
// service-name leaves iterate it. Passed in (like the expanders) so
// identity stays at the bottom of the dependency graph.
type serviceUniverseProvider func(ctx context.Context, orgID uuid.UUID) ([]string, error)

// UserPolicy is an AccessPolicy annotated with the role the user holds
// in the group that carries it — the capability axis (RBAC v2 §2):
// viewer → the policy contributes to Visible only; editor+ → it also
// contributes to Managed.
type UserPolicy struct {
	AccessPolicy
	GroupRole Role
}

// AccessSets is the two-tier resolution result: what the user may SEE
// and the subset they may MANAGE (always Managed ⊆ Visible by
// construction — managed policies are a subset of all policies).
type AccessSets struct {
	Visible    map[string]struct{}
	VisibleAll bool
	Managed    map[string]struct{}
	ManagedAll bool
	// VisibleBySignal narrows Visible per telemetry signal (RBAC v2 §7):
	// a signal's set = union of scopes of policies covering that signal
	// (+ shares, which are all-signal). Nil map values never occur; when
	// VisibleAll is true the per-signal maps are not populated (callers
	// short-circuit on the wildcard first).
	VisibleBySignal map[Signal]map[string]struct{}
	// SignalAll marks signals with a wildcard grant.
	SignalAll map[Signal]bool
}

// VisibleFor returns the visible set for one signal (set, wildcard).
func (a AccessSets) VisibleFor(sig Signal) (map[string]struct{}, bool) {
	if a.VisibleAll || a.SignalAll[sig] {
		return nil, true
	}
	if a.VisibleBySignal == nil {
		return map[string]struct{}{}, false
	}
	set := a.VisibleBySignal[sig]
	if set == nil {
		set = map[string]struct{}{}
	}
	return set, false
}

// ResolveEffectiveAccess composes all of a user's policies into a
// single EffectiveAccess. The integrationExpander is called per
// integration-bearing policy to get the matching service names; it's
// expected to use whatever the caller already has wired (typically
// integrations.Resolver.ServicesFor). expandSystem resolves kind=system
// policies to flagged-system services.
func (s *Store) ResolveEffectiveAccess(ctx context.Context, userID, orgID uuid.UUID, expand integrationExpander, expandSystem systemExpander) (EffectiveAccess, error) {
	return s.ResolveEffectiveAccessMember(ctx, UserRef(userID), orgID, expand, expandSystem)
}

// ResolveEffectiveAccessMember is ResolveEffectiveAccess for any
// membership-bearing principal (user or scoped service account).
func (s *Store) ResolveEffectiveAccessMember(ctx context.Context, ref MemberRef, orgID uuid.UUID, expand integrationExpander, expandSystem systemExpander) (EffectiveAccess, error) {
	policies, err := s.ListPoliciesForMember(ctx, ref, orgID)
	if err != nil {
		return EffectiveAccess{}, err
	}
	all := make([]AccessPolicy, 0, len(policies))
	for _, up := range policies {
		all = append(all, up.AccessPolicy)
	}
	return s.composeAccess(ctx, orgID, all, expand, expandSystem)
}

// composeAccess folds a policy list into EffectiveAccess (the shared
// composition path for both the Visible and Managed resolutions).
func (s *Store) composeAccess(ctx context.Context, orgID uuid.UUID, policies []AccessPolicy, expand integrationExpander, expandSystem systemExpander) (EffectiveAccess, error) {
	out := EffectiveAccess{Services: map[string]struct{}{}}
	for _, p := range policies {
		switch p.Kind {
		case PolicyAllOrg:
			out.AllOrg = true
			return out, nil // short-circuit — wildcard wins
		case PolicyService:
			if p.TargetServiceName != nil {
				out.Services[*p.TargetServiceName] = struct{}{}
			}
		case PolicySystem:
			if expandSystem != nil {
				kind := ""
				if p.TargetSystemKind != nil {
					kind = *p.TargetSystemKind
				}
				svcs, err := expandSystem(ctx, orgID, kind, p.TargetSystemID)
				if err != nil {
					return EffectiveAccess{}, fmt.Errorf("expand system: %w", err)
				}
				for _, name := range svcs {
					out.Services[name] = struct{}{}
				}
			}
		case PolicyIntegration:
			if p.TargetIntegrationID != nil && expand != nil {
				svcs, err := expand(ctx, orgID, *p.TargetIntegrationID)
				if err != nil {
					return EffectiveAccess{}, fmt.Errorf("expand integration: %w", err)
				}
				for _, name := range svcs {
					out.Services[name] = struct{}{}
				}
			}
		case PolicyAttributes:
			out.AttributePredicates = append(out.AttributePredicates, p.AttributeMatch)
		case PolicyCompound:
			cp := CompoundPredicate{AttributeMatch: p.AttributeMatch}
			if p.TargetServiceName != nil {
				cp.Services = []string{*p.TargetServiceName}
			} else if p.TargetIntegrationID != nil && expand != nil {
				svcs, err := expand(ctx, orgID, *p.TargetIntegrationID)
				if err != nil {
					return EffectiveAccess{}, fmt.Errorf("expand integration: %w", err)
				}
				cp.Services = svcs
			}
			out.CompoundPredicates = append(out.CompoundPredicates, cp)
		case PolicyExpression:
			if p.Conditions != nil {
				out.Expressions = append(out.Expressions, *p.Conditions)
			}
		}
	}
	return out, nil
}

// ServicesMatchingAttributes returns the distinct service_names whose
// recent resource attributes satisfy ALL the kv pairs in `match`.
// Backed by the service_resource_attributes table populated by the
// catalog reconciler. An empty match map returns nothing (callers
// validate before calling).
func (s *Store) ServicesMatchingAttributes(ctx context.Context, orgID uuid.UUID, match map[string]string) ([]string, error) {
	if len(match) == 0 {
		return nil, nil
	}
	// One INTERSECT per kv pair — efficient at small N and lets PG
	// use the (org_id, attr_key, attr_value) index.
	parts := make([]string, 0, len(match))
	args := []any{orgID}
	for k, v := range match {
		args = append(args, k, v)
		i := len(args)
		parts = append(parts,
			fmt.Sprintf("SELECT service_name FROM service_resource_attributes WHERE org_id = $1 AND attr_key = $%d AND attr_value = $%d", i-1, i))
	}
	query := strings.Join(parts, " INTERSECT ")
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("identity: services matching attrs: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// allServiceAttributes loads every service's resource attributes for an
// org as attrs[serviceName][attrKey] = attrValue. Used by the expression
// evaluator, which needs the full per-service attribute picture (not just
// "does any service match this one kv"). Bounded by the org's catalog
// size × distinct attribute keys — admin-scale, not telemetry-scale.
func (s *Store) allServiceAttributes(ctx context.Context, orgID uuid.UUID) (map[string]map[string]string, error) {
	const q = `SELECT service_name, attr_key, attr_value FROM service_resource_attributes WHERE org_id = $1`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("identity: all service attributes: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var svc, k, v string
		if err := rows.Scan(&svc, &k, &v); err != nil {
			return nil, err
		}
		m := out[svc]
		if m == nil {
			m = map[string]string{}
			out[svc] = m
		}
		m[k] = v
	}
	return out, rows.Err()
}

// ResolveVisibleServiceSet returns the union of service names the
// user can see across all policy kinds:
//   - all_org: returns ("", true) meaning "no filter — show everything"
//   - empty access: returns (empty, false) meaning "filter to none"
//   - else: returns (set, false) — only these services are visible
//
// AttributePredicates resolve via ServicesMatchingAttributes against
// the catalog's resource-attribute snapshot.
//
// CompoundPredicates intersect their service-side with their
// attribute-side: a compound policy "Integration A where env=prod"
// grants only services in A that also have env=prod in their catalog
// attributes.
func (s *Store) ResolveVisibleServiceSet(ctx context.Context, userID, orgID uuid.UUID, expand integrationExpander, expandSystem systemExpander, universe serviceUniverseProvider) (set map[string]struct{}, wildcard bool, err error) {
	return s.ResolveVisibleServiceSetMember(ctx, UserRef(userID), orgID, expand, expandSystem, universe)
}

// ResolveVisibleServiceSetMember is ResolveVisibleServiceSet for any
// membership-bearing principal. Shares are a user-only supplement
// (resource shares target org members, not machines — §6); service
// accounts resolve from group policies alone.
func (s *Store) ResolveVisibleServiceSetMember(ctx context.Context, ref MemberRef, orgID uuid.UUID, expand integrationExpander, expandSystem systemExpander, universe serviceUniverseProvider) (set map[string]struct{}, wildcard bool, err error) {
	access, err := s.ResolveEffectiveAccessMember(ctx, ref, orgID, expand, expandSystem)
	if err != nil {
		return nil, false, err
	}
	set, wildcard, err = s.materializeAccess(ctx, orgID, access, universe)
	if err != nil || wildcard {
		return set, wildcard, err
	}
	if ref.UserID == nil {
		return set, false, nil
	}
	// Shares supplement Visible (never Managed) — see §6.
	shared, err := s.expandShares(ctx, *ref.UserID, orgID, expand, expandSystem)
	if err != nil {
		return nil, false, err
	}
	for n := range shared {
		set[n] = struct{}{}
	}
	return set, false, nil
}

// ResolveAccessSets is the two-tier resolution (RBAC v2 §2): Visible from
// ALL the user's policies, Managed from only the policies carried by
// groups where the user is editor+. One policy fetch, two compositions.
func (s *Store) ResolveAccessSets(ctx context.Context, userID, orgID uuid.UUID, expand integrationExpander, expandSystem systemExpander, universe serviceUniverseProvider) (AccessSets, error) {
	return s.ResolveAccessSetsMember(ctx, UserRef(userID), orgID, expand, expandSystem, universe)
}

// ResolveAccessSetsMember is ResolveAccessSets for any membership-
// bearing principal. Shares (the user-only Visible supplement) are
// skipped for service accounts.
func (s *Store) ResolveAccessSetsMember(ctx context.Context, ref MemberRef, orgID uuid.UUID, expand integrationExpander, expandSystem systemExpander, universe serviceUniverseProvider) (AccessSets, error) {
	policies, err := s.ListPoliciesForMember(ctx, ref, orgID)
	if err != nil {
		return AccessSets{}, err
	}
	policyCoversSignal := func(up UserPolicy, sig Signal) bool {
		if len(up.Signals) == 0 {
			return true
		}
		for _, ps := range up.Signals {
			if ps == sig {
				return true
			}
		}
		return false
	}

	all := make([]AccessPolicy, 0, len(policies))
	managed := make([]AccessPolicy, 0)
	perSignal := map[Signal][]AccessPolicy{}
	perSignalKey := map[Signal]string{}
	for _, up := range policies {
		all = append(all, up.AccessPolicy)
		// Managed requires FULL-signal scope (§7): a signal-narrowed
		// policy never grants manage — you can't manage what you can
		// only partially observe.
		if up.GroupRole.CanWrite() && len(up.Signals) == 0 {
			managed = append(managed, up.AccessPolicy)
		}
		for _, sig := range AllSignals {
			if policyCoversSignal(up, sig) {
				perSignal[sig] = append(perSignal[sig], up.AccessPolicy)
				perSignalKey[sig] += up.ID.String() + ","
			}
		}
	}

	out := AccessSets{VisibleBySignal: map[Signal]map[string]struct{}{}, SignalAll: map[Signal]bool{}}
	// materialize memo: identical policy subsets share one evaluation —
	// the common all-signal case costs exactly one extra map lookup per
	// signal, not four re-resolutions.
	type matResult struct {
		set      map[string]struct{}
		wildcard bool
	}
	memo := map[string]matResult{}
	materialize := func(key string, pols []AccessPolicy) (map[string]struct{}, bool, error) {
		if r, ok := memo[key]; ok {
			return r.set, r.wildcard, nil
		}
		access, err := s.composeAccess(ctx, orgID, pols, expand, expandSystem)
		if err != nil {
			return nil, false, err
		}
		set, wildcard, err := s.materializeAccess(ctx, orgID, access, universe)
		if err != nil {
			return nil, false, err
		}
		memo[key] = matResult{set: set, wildcard: wildcard}
		return set, wildcard, nil
	}

	allKey := ""
	for _, ap := range all {
		allKey += ap.ID.String() + ","
	}
	var err2 error
	if out.Visible, out.VisibleAll, err2 = materialize(allKey, all); err2 != nil {
		return AccessSets{}, err2
	}
	manKey := "managed:"
	for _, ap := range managed {
		manKey += ap.ID.String() + ","
	}
	if out.Managed, out.ManagedAll, err2 = materialize(manKey, managed); err2 != nil {
		return AccessSets{}, err2
	}
	if !out.VisibleAll {
		for _, sig := range AllSignals {
			set, wild, err := materialize(perSignalKey[sig], perSignal[sig])
			if err != nil {
				return AccessSets{}, err
			}
			out.SignalAll[sig] = wild
			if !wild {
				// Copy: shares are appended below and must reach every
				// signal without aliasing the memoized map.
				cp := make(map[string]struct{}, len(set))
				for n := range set {
					cp[n] = struct{}{}
				}
				out.VisibleBySignal[sig] = cp
			}
		}
		shared := map[string]struct{}{}
		if ref.UserID != nil {
			if shared, err = s.expandShares(ctx, *ref.UserID, orgID, expand, expandSystem); err != nil {
				return AccessSets{}, err
			}
		}
		// Visible may alias the memoized map too — copy before adding shares.
		vis := make(map[string]struct{}, len(out.Visible)+len(shared))
		for n := range out.Visible {
			vis[n] = struct{}{}
		}
		for n := range shared {
			vis[n] = struct{}{}
		}
		out.Visible = vis
		for _, sig := range AllSignals {
			if out.SignalAll[sig] {
				continue
			}
			for n := range shared {
				out.VisibleBySignal[sig][n] = struct{}{}
			}
		}
	}
	return out, nil
}

// expandShares resolves the user's shared resources into service names —
// the Visible-only supplement (RBAC v2 §6). Shares never touch Managed.
func (s *Store) expandShares(ctx context.Context, userID, orgID uuid.UUID, expand integrationExpander, expandSystem systemExpander) (map[string]struct{}, error) {
	shares, err := s.SharedResourcesForUser(ctx, userID, orgID)
	if err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	for _, sr := range shares {
		var names []string
		switch sr.ResourceKind {
		case ShareIntegration:
			if expand != nil {
				if names, err = expand(ctx, orgID, sr.ResourceID); err != nil {
					return nil, fmt.Errorf("expand shared integration: %w", err)
				}
			}
		case ShareSystem:
			if expandSystem != nil {
				id := sr.ResourceID
				if names, err = expandSystem(ctx, orgID, "", &id); err != nil {
					return nil, fmt.Errorf("expand shared system: %w", err)
				}
			}
		}
		for _, n := range names {
			out[n] = struct{}{}
		}
	}
	return out, nil
}

// materializeAccess turns a composed EffectiveAccess into a concrete
// service-name set (or a wildcard).
func (s *Store) materializeAccess(ctx context.Context, orgID uuid.UUID, access EffectiveAccess, universe serviceUniverseProvider) (set map[string]struct{}, wildcard bool, err error) {
	if access.AllOrg {
		return nil, true, nil
	}
	if access.HasNoAccess() {
		return map[string]struct{}{}, false, nil
	}
	out := make(map[string]struct{}, len(access.Services))
	for name := range access.Services {
		out[name] = struct{}{}
	}
	// Expression policies: evaluate each boolean tree against the org's
	// service universe + resource attributes, union the results.
	if len(access.Expressions) > 0 {
		var allNames []string
		if universe != nil {
			if allNames, err = universe(ctx, orgID); err != nil {
				return nil, false, fmt.Errorf("resolve service universe: %w", err)
			}
		}
		attrs, err := s.allServiceAttributes(ctx, orgID)
		if err != nil {
			return nil, false, fmt.Errorf("load service attributes: %w", err)
		}
		for i := range access.Expressions {
			for name := range evalExpr(&access.Expressions[i], allNames, attrs) {
				out[name] = struct{}{}
			}
		}
	}
	// Pure attribute policies — every service matching the predicate
	// is visible.
	for _, predicate := range access.AttributePredicates {
		names, err := s.ServicesMatchingAttributes(ctx, orgID, predicate)
		if err != nil {
			return nil, false, err
		}
		for _, n := range names {
			out[n] = struct{}{}
		}
	}
	// Compound — service ∈ target AND service matches attrs.
	for _, cp := range access.CompoundPredicates {
		if len(cp.Services) == 0 {
			continue
		}
		matching, err := s.ServicesMatchingAttributes(ctx, orgID, cp.AttributeMatch)
		if err != nil {
			return nil, false, err
		}
		matchSet := make(map[string]struct{}, len(matching))
		for _, n := range matching {
			matchSet[n] = struct{}{}
		}
		for _, n := range cp.Services {
			if _, ok := matchSet[n]; ok {
				out[n] = struct{}{}
			}
		}
	}
	return out, false, nil
}

// ── service_resource_attributes upsert (called by the catalog reconciler) ────

// UpsertServiceResourceAttributes inserts or refreshes the last_seen_at
// for each (svc, k, v) tuple. Called by the catalog reconciler with a
// batch sampled from recent telemetry.
func (s *Store) UpsertServiceResourceAttributes(ctx context.Context, orgID uuid.UUID, serviceName string, attrs map[string]string) error {
	if len(attrs) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for k, v := range attrs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO service_resource_attributes (org_id, service_name, attr_key, attr_value)
			   VALUES ($1, $2, $3, $4)
			   ON CONFLICT (org_id, service_name, attr_key, attr_value)
			   DO UPDATE SET last_seen_at = now()`,
			orgID, serviceName, k, v); err != nil {
			return fmt.Errorf("upsert sra: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// PruneStaleServiceResourceAttributes deletes (svc, k, v) rows whose
// last_seen_at is older than the given cutoff. Called periodically
// by the reconciler so attributes a service stopped emitting drop
// off and stop granting access.
func (s *Store) PruneStaleServiceResourceAttributes(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM service_resource_attributes WHERE last_seen_at < $1`,
		olderThan)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// AttributeKey is one distinct attribute key seen in telemetry, with the
// map it came from (span or resource). Drives the attribute picker in the
// integration matcher editor + the message-view filter builder.
type AttributeKey struct {
	Key    string `json:"key"`
	Source string `json:"source"` // "span" | "resource"
}

// UpsertAttributeKeys records the distinct attribute keys seen this tick
// (keyed by attr_key → source), refreshing last_seen_at on existing rows.
// Idempotent. Primitive map shape mirrors UpsertServiceResourceAttributes
// so the reconciler's AttributeSink stays free of identity types.
func (s *Store) UpsertAttributeKeys(ctx context.Context, orgID uuid.UUID, keys map[string]string) error {
	if len(keys) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for key, src := range keys {
		if key == "" {
			continue
		}
		if src != "resource" {
			src = "span"
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO attribute_keys (org_id, attr_key, source)
			   VALUES ($1, $2, $3)
			   ON CONFLICT (org_id, attr_key)
			   DO UPDATE SET last_seen_at = now(), source = EXCLUDED.source`,
			orgID, key, src); err != nil {
			return fmt.Errorf("upsert attribute key: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// ListAttributeKeys returns the org's distinct attribute keys, most
// recently seen first. The picker dedupes by key across sources.
func (s *Store) ListAttributeKeys(ctx context.Context, orgID uuid.UUID) ([]AttributeKey, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT attr_key, source FROM attribute_keys
		   WHERE org_id = $1
		   ORDER BY last_seen_at DESC, attr_key ASC`,
		orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AttributeKey{}
	for rows.Next() {
		var k AttributeKey
		if err := rows.Scan(&k.Key, &k.Source); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// PruneStaleAttributeKeys drops keys not seen since olderThan, so the
// picker stops offering attributes the fleet no longer emits.
func (s *Store) PruneStaleAttributeKeys(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM attribute_keys WHERE last_seen_at < $1`,
		olderThan)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
