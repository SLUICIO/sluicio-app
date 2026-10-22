// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"strings"
	"testing"
)

// The built-in email names what failed when the scope recorded an
// exception (#42), and escapes it: an exception message is whatever the
// monitored application put there, so it must not become markup.
func TestDefaultEmailNamesTheException(t *testing.T) {
	c := SampleAlertContext()
	c.Exception = &ExceptionFacts{Type: "System.InvalidOperationException", Message: `<b>bad</b> & "worse"`, Service: "checkout-api"}
	_, body := RenderForPreview(context.Background(), ChannelEmail, NotificationContent{}, c)
	if !strings.Contains(body, "Latest exception") || !strings.Contains(body, "System.InvalidOperationException") {
		t.Fatalf("exception block missing:\n%s", body)
	}
	if strings.Contains(body, "<b>bad</b>") {
		t.Error("exception message rendered as markup")
	}
	if !strings.Contains(body, "&lt;b&gt;bad&lt;/b&gt;") {
		t.Error("exception message not escaped")
	}

	c.Exception = nil
	_, body = RenderForPreview(context.Background(), ChannelEmail, NotificationContent{}, c)
	if strings.Contains(body, "Latest exception") {
		t.Error("exception block rendered without an exception")
	}
}

// The webhook payload carries the exception when there is one, and no
// key at all when there is not.
func TestWebhookPayloadCarriesTheException(t *testing.T) {
	c := SampleAlertContext()
	p := c.webhookPayload(NotificationContent{})
	ex, ok := p["exception"].(*ExceptionFacts)
	if !ok || ex.Type == "" {
		t.Fatalf("exception missing from payload: %#v", p["exception"])
	}
	c.Exception = nil
	if _, present := c.webhookPayload(NotificationContent{})["exception"]; present {
		t.Error("exception key present without an exception")
	}
}
