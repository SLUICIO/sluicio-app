// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Proposal kinds that author rather than tune: a new health check, a new
// trace-completion rule, and an integration's settings. Each is filed
// with the exact body a person would send to make the change, checked
// when filed so the inbox only holds changes that can be approved, and
// applied by replaying that request as the approving reviewer
// (replayAsReviewer). The route's own permission gates and validation
// then decide, and its audit entry names the reviewer, exactly as if
// they had made the change by hand.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/proposals"
)

// Kinds, as target_kind on the wire.
const (
	proposalAlertRule          = "alert_rule"
	proposalNewAlertRule       = "new_alert_rule"
	proposalNewCompletionRule  = "new_completion_rule"
	proposalIntegrationSetting = "integration_settings"
)

// integrationSettingFields are what an integration_settings proposal may
// change, each the body of the route that changes it.
var integrationSettingFields = map[string]bool{
	"matchers":                     true,
	"notify_unacknowledged_errors": true,
	"declared_environments":        true,
	"sources":                      true,
}

func init() {
	proposalTargets[proposalNewAlertRule] = proposalTarget{
		fields:  map[string]bool{"rule": true},
		creates: true,
		check: func(h *Handlers, r *http.Request, _ *uuid.UUID, changes []proposals.Change) (string, string, error) {
			var req alertRuleRequest
			if err := json.Unmarshal(changes[0].After, &req); err != nil {
				return "", "", fmt.Errorf("rule: %w", err)
			}
			rule, err := h.buildRule(middleware.OrgID(r), uuid.Nil, req)
			if err != nil {
				return "", "", err
			}
			if !h.canSeeAlertTarget(r, rule.ServiceName, rule.IntegrationID, rule.SystemID) {
				return "", "", errors.New("the check's scope was not found")
			}
			return "New check on " + h.ruleScopeName(r, rule) + ": " + rule.Name,
				"new_alert_rule:" + ruleScopeKey(rule) + ":" + strings.ToLower(rule.Name), nil
		},
		snapshot: nullSnapshot("rule"),
		apply: func(h *Handlers, r *http.Request, _ *uuid.UUID, changes []proposals.Change) error {
			return h.replayAsReviewer(r, http.MethodPost, "/api/v1/alert-rules", changes[0].After)
		},
	}

	proposalTargets[proposalNewCompletionRule] = proposalTarget{
		fields:      map[string]bool{"rule": true},
		creates:     true,
		needsTarget: true,
		check: func(h *Handlers, r *http.Request, id *uuid.UUID, changes []proposals.Change) (string, string, error) {
			name, err := h.integrationName(r, *id)
			if err != nil {
				return "", "", err
			}
			var body traceCompletionRuleInput
			if err := json.Unmarshal(changes[0].After, &body); err != nil {
				return "", "", fmt.Errorf("rule: %w", err)
			}
			in, perr := h.parseTraceCompletionInput(body, *id)
			if perr != "" {
				return "", "", errors.New(perr)
			}
			if err := in.Validate(); err != nil {
				return "", "", err
			}
			return "New completion rule on " + name + ": " + in.Name, "new_completion_rule:" + id.String() + ":" + strings.ToLower(in.Name), nil
		},
		snapshot: nullSnapshot("rule"),
		apply: func(h *Handlers, r *http.Request, id *uuid.UUID, changes []proposals.Change) error {
			return h.replayAsReviewer(r, http.MethodPost, "/api/v1/integrations/"+id.String()+"/completion-rules", changes[0].After)
		},
	}

	proposalTargets[proposalIntegrationSetting] = proposalTarget{
		fields:      integrationSettingFields,
		needsTarget: true,
		check: func(h *Handlers, r *http.Request, id *uuid.UUID, changes []proposals.Change) (string, string, error) {
			name, err := h.integrationName(r, *id)
			if err != nil {
				return "", "", err
			}
			for _, c := range changes {
				if err := checkIntegrationSetting(c); err != nil {
					return "", "", err
				}
			}
			return name, "", nil
		},
		snapshot: func(h *Handlers, ctx context.Context, orgID, id uuid.UUID) (map[string]json.RawMessage, string, error) {
			integ, err := h.Integrations.Get(ctx, orgID, id)
			if err != nil {
				return nil, "", err
			}
			sources, err := h.Integrations.SourcesForIntegration(ctx, id)
			if err != nil {
				return nil, "", err
			}
			out := map[string]json.RawMessage{}
			put := func(k string, v any) {
				if b, mErr := json.Marshal(v); mErr == nil {
					out[k] = b
				}
			}
			put("matchers", matchersAsInput(integ.Matchers))
			put("notify_unacknowledged_errors", integ.Integration.NotifyUnacknowledgedErrors)
			envs := integ.Integration.DeclaredEnvironments
			if envs == nil {
				envs = []string{}
			}
			put("declared_environments", envs)
			put("sources", sourcesAsInput(sources))
			return out, integ.Integration.Name, nil
		},
		apply: func(h *Handlers, r *http.Request, id *uuid.UUID, changes []proposals.Change) error {
			base := "/api/v1/integrations/" + id.String()
			for _, c := range changes {
				var err error
				switch c.Field {
				case "matchers":
					err = h.replayAsReviewer(r, http.MethodPut, base+"/matchers", wrapJSON("matchers", c.After))
				case "notify_unacknowledged_errors":
					err = h.replayAsReviewer(r, http.MethodPut, base+"/error-notifications", wrapJSON("enabled", c.After))
				case "declared_environments":
					err = h.replayAsReviewer(r, http.MethodPut, base+"/declared-environments", wrapJSON("environments", c.After))
				case "sources":
					err = h.replayAsReviewer(r, http.MethodPut, base+"/sources", wrapJSON("sources", c.After))
				default:
					err = fmt.Errorf("field %q is not proposable on an integration", c.Field)
				}
				if err != nil {
					return fmt.Errorf("%s: %w", c.Field, err)
				}
			}
			return nil
		},
	}
}

