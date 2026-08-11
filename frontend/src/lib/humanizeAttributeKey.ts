// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The first-guess column heading for a promoted attribute (issue #23).
//
// Deliberately a mirror of integrations.HumanizeKey in Go, which is the
// authority: the server fills a blank label the same way, so a column
// added from the picker and a column added by PUTting a bare key must
// come out identically. If these two drift, the same attribute gets two
// different headings depending on which path created it.
//
// The rules, and why:
//   - Separators become spaces and the first letter is capitalised.
//   - The leading namespace is dropped only at THREE or more dotted
//     segments. At three the first is nearly always a vendor or signal
//     namespace (node_red., http., messaging.) and repeating it inside
//     an integration that already names the system is noise. At two it
//     is just as likely to be the subject — dropping it would turn
//     "documents.exported" into "Exported", losing the word that
//     mattered.
//
// No rule is right for every key, because a namespace is sometimes the
// scope and sometimes the subject. This is a pre-fill the user edits.

export function humanizeAttributeKey(key: string): string {
  let s = key.trim();
  if (s === "") return "";
  const parts = s.split(".");
  if (parts.length >= 3) s = parts.slice(1).join(".");
  s = s.replace(/[._-]+/g, " ").trim().replace(/\s+/g, " ");
  if (s === "") return key.trim();
  return s[0].toUpperCase() + s.slice(1);
}
