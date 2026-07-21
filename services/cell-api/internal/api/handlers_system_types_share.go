// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shareable system types (docs/system-types-sharing.md): a system type
// — key, label, detection prefixes, starter checks — exports to a
// single portable YAML/JSON document that can live in a gist or a
// GitHub repo, and imports into any other cell. Built-ins export too,
// so "fork the RabbitMQ type and tweak it for our broker" is one
// download away. Import creates an org row (an override when the key
// matches a built-in), never touches the code-defined catalog.

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/monitoringtemplates"
)

// SystemTypeDocFormat is the format marker every shared document
// carries — versioned so a future v2 can evolve the shape without
// breaking files already published in the wild.
const SystemTypeDocFormat = "sluicio/system-type/v1"

// systemTypeDoc is the portable document. It mirrors the stored entity
// with BOTH json and yaml tags so either serialization reads naturally
// (yaml.v3 alone would lowercase Go names instead of snake_case).
type systemTypeDoc struct {
	Format         string     `json:"format" yaml:"format"`
	Key            string     `json:"key" yaml:"key"`
	Label          string     `json:"label" yaml:"label"`
	IsSystem       bool       `json:"is_system" yaml:"is_system"`
	DetectPrefixes []string   `json:"detect_prefixes" yaml:"detect_prefixes"`
	Checks         []docCheck `json:"checks" yaml:"checks"`
}

// docCheck mirrors monitoringtemplates.Check with yaml tags.
type docCheck struct {
	Name           string          `json:"name" yaml:"name"`
	Description    string          `json:"description,omitempty" yaml:"description,omitempty"`
	Signal         string          `json:"signal,omitempty" yaml:"signal,omitempty"`
	Metric         string          `json:"metric,omitempty" yaml:"metric,omitempty"`
	Agg            string          `json:"agg,omitempty" yaml:"agg,omitempty"`
	Op             string          `json:"op,omitempty" yaml:"op,omitempty"`
	Threshold      float64         `json:"threshold,omitempty" yaml:"threshold,omitempty"`
	Attrs          []docAttrFilter `json:"attrs,omitempty" yaml:"attrs,omitempty"`
	SplitBy        string          `json:"split_by,omitempty" yaml:"split_by,omitempty"`
	MinSeverity    int32           `json:"min_severity,omitempty" yaml:"min_severity,omitempty"`
	BodyContains   string          `json:"body_contains,omitempty" yaml:"body_contains,omitempty"`
	LogThreshold   int             `json:"log_threshold,omitempty" yaml:"log_threshold,omitempty"`
	TraceThreshold int             `json:"trace_threshold,omitempty" yaml:"trace_threshold,omitempty"`
	ThresholdMs    int             `json:"threshold_ms,omitempty" yaml:"threshold_ms,omitempty"`
	WindowSeconds  int             `json:"window_seconds,omitempty" yaml:"window_seconds,omitempty"`
	Severity       string          `json:"severity,omitempty" yaml:"severity,omitempty"`
	Unit           string          `json:"unit,omitempty" yaml:"unit,omitempty"`
	Display        bool            `json:"display,omitempty" yaml:"display,omitempty"`
}

type docAttrFilter struct {
	Key   string `json:"key" yaml:"key"`
	Op    string `json:"op" yaml:"op"`
	Value string `json:"value" yaml:"value"`
}

func checkToDoc(c monitoringtemplates.Check) docCheck {
	attrs := make([]docAttrFilter, 0, len(c.Attrs))
	for _, a := range c.Attrs {
		attrs = append(attrs, docAttrFilter{Key: a.Key, Op: a.Op, Value: a.Value})
	}
	return docCheck{
		Name: c.Name, Description: c.Description, Signal: c.Signal,
		Metric: c.Metric, Agg: c.Agg, Op: c.Op, Threshold: c.Threshold,
		Attrs: attrs, SplitBy: c.SplitBy,
		MinSeverity: c.MinSeverity, BodyContains: c.BodyContains, LogThreshold: c.LogThreshold,
		TraceThreshold: c.TraceThreshold, ThresholdMs: c.ThresholdMs, WindowSeconds: c.WindowSeconds,
		Severity: c.Severity, Unit: c.Unit, Display: c.Display,
	}
}

