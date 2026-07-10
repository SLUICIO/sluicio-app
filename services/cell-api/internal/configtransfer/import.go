// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package configtransfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ConflictError is a natural-key collision under ModeStrict. The
// handler maps it to 409; the transaction rolls back — by the
// atomicity contract the import then never happened.
type ConflictError struct {
	Section string
	Key     string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("configtransfer: %s %q already exists (strict mode)", e.Section, e.Key)
}

// ValidationError is a malformed or unresolvable bundle reference.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "configtransfer: " + e.Msg }

// Import applies a bundle to the org. THE CALLER OWNS THE TRANSACTION:
// wrap this in exactly one pgx.Tx and commit only on nil error (or roll
// back unconditionally for a dry run). Nothing in here commits, saves
// points, or writes outside tx — that is what makes a failed import
// indistinguishable from no import.
func Import(ctx context.Context, tx pgx.Tx, orgID string, b *Bundle, opts Options, rep *Report) error {
	if b.FormatVersion != FormatVersion {
		return &ValidationError{Msg: fmt.Sprintf("bundle format_version %d, this cell supports %d", b.FormatVersion, FormatVersion)}
	}
	if opts.Mode != ModeStrict && opts.Mode != ModeReplace {
		return &ValidationError{Msg: "mode must be strict or replace"}
	}
	rep.Mode = opts.Mode

	// One import at a time per org — same advisory-lock idiom as the
	// audit chain. Scoped to the transaction.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('configtransfer:' || $1::text))`, orgID); err != nil {
		return fmt.Errorf("configtransfer: lock: %w", err)
	}

	r := &resolver{ctx: ctx, tx: tx, org: orgID, opts: opts, rep: rep}

	type step struct {
		name string
		fn   func() error
	}
	// Dependency order — referenced objects import before referrers.
	steps := []step{
		{"tags", r.importTags(b.Sections.Tags)},
		{"metadata_fields", r.importFields(b.Sections.MetadataFields)},
		{"schemas", r.importSchemas(b.Sections.Schemas)},
		{"maps", r.importMaps(b.Sections.Maps)},
		{"system_types", r.importSystemTypes(b.Sections.SystemTypes)},
		{"systems", r.importSystems(b.Sections.Systems)},
		{"service_facets", r.importFacets(b.Sections.ServiceFacets, b.Sections.FacetMappings, b.Sections.FacetOverrides)},
		{"groups", r.importGroups(b.Sections.Groups)},
		{"notification_channels", r.importChannels(b.Sections.Channels)},
		{"notification_profiles", r.importProfiles(b.Sections.Profiles)},
		{"integrations", r.importIntegrations(b.Sections.Integrations)},
		{"access_policies", r.importPolicies(b.Sections.AccessPolicies)},
		{"service_config", r.importServiceConfig(b.Sections.ServiceMetadata, b.Sections.ServiceTags, b.Sections.ServiceSchemas)},
		{"message_views", r.importMessageViews(b.Sections.MessageViews)},
		{"monitoring_templates", r.importTemplates(b.Sections.Templates)},
		{"alert_rules", r.importAlertRules(b.Sections.AlertRules)},
		{"dashboards", r.importDashboards(b.Sections.Dashboards)},
		{"cell_settings", r.importCellSettings(b.Sections.CellSettings)},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			return fmt.Errorf("configtransfer: import %s: %w", s.name, err)
		}
	}
	return nil
}

// resolver carries the transaction plus natural-key → id maps that fill
// as sections import. Lookups fall back to the target org's existing
// rows, so a bundle may reference objects that already live there.
type resolver struct {
	ctx  context.Context
	tx   pgx.Tx
	org  string
	opts Options
	rep  *Report

	tags     map[string]string
	fields   map[string]string
	schemas  map[string]string
	groups   map[string]string
	systems  map[string]string
	integs   map[string]string
	channels map[string]string
	profiles map[string]string
}

// lookup resolves a natural key: imported map first, then the target
// org. A miss is a validation error — dangling references must not
// survive an import.
func (r *resolver) lookup(kind string, m map[string]string, key, query string, args ...any) (string, error) {
	if id, ok := m[key]; ok {
		return id, nil
	}
	var id string
	err := r.tx.QueryRow(r.ctx, query, args...).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", &ValidationError{Msg: fmt.Sprintf("%s %q referenced but not found in bundle or target org", kind, key)}
	}
	if err != nil {
		return "", err
	}
	m[key] = id
	return id, nil
}

