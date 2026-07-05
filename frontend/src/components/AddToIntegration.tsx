// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import type { Integration } from "../api/types";
import SearchableSelect from "./SearchableSelect";

interface Props {
  serviceName: string;
  // IDs the service already belongs to — these are hidden from the
  // picker so a service can't be added to the same integration twice.
  currentIntegrationIds: string[];
  // Called after a successful add/create so the parent page re-fetches
  // (the new integration badge appears in the list). Wire this — without
  // it the connection saves but the page won't reflect it until reload.
  onAdded?: () => void;
}

// slugify turns an integration name into a URL-safe slug for creation.
function slugify(name: string): string {
  return name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

/**
 * AddToIntegration is the inline "this service should belong here"
 * control on ServiceDetail. Two modes:
 *   - Pick: add to an existing integration → POST a matcher
 *     (attribute=service.name, operator=equals, value=<service>).
 *   - Create: make a brand-new integration with this service already
 *     attached (one createIntegration call carrying the seed matcher),
 *     without leaving the page.
 */
export default function AddToIntegration({
  serviceName,
  currentIntegrationIds,
  onAdded,
}: Props) {
  const [available, setAvailable] = useState<Integration[]>([]);
  const [selected, setSelected] = useState("");
  const [mode, setMode] = useState<"pick" | "create">("pick");
  const [newName, setNewName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Stable primitive key for the current-membership set, so the effect
  // re-runs on actual membership change rather than on every new array
  // identity the parent hands down (UUIDs never contain commas).
  const currentKey = currentIntegrationIds.join(",");

  // id → name lookup so the typeahead shows + filters on the integration name
  // while the selected value stays the id.
  const nameById = useMemo(() => {
    const m = new Map(available.map((i) => [i.id, i.name]));
    return (id: string) => m.get(id) ?? id;
  }, [available]);

  const refresh = useCallback(() => {
    const currentSet = new Set(currentKey ? currentKey.split(",") : []);
    api
      .listIntegrations()
      .then((r) => {
        setAvailable((r.integrations ?? []).filter((i) => !currentSet.has(i.id)));
        setSelected("");
      })
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, [currentKey]);

  // Refresh whenever the parent's notion of "current" changes.
  useEffect(() => {
    refresh();
  }, [refresh]);

  // Add to an existing integration.
  const addExisting = async () => {
    if (!selected) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.addMatcher(selected, {
        attribute: "service.name",
        operator: "equals",
        value: serviceName,
      });
      onAdded?.();
      refresh();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSubmitting(false);
    }
  };

  // Create a new integration with this service already attached.
  const createNew = async () => {
    const name = newName.trim();
    if (!name) return;
    const slug = slugify(name);
    if (!slug) {
      setError("Enter a name with at least one letter or number.");
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await api.createIntegration({
        slug,
        name,
        description: "",
        matchers: [{ attribute: "service.name", operator: "equals", value: serviceName }],
      });
      setNewName("");
      setMode("pick");
      onAdded?.();
      refresh();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <span className="add-to-integration">
      {mode === "pick" ? (
        <>
          <SearchableSelect
            value={selected}
            onChange={setSelected}
            options={available.map((i) => i.id)}
            labelFor={nameById}
            allLabel="+ Add to integration…"
            placeholder="Search integrations…"
          />
          <button
            type="button"
            className="btn"
            onClick={addExisting}
            disabled={!selected || submitting}
          >
            {submitting ? "Adding…" : "Add"}
          </button>
          <button
            type="button"
            className="btn btn--link"
            onClick={() => {
              setMode("create");
              setError(null);
            }}
          >
            or create new
          </button>
        </>
      ) : (
        <>
          <input
            className="toolbar__select"
            style={{ minWidth: 180 }}
            value={newName}
            autoFocus
            placeholder="New integration name…"
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") createNew();
            }}
          />
          <button
            type="button"
            className="btn btn--primary"
            onClick={createNew}
            disabled={!newName.trim() || submitting}
          >
            {submitting ? "Creating…" : "Create & add"}
          </button>
          <button
            type="button"
            className="btn btn--link"
            onClick={() => {
              setMode("pick");
              setNewName("");
              setError(null);
            }}
          >
            cancel
          </button>
        </>
      )}
      {error && <span className="add-to-integration__error">{error}</span>}
    </span>
  );
}
