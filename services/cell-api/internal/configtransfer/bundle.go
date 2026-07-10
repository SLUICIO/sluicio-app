// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package configtransfer implements portable org-configuration bundles:
// export an org's product configuration as one JSON document, import it
// into any other org/cell. Design: docs/config-transfer-design.md.
//
// The two invariants everything here serves:
//
//  1. PORTABILITY — bundles carry natural keys (slugs/names), never
//     UUIDs. Import mints fresh ids and resolves references through
//     the natural keys, so a bundle survives landing in a different
//     environment.
//  2. ATOMICITY — the caller wraps Import in ONE transaction. Any
//     error aborts the whole thing; a failed import never happened.
//     Dry-run is the same code path with an unconditional rollback.
//
// Secrets never enter a bundle: notification-channel credentials are
// redacted on export (the import report flags those channels as
// needs_credentials), and secret-bearing domains (ingest keys, SSO,
// tokens) are out of scope entirely.
package configtransfer

import (
	"encoding/json"
	"time"
)

// FormatVersion gates importability: an importer only accepts bundles
// whose format matches. Bump only when the bundle SHAPE changes.
const FormatVersion = 1

// Bundle is the export envelope.
type Bundle struct {
	FormatVersion int       `json:"format_version"`
	AppVersion    string    `json:"app_version"`
	ExportedAt    time.Time `json:"exported_at"`
	Source        Source    `json:"source"`
	Sections      Sections  `json:"sections"`
}

type Source struct {
	OrgSlug string `json:"org_slug"`
	OrgName string `json:"org_name"`
}

// Sections holds every exported domain, in no particular order — the
// importer applies them in dependency order regardless.
type Sections struct {
	Tags            []Tag             `json:"tags,omitempty"`
	MetadataFields  []MetadataField   `json:"metadata_fields,omitempty"`
	Schemas         []Schema          `json:"schemas,omitempty"`
	Maps            []MapDef          `json:"maps,omitempty"`
	SystemTypes     []SystemType      `json:"system_types,omitempty"`
	Systems         []System          `json:"systems,omitempty"`
	ServiceFacets   []ServiceFacet    `json:"service_facets,omitempty"`
	FacetMappings   []FacetMapping    `json:"facet_mappings,omitempty"`
	FacetOverrides  []FacetOverride   `json:"facet_overrides,omitempty"`
	Groups          []Group           `json:"groups,omitempty"`
	Integrations    []Integration     `json:"integrations,omitempty"`
	AccessPolicies  []AccessPolicy    `json:"access_policies,omitempty"`
	ServiceMetadata []ServiceMetadata `json:"service_metadata,omitempty"`
	ServiceTags     []ServiceTag      `json:"service_tags,omitempty"`
	ServiceSchemas  []ServiceSchema   `json:"service_schemas,omitempty"`
	MessageViews    []MessageView     `json:"message_views,omitempty"`
	Templates       []Template        `json:"monitoring_templates,omitempty"`
	Channels        []Channel         `json:"notification_channels,omitempty"`
	Profiles        []Profile         `json:"notification_profiles,omitempty"`
	AlertRules      []AlertRule       `json:"alert_rules,omitempty"`
	Dashboards      []Dashboard       `json:"dashboards,omitempty"`
	CellSettings    []CellSetting     `json:"cell_settings,omitempty"`
}

// ── section shapes (natural-key references only) ─────────────────────

type Tag struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type MetadataField struct {
	Key                  string          `json:"key"`
	Label                string          `json:"label"`
	Type                 string          `json:"type"`
	Options              json.RawMessage `json:"options,omitempty"`
	Description          string          `json:"description,omitempty"`
	AppliesToIntegration bool            `json:"applies_to_integration"`
	AppliesToService     bool            `json:"applies_to_service"`
	AppliesToSystem      bool            `json:"applies_to_system"`
	Required             bool            `json:"required"`
	SystemTypeKey        *string         `json:"system_type_key,omitempty"`
}

