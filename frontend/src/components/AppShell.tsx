// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// AppShell — full-width top bar above a (sidebar + main) split.
// Structure and colour follow the Sluicio Color System "Chrome
// anatomy" mockup verbatim:
//
//   ┌─────────────────────────────────────────────────────────────┐
//   │  [S] Sluicio · Integrations / Orders → ERP · ⌕ search… · live · 🕒 · ☀ · RM │  ← --surface-2
//   ├──────────┬──────────────────────────────────────────────────┤
//   │ MONITORING│                                                 │
//   │ Dashboard │                                                 │
//   │ Integ…    │                  main content                   │
//   │ Messages  │                  --surface (page bg)            │
//   │ Errors    │                                                 │
//   │ CONFIG    │                                                 │
//   │ Users     │                                                 │
//   │ Settings  │                                                 │
//   └──────────┴──────────────────────────────────────────────────┘
//
// Top bar background = --surface-2 with --border bottom.
// Sidebar background = --surface-2 with --border right.
// Main background = --surface (warm off-white / deep navy).
// Active nav item = --primary-soft + --primary-ink + --primary glyph.

import { ReactNode, useEffect, useRef, useState } from "react";
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { api } from "../api/client";
import type { GlobalSearchGroup, Organization, OrganizationMembership } from "../api/types";
import { switchToOrg } from "../lib/activeOrg";
import { BreadcrumbProvider, useBreadcrumbLeafValue, useBreadcrumbTrailValue, type Crumb } from "../lib/breadcrumb";
import { useCurrentUser } from "../lib/useCurrentUser";
import { LogoMark } from "./brand/Logo";
import TimeWindowPicker from "./TimeWindowPicker";
import DigestBell from "./DigestBell";
import { AnnouncementsBanner } from "./AnnouncementsBanner";
import { MFAEnrollmentBanner } from "./MFAEnrollmentBanner";
import ForcePasswordChange from "./ForcePasswordChange";
import { IntegrationLimitBanner } from "./IntegrationLimitBanner";

interface NavItem {
  to: string;
  label: string;
  icon: ReactNode;
  // adminOnly items are hidden (and their routes blocked) for anyone
  // without org.manage — i.e. viewers/operators don't see Settings.
  adminOnly?: boolean;
  // requiresWrite items are hidden for read-only users (viewers): the
  // whole Config section is editing surface, so someone who can't write
  // shouldn't see Schemas / Tags / Maps / facets / Alerts in the nav.
  requiresWrite?: boolean;
  // operatorOnly items are hidden for everyone but cell operators.
  operatorOnly?: boolean;
}

interface NavGroup {
  header: string;
  items: NavItem[];
}

// Three intents, three groups: Monitor (live observability views), Configure
// (how the estate is modelled + alerted), and Admin (org + developer access).
const navGroups: NavGroup[] = [
  {
    header: "Monitor",
    items: [
      { to: "/health", label: "Dashboard", icon: <DashboardIcon /> },
      { to: "/integrations", label: "Integrations", icon: <IntegrationsIcon /> },
      { to: "/services", label: "Services", icon: <ServicesIcon /> },
      { to: "/systems", label: "Systems", icon: <SystemsIcon /> },
      { to: "/topology", label: "Topology", icon: <TopologyIcon /> },
      { to: "/search", label: "Messages", icon: <MessageIcon /> },
      { to: "/metrics", label: "Metrics", icon: <MetricsIcon /> },
      { to: "/logs", label: "Logs", icon: <LogsIcon /> },
      { to: "/stuck", label: "Errors", icon: <BellIcon /> },
    ],
  },
  {
    header: "Configure",
    items: [
      { to: "/alerts", label: "Alerts", icon: <BellIcon />, requiresWrite: true },
      { to: "/monitoring-templates", label: "Templates", icon: <TemplateIcon />, requiresWrite: true },
      { to: "/system-types", label: "System types", icon: <SystemsIcon />, requiresWrite: true },
      { to: "/service-facets", label: "Service facets", icon: <FacetsIcon />, requiresWrite: true },
      { to: "/tags", label: "Tags", icon: <TagsIcon />, requiresWrite: true },
      { to: "/metadata-fields", label: "Metadata fields", icon: <MetadataIcon />, requiresWrite: true },
      { to: "/schemas", label: "Schemas", icon: <SchemaIcon />, requiresWrite: true },
      { to: "/maps", label: "Maps", icon: <MapsIcon />, requiresWrite: true },
    ],
  },
  {
    header: "Admin",
    items: [
      { to: "/usage", label: "Usage", icon: <UsageIcon />, adminOnly: true },
      { to: "/developers", label: "API & MCP", icon: <CodeIcon /> },
      { to: "/settings", label: "Settings", icon: <SettingsIcon />, adminOnly: true },
      { to: "/operator", label: "Operator", icon: <OperatorIcon />, operatorOnly: true },
    ],
  },
];

