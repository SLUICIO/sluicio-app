// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// What to tell someone whose advisor has nothing to say.
//
// The panel used to render one sentence for every not-ready ledger:
// "The advisor has N days of consumption history and needs 30. It will
// start advising in about (30 - N) days." That is true while the ledger
// is FILLING and false when it is EMPTY, and empty is the case people
// actually hit — on a demo cell, on a fresh install, on any org where
// the team lives in Messages. Demand is recorded when a human opens a
// view, so an org that ingests terabytes and browses nothing sits at
// zero days indefinitely, being promised a date that never arrives.
//
// Worse, the promise is self-defeating: it tells the reader to wait,
// which is the one action that cannot fix it. The only thing that moves
// an empty ledger is somebody using the product.
//
// So the decision of WHICH sentence is here, in one function with
// tests, rather than inline in JSX where the empty case was never
// distinguished in the first place.

import type { AdvisorLedger } from "../api/types";

export type LedgerState = "ready" | "filling" | "empty" | "unavailable";

export interface LedgerMessage {
  state: LedgerState;
  title: string;
  /** The headline: what is true right now. */
  lead: string;
  /** Why it works this way, or what to do about it. */
  detail: string;
}

/**
 * Classify a ledger. `unavailable` wins over `empty`, because a ledger
 * we could not read is not a ledger we know to be empty — reporting an
 * outage as "you have no history" is a false claim about someone's own
 * data.
 */
export function ledgerState(l: AdvisorLedger): LedgerState {
  if (l.unavailable) return "unavailable";
  if (l.ready) return "ready";
  if (l.empty || l.days <= 0) return "empty";
  return "filling";
}

const WHY_WAIT =
  'This wait is deliberate. "Nobody used this" is only true if we were watching the whole time: ' +
  "judging a month of telemetry against a few days would call anything you charted last month " +
  "unused, and recommend deleting it. Config you already have (alert rules, facet mappings, " +
  "dashboards) is protected from day one; it is people's queries that need the history.";

/**
 * The message for a ledger that cannot yet advise. Returns null when the
 * advisor is ready and the panel should show findings instead.
 */
export function ledgerMessage(l: AdvisorLedger): LedgerMessage | null {
  const state = ledgerState(l);
  if (state === "ready") return null;

  if (state === "unavailable") {
    return {
      state,
      title: "Consumption history unavailable",
      lead: "The advisor could not read its demand ledger, so it cannot say what is used and what is not.",
      detail:
        "This is a storage problem, not a verdict about your telemetry — nothing has been judged unused. " +
        "Check that ClickHouse is reachable from the cell, then evaluate again.",
    };
  }

  if (state === "empty") {
    return {
      state,
      title: "Nothing consumed yet",
      lead:
        "The advisor has no consumption history at all, so it has nothing to weigh your ingest against.",
      // The point of this screen: waiting is not the fix.
      detail:
        "The ledger records when someone opens a view — messages, traces, logs, a metric chart — not how " +
        "much telemetry arrives. So it stays empty on a cell nobody browses, however much data is flowing " +
        `in, and will not fill on its own after ${l.needs_days} days. Use the product normally for a few ` +
        "weeks and the advisor will start having something to say.",
    };
  }

  const left = Math.max(l.needs_days - l.days, 1);
  return {
    state,
    title: "Still watching",
    lead:
      `The advisor has ${l.days} ${l.days === 1 ? "day" : "days"} of consumption history and needs ` +
      `${l.needs_days}. It will start advising in about ${left} ${left === 1 ? "day" : "days"}, ` +
      "as long as the cell keeps being used.",
    detail: WHY_WAIT,
  };
}

/**
 * What to say after a manual evaluation finishes.
 *
 * "Evaluate now" ran correctly and reported nothing, which is
 * indistinguishable from a dead button. The run always gets a sentence
 * now, and when the answer was zero it says which kind of zero.
 */
export function runOutcomeMessage(openSuggestions: number, l: AdvisorLedger): string {
  if (openSuggestions > 0) {
    return `Evaluation finished: ${openSuggestions} open ${
      openSuggestions === 1 ? "suggestion" : "suggestions"
    }.`;
  }
  switch (ledgerState(l)) {
    case "unavailable":
      return "Evaluation ran, but the demand ledger could not be read, so nothing could be judged.";
    case "empty":
      return "Evaluation ran and found nothing to suggest: there is no consumption history yet to compare your ingest against.";
    case "filling":
      return `Evaluation ran and found nothing to suggest: the ledger holds ${l.days} of the ${l.needs_days} days needed.`;
    default:
      return "Evaluation finished: nothing to suggest.";
  }
}
