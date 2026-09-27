// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// MembershipEditor - what belongs to an integration, on both the create
// page and the integration's Settings tab.
//
// It is built around the question people actually ask while they edit
// this: did I get all of it, and nothing extra? So the whole integration
// is previewed at the top, every member says what it takes in words and
// how much traffic that is, a pattern says which services it matches
// right now, and anything that matches nothing is counted as needing a
// look rather than left for somebody to discover after saving.
//
// The common member is a whole service, so that is one compact row and
// one line in the add box. Conditions are how a member is narrowed, and
// they open inside its row. How members combine across a trace is rare
// and easy to misread, so it lives under Advanced.
//
// Nothing here changes what gets stored: members are the same Rules the
// MatcherRules translation turns into matcher rows, and saving belongs
// to the page around this.

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api } from "../../api/client";
import type { NeighborsResponse, RuleMatch, ServiceSummary } from "../../api/types";
import { formatNumber } from "../../lib/format";
import { RuleEditor, rulesToMatchers, type Rule, type RulePreview } from "../MatcherRules";
import {
  conditionsPhrase,
  completeConds,
  mergeSuggestions,
  OR_UNAVAILABLE,
  orConflict,
  patternWords,
  parseAddQuery,
  ruleCovers,
  ruleKey,
  rulesCover,
  windowPhrase,
  windowShort,
} from "./membership";

const SERVICE_NAME_ATTR = "service.name";

// What a member row needs to be, beyond the rule: what the preview said.
type Look = { tone: "ok" | "err" | "muted" | "warn"; note?: string };

const previewSig = (rules: Rule[], combine: RuleMatch) => JSON.stringify([rulesToMatchers(rules), combine]);

// Typing is not a question. The pause is what asks.
const PREVIEW_DEBOUNCE_MS = 450;