// localStorage key for the sidebar visibility preference. Sticky across
// sessions: on small screens people hide the nav once and expect it to
// stay hidden.
const NAV_HIDDEN_KEY = "sluicio.nav.hidden";

export default function AppShell() {
  const { user } = useCurrentUser();
  const [navHidden, setNavHidden] = useState(() => {
    try {
      return localStorage.getItem(NAV_HIDDEN_KEY) === "1";
    } catch {
      return false;
    }
  });
  const toggleNav = () =>
    setNavHidden((v) => {
      const next = !v;
      try {
        localStorage.setItem(NAV_HIDDEN_KEY, next ? "1" : "0");
      } catch {
        /* private mode etc. — the toggle still works for the session */
      }
      return next;
    });
  // Hard gate: a pending temporary-password change replaces the whole app
  // until resolved (the cell-api 403s everything else anyway).
  if (user.mustResetPassword) {
    return <ForcePasswordChange />;
  }
  return (
    <BreadcrumbProvider>
      <div className="flex h-screen flex-col bg-background text-foreground">
        <TopBar navHidden={navHidden} onToggleNav={toggleNav} />
        <div className="flex min-h-0 flex-1">
          {!navHidden && <SideNav />}
          <main className="flex min-w-0 flex-1 flex-col overflow-auto">
            <div className="flex-1 px-8 py-6">
              <AnnouncementsBanner />
              <MFAEnrollmentBanner />
              <IntegrationLimitBanner />
              <Outlet />
            </div>
          </main>
        </div>
      </div>
    </BreadcrumbProvider>
  );
}

// ── Top bar ────────────────────────────────────────────────────────
function TopBar({ navHidden, onToggleNav }: { navHidden: boolean; onToggleNav: () => void }) {
  return (
    <header
      className="flex h-12 shrink-0 items-center gap-3 border-b border-border bg-surface-2 px-4"
      // Thin brand-blue accent across the very top of the app chrome.
      style={{ borderTop: "2px solid var(--primary)" }}
    >
      <button
        type="button"
        aria-label={navHidden ? "Show navigation" : "Hide navigation"}
        aria-expanded={!navHidden}
        title={navHidden ? "Show navigation" : "Hide navigation"}
        onClick={onToggleNav}
        className="grid h-7 w-7 shrink-0 place-items-center rounded hover:bg-surface-3 focus:outline-none focus-visible:ring-2"
        style={{ color: "var(--muted)" }}
      >
        <HamburgerIcon />
      </button>
      <Brand />
      <OrgBadge />
      <Breadcrumb />
      <TopSearch />
      <div className="flex-1" />
      <LivePulse />
      <EnvLabel />
      <TimeWindowPicker />
      <DigestBell />
      <UserMenu />
    </header>
  );
}

function Brand() {
  // Flow-S mark + "Sluicio" wordmark — the primary brand lockup per
  // the v2 design handoff. Mark inherits color via currentColor and
  // is set to var(--primary) for the standard on-surface use.
  return (
    <div className="flex items-center gap-2" aria-label="Sluicio">
      <LogoMark size={22} style={{ color: "var(--primary)" }} />
      <span className="text-sm font-bold tracking-tight" style={{ color: "var(--primary)" }}>Sluicio</span>
    </div>
  );
}

// OrgBadge shows the current organization (tenant) name next to the
// brand — the "product │ workspace" lockup. Set off by a thin divider so
// it reads as the tenant, not a product sub-brand. Renders nothing until
// the org loads, and truncates long names. With a single membership,
// clicking it goes home (the dashboard), like clicking a workspace name
// in most apps; with several, it opens the org switcher instead.
function OrgBadge() {
  const { organization, memberships } = useCurrentUser();
  if (!organization?.name) return null;
  return (
    <div className="flex items-center gap-2" aria-label="Organization">
      <span aria-hidden style={{ width: 1, height: 18, background: "var(--border)" }} />
      {memberships.length > 1 ? (
        <OrgSwitcher active={organization} memberships={memberships} />
      ) : (
        <Link
          to="/"
          className="truncate text-sm font-medium hover:underline"
          style={{ color: "var(--ink-2)", maxWidth: 200 }}
          title={`${organization.name} — go to dashboard`}
        >
          {organization.name}
        </Link>
      )}
    </div>
  );
}

