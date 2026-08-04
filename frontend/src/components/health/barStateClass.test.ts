// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A check row's colour must describe the check's STATE, not its
// configuration.
//
// severity answers "how bad would this be if it fired". Painting the row
// with it made every check look permanently angry: a critical check that
// has never fired is not an incident, it is a healthy check with a
// serious threshold. On a system with a dozen checks the whole list read
// as red.

import { describe, expect, it } from "vitest";
import { barStateClass } from "./HealthChecks";

const rule = (severity: string, enabled = true) => ({ severity, enabled });

describe("barStateClass", () => {
  it("shows healthy for a check that is not firing, whatever its severity", () => {
    for (const sev of ["info", "warning", "critical"]) {
      expect(barStateClass(rule(sev), false), `severity ${sev}`).toBe("state-ok");
    }
  });

  it("uses the severity only while the check is actually firing", () => {
    expect(barStateClass(rule("critical"), true)).toBe("sev-critical");
    expect(barStateClass(rule("warning"), true)).toBe("sev-warning");
  });

  it("shows a disabled check as idle, not as healthy", () => {
    // A disabled check asserts nothing. Calling it healthy would claim a
    // guarantee nobody is checking.
    expect(barStateClass(rule("critical", false), false)).toBe("state-idle");
  });

  it("keeps a disabled check idle even if an instance is still open", () => {
    // Disabling a firing check stops evaluation; the stale instance must
    // not keep the row red as though it were still being watched.
    expect(barStateClass(rule("critical", false), true)).toBe("state-idle");
  });
});
