// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// defineConfig is imported from vitest/config (a superset of vite's) so
// the `test` block below is typed and shares the same plugins + aliases
// the app build uses. It remains a valid vite config for `vite build`.
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { createRequire } from "node:module";
import license from "rollup-plugin-license";

// Surface the build version to the bundle as __APP_VERSION__ so the
// chrome can show it. The git-derived version (scripts/version.sh) is
// passed in via APP_VERSION at build time (publish.docker.sh + the
// frontend Dockerfile); a plain local build falls back to package.json.
const pkg = createRequire(import.meta.url)("./package.json") as {
  version: string;
};
const appVersion = process.env.APP_VERSION || pkg.version;

// Licences a component bundled into the app may carry (issue #41): the
// same allowlist as the server components (scripts/thirdparty), plus
// Python-2.0 (argparse, permissive).
const ALLOWED_LICENSES = new Set([
  "MIT", "MIT-0", "ISC", "0BSD", "Zlib", "BSD-2-Clause", "BSD-3-Clause",
  "Apache-2.0", "MPL-2.0", "Unlicense", "CC0-1.0", "Python-2.0", "BlueOak-1.0.0",
]);

// An SPDX expression is allowed when one of its OR alternatives is made
// only of allowed licences: "(MIT OR GPL-3.0)" may be taken as MIT.
function licenseAllowed(expr: string | null): boolean {
  if (!expr) return false;
  return expr
    .replace(/[()]/g, " ")
    .split(/\s+OR\s+/i)
    .some((alt) => alt.split(/\s+AND\s+/i).every((id) => ALLOWED_LICENSES.has(id.trim())));
}

type Dep = { name: string | null; version: string | null; license: string | null; licenseText: string | null; noticeText: string | null };

// The bundle's third-party components, written next to it as
// third-party.json in the same shape as the server components' list, so
// the app's licence page reads both the same way. Only what the bundle
// actually contains: the plugin sees the modules Rollup included, not
// package.json. A licence outside the allowlist fails the build.
const thirdParty = license({
  thirdParty: {
    includePrivate: false,
    allow: {
      test: (dep) => licenseAllowed(dep.license),
      failOnUnlicensed: true,
      failOnViolation: true,
    },
    output: {
      file: path.resolve(__dirname, "dist/third-party.json"),
      template(deps: Dep[]) {
        const components = deps
          .map((d) => ({
            name: d.name ?? "",
            version: d.version ?? "",
            licenses: d.license ? [d.license] : [],
            files: [
              ...(d.licenseText ? [{ name: "LICENSE", text: d.licenseText }] : []),
              ...(d.noticeText ? [{ name: "NOTICE", text: d.noticeText }] : []),
            ],
          }))
          .sort((a, b) => a.name.localeCompare(b.name));
        return JSON.stringify({ product: "Sluicio web app", components }, null, 2) + "\n";
      },
    },
  },
});

export default defineConfig({
  plugins: [react(), thirdParty],
  define: {
    __APP_VERSION__: JSON.stringify(appVersion),
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
    },
  },
  server: {
    port: 5173,
    // Dual-stack loopback: with Node ≥17 "localhost" resolves to ::1
    // first, so vite otherwise listens IPv6-only and IPv4-only clients
    // (e.g. browser previews) get connection refused.
    host: "0.0.0.0",
    proxy: {
      "/api": {
        // Override with API_PROXY_TARGET to point the dev server at a
        // non-default cell-api (e.g. a throwaway instance on another port).
        target: process.env.API_PROXY_TARGET || "http://localhost:8081",
        changeOrigin: true,
      },
    },
  },
  test: {
    // jsdom gives component tests a DOM; node-only lib tests ignore it.
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    // Only our *.test.* files — never reach into node_modules.
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    css: false,
    clearMocks: true,
  },
});