// OrgSwitcher — dropdown over the user's memberships. Selecting an org
// pins it (localStorage) and hard-reloads to the dashboard: the API
// client stamps X-Sluicio-Org on every request, and the user's role —
// so also nav visibility — can differ per org, so remounting the whole
// app is the honest thing to do.
function OrgSwitcher({
  active,
  memberships,
}: {
  active: Organization;
  memberships: OrganizationMembership[];
}) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement | null>(null);

  // Close on outside click / Escape — same contract as UserMenu.
  useEffect(() => {
    if (!open) return;
    const onPointer = (e: MouseEvent) => {
      if (!wrapRef.current) return;
      if (!wrapRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Switch organization (current: ${active.name})`}
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1 truncate rounded text-sm font-medium hover:underline focus:outline-none focus-visible:ring-2"
        style={{ color: "var(--ink-2)", maxWidth: 220 }}
        title={`${active.name} — switch organization`}
      >
        <span className="truncate">{active.name}</span>
        <svg
          width="12"
          height="12"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden
          style={{ flexShrink: 0, color: "var(--muted)" }}
        >
          <path d="M4 6l4 4 4-4" />
        </svg>
      </button>

      {open && (
        <div
          role="menu"
          className="absolute left-0 top-8 z-50 w-64 overflow-hidden rounded-lg border shadow-lg"
          style={{
            background: "var(--surface-2)",
            borderColor: "var(--border)",
            boxShadow: "var(--shadow-pop, 0 8px 24px rgba(15, 23, 42, 0.18))",
          }}
        >
          <div
            className="border-b px-3 py-2 text-[10px] font-semibold uppercase tracking-wider"
            style={{ borderColor: "var(--border)", color: "var(--muted)" }}
          >
            Organizations
          </div>
          {memberships.map((m) => {
            const isActive = m.organization.id === active.id;
            return (
              <button
                key={m.organization.id}
                type="button"
                role="menuitem"
                className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors hover:bg-surface-3"
                style={{ color: "var(--ink)" }}
                onClick={() => {
                  setOpen(false);
                  if (!isActive) switchToOrg(m.organization.slug);
                }}
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate">{m.organization.name}</span>
                  <span className="block truncate text-xs" style={{ color: "var(--muted)" }}>
                    {m.roles.map((r) => r.name).join(", ") || "No role"}
                  </span>
                </span>
                {isActive && (
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 16 16"
                    fill="none"
                    stroke="var(--primary)"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    aria-hidden
                  >
                    <path d="M3 8.5l3.5 3.5L13 5" />
                  </svg>
                )}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

function Breadcrumb() {
  const { pathname } = useLocation();
  const leaf = useBreadcrumbLeafValue();
  // Pages reached from several origins (the full trace view) set the
  // whole trail themselves; everything else derives it from the URL.
  const trailOverride = useBreadcrumbTrailValue();
  const crumbs = trailOverride ?? trailForPath(pathname, leaf);
  return (
    <div className="ml-2 truncate text-sm" aria-label="Breadcrumb">
      {crumbs.map((c, i) => {
        const last = i === crumbs.length - 1;
        return (
          <span key={`${c.label}-${i}`}>
            {i > 0 && <span style={{ color: "var(--muted)" }}> / </span>}
            {last ? (
              <span className="font-semibold" style={{ color: "var(--ink)" }}>
                {c.label}
              </span>
            ) : c.to ? (
              <Link to={c.to} className="hover:underline" style={{ color: "var(--muted)" }}>
                {c.label}
              </Link>
            ) : (
              <span style={{ color: "var(--muted)" }}>{c.label}</span>
            )}
          </span>
        );
      })}
    </div>
  );
}

// trailForPath builds the breadcrumb trail for a route: a linked section
// crumb, plus an entity leaf on detail pages. The leaf comes from the
// breadcrumb context (set by the page) when the name isn't in the URL
// (e.g. integrations, keyed by id); otherwise it's derived from the URL.
function trailForPath(p: string, leaf: string | null): Crumb[] {
  const seg = (i: number) => {
    const s = p.split("/")[i] || "";
    try {
      return decodeURIComponent(s);
    } catch {
      return s;
    }
  };
  if (p === "/" || p.startsWith("/health")) return [{ label: "Dashboard" }];
  if (p.startsWith("/integrations/new"))
    return [{ label: "Integrations", to: "/integrations" }, { label: "New integration" }];
  if (p.startsWith("/integrations/"))
    return [{ label: "Integrations", to: "/integrations" }, { label: leaf || "Integration" }];
  if (p.startsWith("/integrations")) return [{ label: "Integrations" }];
  if (p.startsWith("/services/"))
    return [{ label: "Services", to: "/services" }, { label: leaf || seg(2) || "Service" }];
  if (p.startsWith("/services")) return [{ label: "Services" }];
  if (p.startsWith("/systems/"))
    return [{ label: "Systems", to: "/systems" }, { label: leaf || "System" }];
  if (p.startsWith("/systems")) return [{ label: "Systems" }];
  if (p.startsWith("/service-facets/"))
    return [{ label: "Service facets", to: "/service-facets" }, { label: leaf || seg(2) }];
  if (p.startsWith("/service-facets")) return [{ label: "Service facets" }];
  if (p.startsWith("/traces/")) {
    const id = seg(2);
    return [{ label: "Trace" }, { label: leaf || (id.length > 12 ? `${id.slice(0, 12)}…` : id) }];
  }
  if (p.startsWith("/search")) return [{ label: "Messages" }];
  if (p.startsWith("/logs")) return [{ label: "Logs" }];
  if (p.startsWith("/metrics")) return [{ label: "Metrics" }];
  if (p.startsWith("/maps")) return [{ label: "Maps" }];
  if (p.startsWith("/tags")) return [{ label: "Tags" }];
  if (p.startsWith("/metadata-fields")) return [{ label: "Metadata fields" }];
  if (p.startsWith("/system-types")) return [{ label: "System types" }];
  if (p.startsWith("/developers")) return [{ label: "API & MCP" }];
  if (p.startsWith("/schemas")) return [{ label: "Schemas" }];
  if (p.startsWith("/topology")) return [{ label: "Topology" }];
  if (p.startsWith("/stuck")) return [{ label: "Errors" }];
  if (p.startsWith("/alerts")) return [{ label: "Alerts" }];
  if (p.startsWith("/settings")) return [{ label: "Settings" }];
  if (p.startsWith("/account")) return [{ label: "Account" }];
  return [{ label: "Sluicio" }];
}

// Global-search window: a finder should reach back further than the
// page's current selector, so it queries a fixed wide window.
const GLOBAL_SEARCH_WINDOW = "7d";

function TopSearch() {
  // The top-bar "search everything" command palette (#28): a debounced
  // query against /global-search, results grouped by source. Click a hit
  // or press Enter to open the top result. (No ⌘K shortcut — that
  // collides with the browser's focus-address-bar binding.)
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [groups, setGroups] = useState<GlobalSearchGroup[]>([]);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  // Debounced fetch. A blank query clears results without a round trip.
  // We open the dropdown as soon as a query is in flight so the loading
  // and error states are visible — a failed request (e.g. a stale
  // backend missing the route) must surface, not fail silently.
  useEffect(() => {
    const term = q.trim();
    if (!term) {
      setGroups([]);
      setLoading(false);
      setError(null);
      setOpen(false);
      return;
    }
    setLoading(true);
    setError(null);
    setOpen(true);
    const t = window.setTimeout(() => {
      api
        .globalSearch(term, GLOBAL_SEARCH_WINDOW)
        .then((r) => {
          setGroups(r.groups ?? []);
        })
        .catch((e) => {
          setGroups([]);
          setError(String(e?.message ?? e));
        })
        .finally(() => setLoading(false));
    }, 200);
    return () => window.clearTimeout(t);
  }, [q]);

  // Close on outside click.
  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, [open]);

  const go = (href: string) => {
    setOpen(false);
    setQ("");
    setGroups([]);
    setError(null);
    navigate(href);
  };

  const firstHref = groups.find((g) => g.hits.length > 0)?.hits[0]?.href;
  const hasResults = groups.some((g) => g.hits.length > 0);

  return (
    <div ref={wrapRef} className="relative ml-4 max-w-[320px] flex-1">
      <div
        className="flex h-8 items-center gap-2 rounded-lg border px-2.5 text-xs"
        style={{ background: "var(--surface-3)", borderColor: "var(--border)" }}
      >
        <span aria-hidden style={{ color: "var(--muted)" }}>
          ⌕
        </span>
        <input
          ref={inputRef}
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onFocus={() => q.trim() && setOpen(true)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              setOpen(false);
              inputRef.current?.blur();
            } else if (e.key === "Enter" && firstHref) {
              go(firstHref);
            }
          }}
          placeholder="Search integrations, services, messages…"
          className="min-w-0 flex-1 bg-transparent text-xs outline-none"
          style={{ color: "var(--ink)" }}
        />
        <kbd
          className="hidden items-center rounded border px-1.5 py-0.5 text-[10px] sm:inline-flex"
          style={{
            background: "var(--surface-2)",
            borderColor: "var(--border)",
            color: "var(--muted)",
            fontFamily: "'JetBrains Mono', ui-monospace, monospace",
          }}
          title="Press Enter to open the top result"
        >
          ⏎
        </kbd>
      </div>

      {open && q.trim() && (
        <div
          role="listbox"
          className="absolute left-0 top-full z-40 mt-1 max-h-[70vh] w-[420px] overflow-auto rounded-lg border p-1 text-sm shadow-lg"
          style={{ background: "var(--surface-2)", borderColor: "var(--border)" }}
        >
          {error ? (
            <div className="px-3 py-3" style={{ color: "var(--err, var(--critical))" }}>
              <div className="font-medium">Search failed</div>
              <div className="mt-0.5 text-xs text-muted">{error}</div>
            </div>
          ) : loading && groups.length === 0 ? (
            <div className="px-3 py-3 text-muted">Searching…</div>
          ) : !hasResults ? (
            <div className="px-3 py-3 text-muted">No matches for “{q.trim()}”.</div>
          ) : null}
          {groups
            .filter((g) => g.hits.length > 0)
            .map((g) => (
              <div key={g.type} className="mb-1">
                <div className="px-2 py-1 text-[10px] uppercase tracking-wide text-muted">
                  {g.label}
                </div>
                {g.hits.map((hit, i) => (
                  <button
                    key={`${g.type}-${i}-${hit.href}`}
                    type="button"
                    role="option"
                    className="block w-full rounded px-2 py-1.5 text-left hover:bg-surface-3"
                    onClick={() => go(hit.href)}
                  >
                    <span className="truncate font-medium">{hit.label || "—"}</span>
                    {hit.sublabel && (
                      <span className="ml-2 text-xs text-muted">· {hit.sublabel}</span>
                    )}
                  </button>
                ))}
                {g.has_more && g.see_all_href && (
                  <button
                    type="button"
                    className="block w-full rounded px-2 py-1 text-left text-xs hover:bg-surface-3"
                    style={{ color: "var(--primary)" }}
                    onClick={() => go(g.see_all_href!)}
                  >
                    See all {g.label.toLowerCase()} →
                  </button>
                )}
              </div>
            ))}
        </div>
      )}
    </div>
  );
}

function LivePulse() {
  // 10s pulse — visual proxy for the polling cycle the design
  // handoff calls for. Dot with an --ok-soft halo, per the spec.
  const [active, setActive] = useState(true);
  useEffect(() => {
    const t = window.setInterval(() => setActive((a) => !a), 10000);
    return () => window.clearInterval(t);
  }, []);
  return (
    <span
      className="inline-flex items-center gap-1.5 text-xs"
      style={{ color: "var(--muted)" }}
      title="Auto-refresh every 10s"
    >
      <span
        aria-hidden
        className="h-2 w-2 rounded-full transition-opacity"
        style={{
          background: "var(--ok)",
          opacity: active ? 1 : 0.35,
          boxShadow: active ? `0 0 0 3px var(--ok-soft)` : "none",
        }}
      />
      live
    </span>
  );
}

function EnvLabel() {
  // The value comes from the cell-wide system setting (Settings → System
  // settings); falls back to "prod" until loaded / if unavailable.
  // Production reads as muted text (calm default); every OTHER
  // environment renders as a soft warning chip — the loud variant is
  // for the non-prod tabs, so prod-vs-staging is distinguishable at a
  // glance and the classic wrong-tab edit gets a visual guard.
  const [env, setEnv] = useState("prod");
  useEffect(() => {
    let cancelled = false;
    api
      .getSystemSettings()
      .then((s) => {
        if (!cancelled && s.environment) setEnv(s.environment);
      })
      .catch(() => {
        /* keep the fallback label */
      });
    return () => {
      cancelled = true;
    };
  }, []);
  const isProd = /^prod(uction)?$/i.test(env.trim());
  if (isProd) {
    return (
      <span className="text-xs uppercase tracking-wide" style={{ color: "var(--muted)" }}>
        env · {env}
      </span>
    );
  }
  return (
    <span
      className="text-xs uppercase tracking-wide"
      title="Non-production environment"
      style={{
        color: "var(--warn-ink)",
        background: "var(--warn-soft)",
        border: "1px solid var(--warn)",
        borderRadius: 999,
        padding: "2px 9px",
        fontWeight: 600,
      }}
    >
      env · {env}
    </span>
  );
}

// ── User menu (avatar + dropdown) ──────────────────────────────────
//
// Clicking the avatar opens a popover anchored to the top-right of
// the viewport. Closes on outside click, Escape, or selecting an
// action. The menu surfaces the user's identity, their org + role,
// and a sign-out action — the natural place to put it instead of
// burying logout in a settings page.

function UserMenu() {
  const { user, organization, roles, signOut, can } = useCurrentUser();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement | null>(null);

  // Close on outside click / Escape.
  useEffect(() => {
    if (!open) return;
    const onPointer = (e: MouseEvent) => {
      if (!wrapRef.current) return;
      if (!wrapRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const roleLabel = roles.map((r) => r.name).join(", ") || "No role";

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Account menu for ${user.name}`}
        onClick={() => setOpen((v) => !v)}
        className="grid h-7 w-7 place-items-center rounded-full text-[11px] font-semibold focus:outline-none focus-visible:ring-2"
        style={{
          background: "var(--primary-soft)",
          color: "var(--primary-ink)",
        }}
        title={user.email}
      >
        {user.initials}
      </button>

      {open && (
        <div
          role="menu"
          className="absolute right-0 top-9 z-50 w-64 overflow-hidden rounded-lg border shadow-lg"
          style={{
            background: "var(--surface-2)",
            borderColor: "var(--border)",
            // Soft drop shadow tinted with --shadow if defined; falls
            // back to a neutral one for older theme palettes.
            boxShadow:
              "var(--shadow-pop, 0 8px 24px rgba(15, 23, 42, 0.18))",
          }}
        >
          {/* Identity header */}
          <div
            className="border-b px-3 py-2.5"
            style={{ borderColor: "var(--border)" }}
          >
            <div
              className="truncate text-sm font-semibold"
              style={{ color: "var(--ink)" }}
            >
              {user.name}
            </div>
            <div
              className="truncate text-xs"
              style={{ color: "var(--muted)" }}
            >
              {user.email}
            </div>
          </div>

          {/* Organization + role */}
          <div
            className="border-b px-3 py-2.5"
            style={{ borderColor: "var(--border)" }}
          >
            <div
              className="text-[10px] font-semibold uppercase tracking-wider"
              style={{ color: "var(--muted)" }}
            >
              Organization
            </div>
            <div
              className="mt-0.5 truncate text-sm"
              style={{ color: "var(--ink)" }}
            >
              {organization.name}
            </div>
            <div
              className="mt-1 text-xs"
              style={{ color: "var(--muted)" }}
            >
              {roleLabel}
            </div>
          </div>

          {/* Actions */}
          <MenuItem
            onSelect={() => {
              setOpen(false);
              navigate("/account");
            }}
          >
            Account settings
          </MenuItem>
          {/* Org settings is admin-only (the /settings route bounces non-admins
              to /health) — so don't surface the item to them at all. */}
          {can("org.manage") && (
            <MenuItem
              onSelect={() => {
                setOpen(false);
                navigate("/settings");
              }}
            >
              Organization settings
            </MenuItem>
          )}
          <div
            className="my-1 border-t"
            style={{ borderColor: "var(--border)" }}
          />
          <MenuItem
            onSelect={() => {
              setOpen(false);
              signOut();
            }}
            danger
          >
            Sign out
          </MenuItem>
        </div>
      )}
    </div>
  );
}

function MenuItem({
  children,
  onSelect,
  disabled,
  danger,
}: {
  children: ReactNode;
  onSelect: () => void;
  disabled?: boolean;
  danger?: boolean;
}) {
  return (
    <button
      type="button"
      role="menuitem"
      disabled={disabled}
      onClick={onSelect}
      className="block w-full px-3 py-2 text-left text-sm transition-colors disabled:cursor-not-allowed disabled:opacity-50 hover:enabled:bg-surface-3"
      style={{
        color: danger ? "var(--danger, #b91c1c)" : "var(--ink-2)",
      }}
    >
      {children}
    </button>
  );
}

// ── Side nav ───────────────────────────────────────────────────────
function SideNav() {
  const { can, isOperator } = useCurrentUser();
  const canWrite = can("integration.write");
  const canManage = can("org.manage");
  // Org-wide failing-health-check count for the Errors nav pill — every
  // firing health check across all services + integrations. Refreshed on a
  // slow poll so it stays current without hammering the errors feed.
  const [failingChecks, setFailingChecks] = useState(0);
  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api
        .errorsFeed()
        .then((r) => {
          if (!cancelled) setFailingChecks(r.counts?.failing_checks ?? 0);
        })
        .catch(() => {});
    load();
    const t = setInterval(load, 60_000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, []);
  // Drop admin-only items (Settings) for non-admins and write-only items
  // (the whole Config section) for read-only viewers, then any group left
  // empty. A viewer is left with just the Monitoring section.
  const visibleGroups = navGroups
    .map((g) => ({
      ...g,
      items: g.items.filter(
        (it) =>
          (!it.adminOnly || canManage) &&
          (!it.requiresWrite || canWrite) &&
          (!it.operatorOnly || isOperator),
      ),
    }))
    .filter((g) => g.items.length > 0);
  return (
    <aside
      className="flex w-[200px] shrink-0 flex-col border-r border-border bg-surface-2 py-3"
    >
      <nav className="flex-1 space-y-3 px-2">
        {visibleGroups.map((group, gi) => (
          <div key={group.header}>
            <div
              className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider"
              style={{ color: "var(--muted)" }}
            >
              {group.header}
            </div>
            <div className="space-y-0.5">
              {group.items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  className={({ isActive }) =>
                    [
                      "flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-sm transition-colors",
                      isActive ? "" : "hover:bg-surface-3",
                    ].join(" ")
                  }
                  style={({ isActive }) => ({
                    background: isActive ? "var(--primary-soft)" : undefined,
                    color: isActive ? "var(--primary-ink)" : "var(--ink-2)",
                    fontWeight: isActive ? 600 : 400,
                  })}
                >
                  {({ isActive }) => (
                    <>
                      <span
                        className="h-4 w-4 shrink-0"
                        style={{
                          color: isActive ? "var(--primary)" : "var(--muted)",
                        }}
                      >
                        {item.icon}
                      </span>
                      <span>{item.label}</span>
                      {item.to === "/stuck" && failingChecks > 0 && (
                        <span
                          className="ml-auto"
                          style={{
                            font: "500 11px 'JetBrains Mono', monospace",
                            padding: "0 6px",
                            borderRadius: 999,
                            background: "var(--err-soft)",
                            color: "var(--err-ink)",
                          }}
                          title={`${failingChecks} failing health check${failingChecks === 1 ? "" : "s"} across all services and integrations`}
                        >
                          {failingChecks}
                        </span>
                      )}
                    </>
                  )}
                </NavLink>
              ))}
            </div>
            {gi < visibleGroups.length - 1 && (
              <div className="mt-2 border-t border-border" />
            )}
          </div>
        ))}
      </nav>
      <SidebarFooter />
    </aside>
  );
}

