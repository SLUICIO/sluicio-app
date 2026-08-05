// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The message has to be well-formed before it leaves us. A missing
// Message-ID is the header receiving systems use to recognise a
// duplicate, so without it the same mail delivered twice arrives as two
// unrelated ones with nothing tying them together — and spam filters
// treat its absence as a signal in its own right.

package mail

import (
	"strings"
	"testing"
)

func headerOf(raw []byte, name string) string {
	for _, line := range strings.Split(string(raw), "\r\n") {
		if line == "" {
			break // end of headers
		}
		if strings.HasPrefix(strings.ToLower(line), strings.ToLower(name)+":") {
			return strings.TrimSpace(line[len(name)+1:])
		}
	}
	return ""
}

func TestMessageCarriesAMessageID(t *testing.T) {
	raw := buildMessage(Config{From: "alerts@example.com"}, []string{"a@b.test"}, "s", "b")
	id := headerOf(raw, "Message-ID")
	if id == "" {
		t.Fatal("no Message-ID header")
	}
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, ">") {
		t.Errorf("Message-ID must be angle-bracketed, got %q", id)
	}
	if !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("Message-ID should use the From domain, got %q", id)
	}
}

func TestMessageIDsAreUnique(t *testing.T) {
	// A constant id would be worse than none: receivers would treat two
	// genuinely different mails as the same one and drop the second.
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := headerOf(buildMessage(Config{From: "a@example.com"}, []string{"x@y.test"}, "s", "b"), "Message-ID")
		if seen[id] {
			t.Fatalf("duplicate Message-ID after %d messages: %s", i, id)
		}
		seen[id] = true
	}
}

func TestMessageIDStaysWellFormedWithoutAFromDomain(t *testing.T) {
	// A malformed From must not produce a malformed header.
	id := headerOf(buildMessage(Config{From: "not-an-address"}, []string{"x@y.test"}, "s", "b"), "Message-ID")
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, ">") || strings.Count(id, "@") != 1 {
		t.Errorf("got %q", id)
	}
}

func TestMessageKeepsTheHeadersItAlreadyHad(t *testing.T) {
	raw := buildMessage(Config{From: "a@example.com", FromName: "Sluicio"}, []string{"x@y.test"}, "Subj", "Body")
	for name, want := range map[string]string{
		"From":    "Sluicio <a@example.com>",
		"To":      "x@y.test",
		"Subject": "Subj",
	} {
		if got := headerOf(raw, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if !strings.HasSuffix(string(raw), "Body\r\n") {
		t.Error("body missing or not terminated")
	}
}
