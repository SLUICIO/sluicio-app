// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// How a secret written into a collector's configuration in plain text
// reads. The server keeps where it is, never what it is.

// plainSecretsSummary is the sentence for the collector's page (or, with
// lead, for a configuration about to be sent), or "" when there is none.
export function plainSecretsSummary(
  paths?: string[] | null,
  shown = 5,
  lead = "Its configuration has",
): string {
  if (!paths || paths.length === 0) return "";
  const more = paths.length - shown;
  const count = paths.length === 1 ? "a secret" : `${paths.length} secrets`;
  return (
    `${lead} ${count} written in plain text: ${paths.slice(0, shown).join(", ")}` +
    (more > 0 ? `, and ${more} more` : "") +
    "."
  );
}
