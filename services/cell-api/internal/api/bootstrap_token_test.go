// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Who may claim a fresh instance.
//
// A self-hosted install needs no token: whoever reaches the first-run
// screen is whoever just installed it. A MANAGED instance - one run by a
// platform on behalf of someone else - is reachable before its owner has
// ever opened it, so without a token the first visitor to find the
// address would own it, along with every service it goes on to monitor.
//
// The cases below are the ones that decide that, and one of them is the
// deployment mistake: managed mode on with no token configured must
// refuse every claim rather than leave it open.

package api

import (
	"testing"
	"time"
)

func TestBootstrapTokenGate(t *testing.T) {
	const secret = "s3cret-claim-token"

	cases := []struct {
		name       string
		managed    bool
		configured string
		presented  string
		want       bool
	}{
		{
			name:    "self-hosted needs no token",
			managed: false, configured: "", presented: "",
			want: true,
		},
		{
			name:    "self-hosted ignores one that is sent anyway",
			managed: false, configured: "", presented: "whatever",
			want: true,
		},
		{
			name:    "managed accepts the right token",
			managed: true, configured: secret, presented: secret,
			want: true,
		},
		{
			name:    "managed refuses a wrong token",
			managed: true, configured: secret, presented: "not-the-token",
			want: false,
		},
		{
			name:    "managed refuses a missing token",
			managed: true, configured: secret, presented: "",
			want: false,
		},
		{
			// The deployment mistake. An instance nobody can claim is a
			// support call; an instance anybody can claim is somebody
			// else's data.
			name:    "managed with no token configured refuses everything",
			managed: true, configured: "", presented: "anything",
			want: false,
		},
		{
			// Whitespace around a token pasted out of an email or a URL
			// fragment must not be the reason a claim fails.
			name:    "managed tolerates surrounding whitespace",
			managed: true, configured: secret, presented: "  " + secret + "\n",
			want: true,
		},
		{
			// A near-miss must fail: this is the case a length-only or
			// prefix comparison would let through.
			name:    "managed refuses a token that only shares a prefix",
			managed: true, configured: secret, presented: secret[:len(secret)-1] + "X",
			want: false,
		},
	}

	for _, c := range cases {
		h := &Handlers{Managed: c.managed, BootstrapToken: c.configured}
		if got := h.bootstrapTokenOK(c.presented); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// The claim token can also lapse. Whoever issues it owns its lifetime -
// this is the backstop for a setup link that was sent and never used.
func TestAnExpiredClaimTokenIsRefused(t *testing.T) {
	const secret = "s3cret-claim-token"
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)

	cases := []struct {
		name    string
		expires time.Time
		want    bool
	}{
		{"no expiry set", time.Time{}, true},
		{"expiry in the future", future, true},
		{"expiry in the past", past, false},
	}
	for _, c := range cases {
		h := &Handlers{Managed: true, BootstrapToken: secret, BootstrapTokenExpiresAt: c.expires}
		if got := h.bootstrapTokenOK(secret); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}

	// An expired token is refused exactly as a wrong one is, so a holder
	// of a stale link and somebody guessing get the same answer.
	h := &Handlers{Managed: true, BootstrapToken: secret, BootstrapTokenExpiresAt: past}
	if h.bootstrapTokenOK("not-the-token") {
		t.Error("a wrong token was accepted past the expiry")
	}

	// And it changes nothing for a self-hosted install, which never reads
	// the token at all.
	selfHosted := &Handlers{Managed: false, BootstrapToken: secret, BootstrapTokenExpiresAt: past}
	if !selfHosted.bootstrapTokenOK("") {
		t.Error("an expiry blocked setup on a self-hosted install")
	}
}
