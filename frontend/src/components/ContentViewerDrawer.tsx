// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ContentViewerDrawer — a read-only right-side drawer that shows a
// Map's or Schema's content syntax-highlighted, with a one-click Copy
// button. Replaces the old inline table-row expander on the Maps and
// Schemas admin pages: opening content no longer pushes the table rows
// apart, and the whole body is easy to copy at once.

import { useState } from "react";
import { EditDrawer } from "./primitives";
import SyntaxView from "./SyntaxView";

interface Props {
  // Drawer header — e.g. "OrderToOrderEvent · v1".
  title: string;
  content: string;
  // Declared format (json, yaml, xslt, …) — drives syntax highlighting.
  format: string;
  onClose: () => void;
}

export default function ContentViewerDrawer({ title, content, format, onClose }: Props) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      // Revert the label after a moment so a repeat copy re-confirms.
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard blocked — content is visible to select + copy manually */
    }
  };

  return (
    <EditDrawer title={title} width="wide" onClose={onClose}>
      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: 8,
          }}
        >
          <div
            className="muted"
            style={{ fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5 }}
          >
            Content · {format}
          </div>
          <button
            type="button"
            className="btn"
            onClick={copy}
            disabled={!content}
            title="Copy content to clipboard"
          >
            {copied ? "Copied ✓" : "Copy"}
          </button>
        </div>
        {content ? (
          // Large maxHeight so the content grows naturally and the
          // drawer body (overflow: auto) owns the scroll.
          <SyntaxView content={content} format={format} maxHeight={5000} />
        ) : (
          <div className="placeholder">No content.</div>
        )}
      </div>
    </EditDrawer>
  );
}
