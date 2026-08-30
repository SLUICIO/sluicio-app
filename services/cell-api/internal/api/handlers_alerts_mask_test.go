// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
)

// Channel listing is open to every member, so credentials in the config
// must not ride along.
func TestChannelSecretsAreMasked(t *testing.T) {
	in := []alerting.NotificationChannel{{
		Name: "ahasend",
		Kind: alerting.ChannelWebhook,
		Config: map[string]string{
			"url":         "https://api.ahasend.com/v2/email/send",
			"auth_header": "Bearer super-secret",
			"secret":      "hmac-key",
			"format":      "template",
		},
	}}
	out := maskChannelSecrets(in)
	if out[0].Config["auth_header"] != SecretMask {
		t.Fatalf("auth_header leaked: %q", out[0].Config["auth_header"])
	}
	if out[0].Config["secret"] != SecretMask {
		t.Fatalf("secret leaked: %q", out[0].Config["secret"])
	}
	// Not a credential: the form needs it, and it is not a token.
	if out[0].Config["url"] == SecretMask {
		t.Fatal("url should not be masked")
	}
	// The input must not be mutated — it is the store's own map.
	if in[0].Config["auth_header"] != "Bearer super-secret" {
		t.Fatal("masking mutated the source channel")
	}
}

// A form that only ever saw the mask must not be able to erase the token
// by saving.
func TestMaskedSecretsArePreservedOnSave(t *testing.T) {
	prev := map[string]string{"auth_header": "Bearer real", "url": "https://x"}
	next := map[string]string{"auth_header": SecretMask, "url": "https://y"}
	got := keepMaskedSecrets(next, prev)
	if got["auth_header"] != "Bearer real" {
		t.Fatalf("token not restored: %q", got["auth_header"])
	}
	if got["url"] != "https://y" {
		t.Fatalf("non-secret edit lost: %q", got["url"])
	}
}

// A real new value replaces the old one — the mask is only a placeholder.
func TestReplacingASecretWorks(t *testing.T) {
	got := keepMaskedSecrets(
		map[string]string{"auth_header": "Bearer new"},
		map[string]string{"auth_header": "Bearer old"},
	)
	if got["auth_header"] != "Bearer new" {
		t.Fatalf("replacement lost: %q", got["auth_header"])
	}
}

// The mask arriving with nothing stored is not a value.
func TestMaskWithNoStoredSecretIsDropped(t *testing.T) {
	got := keepMaskedSecrets(map[string]string{"auth_header": SecretMask}, map[string]string{})
	if _, ok := got["auth_header"]; ok {
		t.Fatalf("stored the mask as a token: %v", got)
	}
}
