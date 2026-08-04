// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Viewport coordinates for a popover that must escape its container.
//
// `.card` sets `overflow: hidden` so its rounded corners clip content.
// That also clips any absolutely-positioned child, so a menu opened from
// a card header is cut off at the card's edge — the "+ Add health check"
// menu lost its third option entirely, and the two that showed were the
// only ones anyone knew existed.
//
// The fix is to portal the popover to <body> and place it with
// `position: fixed`, which is not clipped by an ancestor's overflow.
// That means computing coordinates by hand, keeping them correct while
// the page scrolls, and flipping upward when the space below is tight —
// the three things every from-scratch dropdown gets wrong.
//
// SearchableSelect solves the same problem with its own copy of this
// logic; it predates this hook and is deliberately left alone (it is
// used in a lot of places and has its own e2e). New popovers should use
// this, and that copy can converge on it later.

import { useEffect, useState, type RefObject } from "react";

export interface AnchorCoords {
  top: number;
  left: number;
  minWidth: number;
  /** True when the popover hangs above the trigger instead of below. */
  openUp: boolean;
  /** Room the chosen direction actually has, for clamping a long list. */
  maxHeight: number;
}

export interface AnchorOptions {
  /** Anchor the popover's right edge to the trigger's right edge. */
  align?: "left" | "right";
  /** Below this much room underneath, prefer opening upward. */
  flipBelow?: number;
  /** Never make the popover narrower than this. */
  minWidth?: number;
}

/**
 * Tracks where a popover anchored to `ref` should sit, or null while closed.
 *
 * Scroll is observed in the CAPTURE phase: the trigger may live inside a
 * scrollable panel rather than the document, and a bubbling listener
 * never sees that scroll — the popover would then hang in place while
 * its trigger moved away underneath it.
 */
export function useAnchoredPosition(
  ref: RefObject<HTMLElement | null>,
  open: boolean,
  opts: AnchorOptions = {},
): AnchorCoords | null {
  const { align = "left", flipBelow = 220, minWidth = 0 } = opts;
  const [coords, setCoords] = useState<AnchorCoords | null>(null);

  useEffect(() => {
    if (!open) {
      setCoords(null);
      return;
    }
    const place = () => {
      const el = ref.current;
      if (!el) return;
      const r = el.getBoundingClientRect();
      const spaceBelow = window.innerHeight - r.bottom - 12;
      const spaceAbove = r.top - 12;
      // Only flip when up is genuinely roomier, so a popover near the
      // middle of a tall window keeps the conventional downward drop.
      const openUp = spaceBelow < flipBelow && spaceAbove > spaceBelow;
      setCoords({
        top: openUp ? r.top - 4 : r.bottom + 4,
        left: align === "right" ? r.right : r.left,
        minWidth: Math.max(r.width, minWidth),
        openUp,
        // Floored so a popover in a very short viewport is scrollable
        // rather than collapsed to nothing.
        maxHeight: Math.max(120, (openUp ? spaceAbove : spaceBelow) - 8),
      });
    };
    place();
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    return () => {
      window.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", place);
    };
  }, [ref, open, align, flipBelow, minWidth]);

  return coords;
}

/**
 * The transform that turns a top/left anchor into the requested
 * alignment and direction. Kept beside the hook so a caller cannot
 * apply the coordinates without it and end up subtly misplaced.
 */
export function anchorTransform(coords: AnchorCoords, align: "left" | "right"): string | undefined {
  const parts = [align === "right" ? "translateX(-100%)" : "", coords.openUp ? "translateY(-100%)" : ""];
  return parts.filter(Boolean).join(" ") || undefined;
}
