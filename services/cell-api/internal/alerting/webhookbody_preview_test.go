// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The preview must run the delivery renderer, not a lookalike: an author who
// trusts a preview from a second implementation finds out it disagreed when an
// alert fails to arrive.
func TestWebhookPreviewRendersTheTemplate(t *testing.T) {
	out, err := RenderWebhookPreview(context.Background(),
		`{"from":{"email":"alerts@example.com"},"subject":"$alert.summary","severity":"$alert.severity"}`,
		NotificationContent{}, SampleAlertContext())
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("preview is not JSON: %v\n%s", err, out)
	}
	if got["severity"] != "critical" {
		t.Fatalf("severity not substituted: %v", got["severity"])
	}
	if s, _ := got["subject"].(string); !strings.Contains(s, "error rate") {
		t.Fatalf("summary not substituted: %v", got["subject"])
	}
}

// An empty template previews the built-in payload, which is what an empty
// template delivers.
func TestWebhookPreviewFallsBackToTheDefaultPayload(t *testing.T) {
	out, err := RenderWebhookPreview(context.Background(), "  ", NotificationContent{}, SampleAlertContext())
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	// The built-in payload is flat (state/summary/rule at the root), not the
	// dotted binding namespace a template addresses.
	if !strings.Contains(out, `"summary"`) || !strings.Contains(out, `"state"`) {
		t.Fatalf("default payload is not the built-in shape:\n%s", out)
	}
}

// A typo in a path must fail loudly in the editor rather than silently
// delivering null.
func TestWebhookPreviewRejectsAnUnknownPath(t *testing.T) {
	if _, err := RenderWebhookPreview(context.Background(), `{"x":"$alert.sumary"}`, NotificationContent{}, SampleAlertContext()); err == nil {
		t.Fatal("expected an error for an unknown path")
	}
}
