// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A change for a collector whose configuration the team's pipeline owns:
// Sluicio sends nothing to it, so the change is handed over as a unified
// diff of the configuration the collector reports, to make the same
// change in the team's repository.

import { useState } from "react";
import { copyText } from "../../lib/clipboard";
import { unifiedDiff } from "../../lib/lineDiff";

export default function DiffExport({
  before,
  after,
  file,
}: {
  before: string;
  after: string;
  // The file name in the diff and of the download, e.g. the collector's.
  file: string;
}) {
  const patch = unifiedDiff(before, after, file);
  const [copied, setCopied] = useState(false);
  if (!patch) {
    return (
      <div className="muted" style={{ fontSize: 13 }}>
        Nothing to change.
      </div>
    );
  }
  const download = () => {
    const url = URL.createObjectURL(new Blob([patch], { type: "text/x-diff" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = `${file}.patch`;
    a.click();
    URL.revokeObjectURL(url);
  };
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      <p className="muted" style={{ margin: 0, fontSize: 12.5 }}>
        The team&apos;s pipeline owns this configuration, so Sluicio sends it
        nothing. Make the same change in the repository the pipeline deploys
        from. The diff is against the configuration as the collector reports it,
        which can be ordered differently from your file and has its secrets
        masked, so read it as what to change rather than a patch to apply as it
        is.
      </p>
      <pre
        className="mono"
        style={{
          margin: 0,
          fontSize: 12,
          background: "var(--surface)",
          padding: 10,
          borderRadius: 6,
          overflowX: "auto",
          maxHeight: 360,
        }}
      >
        {patch}
      </pre>
      <div style={{ display: "flex", gap: 8 }}>
        <button
          className="btn btn--sm"
          onClick={async () => setCopied(await copyText(patch))}
        >
          {copied ? "Copied" : "Copy diff"}
        </button>
        <button className="btn btn--sm" onClick={download}>
          Download .patch
        </button>
      </div>
    </div>
  );
}
