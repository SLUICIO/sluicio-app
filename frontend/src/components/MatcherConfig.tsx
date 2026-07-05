// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// MatcherConfig — the "Configuration · matchers" surface for an
// integration. Presents the integration's matchers as a list of
// per-service Rules (see MatcherRules), lets a contributor edit them, and
// on Save replaces the stored matcher set. Also surfaces trace-graph
// dependency suggestions for the rules' focal (equals-matched) services.
//
// It lives on the Settings tab — the operational view (Overview) shouldn't
// carry admin configuration. The matcher logic is self-contained here so any
// surface can mount it.

import { useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import ServiceDependencySuggestions from "./ServiceDependencySuggestions";
import MatcherRules, {
  Rule,
  blankRule,
  matchersToRules,
  rulesToMatchers,
} from "./MatcherRules";
import type { IntegrationDetail } from "../api/types";

const SERVICE_NAME_ATTR = "service.name";

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

  // Re-initialise the draft whenever the stored matcher set changes (mount,
  // and after a save → onChanged → refetch). dirty tracks unsaved edits.
  const matchersSig = useMemo(
    () => JSON.stringify((data.matchers ?? []).map((m) => [m.attribute, m.operator, m.value, m.match_group])),
    [data.matchers],
  );
  const [rules, setRules] = useState<Rule[]>(() => matchersToRules(data.matchers ?? []));
  const [dirty, setDirty] = useState(false);
  useEffect(() => {
    setRules(matchersToRules(data.matchers ?? []));
    setDirty(false);
    // Intentionally keyed off matchersSig (a value-equality signature of
    // data.matchers), not data.matchers itself: depending on the array
    // identity would re-init the draft on every parent re-render that
    // hands us a new-but-equal array, blowing away unsaved edits.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [matchersSig]);
  const onRulesChange = (r: Rule[]) => {
    setRules(r);
    setDirty(true);
  };

  const [knownServices, setKnownServices] = useState<string[]>([]);
  const [attrKeys, setAttrKeys] = useState<string[]>([]);
  useEffect(() => {
    api
      .listServices(windowVal)
      .then((r) => setKnownServices((r.services ?? []).map((s) => s.service_name).sort()))
      .catch(() => setKnownServices([]));
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

  // save replaces the stored matcher set with the draft's DNF expansion. We
  // add the new rows first, then delete the old ones — there's no unique
  // constraint on integration_matchers, so this never conflicts, and adding
  // first avoids a window where the integration matches nothing.
  const save = async () => {
    setError(null);
    setSaving(true);
    try {
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

  // Append accepted dependency suggestions as service-only rules in the
  // draft (saved together with the rest on Save), skipping services already
  // pinned by an equals rule.
  const addDependencyMatchers = (names: string[]) => {
    setRules((curr) => {
      const existing = new Set(
        curr.filter((r) => r.serviceOp === "equals").map((r) => r.service.trim()),
      );
      const additions = names
        .filter((n) => !existing.has(n))
        .map((n) => blankRule({ serviceOp: "equals", service: n }));
      if (additions.length === 0) return curr;
      setDirty(true);
      return [...curr, ...additions];
    });
  };

  // Focal services for the suggestion panels are the draft's equals rules
  // whose service is a known service in the trace data.
  const knownServiceSet = useMemo(() => new Set(knownServices), [knownServices]);
  const equalsCoverage = useMemo(
    () => rules.filter((r) => r.serviceOp === "equals" && r.service.trim()).map((r) => r.service.trim()),
    [rules],
  );
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

  // ruleCovers evaluates a candidate service against the draft's service
  // rules so a service already pinned (by any operator) is hidden from
  // suggestions even before it appears in resolved traffic.
  const ruleCovers = useMemo(() => {
    return (name: string): boolean =>
      rules.some((r) => {
        const v = r.service.trim();
        if (!v) return false;
        switch (r.serviceOp) {
          case "equals": return name === v;
          case "prefix": return name.startsWith(v);
          case "suffix": return name.endsWith(v);
          case "contains": return name.includes(v);
          case "regex":
            try { return new RegExp(v).test(name); } catch { return false; }
          default: return false;
        }
      });
  }, [rules]);

  return (
    <section
      // No overflow-hidden: the service picker (SearchableSelect) drops a
      // popover below its trigger, and overflow-hidden on this card would
      // clip the list.
      className="rounded-lg border bg-surface-2"
      style={{ borderColor: "var(--border)" }}
    >
      <div className="border-b border-border px-4 py-3">
        <h2 className="text-base font-semibold">Configuration · matchers</h2>
        <p className="text-xs text-muted mt-1">
          The integration matches the union of its rules. Each rule pins a
          service and can scope attribute conditions to it.
        </p>
      </div>
      <div className="p-4">
        {error && <div className="alert alert--error" style={{ marginBottom: 12 }}>{error}</div>}

        {canWrite ? (
          <>
            <MatcherRules
              rules={rules}
              onChange={onRulesChange}
              knownServices={knownServices}
              attrKeys={attrKeys}
            />
            <div style={{ display: "flex", gap: 8, marginTop: 12, alignItems: "center" }}>
              <button
                className="btn btn--primary"
                type="button"
                onClick={save}
                disabled={saving || !dirty}
              >
                {saving ? "Saving…" : "Save matchers"}
              </button>
              {dirty && !saving && <span className="muted" style={{ fontSize: 12 }}>Unsaved changes</span>}
            </div>
          </>
        ) : rules.length === 0 ? (
          <div className="placeholder">No matchers configured.</div>
        ) : (
          <>
            <MatcherRules
              rules={rules}
              onChange={() => {}}
              knownServices={knownServices}
              attrKeys={attrKeys}
            />
            <p className="muted" style={{ fontSize: 12, marginTop: 12 }}>
              Your role doesn't allow editing matchers. Ask an{" "}
              <strong>integration contributor</strong> or <strong>org admin</strong>{" "}
              to make changes.
            </p>
          </>
        )}

        {canWrite && focalServices.length > 0 && (
          <div style={{ marginTop: 16, display: "flex", flexDirection: "column", gap: 10 }}>
            <div className="muted" style={{ fontSize: 12 }}>
              Trace data suggests these services may belong here too — pick the
              ones that should join this integration:
            </div>
            {focalServices.map((focal) => (
              <ServiceDependencySuggestions
                key={focal}
                serviceName={focal}
                window={windowVal}
                alreadyCovered={equalsCoverage}
                covers={ruleCovers}
                onAdd={addDependencyMatchers}
                addButtonLabel="Add to integration"
              />
            ))}
          </div>
        )}
      </div>
    </section>
  );
}
