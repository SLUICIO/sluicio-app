// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// FacetMappingsEditor is the minimal CRUD UI for the service's
// user-defined facet attribute mappings. Each row says "when this
// attribute satisfies this condition, treat the span as carrying
// io.kind=X, io.role=Y" — the backend then runs facet classification
// and widget filtering using the derived (kind, role) instead of the
// raw SpanAttributes lookups. So services that don't emit io.kind /
// io.role can still land on the right facet dashboards without being
// re-instrumented.
//
// The UI is intentionally bare-bones for the first cut: list +
// inline add + per-row delete, no validation feedback beyond what
// the server returns, no preview of which spans the rule would
// match. Those land in a follow-up; the underlying rule engine
// supports them already.

import { FormEvent, useEffect, useState } from "react";
import { api } from "../api/client";
import type {
  CreateFacetMappingRequest,
  FacetMapping,
  FacetMappingAttributeSource,
  FacetMappingIOKind,
  FacetMappingIORole,
  FacetMappingOperator,
} from "../api/types";

interface Props {
  serviceName: string;
  // Called after a successful create / delete so the parent can
  // refresh widgets — adding or removing a rule changes the
  // facet membership and widget filtering for the service.
  onChanged?: () => void;
}

const SOURCES: { value: FacetMappingAttributeSource; label: string }[] = [
  { value: "span", label: "Span attribute" },
  { value: "resource", label: "Resource attribute" },
];

const OPERATORS: { value: FacetMappingOperator; label: string }[] = [
  { value: "equals", label: "equals" },
  { value: "prefix", label: "starts with" },
  { value: "suffix", label: "ends with" },
  { value: "contains", label: "contains" },
  { value: "exists", label: "is present" },
];

const IO_KINDS: FacetMappingIOKind[] = ["file", "queue", "stream", "http", "db", "email"];
const IO_ROLES: FacetMappingIORole[] = ["input", "output"];

const EMPTY_DRAFT: CreateFacetMappingRequest = {
  attribute_source: "span",
  attribute_key: "",
  match_operator: "equals",
  match_value: "",
  set_io_kind: "file",
  set_io_role: "input",
};

