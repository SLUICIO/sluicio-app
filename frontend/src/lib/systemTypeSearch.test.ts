// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { filterSystemTypes, normalizeForSearch, systemTypeMatches } from "./systemTypeSearch";
import type { SystemType } from "../api/types";

function type(over: Partial<SystemType> & { key: string; label: string }): SystemType {
  return {
    id: over.key,
    built_in: true,
    is_system: true,
    detect_prefixes: [],
    checks: [],
    ...over,
  } as SystemType;
}

const CATALOG = [
  type({ key: "rabbitmq", label: "RabbitMQ" }),
  type({ key: "kafka", label: "Apache Kafka" }),
  type({ key: "otel_collector", label: "OTel Collector" }),
  type({ key: "sftp", label: "SFTP server" }),
  type({ key: "journalforing", label: "Journalföring", built_in: false }),
];

describe("normalizeForSearch", () => {
  it("folds case and drops punctuation and spacing", () => {
    expect(normalizeForSearch("OTel Collector")).toBe("otelcollector");
    expect(normalizeForSearch("otel_collector")).toBe("otelcollector");
    expect(normalizeForSearch("  Rabbit-MQ  ")).toBe("rabbitmq");
  });

  it("keeps non-ASCII letters", () => {
    // Stripping these would make every Swedish-named custom type
    // unsearchable, which is most of them on Robert's own cells.
    expect(normalizeForSearch("Journalföring")).toBe("journalföring");
    expect(normalizeForSearch("Kötjänst")).toBe("kötjänst");
  });

  it("folds digits in, not out", () => {
    expect(normalizeForSearch("S3 bucket")).toBe("s3bucket");
  });
});

describe("systemTypeMatches", () => {
  const rabbit = CATALOG[0];

  it("matches the label", () => {
    expect(systemTypeMatches(rabbit, "rabbit")).toBe(true);
  });

  it("matches the key, which is on screen right next to the label", () => {
    expect(systemTypeMatches(CATALOG[2], "otel_collector")).toBe(true);
  });

  it("ignores spacing the user did not know about", () => {
    // The whole point: "otel collector" must find `otel_collector`.
    expect(systemTypeMatches(CATALOG[2], "otel collector")).toBe(true);
    expect(systemTypeMatches(rabbit, "rabbit mq")).toBe(true);
  });

  it("is case insensitive", () => {
    expect(systemTypeMatches(rabbit, "RABBITMQ")).toBe(true);
  });

  it("matches on a substring, not only a prefix", () => {
    // Typing "kafka" must find "Apache Kafka".
    expect(systemTypeMatches(CATALOG[1], "kafka")).toBe(true);
  });

  it("does not match an unrelated query", () => {
    expect(systemTypeMatches(rabbit, "kafka")).toBe(false);
  });

  it("treats an empty or whitespace query as matching everything", () => {
    expect(systemTypeMatches(rabbit, "")).toBe(true);
    expect(systemTypeMatches(rabbit, "   ")).toBe(true);
  });

  it("does not match on punctuation alone", () => {
    // A query of "___" normalizes to nothing, so it must behave as
    // "no query" rather than matching every key containing underscores.
    expect(systemTypeMatches(CATALOG[3], "___")).toBe(true);
  });
});

describe("filterSystemTypes", () => {
  it("returns the whole catalog for an empty query", () => {
    expect(filterSystemTypes(CATALOG, "")).toHaveLength(CATALOG.length);
  });

  it("returns the same array identity when there is nothing to filter", () => {
    // Avoids a new array on every render for the common case.
    expect(filterSystemTypes(CATALOG, "  ")).toBe(CATALOG);
  });

  it("keeps the catalog's own order rather than ranking by match", () => {
    // People learn where things sit; reshuffling per keystroke would
    // make the page feel like it is moving under them.
    const got = filterSystemTypes(CATALOG, "a").map((t) => t.key);
    expect(got).toEqual(got.slice().sort((x, y) => CATALOG.findIndex(t => t.key === x) - CATALOG.findIndex(t => t.key === y)));
  });

  it("finds a custom type by its accented name", () => {
    expect(filterSystemTypes(CATALOG, "journalför").map((t) => t.key)).toEqual(["journalforing"]);
  });

  it("returns nothing when nothing matches", () => {
    expect(filterSystemTypes(CATALOG, "nats")).toEqual([]);
  });
});
