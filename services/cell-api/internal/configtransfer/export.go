// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package configtransfer

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// Export reads one org's configuration into a Bundle. Run it inside a
// REPEATABLE READ read-only transaction so the bundle is a consistent
// point-in-time snapshot. Secrets are redacted here (see channels).
func Export(ctx context.Context, tx pgx.Tx, orgID string, appVersion string) (*Bundle, error) {
	b := &Bundle{
		FormatVersion: FormatVersion,
		AppVersion:    appVersion,
		ExportedAt:    time.Now().UTC(),
	}
	if err := tx.QueryRow(ctx,
		`SELECT slug, name FROM orgs WHERE id = $1`, orgID).
		Scan(&b.Source.OrgSlug, &b.Source.OrgName); err != nil {
		return nil, fmt.Errorf("configtransfer: org lookup: %w", err)
	}

	// Natural-key lookup maps (uuid → key), built as sections export.
	tagSlug := map[string]string{}
	fieldKey := map[string]string{}
	schemaKey := map[string]string{}
	groupSlug := map[string]string{}
	integSlug := map[string]string{}
	systemName := map[string]string{}
	channelName := map[string]string{}
	profileName := map[string]string{}

	type step struct {
		name string
		fn   func() error
	}
	steps := []step{
		{"tags", func() error { return exportTags(ctx, tx, orgID, b, tagSlug) }},
		{"metadata_fields", func() error { return exportFields(ctx, tx, orgID, b, fieldKey) }},
		{"schemas", func() error { return exportSchemas(ctx, tx, orgID, b, schemaKey) }},
		{"maps", func() error { return exportMaps(ctx, tx, orgID, b, schemaKey) }},
		{"system_types", func() error { return exportSystemTypes(ctx, tx, orgID, b) }},
		{"systems", func() error { return exportSystems(ctx, tx, orgID, b, fieldKey, systemName) }},
		{"service_facets", func() error { return exportFacets(ctx, tx, orgID, b) }},
		{"groups", func() error { return exportGroups(ctx, tx, orgID, b, groupSlug) }},
		{"channels", func() error { return exportChannels(ctx, tx, orgID, b, channelName) }},
		{"profiles", func() error { return exportProfiles(ctx, tx, orgID, b, groupSlug, channelName, profileName) }},
		{"integrations", func() error { return exportIntegrations(ctx, tx, orgID, b, tagSlug, fieldKey, profileName, integSlug) }},
		{"access_policies", func() error { return exportPolicies(ctx, tx, orgID, b, groupSlug, integSlug, systemName) }},
		{"service_config", func() error { return exportServiceConfig(ctx, tx, orgID, b, tagSlug, fieldKey, schemaKey) }},
		{"message_views", func() error { return exportMessageViews(ctx, tx, orgID, b, integSlug) }},
		{"templates", func() error { return exportTemplates(ctx, tx, orgID, b) }},
		{"alert_rules", func() error { return exportAlertRules(ctx, tx, orgID, b, integSlug, groupSlug, channelName) }},
		{"dashboards", func() error { return exportDashboards(ctx, tx, orgID, b, groupSlug, integSlug) }},
		{"cell_settings", func() error { return exportCellSettings(ctx, tx, b) }},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			return nil, fmt.Errorf("configtransfer: export %s: %w", s.name, err)
		}
	}
	return b, nil
}

func exportTags(ctx context.Context, tx pgx.Tx, org string, b *Bundle, ids map[string]string) error {
	rows, err := tx.Query(ctx, `SELECT id, slug, name, COALESCE(color,'') FROM tags WHERE organization_id=$1 ORDER BY slug`, org)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var t Tag
		if err := rows.Scan(&id, &t.Slug, &t.Name, &t.Color); err != nil {
			return err
		}
		ids[id] = t.Slug
		b.Sections.Tags = append(b.Sections.Tags, t)
	}
	return rows.Err()
}

