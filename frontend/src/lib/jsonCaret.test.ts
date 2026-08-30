// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { caretIsInJSONString } from "./jsonCaret";

describe("caretIsInJSONString", () => {
  it("is false at the start of a line", () => {
    expect(caretIsInJSONString("")).toBe(false);
    expect(caretIsInJSONString("  ")).toBe(false);
  });

  it("is false after a key, before the value", () => {
    expect(caretIsInJSONString('  "subject": ')).toBe(false);
  });

  it("is true inside a value", () => {
    expect(caretIsInJSONString('  "subject": "[')).toBe(true);
  });

  it("is false after a closed value", () => {
    expect(caretIsInJSONString('  "subject": "hello",')).toBe(false);
  });

  it("does not end a string on an escaped quote", () => {
    expect(caretIsInJSONString('  "subject": "he said \\"hi')).toBe(true);
  });

  // The case that broke the regex this replaced: a quote-count that
  // consumes the character before each quote reads "" as one quote, so
  // two inserted references in a row flipped the answer.
  it("counts adjacent quotes as two", () => {
    expect(caretIsInJSONString('  "a": "x""y"')).toBe(false);
    expect(caretIsInJSONString('  "a": "x""')).toBe(true);
  });

  it("handles a trailing backslash without running past the end", () => {
    expect(caretIsInJSONString('  "a": "x\\')).toBe(true);
  });
});
