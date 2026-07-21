// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { EditDrawer } from "@sluicio/frontend";

const noop = () => {};
const field: React.CSSProperties = { display: "flex", flexDirection: "column", gap: 6, marginBottom: 16 };
const label: React.CSSProperties = { fontSize: 12, fontWeight: 600, color: "var(--muted)" };
const input: React.CSSProperties = {
  padding: "8px 10px",
  border: "1px solid var(--border)",
  borderRadius: 8,
  background: "var(--surface-3)",
  color: "var(--ink)",
  fontSize: 13,
};

// The right-side overlay drawer used for create/edit forms (Tags,
// Metadata, Schemas, Maps) — it floats over the page with a backdrop
// and slides in from the right. Rendered open here with a sample form.
export const Open = () => (
  <EditDrawer title="Edit tag" width="medium" onClose={noop}>
    <div style={{ fontFamily: "Inter, system-ui, sans-serif" }}>
      <div style={field}>
        <span style={label}>Key</span>
        <input style={input} defaultValue="team" />
      </div>
      <div style={field}>
        <span style={label}>Value</span>
        <input style={input} defaultValue="platform" />
      </div>
      <div style={field}>
        <span style={label}>Description</span>
        <input style={input} defaultValue="Owning team for routing + on-call" />
      </div>
      <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
        <button className="btn btn--primary" type="button">Save changes</button>
        <button className="btn" type="button">Cancel</button>
      </div>
    </div>
  </EditDrawer>
);
