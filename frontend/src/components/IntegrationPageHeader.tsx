// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationPageHeader — the shared header used across every
// integration page (Overview, Messages, Logs, Settings) so the
// identity of the integration is consistent regardless of which tab
// you're on.
//
// Layout (left-aligned column):
//   - breadcrumb     "integrations / orders-team"
//   - title row      "Orders Team [status-pip]"
//   - stats line     "3 services · description · last updated 5m ago"
//   - tags           read-only chips, on every tab
//   - belowStats     optional slot (Overview puts tag EDITING here,
//                    which supersedes the read-only chips)
//
// Right side (header__actions):
//   - "Edit integration", on EVERY tab
//   - actions        optional slot (Overview adds Clone + Delete)
//
// The header deliberately mirrors what the Overview page used to
// render inline — Overview just passes its tag picker as `belowStats`
// and its Delete button as `actions`.
//
// Tags render here rather than only on Overview because they are part
// of the identity this header exists to keep constant: which team or
// environment an integration belongs to is exactly the context you want
// while reading its logs or messages, and it used to vanish the moment
// you left the first tab. Overview still supersedes them with the
// editable picker, so the chips are never shown twice.

import { Link } from "react-router-dom";
import { useCanOpenServices } from "../lib/useNavigationReach";
import type { ReactNode } from "react";
import { StatusPip, pipForStatus } from "./primitives";
import TagChip from "./tags/TagChip";
import type { IntegrationDetail, ServiceStatus, Tag } from "../api/types";
import { formatRelative, statusLabel } from "../lib/format";
import { useBreadcrumbLeaf } from "../lib/breadcrumb";
import { useCurrentUser } from "../lib/useCurrentUser";

interface Props {
  // Source of the displayed identity. null while loading.
  detail: IntegrationDetail | null;
  // Optional right-side header slot — Overview uses it for Clone and
  // Delete. "Edit integration" is not passed in: it renders here for
  // every tab, so it does not vanish the moment you leave Overview.
  actions?: ReactNode;
  // Optional slot rendered under the stats line — Overview uses it
  // for the tag EDITOR, which replaces the read-only chips below.
  belowStats?: ReactNode;
}

export default function IntegrationPageHeader({ detail, actions, belowStats }: Props) {
  const canOpenServices = useCanOpenServices();
  // Same expression Overview used when it owned this button: the server
  // decides per integration (group editors only manage what is fully in
  // their scope), with the capability as the fallback when the field is
  // absent. Viewers see nothing.
  const { can } = useCurrentUser();
  const canEdit = detail?.can_manage ?? can("integration.write");
  // Feed the integration's name to the top-bar breadcrumb (its route
  // carries the id, not the name). Covers Overview/Services/Settings/Logs.
  useBreadcrumbLeaf(detail?.integration.name);
  return (
    <header className="flex items-start justify-between gap-4">
      <div className="min-w-0">
        <p className="text-xs uppercase tracking-wide text-muted">
          <Link to="/integrations" className="hover:underline">integrations</Link>
          {" / "}
          {detail?.integration.slug ?? "—"}
        </p>
        <h1 className="mt-1 flex items-center gap-3 text-2xl font-semibold">
          <span>{detail?.integration.name ?? "Loading…"}</span>
          {detail?.status && (
            <StatusPip
              kind={pipForStatus(detail.status)}
              label={statusLabel(detail.status as ServiceStatus)}
            />
          )}
        </h1>
        {detail && (
          <p className="mt-1 text-sm text-muted">
            {/* The service count is dropped for a reader who cannot open
                one (issue #32). It was the third place on this page
                saying the same thing, after the KPI tile and the list
                column, and a count still describes an estate. The
                description and the timestamp are about the integration
                itself and stay. */}
            {canOpenServices && (
              <>
                {detail.services?.length ?? 0} service
                {(detail.services?.length ?? 0) === 1 ? "" : "s"}
                {" · "}
              </>
            )}
            {detail.integration.description && <>{detail.integration.description} · </>}
            <>last updated {formatRelative(detail.integration.updated_at)}</>
          </p>
        )}
        {belowStats ?? <HeaderTags tags={detail?.tags ?? []} />}
      </div>
      {(canEdit || actions) && (
        <div className="flex items-center gap-2 flex-shrink-0">
          {actions}
          {canEdit && detail && (
            <Link
              className="btn primary"
              to={`/integrations/${encodeURIComponent(detail.integration.id)}/settings`}
            >
              ✎ Edit integration
            </Link>
          )}
        </div>
      )}
    </header>
  );
}

// HeaderTags is the read-only strip. It renders nothing at all when
// there are no tags: an "untagged" label on every tab of every
// integration is noise, and the Overview picker is where you'd go to
// add one anyway.
function HeaderTags({ tags }: { tags: Tag[] }) {
  if (tags.length === 0) return null;
  return (
    <div className="mt-2 flex items-center gap-2 flex-wrap">
      <span
        className="muted"
        style={{ fontSize: 12, textTransform: "uppercase", letterSpacing: 0.5 }}
      >
        tags
      </span>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
        {tags.map((t) => (
          <TagChip key={t.id} tag={t} />
        ))}
      </div>
    </div>
  );
}
