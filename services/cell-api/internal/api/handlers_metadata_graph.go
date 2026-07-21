// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The metadata relationship graph: integrations linked to their metadata
// values + tags, so you can explore ownership/dependency by attribute rather
// than by raw service topology ("which integrations is Robert responsible for",
// "which are P1", "which use file-integration"). A different lens over data we
// already store — no new telemetry. Visibility-scoped per access policy.
// See docs/future-features.md (the v1.1 idea) — shipped here.

package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
)

type metaGraphNode struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"` // "integration" | "value" | "tag"
	Label         string `json:"label"`
	IntegrationID string `json:"integration_id,omitempty"`
	Field         string `json:"field,omitempty"`       // field key (value nodes)
	FieldLabel    string `json:"field_label,omitempty"` // field display label
	Value         string `json:"value,omitempty"`
}

type metaGraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type metaFieldRef struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// metadataGraph: GET /api/v1/metadata-graph
func (h *Handlers) metadataGraph(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgID(r)

	rows, err := h.Integrations.List(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("metadata-graph: list integrations failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	fields, fErr := h.Metadata.ListFields(r.Context(), orgID)
	if fErr != nil {
		h.Logger.Warn("metadata-graph: list fields failed", "err", fErr)
	}
	integFields := scopedFields(fields, true)
	keyByFieldID := make(map[uuid.UUID]string, len(integFields))
	labelByKey := make(map[string]string, len(integFields))
	for _, f := range integFields {
		keyByFieldID[f.ID] = f.Key
		labelByKey[f.Key] = f.Label
	}

	integIDs := make([]uuid.UUID, 0, len(rows))
	for _, ig := range rows {
		integIDs = append(integIDs, ig.ID)
	}
	bulkValues, vErr := h.Metadata.IntegrationValuesBulk(r.Context(), integIDs)
	if vErr != nil {
		h.Logger.Warn("metadata-graph: bulk values failed", "err", vErr)
		bulkValues = map[uuid.UUID]map[uuid.UUID]string{}
	}
	tagsByIntegration, tErr := h.Tags.ListForIntegrations(r.Context(), orgID, integIDs)
	if tErr != nil {
		h.Logger.Warn("metadata-graph: tags failed", "err", tErr)
	}
	membersByIntegration, _ := h.Catalog.IntegrationServicesBulk(r.Context(), orgID)
	canSee, seesAll, csErr := h.visibleServiceChecker(r)
	if csErr != nil {
		h.Logger.Error("metadata-graph: visibility resolve failed", "err", csErr)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	nodes := make([]metaGraphNode, 0)
	edges := make([]metaGraphEdge, 0)
	seen := make(map[string]bool)
	addNode := func(n metaGraphNode) {
		if !seen[n.ID] {
			seen[n.ID] = true
			nodes = append(nodes, n)
		}
	}

	for _, ig := range rows {
		// Visibility: non-admins only see integrations with a member they may
		// see; admins/wildcard see all (incl. member-less integrations).
		if !seesAll {
			ok := false
			for _, m := range membersByIntegration[ig.ID] {
				if canSee(m) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		intID := "int:" + ig.ID.String()
		addNode(metaGraphNode{ID: intID, Kind: "integration", Label: ig.Name, IntegrationID: ig.ID.String()})

		for fid, v := range bulkValues[ig.ID] {
			key, ok := keyByFieldID[fid]
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			// \x1f (unit separator) keeps "field=value" unambiguous if a value
			// contains '='.
			valID := "val:" + key + "\x1f" + v
			addNode(metaGraphNode{ID: valID, Kind: "value", Label: v, Field: key, FieldLabel: labelByKey[key], Value: v})
			edges = append(edges, metaGraphEdge{Source: intID, Target: valID})
		}

		for _, t := range tagsByIntegration[ig.ID] {
			tagID := "tag:" + t.Slug
			addNode(metaGraphNode{ID: tagID, Kind: "tag", Label: t.Name})
			edges = append(edges, metaGraphEdge{Source: intID, Target: tagID})
		}
	}

	fieldRefs := make([]metaFieldRef, 0, len(integFields))
	for _, f := range integFields {
		fieldRefs = append(fieldRefs, metaFieldRef{Key: f.Key, Label: f.Label})
	}

	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"nodes":  nodes,
		"edges":  edges,
		"fields": fieldRefs,
	})
}
