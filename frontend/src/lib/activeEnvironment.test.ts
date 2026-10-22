// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import {
  NO_ENVIRONMENT_PARAM,
  envFromParam,
  envQuery,
  envToParam,
  environmentLabel,
  readsIntegrationData,
} from "./activeEnvironment";

describe("environment parameter", () => {
  it("round-trips a name and the empty environment", () => {
    expect(envFromParam(envToParam("production"))).toBe("production");
    expect(envToParam("")).toBe(NO_ENVIRONMENT_PARAM);
    expect(envFromParam(NO_ENVIRONMENT_PARAM)).toBe("");
    expect(envFromParam(null)).toBeNull();
    expect(envFromParam("")).toBeNull();
  });

  it("builds the link part", () => {
    expect(envQuery(null)).toBe("");
    expect(envQuery("prod eu")).toBe("?env=prod%20eu");
    expect(envQuery("")).toBe("?env=(none)");
    expect(environmentLabel("")).toBe("(no environment)");
  });
});

describe("readsIntegrationData", () => {
  it("narrows reads of integration data only", () => {
    expect(readsIntegrationData("/integrations/abc/flow?range=1h", "GET")).toBe(true);
    expect(readsIntegrationData("/messages/search?range=1h", "POST")).toBe(true);
    expect(readsIntegrationData("/logs?integration=Order%20sync", "GET")).toBe(true);
    expect(readsIntegrationData("/metric-catalog?integration=x", "GET")).toBe(true);
    // Writes never follow it: an environment narrows what is read.
    expect(readsIntegrationData("/integrations/abc/declared-environments", "PUT")).toBe(false);
    expect(readsIntegrationData("/integrations", "POST")).toBe(false);
    // Nothing else does either.
    expect(readsIntegrationData("/services?range=1h", "GET")).toBe(false);
    expect(readsIntegrationData("/me", "GET")).toBe(false);
  });
});
