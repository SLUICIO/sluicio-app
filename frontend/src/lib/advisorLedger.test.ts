// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The reported bug (issue #1) was a sentence, not a crash: a demo cell
// with weeks of telemetry was told it had "0 days of consumption
// history" and would start advising "in about 30 days". Both halves
// were misleading — the ledger counts human queries, not ingest, and an
// empty one does not fill by waiting.
//
// These tests pin the distinction so the empty case cannot quietly
// collapse back into the filling case.

import { describe, expect, it } from "vitest";
import { ledgerMessage, ledgerState, runOutcomeMessage } from "./advisorLedger";
import type { AdvisorLedger } from "../api/types";

const led = (o: Partial<AdvisorLedger>): AdvisorLedger => ({
  ready: false,
  days: 0,
  needs_days: 30,
  ...o,
});

describe("ledgerState", () => {
  it("calls an empty ledger empty, not merely young", () => {
    expect(ledgerState(led({ empty: true }))).toBe("empty");
  });

  it("treats zero days as empty even without the flag", () => {
    // Older cells answer without `empty`; zero days means the same thing.
    expect(ledgerState(led({ days: 0 }))).toBe("empty");
  });

  it("separates a filling ledger from an empty one", () => {
    expect(ledgerState(led({ days: 12 }))).toBe("filling");
  });

  it("reports an unreadable ledger as unavailable, never as empty", () => {
    // An outage is not evidence that nobody looked at anything.
    expect(ledgerState(led({ unavailable: true, days: 0 }))).toBe("unavailable");
  });

  it("lets unavailable win over ready", () => {
    expect(ledgerState(led({ unavailable: true, ready: true }))).toBe("unavailable");
  });
});

describe("ledgerMessage", () => {
  it("says nothing when the advisor is ready", () => {
    expect(ledgerMessage(led({ ready: true }))).toBeNull();
  });

  it("does NOT promise an empty ledger a date", () => {
    // The exact regression from issue #1.
    const m = ledgerMessage(led({ empty: true }))!;
    expect(m.state).toBe("empty");
    expect(`${m.lead} ${m.detail}`).not.toMatch(/will start advising in about/);
  });

  it("tells an empty ledger that waiting is not the fix", () => {
    const m = ledgerMessage(led({ empty: true }))!;
    expect(m.detail).toMatch(/will not fill on its own/);
  });

  it("explains that the ledger counts views, not ingest volume", () => {
    // Robert's cell had weeks of data and still read zero; the copy has
    // to account for that or the number looks like a bug.
    const m = ledgerMessage(led({ empty: true }))!;
    expect(m.detail).toMatch(/not how much telemetry arrives/);
  });

  it("still gives a filling ledger its countdown", () => {
    const m = ledgerMessage(led({ days: 23 }))!;
    expect(m.state).toBe("filling");
    expect(m.lead).toContain("23 days");
    expect(m.lead).toContain("about 7 days");
  });

  it("never counts down to zero or below", () => {
    const m = ledgerMessage(led({ days: 30, needs_days: 30 }))!;
    expect(m.lead).toContain("about 1 day");
  });

  it("singularises one day", () => {
    expect(ledgerMessage(led({ days: 1 }))!.lead).toContain("1 day of");
  });

  it("blames storage, not the user's telemetry, when unreadable", () => {
    const m = ledgerMessage(led({ unavailable: true }))!;
    expect(m.detail).toMatch(/nothing has been judged unused/);
  });
});

describe("runOutcomeMessage", () => {
  it("always says something, so the button is never silent", () => {
    expect(runOutcomeMessage(0, led({ empty: true }))).not.toBe("");
  });

  it("reports the count when there are findings", () => {
    expect(runOutcomeMessage(3, led({ ready: true }))).toContain("3 open suggestions");
  });

  it("singularises one finding", () => {
    expect(runOutcomeMessage(1, led({ ready: true }))).toContain("1 open suggestion");
  });

  it("explains a zero caused by an empty ledger", () => {
    expect(runOutcomeMessage(0, led({ empty: true }))).toMatch(/no consumption history yet/);
  });

  it("explains a zero caused by a young ledger, with the numbers", () => {
    const msg = runOutcomeMessage(0, led({ days: 9 }));
    expect(msg).toContain("9 of the 30 days");
  });

  it("distinguishes a genuine zero on a ready ledger", () => {
    const msg = runOutcomeMessage(0, led({ ready: true }));
    expect(msg).toBe("Evaluation finished: nothing to suggest.");
  });
});
