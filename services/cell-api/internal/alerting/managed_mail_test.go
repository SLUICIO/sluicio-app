// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The delivery half of "email belongs to the deployment".
//
// The API refuses a server key on a managed instance, which stops new
// ones being saved. It does nothing about a channel that already carries
// one - saved before the instance became managed, or imported with a
// config bundle. Ignoring them here is what makes the rule true rather
// than merely announced.

package alerting

import (
	"context"
	"testing"
)

func withSystemMail(t *testing.T, sys map[string]string, isManaged bool) {
	t.Helper()
	prevResolver, prevManaged := systemMailDefaults, managed
	SetSystemMailResolver(func(context.Context) map[string]string {
		// A copy per call: the real resolver builds a fresh map, and
		// effectiveMailConfig writes into what it is given.
		out := map[string]string{}
		for k, v := range sys {
			out[k] = v
		}
		return out
	})
	SetManaged(isManaged)
	t.Cleanup(func() {
		systemMailDefaults, managed = prevResolver, prevManaged
	})
}

func TestAManagedInstanceIgnoresAChannelsOwnServer(t *testing.T) {
	withSystemMail(t, map[string]string{
		"smtp_host": "platform-relay", "smtp_port": "2525", "from": "notifications@platform.test",
	}, true)

	got := effectiveMailConfig(context.Background(), map[string]string{
		"to":        "on-call@acme.test",
		"smtp_host": "smuggled.acme.test",
		"from":      "spoofed@acme.test",
		"username":  "u",
		"password":  "p",
	})

	if got["smtp_host"] != "platform-relay" {
		t.Errorf("smtp_host = %q; a channel overrode the deployment's server", got["smtp_host"])
	}
	if got["from"] != "notifications@platform.test" {
		t.Errorf("from = %q; a channel overrode the deployment's sender", got["from"])
	}
	if got["username"] != "" || got["password"] != "" {
		t.Errorf("credentials leaked from the channel: username=%q password set=%v",
			got["username"], got["password"] != "")
	}
	// And the one thing that IS the customer's survives.
	if got["to"] != "on-call@acme.test" {
		t.Errorf("to = %q, want the channel's recipients", got["to"])
	}
}

func TestASelfHostedChannelStillOverridesTheSystemServer(t *testing.T) {
	withSystemMail(t, map[string]string{
		"smtp_host": "system.acme.test", "from": "alerts@acme.test",
	}, false)

	got := effectiveMailConfig(context.Background(), map[string]string{
		"to":        "on-call@acme.test",
		"smtp_host": "channel.acme.test",
	})

	if got["smtp_host"] != "channel.acme.test" {
		t.Errorf("smtp_host = %q, want the channel's own server on a self-hosted install", got["smtp_host"])
	}
	// Unset keys still fall back to the system settings, as they always have.
	if got["from"] != "alerts@acme.test" {
		t.Errorf("from = %q, want the system default", got["from"])
	}
}
