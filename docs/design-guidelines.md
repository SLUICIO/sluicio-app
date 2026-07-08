<!-- SPDX-License-Identifier: Apache-2.0 -->

# Sluicio design guidelines

The design language of the Sluicio product, packaged so every other
surface of the business — the docs site, the website, marketing
material, future apps and tools — looks and sounds like the same
company. The product frontend (`frontend/`) is the reference
implementation; when this document and the product disagree, fix one of
them.

---

## 1. Brand

### Name

**Sluicio.** Capital S, never all-caps in prose (the GitHub org
`SLUICIO` is an artifact of org naming, not typography). No definite
article: "Sluicio ships with…", not "the Sluicio".

### The mark — Block-S

A monogram S cut from three bars with sharp 90° miters and square ends:
the sluice channel rendered as hard geometry.

Construction (exact, do not eyeball): 64×64 viewBox, one open stroke
path `M 48 17 H 16 V 32 H 48 V 47 H 16` — top bar → left riser → middle
bar → right riser → bottom bar. `stroke-width: 9` (≈14% of the box),
miter joins, square caps, no fills, no curves.

- The miters and square caps ARE the identity — never round them.
- Color comes from `currentColor`; the canonical on-surface use is
  `var(--primary)`. On photos/dark marketing surfaces, white is fine.
- The ~11px optical inset baked into the path is the built-in clear
  space; don't add decorative frames.
- Survives to 16px (favicon). Below that, don't use it.

### The lockup

Mark + wordmark side by side: mark at 22–32px, wordmark in Inter
Bold, tight tracking, same color as the mark. This is the presentation
for nav chrome, login surfaces, docs headers, and slide title pages.

### Voice

Calm, precise, honest. The product monitors things that break at 3 AM —
the brand never adds drama to that.

