// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A payload-link cell shows the earliest link of a message and "+N" for
// the others; the count opens every link with the span that carried it.
// A value that is not an address is text, never a link.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import PayloadLinkCell from "./PayloadLinkCell";
import type { PayloadLinks } from "../api/types";

const transfer: PayloadLinks = {
  links: [
    {
      url: "https://mft-archive.example.com/files/in/INV_42.pdf",
      value: "https://mft-archive.example.com/files/in/INV_42.pdf",
      spans: [{ span_id: "a", service: "mft-supplier-to-ap", span_name: "pickup INV_42.pdf", role: "input", time: "2026-11-27T10:00:00Z" }],
    },
    {
      url: "https://mft-archive.example.com/files/out/INV_42.pdf",
      value: "https://mft-archive.example.com/files/out/INV_42.pdf",
      spans: [{ span_id: "b", service: "mft-supplier-to-ap", span_name: "deliver INV_42.pdf", role: "output", time: "2026-11-27T10:00:02Z" }],
    },
  ],
};

describe("PayloadLinkCell", () => {
  it("links the earliest payload and counts the others", () => {
    const onRow = vi.fn();
    render(
      <div onClick={onRow}>
        <PayloadLinkCell links={transfer} loading={false} />
      </div>,
    );
    const a = screen.getByRole("link", { name: "INV_42.pdf" });
    expect(a.getAttribute("href")).toBe("https://mft-archive.example.com/files/in/INV_42.pdf");
    expect(a.getAttribute("target")).toBe("_blank");
    expect(a.getAttribute("rel")).toBe("noopener noreferrer");

    fireEvent.click(screen.getByRole("button", { name: "+1" }));
    // Opening the list does not open the message behind the row.
    expect(onRow).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog", { name: "Payload links of this message" });
    expect(dialog.textContent).toMatch(/^.*received · .+ · mft-supplier-to-ap · pickup INV_42\.pdf/);
    expect(dialog.textContent).toMatch(/sent · .+ · mft-supplier-to-ap · deliver INV_42\.pdf/);
  });

  it("says received or sent only when the span carries io.role", () => {
    const noRole: PayloadLinks = {
      links: [
        transfer.links[0],
        { ...transfer.links[1], spans: [{ ...transfer.links[1].spans[0], role: undefined }] },
        { url: "https://a.example.com/x.pdf", value: "x", spans: [{ span_id: "c", service: "svc", span_name: "store", role: "internal", time: "2026-11-27T10:00:03Z" }] },
      ],
    };
    render(<PayloadLinkCell links={noRole} loading={false} />);
    fireEvent.click(screen.getByRole("button", { name: "+2" }));
    const text = screen.getByRole("dialog").textContent ?? "";
    expect(text).toContain("received");
    expect(text).not.toContain("sent");
    expect(text).not.toContain("internal");
  });

  it("shows a value that is not an address as text", () => {
    render(<PayloadLinkCell links={{ links: [{ value: "INV_9.pdf", spans: [] }] }} loading={false} />);
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getByText("INV_9.pdf").getAttribute("title")).toMatch(/not an address/);
  });

  it("says loading, and a dash for a message without links", () => {
    const { rerender } = render(<PayloadLinkCell loading={true} />);
    expect(screen.getByText("…")).toBeTruthy();
    rerender(<PayloadLinkCell links={undefined} loading={false} />);
    expect(screen.getByText("–")).toBeTruthy();
  });

  it("counts the links left out beyond the cap", () => {
    render(<PayloadLinkCell links={{ ...transfer, more: 30 }} loading={false} />);
    fireEvent.click(screen.getByRole("button", { name: "+31" }));
    expect(screen.getByText("and 30 more")).toBeTruthy();
  });
});
