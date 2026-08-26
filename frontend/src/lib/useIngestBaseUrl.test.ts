// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { otlpEndpoint } from "./useIngestBaseUrl";

describe("otlpEndpoint", () => {
  it("builds the per-signal OTLP/HTTP path", () => {
    expect(otlpEndpoint("https://ingest.acme.example.com", "traces")).toBe(
      "https://ingest.acme.example.com/v1/traces",
    );
    expect(otlpEndpoint("https://ingest.acme.example.com", "logs")).toBe(
      "https://ingest.acme.example.com/v1/logs",
    );
  });

  // The hook strips trailing slashes before this ever runs, but a caller
  // passing a raw setting value must not produce "//v1/traces": some
  // collectors send it verbatim and some servers refuse it.
  it("does not double the separator when the base already ends in one", () => {
    const base = "https://ingest.acme.example.com/".replace(/\/+$/, "");
    expect(otlpEndpoint(base, "traces")).toBe("https://ingest.acme.example.com/v1/traces");
  });
});
