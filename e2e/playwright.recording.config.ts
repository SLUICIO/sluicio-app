// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Playwright config for recorded walkthroughs (e2e/recordings), kept
// apart from the test suite: different files (*.rec.ts), a fixed
// viewport, video always on, no retries, and never a dev server of its
// own. See recordings/README.md.

import { defineConfig } from "@playwright/test";
import { loadRecordingEnv } from "./recordings/stagecraft";

loadRecordingEnv();

const width = Number(process.env.RECORDING_WIDTH ?? "1440");
const height = Number(process.env.RECORDING_HEIGHT ?? "900");

export default defineConfig({
  testDir: "./recordings",
  testMatch: "**/*.rec.ts",
  outputDir: "./recordings-output/raw",
  // A take is slow on purpose; give it room.
  timeout: 5 * 60_000,
  retries: 0,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: process.env.RECORDING_BASE_URL,
    viewport: { width, height },
    // 2x the CSS pixels, so text stays sharp when the video is scaled up
    // or zoomed in the edit.
    deviceScaleFactor: 2,
    video: { mode: "on", size: { width, height } },
    colorScheme: process.env.RECORDING_THEME === "dark" ? "dark" : "light",
    // Headed when somebody wants to capture the screen with their own
    // recorder instead (RECORDING_HEADED=1).
    headless: process.env.RECORDING_HEADED !== "1",
    trace: "retain-on-failure",
  },
});
