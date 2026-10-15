// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package configtransfer

import (
	"encoding/json"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
)

// An email channel imported into a managed instance keeps its recipients
// and loses its server: the API refuses those keys on write, so an import
// must not be the way round it.
func TestWithoutKeysDropsTheServerKeepsTheRecipients(t *testing.T) {
	in := json.RawMessage(`{"to":"ops@example.test","smtp_host":"smtp.example.test","smtp_port":"587","from":"a@example.test","username":"u","password":"p"}`)
	out, err := withoutKeys(in, alerting.ChannelTransportKeys())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["to"] != "ops@example.test" {
		t.Errorf("want only the recipients left, got %v", m)
	}
	if out, err := withoutKeys(nil, alerting.ChannelTransportKeys()); err != nil || len(out) != 0 {
		t.Errorf("an empty config should stay empty, got %q (err %v)", out, err)
	}
}