func docToCheck(d docCheck) monitoringtemplates.Check {
	attrs := make([]monitoringtemplates.AttrFilter, 0, len(d.Attrs))
	for _, a := range d.Attrs {
		attrs = append(attrs, monitoringtemplates.AttrFilter{Key: a.Key, Op: a.Op, Value: a.Value})
	}
	return monitoringtemplates.Check{
		Name: d.Name, Description: d.Description, Signal: d.Signal,
		Metric: d.Metric, Agg: d.Agg, Op: d.Op, Threshold: d.Threshold,
		Attrs: attrs, SplitBy: d.SplitBy,
		MinSeverity: d.MinSeverity, BodyContains: d.BodyContains, LogThreshold: d.LogThreshold,
		TraceThreshold: d.TraceThreshold, ThresholdMs: d.ThresholdMs, WindowSeconds: d.WindowSeconds,
		Severity: d.Severity, Unit: d.Unit, Display: d.Display,
	}
}

// ── validation ───────────────────────────────────────────────────────
//
// Import consumes files from the INTERNET (that's the point), so the
// document is validated strictly rather than trusting it like our own
// UI's payloads.

var systemTypeKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

var validCheckSignals = map[string]bool{
	"": true, "metric": true, "log": true,
	"trace_error": true, "trace_latency": true, "trace_volume": true,
}

var validCheckSeverities = map[string]bool{"": true, "info": true, "warning": true, "critical": true}

func validateSystemTypeDoc(d *systemTypeDoc) error {
	if d.Format != SystemTypeDocFormat {
		return fmt.Errorf("unsupported format %q (want %q)", d.Format, SystemTypeDocFormat)
	}
	d.Key = strings.ToLower(strings.TrimSpace(d.Key))
	d.Label = strings.TrimSpace(d.Label)
	if !systemTypeKeyRe.MatchString(d.Key) {
		return fmt.Errorf("invalid key %q (lowercase letters, digits, . _ -; max 63 chars)", d.Key)
	}
	if d.Label == "" || len(d.Label) > 120 {
		return fmt.Errorf("label is required (max 120 chars)")
	}
	if len(d.DetectPrefixes) > 20 {
		return fmt.Errorf("too many detect_prefixes (max 20)")
	}
	for _, p := range d.DetectPrefixes {
		if strings.TrimSpace(p) == "" || len(p) > 200 {
			return fmt.Errorf("invalid detect prefix %q", p)
		}
	}
	if len(d.Checks) > 50 {
		return fmt.Errorf("too many checks (max 50)")
	}
	for i, c := range d.Checks {
		if strings.TrimSpace(c.Name) == "" || len(c.Name) > 200 {
			return fmt.Errorf("check[%d]: name is required (max 200 chars)", i)
		}
		if !validCheckSignals[c.Signal] {
			return fmt.Errorf("check[%d] %q: unknown signal %q", i, c.Name, c.Signal)
		}
		if !validCheckSeverities[strings.ToLower(c.Severity)] {
			return fmt.Errorf("check[%d] %q: unknown severity %q", i, c.Name, c.Severity)
		}
		if (c.Signal == "" || c.Signal == "metric") && strings.TrimSpace(c.Metric) == "" {
			return fmt.Errorf("check[%d] %q: metric checks need a metric name", i, c.Name)
		}
		for _, a := range c.Attrs {
			if strings.TrimSpace(a.Key) == "" {
				return fmt.Errorf("check[%d] %q: attr filter needs a key", i, c.Name)
			}
		}
	}
	return nil
}

// ── handlers ─────────────────────────────────────────────────────────

