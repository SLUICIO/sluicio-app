// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// @vitest-environment node
import { describe, expect, it } from "vitest";
import { otelflowEmbedUrl, otelflowPayload } from "./otelflowEmbed";

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
