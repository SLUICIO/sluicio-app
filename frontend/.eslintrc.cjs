/* SPDX-License-Identifier: FSL-1.1-Apache-2.0 */
module.exports = {
  root: true,
  env: { browser: true, es2022: true },
  extends: [
    "eslint:recommended",
    "plugin:@typescript-eslint/recommended",
    "plugin:react-hooks/recommended",
  ],
  ignorePatterns: ["dist", ".eslintrc.cjs", "node_modules"],
  parser: "@typescript-eslint/parser",
  plugins: ["react-refresh"],
  rules: {
    // Dev-only hint about Vite fast-refresh granularity: it fires when a
    // module exports a component alongside a small helper / constant /
    // context. We deliberately co-locate those; it has no bearing on
    // production output or correctness, so it's off. The react-hooks
    // rules (real correctness signals) stay on via the recommended config.
    "react-refresh/only-export-components": "off",
  },
};
