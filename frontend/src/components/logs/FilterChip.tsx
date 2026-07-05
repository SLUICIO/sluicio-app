// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import type { LogAttrOp } from "../../api/types";

const OP_GLYPH: Record<LogAttrOp, string> = {
  eq: "=",
  neq: "≠",
  contains: "contains",
  not_contains: "!contains",
  starts_with: "starts",
  exists: "exists",
  gt: ">",
  gte: "≥",
  lt: "<",
  lte: "≤",
};

// One active filter rendered as a composite key·op·value chip with a
// remove button. `accent` highlights the chip the user is investigating
// (e.g. a pinned domain id) — at most one in the bar.
export default function FilterChip({
  k,
  op,
  value,
  accent,
  onRemove,
}: {
  k: string;
  op: LogAttrOp;
  value: string;
  accent?: boolean;
  onRemove: () => void;
}) {
  return (
    <span className={`fchip ${accent ? "fchip--accent" : ""}`}>
      <span className="fchip__k">{k}</span>
      <span className="fchip__o">{OP_GLYPH[op] ?? op}</span>
      {op !== "exists" && <span className="fchip__v">{value}</span>}
      <button className="fchip__x" type="button" onClick={onRemove} aria-label={`Remove ${k} filter`}>
        ✕
      </button>
    </span>
  );
}
