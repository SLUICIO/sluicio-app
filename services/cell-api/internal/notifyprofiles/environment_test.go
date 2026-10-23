// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package notifyprofiles

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/envrole"
)

// "Production pages, everything else posts to Slack" is a profile for
// production over a default that excludes it.
func TestProfileAppliesTo(t *testing.T) {
	prodOnly := Profile{EnvironmentMode: EnvironmentOnly, Environments: []string{"production"}}
	notProd := Profile{EnvironmentMode: EnvironmentExcept, Environments: []string{"production"}}
	any := Profile{EnvironmentMode: EnvironmentAny}
	cases := []struct {
		p      Profile
		env    string
		hasEnv bool
		want   bool
	}{
		{prodOnly, "production", true, true},
		{prodOnly, "test", true, false},
		{prodOnly, "", true, false},
		{notProd, "production", true, false},
		{notProd, "test", true, true},
		{notProd, "", true, true},
		{any, "test", true, true},
		// An alert that is not about an environment is routed as before,
		// whatever the profile says.
		{prodOnly, "", false, true},
		{notProd, "", false, true},
	}
	for _, c := range cases {
		if got := c.p.AppliesTo(c.env, c.hasEnv); got != c.want {
			t.Errorf("%s %v in %q (hasEnv %v): got %v", c.p.EnvironmentMode, c.p.Environments, c.env, c.hasEnv, got)
		}
	}
}

// "Production environments" follows the roles (envrole).
func TestProfileProductionToken(t *testing.T) {
	t.Cleanup(func() { envrole.Set(envrole.Config{}) })
	envrole.Set(envrole.Config{Roles: map[string]string{"Blablablbu": envrole.Production}})
	prod := Profile{EnvironmentMode: EnvironmentOnly, Environments: []string{envrole.ProductionToken}}
	notProd := Profile{EnvironmentMode: EnvironmentExcept, Environments: []string{envrole.ProductionToken}}
	for env, want := range map[string]bool{"Blablablbu": true, "prod-us": true, "test": false, "": false} {
		if got := prod.AppliesTo(env, true); got != want {
			t.Errorf("only production, %q: %v", env, got)
		}
		if got := notProd.AppliesTo(env, true); got == want {
			t.Errorf("except production, %q: %v", env, got)
		}
	}
}
