// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package collectors is the registry behind collector management over
// OpAMP: team tokens, the agents that connected with them, and the
// configuration each one reports, stored encrypted with its history.
//
// Reading is open core. Pushing, approval and the advisor build on this
// package later; nothing here sends anything to a collector.
package collectors

import "github.com/open-telemetry/opamp-go/protobufs"

// Kind is the collector type the UI and, later, the push path care about.
type Kind string

const (
	// KindExtension reports status and configuration but takes none: the
	// OpAMP extension in the collector's own configuration, or a
	// supervisor that is not set up to accept remote configuration.
	KindExtension Kind = "extension"
	// KindSupervisor accepts remote configuration: the OpAMP supervisor
	// running the collector as its child process.
	KindSupervisor Kind = "supervisor"
)

// DetectKind derives the type from the capabilities an agent reports.
//
// The capability is what matters, not which program sent it: a supervisor
// configured without accepts_remote_config cannot take a push either, so
// it is read-only here like the extension. Capabilities are re-evaluated
// on every report, so a team that moves from the extension to the
// supervisor shows up as the new type without anything to migrate.
func DetectKind(capabilities uint64) Kind {
	if capabilities&uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig) != 0 {
		return KindSupervisor
	}
	return KindExtension
}

// capabilityNames lists the capabilities in bit order, for display.
var capabilityNames = []struct {
	bit  protobufs.AgentCapabilities
	name string
}{
	{protobufs.AgentCapabilities_AgentCapabilities_ReportsStatus, "reports_status"},
	{protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig, "accepts_remote_config"},
	{protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig, "reports_effective_config"},
	{protobufs.AgentCapabilities_AgentCapabilities_AcceptsPackages, "accepts_packages"},
	{protobufs.AgentCapabilities_AgentCapabilities_ReportsPackageStatuses, "reports_package_statuses"},
	{protobufs.AgentCapabilities_AgentCapabilities_ReportsOwnTraces, "reports_own_traces"},
	{protobufs.AgentCapabilities_AgentCapabilities_ReportsOwnMetrics, "reports_own_metrics"},
	{protobufs.AgentCapabilities_AgentCapabilities_ReportsOwnLogs, "reports_own_logs"},
	{protobufs.AgentCapabilities_AgentCapabilities_AcceptsOpAMPConnectionSettings, "accepts_opamp_connection_settings"},
	{protobufs.AgentCapabilities_AgentCapabilities_AcceptsOtherConnectionSettings, "accepts_other_connection_settings"},
	{protobufs.AgentCapabilities_AgentCapabilities_AcceptsRestartCommand, "accepts_restart_command"},
	{protobufs.AgentCapabilities_AgentCapabilities_ReportsHealth, "reports_health"},
	{protobufs.AgentCapabilities_AgentCapabilities_ReportsRemoteConfig, "reports_remote_config"},
	{protobufs.AgentCapabilities_AgentCapabilities_ReportsHeartbeat, "reports_heartbeat"},
	{protobufs.AgentCapabilities_AgentCapabilities_ReportsAvailableComponents, "reports_available_components"},
	{protobufs.AgentCapabilities_AgentCapabilities_ReportsConnectionSettingsStatus, "reports_connection_settings_status"},
}

// CapabilityNames spells out the set bits. A bit this build does not know
// yet is left out rather than guessed at.
func CapabilityNames(capabilities uint64) []string {
	out := make([]string, 0, len(capabilityNames))
	for _, c := range capabilityNames {
		if capabilities&uint64(c.bit) != 0 {
			out = append(out, c.name)
		}
	}
	return out
}