type Schema struct {
	Name        string `json:"name"` // natural key: name + version
	Version     string `json:"version,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Description string `json:"description,omitempty"`
	Format      string `json:"format,omitempty"`
	Content     string `json:"content,omitempty"`
}

type MapDef struct {
	Name        string  `json:"name"` // natural key: name + version
	Version     string  `json:"version,omitempty"`
	Description string  `json:"description,omitempty"`
	Format      string  `json:"format,omitempty"`
	Content     string  `json:"content,omitempty"`
	FromSchema  *string `json:"from_schema,omitempty"` // "name@version"
	ToSchema    *string `json:"to_schema,omitempty"`
}

type SystemType struct {
	Key            string          `json:"key"`
	Label          string          `json:"label"`
	IsSystem       bool            `json:"is_system"`
	DetectPrefixes json.RawMessage `json:"detect_prefixes,omitempty"`
	Checks         json.RawMessage `json:"checks,omitempty"`
}

type System struct {
	Name        string `json:"name"`
	TypeKey     string `json:"type_key"`
	Description string `json:"description,omitempty"`
	BadgePublic bool   `json:"badge_public"`
	// Metadata values keyed by metadata-field key.
	Metadata map[string]string `json:"metadata,omitempty"`
}

type ServiceFacet struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type FacetMapping struct {
	ServiceName     string `json:"service_name"`
	AttributeSource string `json:"attribute_source"`
	AttributeKey    string `json:"attribute_key"`
	MatchOperator   string `json:"match_operator"`
	MatchValue      string `json:"match_value"`
	SetIOKind       string `json:"set_io_kind,omitempty"`
	SetIORole       string `json:"set_io_role,omitempty"`
}

type FacetOverride struct {
	ServiceName string `json:"service_name"`
	FacetSlug   string `json:"facet_slug"`
	Action      string `json:"action"`
}

type Group struct {
	Slug        string        `json:"slug"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Members     []GroupMember `json:"members,omitempty"` // imported only with the email-match opt-in
}

type GroupMember struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type Integration struct {
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	BadgePublic bool              `json:"badge_public"`
	Profile     *string           `json:"notification_profile,omitempty"` // profile name
	Matchers    []Matcher         `json:"matchers,omitempty"`
	Tags        []string          `json:"tags,omitempty"`     // tag slugs
	Metadata    map[string]string `json:"metadata,omitempty"` // field key → value
}

type Matcher struct {
	Attribute  string `json:"attribute"`
	Operator   string `json:"operator"`
	Value      string `json:"value"`
	MatchGroup int16  `json:"match_group"`
}

type AccessPolicy struct {
	Group             string          `json:"group"` // group slug
	Kind              string          `json:"kind"`
	TargetServiceName *string         `json:"target_service_name,omitempty"`
	TargetIntegration *string         `json:"target_integration,omitempty"` // slug
	TargetSystem      *string         `json:"target_system,omitempty"`      // system name
	TargetSystemKind  *string         `json:"target_system_kind,omitempty"`
	AttributeMatch    json.RawMessage `json:"attribute_match,omitempty"`
	Conditions        json.RawMessage `json:"conditions,omitempty"`
	Signals           []string        `json:"signals,omitempty"`
}

type ServiceMetadata struct {
	ServiceName string            `json:"service_name"`
	Description string            `json:"description,omitempty"`
	Owner       string            `json:"owner,omitempty"`
	OnCall      string            `json:"on_call,omitempty"`
	Team        string            `json:"team,omitempty"`
	Repository  string            `json:"repository,omitempty"`
	RunbookURL  string            `json:"runbook_url,omitempty"`
	Extras      map[string]string `json:"extras,omitempty"` // field key → value
}

type ServiceTag struct {
	ServiceName string `json:"service_name"`
	Tag         string `json:"tag"` // slug
}

type ServiceSchema struct {
	ServiceName string `json:"service_name"`
	Direction   string `json:"direction"`
	Schema      string `json:"schema"` // "name@version"
}

type MessageView struct {
	Name             string          `json:"name"` // shared views only
	Description      string          `json:"description,omitempty"`
	Pinned           bool            `json:"pinned"`
	Filters          json.RawMessage `json:"filters,omitempty"`
	ScopeIntegration *string         `json:"scope_integration,omitempty"` // slug
	ScopeServiceID   *string         `json:"scope_service_id,omitempty"`
}

type Template struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Source      string          `json:"source,omitempty"`
	Checks      json.RawMessage `json:"checks,omitempty"`
}