func (r *resolver) tag(slug string) (string, error) {
	if r.tags == nil {
		r.tags = map[string]string{}
	}
	return r.lookup("tag", r.tags, slug, `SELECT id FROM tags WHERE organization_id=$1 AND slug=$2`, r.org, slug)
}
func (r *resolver) field(key string) (string, error) {
	if r.fields == nil {
		r.fields = map[string]string{}
	}
	return r.lookup("metadata field", r.fields, key, `SELECT id FROM metadata_fields WHERE organization_id=$1 AND key=$2`, r.org, key)
}
func (r *resolver) schema(nk string) (string, error) {
	if r.schemas == nil {
		r.schemas = map[string]string{}
	}
	name, version := nk, ""
	if i := strings.LastIndex(nk, "@"); i > 0 {
		name, version = nk[:i], nk[i+1:]
	}
	return r.lookup("schema", r.schemas, nk, `SELECT id FROM schemas WHERE organization_id=$1 AND name=$2 AND COALESCE(version,'')=$3`, r.org, name, version)
}
func (r *resolver) group(slug string) (string, error) {
	if r.groups == nil {
		r.groups = map[string]string{}
	}
	return r.lookup("group", r.groups, slug, `SELECT id FROM groups WHERE org_id=$1 AND slug=$2`, r.org, slug)
}
func (r *resolver) system(name string) (string, error) {
	if r.systems == nil {
		r.systems = map[string]string{}
	}
	return r.lookup("system", r.systems, name, `SELECT id FROM systems WHERE org_id=$1 AND name=$2`, r.org, name)
}
func (r *resolver) integration(slug string) (string, error) {
	if r.integs == nil {
		r.integs = map[string]string{}
	}
	return r.lookup("integration", r.integs, slug, `SELECT id FROM integrations WHERE organization_id=$1 AND slug=$2`, r.org, slug)
}
func (r *resolver) channel(name string) (string, error) {
	if r.channels == nil {
		r.channels = map[string]string{}
	}
	return r.lookup("notification channel", r.channels, name, `SELECT id FROM notification_channels WHERE organization_id=$1 AND name=$2`, r.org, name)
}
func (r *resolver) profile(name string) (string, error) {
	if r.profiles == nil {
		r.profiles = map[string]string{}
	}
	return r.lookup("notification profile", r.profiles, name, `SELECT id FROM notification_profiles WHERE organization_id=$1 AND name=$2`, r.org, name)
}

// upsert is the shared collision protocol: find an existing row by the
// natural-key query. strict + found → ConflictError. replace + found →
// run update, count updated. Not found → run insert, count created.
// Child collections of an updated parent are replaced wholesale by the
// caller — "the bundle wins for this object"; org-level objects the
// bundle doesn't mention are never touched.
func (r *resolver) upsert(sec *SectionReport, section, key, findQ string, findArgs []any,
	insert func() (string, error), update func(id string) error) (string, error) {
	var id string
	err := r.tx.QueryRow(r.ctx, findQ, findArgs...).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		nid, ierr := insert()
		if ierr != nil {
			return "", ierr
		}
		sec.Created++
		return nid, nil
	case err != nil:
		return "", err
	default:
		if r.opts.Mode == ModeStrict {
			return "", &ConflictError{Section: section, Key: key}
		}
		if err := update(id); err != nil {
			return "", err
		}
		sec.Updated++
		return id, nil
	}
}

func jsonArg(m json.RawMessage) any {
	if len(m) == 0 {
		return nil
	}
	return string(m)
}

// ── sections ─────────────────────────────────────────────────────────

func (r *resolver) importTags(tags []Tag) func() error {
	return func() error {
		sec := r.rep.section("tags")
		defer r.rep.save("tags", sec)
		if r.tags == nil {
			r.tags = map[string]string{}
		}
		for _, t := range tags {
			id, err := r.upsert(sec, "tag", t.Slug,
				`SELECT id FROM tags WHERE organization_id=$1 AND slug=$2`, []any{r.org, t.Slug},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `INSERT INTO tags (organization_id, slug, name, color) VALUES ($1,$2,$3,$4) RETURNING id`,
						r.org, t.Slug, t.Name, t.Color).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `UPDATE tags SET name=$2, color=$3, updated_at=now() WHERE id=$1`, id, t.Name, t.Color)
					return err
				})
			if err != nil {
				return err
			}
			r.tags[t.Slug] = id
		}
		return nil
	}
}

func (r *resolver) importFields(fields []MetadataField) func() error {
	return func() error {
		sec := r.rep.section("metadata_fields")
		defer r.rep.save("metadata_fields", sec)
		if r.fields == nil {
			r.fields = map[string]string{}
		}
		for _, f := range fields {
			id, err := r.upsert(sec, "metadata field", f.Key,
				`SELECT id FROM metadata_fields WHERE organization_id=$1 AND key=$2`, []any{r.org, f.Key},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO metadata_fields (organization_id, key, label, type, options, description,
						  applies_to_integration, applies_to_service, applies_to_system, required, system_type_key)
						VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10,$11) RETURNING id`,
						r.org, f.Key, f.Label, f.Type, jsonArg(f.Options), f.Description,
						f.AppliesToIntegration, f.AppliesToService, f.AppliesToSystem, f.Required, f.SystemTypeKey).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE metadata_fields SET label=$2, type=$3, options=$4::jsonb, description=$5,
						  applies_to_integration=$6, applies_to_service=$7, applies_to_system=$8, required=$9,
						  system_type_key=$10, updated_at=now() WHERE id=$1`,
						id, f.Label, f.Type, jsonArg(f.Options), f.Description,
						f.AppliesToIntegration, f.AppliesToService, f.AppliesToSystem, f.Required, f.SystemTypeKey)
					return err
				})
			if err != nil {
				return err
			}
			r.fields[f.Key] = id
		}
		return nil
	}
}

