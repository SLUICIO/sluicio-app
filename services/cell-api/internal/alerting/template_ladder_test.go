// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package alerting

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// withResolvers swaps in test ladder/cell resolvers and restores them.
func withResolvers(t *testing.T, ladder func(context.Context, uuid.UUID, *uuid.UUID) MessageTemplates, cell func(context.Context) (string, string)) {
	t.Helper()
	prevLadder, prevCell := messageTemplateResolver, defaultEmailTemplateResolver
	messageTemplateResolver, defaultEmailTemplateResolver = ladder, cell
	t.Cleanup(func() { messageTemplateResolver, defaultEmailTemplateResolver = prevLadder, prevCell })
}

// TestTemplateLadderPerField pins the per-field fallthrough: rule inline →
// team/org stored set → cell email setting → built-in constants, each field
// independently.
func TestTemplateLadderPerField(t *testing.T) {
	gid := uuid.New()
	job := DeliveryJob{RuleGroupID: &gid}
	job.Channel.OrganizationID = uuid.New()

	withResolvers(t,
		func(_ context.Context, org uuid.UUID, group *uuid.UUID) MessageTemplates {
			if org != job.Channel.OrganizationID || group == nil || *group != gid {
				t.Fatalf("resolver called with wrong scope: %v %v", org, group)
			}
			// The stored ladder sets only the email body + slack body.
			return MessageTemplates{EmailBody: "ladder-body", SlackBody: "ladder-slack"}
		},
		func(context.Context) (string, string) { return "cell-subject", "cell-body" },
	)

	// No inline overrides: subject falls to the cell setting, body to the
	// stored ladder; slack body from the ladder, title stays empty.
	sub, body := effectiveEmailTemplate(context.Background(), job, NotificationContent{})
	if sub != "cell-subject" || body != "ladder-body" {
		t.Fatalf("per-field merge wrong: %q / %q", sub, body)
	}
	title, sbody := effectiveSlackTemplate(context.Background(), job, NotificationContent{})
	if title != "" || sbody != "ladder-slack" {
		t.Fatalf("slack merge wrong: %q / %q", title, sbody)
	}

	// Inline wins per field: only the slack title inline — body still ladder.
	title, sbody = effectiveSlackTemplate(context.Background(), job, NotificationContent{SlackTitle: "inline-title"})
	if title != "inline-title" || sbody != "ladder-slack" {
		t.Fatalf("inline slack title should win: %q / %q", title, sbody)
	}

	// Nothing anywhere: email falls to built-ins, slack to empty (the
	// notifier's built-in line).
	withResolvers(t, nil, nil)
	sub, body = effectiveEmailTemplate(context.Background(), job, NotificationContent{})
	if sub != DefaultEmailSubject || body != DefaultEmailBody {
		t.Fatalf("built-in fallthrough broken")
	}
	title, sbody = effectiveSlackTemplate(context.Background(), job, NotificationContent{})
	if title != "" || sbody != "" {
		t.Fatalf("unconfigured slack must resolve empty (built-in line), got %q/%q", title, sbody)
	}
}

// TestSlackRenderFailureFallsThrough: a template that fails to render never
// blocks the alert — the message falls back to the built-in Slack line.
func TestSlackRenderFailureFallsThrough(t *testing.T) {
	gid := uuid.New()
	job := DeliveryJob{State: "firing", Summary: "it broke", RuleGroupID: &gid}
	job.Channel.Kind = ChannelSlack
	job.Channel.OrganizationID = uuid.New()
	job.Labels = map[string]string{"severity": "critical"}

	withResolvers(t, func(context.Context, uuid.UUID, *uuid.UUID) MessageTemplates {
		return MessageTemplates{SlackBody: "{% if alert.state %}unclosed"}
	}, nil)

	msg := messageFromJob(context.Background(), job, "prod", "Acme")
	if msg.SlackText != "" {
		t.Fatalf("broken template must fall through to the built-in line, got %q", msg.SlackText)
	}

	// And a healthy template renders with the title bolded on top.
	withResolvers(t, func(context.Context, uuid.UUID, *uuid.UUID) MessageTemplates {
		return MessageTemplates{SlackTitle: "{{ alert.state_emoji }} {{ rule.name }}", SlackBody: "{{ alert.summary }}"}
	}, nil)
	job.Labels["rule_name"] = "Checkout errors"
	msg = messageFromJob(context.Background(), job, "prod", "Acme")
	if !strings.HasPrefix(msg.SlackText, "*:red_circle: Checkout errors*\n") || !strings.Contains(msg.SlackText, "it broke") {
		t.Fatalf("rendered slack text wrong: %q", msg.SlackText)
	}
}

// TestTemplateSchemaComplete forces every reflected variable to carry a
// description — a new AlertContext field without a docs-table entry fails
// here, keeping the UI palette complete by construction.
func TestTemplateSchemaComplete(t *testing.T) {
	vars := TemplateContextSchema()
	if len(vars) == 0 {
		t.Fatal("schema is empty")
	}
	seen := map[string]bool{}
	for _, v := range vars {
		seen[v.Path] = true
		if v.Description == "" || v.Available == "" {
			t.Errorf("variable %q lacks a docs entry (add it to templateVariableDocs)", v.Path)
		}
	}
	// And the reverse: no orphaned docs for variables that no longer exist.
	for path := range templateVariableDocs {
		if !seen[path] {
			t.Errorf("templateVariableDocs has %q but AlertContext doesn't produce it", path)
		}
	}
	// Spot-pin the public contract (additive only, no renames).
	for _, must := range []string{"alert.severity", "alert.state_emoji", "check.value", "service.metadata.<key>", "sent_at"} {
		if !seen[must] {
			t.Errorf("public variable %q missing from schema", must)
		}
	}
}
