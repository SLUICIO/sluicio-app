// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A line diff for reading a configuration change: the longest common
// subsequence of lines, then what was removed and added around it.
// Configurations are a few hundred lines, so the quadratic table is fine.

export interface DiffLine {
  op: " " | "-" | "+";
  text: string;
}

export function lineDiff(before: string, after: string): DiffLine[] {
  const a = before.replace(/\n$/, "").split("\n");
  const b = after.replace(/\n$/, "").split("\n");
  const n = a.length;
  const m = b.length;
  const lcs: number[][] = Array.from({ length: n + 1 }, () =>
    new Array<number>(m + 1).fill(0),
  );
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] =
        a[i] === b[j]
          ? lcs[i + 1][j + 1] + 1
          : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ op: " ", text: a[i] });
      i++;
      j++;
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ op: "-", text: a[i++] });
    } else {
      out.push({ op: "+", text: b[j++] });
    }
  }
  while (i < n) out.push({ op: "-", text: a[i++] });
  while (j < m) out.push({ op: "+", text: b[j++] });
  return out;
}

/** True when the two texts differ in any line. */
export function hasChanges(lines: DiffLine[]): boolean {
  return lines.some((l) => l.op !== " ");
}

/**
 * A unified diff (`diff -u`, three lines of context), for a team whose
 * pipeline owns a collector's configuration to apply in its repository
 * with `git apply` or `patch`. `file` names both sides.
 */
export function unifiedDiff(before: string, after: string, file: string, context = 3): string {
  const lines = lineDiff(before, after);
  if (!hasChanges(lines)) return "";
  // Line numbers on each side, for the hunk headers.
  const at: { a: number; b: number }[] = [];
  let a = 1;
  let b = 1;
  for (const l of lines) {
    at.push({ a, b });
    if (l.op !== "+") a++;
    if (l.op !== "-") b++;
  }
  const out = [`--- a/${file}`, `+++ b/${file}`];
  let i = 0;
  while (i < lines.length) {
    if (lines[i].op === " ") {
      i++;
      continue;
    }
    // A hunk: from context lines before the first change to context lines
    // after the last change that is within 2*context of the next one.
    const start = Math.max(0, i - context);
    let end = i;
    for (let k = i; k < lines.length; k++) {
      if (lines[k].op !== " ") end = k;
      else if (k - end > 2 * context) break;
    }
    const stop = Math.min(lines.length, end + context + 1);
    const hunk = lines.slice(start, stop);
    const aLen = hunk.filter((l) => l.op !== "+").length;
    const bLen = hunk.filter((l) => l.op !== "-").length;
    const aStart = aLen ? at[start].a : at[start].a - 1;
    const bStart = bLen ? at[start].b : at[start].b - 1;
    out.push(`@@ -${aStart},${aLen} +${bStart},${bLen} @@`);
    for (const l of hunk) out.push(`${l.op}${l.text}`);
    i = stop;
  }
  return out.join("\n") + "\n";
}
