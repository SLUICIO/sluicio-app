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

/**
 * Height for the read-only config view, the way OTelFlow's own share
 * dialog computes it: lines at about 19px, clamped, plus its 40px bar.
 * An embed is a snapshot, so the height stays right for it.
 */
export function otelflowConfigHeight(yaml: string): number {
  const lines = yaml.split("\n").length;
  const content = Math.min(Math.max(lines * 19 + 12, 140), 700);
  return Math.ceil((content + 40 + 2) / 10) * 10;
}

export interface OtelflowMeta {
  versions: string[];
  defaultVersion: string;
}

// One request per page load and base: the versions an OTelFlow build
// knows only change with a new image.
const metaCache = new Map<string, Promise<OtelflowMeta | null>>();

/** The collector versions this cell's OTelFlow can check, or null. */
export function otelflowMeta(base: string): Promise<OtelflowMeta | null> {
  const root = base.endsWith("/") ? base : base + "/";
  let p = metaCache.get(root);
  if (!p) {
    p = fetch(`${root}api/meta`)
      .then((r) => (r.ok ? (r.json() as Promise<OtelflowMeta>) : null))
      .catch(() => null);
    metaCache.set(root, p);
  }
  return p;
}

/**
 * The version OTelFlow will actually check against when it does not know
 * the reported one (it falls back to its default without saying so), or
 * null when it knows it or there is nothing to compare.
 */
export function otelflowFallbackVersion(
  meta: OtelflowMeta | null,
  reported: string,
): string | null {
  const v = reported.replace(/^v/, "");
  if (!meta || !v || meta.versions.includes(v)) return null;
  return meta.defaultVersion;
}
