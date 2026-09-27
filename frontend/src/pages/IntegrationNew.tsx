// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { FormEvent, useEffect, useState } from "react";
import { slugify } from "../lib/slugify";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { CreateTagRequest, MetadataField, Tag } from "../api/types";
import { FieldInput } from "../components/MetadataPanel";
import { blankRule, rulesToMatchers, type Rule } from "../components/MatcherRules";
import MembershipEditor from "../components/matcher/MembershipEditor";
import type { RuleMatch } from "../api/types";

import TagPicker from "../components/tags/TagPicker";
import { useCurrentUser } from "../lib/useCurrentUser";
import { usePageTitle } from "../lib/usePageTitle";

// The create page has no range picker of its own, so its counts are
// taken over a fixed day and say so. A day is long enough to catch a
// service that only runs a few times, and short enough to answer fast.
const PREVIEW_WINDOW = "24h";

export default function IntegrationNew() {
  usePageTitle("New integration");
  const nav = useNavigate();
  const [params] = useSearchParams();
  const { can } = useCurrentUser();
  const allowed = can("integration.write");
  // When linked from a service page (?seedService=order-api) the
  // integration starts with that service as its first member.
  const seedService = params.get("seedService") ?? "";

  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  // The slug auto-fills from the name until the user edits it directly.
  const [slugEdited, setSlugEdited] = useState(false);
  const [description, setDescription] = useState("");
  const [rules, setRules] = useState<Rule[]>(
    seedService ? [blankRule({ serviceOp: "equals", service: seedService })] : [],
  );
  const [combine, setCombine] = useState<RuleMatch>("any");
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

  useEffect(() => {
    api
      .listTags()
      .then((d) => setAllTags(d.tags ?? []))
      .catch(() => setAllTags([]));
  }, []);

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
        rule_match: combine,
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
            Give it a name, then add the services that belong to it.
          </p>
          {seedService && (
            <p className="muted" style={{ fontSize: 13, marginTop: 4 }}>
              Starts with <span className="mono">{seedService}</span> as a member. Add the
              other services that belong to it below.
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
          <div className="form__section-title">What belongs to this integration</div>
          <MembershipEditor
            rules={rules}
            onChange={setRules}
            combine={combine}
            onCombineChange={setCombine}
            windowVal={PREVIEW_WINDOW}
          />
        </div>

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