export default function MembershipEditor({
  rules,
  onChange,
  combine,
  onCombineChange,
  windowVal,
  readOnly = false,
}: {
  rules: Rule[];
  onChange: (rules: Rule[]) => void;
  combine: RuleMatch;
  onCombineChange: (mode: RuleMatch) => void;
  /** The range every count on this editor is taken over. */
  windowVal: string;
  /** Shown as it is: no add box, no suggestions, nothing opens. */
  readOnly?: boolean;
}) {
  const [services, setServices] = useState<ServiceSummary[]>([]);
  const [attrKeys, setAttrKeys] = useState<string[]>([]);
  useEffect(() => {
    api
      .listServices(windowVal)
      .then((r) => setServices(r.services ?? []))
      .catch(() => setServices([]));
    api
      .messageFields(windowVal)
      .then((r) =>
        setAttrKeys(
          (r.fields.find((f) => f.field === "payload")?.attributeKeys ?? [])
            .map((k) => k.key)
            .filter((k) => k !== SERVICE_NAME_ATTR),
        ),
      )
      .catch(() => setAttrKeys([]));
  }, [windowVal]);
  const byName = useMemo(() => new Map(services.map((s) => [s.service_name, s])), [services]);
  const knownNames = useMemo(() => services.map((s) => s.service_name).sort(), [services]);

  // ── Previews ─────────────────────────────────────────────────────
  // One per member, keyed by what the member would store, so a redraw
  // that changes nothing does not ask again and undoing an edit reuses
  // the answer it already had. Plus one for the whole integration.
  const [previews, setPreviews] = useState<Map<string, RulePreview>>(new Map());
  const asked = useRef<Set<string>>(new Set());
  useEffect(() => {
    setPreviews(new Map());
    asked.current = new Set();
  }, [windowVal]);
  const memberSigs = rules.map((r) => previewSig([r], "any"));
  const sigKey = memberSigs.join("|");
  useEffect(() => {
    const t = window.setTimeout(() => {
      rules.forEach((r, i) => {
        const sig = memberSigs[i];
        if (asked.current.has(sig)) return;
        asked.current.add(sig);
        const matchers = rulesToMatchers([r]);
        if (matchers.length === 0) {
          setPreviews((m) => new Map(m).set(sig, { incomplete: true }));
          return;
        }
        api
          .previewIntegrationRules({ matchers, rule_match: "any" }, windowVal)
          .then((p) => setPreviews((m) => new Map(m).set(sig, p)))
          .catch(() => asked.current.delete(sig));
      });
    }, PREVIEW_DEBOUNCE_MS);
    return () => window.clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sigKey, windowVal]);

  const [total, setTotal] = useState<RulePreview | null>(null);
  const totalSig = previewSig(rules, combine);
  useEffect(() => {
    const matchers = rulesToMatchers(rules);
    if (matchers.length === 0) {
      setTotal(null);
      return;
    }
    let cancelled = false;
    const t = window.setTimeout(() => {
      api
        .previewIntegrationRules({ matchers, rule_match: combine }, windowVal)
        .then((p) => !cancelled && setTotal(p))
        .catch(() => !cancelled && setTotal(null));
    }, PREVIEW_DEBOUNCE_MS);
    return () => {
      cancelled = true;
      window.clearTimeout(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [totalSig, windowVal]);

  const when = windowPhrase(windowVal);

  // How a member looks, and what it needs said about it. These are the
  // mistakes that used to cost a save and a round trip: a misspelled
  // attribute, a pattern that matches nothing, a service that has gone
  // quiet.
  const lookOf = (rule: Rule, p: RulePreview | undefined): Look => {
    const name = rule.service.trim();
    if (!name) {
      return { tone: "warn", note: "No service: this condition selects nothing on its own, and saving drops it." };
    }
    // Stored before the editor kept "or" out of this mode. Said first,
    // since it is about what the member means, not about its traffic.
    if (orConflict(rule, combine)) {
      return {
        tone: "warn",
        note: 'Its conditions are joined with "or", which this mode reads as every one of them being required. Switch them to "and", or keep one.',
      };
    }
    if (rule.serviceOp === "equals") {
      const svc = byName.get(name);
      if (!svc) {
        return { tone: "warn", note: `Not seen ${when}. Kept, since a quiet service can still belong here.` };
      }
      if (p && !p.incomplete && completeConds(rule).length > 0 && (p.trace_count ?? 0) === 0) {
        return { tone: "warn", note: `No traffic from ${name} meets these conditions ${when}. Check the attribute and value.` };
      }
      const s = svc.status;
      return { tone: s === "ok" ? "ok" : s === "errors" || s === "unhealthy" ? "err" : "muted" };
    }
    if (p && !p.incomplete && (p.service_count ?? 0) === 0) {
      return { tone: "warn", note: `Matches no service ${when}.` };
    }
    return { tone: "muted" };
  };
  const looks = rules.map((r, i) => lookOf(r, previews.get(memberSigs[i])));
  const needsLook = looks.filter((l) => l.note).length;

  // ── Editing ──────────────────────────────────────────────────────
  // One member open at a time: its conditions are the only thing on the
  // screen that needs the room.
  const [open, setOpen] = useState<number | null>(null);
  const replace = (i: number, r: Rule) => onChange(rules.map((x, idx) => (idx === i ? r : x)));
  const remove = (i: number) => {
    onChange(rules.filter((_, idx) => idx !== i));
    setOpen((o) => (o === null || o === i ? null : o > i ? o - 1 : o));
  };
  const has = (r: Rule) => rules.some((x) => ruleKey(x) === ruleKey(r));
  const add = (r: Rule) => {
    if (!has(r)) onChange([...rules, r]);
  };

  return (
    <div>
      <p className="muted" style={{ fontSize: 13, margin: "0 0 12px", lineHeight: 1.5 }}>
        {combine === "all"
          ? "A message belongs here only when one trace passed through every service below."
          : "A message belongs here when any service below handled it."}
      </p>

      {rules.length > 0 && (
        <div
          role="group"
          aria-label="What this integration matches"
          style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(130px, 1fr))", gap: 8, marginBottom: 12 }}
        >
          <Tile label="Services" value={total ? formatNumber(total.service_count ?? 0) : "…"} />
          <Tile label={`Messages, ${windowShort(windowVal)}`} value={total?.trace_count != null ? formatNumber(total.trace_count) : "…"} />
          <Tile
            label="Failing"
            value={
              total?.trace_count
                ? `${(((total.error_trace_count ?? 0) / total.trace_count) * 100).toFixed(1)}%`
                : total
                  ? "-"
                  : "…"
            }
          />
          <Tile label="Needs a look" value={String(needsLook)} warn={needsLook > 0} />
        </div>
      )}

      {rules.length === 0 ? (
        <div className="muted" style={{ fontSize: 13, padding: "12px 0" }}>
          {readOnly
            ? "Nothing belongs to this integration yet."
            : "Add the services that make up this integration. Type a name, or a pattern like order-* to take in a family of services."}
        </div>
      ) : (
        <ul
          aria-label="Services"
          style={{ listStyle: "none", margin: 0, padding: 0, border: "1px solid var(--border)", borderRadius: 8 }}
        >
          {rules.map((rule, i) => {
            const p = previews.get(memberSigs[i]);
            const look = looks[i];
            const value = rule.service.trim();
            const isPattern = rule.serviceOp !== "equals";
            const display = value || "any service";
            const isOpen = open === i;
            const matched = isPattern ? p?.services ?? null : null;
            return (
              <li
                key={i}
                style={{
                  borderTop: i === 0 ? "none" : "1px solid var(--border)",
                  background: isOpen ? "var(--bg)" : undefined,
                }}
              >
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "14px minmax(0, 1fr) auto auto",
                    gap: 10,
                    alignItems: "start",
                    padding: "10px 12px",
                  }}
                >
                  <Dot tone={look.tone} pattern={rule.serviceOp !== "equals"} />
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontSize: 13.5 }}>
                      {isPattern && value && <span className="muted">{patternWords(rule.serviceOp)} </span>}
                      <span className="mono">{display}</span>
                    </div>
                    <div className="muted" style={{ fontSize: 12, marginTop: 2 }}>
                      {conditionsPhrase(rule)}
                    </div>
                    {matched && matched.length > 0 && (
                      <div className="muted" style={{ fontSize: 12, marginTop: 2 }}>
                        Matches {matched.length} now: <span className="mono">{matched.slice(0, 4).join(", ")}</span>
                        {matched.length > 4 && ` and ${matched.length - 4} more`}
                      </div>
                    )}
                    {look.note && (
                      <div style={{ fontSize: 12, marginTop: 2, color: "var(--warn-ink, var(--ink-2))" }}>{look.note}</div>
                    )}
                  </div>
                  <span className="muted" style={{ fontSize: 12, whiteSpace: "nowrap", paddingTop: 2 }}>
                    {p?.trace_count != null ? `${formatNumber(p.trace_count)} msgs` : p ? "" : "…"}
                  </span>
                  <span style={{ display: "flex", gap: 2 }}>
                    {!readOnly && (
                      <>
                        <button
                          type="button"
                          className="btn btn--link"
                          aria-expanded={isOpen}
                          aria-label={`${isOpen ? "Close" : "Edit"} ${display}`}
                          onClick={() => setOpen(isOpen ? null : i)}
                          style={{ fontSize: 12 }}
                        >
                          {isOpen ? "Done" : "Edit"}
                        </button>
                        <button
                          type="button"
                          className="btn btn--link"
                          aria-label={`Remove ${display}`}
                          onClick={() => remove(i)}
                        >
                          ✕
                        </button>
                      </>
                    )}
                  </span>
                </div>
                {isOpen && !readOnly && (
                  <div style={{ padding: "0 12px 12px 36px" }}>
                    <RuleEditor
                      rule={rule}
                      onChange={(r) => replace(i, r)}
                      knownServices={knownNames}
                      attrKeys={attrKeys}
                      orUnavailable={combine === "all" ? OR_UNAVAILABLE : undefined}
                    />
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {!readOnly && (
        <AddBox
          rules={rules}
          knownNames={knownNames}
          when={when}
          onAdd={add}
        />
      )}

      {!readOnly && (
        <Suggestions rules={rules} byName={byName} windowVal={windowVal} onAdd={(n) => add(ruleOf(n))} />
      )}

      <Advanced
        combine={combine}
        onChange={onCombineChange}
        readOnly={readOnly}
        blockers={rules.filter((r) => orConflict(r, "all")).map((r) => r.service.trim() || "a condition with no service")}
      />
    </div>
  );
}

const ruleOf = (name: string): Rule => ({ serviceOp: "equals", service: name, combine: "any", attrs: [] });

function Tile({ label, value, warn = false }: { label: string; value: string; warn?: boolean }) {
  return (
    <div
      style={{
        background: warn ? "var(--warn-soft, var(--bg))" : "var(--bg)",
        borderRadius: 8,
        padding: "8px 10px",
      }}
    >
      <div style={{ fontSize: 11.5, color: warn ? "var(--warn-ink, var(--muted))" : "var(--muted)" }}>{label}</div>
      <div style={{ fontSize: 18, fontWeight: 600, color: warn ? "var(--warn-ink, var(--ink))" : "var(--ink)" }}>{value}</div>
    </div>
  );
}

function Dot({ tone, pattern }: { tone: Look["tone"]; pattern: boolean }) {
  const color = { ok: "var(--ok)", err: "var(--err)", warn: "var(--warn)", muted: "var(--muted)" }[tone];
  // A pattern is a family, not a service with a health of its own, so
  // it gets a mark rather than a status.
  if (pattern && tone !== "warn") {
    return (
      <span aria-hidden className="mono" style={{ color: "var(--muted)", fontSize: 13, lineHeight: "20px" }}>
        *
      </span>
    );
  }
  return (
    <span
      aria-hidden
      style={{ width: 8, height: 8, borderRadius: "50%", background: color, display: "inline-block", marginTop: 6 }}
    />
  );
}

/**
 * The one way in. A name adds that service; a star adds a pattern and
 * says how many services it takes in now; a name the cell has not seen
 * is still allowed, because the quiet nightly job is exactly the service
 * people need to add by hand. The list stays open after a pick, since
 * nobody adds just one.
 */
function AddBox({
  rules,
  knownNames,
  when,
  onAdd,
}: {
  rules: Rule[];
  knownNames: string[];
  when: string;
  onAdd: (r: Rule) => void;
}) {
  const [q, setQ] = useState("");
  const [focused, setFocused] = useState(false);
  const [active, setActive] = useState(0);
  const listId = "membership-add-options";

  const options = useMemo(() => {
    const out: { key: string; label: ReactNode; hint?: string; rule: Rule | null }[] = [];
    const needle = q.trim().toLowerCase();
    const parsed = parseAddQuery(q);
    const taken = (r: Rule) => rules.some((x) => ruleKey(x) === ruleKey(r));
    if (parsed && parsed.serviceOp !== "equals") {
      const n = knownNames.filter((s) => ruleCovers(parsed, s)).length;
      out.push({
        key: "pattern",
        label: (
          <>
            Add {patternWords(parsed.serviceOp)} <span className="mono">{parsed.service}</span>
          </>
        ),
        hint: taken(parsed) ? "already added" : `matches ${n} now`,
        rule: taken(parsed) ? null : parsed,
      });
    }
    if (parsed && parsed.serviceOp === "equals") {
      // Said, rather than the list going quiet: typing a member's name
      // and getting nothing back reads as "that service does not exist".
      if (taken(parsed)) {
        out.push({
          key: "taken",
          label: <span className="mono">{parsed.service}</span>,
          hint: "already added",
          rule: null,
        });
      } else if (!knownNames.includes(parsed.service)) {
        out.push({
          key: "unseen",
          label: (
            <>
              Add <span className="mono">{parsed.service}</span>
            </>
          ),
          hint: `not seen ${when}`,
          rule: parsed,
        });
      }
    }
    // What was typed comes first, then what it partly matches. Enter
    // takes the first option, and adding exactly the name typed is the
    // safe reading: a literal the cell has not seen says so on its row,
    // while a longer name that merely contains it - order-gateway for
    // "order" - would be added silently, traffic and all. An exact known
    // name heads the partial matches, so "gateway" is never beaten by
    // the api-gateway that sorts ahead of it.
    const exact = q.trim();
    const names = knownNames
      .filter((s) => !rulesCover(rules, s) && (!needle || s.toLowerCase().includes(needle)))
      .sort((x, y) => Number(y === exact) - Number(x === exact))
      .slice(0, 8);
    for (const s of names) out.push({ key: s, label: <span className="mono">{s}</span>, rule: ruleOf(s) });
    return out;
  }, [q, rules, knownNames, when]);

  const show = focused && options.length > 0;
  const pick = (i: number) => {
    const o = options[i];
    if (!o?.rule) return;
    onAdd(o.rule);
    setQ("");
    setActive(0);
  };

  return (
    <div style={{ position: "relative", marginTop: 10 }}>
      <input
        className="search__input"
        role="combobox"
        aria-label="Add a service or pattern"
        aria-expanded={show}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={show ? `${listId}-${active}` : undefined}
        placeholder="Add a service, or a pattern like billing-*"
        value={q}
        onChange={(e) => {
          setQ(e.target.value);
          setActive(0);
        }}
        onFocus={() => setFocused(true)}
        // After the click on an option has landed, not before it.
        onBlur={() => window.setTimeout(() => setFocused(false), 120)}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown") {
            e.preventDefault();
            setActive((a) => Math.min(a + 1, options.length - 1));
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setActive((a) => Math.max(a - 1, 0));
          } else if (e.key === "Enter") {
            // Never the form's submit: on the create page this box sits
            // inside the form, and Enter here means "add this".
            e.preventDefault();
            pick(active);
          } else if (e.key === "Escape") {
            setFocused(false);
          }
        }}
        style={{ width: "100%" }}
      />
      {show && (
        <ul
          id={listId}
          role="listbox"
          aria-label="Services and patterns to add"
          style={{
            position: "absolute",
            zIndex: 20,
            left: 0,
            right: 0,
            top: "calc(100% + 4px)",
            margin: 0,
            padding: 4,
            listStyle: "none",
            background: "var(--surface-elevated, var(--surface-2))",
            border: "1px solid var(--border)",
            borderRadius: 8,
            boxShadow: "0 8px 24px rgba(0,0,0,0.12)",
            maxHeight: 280,
            overflowY: "auto",
          }}
        >
          {options.map((o, i) => (
            <li
              key={o.key}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              aria-disabled={!o.rule || undefined}
              onMouseDown={(e) => {
                e.preventDefault();
                pick(i);
              }}
              onMouseEnter={() => setActive(i)}
              style={{
                display: "flex",
                justifyContent: "space-between",
                gap: 12,
                padding: "6px 8px",
                borderRadius: 6,
                fontSize: 13,
                cursor: o.rule ? "pointer" : "default",
                background: i === active ? "var(--primary-soft)" : undefined,
                color: o.rule ? "var(--ink)" : "var(--muted)",
              }}
            >
              <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>{o.label}</span>
              {o.hint && (
                <span className="muted" style={{ fontSize: 12, whiteSpace: "nowrap" }}>
                  {o.hint}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

const SUGGESTIONS_SHOWN = 6;

/**
 * One strip of what traces connect to the members, instead of one
 * panel per member. A click adds the service; nothing to select and
 * then confirm.
 */
function Suggestions({
  rules,
  byName,
  windowVal,
  onAdd,
}: {
  rules: Rule[];
  byName: Map<string, ServiceSummary>;
  windowVal: string;
  onAdd: (name: string) => void;
}) {
  const focals = useMemo(() => {
    const seen = new Set<string>();
    for (const r of rules) {
      const v = r.service.trim();
      if (r.serviceOp === "equals" && v && byName.has(v)) seen.add(v);
    }
    return [...seen].sort();
  }, [rules, byName]);

  // Keyed by range AND service: a neighbourhood is an answer about a
  // range, and a new range has to ask again rather than reuse it.
  const [hoods, setHoods] = useState<Map<string, NeighborsResponse>>(new Map());
  const asked = useRef<Set<string>>(new Set());
  const hoodKey = (f: string) => `${windowVal}\u0000${f}`;
  const focalKey = focals.join("|");
  useEffect(() => {
    for (const f of focals) {
      const k = hoodKey(f);
      if (asked.current.has(k)) continue;
      asked.current.add(k);
      api
        .serviceNeighbors(f, windowVal)
        .then((d) => setHoods((m) => new Map(m).set(k, d)))
        .catch(() => asked.current.delete(k));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focalKey, windowVal]);

  const [all, setAll] = useState(false);
  const list = useMemo(
    () =>
      mergeSuggestions(
        focals.filter((f) => hoods.has(hoodKey(f))).map((f) => ({ focal: f, data: hoods.get(hoodKey(f))! })),
        (n) => rulesCover(rules, n),
      ),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [focals, hoods, rules, windowVal],
  );
  if (list.length === 0) return null;
  const shown = all ? list : list.slice(0, SUGGESTIONS_SHOWN);

  return (
    <div style={{ marginTop: 16 }}>
      <div className="muted" style={{ fontSize: 12, marginBottom: 6 }}>
        Traces connect these to your services
      </div>
      <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
        {shown.map((s) => (
          <button
            key={s.name}
            type="button"
            className="rounded-full border px-3 py-1 text-xs"
            style={{ borderColor: "var(--border)", background: "transparent", color: "var(--ink)" }}
            aria-label={`Add ${s.name}, ${s.reason}`}
            title={`${formatNumber(s.traffic)} traces connect it`}
            onClick={() => onAdd(s.name)}
          >
            + <span className="mono">{s.name}</span> <span className="muted">{s.reason}</span>
          </button>
        ))}
        {list.length > SUGGESTIONS_SHOWN && (
          <button type="button" className="btn btn--link" style={{ fontSize: 12 }} onClick={() => setAll((a) => !a)}>
            {all ? "Show fewer" : `Show ${list.length - SUGGESTIONS_SHOWN} more`}
          </button>
        )}
      </div>
    </div>
  );
}

const MODES: { value: RuleMatch; label: string; hint: string }[] = [
  {
    value: "any",
    label: "Any service",
    hint: "A message belongs here when any of the services handled it. Right for almost every integration.",
  },
  {
    value: "all",
    label: "One trace through every service",
    hint: "A message belongs here only when a single trace passed through every service. For a flow that is only complete once it has crossed all of them.",
  },
];

/**
 * How members combine. Rare, and easy to misread, so it is folded away -
 * but never while it is set to the unusual answer: a setting that
 * changes what the whole integration means is not one to hide.
 */
function Advanced({
  combine,
  onChange,
  readOnly,
  blockers,
}: {
  combine: RuleMatch;
  onChange: (m: RuleMatch) => void;
  readOnly: boolean;
  /** Members whose "or" the stricter mode would misread. While there
   *  are any, switching to it is refused, with the names. */
  blockers: string[];
}) {
  const [open, setOpen] = useState(combine === "all");
  useEffect(() => {
    if (combine === "all") setOpen(true);
  }, [combine]);
  const current = MODES.find((m) => m.value === combine) ?? MODES[0];

  if (readOnly) {
    return combine === "all" ? (
      <p className="muted" style={{ fontSize: 12, marginTop: 14 }}>
        Matching: {current.label.toLowerCase()}.
      </p>
    ) : null;
  }

  return (
    <div style={{ marginTop: 16, borderTop: "1px solid var(--border)", paddingTop: 10 }}>
      <button
        type="button"
        className="btn btn--link"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        style={{ fontSize: 12.5, padding: 0 }}
      >
        {open ? "▾" : "▸"} Advanced matching
        {!open && <span className="muted"> · {current.label.toLowerCase()}</span>}
      </button>
      {open && (
        <div role="radiogroup" aria-label="How the services combine" style={{ display: "grid", gap: 8, marginTop: 10 }}>
          {MODES.map((m) => {
            // Only ever blocks moving INTO the stricter mode. An
            // integration already stored that way keeps it, and its
            // offending members say so on their rows.
            const blocked = m.value === "all" && combine !== "all" && blockers.length > 0;
            return (
              <label
                key={m.value}
                style={{ display: "flex", gap: 8, alignItems: "flex-start", cursor: blocked ? "default" : "pointer" }}
              >
                <input
                  type="radio"
                  name="membership-combine"
                  checked={combine === m.value}
                  disabled={blocked}
                  onChange={() => onChange(m.value)}
                  style={{ marginTop: 3 }}
                />
                <span>
                  <span style={{ fontSize: 13, fontWeight: combine === m.value ? 600 : 400 }}>{m.label}</span>
                  <span className="muted" style={{ display: "block", fontSize: 12, lineHeight: 1.45 }}>
                    {m.hint}
                  </span>
                  {blocked && (
                    <span style={{ display: "block", fontSize: 12, lineHeight: 1.45, color: "var(--warn-ink, var(--ink-2))" }}>
                      Not available while {blockers.join(", ")} {blockers.length === 1 ? "joins its" : "join their"}{" "}
                      conditions with "or": this mode would make every alternative required. Switch{" "}
                      {blockers.length === 1 ? "it" : "them"} to "and" first.
                    </span>
                  )}
                </span>
              </label>
            );
          })}
        </div>
      )}
    </div>
  );
}
