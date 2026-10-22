// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// SpanEvents - what a span recorded while it ran (#42). An exception
// (OpenTelemetry's "exception" event, which .NET's Activity.AddException,
// Java's recordException and Python's record_exception all write) is the
// reason most people open a failed span, so it leads: type and message
// up front, the stack trace one click away. Any other event is listed
// below it with its offset from the span's start.

import { useState } from "react";
import type { SpanEvent, SpanSummary } from "../api/types";
import { KVTable, attributeRows } from "./primitives";

export interface SpanException {
  type: string;
  message: string;
  stacktrace?: string;
}

/** The exception a span recorded: its first exception event, else the
 *  exception.* attributes some instrumentations set on the span itself.
 *  Null when it recorded neither. */
export function exceptionOf(span: SpanSummary): SpanException | null {
  const ev = (span.events ?? []).find((e) => e.name === "exception");
  if (ev) {
    const a = ev.attributes ?? {};
    if (a["exception.type"] || a["exception.message"]) {
      return {
        type: a["exception.type"] || "Exception",
        message: a["exception.message"] ?? "",
        stacktrace: a["exception.stacktrace"] || undefined,
      };
    }
  }
  const a = span.attributes ?? {};
  if (a["exception.type"] || a["exception.message"]) {
    return {
      type: a["exception.type"] || "Exception",
      message: a["exception.message"] ?? "",
      stacktrace: a["exception.stacktrace"] || undefined,
    };
  }
  return null;
}

const EXCEPTION_KEYS = new Set(["exception.type", "exception.message", "exception.stacktrace"]);

export default function SpanEvents({ span }: { span: SpanSummary }) {
  const events = span.events ?? [];
  if (events.length === 0) return null;
  const total = span.events_total ?? events.length;
  const start = Date.parse(span.timestamp);
  return (
    <div className="mt-3 first:mt-0" data-testid="span-events">
      <div className="kv__section">
        Events{" "}
        <span className="text-muted">
          · {total > events.length ? `showing ${events.length} of ${total}` : events.length}
        </span>
      </div>
      <div className="flex flex-col gap-2">
        {events.map((ev, i) =>
          ev.name === "exception" ? (
            <ExceptionEvent key={i} event={ev} offset={offsetLabel(ev, start)} />
          ) : (
            <PlainEvent key={i} event={ev} offset={offsetLabel(ev, start)} />
          ),
        )}
      </div>
    </div>
  );
}

function offsetLabel(ev: SpanEvent, spanStart: number): string {
  const t = Date.parse(ev.timestamp);
  if (Number.isNaN(t) || Number.isNaN(spanStart)) return "";
  const ms = t - spanStart;
  if (ms < 0) return "";
  return ms < 1000 ? `+${ms.toFixed(0)} ms` : `+${(ms / 1000).toFixed(2)} s`;
}

function ExceptionEvent({ event, offset }: { event: SpanEvent; offset: string }) {
  const a = event.attributes ?? {};
  const rest = Object.fromEntries(Object.entries(a).filter(([k]) => !EXCEPTION_KEYS.has(k)));
  return (
    <div
      className="rounded-md p-3 text-sm"
      style={{ borderLeft: "3px solid var(--err)", background: "var(--err-soft)", color: "var(--err-ink)" }}
    >
      <div className="flex items-baseline justify-between gap-2">
        <span className="min-w-0 break-all font-mono font-semibold">{a["exception.type"] || "exception"}</span>
        {offset && <span className="shrink-0 font-mono text-xs opacity-75">{offset}</span>}
      </div>
      {a["exception.message"] && <div className="mt-1 whitespace-pre-wrap break-words">{a["exception.message"]}</div>}
      {a["exception.stacktrace"] && <StackTrace text={a["exception.stacktrace"]} />}
      {Object.keys(rest).length > 0 && (
        <div className="mt-2">
          <KVTable rows={attributeRows(rest)} />
        </div>
      )}
    </div>
  );
}

export function StackTrace({ text, defaultOpen = false }: { text: string; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  const lines = text.split("\n").length;
  return (
    <div className="mt-2">
      <button
        type="button"
        className="btn btn--link text-xs"
        style={{ padding: 0, color: "inherit" }}
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        {open ? "▾" : "▸"} Stack trace <span className="opacity-75">· {lines} line{lines === 1 ? "" : "s"}</span>
      </button>
      {open && (
        <pre
          className="m-0 mt-1 max-h-80 overflow-auto rounded p-2 font-mono text-xs leading-relaxed"
          style={{ background: "var(--surface-3)", color: "var(--ink)" }}
        >
          {text}
        </pre>
      )}
    </div>
  );
}

function PlainEvent({ event, offset }: { event: SpanEvent; offset: string }) {
  const attrs = event.attributes ?? {};
  return (
    <div className="rounded-md border p-2 text-sm" style={{ borderColor: "var(--border)" }}>
      <div className="flex items-baseline justify-between gap-2">
        <span className="min-w-0 break-all font-medium">{event.name || "(unnamed event)"}</span>
        {offset && <span className="shrink-0 font-mono text-xs text-muted">{offset}</span>}
      </div>
      {Object.keys(attrs).length > 0 && (
        <div className="mt-1">
          <KVTable rows={attributeRows(attrs)} />
        </div>
      )}
    </div>
  );
}
