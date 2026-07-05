// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A small pill that renders a tag's name with its configured color as
// the background. Text color is auto-picked (black or white) based on
// the background's luminance so chips stay readable across themes.
//
// Optional onRemove turns the chip into an editor-mode chip with a
// small "×" affordance. The remove button isn't shown when onRemove
// is omitted, so the same component renders in read-only contexts.

import type { CSSProperties } from "react";
import type { Tag } from "../../api/types";

interface TagChipProps {
  tag: Tag;
  onRemove?: () => void;
  // size lets the chips fit naturally next to surrounding controls.
  // "sm" is the default and matches the existing .pill rhythm.
  size?: "sm" | "md";
}

export default function TagChip({ tag, onRemove, size = "sm" }: TagChipProps) {
  const bg = tag.color || "#94a3b8";
  const fg = readableTextColor(bg);
  const padding = size === "md" ? "4px 10px" : "2px 8px";
  const fontSize = size === "md" ? 13 : 12;
  const style: CSSProperties = {
    background: bg,
    color: fg,
    padding,
    fontSize,
    fontWeight: 500,
    borderRadius: 999,
    display: "inline-flex",
    alignItems: "center",
    gap: 6,
    lineHeight: 1.4,
    // A faint dark border so chips with very pale colors still have
    // an edge against the surface.
    border: "1px solid rgba(0,0,0,0.08)",
  };
  return (
    <span style={style} title={tag.slug}>
      <span>{tag.name}</span>
      {onRemove && (
        <button
          type="button"
          onClick={onRemove}
          aria-label={`Remove tag ${tag.name}`}
          style={{
            background: "transparent",
            border: 0,
            color: fg,
            opacity: 0.7,
            cursor: "pointer",
            fontSize: 12,
            padding: 0,
            lineHeight: 1,
          }}
        >
          ×
        </button>
      )}
    </span>
  );
}

// readableTextColor returns either #000 or #fff depending on which
// has higher contrast against the supplied background. Uses the
// standard relative-luminance formula; close enough for chip text.
function readableTextColor(hex: string): string {
  const { r, g, b } = parseHex(hex);
  // sRGB → linear → relative luminance (Rec. 709 coefficients).
  const lin = (c: number) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  const L = 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
  return L > 0.55 ? "#0b0f14" : "#ffffff";
}

function parseHex(hex: string): { r: number; g: number; b: number } {
  let h = hex.replace("#", "");
  if (h.length === 3) {
    h = h
      .split("")
      .map((c) => c + c)
      .join("");
  }
  const n = parseInt(h, 16);
  return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 };
}
