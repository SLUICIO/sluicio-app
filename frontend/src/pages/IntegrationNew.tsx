// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { FormEvent, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { CreateTagRequest, MetadataField, Tag } from "../api/types";
import { FieldInput } from "../components/MetadataPanel";
import ServiceDependencySuggestions from "../components/ServiceDependencySuggestions";
import MatcherRules, { Rule, blankRule, rulesToMatchers } from "../components/MatcherRules";
import TagPicker from "../components/tags/TagPicker";
import { useCurrentUser } from "../lib/useCurrentUser";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";

const SERVICE_NAME_ATTR = "service.name";

// Derive a URL-safe slug from a free-text name: lowercase, non-alphanumerics
// collapse to single dashes, trimmed. Matches the slug input's [a-z0-9-]+.
function slugify(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

export default function IntegrationNew() {
  usePageTitle("New integration");
  const nav = useNavigate();
  const [params] = useSearchParams();
  const { can } = useCurrentUser();
  const allowed = can("integration.write");
  // Suggestions need a time window for their neighbor query. We reuse
  // the app-wide window so it tracks whatever the user has dialed in
  // — no separate picker needed on this form.
  const [windowVal] = useTimeWindow();
  // When linked from a service page (?seedService=order-api) we
  // pre-populate one rule pinning that service.
  const seedService = params.get("seedService") ?? "";

  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  // The slug auto-fills from the name until the user edits it directly.
  const [slugEdited, setSlugEdited] = useState(false);
  const [description, setDescription] = useState("");
  const [rules, setRules] = useState<Rule[]>([
    blankRule(seedService ? { serviceOp: "equals", service: seedService } : { serviceOp: "prefix" }),
  ]);
  // Tag ids selected for attachment after the integration is created.
  // The picker can also create new tags inline via createTag below.
  const [tagIds, setTagIds] = useState<string[]>([]);
  // Metadata fields that apply to integrations — captured as part of
  // creation, not as a post-create detour to the Metadata tab.
  const [metaFields, setMetaFields] = useState<MetadataField[]>([]);
  const [metaValues, setMetaValues] = useState<Record<string, string>>({});
  const [allTags, setAllTags] = useState<Tag[]>([]);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    api
      .listMetadataFields()
      .then((r) => setMetaFields((r.fields ?? []).filter((f) => f.applies_to_integration)))
      .catch(() => setMetaFields([]));
  }, []);
  const [submitting, setSubmitting] = useState(false);

  // Known service names for the service-rule autocomplete.
  // Fetched once on mount; an empty list just means no suggestions.
  const [knownServices, setKnownServices] = useState<string[]>([]);
  // Live attribute keys (producer, consumer, …) suggested for the
  // attribute-condition inputs, discovered from recent telemetry.
  const [attrKeys, setAttrKeys] = useState<string[]>([]);
  useEffect(() => {
    api
      .listServices()
      .then((r) =>
        setKnownServices((r.services ?? []).map((s) => s.service_name).sort())
      )
      .catch(() => setKnownServices([]));
    api
      .listTags()
      .then((d) => setAllTags(d.tags ?? []))
      .catch(() => setAllTags([]));
    api
      .messageFields(windowVal)
      .then((r) => {
        const keys = (r.fields.find((f) => f.field === "payload")?.attributeKeys ?? [])
          .map((k) => k.key)
          .filter((k) => k !== SERVICE_NAME_ATTR);
        setAttrKeys(keys);
      })
      .catch(() => setAttrKeys([]));
  }, [windowVal]);

  // Inline tag creation lives on the parent so the picker can call
  // it without owning any API knowledge. We refresh the local cache
  // after each create so the chip appears as a selectable option for
  // anyone the picker hands the list to next.
  const createTag = async (req: CreateTagRequest): Promise<Tag> => {
    const created = await api.createTag(req);
    setAllTags((curr) =>
      [...curr, created].sort((a, b) => a.name.localeCompare(b.name)),
    );
    return created;
  };

  // The set of "covered" service names for the suggestions panel is the
  // equals rules' services. We deliberately don't expand prefix/regex rules
  // against the known service list — that would duplicate backend matcher
  // logic in TypeScript. Equals coverage is the precise, safe case.
  const equalsCoverage = useMemo(
    () => rules.filter((r) => r.serviceOp === "equals" && r.service.trim()).map((r) => r.service.trim()),
    [rules],
  );

  // The focal services for suggestion panels are equals rules whose service
  // is a known service in the trace data. We dedupe so accidental duplicates
  // in the draft don't render twice.
  const knownServiceSet = useMemo(() => new Set(knownServices), [knownServices]);
  const focalServices = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const r of rules) {
      if (r.serviceOp !== "equals") continue;
      const v = r.service.trim();
      if (!v || !knownServiceSet.has(v) || seen.has(v)) continue;
      seen.add(v);
      out.push(v);
    }
    return out;
  }, [rules, knownServiceSet]);

  // Append accepted dependency-suggestion service names as new service-only
  // rules. We skip services already pinned by an equals rule so accepting a
  // name twice doesn't add it twice.
  const addDependencyMatchers = (names: string[]) => {
    setRules((curr) => {
      const existing = new Set(
        curr.filter((r) => r.serviceOp === "equals").map((r) => r.service.trim()),
      );
      const additions = names
        .filter((n) => !existing.has(n))
        .map((n) => blankRule({ serviceOp: "equals", service: n }));
      return [...curr, ...additions];
    });
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    // Required metadata blocks creation client-side (server re-checks).
    for (const f of metaFields) {
      if (f.required && !(metaValues[f.key] ?? "").trim()) {
        setError(`"${f.label}" is required.`);
        return;
      }
    }
    setSubmitting(true);
    setError(null);
    try {
      const created = await api.createIntegration({
        slug: slug.trim(),
        name: name.trim(),
        description: description.trim(),
        matchers: rulesToMatchers(rules),
      });
      // Attach any preselected tags now that we have the new id.
      // Tag attach failures are non-fatal — surface them but still
      // navigate to the detail page so the user can retry there.
      if (tagIds.length > 0) {
        const results = await Promise.allSettled(
          tagIds.map((tid) => api.attachIntegrationTag(created.integration.id, tid)),
        );
        const failed = results.filter((r) => r.status === "rejected").length;
        if (failed > 0) {
          setError(
            `Integration created, but ${failed} tag(s) failed to attach. You can re-add them from the detail page.`,
          );
        }
      }
      // Metadata rides the same non-fatal pattern as tags: the
      // integration exists either way; a failed save is finishable
      // from its Settings page.
      const metaPayload: Record<string, string> = {};
      for (const f of metaFields) metaPayload[f.key] = (metaValues[f.key] ?? "").trim();
      if (Object.values(metaPayload).some((v) => v !== "")) {
        try {
          await api.setIntegrationMetadata(created.integration.id, metaPayload);
        } catch {
          setError("Integration created, but saving metadata failed. You can finish it under Settings.");
        }
      }
      nav(`/integrations/${created.integration.id}`);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSubmitting(false);
    }
  };

  if (!allowed) {
    return (
      <div>
        <div className="page__header">
          <div>
            <p className="breadcrumb">
              <Link to="/integrations">Integrations</Link> / new
            </p>
            <h1 className="page__title">New integration</h1>
          </div>
        </div>
        <div className="placeholder">
          Your role doesn't allow creating integrations. Ask an{" "}
          <strong>integration contributor</strong> or <strong>org admin</strong> to
          set one up for you.
        </div>
      </div>
    );
  }

  return (
    <div>
      <div className="page__header">
        <div>
          <p className="breadcrumb">
            <Link to="/integrations">Integrations</Link> / new
          </p>
          <h1 className="page__title">New integration</h1>
          <p className="page__subtitle">
            Give it a name, then add one or more rules describing which services belong to it.
          </p>
          {seedService && (
            <p className="muted" style={{ fontSize: 13, marginTop: 4 }}>
              Pre-filled with a rule that matches the service{" "}
              <span className="mono">{seedService}</span>. Add more rules to include
              additional services, or change the operator below.
            </p>
          )}
        </div>
      </div>

      {error && <div className="alert alert--error">{error}</div>}

      <form className="form" onSubmit={submit}>
        <div className="form__row">
          <label className="form__label">
            Name
            <input
              className="search__input"
              required
              value={name}
              onChange={(e) => {
                const v = e.target.value;
                setName(v);
                // Mirror the name into the slug until the user takes it over.
                if (!slugEdited) setSlug(slugify(v));
              }}
              placeholder="Order Sync"
            />
            <span className="form__hint">Human-readable display name shown across the app.</span>
          </label>
          <label className="form__label">
            Slug
            <input
              className="search__input"
              required
              pattern="[a-z0-9-]+"
              value={slug}
              onChange={(e) => {
                setSlug(e.target.value);
                setSlugEdited(true);
              }}
              placeholder="order-sync"
            />
            <span className="form__hint">URL-safe identifier, lowercase letters / digits / dashes.</span>
          </label>
        </div>

        <label className="form__label">
          Description
          <textarea
            className="svc-textarea"
            rows={3}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="End-to-end order processing pipeline."
          />
        </label>

        <div className="form__section">
          <div className="form__section-title">Tags</div>
          <p className="muted form__hint">
            Group integrations along axes the matchers don't capture —
            department, environment, owning team. Pick existing tags or
            type a new name to create one.
          </p>
          <TagPicker
            available={allTags}
            selectedIds={tagIds}
            onChange={setTagIds}
            onCreate={createTag}
          />
        </div>

        {metaFields.length > 0 && (
          <div className="form__section">
            <div className="form__section-title">Metadata</div>
            <p className="muted form__hint">
              The org-defined fields that apply to integrations (managed under{" "}
              <Link to="/metadata-fields">Metadata fields</Link>). Required ones
              must be filled before the integration can be created.
            </p>
            <div style={{ display: "flex", flexDirection: "column", gap: 10, maxWidth: 480 }}>
              {metaFields.map((f) => (
                <FieldInput
                  key={f.id}
                  field={f}
                  value={metaValues[f.key] ?? ""}
                  onChange={(v) => setMetaValues((cur) => ({ ...cur, [f.key]: v }))}
                />
              ))}
            </div>
          </div>
        )}

        <div className="form__section">
          <div className="form__section-title">Rules</div>
          <p className="muted form__hint">
            The integration matches the <strong>union</strong> of its rules. Each rule
            pins a <strong>service</strong> and can add attribute conditions scoped to
            that service — e.g. <span className="mono">service is order-gateway</span>{" "}
            where it matches <span className="mono">producer = ttf</span> OR{" "}
            <span className="mono">consumer = dcd</span>. A rule with no conditions
            includes all of that service's traffic.
          </p>
          <MatcherRules
            rules={rules}
            onChange={setRules}
            knownServices={knownServices}
            attrKeys={attrKeys}
          />
        </div>

        {focalServices.length > 0 && (
          <div className="form__section">
            <div className="form__section-title">Suggested dependencies</div>
            <p className="muted form__hint">
              Based on traces, these services are directly involved in flows
              with the ones you've pinned with <span className="mono">is</span>.
              Add the ones that should be part of this integration —
              they'll each become their own service rule.
            </p>
            <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
              {focalServices.map((focal) => (
                <ServiceDependencySuggestions
                  key={focal}
                  serviceName={focal}
                  window={windowVal}
                  alreadyCovered={equalsCoverage}
                  onAdd={addDependencyMatchers}
                  addButtonLabel="Include in this integration"
                />
              ))}
            </div>
          </div>
        )}

        <div className="form__actions">
          <Link className="btn" to="/integrations">
            Cancel
          </Link>
          <button className="btn btn--primary" type="submit" disabled={submitting}>
            {submitting ? "Creating…" : "Create integration"}
          </button>
        </div>
      </form>
    </div>
  );
}
