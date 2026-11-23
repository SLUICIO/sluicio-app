// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The completion column says, per message, complete or waiting/delayed
// at which stage, and stays quiet for a rule that does not follow it.

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import CompletionCell from "./CompletionCell";
import type { TraceCompletionStatus } from "../../api/types";

const rule = { rule_id: "r1", rule_name: "Send step must complete", stages: 2 };

describe("CompletionCell", () => {
  it("names the stage a message is delayed or waiting at", () => {
    const delayed: TraceCompletionStatus = { ...rule, state: "delayed", stage: 2, stage_name: "create iqpl-sftp", reached: 1 };
    render(<CompletionCell statuses={[delayed]} />);
    const pill = screen.getByText("delayed at create iqpl-sftp");
    expect(pill.getAttribute("title")).toBe(
      "Send step must complete: 1 of 2 stages reached, past the timeout. Stage 2 of 2: create iqpl-sftp.",
    );
  });

  it("says complete", () => {
    render(<CompletionCell statuses={[{ ...rule, state: "complete", reached: 2 }]} />);
    expect(screen.getByText("complete").className).toContain("pill--ok");
  });

  it("shows a dash for a message no rule follows", () => {
    render(<CompletionCell statuses={[{ ...rule, state: "not_started", reached: 0 }]} />);
    expect(screen.getByText("—").getAttribute("title")).toMatch(/does not follow it/);
  });

  it("shows it is still reading while the list has already rendered", () => {
    render(<CompletionCell loading />);
    expect(screen.getByText("…").getAttribute("title")).toMatch(/Reading/);
  });
});
