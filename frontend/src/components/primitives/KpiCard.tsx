// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// KpiCard — the standard tile used in dashboard KPI rows. Title +
// big value + optional sub-row (sparkline, donut, breakdown pills).
// Replaces the hand-drawn SBox primitive from the wireframe handoff.

import { ReactNode } from "react";
import { Link } from "react-router-dom";

interface Props {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  /** When set, the whole card becomes a link (e.g. drill into the
   *  unhealthy list) — pointer cursor + a hover lift. */
  to?: string;
  /**
   * Visual emphasis for the card. Per the Color System "Cards &
   * panels" rules:
   *   - "selected": a 1.5–2px --primary border (the "you are here"
   *     hint when a card is the active selection)
   *   - "attention": normal --surface-2 body with a 4px --err left
   *     border — the "this needs attention" callout. *Never* fill
   *     the whole card red.
   */
  emphasis?: "none" | "selected" | "attention";
  tone?: "default" | "ok" | "warn" | "err";
  children?: ReactNode;
}

export default function KpiCard({
  label,
  value,
  sub,
  to,
  emphasis = "none",
  tone = "default",
  children,
}: Props) {
  const toneColor = {
    default: "var(--ink)",
    ok: "var(--ok)",
    warn: "var(--warn)",
    err: "var(--err)",
  }[tone];

  const isAttention = emphasis === "attention";
  const isSelected = emphasis === "selected";

  const style = {
    borderColor: isSelected ? "var(--primary)" : "var(--border)",
    borderWidth: isSelected ? 2 : 1,
    background: "var(--surface-2)",
    color: "var(--ink)",
    borderLeft: isAttention ? "4px solid var(--err)" : undefined,
  } as const;

  const inner = (
    <>
      <div
        className="text-xs uppercase tracking-wide"
        style={{ color: "var(--muted)" }}
      >
        {label}
      </div>
      <div
        className="mt-1 text-3xl font-semibold leading-none tabular-nums"
        style={{ color: toneColor }}
      >
        {value}
      </div>
      {sub && (
        <div className="mt-2 text-xs" style={{ color: "var(--muted)" }}>
          {sub}
        </div>
      )}
      {children && <div className="mt-3">{children}</div>}
    </>
  );

  if (to) {
    return (
      <Link
        to={to}
        className="block rounded-xl border p-4 shadow-sm no-underline cursor-pointer transition-shadow hover:shadow-md"
        style={style}
      >
        {inner}
      </Link>
    );
  }

  return (
    <div className="rounded-xl border p-4 shadow-sm" style={style}>
      {inner}
    </div>
  );
}
