// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The property: whatever is broken is at the top.
//
// A dashboard is read top-left first, and the previous orderings —
// traffic volume for integrations, pin order for systems — could put the
// one unhealthy card below a screenful of healthy ones. These tests
// state the order as a rule rather than checking a particular list.

import { describe, expect, it } from "vitest";
import { byHealthThenName, healthRank } from "./healthOrder";

interface Card {
  status?: string;
  name: string;
}
const sortCards = (cards: Card[]) =>
  cards
    .slice()
    .sort(byHealthThenName<Card>((c) => c.status, (c) => c.name))
    .map((c) => c.name);

describe("healthRank", () => {
  it("puts unhealthy first and idle last", () => {
    const order = ["unhealthy", "errors", "ok", "quiet", undefined];
    const ranks = order.map(healthRank);
    expect(ranks).toEqual([...ranks].sort((a, b) => a - b));
    expect(new Set(ranks).size).toBe(order.length);
  });

  it("ranks a status it has never heard of last, not first", () => {
    // A new server-side status must not jump the queue and push a real
    // problem below the fold.
    expect(healthRank("degraded")).toBeGreaterThan(healthRank("unhealthy"));
    expect(healthRank("degraded")).toBeGreaterThanOrEqual(healthRank("quiet"));
  });
});

describe("byHealthThenName", () => {
  it("lifts the one broken card above every healthy one", () => {
    expect(
      sortCards([
        { status: "ok", name: "alpha" },
        { status: "ok", name: "bravo" },
        { status: "unhealthy", name: "zulu" },
      ])[0],
    ).toBe("zulu");
  });

  it("orders the whole health ladder", () => {
    expect(
      sortCards([
        { status: "quiet", name: "d" },
        { status: "ok", name: "c" },
        { status: "errors", name: "b" },
        { status: "unhealthy", name: "a" },
      ]),
    ).toEqual(["a", "b", "c", "d"]);
  });

  it("falls back to name within one health state", () => {
    expect(
      sortCards([
        { status: "ok", name: "charlie" },
        { status: "ok", name: "alpha" },
        { status: "ok", name: "bravo" },
      ]),
    ).toEqual(["alpha", "bravo", "charlie"]);
  });

  it("does not reorder a healthy board as traffic moves", () => {
    // The tiebreak is the name, not a live number — so a board with
    // nothing wrong holds still instead of shuffling on every refresh.
    const cards = [
      { status: "ok", name: "bravo" },
      { status: "ok", name: "alpha" },
    ];
    expect(sortCards(cards)).toEqual(sortCards(cards.slice().reverse()));
  });

  it("sorts names the way a reader expects, not by code point", () => {
    // "Å" is above "Z" in code points but belongs with the A's.
    expect(
      sortCards([
        { status: "ok", name: "Zulu" },
        { status: "ok", name: "Åre" },
        { status: "ok", name: "Alpha" },
      ]),
    ).toEqual(["Alpha", "Åre", "Zulu"]);
  });

  it("keeps a card whose status is missing — last, but present", () => {
    // A system the page could not resolve still has to render, or it
    // cannot be removed from the board.
    expect(
      sortCards([
        { name: "unknown-card" },
        { status: "unhealthy", name: "broken" },
      ]),
    ).toEqual(["broken", "unknown-card"]);
  });
});
