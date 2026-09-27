// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// MatcherConfig - what belongs to an existing integration, on its
// Settings tab. The editing itself is MembershipEditor, shared with the
// create page; this owns the stored state, the draft, and saving it.
//
// It lives on the Settings tab - the operational view (Overview)
// shouldn't carry admin configuration.

import { useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import { matchersToRules, rulesToMatchers, type Rule } from "./MatcherRules";
import MembershipEditor from "./matcher/MembershipEditor";
import { describeChanges } from "./matcher/membership";
import type { IntegrationDetail, RuleMatch } from "../api/types";

export default function MatcherConfig({
  integrationId,
  data,
  canWrite,
  windowVal,
  onChanged,
}: {
  integrationId: string;
  data: IntegrationDetail;
  canWrite: boolean;
  windowVal: string;
  onChanged: () => void;
}) {
  const id = integrationId;
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // Re-initialise the draft whenever the stored matcher set changes
  // (mount, and after a save → onChanged → refetch). Keyed off a
  // value-equality signature rather than the array itself: a parent
  // re-render that hands over a new-but-equal array must not blow away
  // unsaved edits.
  const matchersSig = useMemo(
    () => JSON.stringify((data.matchers ?? []).map((m) => [m.attribute, m.operator, m.value, m.match_group, !!m.include_descendants])),
    [data.matchers],
  );
  const storedMode: RuleMatch = data.integration.rule_match ?? "any";
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const stored = useMemo(() => matchersToRules(data.matchers ?? []), [matchersSig]);
  const [rules, setRules] = useState<Rule[]>(stored);
  const [combine, setCombine] = useState<RuleMatch>(storedMode);
  const discard = () => {
    setRules(stored);
    setCombine(storedMode);
    setError(null);
  };
  useEffect(discard, [stored, storedMode]);

  // The draft's differences from what is stored, in words. Derived rather
  // than tracked, so undoing an edit by hand clears it.
  const changes = useMemo(
    () => describeChanges(stored, rules, storedMode, combine),
    [stored, rules, storedMode, combine],
  );

  // save replaces the stored matcher set with the draft's DNF expansion.
  // New rows first, then the old ones go: there's no unique constraint
  // on integration_matchers, so this never conflicts, and adding first
  // avoids a window where the integration matches nothing.
  const save = async () => {
    setError(null);
    setSaving(true);
    try {
      // The mode first: if the matcher write fails halfway the rules are
      // still the ones the user is looking at, whereas a mode saved after
      // a failed write would describe rules that were never stored.
      if (combine !== storedMode) {
        await api.updateIntegration(id, {
          name: data.integration.name,
          description: data.integration.description,
          rule_match: combine,
        });
      }
      const desired = rulesToMatchers(rules);
      await Promise.all(desired.map((d) => api.addMatcher(id, d)));
      await Promise.all((data.matchers ?? []).map((m) => api.removeMatcher(id, m.id)));
      onChanged();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <section
      // No overflow-hidden: the add box and the condition pills drop
      // popovers below their triggers, and overflow-hidden on this card
      // would clip them. It would also stop the save bar sticking.
      className="rounded-lg border bg-surface-2"
      style={{ borderColor: "var(--border)" }}
    >
      <div className="border-b border-border px-4 py-3">
        <h2 className="text-base font-semibold">What belongs to this integration</h2>
      </div>
      <div className="p-4">
        {error && <div className="alert alert--error" style={{ marginBottom: 12 }}>{error}</div>}
        <MembershipEditor
          rules={rules}
          onChange={setRules}
          combine={combine}
          onCombineChange={setCombine}
          windowVal={windowVal}
          readOnly={!canWrite}
        />
        {!canWrite && (
          <p className="muted" style={{ fontSize: 12, marginTop: 12 }}>
            Your role doesn't allow editing what belongs here. Ask an{" "}
            <strong>integration contributor</strong> or <strong>org admin</strong> to make changes.
          </p>
        )}
      </div>

      {canWrite && (changes.length > 0 || saving) && (
        // Sticky, because the moment a change is made is often the moment
        // somebody is furthest from the button - down among the
        // suggestions, having just accepted one.
        <div
          role="region"
          aria-label="Unsaved changes"
          style={{
            position: "sticky",
            bottom: 0,
            zIndex: 10,
            display: "flex",
            alignItems: "center",
            gap: 10,
            flexWrap: "wrap",
            padding: "10px 16px",
            borderTop: "1px solid var(--border)",
            background: "var(--surface-2)",
            borderRadius: "0 0 8px 8px",
          }}
        >
          <span className="muted" style={{ flex: 1, minWidth: 200, fontSize: 12.5 }}>
            {changes.length === 1 ? "1 unsaved change: " : `${changes.length} unsaved changes: `}
            {changes.join("; ")}
          </span>
          <button type="button" className="btn" onClick={discard} disabled={saving}>
            Discard
          </button>
          <button type="button" className="btn btn--primary" onClick={save} disabled={saving}>
            {saving ? "Saving…" : "Save"}
          </button>
        </div>
      )}
    </section>
  );
}
