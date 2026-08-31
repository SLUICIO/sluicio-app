// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The whole promise is that email.html IS the mail, not a lookalike. If
// these two ever diverge the feature is worse than useless: the receiver
// sends something the org never designed, and the preview agreed with it.
func TestWebhookEmailMatchesWhatAnEmailChannelWouldSend(t *testing.T) {
	c := SampleAlertContext()
	content := NotificationContent{Service: true, Integration: true}

	// The same plaintext inputs the preview feeds it, so this compares the
	// two renderings and not two different sets of arguments.
	text := withLink(c.Alert.Summary, c.Org.Product, c.Alert.Link)
	parts := c.emailParts(context.Background(), DeliveryJob{}, content, false, "fallback subject", text)
	if !strings.Contains(parts.HTML, "<") {
		t.Fatalf("email.html is not HTML: %q", parts.HTML)
	}
	if parts.Subject == "fallback subject" {
		t.Fatal("email.subject was not rendered from the template")
	}

	out, err := RenderWebhookPreview(context.Background(),
		`{"content":{"subject":"$email.subject","html_body":"$email.html","text_body":"$email.text"}}`,
		content, c)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("preview is not JSON: %v\n%s", err, out)
	}
	got, _ := doc["content"].(map[string]any)
	if got["subject"] != parts.Subject {
		t.Errorf("webhook subject %q != email subject %q", got["subject"], parts.Subject)
	}
	if got["html_body"] != parts.HTML {
		t.Error("webhook html_body is not the email's HTML body")
	}
	if got["text_body"] != parts.Text {
		t.Error("webhook text_body is not the email's plaintext body")
	}
}

// An HTML mail is full of quotes, newlines and angle brackets. Carrying
// it as a JSON value has to survive that untouched - this is the case a
// text template could never have handled.
func TestEmailHTMLSurvivesAsAJSONValue(t *testing.T) {
	out, err := RenderWebhookPreview(context.Background(),
		`{"html_body":"$email.html"}`, NotificationContent{}, SampleAlertContext())
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("an HTML body broke the JSON: %v\n%s", err, out)
	}
	html, _ := got["html_body"].(string)
	if !strings.Contains(html, `"`) {
		t.Fatal("the sample email has no quotes, so this proves nothing - pick a different fixture")
	}
	if !strings.Contains(html, "<") {
		t.Fatalf("html_body is not HTML: %q", html)
	}
}

// The scan gates a settings-store read per delivery, so it has to be
// right in both directions.
func TestTemplateReferencesEmail(t *testing.T) {
	cases := []struct {
		tmpl string
		want bool
	}{
		{`{"a":"$email.html"}`, true},
		{`{"a":"[${email.subject}] x"}`, true},
		{`{"a":"$alert.summary"}`, false},
		{`{"a":"no refs at all"}`, false},
		// A key named "email" is not a reference - keys are never
		// substituted, so this must not trigger the render.
		{`{"email":"$alert.summary"}`, false},
		// Not a prefix match on some other binding that starts the same.
		{`{"a":"$emailish.thing"}`, false},
	}
	for _, c := range cases {
		if got := TemplateReferencesEmail(c.tmpl); got != c.want {
			t.Errorf("TemplateReferencesEmail(%s) = %v, want %v", c.tmpl, got, c.want)
		}
	}
}

// email.* must be saveable in a webhook body...
func TestWebhookTemplateAcceptsEmailPaths(t *testing.T) {
	if _, err := ValidateWebhookTemplate(`{"content":{"html_body":"$email.html"}}`); err != nil {
		t.Fatalf("email.html rejected in a webhook body: %v", err)
	}
}

// ...and must not be offered to the email or Slack editors, where it
// would be a template asking for its own output.
func TestEmailPathsAreWebhookScoped(t *testing.T) {
	var found int
	for _, v := range TemplateContextSchema() {
		if strings.HasPrefix(v.Path, "email.") {
			found++
			if v.Scope != ScopeWebhook {
				t.Errorf("%s has scope %q, want %q", v.Path, v.Scope, ScopeWebhook)
			}
			if v.Description == "" {
				t.Errorf("%s has no description, so the palette shows a bare path", v.Path)
			}
		}
	}
	if found != len(webhookEmailPaths) {
		t.Fatalf("schema carries %d email paths, want %d", found, len(webhookEmailPaths))
	}
}

// A rule with a legacy Go text/template opted into plaintext. The webhook
// bindings must honour the same choice the mail does, or the two stories
// disagree about what "the email" is.
func TestLegacyPlaintextRuleYieldsNoHTML(t *testing.T) {
	parts := SampleAlertContext().emailParts(context.Background(), DeliveryJob{}, NotificationContent{}, true, "subj", "text")
	if parts.HTML != "" {
		t.Fatalf("legacy plaintext rule produced HTML: %q", parts.HTML)
	}
	if parts.Subject != "subj" || parts.Text != "text" {
		t.Fatalf("legacy rule lost its plaintext: %+v", parts)
	}
}