// SidebarFooter — the version block pinned to the bottom of the nav.
// Product identity only: the environment label lives SOLELY in the top
// bar (EnvLabel) — the sidebar is collapsible, and "which environment
// am I in" must survive a collapsed sidebar, so it isn't duplicated
// here. The version is the real package.json version injected at build
// time; the channel reflects the actual build (dev server vs bundle).
function SidebarFooter() {
  const channel = import.meta.env.DEV ? "dev" : "prod";
  return (
    <div
      className="mt-2 border-t border-border px-4 py-3 text-xs"
      style={{ color: "var(--muted)" }}
    >
      <div>
        {/* The git-derived version already carries a leading "v"
            (v0.1.0-dirty); the package.json fallback ("0.0.0") doesn't.
            Strip any leading v and re-add one so we never render "vv…".
            The version links to the GitHub releases — the in-product
            pointer to the project (changelogs, issues, source). */}
        <a
          href="https://github.com/SLUICIO/sluicio-app/releases"
          target="_blank"
          rel="noreferrer"
          className="hover:underline"
          style={{ color: "inherit" }}
          title="Sluicio on GitHub — release notes and source"
        >
          v{String(__APP_VERSION__).replace(/^v/i, "")} · {channel}
        </a>
      </div>
    </div>
  );
}

