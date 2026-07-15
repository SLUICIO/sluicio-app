// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Developers — an in-app getting-started for consuming the Sluicio API and
// wiring up the MCP server. Ties together token creation (personal + service
// account), the REST base URL, the live OpenAPI/Redoc reference, and a
// copy-paste MCP client config. Static + a couple of dynamic bits (the cell's
// own origin); no data fetching, visible to any authenticated user.

import { useState } from "react";
import { Link } from "react-router-dom";
import { usePageTitle } from "../lib/usePageTitle";
import { useCurrentUser } from "../lib/useCurrentUser";

function CopyBlock({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };
  return (
    <div style={{ position: "relative" }}>
      <pre
        className="mono"
        style={{
          background: "var(--surface)",
          border: "1px solid var(--border)",
          borderRadius: 8,
          padding: "12px 14px",
          overflowX: "auto",
          fontSize: 12.5,
          whiteSpace: "pre",
          margin: 0,
        }}
      >
        {text}
      </pre>
      <button
        type="button"
        className="btn btn--sm"
        style={{ position: "absolute", top: 8, right: 8 }}
        onClick={copy}
      >
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}

function Section({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <div className="card" style={{ padding: "16px 18px", marginBottom: 16 }}>
      <h2 style={{ margin: "0 0 10px", fontSize: 16, display: "flex", alignItems: "center", gap: 8 }}>
        <span
          className="badge-brand"
          style={{ width: 22, height: 22, borderRadius: 999, display: "inline-flex", alignItems: "center", justifyContent: "center", padding: 0 }}
        >
          {n}
        </span>
        {title}
      </h2>
      {children}
    </div>
  );
}

export default function Developers() {
  usePageTitle("API & MCP");
  const { can } = useCurrentUser();
  const isAdmin = can("org.manage");
  const origin = window.location.origin;
  const apiBase = `${origin}/api/v1`;
  // The remote MCP connector URL for this cell — always this host, so a
  // tenant sees their own (https://your-name.sluicio.com/api/v1/mcp), never
  // a baked-in example.
  const mcpUrl = `${apiBase}/mcp`;

  const curlExample = `curl -H "Authorization: Bearer $SLUICIO_TOKEN" \\
  ${apiBase}/integrations`;

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">API &amp; MCP</h1>
          <p className="page__subtitle">
            Build on Sluicio: call the REST API from scripts and CI, or connect an AI assistant (Claude, Cursor)
            to your cell with the Model Context Protocol. Everything here uses the same API tokens.
          </p>
        </div>
      </div>

      <Section n={1} title="Get a token">
        <p className="muted" style={{ fontSize: 13, marginTop: 0 }}>
          Every request authenticates with <code>Authorization: Bearer &lt;token&gt;</code> over HTTPS. Two kinds:
        </p>
        <ul style={{ fontSize: 13.5, lineHeight: 1.7, marginTop: 4 }}>
          <li>
            <strong>Personal access token</strong> — for your own scripts/CLI; inherits your role. Create one under{" "}
            <Link to="/account?tab=tokens">Account → Tokens</Link>.
          </li>
          <li>
            <strong>Service account</strong> — a machine identity for CI/automation/MCP, with its own role,
            independent of any person. Create one under{" "}
            <Link to="/settings?tab=service-accounts">Settings → Service accounts</Link>
            {!isAdmin && <span className="muted"> — org-admin only; ask an admin to create one for you</span>}.
          </li>
        </ul>
        <p className="muted" style={{ fontSize: 13 }}>
          Least privilege: cap a token at <strong>read-only</strong> (or editor) when minting it, and set an
          expiry. A read-only token is exactly right for dashboards and the MCP server.
        </p>
      </Section>

      <Section n={2} title="Call the API">
        <p className="muted" style={{ fontSize: 13, marginTop: 0 }}>
          Base URL: <code>{apiBase}</code>. Example — list integrations with their health:
        </p>
        <CopyBlock text={curlExample} />
        <div style={{ display: "flex", gap: 10, marginTop: 12, flexWrap: "wrap" }}>
          <a className="btn primary" href="/api/docs" target="_blank" rel="noreferrer">
            Open the API reference ↗
          </a>
          <a className="btn btn--primary" href="/api/docs" target="_blank" rel="noreferrer">
            API reference — try it live ↗
          </a>
          <a className="btn" href="/api/v1/openapi.json" target="_blank" rel="noreferrer">
            OpenAPI spec (JSON) ↗
          </a>
          <a className="btn" href="/api/v1/llms.txt" target="_blank" rel="noreferrer" title="Compact markdown spec — the token-frugal format for AI tools">
            llms.txt ↗
          </a>
        </div>
        <p className="muted" style={{ fontSize: 12.5, marginTop: 10 }}>
          The reference (Redoc) and the machine-readable OpenAPI document are generated from the live route table,
          so they always match this cell.
        </p>
      </Section>

      <Section n={3} title="Connect an AI assistant (MCP)">
        <p className="muted" style={{ fontSize: 13, marginTop: 0 }}>
          Sluicio exposes a read-only MCP server so Claude Desktop/Code, Cursor, and other MCP clients can answer
          questions about this cell from live data (“which integrations are unhealthy?”, “show the order-bus system”).
          Pair it with a <strong>viewer service-account token</strong> so the assistant can observe but never change
          anything.
        </p>
        <p className="muted" style={{ fontSize: 13 }}>
          Add a <strong>custom connector</strong> in your client pointing at this cell — nothing to install. It rides
          this same host, so it works even in sandboxed clients (e.g. Claude Cowork) that can't reach a local binary.
          Connector URL:
        </p>
        <CopyBlock text={mcpUrl} />
        <p className="muted" style={{ fontSize: 12.5, marginTop: 8 }}>
          Authenticate with a{" "}
          <Link to="/settings?tab=service-accounts">viewer service account</Link>
          {!isAdmin && <span> (org-admin only — ask an admin)</span>}: paste its token as the connector's Bearer
          token, or sign in if the client uses OAuth.
        </p>
        <p className="muted" style={{ fontSize: 12.5, marginTop: 10 }}>
          Tools exposed: integrations, services, systems (+ members), system types, the “in trouble” errors feed,
          the since-last-visit digest, and the metric catalog.
        </p>
      </Section>
    </div>
  );
}
