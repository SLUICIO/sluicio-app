// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The variable checker's whole value is NOT crying wolf: the false
// positives below (loop vars, assigns, filter arguments, metadata keys)
// are all valid Liquid the schema can't know about. If any of them ever
// starts reporting, the warning becomes noise people click past.

import { describe, expect, it } from "vitest";
import { unknownVariables } from "./liquidVars";

const KNOWN = [
  "alert.state",
  "alert.severity",
  "alert.summary",
  "alert.link",
  "alert.state_emoji",
  "rule.name",
  "check.value",
  "check.metric",
  "service.name",
  "service.error_count",
  "service.metadata.<key>",
  "integration.name",
  "integration.services",
  "org.company",
  "include.check",
  "include.service",
  "sent_at",
];

describe("unknownVariables — must not cry wolf", () => {
  it("accepts known paths, containers and metadata keys", () => {
    const tpl = `
      {{ alert.severity }} {{ rule.name }} {{ sent_at }}
      {{ service.metadata.Team }} {{ service.metadata["On-call"] }}
      {% if include.check and check.value %}{{ check.value }}{% endif %}
    `;
    expect(unknownVariables(tpl, KNOWN)).toEqual([]);
  });

  it("accepts loop variables, assigns and captures", () => {
    const tpl = `
      {% for kv in service.metadata %}{{ kv.key }}={{ kv.value }}{% endfor %}
      {% assign sev = alert.severity %}{{ sev | upcase }}
      {% capture line %}{{ rule.name }}{% endcapture %}{{ line }}
      {% for svc in integration.services %}{{ svc }}{% endfor %}
    `;
    expect(unknownVariables(tpl, KNOWN)).toEqual([]);
  });

  it("ignores filter arguments and string literals", () => {
    const tpl = `{{ rule.name | default: "no.such.path" }} {{ alert.summary | truncate: 40 }}`;
    expect(unknownVariables(tpl, KNOWN)).toEqual([]);
  });

  it("ignores Liquid operators inside conditions", () => {
    const tpl = `{% if alert.state == 'firing' and alert.severity != 'info' %}x{% endif %}`;
    expect(unknownVariables(tpl, KNOWN)).toEqual([]);
  });
});

describe("unknownVariables — catches real mistakes", () => {
  it("flags a typo on a known root and suggests the near miss", () => {
    const found = unknownVariables("{{ alert.severty }}", KNOWN);
    expect(found).toHaveLength(1);
    expect(found[0].path).toBe("alert.severty");
    expect(found[0].suggestion).toBe("alert.severity");
    expect(found[0].line).toBe(1);
  });

  it("flags an unknown root that nothing bound", () => {
    const found = unknownVariables("line one\n{{ alrt.severity }}", KNOWN);
    expect(found).toHaveLength(1);
    expect(found[0].path).toBe("alrt.severity");
    expect(found[0].line).toBe(2);
    expect(found[0].suggestion).toBeUndefined();
  });

  it("flags inside conditions too, and dedupes per line", () => {
    const found = unknownVariables("{% if service.stat == 'ok' %}{{ service.stat }}{% endif %}", KNOWN);
    expect(found).toHaveLength(1);
    expect(found[0].path).toBe("service.stat");
  });

  it("is silent on an empty template or an empty schema", () => {
    expect(unknownVariables("", KNOWN)).toEqual([]);
    expect(unknownVariables("{{ anything.at.all }}", [])).toEqual([]);
  });
});
