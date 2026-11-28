// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Tools for reading collector management (Beta): the OpenTelemetry
// Collectors that report to the cell over OpAMP, by team. Read only, and
// every configuration comes back masked by the server, so a secret in a
// collector's configuration never reaches an assistant. On a cell without
// collector management the routes do not exist and the tools say so.

package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
)

var collectorItem = map[string]any{
	"id":                 strOut("Collector id (uuid); pass to sluicio_get_collector."),
	"group_id":           strOut("The team the collector belongs to (uuid). Every collector belongs to exactly one team: the one whose token it connected with."),
	"group_name":         strOut("That team's name."),
	"instance_uid":       strOut("The agent's own OpAMP instance UID. Unique only within a team."),
	"kind":               strOut("extension (reports only, read only) or supervisor (runs under the OpAMP supervisor and accepts remote configuration)."),
	"capabilities":       strArr("The OpAMP capabilities the agent reported, e.g. reports_effective_config, accepts_remote_config."),
	"description":        objOf("What the agent reported about itself: host.name, service.version, service.instance.id, os.type and so on.", nil),
	"online":             boolOut("Whether it is connected now (WebSocket) or polled within the last 90 seconds (plain HTTP)."),
	"healthy":            boolOut("The collector's own last health report; absent when it reported none."),
	"last_seen_at":       strOut("Last message from the agent (RFC3339)."),
	"config_hash":        strOut("Fingerprint of the newest stored configuration; changes when the configuration does. Absent when none is stored."),
	"config_received_at": strOut("When the newest stored configuration arrived (RFC3339)."),
	"config_owner":       strOut("Who owns the configuration: sluicio (changes are sent from Sluicio) or pipeline (the team's own deployment pipeline; Sluicio sends nothing from its UI and offers its suggestions as a diff)."),
	"drift":              objOf("Whether the collector still runs what was last rolled out to it from Sluicio: deployment_id (the last deployment it applied), paths[] with path and kind (missing: sent but not in its configuration; changed: a different value there; paths only, never values), checked_at. Empty paths means in sync. Absent when not applicable: a read-only collector, none rolled out yet, or no configuration as written reported.", nil),
	"plain_secrets":      strArr("Paths where the configuration as written holds a secret in plain text instead of an ${env:...} reference (paths only, never values). Empty when there is none, or when the collector reports only its effective configuration, where every reference is already resolved. Move each into an environment variable on the host."),
	"plain_secrets_at":   strOut("Since when the configuration has held one (RFC3339); absent when it holds none."),
}

