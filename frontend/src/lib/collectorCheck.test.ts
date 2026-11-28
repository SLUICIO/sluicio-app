// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { uncheckedAnswer } from "./collectorCheck";

const apiError = (status: number, body: unknown) =>
  Object.assign(new Error(`${status} failed`), { status, body });

describe("uncheckedAnswer", () => {
  it("reviews a change OTelFlow could not check", () => {
    expect(
      uncheckedAnswer(
        apiError(502, {
          checked: false,
          kept_by_local: ["extensions.opamp"],
          plain_secrets: ["exporters.otlp_http.headers.x-api-key"],
          error: {
            message: "OTelFlow could not check the configuration: timeout",
            status: 502,
          },
        }),
      ),
    ).toEqual({
      checked: false,
      kept_by_local: ["extensions.opamp"],
      plain_secrets: ["exporters.otlp_http.headers.x-api-key"],
      check_error: "OTelFlow could not check the configuration: timeout",
    });
  });
  it("leaves every other failure an error", () => {
    expect(
      uncheckedAnswer(apiError(500, { error: { message: "boom" } })),
    ).toBeNull();
    expect(uncheckedAnswer(apiError(502, undefined))).toBeNull();
    expect(uncheckedAnswer(new Error("network"))).toBeNull();
  });
});
