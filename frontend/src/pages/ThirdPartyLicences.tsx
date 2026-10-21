// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The third-party components built into this version of the software,
// with their licences and the licence and notice texts that travel with
// them (issue #41).
//
// Readable WITHOUT signing in, and so rendered outside the sign-in gate
// and outside the app's chrome: the list is owed to whoever has the
// software, and it describes the software, not anyone's data. Both lists
// are generated when the release is built - the server's by
// scripts/thirdparty, the web app's while bundling - so this page always
// shows the running version's components, on-premise and offline too.

import { useEffect, useState } from "react";
import { usePageTitle } from "../lib/usePageTitle";

interface Component {
  name: string;
  version: string;
  licenses: string[];
  files: { name: string; text: string }[];
}
interface Report {
  product: string;
  components: Component[];
}
type Loaded = { report: Report } | { error: string } | null;

// Google's PATENTS file is matched as a licence id; it is a patent grant
// that comes with the BSD licence, and reads better as such.
const licenceLabel = (id: string) => (id === "GooglePatentsFile" ? "Google patent grant" : id);

function useReport(url: string): Loaded {
  const [state, setState] = useState<Loaded>(null);
  useEffect(() => {
    let live = true;
    fetch(url, { credentials: "omit" })
      .then(async (r) => {
        if (!r.ok) throw new Error(r.status === 404 ? "not part of this build" : `HTTP ${r.status}`);
        return (await r.json()) as Report;
      })
      .then((report) => live && setState({ report }))
      .catch((e) => live && setState({ error: String((e as Error).message ?? e) }));
    return () => {
      live = false;
    };
  }, [url]);
  return state;
}

function Section({ title, loaded }: { title: string; loaded: Loaded }) {
  return (
    <section style={{ marginTop: 28 }}>
      <h2 style={{ fontSize: 17, fontWeight: 600, margin: "0 0 4px" }}>{title}</h2>
      {loaded === null && <p className="muted">Loading…</p>}
      {loaded && "error" in loaded && (
        <p className="muted">
          The list is generated when a release is built, and is {loaded.error === "not part of this build" ? "not part of this build" : `unavailable (${loaded.error})`}.
        </p>
      )}
      {loaded && "report" in loaded && (
        <>
          <p className="muted" style={{ fontSize: 13, margin: "0 0 10px" }}>
            {loaded.report.components.length} components. Open one for its licence and notice texts.
          </p>
          <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
            {loaded.report.components.map((c) => (
              <details key={`${c.name}@${c.version}`} style={{ borderBottom: "1px solid var(--border)", padding: "6px 0" }}>
                <summary style={{ cursor: "pointer", fontSize: 13.5 }}>
                  <span className="mono">{c.name}</span> <span className="muted">{c.version}</span>
                  <span style={{ float: "right" }} className="muted">
                    {c.licenses.length ? c.licenses.map(licenceLabel).join(", ") : "licence not identified"}
                  </span>
                </summary>
                {c.files.length === 0 ? (
                  <p className="muted" style={{ fontSize: 12.5 }}>
                    The package declares {c.licenses.join(", ") || "no licence"} and ships no licence file to reproduce.
                  </p>
                ) : (
                  c.files.map((f) => (
                    <div key={f.name} style={{ marginTop: 8 }}>
                      <div className="muted" style={{ fontSize: 12 }}>{f.name}</div>
                      <pre
                        className="mono"
                        style={{ whiteSpace: "pre-wrap", fontSize: 11.5, background: "var(--surface-2)", padding: 10, borderRadius: 6, maxHeight: 360, overflow: "auto" }}
                      >
                        {f.text}
                      </pre>
                    </div>
                  ))
                )}
              </details>
            ))}
          </div>
        </>
      )}
    </section>
  );
}

export default function ThirdPartyLicences() {
  usePageTitle("Third-party licences");
  const server = useReport("/api/v1/third-party");
  const web = useReport("/third-party.json");
  return (
    <main style={{ maxWidth: 900, margin: "0 auto", padding: "32px 20px 64px", background: "var(--bg)", minHeight: "100vh" }}>
      <a href="/" className="muted" style={{ fontSize: 13 }}>
        ← Back
      </a>
      <h1 className="page__title" style={{ marginTop: 12 }}>Third-party licences</h1>
      <p className="muted" style={{ fontSize: 14, lineHeight: 1.6, maxWidth: 720 }}>
        This software includes open source and other third-party components, each under its own licence.
        These are the components in <strong>version {String(__APP_VERSION__).replace(/^v/i, "")}</strong>,
        generated when it was built, with the licence and notice texts their licences ask to be passed on.
        Container images also carry operating-system packages from their base image; those are listed in
        the software bill of materials published with each release.
      </p>
      <Section title="Server" loaded={server} />
      <Section title="Web app" loaded={web} />
    </main>
  );
}
