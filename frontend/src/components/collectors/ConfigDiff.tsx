// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A configuration change, line by line, both sides masked by the server.

import { useMemo } from "react";
import { hasChanges, lineDiff } from "../../lib/lineDiff";

export default function ConfigDiff({
  before,
  after,
  maxHeight = 480,
}: {
  before: string;
  after: string;
  maxHeight?: number;
}) {
  const lines = useMemo(() => lineDiff(before, after), [before, after]);
  if (!hasChanges(lines)) {
    return (
      <div className="muted" style={{ fontSize: 13 }}>
        No change from the configuration the collector runs.
      </div>
    );
  }
  return (
    <pre
      className="mono"
      style={{
        margin: 0,
        fontSize: 12,
        lineHeight: 1.5,
        maxHeight,
        overflow: "auto",
        border: "1px solid var(--border)",
        borderRadius: 8,
        background: "var(--surface)",
        padding: "8px 0",
      }}
    >
      {lines.map((l, i) => (
        <div
          key={i}
          style={{
            padding: "0 12px",
            whiteSpace: "pre",
            background:
              l.op === "+"
                ? "var(--ok-soft)"
                : l.op === "-"
                  ? "var(--err-soft)"
                  : undefined,
            color:
              l.op === "+"
                ? "var(--ok-ink)"
                : l.op === "-"
                  ? "var(--err-ink)"
                  : undefined,
          }}
        >
          {l.op === " " ? "  " : l.op + " "}
          {l.text}
        </div>
      ))}
    </pre>
  );
}
