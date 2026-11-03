// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The message has to be well-formed before it leaves us. A missing
// Message-ID is the header receiving systems use to recognise a
// duplicate, so without it the same mail delivered twice arrives as two
// unrelated ones with nothing tying them together — and spam filters
// treat its absence as a signal in its own right.

package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"strings"
	"testing"
	"time"
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

// A body with one non-ASCII character is what a password-reset mail is,
// and "Send test email", being plain ASCII, can't reveal a problem with it.
func TestMessageIsSevenBitOnTheWire(t *testing.T) {
	raw := buildMessage(Config{From: "a@example.com", FromName: "Sluicio Åb"}, []string{"x@y.test"},
		"Återställ lösenord", "Ignore this email — your password is unchanged.\nSecond line.")
	for i, c := range raw {
		if c > 127 {
			t.Fatalf("non-ASCII byte %#x at %d in:\n%s", c, i, raw)
		}
	}
	if got := headerOf(raw, "Content-Transfer-Encoding"); got != "quoted-printable" {
		t.Errorf("Content-Transfer-Encoding = %q", got)
	}
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "this email — your password") || !strings.Contains(string(body), "Second line.") {
		t.Errorf("body does not decode back: %q", body)
	}
	dec := new(mime.WordDecoder)
	if s, err := dec.DecodeHeader(msg.Header.Get("Subject")); err != nil || s != "Återställ lösenord" {
		t.Errorf("Subject decodes to %q (%v)", s, err)
	}
	if from, err := msg.Header.AddressList("From"); err != nil || from[0].Name != "Sluicio Åb" {
		t.Errorf("From decodes to %+v (%v)", from, err)
	}
}

// Port 465 is TLS from the first byte: a server there says nothing until
// the handshake, so a client waiting for its greeting only ever times out.
func TestSendOverImplicitTLS(t *testing.T) {
	cert, pool := selfSigned(t, "127.0.0.1")
	rootCAs = pool
	t.Cleanup(func() { rootCAs = nil })

	// Listening on an arbitrary port, so the test asks sendMail through
	// the port it recognises by dialling a custom address.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{cert}})
		defer conn.Close()
		r := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 test ESMTP\r\n")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case inData && line == ".\r\n":
				inData = false
				got <- data.String()
				fmt.Fprint(conn, "250 ok\r\n")
			case inData:
				data.WriteString(line)
			case strings.HasPrefix(line, "EHLO"):
				fmt.Fprint(conn, "250-test\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(line, "AUTH PLAIN"):
				fmt.Fprint(conn, "235 ok\r\n")
			case strings.HasPrefix(line, "DATA"):
				inData = true
				fmt.Fprint(conn, "354 go\r\n")
			case strings.HasPrefix(line, "QUIT"):
				fmt.Fprint(conn, "221 bye\r\n")
				return
			default:
				fmt.Fprint(conn, "250 ok\r\n")
			}
		}
	}()

	// The dial goes to the listener; the port sendMail reads must say 465.
	realDial := dialContext
	dialContext = func(ctx context.Context, d *net.Dialer, network, _ string) (net.Conn, error) {
		return d.DialContext(ctx, network, ln.Addr().String())
	}
	t.Cleanup(func() { dialContext = realDial })

	cfg := Config{Host: "127.0.0.1", Port: "465", Username: "u", Password: "p", From: "a@example.com"}
	s := NewSender(func(context.Context) (Config, error) { return cfg, nil })
	if err := s.Send(context.Background(), []string{"x@y.test"}, "Subj", "Body"); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case msg := <-got:
		if !strings.Contains(msg, "Subject: Subj") {
			t.Errorf("message: %q", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no message arrived")
	}
}

func selfSigned(t *testing.T, ip string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP(ip)}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(crand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}