var (
	listCollectorsOut = outSchema(map[string]any{
		"collectors":     arrOf("Collectors you may read: your teams' collectors, or every team's for an org admin.", collectorItem),
		"config_storage": boolOut("False when the cell stores no configurations (no SLUICIO_COLLECTOR_CONFIG_KEY)."),
	}, "collectors")

	getCollectorOut = outSchema(map[string]any{
		"collector": objOf("The collector, as in sluicio_list_collectors, plus health per component.", collectorItem),
		"history": arrOf("Stored configuration versions, newest first. A version is kept whenever the reported configuration changes.", map[string]any{
			"id":          strOut("Version id (uuid)."),
			"hash":        strOut("Its fingerprint."),
			"received_at": strOut("When it arrived (RFC3339)."),
		}),
		"config": objOf("The newest stored configuration, secrets masked: files[] with name, content_type and body (YAML). Absent when none is stored.", nil),
	}, "collector")

	listDeploymentsOut = outSchema(map[string]any{
		"deployments": arrOf("Configurations sent or proposed to the collector from Sluicio, newest first, at most 50. Bodies are not included.", map[string]any{
			"id":                    strOut("Deployment id (uuid)."),
			"collector_id":          strOut("The collector (uuid)."),
			"hash":                  strOut("Fingerprint of the configuration sent."),
			"status":                strOut("proposed (waits for approval), pending (waits to be delivered), held (waits behind a temporary change), sent, applying, applied, failed (the collector refused it; see error_message), superseded (a newer one replaced it before it applied), ended (a temporary change that was reverted)."),
			"error_message":         strOut("The collector's own error, for a failed one."),
			"message":               strOut("Why it was sent, as its author wrote it."),
			"created_by_name":       strOut("Who sent or proposed it."),
			"via":                   strOut("How: ui, api, advisor, stamp, rollback (a Restore) or revert (back to the base after a temporary change)."),
			"proposal_id":           strOut("The proposal it waits on, for a proposed one."),
			"proposal_state":        strOut("That proposal's state: pending, approved, rejected, expired or superseded."),
			"rolled_back_from":      strOut("For a Restore: the deployment it sends again."),
			"advisor_suggestion_id": strOut("The Advisor suggestion it applies, if any."),
			"advisor_class":         strOut("That suggestion's class (T1 to T7)."),
			"advisor_target":        objOf("What the Advisor change drops or changes: metric, service, below, attribute.", nil),
			"duration_seconds":      intOut("Set on a temporary change: how long it runs once applied."),
			"expires_at":            strOut("When a temporary change ends (RFC3339), once applied."),
			"ended_at":              strOut("When a temporary change was reverted (RFC3339)."),
			"ended_reason":          strOut("expired or ended (ended early by a person)."),
			"created_at":            strOut("When it was created (RFC3339)."),
			"sent_at":               strOut("When it was delivered (RFC3339)."),
			"finished_at":           strOut("When the collector answered for it, or it was superseded (RFC3339)."),
		}),
	}, "deployments")
)

func collectorTools(s *Server) []tool {
	return []tool{
		{Name: "sluicio_list_collectors", Output: listCollectorsOut,
			Description: "List the OpenTelemetry Collectors that report to this cell over OpAMP (Beta), with their team, type (extension: read only; supervisor: accepts remote configuration), whether they are online and healthy, what they reported about themselves and when their configuration last changed. Only collectors of teams you may read are listed. Fails with 404 when the cell has collector management off.",
			Schema:      objSchema(map[string]any{}),
			Call: func(map[string]any) (string, error) {
				return s.get("/api/v1/collectors", nil)
			}},
		{Name: "sluicio_get_collector", Output: getCollectorOut,
			Description: "Get one collector (Beta): what it reported about itself, its capabilities, its health per component, its configuration history, and the newest configuration it reported with every secret masked.",
			Schema:      objSchema(map[string]any{"id": strProp("The collector id (uuid) from sluicio_list_collectors.")}, "id"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "id")
				if id == "" {
					return "", fmt.Errorf("id is required")
				}
				base := "/api/v1/collectors/" + url.PathEscape(id)
				raw, err := s.get(base, nil)
				if err != nil {
					return "", err
				}
				var payload map[string]any
				if err := json.Unmarshal([]byte(raw), &payload); err != nil {
					return "", fmt.Errorf("unreadable response")
				}
				if hist, _ := payload["history"].([]any); len(hist) > 0 {
					s.mergeInto(payload, "config", base+"/configs/latest", nil)
				}
				out, err := json.Marshal(payload)
				return string(out), err
			}},
		{Name: "sluicio_list_collector_deployments", Output: listDeploymentsOut,
			Description: "List the configurations sent or proposed to one collector from Sluicio (Beta), newest first: status (proposed, pending, sent, applied, failed and so on), who sent it and why, the collector's error for a failed one, and for an Advisor change what it drops. Read only: it does not send or approve anything. Bodies are not included; sluicio_get_collector has the configuration the collector runs.",
			Schema:      objSchema(map[string]any{"id": strProp("The collector id (uuid) from sluicio_list_collectors.")}, "id"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "id")
				if id == "" {
					return "", fmt.Errorf("id is required")
				}
				return s.get("/api/v1/collectors/"+url.PathEscape(id)+"/deployments", nil)
			}},
	}
}
