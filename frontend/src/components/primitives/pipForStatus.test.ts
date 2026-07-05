// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, it, expect } from "vitest";
import { pipForStatus } from "./pipForStatus";

describe("pipForStatus", () => {
  it.each([
    [undefined, "muted"],
    ["ok", "ok"],
    ["quiet", "muted"],
    ["errors", "err"],
    ["unhealthy", "err"],
    ["something-unknown", "muted"],
  ] as const)("maps %s → %s", (status, kind) => {
    expect(pipForStatus(status)).toBe(kind);
  });
});
