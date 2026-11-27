// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Embedding OTelFlow (the Apache-2.0 collector-configuration designer) to
// draw a collector's configuration. OTelFlow's embed is a page whose URL
// fragment carries the whole configuration, in the format its README
// documents as stable: "#embed=<kind>.<base64url>", kind 1 = the JSON
// {"v": version, "c": yaml} deflate-raw compressed, kind 0 = uncompressed.
// A fragment never reaches a server, and the cell serves OTelFlow itself
// (SLUICIO_OTELFLOW_URL), so the configuration stays in the browser and
// the cell.

function toB64Url(bytes: Uint8Array): string {
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function deflateRaw(bytes: Uint8Array): Promise<Uint8Array> {
  const body = new Response(bytes as BodyInit).body;
  if (!body) throw new Error("no stream");
  return new Uint8Array(
    await new Response(
      body.pipeThrough(new CompressionStream("deflate-raw")),
    ).arrayBuffer(),
  );
}

/** The fragment payload for a configuration and the collector version it targets. */
export async function otelflowPayload(
  yaml: string,
  version: string,
): Promise<string> {
  const raw = new TextEncoder().encode(JSON.stringify({ v: version, c: yaml }));
  if (typeof CompressionStream !== "undefined") {
    return "1." + toB64Url(await deflateRaw(raw));
  }
  return "0." + toB64Url(raw);
}

/**
 * The read-only embed URL. base is the cell's OTelFlow ("/otelflow/");
 * Sluicio's UI is light, so the embed is pinned to the light theme.
 */
export function otelflowEmbedUrl(
  base: string,
  payload: string,
  view: "canvas" | "config" | "both" = "canvas",
): string {
  const root = base.endsWith("/") ? base : base + "/";
  return `${root}#embed=${payload}&view=${view}&theme=light`;
}
