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
	}
}
