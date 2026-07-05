// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Tailwind config for the Conduit UI. Color tokens come from the
// Conduit Color System (see src/styles.css and the LLM handoff
// README). The mapping below is taken verbatim from the handoff so
// utility classes match the documented intent:
//
//   bg-surface    → --surface     (page background, warm off-white)
//   bg-surface-2  → --surface-2   (cards, panels, modals)
//   bg-surface-3  → --surface-3   (table-row hover, input fields)
//   bg-primary    → --primary     (one primary action per surface)
//   bg-ok-soft / text-ok-ink etc. → status callouts
//
// Legacy alias utilities (`bg-background`, `bg-accent`, `text-warning`,
// `text-critical`, etc.) remain mapped to the handoff tokens via the
// CSS variables so existing markup keeps rendering correctly.

/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        // ── Handoff tokens (canonical) ──────────────────────────
        primary: {
          DEFAULT: "var(--primary)",
          hover: "var(--primary-hover)",
          soft: "var(--primary-soft)",
          ink: "var(--primary-ink)",
          on: "var(--on-primary)",
        },
        surface: {
          DEFAULT: "var(--surface)",
          2: "var(--surface-2)",
          3: "var(--surface-3)",
        },
        ink: {
          DEFAULT: "var(--ink)",
          2: "var(--ink-2)",
          muted: "var(--muted)",
        },
        border: {
          DEFAULT: "var(--border)",
          strong: "var(--border-strong)",
        },
        ok: {
          DEFAULT: "var(--ok)",
          soft: "var(--ok-soft)",
          ink: "var(--ok-ink)",
        },
        warn: {
          DEFAULT: "var(--warn)",
          soft: "var(--warn-soft)",
          ink: "var(--warn-ink)",
        },
        err: {
          DEFAULT: "var(--err)",
          soft: "var(--err-soft)",
          ink: "var(--err-ink)",
        },
        info: {
          DEFAULT: "var(--info)",
          soft: "var(--info-soft)",
        },

        // ── Legacy aliases ──────────────────────────────────────
        // A handful of existing components still use the old class
        // names. Keep them mapped to handoff tokens so behaviour
        // stays consistent during the migration.
        background: "var(--surface)",     // page bg — warm off-white
        foreground: "var(--ink)",
        muted: "var(--muted)",
        "surface-elevated": "var(--surface-3)",
        accent: "var(--primary)",
        warning: "var(--warn)",
        critical: "var(--err)",
      },
      fontFamily: {
        sans: [
          "Inter",
          "-apple-system",
          "BlinkMacSystemFont",
          "Segoe UI",
          "Roboto",
          "Helvetica Neue",
          "Arial",
          "sans-serif",
        ],
        mono: [
          "JetBrains Mono",
          "ui-monospace",
          "SFMono-Regular",
          "Menlo",
          "Consolas",
          "monospace",
        ],
      },
      boxShadow: {
        sm: "var(--shadow-sm)",
        DEFAULT: "var(--shadow)",
        focus: "var(--focus)",
      },
    },
  },
  plugins: [],
};
