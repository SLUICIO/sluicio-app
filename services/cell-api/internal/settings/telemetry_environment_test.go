// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package settings

import "testing"

// v0.11.180 stored one default environment; it carries over.
func TestTelemetryEnvironmentNormalized(t *testing.T) {
	name, none := "Blablablbu", ""
	if v := (TelemetryEnvironment{}).Normalized(); v.OpenOn != OpenOnProduction || v.Roles != nil {
		t.Errorf("zero: %+v", v)
	}
	if v := (TelemetryEnvironment{Default: &name}).Normalized(); v.Roles["Blablablbu"] != "production" || v.OpenOn != OpenOnProduction || v.Default != nil {
		t.Errorf("a named default becomes a production role: %+v", v)
	}
	if v := (TelemetryEnvironment{Default: &none}).Normalized(); v.OpenOn != OpenOnAll {
		t.Errorf("a cleared default opens on all: %+v", v)
	}
	kept := TelemetryEnvironment{Default: &name, Roles: map[string]string{"Blablablbu": "non-production"}}.Normalized()
	if kept.Roles["Blablablbu"] != "non-production" {
		t.Errorf("a confirmed role wins: %+v", kept)
	}
}
