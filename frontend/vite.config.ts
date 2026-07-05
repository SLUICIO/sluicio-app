// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// defineConfig is imported from vitest/config (a superset of vite's) so
// the `test` block below is typed and shares the same plugins + aliases
// the app build uses. It remains a valid vite config for `vite build`.
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { createRequire } from "node:module";

// Surface the build version to the bundle as __APP_VERSION__ so the
// chrome can show it. The git-derived version (scripts/version.sh) is
// passed in via APP_VERSION at build time (publish.docker.sh + the
// frontend Dockerfile); a plain local build falls back to package.json.
const pkg = createRequire(import.meta.url)("./package.json") as {
  version: string;
};
const appVersion = process.env.APP_VERSION || pkg.version;

export default defineConfig({
  plugins: [react()],
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