export default function FacetMappingsEditor({ serviceName, onChanged }: Props) {
  const [rows, setRows] = useState<FacetMapping[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [draft, setDraft] = useState<CreateFacetMappingRequest>(EMPTY_DRAFT);
  const [submitting, setSubmitting] = useState(false);

  const refresh = () => {
    setLoading(true);
    setError(null);
    api
      .listFacetMappings(serviceName)
      .then((r) => setRows(r.mappings ?? []))
      .catch((e) => setError(String((e as Error).message ?? e)))
      .finally(() => setLoading(false));
  };

  useEffect(refresh, [serviceName]);

  // For exists we don't take a value — clear it so the server doesn't
  // get a stale string left over from when the operator was equals.
  const draftMatchValue =
    draft.match_operator === "exists" ? "" : draft.match_value;

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!draft.attribute_key.trim()) return;
    if (draft.match_operator !== "exists" && !draft.match_value.trim()) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.createFacetMapping(serviceName, {
        ...draft,
        attribute_key: draft.attribute_key.trim(),
        match_value: draftMatchValue.trim(),
      });
      setDraft(EMPTY_DRAFT);
      refresh();
      onChanged?.();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSubmitting(false);
    }
  };

  const onDelete = async (id: string) => {
    if (!confirm("Delete this mapping?")) return;
    setError(null);
    try {
      await api.deleteFacetMapping(serviceName, id);
      refresh();
      onChanged?.();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="card__header">
        Facet classification rules
        <span
          className="muted"
          style={{ marginLeft: 8, fontWeight: 400, fontSize: 13 }}
        >
          · override io.kind / io.role detection
        </span>
      </div>
      <div style={{ padding: "12px 16px" }}>
        <p className="muted" style={{ margin: "0 0 12px", fontSize: 13 }}>
          Use these rules when this service doesn't emit{" "}
          <span className="mono">io.kind</span> /{" "}
          <span className="mono">io.role</span> directly. Each rule says "for
          spans where attribute X matches Y, classify as the chosen{" "}
          (kind, role)." Rules apply in the order they were added; the raw
          span attributes always take precedence when present.
        </p>

        {error && (
          <div className="alert alert--error" style={{ marginBottom: 12 }}>
            {error}
          </div>
        )}

        {loading ? (
          <div className="muted" style={{ fontSize: 12 }}>
            Loading…
          </div>
        ) : rows.length === 0 ? (
          <div className="muted" style={{ fontSize: 12 }}>
            No rules yet for this service.
          </div>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>When attribute</th>
                <th>Match</th>
                <th>Treat as</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {rows.map((m) => (
                <tr key={m.id}>
                  <td>
                    <span className="muted" style={{ fontSize: 11 }}>
                      {m.attribute_source}.
                    </span>
                    <span className="mono">{m.attribute_key}</span>
                  </td>
                  <td>
                    {m.match_operator === "exists" ? (
                      <span className="muted">is present</span>
                    ) : (
                      <>
                        <span className="muted" style={{ fontSize: 11 }}>
                          {m.match_operator}{" "}
                        </span>
                        <span className="mono">{m.match_value}</span>
                      </>
                    )}
                  </td>
                  <td>
                    <span className="mono">
                      {m.set_io_kind}:{m.set_io_role}
                    </span>
                  </td>
                  <td className="num">
                    <button
                      className="btn btn--link"
                      onClick={() => onDelete(m.id)}
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        <form
          onSubmit={onSubmit}
          style={{
            marginTop: 12,
            display: "grid",
            gridTemplateColumns: "repeat(6, auto)",
            gap: 6,
            alignItems: "center",
          }}
        >
          <select
            className="toolbar__select"
            value={draft.attribute_source}
            onChange={(e) =>
              setDraft({
                ...draft,
                attribute_source: e.target.value as FacetMappingAttributeSource,
              })
            }
          >
            {SOURCES.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <input
            className="search__input"
            placeholder="attribute key, e.g. peer.service"
            value={draft.attribute_key}
            onChange={(e) =>
              setDraft({ ...draft, attribute_key: e.target.value })
            }
            style={{ minWidth: 180 }}
          />
          <select
            className="toolbar__select"
            value={draft.match_operator}
            onChange={(e) =>
              setDraft({
                ...draft,
                match_operator: e.target.value as FacetMappingOperator,
              })
            }
          >
            {OPERATORS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <input
            className="search__input"
            placeholder={
              draft.match_operator === "exists" ? "—" : "value"
            }
            value={draftMatchValue}
            onChange={(e) =>
              setDraft({ ...draft, match_value: e.target.value })
            }
            disabled={draft.match_operator === "exists"}
            style={{ minWidth: 160 }}
          />
          <span style={{ display: "flex", gap: 4, alignItems: "center" }}>
            <span className="muted" style={{ fontSize: 12 }}>
              →
            </span>
            <select
              className="toolbar__select"
              value={draft.set_io_kind}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  set_io_kind: e.target.value as FacetMappingIOKind,
                })
              }
            >
              {IO_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
            <span className="muted">:</span>
            <select
              className="toolbar__select"
              value={draft.set_io_role}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  set_io_role: e.target.value as FacetMappingIORole,
                })
              }
            >
              {IO_ROLES.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </span>
          <button
            className="btn btn--primary"
            type="submit"
            disabled={
              submitting ||
              !draft.attribute_key.trim() ||
              (draft.match_operator !== "exists" && !draft.match_value.trim())
            }
          >
            {submitting ? "Adding…" : "Add rule"}
          </button>
        </form>
      </div>
    </div>
  );
}
