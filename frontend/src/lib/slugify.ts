// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Deriving a URL-safe slug from a free-text name.
//
// Shared so the new-integration form and the clone dialog cannot drift:
// both feed the same server-side slug column, and a slug the server
// rejects is a dead end the user cannot diagnose from the message.

/**
 * Lowercase, non-alphanumerics collapsed to single dashes, trimmed —
 * matching the slug input's [a-z0-9-]+ pattern.
 */
export function slugify(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
