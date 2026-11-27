// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The address an example value would open, for the settings page only.
// The links a reader follows are resolved by the server
// (internal/payloadlinks.Resolve); this mirrors its rule closely enough to
// show an admin what a base URL does before saving.

export function previewPayloadLink(base: string, value: string): string | null {
  const v = value.trim();
  if (!v) return null;
  if (!base) return /^https?:\/\/[^/\s]+/i.test(v) ? v : null;
  if (!/^https?:\/\/[^/\s#]+/i.test(base)) return null;
  if (base.endsWith("=") || base.endsWith("?")) return base + encodeURIComponent(v).replace(/%20/g, "+");
  const path = v
    .replace(/^\/+/, "")
    .split("/")
    .map((seg) => encodeURIComponent(seg))
    .join("/");
  if (v.split("/").includes("..")) return null;
  return base.replace(/\/+$/, "") + "/" + path;
}
