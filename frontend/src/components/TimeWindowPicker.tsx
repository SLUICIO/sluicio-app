// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { isAbsolute, useTimeWindow } from "../lib/useTimeWindow";

interface Preset {
  value: string;
  label: string;
}

const PRESETS: Preset[] = [
  { value: "5m", label: "Last 5 minutes" },
  { value: "15m", label: "Last 15 minutes" },
  { value: "30m", label: "Last 30 minutes" },
  { value: "1h", label: "Last hour" },
  { value: "3h", label: "Last 3 hours" },
  { value: "6h", label: "Last 6 hours" },
  { value: "12h", label: "Last 12 hours" },
  { value: "24h", label: "Last 24 hours" },
  { value: "2d", label: "Last 2 days" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
];

export default function TimeWindowPicker() {
  const [value, setValue] = useTimeWindow();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  // Close the popover when clicking outside or pressing Escape.
  useEffect(() => {
    if (!open) return;
    const onPointer = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const display = useMemo(() => labelFor(value), [value]);

  const pick = (next: string) => {
    setValue(next);
    setOpen(false);
  };

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="inline-flex items-center gap-2 rounded-md border border-border bg-surface-2 px-3 py-1.5 text-sm text-foreground hover:bg-surface-3"
        aria-haspopup="dialog"
        aria-expanded={open}
      >
        <ClockIcon />
        <span>{display}</span>
        <CaretIcon />
      </button>
      {open && (
        <div
          role="dialog"
          aria-label="Time range"
          className="absolute right-0 top-full z-20 mt-2 w-80 rounded-lg border border-border bg-surface-2 p-2 shadow"
        >
          <div className="px-2 py-1 text-xs font-medium uppercase tracking-wider text-muted">
            Quick
          </div>
          <div className="grid grid-cols-2 gap-1">
            {PRESETS.map((p) => (
              <button
                key={p.value}
                type="button"
                onClick={() => pick(p.value)}
                className={
                  "rounded-md px-3 py-1.5 text-left text-sm transition-colors " +
                  (value === p.value
                    ? "bg-surface-elevated text-foreground"
                    : "text-muted hover:bg-surface-elevated hover:text-foreground")
                }
              >
                {p.label}
              </button>
            ))}
          </div>
          <div className="my-2 border-t border-border" />
          <CustomRangeForm current={value} onApply={pick} />
        </div>
      )}
    </div>
  );
}

function CustomRangeForm({
  current,
  onApply,
}: {
  current: string;
  onApply: (next: string) => void;
}) {
  // Seed the inputs from the current absolute value if the user is
  // already on one; otherwise default to "yesterday at this time → now".
  const seed = useMemo(() => seedRange(current), [current]);
  const [from, setFrom] = useState(seed.from);
  const [to, setTo] = useState(seed.to);
  const [error, setError] = useState<string | null>(null);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!from || !to) {
      setError("Both From and To are required.");
      return;
    }
    const fromDate = new Date(from);
    const toDate = new Date(to);
    if (Number.isNaN(fromDate.getTime()) || Number.isNaN(toDate.getTime())) {
      setError("Invalid date.");
      return;
    }
    if (toDate <= fromDate) {
      setError("To must be after From.");
      return;
    }
    onApply(`${fromDate.toISOString()}/${toDate.toISOString()}`);
  };

  return (
    <form onSubmit={submit} className="px-2 pb-1 pt-1">
      <div className="px-1 pb-2 text-xs font-medium uppercase tracking-wider text-muted">
        Custom range
      </div>
      <label className="mb-2 block text-xs text-muted">
        From
        <input
          type="datetime-local"
          value={from}
          onChange={(e) => setFrom(e.target.value)}
          className="mt-1 w-full rounded-md border border-border bg-surface-3 px-2 py-1.5 text-sm text-foreground focus:border-primary focus:shadow-focus focus:outline-none"
        />
      </label>
      <label className="mb-2 block text-xs text-muted">
        To
        <input
          type="datetime-local"
          value={to}
          onChange={(e) => setTo(e.target.value)}
          className="mt-1 w-full rounded-md border border-border bg-surface-3 px-2 py-1.5 text-sm text-foreground focus:border-primary focus:shadow-focus focus:outline-none"
        />
      </label>
      {error && (
        <div className="mb-2 rounded-md border border-err/40 bg-err-soft px-2 py-1 text-xs text-err-ink">
          {error}
        </div>
      )}
      <button
        type="submit"
        className="w-full rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-white hover:opacity-90"
      >
        Apply custom range
      </button>
      <button
        type="button"
        onClick={() => {
          const yesterday = new Date(Date.now() - 24 * 60 * 60 * 1000);
          const start = new Date(
            yesterday.getFullYear(),
            yesterday.getMonth(),
            yesterday.getDate(),
            5,
            15,
            0,
            0,
          );
          const end = new Date(
            yesterday.getFullYear(),
            yesterday.getMonth(),
            yesterday.getDate(),
            16,
            15,
            0,
            0,
          );
          setFrom(toDatetimeLocal(start));
          setTo(toDatetimeLocal(end));
        }}
        className="mt-2 w-full rounded-md border border-border px-3 py-1.5 text-xs text-muted hover:text-foreground"
      >
        Example: yesterday 05:15 → 16:15
      </button>
    </form>
  );
}

// --- helpers ----------------------------------------------------------

function labelFor(value: string): string {
  if (isAbsolute(value)) {
    const [from, to] = value.split("/");
    return `${formatShort(from)} → ${formatShort(to)}`;
  }
  const preset = PRESETS.find((p) => p.value === value);
  return preset?.label ?? value;
}

function formatShort(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function toDatetimeLocal(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function seedRange(current: string): { from: string; to: string } {
  if (isAbsolute(current)) {
    const [a, b] = current.split("/");
    const ad = new Date(a);
    const bd = new Date(b);
    if (!Number.isNaN(ad.getTime()) && !Number.isNaN(bd.getTime())) {
      return { from: toDatetimeLocal(ad), to: toDatetimeLocal(bd) };
    }
  }
  const now = new Date();
  const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000);
  return { from: toDatetimeLocal(yesterday), to: toDatetimeLocal(now) };
}

// --- icons ------------------------------------------------------------

function ClockIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      aria-hidden
    >
      <circle cx="8" cy="8" r="6" />
      <path d="M8 4.5V8l2.5 2.5" />
    </svg>
  );
}

function CaretIcon() {
  return (
    <svg
      width="10"
      height="10"
      viewBox="0 0 10 10"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M2 3.5l3 3 3-3" />
    </svg>
  );
}
