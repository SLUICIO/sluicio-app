// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  otelflowConfigHeight,
  otelflowEmbedUrl,
  otelflowFallbackVersion,
  otelflowPayload,
} from "./otelflowEmbed";

function fromB64Url(s: string): Uint8Array {
  const bin = atob(s.replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

// Decodes the way OTelFlow's own share.ts does, so the payload is what it reads.
async function decode(payload: string): Promise<{ v: string; c: string }> {
  const [kind, data] = [
    payload.slice(0, payload.indexOf(".")),
    payload.slice(payload.indexOf(".") + 1),
  ];
  let bytes = fromB64Url(data);
  if (kind === "1") {
    const body = new Response(bytes as BodyInit).body!;
    bytes = new Uint8Array(
      await new Response(
        body.pipeThrough(new DecompressionStream("deflate-raw")),
      ).arrayBuffer(),
    );
  }
  return JSON.parse(new TextDecoder().decode(bytes));
}

describe("otelflowPayload", () => {
  it("round-trips the configuration and version in OTelFlow's format", async () => {
    const yaml = "receivers:\n  otlp: {}\n# åäö and a long line ".repeat(20);
    const payload = await otelflowPayload(yaml, "0.166.0");
    expect(payload).toMatch(/^[01]\.[A-Za-z0-9_-]+$/);
    expect(await decode(payload)).toEqual({ v: "0.166.0", c: yaml });
  });
});

describe("otelflowEmbedUrl", () => {
  it("puts the payload in the fragment, never the path or query", () => {
    const url = otelflowEmbedUrl("/otelflow", "1.abc");
    expect(url).toBe("/otelflow/#embed=1.abc&view=canvas&theme=light");
    expect(url.split("#")[0]).not.toContain("abc");
  });
});

describe("otelflowFallbackVersion", () => {
  const meta = { versions: ["0.162.0", "0.163.0"], defaultVersion: "0.163.0" };
  it("is null when OTelFlow knows the reported version", () => {
    expect(otelflowFallbackVersion(meta, "0.163.0")).toBeNull();
    expect(otelflowFallbackVersion(meta, "v0.162.0")).toBeNull();
  });
  it("names the version OTelFlow checks against instead", () => {
    expect(otelflowFallbackVersion(meta, "0.166.0")).toBe("0.163.0");
  });
  it("says nothing without a version or without meta", () => {
    expect(otelflowFallbackVersion(meta, "")).toBeNull();
    expect(otelflowFallbackVersion(null, "0.166.0")).toBeNull();
  });
});

describe("otelflowConfigHeight", () => {
  it("follows the line count within OTelFlow's bounds", () => {
    expect(otelflowConfigHeight("a: 1")).toBe(190);
    expect(otelflowConfigHeight("x\n".repeat(20))).toBe(460); // 21 lines
    expect(otelflowConfigHeight("x\n".repeat(500))).toBe(750);
  });
});
