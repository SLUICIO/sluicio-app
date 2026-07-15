// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Notifier correctness — one test block per channel kind, pinning the
// exact wire format each destination receives, plus the registry↔kind
// parity that guarantees no channel can be created without a notifier
// able to deliver it.
package alerting

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// capture spins an httptest server that records the last JSON body.
func capture(t *testing.T, status int) (*httptest.Server, *map[string]any) {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("sink: bad JSON body: %v", err)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestRegistryCoversEveryValidKind(t *testing.T) {
	for _, kind := range []string{ChannelWebhook, ChannelSlack, ChannelPagerDuty, ChannelEmail} {
		if !ValidChannelKind(kind) {
			t.Fatalf("kind %q not valid but expected", kind)
		}
		if _, ok := notifierFor(kind); !ok {
			t.Fatalf("kind %q is creatable (ValidChannelKind) but has no registered notifier — deliveries would all fail", kind)
		}
	}
}

func TestWebhookNotifier(t *testing.T) {
	srv, got := capture(t, http.StatusOK)
	msg := Message{
		State:   "firing",
		Subject: "[FIRING] cpu high",
		Body:    "cpu high on api",
		Labels:  map[string]string{"severity": "critical"},
		Config:  map[string]string{"url": srv.URL},
	}
	if err := (webhookNotifier{}).Send(context.Background(), srv.Client(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	for _, key := range []string{"state", "subject", "summary", "labels", "sent_at", "source"} {
		if _, ok := (*got)[key]; !ok {
			t.Fatalf("legacy webhook payload missing %q: %v", key, *got)
		}
	}
	if (*got)["source"] != "sluicio" || (*got)["state"] != "firing" {
		t.Fatalf("payload identity fields wrong: %v", *got)
	}

	// A structured Payload (content-toggled rules) is sent verbatim.
	msg.Payload = map[string]any{"custom": true}
	if err := (webhookNotifier{}).Send(context.Background(), srv.Client(), msg); err != nil {
		t.Fatalf("send payload: %v", err)
	}
	if (*got)["custom"] != true {
		t.Fatalf("structured payload not sent verbatim: %v", *got)
	}

	// Non-2xx must propagate as an error (the worker retries on it).
	bad, _ := capture(t, http.StatusBadGateway)
	msg.Config["url"] = bad.URL
	if err := (webhookNotifier{}).Send(context.Background(), bad.Client(), msg); err == nil {
		t.Fatal("expected error on 502, got nil")
	}
	// Missing URL is a config error, not a panic.
	msg.Config = map[string]string{}
	if err := (webhookNotifier{}).Send(context.Background(), srv.Client(), msg); err == nil {
		t.Fatal("expected error on empty url")
	}
}

func TestWebhookSigning(t *testing.T) {
	// The scheme is a public contract with receivers: HMAC-SHA256 over
	// "<X-Sluicio-Timestamp>.<raw body>", sent as
	// "X-Sluicio-Signature: sha256=<hex>". Changing ANY part of this
	// breaks every receiver's verification — hence the exact pin.
	var gotBody []byte
	var gotSig, gotTS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get(SignatureHeader)
		gotTS = r.Header.Get(TimestampHeader)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	msg := Message{
		State:  "firing",
		Body:   "signed alert",
		Config: map[string]string{"url": srv.URL, "secret": "s3cret"},
	}
	if err := (webhookNotifier{}).Send(context.Background(), srv.Client(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotTS == "" || gotSig == "" {
		t.Fatalf("signature headers missing: sig=%q ts=%q", gotSig, gotTS)
	}
	// Recompute exactly the way a receiver would.
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(gotTS + "."))
	mac.Write(gotBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Fatalf("signature mismatch:\n got %s\nwant %s", gotSig, want)
	}
	// Timestamp is unix seconds, recent.
	ts, err := strconv.ParseInt(gotTS, 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)) > time.Minute {
		t.Fatalf("timestamp not recent unix seconds: %q (%v)", gotTS, err)
	}

	// No secret → no signature headers (today's behaviour untouched).
	gotSig, gotTS = "", ""
	msg.Config = map[string]string{"url": srv.URL}
	if err := (webhookNotifier{}).Send(context.Background(), srv.Client(), msg); err != nil {
		t.Fatalf("send unsigned: %v", err)
	}
	if gotSig != "" || gotTS != "" {
		t.Fatalf("unsigned send must carry no signature headers, got sig=%q ts=%q", gotSig, gotTS)
	}
}

func TestWebhookCloudEventsFormat(t *testing.T) {
	// config.format="cloudevents" wraps the payload in a CE 1.0
	// structured-mode envelope — a contract with CE routers (Event
	// Grid, EventBridge, Knative): pin the required attributes, the
	// type vocabulary, and the content type.
	var gotBody []byte
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Payload-less path (error notifier / broadcasts): Send wraps.
	msg := Message{
		State:  "firing",
		Body:   "errors detected on api",
		Config: map[string]string{"url": srv.URL, "format": "cloudevents"},
	}
	if err := (webhookNotifier{}).Send(context.Background(), srv.Client(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotCT != CloudEventsContentType {
		t.Fatalf("content type = %q, want %q", gotCT, CloudEventsContentType)
	}
	var ev map[string]any
	if err := json.Unmarshal(gotBody, &ev); err != nil {
		t.Fatalf("bad envelope JSON: %v", err)
	}
	if ev["specversion"] != "1.0" || ev["type"] != "com.sluicio.alert.fired" || ev["datacontenttype"] != "application/json" {
		t.Fatalf("envelope attributes wrong: %v", ev)
	}
	if ev["id"] == "" || ev["source"] == "" || ev["time"] == "" {
		t.Fatalf("required CE attributes missing: %v", ev)
	}
	data, _ := ev["data"].(map[string]any)
	if data["summary"] != "errors detected on api" {
		t.Fatalf("data payload not preserved: %v", data)
	}

	// Pre-wrapped alert-path payloads (specversion present) pass through
	// unchanged — no double envelope.
	msg.Payload = cloudEventEnvelope("inst-1.firing", "firing", "rule-9", "2026-07-15T12:00:00Z", map[string]any{"summary": "s"})
	if err := (webhookNotifier{}).Send(context.Background(), srv.Client(), msg); err != nil {
		t.Fatalf("send wrapped: %v", err)
	}
	if err := json.Unmarshal(gotBody, &ev); err != nil {
		t.Fatalf("bad wrapped JSON: %v", err)
	}
	if ev["id"] != "inst-1.firing" || ev["subject"] != "rule-9" {
		t.Fatalf("pre-wrapped envelope mangled: %v", ev)
	}
	if _, nested := ev["data"].(map[string]any)["specversion"]; nested {
		t.Fatalf("double-enveloped: %v", ev)
	}

	// Resolved state maps to the .resolved type.
	if e := cloudEventEnvelope("x", "resolved", "", "t", nil); e["type"] != "com.sluicio.alert.resolved" {
		t.Fatalf("resolved type = %v", e["type"])
	}

	// Default format channels are untouched: plain JSON, no envelope.
	msg = Message{State: "firing", Body: "plain", Config: map[string]string{"url": srv.URL}}
	if err := (webhookNotifier{}).Send(context.Background(), srv.Client(), msg); err != nil {
		t.Fatalf("send plain: %v", err)
	}
	if gotCT != "application/json" {
		t.Fatalf("default content type = %q", gotCT)
	}
	ev = nil // Unmarshal into a used map MERGES keys — start clean.
	_ = json.Unmarshal(gotBody, &ev)
	if _, hasSpec := ev["specversion"]; hasSpec {
		t.Fatalf("default channel must not be enveloped: %v", ev)
	}
}

func TestSlackNotifier(t *testing.T) {
	srv, got := capture(t, http.StatusOK)
	send := func(state string, sev Severity) string {
		t.Helper()
		msg := Message{State: state, Severity: sev, Body: "queue depth 900 > 500", Config: map[string]string{"url": srv.URL}}
		if err := (slackNotifier{}).Send(context.Background(), srv.Client(), msg); err != nil {
			t.Fatalf("send: %v", err)
		}
		text, _ := (*got)["text"].(string)
		return text
	}
	if text := send("firing", SeverityCritical); !strings.Contains(text, ":red_circle:") || !strings.Contains(text, "*[FIRING]*") {
		t.Fatalf("critical firing text = %q", text)
	}
	if text := send("firing", SeverityWarning); !strings.Contains(text, ":large_yellow_circle:") {
		t.Fatalf("warning firing text = %q", text)
	}
	if text := send("resolved", SeverityCritical); !strings.Contains(text, ":large_green_circle:") || !strings.Contains(text, "*[RESOLVED]*") {
		t.Fatalf("resolved text = %q", text)
	}
}

func TestPagerDutyNotifier(t *testing.T) {
	srv, got := capture(t, http.StatusAccepted) // PD returns 202
	base := Message{
		State:    "firing",
		Severity: SeverityCritical,
		Body:     "disk full on db-1",
		Labels:   map[string]string{"rule_id": "rule-123", "metric": "disk.used"},
		Config:   map[string]string{"routing_key": "R0KEY", "events_url": srv.URL},
	}
	if err := (pagerdutyNotifier{}).Send(context.Background(), srv.Client(), base); err != nil {
		t.Fatalf("send: %v", err)
	}
	if (*got)["routing_key"] != "R0KEY" || (*got)["event_action"] != "trigger" || (*got)["dedup_key"] != "rule-123" {
		t.Fatalf("trigger event wrong: %v", *got)
	}
	payload, _ := (*got)["payload"].(map[string]any)
	if payload["severity"] != "critical" || payload["summary"] != "disk full on db-1" || payload["source"] != "sluicio" {
		t.Fatalf("payload wrong: %v", payload)
	}

	// resolved → resolve action, same dedup key (that's what closes the PD incident).
	res := base
	res.State = "resolved"
	if err := (pagerdutyNotifier{}).Send(context.Background(), srv.Client(), res); err != nil {
		t.Fatalf("send resolve: %v", err)
	}
	if (*got)["event_action"] != "resolve" || (*got)["dedup_key"] != "rule-123" {
		t.Fatalf("resolve event wrong: %v", *got)
	}

	// Severity mapping: info → info, everything unnamed → warning.
	info := base
	info.Severity = SeverityInfo
	_ = (pagerdutyNotifier{}).Send(context.Background(), srv.Client(), info)
	if p, _ := (*got)["payload"].(map[string]any); p["severity"] != "info" {
		t.Fatalf("info severity mapped to %v", p["severity"])
	}

	// Body-fallback dedup keys are capped at PD's 255-char limit.
	long := base
	long.Labels = map[string]string{}
	long.Body = strings.Repeat("x", 400)
	_ = (pagerdutyNotifier{}).Send(context.Background(), srv.Client(), long)
	if k, _ := (*got)["dedup_key"].(string); len(k) != 255 {
		t.Fatalf("dedup_key length = %d, want 255", len(k))
	}

	// Without events_url the notifier must target the real PD endpoint —
	// pin the constant so a refactor can't silently break production.
	if pagerdutyEventsURL != "https://events.pagerduty.com/v2/enqueue" {
		t.Fatalf("default events URL changed: %s", pagerdutyEventsURL)
	}
}

func TestEmailMessageHeaderSafety(t *testing.T) {
	// A rule name (→ subject) carrying CRLF must not inject headers.
	evil := "alert\r\nBcc: attacker@example.com\r\nX-Evil: 1"
	// Injection = the payload becoming its own header LINE. After
	// sanitization it may legitimately appear INLINE in the subject —
	// so assert per line, not per substring.
	noInjectedLines := func(raw string) {
		t.Helper()
		headers := strings.SplitN(raw, "\r\n\r\n", 2)[0]
		for _, line := range strings.Split(headers, "\r\n") {
			if strings.HasPrefix(line, "Bcc:") || strings.HasPrefix(line, "X-Evil:") {
				t.Fatalf("header injection survived:\n%s", headers)
			}
		}
	}
	raw := string(emailMessage("from@x.se", []string{"to@x.se"}, evil, "body"))
	noInjectedLines(raw)
	if !strings.Contains(raw, "Subject: alert  Bcc: attacker@example.com  X-Evil: 1") {
		t.Fatalf("subject not flattened onto one line:\n%s", raw)
	}

	multi := string(emailMessageMultipart("from@x.se", []string{"to@x.se"}, evil, "text", "<b>html</b>"))
	noInjectedLines(multi)
	if !strings.Contains(multi, "text/plain") || !strings.Contains(multi, "text/html") {
		t.Fatalf("multipart structure missing parts:\n%s", multi)
	}
}

func TestSubjectAndRecipientHelpers(t *testing.T) {
	if got := firstLine("a\rb"); got != "a" {
		t.Fatalf("firstLine must cut at CR too, got %q", got)
	}
	if got := firstLine("a\nb"); got != "a" {
		t.Fatalf("firstLine LF: %q", got)
	}
	if got := stateSubject("firing", "cpu\nmore"); got != "[FIRING] cpu" {
		t.Fatalf("stateSubject = %q", got)
	}
	if got := splitRecipients(" a@x.se , ,b@x.se,"); len(got) != 2 || got[0] != "a@x.se" || got[1] != "b@x.se" {
		t.Fatalf("splitRecipients = %v", got)
	}
}
