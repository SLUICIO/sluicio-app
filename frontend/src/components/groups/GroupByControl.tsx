// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The "Group by" control shared by the Metrics and Logs lists: a
// dimension dropdown (None / Service / Integration / Type-or-Severity /
// Attribute) plus, when Attribute is chosen, a searchable key picker.

import { useId } from "react";
import AttrKeyPicker from "./AttrKeyPicker";

export interface GroupValue {
  by: string; // "none" | "service" | "integration" | "type" | "severity" | "attribute"
  key: string; // attribute key when by === "attribute"
}

export interface GroupDim {
  value: string;
  label: string;
}

export default function GroupByControl({
  value,
  onChange,
  dims,
  attrKeys,
}: {
  value: GroupValue;
  onChange: (v: GroupValue) => void;
  dims: GroupDim[];
  attrKeys: string[];
}) {
  // Unique per instance: this control appears more than once on a page,
  // and a duplicated id would point every select at the first label.
  const labelId = useId();
  return (
    <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
      {/* A <span>, not a <label>, so it names the control visually but not
          to a screen reader — the select announced only its value. */}
      <span className="muted" style={{ fontSize: 12 }} id={labelId}>Group by</span>
      <select
        className="m-rs-sel"
        aria-labelledby={labelId}
        value={value.by}
        onChange={(e) => onChange({ by: e.target.value, key: e.target.value === "attribute" ? value.key : "" })}
      >
        <option value="none">None</option>
        {dims.map((d) => (
          <option key={d.value} value={d.value}>{d.label}</option>
        ))}
      </select>
      {value.by === "attribute" && (
        <AttrKeyPicker value={value.key} keys={attrKeys} onPick={(k) => onChange({ by: "attribute", key: k })} />
      )}
    </div>
  );
}