func exportFields(ctx context.Context, tx pgx.Tx, org string, b *Bundle, ids map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT id, key, label, type, COALESCE(options,'null'::jsonb)::text, COALESCE(description,''),
		       applies_to_integration, applies_to_service, applies_to_system, required, system_type_key
		FROM metadata_fields WHERE organization_id=$1 ORDER BY key`, org)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, options string
		var f MetadataField
		if err := rows.Scan(&id, &f.Key, &f.Label, &f.Type, &options, &f.Description,
			&f.AppliesToIntegration, &f.AppliesToService, &f.AppliesToSystem, &f.Required, &f.SystemTypeKey); err != nil {
			return err
		}
		if options != "null" {
			f.Options = json.RawMessage(options)
		}
		ids[id] = f.Key
		b.Sections.MetadataFields = append(b.Sections.MetadataFields, f)
	}
	return rows.Err()
}

func schemaNK(name, version string) string {
	if version == "" {
		return name
	}
	return name + "@" + version
}

func exportSchemas(ctx context.Context, tx pgx.Tx, org string, b *Bundle, ids map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT id, name, COALESCE(version,''), COALESCE(kind,''), COALESCE(description,''),
		       COALESCE(format,''), COALESCE(content,'')
		FROM schemas WHERE organization_id=$1 ORDER BY name, version`, org)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var s Schema
		if err := rows.Scan(&id, &s.Name, &s.Version, &s.Kind, &s.Description, &s.Format, &s.Content); err != nil {
			return err
		}
		ids[id] = schemaNK(s.Name, s.Version)
		b.Sections.Schemas = append(b.Sections.Schemas, s)
	}
	return rows.Err()
}

func exportMaps(ctx context.Context, tx pgx.Tx, org string, b *Bundle, schemaKey map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT name, COALESCE(version,''), COALESCE(description,''), COALESCE(format,''),
		       COALESCE(content,''), from_schema_id, to_schema_id
		FROM maps WHERE organization_id=$1 ORDER BY name, version`, org)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m MapDef
		var from, to *string
		if err := rows.Scan(&m.Name, &m.Version, &m.Description, &m.Format, &m.Content, &from, &to); err != nil {
			return err
		}
		if from != nil {
			if k, ok := schemaKey[*from]; ok {
				m.FromSchema = &k
			}
		}
		if to != nil {
			if k, ok := schemaKey[*to]; ok {
				m.ToSchema = &k
			}
		}
		b.Sections.Maps = append(b.Sections.Maps, m)
	}
	return rows.Err()
}

func exportSystemTypes(ctx context.Context, tx pgx.Tx, org string, b *Bundle) error {
	rows, err := tx.Query(ctx, `
		SELECT key, label, is_system, COALESCE(detect_prefixes,'null'::jsonb)::text, COALESCE(checks,'null'::jsonb)::text
		FROM system_types WHERE org_id=$1 ORDER BY key`, org)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var t SystemType
		var prefixes, checks string
		if err := rows.Scan(&t.Key, &t.Label, &t.IsSystem, &prefixes, &checks); err != nil {
			return err
		}
		if prefixes != "null" {
			t.DetectPrefixes = json.RawMessage(prefixes)
		}
		if checks != "null" {
			t.Checks = json.RawMessage(checks)
		}
		b.Sections.SystemTypes = append(b.Sections.SystemTypes, t)
	}
	return rows.Err()
}

func exportSystems(ctx context.Context, tx pgx.Tx, org string, b *Bundle, fieldKey, ids map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT id, name, type_key, COALESCE(description,''), badge_public
		FROM systems WHERE org_id=$1 ORDER BY name`, org)
	if err != nil {
		return err
	}
	defer rows.Close()
	var out []System
	sysIDs := []string{}
	for rows.Next() {
		var id string
		var s System
		if err := rows.Scan(&id, &s.Name, &s.TypeKey, &s.Description, &s.BadgePublic); err != nil {
			return err
		}
		ids[id] = s.Name
		sysIDs = append(sysIDs, id)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// System metadata values, keyed by field key.
	for i, id := range sysIDs {
		mrows, err := tx.Query(ctx, `SELECT field_id, value FROM system_metadata WHERE system_id=$1`, id)
		if err != nil {
			return err
		}
		for mrows.Next() {
			var fid, val string
			if err := mrows.Scan(&fid, &val); err != nil {
				mrows.Close()
				return err
			}
			if k, ok := fieldKey[fid]; ok {
				if out[i].Metadata == nil {
					out[i].Metadata = map[string]string{}
				}
				out[i].Metadata[k] = val
			}
		}
		mrows.Close()
		if err := mrows.Err(); err != nil {
			return err
		}
	}
	b.Sections.Systems = out
	return nil
}