// nullSnapshot is the "before" of a create: nothing.
func nullSnapshot(field string) func(*Handlers, context.Context, uuid.UUID, uuid.UUID) (map[string]json.RawMessage, string, error) {
	return func(*Handlers, context.Context, uuid.UUID, uuid.UUID) (map[string]json.RawMessage, string, error) {
		return map[string]json.RawMessage{field: json.RawMessage("null")}, "", nil
	}
}

// replayAsReviewer sends the request a person would send to make this
// change, carrying the approving reviewer's credentials, through the
// router. Whatever that route refuses the person, it refuses here.
func (h *Handlers) replayAsReviewer(r *http.Request, method, path string, body json.RawMessage) error {
	if h.mux == nil {
		return errors.New("the router is not mounted")
	}
	req, err := http.NewRequestWithContext(r.Context(), method, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header = r.Header.Clone()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Del("Content-Length")
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code < 300 {
		return nil
	}
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &e) == nil && e.Error.Message != "" {
		return errors.New(e.Error.Message)
	}
	return fmt.Errorf("%s %s answered %d", method, path, rec.Code)
}

func wrapJSON(key string, v json.RawMessage) json.RawMessage {
	b, _ := json.Marshal(map[string]json.RawMessage{key: v})
	return b
}

// integrationName reads an integration the caller can see.
func (h *Handlers) integrationName(r *http.Request, id uuid.UUID) (string, error) {
	integ, err := h.Integrations.Get(r.Context(), middleware.OrgID(r), id)
	if err != nil || !h.canSeeIntegration(r, id) {
		return "", errors.New("integration not found")
	}
	return integ.Integration.Name, nil
}

