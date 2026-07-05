import { build } from "vite";
import react from "@vitejs/plugin-react";
await build({
  configFile: false,
  root: process.cwd(),
  plugins: [react()],
  build: { outDir: "/tmp/conduit-build-3", emptyOutDir: true, logLevel: "warn" },
});
