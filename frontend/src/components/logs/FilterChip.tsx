// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { VALUELESS_ATTR_OPS } from "../../api/types";
import type { LogAttrOp } from "../../api/types";

const OP_GLYPH: Record<LogAttrOp, string> = {
  eq: "=",
  neq: "≠",
  contains: "contains",
  not_contains: "!contains",
  starts_with: "starts",
  ends_with: "ends",
  matches: "matches",
  exists: "exists",
  not_exists: "is absent",
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
      {/* exists / not_exists carry no value — rendering an empty span
          leaves a stray separator that reads as a missing value. */}
      {!VALUELESS_ATTR_OPS.includes(op) && <span className="fchip__v">{value}</span>}
      <button className="fchip__x" type="button" onClick={onRemove} aria-label={`Remove ${k} filter`}>
        ✕
      </button>
    </span>
  );
}
