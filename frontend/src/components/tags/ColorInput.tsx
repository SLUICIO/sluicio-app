// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ColorInput is the three-in-one color picker used on the Tags admin
// page: a palette of quick-pick swatches, a hex text field, and the
// browser's native color picker for full-range selection.
//
// The component is controlled. `value` is always a normalized,
// validated hex (#rgb or #rrggbb, lowercase) — the same shape the
// server's CHECK constraint enforces. `onChange` is fired only with
// valid values, so consumers can save unconditionally.

import { useEffect, useState } from "react";

// Server-side regex from migrations/0004_tags.up.sql and the Go
// validator in internal/tags/types.go. Keep these in sync.
const HEX_RE = /^#([0-9a-f]{3}|[0-9a-f]{6})$/;

interface ColorInputProps {
  value: string;
  onChange: (color: string) => void;
  palette?: string[];
  // Optional label rendered above the swatch grid. Kept here so the
  // surrounding form's label and this component's internal layout
  // stay aligned without callers redoing the structure.
  label?: string;
  disabled?: boolean;
}

export const DEFAULT_TAG_PALETTE = [
  "#ef4444", "#f97316", "#f59e0b", "#eab308",
  "#84cc16", "#10b981", "#14b8a6", "#06b6d4",
  "#0ea5e9", "#3b82f6", "#6366f1", "#8b5cf6",
  "#a855f7", "#ec4899", "#f43f5e", "#64748b",
];

export default function ColorInput({
  value,
  onChange,
  palette = DEFAULT_TAG_PALETTE,
  label,
  disabled = false,
}: ColorInputProps) {
  // Local text state so the user can type intermediate values
  // ("#3b8") without the parent thrashing. We sync from `value`
  // whenever it changes upstream (e.g. when the user clicks a
  // swatch), and we only call onChange when the typed value is a
  // valid hex.
  const [text, setText] = useState(value);
  useEffect(() => setText(value), [value]);

  const valid = HEX_RE.test(text);
  const dirty = text.toLowerCase() !== value.toLowerCase();

  const commit = (next: string) => {
    const normalized = next.trim().toLowerCase();
    if (!HEX_RE.test(normalized)) {
      // Snap the text input back to the last good value so the user
      // isn't left staring at "invalid" forever.
      setText(value);
      return;
    }
    if (normalized !== value) onChange(normalized);
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      {label && (
        <span
          className="muted"
          style={{ fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5 }}
        >
          {label}
        </span>
      )}

      {/* Palette */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(8, 1fr)", gap: 6 }}>
        {palette.map((c) => (
          <button
            key={c}
            type="button"
            aria-label={`Use color ${c}`}
            disabled={disabled}
            onClick={() => onChange(c)}
            style={{
              width: 22,
              height: 22,
              borderRadius: 4,
              background: c,
              border:
                c.toLowerCase() === value.toLowerCase()
                  ? "2px solid var(--ink)"
                  : "1px solid rgba(0,0,0,0.15)",
              cursor: disabled ? "default" : "pointer",
              padding: 0,
            }}
          />
        ))}
      </div>

      {/* Hex + native picker */}
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <input
          type="text"
          className="search__input"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onBlur={() => commit(text)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              commit(text);
            } else if (e.key === "Escape") {
              setText(value);
            }
          }}
          disabled={disabled}
          spellCheck={false}
          aria-label="Hex color"
          placeholder="#3b82f6"
          style={{
            width: 100,
            padding: "3px 8px",
            fontSize: 13,
            fontFamily: "'JetBrains Mono', ui-monospace, monospace",
            // Red border while the user is typing something invalid;
            // a soft cue, not a blocker — they can keep editing.
            borderColor: dirty && !valid ? "var(--err)" : undefined,
          }}
        />

        {/* Native color picker — gives full-range selection for the
            rare case where neither the palette nor a hand-typed hex
            is enough. The visible chip doubles as the trigger. */}
        <label
          style={{
            position: "relative",
            width: 28,
            height: 28,
            borderRadius: 4,
            background: valid ? text : value,
            border: "1px solid rgba(0,0,0,0.15)",
            cursor: disabled ? "default" : "pointer",
            overflow: "hidden",
          }}
          title="Open the color picker"
        >
          <input
            type="color"
            value={valid ? text : value}
            onChange={(e) => onChange(e.target.value.toLowerCase())}
            disabled={disabled}
            style={{
              position: "absolute",
              inset: 0,
              opacity: 0,
              cursor: disabled ? "default" : "pointer",
            }}
          />
        </label>

        {dirty && !valid && (
          <span style={{ fontSize: 11, color: "var(--err)" }}>
            #rgb or #rrggbb
          </span>
        )}
      </div>
    </div>
  );
}