func (r *resolver) importSchemas(schemas []Schema) func() error {
	return func() error {
		sec := r.rep.section("schemas")
		defer r.rep.save("schemas", sec)
		if r.schemas == nil {
			r.schemas = map[string]string{}
		}
		for _, s := range schemas {
			nk := schemaNK(s.Name, s.Version)
			id, err := r.upsert(sec, "schema", nk,
				`SELECT id FROM schemas WHERE organization_id=$1 AND name=$2 AND COALESCE(version,'')=$3`,
				[]any{r.org, s.Name, s.Version},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO schemas (organization_id, name, version, kind, description, format, content)
						VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),$5,$6,$7) RETURNING id`,
						r.org, s.Name, s.Version, s.Kind, s.Description, s.Format, s.Content).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE schemas SET kind=NULLIF($2,''), description=$3, format=$4, content=$5, updated_at=now() WHERE id=$1`,
						id, s.Kind, s.Description, s.Format, s.Content)
					return err
				})
			if err != nil {
				return err
			}
			r.schemas[nk] = id
		}
		return nil
	}
}

func (r *resolver) importMaps(maps []MapDef) func() error {
	return func() error {
		sec := r.rep.section("maps")
		defer r.rep.save("maps", sec)
		for _, m := range maps {
			var fromID, toID any
			if m.FromSchema != nil {
				id, err := r.schema(*m.FromSchema)
				if err != nil {
					return err
				}
				fromID = id
			}
			if m.ToSchema != nil {
				id, err := r.schema(*m.ToSchema)
				if err != nil {
					return err
				}
				toID = id
			}
			nk := schemaNK(m.Name, m.Version)
			_, err := r.upsert(sec, "map", nk,
				`SELECT id FROM maps WHERE organization_id=$1 AND name=$2 AND COALESCE(version,'')=$3`,
				[]any{r.org, m.Name, m.Version},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO maps (organization_id, name, version, description, format, content, from_schema_id, to_schema_id)
						VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8) RETURNING id`,
						r.org, m.Name, m.Version, m.Description, m.Format, m.Content, fromID, toID).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE maps SET description=$2, format=$3, content=$4, from_schema_id=$5, to_schema_id=$6, updated_at=now() WHERE id=$1`,
						id, m.Description, m.Format, m.Content, fromID, toID)
					return err
				})
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func (r *resolver) importSystemTypes(types []SystemType) func() error {
	return func() error {
		sec := r.rep.section("system_types")
		defer r.rep.save("system_types", sec)
		for _, t := range types {
			_, err := r.upsert(sec, "system type", t.Key,
				`SELECT id FROM system_types WHERE org_id=$1 AND key=$2`, []any{r.org, t.Key},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO system_types (org_id, key, label, is_system, detect_prefixes, checks)
						VALUES ($1,$2,$3,$4,COALESCE($5::jsonb,'[]'::jsonb),COALESCE($6::jsonb,'[]'::jsonb)) RETURNING id`,
						r.org, t.Key, t.Label, t.IsSystem, jsonArg(t.DetectPrefixes), jsonArg(t.Checks)).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE system_types SET label=$2, is_system=$3, detect_prefixes=COALESCE($4::jsonb,'[]'::jsonb),
						  checks=COALESCE($5::jsonb,'[]'::jsonb), updated_at=now() WHERE id=$1`,
						id, t.Label, t.IsSystem, jsonArg(t.DetectPrefixes), jsonArg(t.Checks))
					return err
				})
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func (r *resolver) importSystems(systems []System) func() error {
	return func() error {
		sec := r.rep.section("systems")
		defer r.rep.save("systems", sec)
		if r.systems == nil {
			r.systems = map[string]string{}
		}
		for _, s := range systems {
			id, err := r.upsert(sec, "system", s.Name,
				`SELECT id FROM systems WHERE org_id=$1 AND name=$2`, []any{r.org, s.Name},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO systems (org_id, name, type_key, description, badge_public)
						VALUES ($1,$2,$3,$4,$5) RETURNING id`,
						r.org, s.Name, s.TypeKey, s.Description, s.BadgePublic).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE systems SET type_key=$2, description=$3, badge_public=$4, updated_at=now() WHERE id=$1`,
						id, s.TypeKey, s.Description, s.BadgePublic)
					return err
				})
			if err != nil {
				return err
			}
			r.systems[s.Name] = id
			if len(s.Metadata) > 0 {
				if _, err := r.tx.Exec(r.ctx, `DELETE FROM system_metadata WHERE system_id=$1`, id); err != nil {
					return err
				}
				for k, v := range s.Metadata {
					fid, err := r.field(k)
					if err != nil {
						return err
					}
					if _, err := r.tx.Exec(r.ctx,
						`INSERT INTO system_metadata (system_id, field_id, value) VALUES ($1,$2,$3)`, id, fid, v); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
}

func (r *resolver) importFacets(facets []ServiceFacet, mappings []FacetMapping, overrides []FacetOverride) func() error {
	return func() error {
		sec := r.rep.section("service_facets")
		defer r.rep.save("service_facets", sec)
		for _, f := range facets {
			_, err := r.upsert(sec, "service facet", f.Slug,
				`SELECT id FROM service_facets WHERE org_id=$1 AND slug=$2`, []any{r.org, f.Slug},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx,
						`INSERT INTO service_facets (org_id, slug, name, description) VALUES ($1,$2,$3,$4) RETURNING id`,
						r.org, f.Slug, f.Name, f.Description).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `UPDATE service_facets SET name=$2, description=$3, updated_at=now() WHERE id=$1`, id, f.Name, f.Description)
					return err
				})
			if err != nil {
				return err
			}
		}
		for _, m := range mappings {
			key := strings.Join([]string{m.ServiceName, m.AttributeSource, m.AttributeKey, m.MatchOperator, m.MatchValue}, "|")
			_, err := r.upsert(sec, "facet mapping", key,
				`SELECT id FROM service_facet_mappings WHERE organization_id=$1 AND service_name=$2
				   AND attribute_source::text=$3 AND attribute_key=$4 AND match_operator::text=$5 AND match_value=$6`,
				[]any{r.org, m.ServiceName, m.AttributeSource, m.AttributeKey, m.MatchOperator, m.MatchValue},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO service_facet_mappings (organization_id, service_name, attribute_source, attribute_key,
						  match_operator, match_value, set_io_kind, set_io_role)
						VALUES ($1,$2,$3::facet_mapping_attr_source,$4,$5::facet_mapping_operator,$6,NULLIF($7,''),NULLIF($8,'')) RETURNING id`,
						r.org, m.ServiceName, m.AttributeSource, m.AttributeKey, m.MatchOperator, m.MatchValue, m.SetIOKind, m.SetIORole).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx,
						`UPDATE service_facet_mappings SET set_io_kind=NULLIF($2,''), set_io_role=NULLIF($3,''), updated_at=now() WHERE id=$1`,
						id, m.SetIOKind, m.SetIORole)
					return err
				})
			if err != nil {
				return err
			}
		}
		for _, o := range overrides {
			key := o.ServiceName + "|" + o.FacetSlug
			_, err := r.upsert(sec, "facet override", key,
				`SELECT id FROM service_facet_overrides WHERE organization_id=$1 AND service_name=$2 AND facet_slug=$3`,
				[]any{r.org, o.ServiceName, o.FacetSlug},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO service_facet_overrides (organization_id, service_name, facet_slug, action)
						VALUES ($1,$2,$3,$4::service_facet_override_action) RETURNING id`,
						r.org, o.ServiceName, o.FacetSlug, o.Action).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx,
						`UPDATE service_facet_overrides SET action=$2::service_facet_override_action, updated_at=now() WHERE id=$1`, id, o.Action)
					return err
				})
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func (r *resolver) importGroups(groups []Group) func() error {
	return func() error {
		sec := r.rep.section("groups")
		defer r.rep.save("groups", sec)
		if r.groups == nil {
			r.groups = map[string]string{}
		}
		matched, skipped := 0, 0
		for _, g := range groups {
			id, err := r.upsert(sec, "group", g.Slug,
				`SELECT id FROM groups WHERE org_id=$1 AND slug=$2`, []any{r.org, g.Slug},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx,
						`INSERT INTO groups (org_id, slug, name, description) VALUES ($1,$2,$3,$4) RETURNING id`,
						r.org, g.Slug, g.Name, g.Description).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `UPDATE groups SET name=$2, description=$3, updated_at=now() WHERE id=$1`, id, g.Name, g.Description)
					return err
				})
			if err != nil {
				return err
			}
			r.groups[g.Slug] = id
			if r.opts.MatchMembersByEmail {
				for _, m := range g.Members {
					tag, err := r.tx.Exec(r.ctx, `
						INSERT INTO group_members (user_id, group_id, role)
						SELECT u.id, $1, $3::text
						FROM users u JOIN org_members om ON om.user_id = u.id AND om.org_id = $2
						WHERE lower(u.email) = lower($4)
						ON CONFLICT (user_id, group_id) DO UPDATE SET role = EXCLUDED.role`,
						id, r.org, m.Role, m.Email)
					if err != nil {
						return err
					}
					if tag.RowsAffected() > 0 {
						matched++
					} else {
						skipped++
					}
				}
			} else if len(g.Members) > 0 {
				skipped += len(g.Members)
			}
		}
		if skipped > 0 {
			r.rep.warnf("groups: %d member(s) not attached (no matching user in the target org, or email matching disabled)", skipped)
		}
		_ = matched
		return nil
	}
}

