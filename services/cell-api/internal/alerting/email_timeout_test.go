// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// An SMTP server that accepts the connection and then says nothing must
// not hold alert delivery open.
//
// This is the failure the alert email path used to have and the
// transactional path did not: net/smtp.SendMail has no timeout of any
// kind, so a mail host in that state kept the call - and the delivery
// worker waiting on it - for ever. One unreachable mail server stalled
// every channel behind it, including the webhooks that had nothing to do
// with email.
//
// The test asserts the bound, not the wording: it fails if the send ever
// returns "eventually" rather than inside the deadline it was given.

package alerting

import (
	"context"
	"net"
	"testing"
	"time"
)

// silentSMTP accepts connections and never writes the 220 greeting, which
// is what a wedged mail server looks like from the client side. Returns
// its address; closed when the test ends.
func silentSMTP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold it open, say nothing, and keep a reference so the
			// connection is not closed by a GC cycle mid-test.
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	return ln.Addr().String()
}

func TestEmailSendGivesUpOnASilentServer(t *testing.T) {
	host, port, err := net.SplitHostPort(silentSMTP(t))
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	// A deadline far below the transport's own 20s, so the test proves the
	// context is honoured without waiting on the constant.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- emailNotifier{}.Send(ctx, nil, Message{
			Subject: "probe",
			Body:    "probe",
			Config: map[string]string{
				"smtp_host": host,
				"smtp_port": port,
				"from":      "alerts@example.test",
				"to":        "on-call@example.test",
			},
		})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a server that never answered reported a successful send")
		}
		// Generous headroom over the 2s deadline: the point is that it
		// returns at all, near the deadline rather than never.
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Errorf("send took %s to give up; the deadline was 2s", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("send never returned: a hanging SMTP server still stalls alert delivery")
	}
}

func TestEmailSendStillRefusesAChannelWithNoHost(t *testing.T) {
	// The config checks must keep happening before anything is dialled,
	// so a misconfigured channel fails immediately rather than after a
	// timeout against nothing.
	err := emailNotifier{}.Send(context.Background(), nil, Message{
		Config: map[string]string{"to": "on-call@example.test"},
	})
	if err == nil {
		t.Fatal("a channel with no SMTP host reported a successful send")
	}
}
