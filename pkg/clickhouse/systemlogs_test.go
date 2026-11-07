// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package clickhouse

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Every deployment gives its bundled ClickHouse deploy/clickhouse/sluicio-logs.xml,
// word for word: the compose files inline (so each stays one file), the
// Helm chart and the AKS manifest as a file. A deployment without it lets
// ClickHouse's own logs fill the disk (deploy/clickhouse/README.md).
func TestEveryDeploymentConfiguresClickHouseLogs(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	canonical := read("deploy/clickhouse/sluicio-logs.xml")
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(canonical), "\n") {
		lines = append(lines, strings.TrimSpace(l))
	}
	// containsInOrder: the config's lines appear one after another,
	// whatever the indentation the file needs.
	containsInOrder := func(s string) bool {
		var got []string
		for _, l := range strings.Split(s, "\n") {
			got = append(got, strings.TrimSpace(l))
		}
		for i := range got {
			if i+len(lines) <= len(got) && slices.Equal(got[i:i+len(lines)], lines) {
				return true
			}
		}
		return false
	}
	for _, p := range []string{
		"docker-compose.yml",
		"deploy/quickstart/docker-compose.yml",
		"deploy/server/docker-compose.yml",
		"deploy/server/docker-compose.registry.yml",
		"deploy/server/docker-compose.bundled-clickhouse.yml",
	} {
		s := read(p)
		if !containsInOrder(s) {
			t.Errorf("%s: SLUICIO_CLICKHOUSE_CONFIG differs from deploy/clickhouse/sluicio-logs.xml", p)
		}
		if !strings.Contains(s, `> /etc/clickhouse-server/config.d/sluicio.xml && exec /entrypoint.sh`) {
			t.Errorf("%s: the ClickHouse service does not write its configuration before starting", p)
		}
	}
	if read("deploy/helm/cell/files/clickhouse-sluicio-logs.xml") != canonical {
		t.Error("the Helm chart's copy differs from deploy/clickhouse/sluicio-logs.xml")
	}
	if !containsInOrder(read("deploy/azure/aks/clickhouse.yaml")) {
		t.Error("deploy/azure/aks/clickhouse.yaml differs from deploy/clickhouse/sluicio-logs.xml")
	}

	// And the cleanup drops exactly the logs the configuration turns off.
	var off []string
	for _, m := range regexp.MustCompile(`<([a-z_]+) remove="1"/>`).FindAllStringSubmatch(canonical, -1) {
		off = append(off, m[1])
	}
	if !slices.Equal(off, switchedOffLogs) {
		t.Errorf("the configuration turns off %v, the cleanup drops %v", off, switchedOffLogs)
	}
}