func (r *resolver) importChannels(channels []Channel) func() error {
	return func() error {
		sec := r.rep.section("notification_channels")
		defer r.rep.save("notification_channels", sec)
		if r.channels == nil {
			r.channels = map[string]string{}
		}
		for _, c := range channels {
			id, err := r.upsert(sec, "notification channel", c.Name,
				`SELECT id FROM notification_channels WHERE organization_id=$1 AND name=$2`, []any{r.org, c.Name},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO notification_channels (organization_id, name, kind, config)
						VALUES ($1,$2,$3,COALESCE($4::jsonb,'{}'::jsonb)) RETURNING id`,
						r.org, c.Name, c.Kind, jsonArg(c.Config)).Scan(&id)
					return id, err
				},
				func(id string) error {
					// A redacted bundle config must not clobber real
					// credentials already present in the target: only
					// non-secret-bearing kinds update config.
					if c.NeedsCredentials {
						return nil
					}
					_, err := r.tx.Exec(r.ctx,
						`UPDATE notification_channels SET kind=$2, config=COALESCE($3::jsonb,'{}'::jsonb), updated_at=now() WHERE id=$1`,
						id, c.Kind, jsonArg(c.Config))
					return err
				})
			if err != nil {
				return err
			}
			r.channels[c.Name] = id
			if c.NeedsCredentials {
				r.rep.NeedsCredentials = append(r.rep.NeedsCredentials, c.Name)
			}
		}
		return nil
	}
}

func (r *resolver) importProfiles(profiles []Profile) func() error {
	return func() error {
		sec := r.rep.section("notification_profiles")
		defer r.rep.save("notification_profiles", sec)
		if r.profiles == nil {
			r.profiles = map[string]string{}
		}
		for _, p := range profiles {
			var gid any
			if p.Group != nil {
				id, err := r.group(*p.Group)
				if err != nil {
					return err
				}
				gid = id
			}
			id, err := r.upsert(sec, "notification profile", p.Name,
				`SELECT id FROM notification_profiles WHERE organization_id=$1 AND name=$2`, []any{r.org, p.Name},
				func() (string, error) {
					var id string
					// notification_profiles has no id default — mint one here.
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO notification_profiles (id, organization_id, group_id, name, grouping, renotify_minutes, is_default)
						VALUES (gen_random_uuid(),$1,$2,$3,NULLIF($4,''),$5,$6) RETURNING id`,
						r.org, gid, p.Name, p.Grouping, p.RenotifyMinutes, p.IsDefault).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE notification_profiles SET group_id=$2, grouping=NULLIF($3,''), renotify_minutes=$4,
						  is_default=$5, updated_at=now() WHERE id=$1`,
						id, gid, p.Grouping, p.RenotifyMinutes, p.IsDefault)
					return err
				})
			if err != nil {
				return err
			}
			r.profiles[p.Name] = id
			if _, err := r.tx.Exec(r.ctx, `DELETE FROM notification_profile_channels WHERE profile_id=$1`, id); err != nil {
				return err
			}
			for _, cn := range p.Channels {
				cid, err := r.channel(cn)
				if err != nil {
					return err
				}
				if _, err := r.tx.Exec(r.ctx,
					`INSERT INTO notification_profile_channels (profile_id, channel_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, cid); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func (r *resolver) importIntegrations(integrations []Integration) func() error {
	return func() error {
		sec := r.rep.section("integrations")
		defer r.rep.save("integrations", sec)
		if r.integs == nil {
			r.integs = map[string]string{}
		}
		for _, in := range integrations {
			var pid any
			if in.Profile != nil {
				id, err := r.profile(*in.Profile)
				if err != nil {
					return err
				}
				pid = id
			}
			id, err := r.upsert(sec, "integration", in.Slug,
				`SELECT id FROM integrations WHERE organization_id=$1 AND slug=$2`, []any{r.org, in.Slug},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO integrations (organization_id, slug, name, description, badge_public, notification_profile_id)
						VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
						r.org, in.Slug, in.Name, in.Description, in.BadgePublic, pid).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE integrations SET name=$2, description=$3, badge_public=$4, notification_profile_id=$5, updated_at=now() WHERE id=$1`,
						id, in.Name, in.Description, in.BadgePublic, pid)
					return err
				})
			if err != nil {
				return err
			}
			r.integs[in.Slug] = id

			// Child collections: bundle wins for this integration.
			if _, err := r.tx.Exec(r.ctx, `DELETE FROM integration_matchers WHERE integration_id=$1`, id); err != nil {
				return err
			}
			for _, m := range in.Matchers {
				if _, err := r.tx.Exec(r.ctx, `
					INSERT INTO integration_matchers (integration_id, attribute, operator, value, match_group)
					VALUES ($1,$2,$3::matcher_operator,$4,$5)`,
					id, m.Attribute, m.Operator, m.Value, m.MatchGroup); err != nil {
					return err
				}
			}
			if _, err := r.tx.Exec(r.ctx, `DELETE FROM integration_tags WHERE integration_id=$1`, id); err != nil {
				return err
			}
			for _, slug := range in.Tags {
				tid, err := r.tag(slug)
				if err != nil {
					return err
				}
				if _, err := r.tx.Exec(r.ctx,
					`INSERT INTO integration_tags (integration_id, tag_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, tid); err != nil {
					return err
				}
			}
			if _, err := r.tx.Exec(r.ctx, `DELETE FROM integration_metadata WHERE integration_id=$1`, id); err != nil {
				return err
			}
			for k, v := range in.Metadata {
				fid, err := r.field(k)
				if err != nil {
					return err
				}
				if _, err := r.tx.Exec(r.ctx,
					`INSERT INTO integration_metadata (integration_id, field_id, value) VALUES ($1,$2,$3)`, id, fid, v); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func (r *resolver) importPolicies(policies []AccessPolicy) func() error {
	return func() error {
		sec := r.rep.section("access_policies")
		defer r.rep.save("access_policies", sec)
		for _, p := range policies {
			gid, err := r.group(p.Group)
			if err != nil {
				return err
			}
			var integID, sysID any
			if p.TargetIntegration != nil {
				id, err := r.integration(*p.TargetIntegration)
				if err != nil {
					return err
				}
				integID = id
			}
			if p.TargetSystem != nil {
				id, err := r.system(*p.TargetSystem)
				if err != nil {
					return err
				}
				sysID = id
			}
			// Pseudo natural key: group + kind + targets + conditions.
			key := strings.Join([]string{p.Group, p.Kind, deref(p.TargetServiceName), deref(p.TargetIntegration),
				deref(p.TargetSystem), deref(p.TargetSystemKind), string(p.AttributeMatch), string(p.Conditions)}, "|")
			_, err = r.upsert(sec, "access policy", key,
				`SELECT id FROM group_access_policies
				 WHERE group_id=$1 AND kind=$2
				   AND COALESCE(target_service_name,'') = COALESCE($3,'')
				   AND COALESCE(target_integration_id::text,'') = COALESCE($4::text,'')
				   AND COALESCE(target_system_id::text,'') = COALESCE($5::text,'')
				   AND COALESCE(target_system_kind,'') = COALESCE($6,'')
				   AND COALESCE(attribute_match::text,'null') = COALESCE(NULLIF($7,'')::jsonb::text,'null')
				   AND COALESCE(conditions::text,'null') = COALESCE(NULLIF($8,'')::jsonb::text,'null')`,
				[]any{gid, p.Kind, p.TargetServiceName, integID, sysID, p.TargetSystemKind, string(p.AttributeMatch), string(p.Conditions)},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO group_access_policies (group_id, kind, target_service_name, target_integration_id,
						  attribute_match, target_system_kind, conditions, target_system_id, signals)
						VALUES ($1,$2,$3,$4,NULLIF($5,'')::jsonb,$6,NULLIF($7,'')::jsonb,$8,$9) RETURNING id`,
						gid, p.Kind, p.TargetServiceName, integID, string(p.AttributeMatch),
						p.TargetSystemKind, string(p.Conditions), sysID, p.Signals).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `UPDATE group_access_policies SET signals=$2 WHERE id=$1`, id, p.Signals)
					return err
				})
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func (r *resolver) importServiceConfig(meta []ServiceMetadata, tags []ServiceTag, schemas []ServiceSchema) func() error {
	return func() error {
		sec := r.rep.section("service_config")
		defer r.rep.save("service_config", sec)
		for _, m := range meta {
			_, err := r.upsert(sec, "service metadata", m.ServiceName,
				`SELECT service_name FROM service_metadata WHERE organization_id=$1 AND service_name=$2`,
				[]any{r.org, m.ServiceName},
				func() (string, error) {
					_, err := r.tx.Exec(r.ctx, `
						INSERT INTO service_metadata (organization_id, service_name, description, owner, on_call, team, repository, runbook_url)
						VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
						r.org, m.ServiceName, m.Description, m.Owner, m.OnCall, m.Team, m.Repository, m.RunbookURL)
					return m.ServiceName, err
				},
				func(string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE service_metadata SET description=$3, owner=$4, on_call=$5, team=$6, repository=$7, runbook_url=$8, updated_at=now()
						WHERE organization_id=$1 AND service_name=$2`,
						r.org, m.ServiceName, m.Description, m.Owner, m.OnCall, m.Team, m.Repository, m.RunbookURL)
					return err
				})
			if err != nil {
				return err
			}
			if len(m.Extras) > 0 {
				if _, err := r.tx.Exec(r.ctx,
					`DELETE FROM service_metadata_extras WHERE organization_id=$1 AND service_name=$2`, r.org, m.ServiceName); err != nil {
					return err
				}
				for k, v := range m.Extras {
					fid, err := r.field(k)
					if err != nil {
						return err
					}
					if _, err := r.tx.Exec(r.ctx, `
						INSERT INTO service_metadata_extras (organization_id, service_name, field_id, value)
						VALUES ($1,$2,$3,$4)`, r.org, m.ServiceName, fid, v); err != nil {
						return err
					}
				}
			}
		}
		for _, t := range tags {
			tid, err := r.tag(t.Tag)
			if err != nil {
				return err
			}
			ct, err := r.tx.Exec(r.ctx, `
				INSERT INTO service_tags (organization_id, service_name, tag_id)
				VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, r.org, t.ServiceName, tid)
			if err != nil {
				return err
			}
			if ct.RowsAffected() > 0 {
				sec.Created++
			}
		}
		for _, s := range schemas {
			sid, err := r.schema(s.Schema)
			if err != nil {
				return err
			}
			_, err = r.upsert(sec, "service schema", s.ServiceName+"|"+s.Direction,
				`SELECT service_name FROM service_schemas WHERE organization_id=$1 AND service_name=$2 AND direction=$3`,
				[]any{r.org, s.ServiceName, s.Direction},
				func() (string, error) {
					_, err := r.tx.Exec(r.ctx, `
						INSERT INTO service_schemas (organization_id, service_name, direction, schema_id)
						VALUES ($1,$2,$3,$4)`, r.org, s.ServiceName, s.Direction, sid)
					return s.ServiceName, err
				},
				func(string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE service_schemas SET schema_id=$4, updated_at=now()
						WHERE organization_id=$1 AND service_name=$2 AND direction=$3`, r.org, s.ServiceName, s.Direction, sid)
					return err
				})
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func (r *resolver) importMessageViews(views []MessageView) func() error {
	return func() error {
		sec := r.rep.section("message_views")
		defer r.rep.save("message_views", sec)
		for _, v := range views {
			var integID any
			if v.ScopeIntegration != nil {
				id, err := r.integration(*v.ScopeIntegration)
				if err != nil {
					return err
				}
				integID = id
			}
			_, err := r.upsert(sec, "message view", v.Name,
				`SELECT id FROM message_views WHERE organization_id=$1 AND shared AND name=$2`, []any{r.org, v.Name},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO message_views (organization_id, owner_user_id, name, description, pinned, shared, filters, scope_integration_id, scope_service_id)
						VALUES ($1,$2,$3,$4,$5,true,COALESCE($6::jsonb,'{}'::jsonb),$7,$8) RETURNING id`,
						r.org, r.opts.ActorUserID, v.Name, v.Description, v.Pinned, jsonArg(v.Filters), integID, v.ScopeServiceID).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE message_views SET description=$2, pinned=$3, filters=COALESCE($4::jsonb,'{}'::jsonb),
						  scope_integration_id=$5, scope_service_id=$6, updated_at=now() WHERE id=$1`,
						id, v.Description, v.Pinned, jsonArg(v.Filters), integID, v.ScopeServiceID)
					return err
				})
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func (r *resolver) importTemplates(templates []Template) func() error {
	return func() error {
		sec := r.rep.section("monitoring_templates")
		defer r.rep.save("monitoring_templates", sec)
		for _, t := range templates {
			_, err := r.upsert(sec, "monitoring template", t.Name,
				`SELECT id FROM monitoring_templates WHERE org_id=$1 AND name=$2`, []any{r.org, t.Name},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO monitoring_templates (org_id, name, description, source, checks)
						VALUES ($1,$2,$3,NULLIF($4,''),COALESCE($5::jsonb,'[]'::jsonb)) RETURNING id`,
						r.org, t.Name, t.Description, t.Source, jsonArg(t.Checks)).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE monitoring_templates SET description=$2, source=NULLIF($3,''), checks=COALESCE($4::jsonb,'[]'::jsonb), updated_at=now() WHERE id=$1`,
						id, t.Description, t.Source, jsonArg(t.Checks))
					return err
				})
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func (r *resolver) importAlertRules(rules []AlertRule) func() error {
	return func() error {
		sec := r.rep.section("alert_rules")
		defer r.rep.save("alert_rules", sec)
		for _, a := range rules {
			var integID, gid any
			if a.Integration != nil {
				id, err := r.integration(*a.Integration)
				if err != nil {
					return err
				}
				integID = id
			}
			if a.Group != nil {
				id, err := r.group(*a.Group)
				if err != nil {
					return err
				}
				gid = id
			}
			key := strings.Join([]string{a.Name, a.Signal, deref(a.ServiceName), deref(a.Integration), deref(a.Group)}, "|")
			interval := a.EvaluationInterval
			if interval == "" {
				interval = "1 minute"
			}
			id, err := r.upsert(sec, "alert rule", key,
				`SELECT id FROM alert_rules
				 WHERE organization_id=$1 AND name=$2 AND signal::text=$3
				   AND COALESCE(service_name,'') = COALESCE($4,'')
				   AND COALESCE(integration_id::text,'') = COALESCE($5::text,'')
				   AND COALESCE(group_id::text,'') = COALESCE($6::text,'')`,
				[]any{r.org, a.Name, a.Signal, a.ServiceName, integID, gid},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO alert_rules (organization_id, integration_id, name, description, signal, rule_spec,
						  severity, evaluation_interval, enabled, service_name, group_id, title_template, body_template,
						  source, display_on_service, unit, resolve_mode, notification_config)
						VALUES ($1,$2,$3,$4,$5::alert_signal,COALESCE($6::jsonb,'{}'::jsonb),$7::alert_severity,$8::interval,
						  $9,$10,$11,$12,$13,COALESCE(NULLIF($14,''),'custom'),$15,$16,NULLIF($17,''),NULLIF($18,'')::jsonb) RETURNING id`,
						r.org, integID, a.Name, a.Description, a.Signal, jsonArg(a.RuleSpec), a.Severity, interval,
						a.Enabled, a.ServiceName, gid, a.TitleTemplate, a.BodyTemplate, a.Source,
						a.DisplayOnService, a.Unit, a.ResolveMode, string(a.NotificationConfig)).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE alert_rules SET description=$2, rule_spec=COALESCE($3::jsonb,'{}'::jsonb), severity=$4::alert_severity,
						  evaluation_interval=$5::interval, enabled=$6, title_template=$7, body_template=$8,
						  display_on_service=$9, unit=$10, resolve_mode=NULLIF($11,''), notification_config=NULLIF($12,'')::jsonb,
						  updated_at=now() WHERE id=$1`,
						id, a.Description, jsonArg(a.RuleSpec), a.Severity, interval, a.Enabled,
						a.TitleTemplate, a.BodyTemplate, a.DisplayOnService, a.Unit, a.ResolveMode, string(a.NotificationConfig))
					return err
				})
			if err != nil {
				return err
			}
			if _, err := r.tx.Exec(r.ctx, `DELETE FROM alert_rule_routes WHERE alert_rule_id=$1`, id); err != nil {
				return err
			}
			for _, cn := range a.Channels {
				cid, err := r.channel(cn)
				if err != nil {
					return err
				}
				if _, err := r.tx.Exec(r.ctx,
					`INSERT INTO alert_rule_routes (alert_rule_id, channel_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, cid); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func (r *resolver) importDashboards(dashboards []Dashboard) func() error {
	return func() error {
		sec := r.rep.section("dashboards")
		defer r.rep.save("dashboards", sec)
		for _, d := range dashboards {
			var gid any
			if d.Group != nil {
				id, err := r.group(*d.Group)
				if err != nil {
					return err
				}
				gid = id
			}
			id, err := r.upsert(sec, "dashboard", d.Name,
				`SELECT id FROM dashboards WHERE organization_id=$1 AND name=$2`, []any{r.org, d.Name},
				func() (string, error) {
					var id string
					err := r.tx.QueryRow(r.ctx, `
						INSERT INTO dashboards (organization_id, owner_user_id, name, is_default, auto_include_all, default_widget_type, position, group_id)
						VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8) RETURNING id`,
						r.org, r.opts.ActorUserID, d.Name, d.IsDefault, d.AutoIncludeAll, d.DefaultWidgetType, d.Position, gid).Scan(&id)
					return id, err
				},
				func(id string) error {
					_, err := r.tx.Exec(r.ctx, `
						UPDATE dashboards SET is_default=$2, auto_include_all=$3, default_widget_type=NULLIF($4,''), position=$5, group_id=$6, updated_at=now() WHERE id=$1`,
						id, d.IsDefault, d.AutoIncludeAll, d.DefaultWidgetType, d.Position, gid)
					return err
				})
			if err != nil {
				return err
			}
			if _, err := r.tx.Exec(r.ctx, `DELETE FROM dashboard_items WHERE dashboard_id=$1`, id); err != nil {
				return err
			}
			for _, it := range d.Items {
				var integID any
				if it.Integration != nil {
					iid, err := r.integration(*it.Integration)
					if err != nil {
						return err
					}
					integID = iid
				}
				if _, err := r.tx.Exec(r.ctx, `
					INSERT INTO dashboard_items (dashboard_id, integration_id, widget_type, position, entity_kind, system_name)
					VALUES ($1,$2,$3,$4,NULLIF($5,''),$6)`,
					id, integID, it.WidgetType, it.Position, it.EntityKind, it.SystemName); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

// importCellSettings applies cell-wide settings (retention, alert email
// template, environment label). Cell settings affect every org, so the
// section imports only for operators; the mfa/security policy is always
// skipped — importing an MFA requirement could lock people out.
func (r *resolver) importCellSettings(settings []CellSetting) func() error {
	return func() error {
		sec := r.rep.section("cell_settings")
		defer r.rep.save("cell_settings", sec)
		if len(settings) == 0 {
			return nil
		}
		if !r.opts.IsOperator {
			sec.Skipped = len(settings)
			r.rep.warnf("cell_settings: skipped %d setting(s) — importing them requires a cell operator", len(settings))
			return nil
		}
		for _, s := range settings {
			if strings.Contains(s.Key, "security") || strings.Contains(s.Key, "mfa") {
				sec.Skipped++
				r.rep.warnf("cell_settings: skipped %q — security policies are never imported", s.Key)
				continue
			}
			ct, err := r.tx.Exec(r.ctx, `
				INSERT INTO cell_settings (key, value, description)
				VALUES ($1, $2::jsonb, $3)
				ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
				s.Key, string(s.Value), s.Description)
			if err != nil {
				return err
			}
			_ = ct
			sec.Updated++
		}
		return nil
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