func exportFacets(ctx context.Context, tx pgx.Tx, org string, b *Bundle) error {
	rows, err := tx.Query(ctx, `SELECT slug, name, COALESCE(description,'') FROM service_facets WHERE org_id=$1 ORDER BY slug`, org)
	if err != nil {
		return err
	}
	for rows.Next() {
		var f ServiceFacet
		if err := rows.Scan(&f.Slug, &f.Name, &f.Description); err != nil {
			rows.Close()
			return err
		}
		b.Sections.ServiceFacets = append(b.Sections.ServiceFacets, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	mrows, err := tx.Query(ctx, `
		SELECT service_name, attribute_source::text, attribute_key, match_operator::text, match_value,
		       COALESCE(set_io_kind,''), COALESCE(set_io_role,'')
		FROM service_facet_mappings WHERE organization_id=$1 ORDER BY service_name, attribute_key, match_value`, org)
	if err != nil {
		return err
	}
	for mrows.Next() {
		var m FacetMapping
		if err := mrows.Scan(&m.ServiceName, &m.AttributeSource, &m.AttributeKey, &m.MatchOperator, &m.MatchValue, &m.SetIOKind, &m.SetIORole); err != nil {
			mrows.Close()
			return err
		}
		b.Sections.FacetMappings = append(b.Sections.FacetMappings, m)
	}
	mrows.Close()
	if err := mrows.Err(); err != nil {
		return err
	}

	orows, err := tx.Query(ctx, `
		SELECT service_name, facet_slug, action::text
		FROM service_facet_overrides WHERE organization_id=$1 ORDER BY service_name, facet_slug`, org)
	if err != nil {
		return err
	}
	defer orows.Close()
	for orows.Next() {
		var o FacetOverride
		if err := orows.Scan(&o.ServiceName, &o.FacetSlug, &o.Action); err != nil {
			return err
		}
		b.Sections.FacetOverrides = append(b.Sections.FacetOverrides, o)
	}
	return orows.Err()
}

func exportGroups(ctx context.Context, tx pgx.Tx, org string, b *Bundle, ids map[string]string) error {
	rows, err := tx.Query(ctx, `SELECT id, slug, name, COALESCE(description,'') FROM groups WHERE org_id=$1 ORDER BY slug`, org)
	if err != nil {
		return err
	}
	var gids []string
	for rows.Next() {
		var id string
		var g Group
		if err := rows.Scan(&id, &g.Slug, &g.Name, &g.Description); err != nil {
			rows.Close()
			return err
		}
		ids[id] = g.Slug
		gids = append(gids, id)
		b.Sections.Groups = append(b.Sections.Groups, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i, gid := range gids {
		mrows, err := tx.Query(ctx, `
			SELECT u.email, gm.role FROM group_members gm JOIN users u ON u.id = gm.user_id
			WHERE gm.group_id = $1 ORDER BY u.email`, gid)
		if err != nil {
			return err
		}
		for mrows.Next() {
			var m GroupMember
			if err := mrows.Scan(&m.Email, &m.Role); err != nil {
				mrows.Close()
				return err
			}
			b.Sections.Groups[i].Members = append(b.Sections.Groups[i].Members, m)
		}
		mrows.Close()
		if err := mrows.Err(); err != nil {
			return err
		}
	}
	return nil
}

// channelSecretSafe lists config keys that may travel per channel kind.
// Anything not listed is credential material and is redacted; a channel
// that loses keys is flagged needs_credentials.
var channelSecretSafe = map[string]map[string]bool{
	"email": {"to": true, "addresses": true, "cc": true},
}

func exportChannels(ctx context.Context, tx pgx.Tx, org string, b *Bundle, ids map[string]string) error {
	rows, err := tx.Query(ctx, `SELECT id, name, kind, COALESCE(config,'{}'::jsonb)::text FROM notification_channels WHERE organization_id=$1 ORDER BY name`, org)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, cfg string
		var c Channel
		if err := rows.Scan(&id, &c.Name, &c.Kind, &cfg); err != nil {
			return err
		}
		ids[id] = c.Name
		var full map[string]json.RawMessage
		if err := json.Unmarshal([]byte(cfg), &full); err != nil {
			full = map[string]json.RawMessage{}
		}
		safe := map[string]json.RawMessage{}
		allow := channelSecretSafe[c.Kind]
		for k, v := range full {
			if allow[k] {
				safe[k] = v
			}
		}
		if len(safe) != len(full) {
			c.NeedsCredentials = true
		}
		redacted, _ := json.Marshal(safe)
		c.Config = redacted
		b.Sections.Channels = append(b.Sections.Channels, c)
	}
	return rows.Err()
}

func exportProfiles(ctx context.Context, tx pgx.Tx, org string, b *Bundle, groupSlug, channelName, ids map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT id, name, group_id, COALESCE(grouping,''), renotify_minutes, is_default
		FROM notification_profiles WHERE organization_id=$1 ORDER BY name`, org)
	if err != nil {
		return err
	}
	var pids []string
	for rows.Next() {
		var id string
		var gid *string
		var p Profile
		if err := rows.Scan(&id, &p.Name, &gid, &p.Grouping, &p.RenotifyMinutes, &p.IsDefault); err != nil {
			rows.Close()
			return err
		}
		if gid != nil {
			if s, ok := groupSlug[*gid]; ok {
				p.Group = &s
			}
		}
		ids[id] = p.Name
		pids = append(pids, id)
		b.Sections.Profiles = append(b.Sections.Profiles, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i, pid := range pids {
		crows, err := tx.Query(ctx, `SELECT channel_id FROM notification_profile_channels WHERE profile_id=$1`, pid)
		if err != nil {
			return err
		}
		for crows.Next() {
			var cid string
			if err := crows.Scan(&cid); err != nil {
				crows.Close()
				return err
			}
			if n, ok := channelName[cid]; ok {
				b.Sections.Profiles[i].Channels = append(b.Sections.Profiles[i].Channels, n)
			}
		}
		crows.Close()
		if err := crows.Err(); err != nil {
			return err
		}
		sort.Strings(b.Sections.Profiles[i].Channels)
	}
	return nil
}

func exportIntegrations(ctx context.Context, tx pgx.Tx, org string, b *Bundle, tagSlug, fieldKey, profileName, ids map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT id, slug, name, COALESCE(description,''), badge_public, notification_profile_id
		FROM integrations WHERE organization_id=$1 ORDER BY slug`, org)
	if err != nil {
		return err
	}
	var iids []string
	for rows.Next() {
		var id string
		var pid *string
		var in Integration
		if err := rows.Scan(&id, &in.Slug, &in.Name, &in.Description, &in.BadgePublic, &pid); err != nil {
			rows.Close()
			return err
		}
		if pid != nil {
			if n, ok := profileName[*pid]; ok {
				in.Profile = &n
			}
		}
		ids[id] = in.Slug
		iids = append(iids, id)
		b.Sections.Integrations = append(b.Sections.Integrations, in)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i, iid := range iids {
		mrows, err := tx.Query(ctx, `
			SELECT attribute, operator::text, value, COALESCE(match_group,0)
			FROM integration_matchers WHERE integration_id=$1 ORDER BY match_group, attribute, value`, iid)
		if err != nil {
			return err
		}
		for mrows.Next() {
			var m Matcher
			if err := mrows.Scan(&m.Attribute, &m.Operator, &m.Value, &m.MatchGroup); err != nil {
				mrows.Close()
				return err
			}
			b.Sections.Integrations[i].Matchers = append(b.Sections.Integrations[i].Matchers, m)
		}
		mrows.Close()
		if err := mrows.Err(); err != nil {
			return err
		}
		trows, err := tx.Query(ctx, `SELECT tag_id FROM integration_tags WHERE integration_id=$1`, iid)
		if err != nil {
			return err
		}
		for trows.Next() {
			var tid string
			if err := trows.Scan(&tid); err != nil {
				trows.Close()
				return err
			}
			if s, ok := tagSlug[tid]; ok {
				b.Sections.Integrations[i].Tags = append(b.Sections.Integrations[i].Tags, s)
			}
		}
		trows.Close()
		if err := trows.Err(); err != nil {
			return err
		}
		sort.Strings(b.Sections.Integrations[i].Tags)
		frows, err := tx.Query(ctx, `SELECT field_id, value FROM integration_metadata WHERE integration_id=$1`, iid)
		if err != nil {
			return err
		}
		for frows.Next() {
			var fid, val string
			if err := frows.Scan(&fid, &val); err != nil {
				frows.Close()
				return err
			}
			if k, ok := fieldKey[fid]; ok {
				if b.Sections.Integrations[i].Metadata == nil {
					b.Sections.Integrations[i].Metadata = map[string]string{}
				}
				b.Sections.Integrations[i].Metadata[k] = val
			}
		}
		frows.Close()
		if err := frows.Err(); err != nil {
			return err
		}
	}
	return nil
}

func exportPolicies(ctx context.Context, tx pgx.Tx, org string, b *Bundle, groupSlug, integSlug, systemName map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT p.group_id, p.kind, p.target_service_name, p.target_integration_id,
		       COALESCE(p.attribute_match,'null'::jsonb)::text, p.target_system_kind,
		       COALESCE(p.conditions,'null'::jsonb)::text, p.target_system_id, COALESCE(p.signals,'{}')
		FROM group_access_policies p JOIN groups g ON g.id = p.group_id
		WHERE g.org_id = $1 ORDER BY p.kind`, org)
	if err != nil {
		return err
	}
	defer rows.Close()
	// The store doesn't enforce policy uniqueness (an exact duplicate on
	// a group is semantically harmless — the resolver unions), but a
	// bundle carrying the same natural key twice would SELF-conflict on
	// strict import. Dedupe on the importer's key so every legal org
	// state exports to an importable bundle.
	seen := map[string]struct{}{}
	for rows.Next() {
		var gid string
		var integID, sysID *string
		var attrMatch, conditions string
		var p AccessPolicy
		if err := rows.Scan(&gid, &p.Kind, &p.TargetServiceName, &integID, &attrMatch,
			&p.TargetSystemKind, &conditions, &sysID, &p.Signals); err != nil {
			return err
		}
		p.Group = groupSlug[gid]
		if integID != nil {
			if s, ok := integSlug[*integID]; ok {
				p.TargetIntegration = &s
			}
		}
		if sysID != nil {
			if n, ok := systemName[*sysID]; ok {
				p.TargetSystem = &n
			}
		}
		if attrMatch != "null" {
			p.AttributeMatch = json.RawMessage(attrMatch)
		}
		if conditions != "null" {
			p.Conditions = json.RawMessage(conditions)
		}
		// Mirror of the importer's pseudo natural key (import.go).
		key := strings.Join([]string{p.Group, p.Kind, deref(p.TargetServiceName), deref(p.TargetIntegration),
			deref(p.TargetSystem), deref(p.TargetSystemKind), string(p.AttributeMatch), string(p.Conditions)}, "|")
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		b.Sections.AccessPolicies = append(b.Sections.AccessPolicies, p)
	}
	return rows.Err()
}

func exportServiceConfig(ctx context.Context, tx pgx.Tx, org string, b *Bundle, tagSlug, fieldKey, schemaKey map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT service_name, COALESCE(description,''), COALESCE(owner,''), COALESCE(on_call,''),
		       COALESCE(team,''), COALESCE(repository,''), COALESCE(runbook_url,'')
		FROM service_metadata WHERE organization_id=$1 ORDER BY service_name`, org)
	if err != nil {
		return err
	}
	idx := map[string]int{}
	for rows.Next() {
		var m ServiceMetadata
		if err := rows.Scan(&m.ServiceName, &m.Description, &m.Owner, &m.OnCall, &m.Team, &m.Repository, &m.RunbookURL); err != nil {
			rows.Close()
			return err
		}
		idx[m.ServiceName] = len(b.Sections.ServiceMetadata)
		b.Sections.ServiceMetadata = append(b.Sections.ServiceMetadata, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	erows, err := tx.Query(ctx, `SELECT service_name, field_id, value FROM service_metadata_extras WHERE organization_id=$1`, org)
	if err != nil {
		return err
	}
	for erows.Next() {
		var svc, fid, val string
		if err := erows.Scan(&svc, &fid, &val); err != nil {
			erows.Close()
			return err
		}
		k, ok := fieldKey[fid]
		if !ok {
			continue
		}
		i, ok := idx[svc]
		if !ok {
			i = len(b.Sections.ServiceMetadata)
			idx[svc] = i
			b.Sections.ServiceMetadata = append(b.Sections.ServiceMetadata, ServiceMetadata{ServiceName: svc})
		}
		if b.Sections.ServiceMetadata[i].Extras == nil {
			b.Sections.ServiceMetadata[i].Extras = map[string]string{}
		}
		b.Sections.ServiceMetadata[i].Extras[k] = val
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return err
	}

	trows, err := tx.Query(ctx, `SELECT service_name, tag_id FROM service_tags WHERE organization_id=$1 ORDER BY service_name`, org)
	if err != nil {
		return err
	}
	for trows.Next() {
		var svc, tid string
		if err := trows.Scan(&svc, &tid); err != nil {
			trows.Close()
			return err
		}
		if s, ok := tagSlug[tid]; ok {
			b.Sections.ServiceTags = append(b.Sections.ServiceTags, ServiceTag{ServiceName: svc, Tag: s})
		}
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return err
	}

	srows, err := tx.Query(ctx, `SELECT service_name, direction, schema_id FROM service_schemas WHERE organization_id=$1 ORDER BY service_name, direction`, org)
	if err != nil {
		return err
	}
	defer srows.Close()
	for srows.Next() {
		var svc, dir, sid string
		if err := srows.Scan(&svc, &dir, &sid); err != nil {
			return err
		}
		if k, ok := schemaKey[sid]; ok {
			b.Sections.ServiceSchemas = append(b.Sections.ServiceSchemas, ServiceSchema{ServiceName: svc, Direction: dir, Schema: k})
		}
	}
	return srows.Err()
}

func exportMessageViews(ctx context.Context, tx pgx.Tx, org string, b *Bundle, integSlug map[string]string) error {
	// Shared views only — personal (unshared) views belong to a person,
	// and people don't travel between environments.
	rows, err := tx.Query(ctx, `
		SELECT name, COALESCE(description,''), pinned, COALESCE(filters,'null'::jsonb)::text,
		       scope_integration_id, scope_service_id
		FROM message_views WHERE organization_id=$1 AND shared ORDER BY name`, org)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v MessageView
		var filters string
		var integID *string
		if err := rows.Scan(&v.Name, &v.Description, &v.Pinned, &filters, &integID, &v.ScopeServiceID); err != nil {
			return err
		}
		if filters != "null" {
			v.Filters = json.RawMessage(filters)
		}
		if integID != nil {
			if s, ok := integSlug[*integID]; ok {
				v.ScopeIntegration = &s
			}
		}
		b.Sections.MessageViews = append(b.Sections.MessageViews, v)
	}
	return rows.Err()
}

func exportTemplates(ctx context.Context, tx pgx.Tx, org string, b *Bundle) error {
	rows, err := tx.Query(ctx, `
		SELECT name, COALESCE(description,''), COALESCE(source,''), COALESCE(checks,'null'::jsonb)::text
		FROM monitoring_templates WHERE org_id=$1 ORDER BY name`, org)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var t Template
		var checks string
		if err := rows.Scan(&t.Name, &t.Description, &t.Source, &checks); err != nil {
			return err
		}
		if checks != "null" {
			t.Checks = json.RawMessage(checks)
		}
		b.Sections.Templates = append(b.Sections.Templates, t)
	}
	return rows.Err()
}

func exportAlertRules(ctx context.Context, tx pgx.Tx, org string, b *Bundle, integSlug, groupSlug, channelName map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT id, name, COALESCE(description,''), signal::text, COALESCE(rule_spec,'null'::jsonb)::text,
		       severity::text, COALESCE(evaluation_interval::text,''), enabled, service_name, integration_id,
		       group_id, COALESCE(title_template,''), COALESCE(body_template,''), COALESCE(source,''),
		       COALESCE(display_on_service,false), COALESCE(unit,''), COALESCE(resolve_mode,''),
		       COALESCE(notification_config,'null'::jsonb)::text
		FROM alert_rules WHERE organization_id=$1 ORDER BY name, id`, org)
	if err != nil {
		return err
	}
	var rids []string
	for rows.Next() {
		var id, spec, nconf string
		var integID, gid *string
		var r AlertRule
		if err := rows.Scan(&id, &r.Name, &r.Description, &r.Signal, &spec, &r.Severity,
			&r.EvaluationInterval, &r.Enabled, &r.ServiceName, &integID, &gid,
			&r.TitleTemplate, &r.BodyTemplate, &r.Source, &r.DisplayOnService,
			&r.Unit, &r.ResolveMode, &nconf); err != nil {
			rows.Close()
			return err
		}
		if spec != "null" {
			r.RuleSpec = json.RawMessage(spec)
		}
		if nconf != "null" {
			r.NotificationConfig = json.RawMessage(nconf)
		}
		if integID != nil {
			if s, ok := integSlug[*integID]; ok {
				r.Integration = &s
			}
		}
		if gid != nil {
			if s, ok := groupSlug[*gid]; ok {
				r.Group = &s
			}
		}
		rids = append(rids, id)
		b.Sections.AlertRules = append(b.Sections.AlertRules, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i, rid := range rids {
		crows, err := tx.Query(ctx, `SELECT channel_id FROM alert_rule_routes WHERE alert_rule_id=$1`, rid)
		if err != nil {
			return err
		}
		for crows.Next() {
			var cid string
			if err := crows.Scan(&cid); err != nil {
				crows.Close()
				return err
			}
			if n, ok := channelName[cid]; ok {
				b.Sections.AlertRules[i].Channels = append(b.Sections.AlertRules[i].Channels, n)
			}
		}
		crows.Close()
		if err := crows.Err(); err != nil {
			return err
		}
		sort.Strings(b.Sections.AlertRules[i].Channels)
	}
	return nil
}

func exportDashboards(ctx context.Context, tx pgx.Tx, org string, b *Bundle, groupSlug, integSlug map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT id, name, is_default, auto_include_all, COALESCE(default_widget_type,''), COALESCE(position,0), group_id
		FROM dashboards WHERE organization_id=$1 ORDER BY name`, org)
	if err != nil {
		return err
	}
	var dids []string
	for rows.Next() {
		var id string
		var gid *string
		var d Dashboard
		if err := rows.Scan(&id, &d.Name, &d.IsDefault, &d.AutoIncludeAll, &d.DefaultWidgetType, &d.Position, &gid); err != nil {
			rows.Close()
			return err
		}
		if gid != nil {
			if s, ok := groupSlug[*gid]; ok {
				d.Group = &s
			}
		}
		dids = append(dids, id)
		b.Sections.Dashboards = append(b.Sections.Dashboards, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i, did := range dids {
		irows, err := tx.Query(ctx, `
			SELECT widget_type, COALESCE(position,0), COALESCE(entity_kind,''), integration_id, system_name
			FROM dashboard_items WHERE dashboard_id=$1 ORDER BY position`, did)
		if err != nil {
			return err
		}
		for irows.Next() {
			var it DashboardItem
			var integID *string
			if err := irows.Scan(&it.WidgetType, &it.Position, &it.EntityKind, &integID, &it.SystemName); err != nil {
				irows.Close()
				return err
			}
			if integID != nil {
				if s, ok := integSlug[*integID]; ok {
					it.Integration = &s
				}
			}
			b.Sections.Dashboards[i].Items = append(b.Sections.Dashboards[i].Items, it)
		}
		irows.Close()
		if err := irows.Err(); err != nil {
			return err
		}
	}
	return nil
}

func exportCellSettings(ctx context.Context, tx pgx.Tx, b *Bundle) error {
	rows, err := tx.Query(ctx, `SELECT key, value::text, COALESCE(description,'') FROM cell_settings ORDER BY key`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s CellSetting
		var val string
		if err := rows.Scan(&s.Key, &val, &s.Description); err != nil {
			return err
		}
		s.Value = json.RawMessage(val)
		b.Sections.CellSettings = append(b.Sections.CellSettings, s)
	}
	return rows.Err()
}