// ── Icons ──────────────────────────────────────────────────────────
// Tiny inline SVGs at 16×16 with currentColor strokes so they pick up
// the surrounding text color (set via inline style above for the
// active/inactive nav states).

// HamburgerIcon — the nav show/hide toggle in the top bar.
function HamburgerIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden>
      <path d="M2.5 4h11M2.5 8h11M2.5 12h11" />
    </svg>
  );
}

function DashboardIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
      <rect x="2" y="2" width="5" height="6" rx="1" />
      <rect x="9" y="2" width="5" height="4" rx="1" />
      <rect x="9" y="8" width="5" height="6" rx="1" />
      <rect x="2" y="10" width="5" height="4" rx="1" />
    </svg>
  );
}

function ServicesIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
      <rect x="2" y="2" width="5" height="5" rx="1" />
      <rect x="9" y="2" width="5" height="5" rx="1" />
      <rect x="2" y="9" width="5" height="5" rx="1" />
      <rect x="9" y="9" width="5" height="5" rx="1" />
    </svg>
  );
}

function SystemsIcon() {
  // A database/infrastructure cylinder — distinct from the Services grid.
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" aria-hidden>
      <ellipse cx="8" cy="3.5" rx="5" ry="1.8" />
      <path d="M3 3.5v9c0 1 2.2 1.8 5 1.8s5-.8 5-1.8v-9" />
      <path d="M3 8c0 1 2.2 1.8 5 1.8s5-.8 5-1.8" />
    </svg>
  );
}

