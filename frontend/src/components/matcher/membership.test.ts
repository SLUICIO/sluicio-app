// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The membership editor's reasoning, as sentences: what the add box turns
// text into, what a member says about itself, and what the save bar
// claims is about to change.

import { describe, expect, it } from "vitest";
import { blankRule, type Rule } from "../MatcherRules";
import type { NeighborsResponse } from "../../api/types";
import {
  conditionsPhrase,
  describeChanges,
  memberPhrase,
  mergeSuggestions,
  parseAddQuery,
  windowPhrase,
} from "./membership";

const svc = (service: string, extra: Partial<Rule> = {}) => blankRule({ serviceOp: "equals", service, ...extra });

describe("what the add box adds", () => {
  it("adds a plain name as that one service", () => {
    expect(parseAddQuery("  order-gateway ")).toMatchObject({ serviceOp: "equals", service: "order-gateway" });
  });

  it("reads a star the way a shell does", () => {
    expect(parseAddQuery("order-*")).toMatchObject({ serviceOp: "prefix", service: "order-" });
    expect(parseAddQuery("*-worker")).toMatchObject({ serviceOp: "suffix", service: "-worker" });
    expect(parseAddQuery("*pay*")).toMatchObject({ serviceOp: "contains", service: "pay" });
  });

  it("makes a star in the middle a regex, with the rest taken literally", () => {
    const r = parseAddQuery("order.*.v2")!;
    expect(r.serviceOp).toBe("regex");
    expect(new RegExp(r.service).test("order.eu.v2")).toBe(true);
    // The dots are dots, not "any character".
    expect(new RegExp(r.service).test("orderXeuXv2")).toBe(false);
  });

  it("takes /.../ as a regex as written", () => {
    expect(parseAddQuery("/^pay(ment)?-/")).toMatchObject({ serviceOp: "regex", service: "^pay(ment)?-" });
  });

  // A lone star takes in every service in the cell: somebody still typing.
  it("refuses a pattern that matches everything, and empty input", () => {
    expect(parseAddQuery("*")).toBeNull();
    expect(parseAddQuery("**")).toBeNull();
    expect(parseAddQuery("   ")).toBeNull();
  });
});

describe("what a member says about itself", () => {
  it("says all traffic when nothing narrows it", () => {
    expect(conditionsPhrase(svc("a"))).toBe("All traffic");
  });

  it("spells the conditions out, joined the way they combine", () => {
    const r = svc("a", {
      combine: "all",
      descendants: true,
      attrs: [
        { attribute: "messaging.destination", operator: "equals", value: "orders" },
        { attribute: "tenant", operator: "exists", value: "" },
      ],
    });
    expect(conditionsPhrase(r)).toBe("Only where messaging.destination is orders and tenant is set, with child spans");
  });

  // A half-typed condition is not part of the rule; the summary must not
  // claim a narrowing the save would not store.
  it("leaves half-typed conditions out", () => {
    const r = svc("a", { attrs: [{ attribute: "x", operator: "equals", value: "" }] });
    expect(conditionsPhrase(r)).toBe("All traffic");
  });

  it("names a pattern by how it matches", () => {
    expect(memberPhrase(blankRule({ serviceOp: "prefix", service: "order-" }))).toBe("services starting with order-");
    expect(memberPhrase(svc("ledger-sync"))).toBe("ledger-sync");
  });
});

describe("what the save bar says will change", () => {
  const stored = [svc("a"), svc("b", { attrs: [{ attribute: "x", operator: "equals", value: "1" }] })];

  it("says nothing when the draft is what is stored", () => {
    expect(describeChanges(stored, stored.map((r) => ({ ...r })), "any", "any")).toEqual([]);
  });

  it("names additions, removals, and narrowing or widening", () => {
    const draft = [
      svc("a", { attrs: [{ attribute: "y", operator: "equals", value: "2" }] }),
      svc("b"),
      svc("c"),
    ];
    expect(describeChanges(stored, draft, "any", "any").sort()).toEqual(
      ["added c", "narrowed a", "widened b to all traffic"].sort(),
    );
    expect(describeChanges(stored, [svc("a")], "any", "any")).toEqual(["removed b"]);
  });

  it("names a change of how members combine", () => {
    expect(describeChanges(stored, stored, "any", "all")).toEqual(["now needs one trace through every member"]);
  });

  // The draft is read as it will be stored. A condition with no service
  // is dropped by the save, so the bar has to say so rather than let it
  // vanish after the click.
  it("owns up to a row the save will drop", () => {
    const orphan = blankRule({ service: "", attrs: [{ attribute: "x", operator: "equals", value: "1" }] });
    expect(describeChanges([svc("a"), orphan], [svc("a"), svc("b"), orphan], "any", "any")).toEqual([
      "added b",
      "removed a condition with no service",
    ]);
  });

  // ...but only once there is a save to lose it in. On its own it would
  // show an unsaved change on an editor nobody has touched.
  it("does not invent a change on an untouched editor", () => {
    const orphan = blankRule({ service: "", attrs: [{ attribute: "x", operator: "equals", value: "1" }] });
    expect(describeChanges([svc("a"), orphan], [svc("a"), orphan], "any", "any")).toEqual([]);
  });
});

describe("suggestions from traces", () => {
  const hood = (focal: string, down: [string, number][], up: [string, number][] = []) => ({
    focal,
    data: {
      service_name: focal,
      window: {} as never,
      downstream: down.map(([service_name, trace_count]) => ({ service_name, trace_count, error_count: 0 })),
      upstream: up.map(([service_name, trace_count]) => ({ service_name, trace_count, error_count: 0 })),
    } as NeighborsResponse,
  });

  // The queue behind three members used to be suggested three times.
  it("lists a shared neighbour once, under its busiest connection", () => {
    const out = mergeSuggestions(
      [hood("a", [["queue", 10]]), hood("b", [["queue", 40]]), hood("c", [], [["gateway", 5]])],
      () => false,
    );
    expect(out.map((s) => s.name)).toEqual(["queue", "gateway"]);
    expect(out[0].reason).toBe("called by b");
    expect(out[1].reason).toBe("calls c");
  });

  it("never suggests a member, or a service a member already covers", () => {
    const out = mergeSuggestions([hood("a", [["b", 5], ["order-x", 5], ["z", 1]])], (n) => n.startsWith("order-"));
    expect(out.map((s) => s.name)).toEqual(["b", "z"]);
    const withMembers = mergeSuggestions([hood("a", [["b", 5]]), hood("b", [["a", 5]])], () => false);
    expect(withMembers).toEqual([]);
  });
});

describe("the range, in words", () => {
  it("says a relative range as one, and an absolute one without inventing a length", () => {
    expect(windowPhrase("24h")).toBe("in the last 24h");
    expect(windowPhrase("2026-09-01T00:00:00Z,2026-09-02T00:00:00Z")).toBe("in this range");
  });
});
