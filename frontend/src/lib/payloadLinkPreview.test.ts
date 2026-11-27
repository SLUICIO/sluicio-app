// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, expect, it } from "vitest";
import { previewPayloadLink } from "./payloadLinkPreview";
import { presentAttributes } from "./payloadLinks";
import { payloadLinkName, roleLabel } from "../components/PayloadLinkCell";

// The same cases as internal/payloadlinks TestResolve, for the ones the
// settings preview shows.
describe("previewPayloadLink", () => {
  it("takes a full address as it is, and nothing else", () => {
    expect(previewPayloadLink("", "https://blob.example.com/in/INV_42.pdf")).toBe("https://blob.example.com/in/INV_42.pdf");
    expect(previewPayloadLink("", "INV_42.pdf")).toBeNull();
    expect(previewPayloadLink("", "javascript:alert(1)")).toBeNull();
  });
  it("puts exactly one slash between base and value", () => {
    expect(previewPayloadLink("https://archive.example.com/files", "2026/INV_42.pdf")).toBe("https://archive.example.com/files/2026/INV_42.pdf");
    expect(previewPayloadLink("https://archive.example.com/files/", "/2026/INV_42.pdf")).toBe("https://archive.example.com/files/2026/INV_42.pdf");
  });
  it("encodes what a URL cannot carry, keeping the slashes", () => {
    expect(previewPayloadLink("https://archive.example.com/files", "in box/INV #42.pdf")).toBe("https://archive.example.com/files/in%20box/INV%20%2342.pdf");
  });
  it("takes a query value after = or ?", () => {
    expect(previewPayloadLink("https://dms.example.com/open?id=", "INV_42")).toBe("https://dms.example.com/open?id=INV_42");
  });
  it("refuses to climb out of the base", () => {
    expect(previewPayloadLink("https://archive.example.com/files", "../secrets")).toBeNull();
  });
});

describe("payload link columns", () => {
  const attrs = [{ attribute: "io.payload.url" }, { attribute: "archive.path", label: "Archived file" }];
  it("shows an attribute only when a message on the page carries it, in configured order", () => {
    const links = {
      t1: { "archive.path": { links: [{ url: "https://a.example.com/x", value: "x", spans: [] }] } },
      t2: {},
    };
    expect(presentAttributes(attrs, links).map((a) => a.attribute)).toEqual(["archive.path"]);
    expect(presentAttributes(attrs, {})).toEqual([]);
  });
  it("names a link by its file", () => {
    expect(payloadLinkName({ url: "https://a.example.com/in/INV%2042.pdf", value: "" })).toBe("INV 42.pdf");
    expect(payloadLinkName({ url: "https://dms.example.com/open?id=INV_42", value: "" })).toBe("INV_42");
    expect(payloadLinkName({ value: "INV_9.pdf" })).toBe("INV_9.pdf");
    expect(roleLabel("input")).toBe("received");
    expect(roleLabel("output")).toBe("sent");
  });
});