// exportSystemType: GET /api/v1/system-types/{key}/export?format=yaml|json
//
// Any signed-in member can export (the catalog is org-visible config,
// and sharing is the point). Built-ins export like org types.
func (h *Handlers) exportSystemType(w http.ResponseWriter, r *http.Request) {
	key := strings.ToLower(strings.TrimSpace(r.PathValue("key")))
	merged, err := h.mergedSystemTypes(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("export system type: catalog failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "catalog failed")
		return
	}
	var found *effectiveType
	for i := range merged {
		if merged[i].Template.Kind == key {
			found = &merged[i]
			break
		}
	}
	if found == nil {
		httpserver.WriteError(w, http.StatusNotFound, "system type not found")
		return
	}
	checks := make([]docCheck, 0, len(found.Template.Checks))
	for _, c := range found.Template.Checks {
		checks = append(checks, checkToDoc(systemCheckToCustom(c)))
	}
	doc := systemTypeDoc{
		Format:         SystemTypeDocFormat,
		Key:            found.Template.Kind,
		Label:          found.Template.Label,
		IsSystem:       found.Template.System,
		DetectPrefixes: found.Template.DetectPrefixes,
		Checks:         checks,
	}

	if strings.EqualFold(r.URL.Query().Get("format"), "json") {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", doc.Key+".systemtype.json"))
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(doc)
		return
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "encode failed")
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", doc.Key+".systemtype.yaml"))
	_, _ = w.Write([]byte("# Sluicio system type — import via System types → Import, or share it.\n"))
	_, _ = w.Write(out)
}

// importSystemType: POST /api/v1/system-types/import?replace=true (writer+)
//
// Body is a systemTypeDoc as YAML or JSON (sniffed — a leading '{' is
// JSON; YAML parses both anyway). Creates an org system type; when the
// key already has an ORG row, 409 unless replace=true (matching a
// BUILT-IN key is fine — that's an override, the normal customization
// path).
func (h *Handlers) importSystemType(w http.ResponseWriter, r *http.Request) {
	if h.SystemTypes == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "system types store unavailable")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10)) // 256 KiB is plenty for a type doc
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "read failed")
		return
	}
	var doc systemTypeDoc
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(body, &doc); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	} else if err := yaml.Unmarshal(body, &doc); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid YAML: "+err.Error())
		return
	}
	if err := validateSystemTypeDoc(&doc); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	checks := make([]monitoringtemplates.Check, 0, len(doc.Checks))
	for _, c := range doc.Checks {
		checks = append(checks, docToCheck(c))
	}
	prefixes := cleanPrefixes(doc.DetectPrefixes)
	orgID := middleware.OrgID(r)

	// Existing ORG row with this key → conflict unless replace.
	existing, err := h.SystemTypes.List(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("import system type: list failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "import failed")
		return
	}
	for _, st := range existing {
		if st.Key == doc.Key {
			if r.URL.Query().Get("replace") != "true" {
				httpserver.WriteError(w, http.StatusConflict, fmt.Sprintf("system type %q already exists — re-import with replace=true to overwrite", doc.Key))
				return
			}
			updated, ok, uerr := h.SystemTypes.Update(r.Context(), orgID, st.ID, doc.Label, doc.IsSystem, prefixes, checks)
			if uerr != nil || !ok {
				h.Logger.Error("import system type: replace failed", "err", uerr)
				httpserver.WriteError(w, http.StatusInternalServerError, "import failed")
				return
			}
			h.recordAudit(r, "system_type.imported", "system_type", updated.ID.String(), map[string]any{"key": updated.Key, "replaced": true, "checks": len(checks)})
			httpserver.WriteJSON(w, http.StatusOK, effectiveToDTO(effectiveType{Template: systemTypeToTemplate(updated), ID: updated.ID}))
			return
		}
	}

	st, err := h.SystemTypes.Create(r.Context(), orgID, doc.Key, doc.Label, doc.IsSystem, prefixes, checks)
	if err != nil {
		if isUniqueViolation(err) {
			httpserver.WriteError(w, http.StatusConflict, fmt.Sprintf("system type %q already exists — re-import with replace=true to overwrite", doc.Key))
			return
		}
		h.Logger.Error("import system type failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "import failed")
		return
	}
	h.recordAudit(r, "system_type.imported", "system_type", st.ID.String(), map[string]any{"key": st.Key, "replaced": false, "checks": len(checks)})
	httpserver.WriteJSON(w, http.StatusCreated, effectiveToDTO(effectiveType{Template: systemTypeToTemplate(st), ID: st.ID}))
}
