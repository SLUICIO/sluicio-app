// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import {
  ALL_ENVIRONMENTS_PARAM,
  NO_ENVIRONMENT_PARAM,
  rememberEnvironmentParams,
  rememberedEnvironmentParams,
  rememberedSingleEnvironment,
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

// The menus link to pages without ?env, so the environment chosen was
// dropped on every move. The last choice is remembered for the tab.
describe("remembered environment", () => {
  it("remembers a choice and forgets it on all", () => {
    rememberEnvironmentParams(["production"]);
    expect(rememberedEnvironmentParams()).toEqual(["production"]);
    rememberEnvironmentParams(["prod-eu", "prod-us"]);
    expect(rememberedEnvironmentParams()).toEqual(["prod-eu", "prod-us"]);
    rememberEnvironmentParams(null);
    expect(rememberedEnvironmentParams()).toBeNull();
  });

  it("ignores what it cannot read", () => {
    window.sessionStorage.setItem("conduit:env", "not json");
    expect(rememberedEnvironmentParams()).toBeNull();
    window.sessionStorage.setItem("conduit:env", JSON.stringify([1, 2]));
    expect(rememberedEnvironmentParams()).toBeNull();
    rememberEnvironmentParams(null);
  });

  it("gives a single-environment picker one named environment only", () => {
    expect(rememberedSingleEnvironment(["production"])).toBe("production");
    expect(rememberedSingleEnvironment([NO_ENVIRONMENT_PARAM])).toBe(NO_ENVIRONMENT_PARAM);
    // A set it cannot show, or all, leaves the page on its default.
    expect(rememberedSingleEnvironment(["prod-eu", "prod-us"])).toBeNull();
    expect(rememberedSingleEnvironment([ALL_ENVIRONMENTS_PARAM])).toBeNull();
    expect(rememberedSingleEnvironment(null)).toBeNull();
  });
});
