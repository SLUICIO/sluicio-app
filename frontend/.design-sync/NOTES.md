# design-sync notes — Sluicio frontend

## Repo shape
- This is a **Vite application**, not a published component library. There is
  no `dist/` of compiled components and no `package.json` component entry.
  Sync runs in **synth/src-entry mode**: esbuild bundles directly from source.
- Scope is deliberately the **shared presentational primitives only**
  (`src/components/primitives/` + `SeverityBadge`). The rest of `src/components/`
  is app-coupled (react-router, api/client, data) and will NOT render
  standalone — do not add those to the entry.

## Entry
- Custom scoped barrel: **`.design-sync/entry.ts`** re-exports exactly the 8
  components. Pass it to the converter as `--entry ./.design-sync/entry.ts`.
  (SeverityBadge lives outside the primitives barrel, hence the custom entry.)
- Path alias `@/* -> src/*` is in `tsconfig.json` (`cfg.tsconfig`). esbuild
  reads `compilerOptions.paths` for it. The primitives use relative imports
  (`../../lib/useTableSort`, type-only) which resolve from their own location.

## Components (8)
StatusPip, Sparkline, Donut, KpiCard, SortableTh, KVTable, EditDrawer (overlay —
will need `cfg.overrides.EditDrawer.cardMode: "single"`), SeverityBadge.

## Styling / tokens
- Tokens + component CSS both live in **`src/styles.css`** (`cfg.cssEntry`):
  `--bg`, `--surface`, `--surface-2`, `--border`, `--accent`/`--primary`,
  `--ok`, `--warn`/`--warning`, `--err`/`--critical`, `--ink`, `--muted`, fonts.

## Auth / environment
- The `DesignSync` tool requires `/login` (claude.ai design scopes); the
  session's CLAUDE_CODE_OAUTH_TOKEN cannot be upgraded. Local build + preview
  authoring + grading run without it; project-pick + upload need the login.

## Build recipe (run in order, from frontend/)
1. **Compile Tailwind FIRST** (cssEntry points at the output):
   `npx tailwindcss -i src/styles.css -o .design-sync/compiled.css`
   src/styles.css is raw `@tailwind` directives — copying it un-compiled
   ships 0 utility classes, so KpiCard (`p-4 rounded-xl shadow-sm`) +
   StatusPip (`inline-flex gap`) lose their styling. The compiled file is
   gitignored + regenerated each sync.
2. `node .ds-sync/package-build.mjs --config design-sync.config.json --node-modules ./node_modules --entry ./.design-sync/entry.ts --out ./ds-bundle`
3. `node .ds-sync/package-validate.mjs ./ds-bundle`
4. `node .ds-sync/package-capture.mjs --out ./ds-bundle`  (grades carry forward)

## Fonts decision
- Inter + JetBrains Mono load via a Google Fonts `<link>` in index.html (NOT
  in styles.css). `cfg.runtimeFontPrefixes` suppresses [FONT_MISSING] honestly
  (host serves them at runtime). In the design tool, designs fall back to the
  CSS fallback chain (-apple-system / ui-monospace) unless the tool loads
  Google Fonts. **Deferred decision for the user:** ship Inter/JetBrains woff2
  via `cfg.extraFonts` for guaranteed in-tool fidelity. Local render/grade ran
  in system fallback; layout + color were unaffected.

## Status (first sync, local prep done)
- 8 components, all authored previews (16 cells) graded **good**; render check
  8/8 clean; bundle validate exit 0.
- Upload pending `/login` (DesignSync needs claude.ai design scopes). Bundle
  is at `ds-bundle/`; pick project + upload per skill §5 once authed.

## Re-sync risks
- Synth/src-entry mode: `.d.ts` contracts are extracted by ts-morph from
  source `.tsx`, weaker than a real build's emitted types. If prop extraction
  misfires, use `cfg.dtsPropsFor.<Name>`.
- The Tailwind compile (step 1) is a REQUIRED manual pre-step — easy to forget
  on re-sync; without it the bundle silently loses utility classes.
- No app build pins the toolchain; output is deterministic from source +
  pinned tailwind 3.4. Re-grade if `src/styles.css` tokens move.
- `.design-sync/entry.ts` is hand-maintained — if a primitive is added/renamed
  in the barrel, update entry.ts + componentSrcMap to match.