// checkIntegrationSetting refuses a value the setting's route would
// refuse for its shape, so it is caught when filed rather than when
// approved. The route validates again on apply.
func checkIntegrationSetting(c proposals.Change) error {
	switch c.Field {
	case "matchers":
		var v []matcherInput
		if err := json.Unmarshal(c.After, &v); err != nil {
			return fmt.Errorf("matchers: %w", err)
		}
		hasService := false
		for _, m := range v {
			if strings.TrimSpace(m.Value) == "" && m.Operator != integrations.OperatorExists && m.Operator != integrations.OperatorNotExists {
				return errors.New("matchers: every matcher needs a value")
			}
			hasService = hasService || m.Attribute == "" || m.Attribute == integrations.ServiceNameAttribute
		}
		if !hasService {
			return errors.New("matchers: an integration needs at least one service.name matcher")
		}
	case "notify_unacknowledged_errors":
		var v bool
		if err := json.Unmarshal(c.After, &v); err != nil {
			return errors.New("notify_unacknowledged_errors: true or false")
		}
	case "declared_environments":
		var v []string
		if err := json.Unmarshal(c.After, &v); err != nil {
			return errors.New("declared_environments: a list of environment names")
		}
	case "sources":
		var v []integrations.Source
		if err := json.Unmarshal(c.After, &v); err != nil {
			return fmt.Errorf("sources: %w", err)
		}
		for _, s := range v {
			if err := s.Validate(); err != nil {
				return fmt.Errorf("sources: %w", err)
			}
		}
	}
	return nil
}

// matchersAsInput is an integration's matchers in the shape
// PUT /matchers takes, so before and after compare like for like.
func matchersAsInput(ms []integrations.Matcher) []matcherInput {
	out := make([]matcherInput, 0, len(ms))
	for _, m := range ms {
		out = append(out, matcherInput{
			Attribute: m.Attribute, Operator: m.Operator, Value: m.Value,
			MatchGroup: m.MatchGroup, IncludeDescendants: m.IncludeDescendants, Member: m.Member,
		})
	}
	return out
}

// sourceInput is a source as PUT /sources takes it.
type sourceInput struct {
	Signal      string                         `json:"signal"`
	OriginKind  string                         `json:"origin_kind"`
	Origin      string                         `json:"origin"`
	Conditions  []integrations.SourceCondition `json:"conditions"`
	MetricNames []string                       `json:"metric_names"`
}

func sourcesAsInput(ss []integrations.Source) []sourceInput {
	out := make([]sourceInput, 0, len(ss))
	for _, s := range ss {
		conds := s.Conditions
		if conds == nil {
			conds = []integrations.SourceCondition{}
		}
		names := s.MetricNames
		if names == nil {
			names = []string{}
		}
		out = append(out, sourceInput{Signal: s.Signal, OriginKind: s.OriginKind, Origin: s.Origin, Conditions: conds, MetricNames: names})
	}
	return out
}

// ruleScopeKey identifies what a check is bound to, stably, for the
// dedup key.
func ruleScopeKey(r alerting.AlertRule) string {
	switch {
	case r.SystemID != nil:
		return "system:" + r.SystemID.String()
	case r.IntegrationID != nil:
		return "integration:" + r.IntegrationID.String()
	case r.ServiceName != "":
		return "service:" + r.ServiceName
	}
	return "cell"
}

// ruleScopeName names what a check is bound to, for the inbox.
func (h *Handlers) ruleScopeName(r *http.Request, rule alerting.AlertRule) string {
	switch {
	case rule.SystemID != nil:
		if sys, ok, err := h.Catalog.GetSystem(r.Context(), middleware.OrgID(r), *rule.SystemID); err == nil && ok {
			return sys.Name
		}
		return "system " + rule.SystemID.String()
	case rule.IntegrationID != nil:
		if name, err := h.integrationName(r, *rule.IntegrationID); err == nil {
			return name
		}
		return "integration " + rule.IntegrationID.String()
	case rule.ServiceName != "":
		return rule.ServiceName
	}
	return "the whole cell"
}