function TopologyIcon() {
  // A small dependency graph — one node feeding two others.
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <circle cx="3.5" cy="8" r="1.8" />
      <circle cx="12.5" cy="4" r="1.8" />
      <circle cx="12.5" cy="12" r="1.8" />
      <path d="M5.2 7.1l5.5-2.2" />
      <path d="M5.2 8.9l5.5 2.2" />
    </svg>
  );
}

function IntegrationsIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden>
      <path d="M6.5 9.5l3-3" />
      <path d="M9 4l1-1a2.5 2.5 0 113.5 3.5l-1 1" />
      <path d="M7 12l-1 1a2.5 2.5 0 11-3.5-3.5l1-1" />
    </svg>
  );
}

// TagsIcon shows two stacked tags — distinct from the single tag we
// reuse for Service types so the sidebar reads at a glance.
function TagsIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" aria-hidden>
      <path d="M4 4.5V1.5h3L13 7.5l-3 3-6-6z" />
      <path d="M2 7.5V4.5h3" />
      <circle cx="6" cy="3.5" r="0.7" fill="currentColor" stroke="none" />
    </svg>
  );
}

// TemplateIcon — a layout/template frame (header bar + split body).
function TemplateIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" aria-hidden>
      <rect x="2" y="2.5" width="12" height="11" rx="1.5" />
      <line x1="2" y1="6" x2="14" y2="6" />
      <line x1="6.5" y1="6" x2="6.5" y2="13.5" />
    </svg>
  );
}

