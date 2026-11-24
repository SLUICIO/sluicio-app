// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, expect, it } from "vitest";
import type { Integration, IntegrationStats } from "../api/types";
import { carryStats, fillStatsInBatches, mergeStats, prioritise } from "./integrationStats";

const row = (id: string, extra: Partial<Integration> = {}) => ({ id, name: id, ...extra }) as Integration;

describe("mergeStats", () => {
  it("folds each batch into its own row and leaves the rest alone", () => {
    const rows = [row("a"), row("b")];
    const out = mergeStats(rows, [{ id: "b", trace_count: 7, status: "ok" } as IntegrationStats]);
    expect(out[0].trace_count).toBeUndefined();
    expect(out[1].trace_count).toBe(7);
    expect(out[1].name).toBe("b");
  });
});

describe("carryStats", () => {
  it("keeps the last numbers while a refresh re-reads the names", () => {
    const prev = [row("a", { trace_count: 5, status: "errors" }), row("gone", { trace_count: 1 })];
    const out = carryStats(prev, [row("a", { name: "A renamed" }), row("new")]);
    expect(out.map((r) => r.id)).toEqual(["a", "new"]);
    expect(out[0].name).toBe("A renamed");
    expect(out[0].trace_count).toBe(5);
    expect(out[0].status).toBe("errors");
    expect(out[1].trace_count).toBeUndefined();
  });
});

describe("prioritise", () => {
  it("puts the cards on screen first and drops ids that are not in the list", () => {
    expect(prioritise(["a", "b", "c", "d"], ["c", "x", "a"])).toEqual(["c", "a", "b", "d"]);
  });
});

describe("fillStatsInBatches", () => {
  it("asks one batch at a time, in order", async () => {
    const asked: string[][] = [];
    const done: string[][] = [];
    let inFlight = 0;
    let maxInFlight = 0;
    await fillStatsInBatches(
      ["a", "b", "c", "d", "e"],
      async (batch) => {
        inFlight++;
        maxInFlight = Math.max(maxInFlight, inFlight);
        asked.push(batch);
        await Promise.resolve();
        inFlight--;
        return batch.map((id) => ({ id }) as IntegrationStats);
      },
      { isCurrent: () => true, stats: () => {}, done: (b) => done.push(b) },
      2,
    );
    expect(asked).toEqual([["a", "b"], ["c", "d"], ["e"]]);
    expect(done).toEqual(asked);
    expect(maxInFlight).toBe(1);
  });

  it("reports a failed batch done without stats and carries on", async () => {
    const got: string[] = [];
    const done: string[][] = [];
    await fillStatsInBatches(
      ["a", "b"],
      async (batch) => {
        if (batch[0] === "a") throw new Error("boom");
        return [{ id: "b" } as IntegrationStats];
      },
      { isCurrent: () => true, stats: (s) => got.push(...s.map((x) => x.id)), done: (b) => done.push(b) },
      1,
    );
    expect(got).toEqual(["b"]);
    expect(done).toEqual([["a"], ["b"]]);
  });

  it("stops writing once a newer pass has replaced it", async () => {
    let current = true;
    const got: string[] = [];
    await fillStatsInBatches(
      ["a", "b", "c"],
      async (batch) => {
        current = false;
        return batch.map((id) => ({ id }) as IntegrationStats);
      },
      { isCurrent: () => current, stats: (s) => got.push(...s.map((x) => x.id)), done: () => {} },
      1,
    );
    expect(got).toEqual([]);
  });
});