- Sentence case everywhere: titles, buttons, tab labels. ("Schedule
  maintenance", not "Schedule Maintenance".)
- Say what things do, not how exciting they are. Feature copy is a
  claim you could defend in a support ticket.
- Honest states: suppressed is not healthy, expired is expired, an
  empty list says what would fill it ("No custom templates yet. Fork a
  built-in above, or…").
- Destructive confirmations state the consequence, not just the
  question: "Delete template X? Health checks already applied to
  services are not removed."
- Explanatory text is welcome — one muted paragraph under a heading
  beats a tooltip. Never write "(soon)"; ship it or don't mention it.

---

## 2. Color system (v2 — sluice azure)

Three principles, in priority order:

1. **Red means broken.** Status colors are semantic, never decorative.
   A marketing page never uses `--err` red for emphasis; a chart never
   uses it for an arbitrary series.
2. **Default is neutral.** ~90% of any surface is slate-and-paper so
   that color spikes become signal. If a screen looks colorful,
   something is wrong (with the screen or with the system).
3. **Both modes, both audiences.** Warm off-white in light mode; deep
   navy in dark mode — control-room, not OLED-pitch. Every surface of
   the business that has a dark mode uses these values.

### Tokens — light (default)

| Token | Value | Use |
| --- | --- | --- |
| `--surface` | `#FAFAF7` | page background (warm off-white) |
| `--surface-2` | `#FFFFFF` | cards, top bar, sidebar, elevated surfaces |
| `--surface-3` | `#F1F1EC` | table stripes, hovers, inputs |
| `--border` | `#E5E7EB` | hairlines |
| `--border-strong` | `#CBD5E1` | emphasised/focused borders |
| `--ink` | `#0F172A` | primary text |
| `--ink-2` | `#334155` | secondary text |
| `--muted` | `#64748B` | tertiary text, labels, hints |
| `--primary` | `#0E6E9E` | sluice azure — links, active nav, primary buttons, the mark |
| `--primary-hover` | `#0B587F` | hover state |
| `--primary-soft` | `#D4EAF7` | tinted backgrounds (active nav item, selection) |
| `--primary-ink` | `#0A4A6B` | text on `--primary-soft` |
| `--on-primary` | `#FFFFFF` | text on `--primary` |
| `--ok` / `--ok-soft` / `--ok-ink` | `#15803D` / `#DCFCE7` / `#14532D` | healthy |
| `--warn` / `--warn-soft` / `--warn-ink` | `#B45309` / `#FEF3C7` / `#78350F` | degraded, maintenance, expiring |
| `--err` / `--err-soft` / `--err-ink` | `#B91C1C` / `#FEE2E2` / `#7F1D1D` | broken. Only broken. |
| `--info` / `--info-soft` / `--info-ink` | `#475569` / `#E2E8F0` / `#334155` | neutral callouts |

### Tokens — dark (`data-theme="dark"`)

| Token | Value |
| --- | --- |
| `--surface` / `--surface-2` / `--surface-3` | `#0B1220` / `#111827` / `#1F2937` |
| `--border` / `--border-strong` | `#1F2937` / `#334155` |
| `--ink` / `--ink-2` / `--muted` | `#E2E8F0` / `#CBD5E1` / `#94A3B8` |
| `--primary` / `--primary-hover` | `#38B6E0` / `#7FD3F0` |
| `--primary-soft` / `--primary-ink` / `--on-primary` | `#08283A` / `#AEE0F4` / `#04222F` |
| `--ok` / `--ok-soft` / `--ok-ink` | `#22C55E` / `#0F2A1A` / `#86EFAC` |
| `--warn` / `--warn-soft` / `--warn-ink` | `#F59E0B` / `#2A1E07` / `#FCD34D` |
| `--err` / `--err-soft` / `--err-ink` | `#F87171` / `#2A1010` / `#FCA5A5` |

### Rules

- **Tokens, never hex.** Components reference `var(--…)`; only the two
  theme blocks contain literal colors. This is what makes dark mode and
  future rebrands one-file changes.
- **Soft/ink pairs travel together.** Text on a `-soft` background uses
  the matching `-ink` — never `--ink` or raw color.
- **Status colors are anchored.** The v2 rebrand changed the primary
  (teal → azure) and deliberately did NOT touch ok/warn/err: semantic
  meaning must survive rebrands.
- Theme is `data-theme="light" | "dark"` on `<html>`, applied by an
  inline script before the stylesheet loads (no flash). Light is the
  default; "auto" follows the OS. Theme choice is a per-device user
  setting, not a per-session toggle.

---

## 3. Typography

- **UI / prose:** Inter (400 / 500 / 600 / 700), fallback to the system
  stack. Line-height 1.5 body, ~1.55 for muted explanatory text.
- **Code / identifiers / data:** `ui-monospace` stack (JetBrains Mono
  where loaded) at `0.92em`. Anything a machine produced — IDs, slugs,
  versions, conditions, counts in pills — is mono.

Scale (px, from the product):

| Role | Size / weight |
| --- | --- |
| Page title (`.page__title`) | 22 / 600 |
| Card header | 16 / 600 |
| Section heading (settings `h3`) | 14 / 600 |
| Body | 13.5–14 / 400 |
| Muted intro / hints | 12.5–13 / 400, `--muted` |
| Table meta, badges | 12 / 400–500 |
| Pills, uppercase labels | 10–11 / 600–700, tracking-wide |

Nav group headers and pill labels are the only UPPERCASE text in the
system. Everything else is sentence case.

---

## 4. Chrome anatomy (application surfaces)

```
┌─────────────────────────────────────────────────────────────┐
│ ☰ [S] Sluicio │ Org · Breadcrumb · ⌕ search · live · env · 🕒 · 🔔 · RM │  top bar, --surface-2
├──────────┬──────────────────────────────────────────────────┤
│ MONITOR  │  banner slot (announcements, MFA, limits)        │
│ Dashboard│                                                  │
│ …        │  main content, --surface                         │
│ CONFIGURE│                                                  │
│ ADMIN    │                                                  │
└──────────┴──────────────────────────────────────────────────┘
```

- Top bar: 48px, `--surface-2`, hairline bottom border, and a **2px
  `--primary` accent line across the very top** — the one place the
  brand color runs edge to edge.
- Sidebar: 200px, `--surface-2`, groups Monitor / Configure / Admin
  with uppercase 10px headers. Active item: `--primary-soft` background,
  `--primary-ink` text, `--primary` icon, weight 600. Items the user
  can't use are hidden, not disabled. Collapsible via the top-bar
  hamburger; preference persists per browser.
- Banner slot: page-level, above content — announcements, then
  enforcement banners (MFA, plan limits). Banners use the alert styles
  (§5) and never push more than a few lines.
- Page: `.page__header` with 22px title + muted subtitle (max-width
  640), 24px below. Content is cards and flat sections, never raw text
  on `--surface`.

---

## 5. Component idioms

**Cards** — `--surface-2`, 1px `--border`, radius 12, `--shadow-sm`;
16px padding; optional bordered header strip for list cards
("Your templates · 12").

**Flat settings sections** — the System-tab pattern, for stacked
configuration on one page: top-border divider, `margin-top: 28px`,
`padding-top: 20px`, 14px/600 `h3`, muted 13px intro paragraph, then
the form (max-width ~640–720). Sibling sections on one tab must all use
this — no mixing boxed cards between flat sections.

**Buttons** — `.btn` (neutral, `--surface-2` + border), `.btn--primary`
(azure, `--on-primary` text), `.btn--sm`, `.btn--danger` (err-tinted,
destructive only), `.btn--link` (inline text action). One primary
button per view region. Busy states swap the label ("Saving…"), never
spinners next to text.

**Pills / badges** — mono, 10–11px, weight 700, uppercase, radius 999,
either outlined (border in the status color) or soft-filled
(`-soft` bg + `-ink` text). Used for statuses (ENTERPRISE, ACTIVE,
CRITICAL, BUILT-IN) and counts. A pill is a fact, not a button.

**Alerts / banners** — `.alert` + `--info|--warn|--error` variants:
soft background, matching ink, 13.5px, optional action button pushed
right (`margin-left: auto`), optional `×` dismiss. Dismissal that
should stick is stored server-side per user, not in localStorage.

**Tabs** — page-level content tabs (`.svc-tabs`) with count chips;
settings pages use the underline style (2px `--primary` on the active
tab). Tab state that should be shareable lives in `?tab=` query params.

**Tables** — hairline row borders, `--surface-3` hover, muted meta
columns, right-aligned action column of `.btn--link`s. Row primary text
is a real `<button>`/link when it opens something.

**Drawers (blades)** — detail/edit surfaces slide in from the right
(EditDrawer) rather than navigating away; the row click opens, explicit
Edit buttons also open. Modals are avoided; `window.confirm` is used
for destructive confirmation with consequence-stating copy.

**Forms** — `.form__label` (label wraps input), `.search__input`
styling for all text inputs/selects, quick-pick `<select>`s over free
text where values are enumerable (durations, severities), typeahead
`SearchableSelect` for one-of-hundreds, chips for multi-select.
Submit disabled until valid; errors in an `.alert--error` above or
inline below the control.

**Empty states** — `.placeholder`: say what this list is and the next
action to fill it, with the action named exactly as its button.

---

## 6. Interaction & content patterns

- **Progressive disclosure**: rows expand (▸/▾) for detail; both the
  chevron and the name are clickable.
- **Optimistic where cheap, honest where not**: dismissing a banner
  hides immediately and resyncs on failure; saving config waits for
  the server.
- **Live data breathes quietly**: slow polls (10–60s), a subtle "live"
  pulse, no loading flashes on refresh (keep old data until new
  arrives).
- **Hints self-destruct**: onboarding hints (fresh-install credentials,
  first-run pointers) render only while relevant and disappear forever
  once the condition passes. A hint that lingers is noise.
- **Everything a user changes answers "who did this?"** — org-visible
  mutations get audit entries; UI copy may reference that fact.
- **Bounded by design**: anything that silences, caps, or overrides has
  an expiry or an explicit end (maintenance windows ≤7 days, trials
  expire). No forever-switches for dangerous states.
- **External links** open in a new tab with `rel="noreferrer"`; the
  version string in the footer links to the GitHub releases.

---

## 7. Accessibility contract

- Every interactive element shows the canonical focus ring: 3px
  `--focus` glow (azure at 35% alpha), applied as `box-shadow` on
  `:focus-visible` so radii are respected.
- Icon-only buttons carry `aria-label`s; decorative SVGs are
  `aria-hidden`.
- Contrast: `--ink` on any surface and every `-ink`-on-`-soft` pair
  meet WCAG AA. Don't invent new text/background combinations outside
  the token pairs.
- Color never carries meaning alone — status pills say the word
  (FIRING, ACTIVE), not just the hue.

---

## 8. Applying this beyond the product

**Docs site (sluicio-docs, Astro/Starlight)** — map Starlight theme
variables onto the tokens: accent = sluice azure (`#0E6E9E` light /
`#38B6E0` dark), backgrounds from the surface stack, both theme modes.
Code blocks use the mono stack. The Block-S mark in the header at 24px.

**Website / marketing** — same palette discipline: neutral-first pages
where azure is the accent and status colors appear only when depicting
actual product states (screenshots, mock alerts). Headlines Inter
600/700, sentence case. Claims follow the voice rules (§1) — defendable,
concrete, no drama.

**Emails / PDFs / slides** — white or `--surface` backgrounds, ink
text, azure accents, the lockup top-left, mono for anything technical.
Transactional email already follows this via the built-in alert
template; match it.

**New tools/services** — start from the token block (copy the two
`:root` sections of `frontend/src/styles.css` verbatim), the focus
contract, and §5's idioms. If a new pattern is needed, build it in the
product first, then document it here.

---

*Reference implementation: `frontend/src/styles.css` (tokens),
`frontend/src/components/brand/Logo.tsx` (mark),
`frontend/src/components/AppShell.tsx` (chrome). Questions this
document doesn't answer are design decisions — make them in the
product, then update this document.*