// FacetsIcon — overlapping shapes, for the data-flow "facets" of a service.
function FacetsIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
      <circle cx="6" cy="8" r="4" />
      <circle cx="10" cy="8" r="4" />
    </svg>
  );
}

// MetadataIcon — key/value rows (a dot + value line per field).
function MetadataIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden>
      <circle cx="3.5" cy="4" r="0.9" fill="currentColor" stroke="none" />
      <line x1="6" y1="4" x2="13.5" y2="4" />
      <circle cx="3.5" cy="8" r="0.9" fill="currentColor" stroke="none" />
      <line x1="6" y1="8" x2="13.5" y2="8" />
      <circle cx="3.5" cy="12" r="0.9" fill="currentColor" stroke="none" />
      <line x1="6" y1="12" x2="13.5" y2="12" />
    </svg>
  );
}

// SchemaIcon — curly braces, the JSON-schema glyph.
function CodeIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M6 5 3 8l3 3" />
      <path d="m10 5 3 3-3 3" />
    </svg>
  );
}

function SchemaIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M6.5 2.5C5 2.5 4.5 3.3 4.5 4.5v1.6c0 .9-.6 1.4-1.5 1.4.9 0 1.5.5 1.5 1.4v1.6c0 1.2.5 2 2 2" />
      <path d="M9.5 2.5c1.5 0 2 .8 2 2v1.6c0 .9.6 1.4 1.5 1.4-.9 0-1.5.5-1.5 1.4v1.6c0 1.2-.5 2-2 2" />
    </svg>
  );
}

