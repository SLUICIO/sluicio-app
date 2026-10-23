// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Production as a role (issue #40): what an integration page opens on,
// and how "production environments" reads as names.

import { describe, expect, it } from "vitest";
import type { IntegrationEnvironment } from "../api/types";
import { productionToOpen } from "../components/EnvironmentScope";
import { PRODUCTION_TOKEN, envQuery, envsFromParams, expandProduction } from "./activeEnvironment";

const env = (environment: string, production: boolean, trace_count: number, declared = false): IntegrationEnvironment => ({
  environment,
  production,
  trace_count,
  declared,
  status: "ok",
  error_trace_count: 0,
  log_records: 0,
  metric_rows: 0,
});

describe("productionToOpen", () => {
  it("opens on every production environment with messages", () => {
    expect(productionToOpen([env("prod-us", true, 5), env("test", false, 9), env("Blablablbu", true, 2)])).toEqual([
      "Blablablbu",
      "prod-us",
    ]);
  });
  it("skips one only logs or metrics come from", () => {
    expect(productionToOpen([env("production", true, 0), env("dev", false, 40)])).toEqual([]);
  });
  it("keeps a declared one, and takes any for an integration without messages", () => {
    expect(productionToOpen([env("production", true, 0, true), env("dev", false, 40)])).toEqual(["production"]);
    expect(productionToOpen([env("production", true, 0), env("dev", false, 0)])).toEqual(["production"]);
  });
});

describe("environment sets", () => {
  it("reads and writes repeated ?env=", () => {
    expect(envsFromParams(new URLSearchParams("env=prod-eu&env=prod-us"))).toEqual(["prod-eu", "prod-us"]);
    expect(envsFromParams(new URLSearchParams("env=*"))).toBeNull();
    expect(envsFromParams(new URLSearchParams("env=(none)"))).toEqual([""]);
    expect(envQuery(["prod-eu", "prod-us"])).toBe("?env=prod-eu&env=prod-us");
    expect(envQuery(null, true)).toBe("?env=*");
  });
  it("expands production environments", () => {
    expect(expandProduction([PRODUCTION_TOKEN, "test", "prod-eu"], ["prod-eu", "prod-us"])).toEqual(["prod-eu", "prod-us", "test"]);
  });
});
