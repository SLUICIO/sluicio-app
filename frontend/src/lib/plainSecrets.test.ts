// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { plainSecretsSummary } from "./plainSecrets";

describe("plainSecretsSummary", () => {
  it("says nothing without secrets", () => {
    expect(plainSecretsSummary(undefined)).toBe("");
    expect(plainSecretsSummary([])).toBe("");
  });
  it("names where they are, a few at most", () => {
    expect(
      plainSecretsSummary(["exporters.otlp_http/sluicio.headers.x-api-key"]),
    ).toBe(
      "Its configuration has a secret written in plain text: exporters.otlp_http/sluicio.headers.x-api-key.",
    );
    expect(plainSecretsSummary(["a", "b", "c"], 2)).toBe(
      "Its configuration has 3 secrets written in plain text: a, b, and 1 more.",
    );
    expect(plainSecretsSummary(["a"], 5, "This change has")).toBe(
      "This change has a secret written in plain text: a.",
    );
  });
});
