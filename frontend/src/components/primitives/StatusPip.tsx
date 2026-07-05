// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// StatusPip — small coloured glyph + optional label. The wireframe
// handoff uses ●/▲/✖ glyphs with ok/warn/err semantics; we keep that
// vocabulary but render with the codebase's semantic colour tokens
// (--ok / --warning / --critical / --text-muted).

import { ReactNode } from "react";

export type PipKind = "ok" | "warn" | "err" | "muted";

interface Props {
  kind?: PipKind;
  label?: ReactNode;
  className?: string;
}

const COLOR: Record<PipKind, string> = {
  ok: "var(--ok)",
  warn: "var(--warn)",
  err: "var(--err)",
  muted: "var(--muted)",
};

const GLYPH: Record<PipKind, string> = {
  ok: "●",
  warn: "▲",
  err: "✖",
  muted: "○",
};

export default function StatusPip({ kind = "ok", label, className = "" }: Props) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 text-xs ${className}`}
      style={{ color: COLOR[kind] }}
      title={typeof label === "string" ? label : undefined}
    >
      <span aria-hidden style={{ fontSize: 10 }}>{GLYPH[kind]}</span>
      {label !== undefined && <span style={{ color: "var(--ink)" }}>{label}</span>}
    </span>
  );
}