type Channel struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Config with credential fields redacted. Channels whose config
	// was redacted arrive needing credentials in the target env.
	Config           json.RawMessage `json:"config,omitempty"`
	NeedsCredentials bool            `json:"needs_credentials,omitempty"`
}

type Profile struct {
	Name            string   `json:"name"`
	Group           *string  `json:"group,omitempty"` // slug
	Grouping        string   `json:"grouping,omitempty"`
	RenotifyMinutes int      `json:"renotify_minutes"`
	IsDefault       bool     `json:"is_default"`
	Channels        []string `json:"channels,omitempty"` // channel names
}

type AlertRule struct {
	Name               string          `json:"name"`
	Description        string          `json:"description,omitempty"`
	Signal             string          `json:"signal"`
	RuleSpec           json.RawMessage `json:"rule_spec,omitempty"`
	Severity           string          `json:"severity"`
	EvaluationInterval string          `json:"evaluation_interval,omitempty"`
	Enabled            bool            `json:"enabled"`
	ServiceName        *string         `json:"service_name,omitempty"`
	Integration        *string         `json:"integration,omitempty"` // slug
	Group              *string         `json:"group,omitempty"`       // slug
	TitleTemplate      string          `json:"title_template,omitempty"`
	BodyTemplate       string          `json:"body_template,omitempty"`
	Source             string          `json:"source,omitempty"`
	DisplayOnService   bool            `json:"display_on_service"`
	Unit               string          `json:"unit,omitempty"`
	ResolveMode        string          `json:"resolve_mode,omitempty"`
	NotificationConfig json.RawMessage `json:"notification_config,omitempty"`
	Channels           []string        `json:"channels,omitempty"` // channel names (alert_rule_routes)
}

type Dashboard struct {
	Name              string          `json:"name"`
	IsDefault         bool            `json:"is_default"`
	AutoIncludeAll    bool            `json:"auto_include_all"`
	DefaultWidgetType string          `json:"default_widget_type,omitempty"`
	Position          int             `json:"position"`
	Group             *string         `json:"group,omitempty"` // slug
	Items             []DashboardItem `json:"items,omitempty"`
}

type DashboardItem struct {
	WidgetType  string  `json:"widget_type"`
	Position    int     `json:"position"`
	EntityKind  string  `json:"entity_kind,omitempty"`
	Integration *string `json:"integration,omitempty"` // slug
	SystemName  *string `json:"system_name,omitempty"`
}

// CellSetting rows are cell-wide (retention, alert email template,
// environment label). They import only when the caller is a cell
// operator; otherwise the section is skipped with a report warning.
type CellSetting struct {
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value"`
	Description string          `json:"description,omitempty"`
}

// ── import options + report ──────────────────────────────────────────

// Mode controls collision behavior. Neither mode ever deletes.
type Mode string

const (
	// ModeStrict fails (→ rollback) on any natural-key collision.
	ModeStrict Mode = "strict"
	// ModeReplace upserts collisions; the bundle wins, extras stay.
	ModeReplace Mode = "replace"
)

type Options struct {
	Mode Mode
	// MatchMembersByEmail attaches exported group members to target
	// users with the same email (only when they're already members of
	// the target org). Off by default — people belong to environments.
	MatchMembersByEmail bool
	// ActorUserID owns imported owner-scoped rows (dashboards, views).
	ActorUserID string
	// IsOperator gates the cell_settings section.
	IsOperator bool
}

// Report is what a dry-run (and a real import) returns.
type Report struct {
	Mode     Mode                     `json:"mode"`
	DryRun   bool                     `json:"dry_run"`
	Sections map[string]SectionReport `json:"sections"`
	// NeedsCredentials lists channels imported without secrets.
	NeedsCredentials []string `json:"needs_credentials,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

type SectionReport struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
}

func (r *Report) section(name string) *SectionReport {
	if r.Sections == nil {
		r.Sections = map[string]SectionReport{}
	}
	s := r.Sections[name]
	return &s
}

func (r *Report) save(name string, s *SectionReport) { r.Sections[name] = *s }

func (r *Report) warnf(format string, args ...any) {
	r.Warnings = append(r.Warnings, sprintf(format, args...))
}
