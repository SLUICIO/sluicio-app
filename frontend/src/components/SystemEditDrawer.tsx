// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Right-side blade for editing a system — opened from the System detail
// "Edit" button. Consolidates what used to be scattered across window.prompt
// dialogs and separate cards: edit name / type / description, toggle the
// public status badge, and attach a member service. Uses the shared
// EditDrawer primitive (portal, backdrop, Esc, unsaved-changes guard).

import { useState } from "react";
import { api } from "../api/client";
import type { System } from "../api/types";
import EditDrawer from "./primitives/EditDrawer";
import SearchableSelect from "./SearchableSelect";
import PublicBadgeControl from "./PublicBadgeControl";

interface Props {
  system: System;
  /** Services not yet members — the attach picker's options. */
  attachable: string[];
  canWrite: boolean;
  onClose: () => void;
  /** Called after any successful mutation so the page can refresh. */
  onSaved: () => void;
}

export default function SystemEditDrawer({ system, attachable, canWrite, onClose, onSaved }: Props) {
  const [name, setName] = useState(system.name);
  const [typeKey, setTypeKey] = useState(system.type_key);
  const [description, setDescription] = useState(system.description ?? "");
  const [attachName, setAttachName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const dirty =
    name !== system.name ||
    typeKey !== system.type_key ||
    description !== (system.description ?? "");

  const saveDetails = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.updateSystem(system.id, {
        name: name.trim() || system.name,
        type_key: typeKey.trim().toLowerCase(),
        description: description.trim(),
      });
      onSaved();
      onClose();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const attach = async () => {
    const svc = attachName.trim();
    if (!svc) return;
    setBusy(true);
    setError(null);
    try {
      await api.attachSystemService(system.id, svc);
      setAttachName("");
      onSaved();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <EditDrawer title={`Edit system · ${system.name}`} width="medium" onClose={onClose} dirty={dirty}>
      <div style={{ display: "flex", flexDirection: "column", gap: 20 }}>
        {error && <div className="alert alert--error">{error}</div>}

        <section style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div className="drawer__section-title">Details</div>
          <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 13 }}>
            Name
            <input className="svc-input" value={name} onChange={(e) => setName(e.target.value)} disabled={!canWrite} />
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 13 }}>
            Type
            <input
              className="svc-input"
              value={typeKey}
              onChange={(e) => setTypeKey(e.target.value)}
              placeholder="e.g. rabbitmq, kafka"
              disabled={!canWrite}
            />
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 13 }}>
            Description
            <textarea
              className="svc-textarea"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={3}
              placeholder="What this system is and who owns it"
              disabled={!canWrite}
            />
          </label>
          {canWrite && (
            <button
              className="btn primary"
              onClick={saveDetails}
              disabled={busy || !dirty}
              style={{ alignSelf: "flex-start" }}
            >
              {busy ? "Saving…" : "Save changes"}
            </button>
          )}
        </section>

        {canWrite && (
          <section style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <div className="drawer__section-title">Add member service</div>
            {attachable.length === 0 ? (
              <span className="muted" style={{ fontSize: 12 }}>Every service is already a member.</span>
            ) : (
              <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
                <div style={{ minWidth: 240, flex: 1 }}>
                  <SearchableSelect
                    value={attachName}
                    onChange={setAttachName}
                    options={attachable}
                    placeholder="Search services…"
                    allLabel="— select a service —"
                  />
                </div>
                <button className="btn primary" onClick={attach} disabled={busy || !attachName.trim()}>
                  Attach
                </button>
              </div>
            )}
          </section>
        )}

        <section style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          <div className="drawer__section-title">Public status badge</div>
          <PublicBadgeControl
            kind="system"
            id={system.id}
            enabled={system.badge_public ?? false}
            canManage={canWrite}
            onChange={() => onSaved()}
          />
        </section>
      </div>
    </EditDrawer>
  );
}