// MapsIcon — two opposing arrows: a transform / mapping.
function MapsIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M2.5 5.5h9" />
      <path d="M9 3l2.5 2.5L9 8" />
      <path d="M13.5 10.5h-9" />
      <path d="M7 8l-2.5 2.5L7 13" />
    </svg>
  );
}

// OperatorIcon — a shield (cell super-admin).
function OperatorIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M8 1.5l5 2v3.5c0 3.2-2.1 5.6-5 7-2.9-1.4-5-3.8-5-7V3.5l5-2z" />
      <path d="M5.8 8l1.5 1.5L10.4 6.4" />
    </svg>
  );
}

// SettingsIcon — a gear/cog.
function SettingsIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <circle cx="8" cy="8" r="2.2" />
      <path d="M8 1.6v2M8 12.4v2M1.6 8h2M12.4 8h2M3.4 3.4l1.5 1.5M11.1 11.1l1.5 1.5M12.6 3.4l-1.5 1.5M4.9 11.1l-1.5 1.5" />
    </svg>
  );
}

function MessageIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden>
      <path d="M2 4.5L8 9l6-4.5" />
      <rect x="2" y="3" width="12" height="10" rx="1.5" />
    </svg>
  );
}

function LogsIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden>
      <rect x="2.5" y="2" width="11" height="12" rx="1.5" />
      <path d="M5 5.5h6M5 8h6M5 10.5h4" />
    </svg>
  );
}

function UsageIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M2 13V3" />
      <path d="M2 13h12" />
      <rect x="4" y="8" width="2.2" height="3" />
      <rect x="7.4" y="5.5" width="2.2" height="5.5" />
      <rect x="10.8" y="9.5" width="2.2" height="1.5" />
    </svg>
  );
}

function MetricsIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M2 13V3" />
      <path d="M2 13h12" />
      <path d="M4.5 10.5l3-3 2 2 3.5-4" />
    </svg>
  );
}

function BellIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M4 11V7a4 4 0 018 0v4" />
      <path d="M2.5 11h11" />
      <path d="M6.5 13.5a1.5 1.5 0 003 0" />
    </svg>
  );
}
