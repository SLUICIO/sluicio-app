// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Is a caret inside a JSON string literal?
//
// The webhook body editor inserts a reference at the caret, and the form
// it inserts depends on the answer: "$alert.summary" outside a string,
// ${alert.summary} inside one. Get it wrong and the author gets
// "…"$alert.summary"" — invalid JSON they then have to unpick.
//
// JSON strings cannot span lines, so the line up to the caret is enough.

/**
 * True when the caret sits inside a JSON string literal.
 *
 * @param head the text from the start of the line up to the caret.
 */
export function caretIsInJSONString(head: string): boolean {
  let inString = false;
  for (let i = 0; i < head.length; i++) {
    // A backslash escapes the next character, so \" does not close a
    // string. Outside a string a backslash is not meaningful in JSON, and
    // skipping one character there is harmless.
    if (head[i] === "\\") {
      i++;
      continue;
    }
    if (head[i] === '"') inString = !inString;
  }
  return inString;
}
