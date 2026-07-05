// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Logo — the Sluicio brand mark ("Block-S").
//
// Per the logo handoff (design_handoff_sluicio_logo): a monogram S cut
// from three bars with sharp 90° miters and square ends — the sluice
// channel rendered as hard geometry. Bold and unmistakable at any size;
// survives down to a 16px favicon. One open stroke path, no fills, no
// curves.
//
// Construction (final, exact): 64×64 viewBox, single path
// `M 48 17 H 16 V 32 H 48 V 47 H 16` — top bar → left riser → middle bar
// → right riser → bottom bar. stroke-width 9, miter joins, square caps.
// The miter + square caps are what make it sharp — do NOT round them,
// and keep the 9/64 (~14%) stroke weight.
//
// Color is inherited from currentColor so callers control hue with CSS
// `color` (or inline `style={{ color: "var(--primary)" }}`). The ~11px
// optical inset baked into the path IS the built-in clear space.

import type { SVGProps } from "react";

export interface LogoMarkProps extends SVGProps<SVGSVGElement> {
  size?: number;
}

export function LogoMark({ size = 24, ...rest }: LogoMarkProps) {
  return (
    <svg
      viewBox="0 0 64 64"
      width={size}
      height={size}
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden
      {...rest}
    >
      <path
        d="M 48 17 H 16 V 32 H 48 V 47 H 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="9"
        strokeLinejoin="miter"
        strokeLinecap="square"
      />
    </svg>
  );
}

// LogoLockup — mark + wordmark side by side. Default presentation
// in nav chrome, login surface, and any "brand moment" the design
// calls out. Wordmark color follows --ink so it adapts to theme.
export interface LogoLockupProps {
  size?: number;
  className?: string;
  /** When true, hides the wordmark and shows only the mark. */
  markOnly?: boolean;
}

export function LogoLockup({ size = 22, className, markOnly = false }: LogoLockupProps) {
  return (
    <div className={className} style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
      <LogoMark size={size} style={{ color: "var(--primary)" }} />
      {!markOnly && (
        <span
          style={{
            fontSize: 14,
            fontWeight: 700,
            letterSpacing: "-0.035em",
            color: "var(--ink)",
          }}
        >
          Sluicio
        </span>
      )}
    </div>
  );
}
