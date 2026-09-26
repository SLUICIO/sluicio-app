// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Email belongs to the deployment on a managed instance.
//
// The customer keeps what is theirs - who gets alerted, and which alerts
// go to which channel. What they no longer choose is the SERVER, because
// on an instance run by a platform on behalf of someone else the mail
// transport is part of the service.
//
// The two halves have to agree. If the API refused a server key but
// delivery still honoured one already stored, a channel saved before the
// instance became managed would go on using its own server, quietly.

package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
)

func TestAManagedEmailChannelChoosesRecipientsOnly(t *testing.T) {
	// Every key that picks a server is refused, one at a time, so a new
	// one added to the list without a thought here shows up as a failure
	// rather than as a hole.
	for _, key := range alerting.ChannelTransportKeys() {
		req := &channelRequest{
			Name: "ops", Kind: alerting.ChannelEmail,
			Config: map[string]string{"to": "on-call@example.test", key: "something"},
		}
		err := validateChannel(req, true)
		if err == nil {
			t.Errorf("managed mode accepted config.%s on an email channel", key)
			continue
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("config.%s: the error does not name the key: %v", key, err)
		}
	}
}

func TestAManagedEmailChannelStillNeedsRecipients(t *testing.T) {
	req := &channelRequest{Name: "ops", Kind: alerting.ChannelEmail, Config: map[string]string{}}
	if err := validateChannel(req, true); err == nil {
		t.Error("an email channel with no recipients was accepted")
	}
	// And a channel that only says where mail goes is fine, which is the
	// whole point: recipients stay the customer's.
	ok := &channelRequest{
		Name: "ops", Kind: alerting.ChannelEmail,
		Config: map[string]string{"to": "on-call@example.test, second@example.test"},
	}
	if err := validateChannel(ok, true); err != nil {
		t.Errorf("a recipients-only channel was refused: %v", err)
	}
}

func TestSelfHostedEmailChannelsKeepTheirOwnServer(t *testing.T) {
	// The behaviour that must not change: a self-hosted install has always
	// let a channel carry its own SMTP server, and still does.
	req := &channelRequest{
		Name: "ops", Kind: alerting.ChannelEmail,
		Config: map[string]string{
			"to": "on-call@example.test", "smtp_host": "mail.example.test",
			"from": "alerts@example.test", "username": "u", "password": "p",
		},
	}
	if err := validateChannel(req, false); err != nil {
		t.Errorf("a self-hosted channel with its own server was refused: %v", err)
	}
}

func TestOtherChannelKindsAreUntouchedByManagedMode(t *testing.T) {
	// Only email is affected. A webhook, Slack or PagerDuty channel has
	// nothing to do with the deployment's mail transport.
	for _, kind := range []string{alerting.ChannelWebhook, alerting.ChannelSlack} {
		req := &channelRequest{
			Name: "ops-" + kind, Kind: kind,
			Config: map[string]string{"url": "https://hooks.example.test/x"},
		}
		if err := validateChannel(req, true); err != nil {
			t.Errorf("%s channel refused on a managed instance: %v", kind, err)
		}
	}
}

func TestTheRefusedKeysAndTheIgnoredKeysAreOneList(t *testing.T) {
	// The API refuses these on write; delivery ignores them on send. Two
	// separate lists would drift, and the drift is silent: a key refused
	// but not ignored lets an old channel keep its own server for ever.
	keys := alerting.ChannelTransportKeys()
	if len(keys) == 0 {
		t.Fatal("no transport keys at all: a managed channel could set anything")
	}
	for _, want := range []string{"smtp_host", "smtp_port", "from", "username", "password"} {
		if !slices.Contains(keys, want) {
			t.Errorf("%q is not in the transport-key list, so it is neither refused nor ignored", want)
		}
	}
	// "to" must NOT be in it - that is the one thing a channel does choose.
	if slices.Contains(keys, "to") {
		t.Error(`"to" is in the transport-key list, which would refuse every managed email channel`)
	}
}
