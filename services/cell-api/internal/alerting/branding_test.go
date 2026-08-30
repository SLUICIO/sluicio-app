// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package alerting

import (
	"context"
	"strings"
	"testing"
)

// withProductName swaps in a test wordmark resolver and restores it.
func withProductName(t *testing.T, f func(context.Context) string) {
	t.Helper()
	prev := productName
	productName = f
	t.Cleanup(func() { productName = prev })
}

// TestProductNameFallsBackToSluicio pins the three ways a wordmark can be
// absent — unwired, empty, whitespace — all of which must yield the default
// rather than an empty string. Every caller splices the result straight into
// a subject line, so "" would ship a notification headed by a space.
func TestProductNameFallsBackToSluicio(t *testing.T) {
	withProductName(t, nil)
	if got := ProductName(context.Background()); got != DefaultProductName {
		t.Fatalf("unwired resolver: got %q, want %q", got, DefaultProductName)
	}
	for _, wordmark := range []string{"", "   "} {
		withProductName(t, func(context.Context) string { return wordmark })
		if got := ProductName(context.Background()); got != DefaultProductName {
			t.Fatalf("resolver returning %q: got %q, want %q", wordmark, got, DefaultProductName)
		}
	}
	withProductName(t, func(context.Context) string { return "  Maxbo Insight  " })
	if got := ProductName(context.Background()); got != "Maxbo Insight" {
		t.Fatalf("trimmed wordmark: got %q, want %q", got, "Maxbo Insight")
	}
}

// TestBrandedNotificationText walks the notification text a white-label
// partner's own users actually receive: the subject head, the deep-link
// footer that rides on every plaintext email and Slack message, and the
// built-in Liquid email's three "Sluicio"s (subject, CTA button, footer).
// The whole point of the feature is that none of them says Sluicio.
func TestBrandedNotificationText(t *testing.T) {
	const brand = "Maxbo Insight"

	if got := NotifSubject(brand, "prod", "Acme", "[FIRING] boom"); got != brand+" prod - [FIRING] boom - Acme" {
		t.Errorf("NotifSubject: got %q", got)
	}
	if got := NotifSubject("", "", "", "[FIRING] boom"); !strings.HasPrefix(got, DefaultProductName+" ") {
		t.Errorf("NotifSubject with no product: got %q, want the %s default", got, DefaultProductName)
	}
	if got := withLink("body", brand, "https://cell.example/alerts/1"); !strings.Contains(got, "View in "+brand+": ") {
		t.Errorf("withLink: got %q", got)
	}

	c := SampleAlertContext()
	c.Org.Product = brand
	c.Alert.Link = "https://cell.example/alerts/1"
	b := c.bindings(NotificationContent{})

	subject, ok := renderLiquid(DefaultEmailSubject, b)
	if !ok {
		t.Fatal("default email subject did not render")
	}
	if !strings.HasPrefix(subject, brand+" ") {
		t.Errorf("email subject: got %q, want it headed by %q", subject, brand)
	}
	body, ok := renderLiquid(DefaultEmailBody, b)
	if !ok {
		t.Fatal("default email body did not render")
	}
	if !strings.Contains(body, ">View in "+brand+"</a>") {
		t.Error("email body: CTA button is not branded")
	}
	if !strings.Contains(body, brand+" · prod · Acme") {
		t.Error("email body: footer is not branded")
	}
	if strings.Contains(subject, DefaultProductName) || strings.Contains(body, DefaultProductName) {
		t.Errorf("a branded email still says %s", DefaultProductName)
	}
}
