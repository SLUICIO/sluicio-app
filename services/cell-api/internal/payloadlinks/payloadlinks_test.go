// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package payloadlinks

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	full := Attribute{Attribute: "io.payload.url"}
	archive := Attribute{Attribute: "archive.path", BaseURL: "https://archive.example.com/files"}
	archiveSlash := Attribute{Attribute: "archive.path", BaseURL: "https://archive.example.com/files/"}
	byID := Attribute{Attribute: "doc.id", BaseURL: "https://dms.example.com/open?id="}
	bareQuery := Attribute{Attribute: "doc.id", BaseURL: "https://dms.example.com/open?"}
	for _, c := range []struct {
		name  string
		attr  Attribute
		value string
		want  string
	}{
		{"a full address is the link", full, "https://blob.example.com/in/INV_42.pdf", "https://blob.example.com/in/INV_42.pdf"},
		{"plain http works too", full, "http://archive.local/INV_42.pdf", "http://archive.local/INV_42.pdf"},
		{"a file name is not a link", full, "INV_42.pdf", ""},
		{"javascript stays text", full, "javascript:alert(1)", ""},
		{"file stays text", full, "file:///etc/passwd", ""},
		{"data stays text", full, "data:text/html,<b>x</b>", ""},
		{"one slash between base and value", archive, "2026/INV_42.pdf", "https://archive.example.com/files/2026/INV_42.pdf"},
		{"slashes on both sides collapse", archiveSlash, "/2026/INV_42.pdf", "https://archive.example.com/files/2026/INV_42.pdf"},
		{"spaces and # are encoded", archive, "in box/INV #42.pdf", "https://archive.example.com/files/in%20box/INV%20%2342.pdf"},
		{"non-ASCII is encoded", archive, "lön/maj.txt", "https://archive.example.com/files/l%C3%B6n/maj.txt"},
		{"a value cannot climb out of the base", archive, "../../secrets", ""},
		{"a value cannot change the host", archive, "//evil.example.com/x", "https://archive.example.com/files/evil.example.com/x"},
		{"a base ending in = takes the value", byID, "INV 42/a", "https://dms.example.com/open?id=INV+42%2Fa"},
		{"a base ending in ? takes the value", bareQuery, "INV_42", "https://dms.example.com/open?INV_42"},
		{"an empty value is nothing", archive, "  ", ""},
		{"a bad base makes nothing", Attribute{Attribute: "x", BaseURL: "javascript:alert(1)//"}, "a", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Resolve(c.attr, c.value)
			if ok != (c.want != "") || got != c.want {
				t.Fatalf("Resolve(%q, %q) = %q, %v; want %q", c.attr.BaseURL, c.value, got, ok, c.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	got, err := Normalize([]Attribute{{Attribute: " io.payload.url ", Label: " Payload "}, {Attribute: "archive.path", BaseURL: "https://archive.example.com/files"}})
	if err != nil || got[0].Attribute != "io.payload.url" || got[0].Label != "Payload" {
		t.Fatalf("got %+v, %v", got, err)
	}
	for name, bad := range map[string][]Attribute{
		"no attribute":    {{Label: "x"}},
		"twice":           {{Attribute: "a"}, {Attribute: "a"}},
		"space in key":    {{Attribute: "a b"}},
		"ftp base":        {{Attribute: "a", BaseURL: "ftp://archive.example.com"}},
		"no host":         {{Attribute: "a", BaseURL: "https:///files"}},
		"fragment":        {{Attribute: "a", BaseURL: "https://archive.example.com/#/files"}},
		"credentials":     {{Attribute: "a", BaseURL: "https://user:pw@archive.example.com/"}},
		"javascript base": {{Attribute: "a", BaseURL: "javascript:alert(1)"}},
		"long label":      {{Attribute: "a", Label: strings.Repeat("x", 61)}},
	} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("%s: accepted %+v", name, bad)
		}
	}
	many := make([]Attribute, MaxAttributes+1)
	for i := range many {
		many[i] = Attribute{Attribute: fmt.Sprintf("a%d", i)}
	}
	if _, err := Normalize(many); err == nil {
		t.Error("accepted more than the maximum")
	}
	if got, err := Normalize(nil); err != nil || len(got) != 0 {
		t.Errorf("an empty list is allowed: %v, %v", got, err)
	}
}

// A file transfer records the file as picked up and as delivered; a retry
// records the same file twice. The earliest link comes first, identical
// addresses are one link with every span that carried them.
func TestCollect(t *testing.T) {
	attrs := []Attribute{{Attribute: "io.payload.url"}, {Attribute: "archive.path", BaseURL: "https://archive.example.com/files"}}
	t0 := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	in := "https://archive.example.com/in/INV_42.pdf"
	out := "https://archive.example.com/out/INV_42.pdf"
	spans := []Span{
		{TraceID: "t1", SpanID: "deliver", ServiceName: "mft", SpanName: "deliver", Role: "output", Time: t0.Add(2 * time.Second), Values: map[string]string{"io.payload.url": out}},
		{TraceID: "t1", SpanID: "pickup", ServiceName: "mft", SpanName: "pickup", Role: "input", Time: t0, Values: map[string]string{"io.payload.url": in}},
		{TraceID: "t1", SpanID: "retry", ServiceName: "mft", SpanName: "deliver", Role: "output", Time: t0.Add(3 * time.Second), Values: map[string]string{"io.payload.url": out}},
		{TraceID: "t1", SpanID: "archive", ServiceName: "arc", SpanName: "store", Time: t0.Add(4 * time.Second), Values: map[string]string{"archive.path": "2026/INV_42.pdf"}},
		{TraceID: "t2", SpanID: "x", ServiceName: "mft", SpanName: "pickup", Time: t0, Values: map[string]string{"io.payload.url": "INV_9.pdf"}},
	}
	got := Collect(attrs, spans)
	p := got["t1"]["io.payload.url"]
	if len(p.Links) != 2 || p.Links[0].URL != in || p.Links[1].URL != out {
		t.Fatalf("t1 payload links: %+v", p)
	}
	if p.Links[0].Spans[0].Role != "input" || len(p.Links[1].Spans) != 2 || p.Links[1].Spans[1].SpanID != "retry" {
		t.Fatalf("carriers: %+v", p.Links)
	}
	if a := got["t1"]["archive.path"]; len(a.Links) != 1 || a.Links[0].URL != "https://archive.example.com/files/2026/INV_42.pdf" {
		t.Fatalf("archive link: %+v", a)
	}
	if v := got["t2"]["io.payload.url"]; len(v.Links) != 1 || v.Links[0].URL != "" || v.Links[0].Value != "INV_9.pdf" {
		t.Fatalf("a value that is not an address stays text: %+v", v)
	}
}

func TestCollectIsBounded(t *testing.T) {
	attrs := []Attribute{{Attribute: "io.payload.url"}}
	t0 := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	var spans []Span
	for i := 0; i < MaxLinksPerMessage+5; i++ {
		spans = append(spans, Span{TraceID: "t", SpanID: fmt.Sprint(i), Time: t0.Add(time.Duration(i) * time.Second),
			Values: map[string]string{"io.payload.url": fmt.Sprintf("https://a.example.com/%d", i)}})
	}
	// The same overflowing address again is not counted twice.
	spans = append(spans, Span{TraceID: "t", SpanID: "again", Time: t0.Add(time.Hour), Values: map[string]string{"io.payload.url": "https://a.example.com/24"}})
	got := Collect(attrs, spans)["t"]["io.payload.url"]
	if len(got.Links) != MaxLinksPerMessage || got.More != 5 {
		t.Fatalf("%d links, %d more", len(got.Links), got.More)
	}
}

func TestForSpan(t *testing.T) {
	attrs := []Attribute{{Attribute: "io.payload.url", Label: "Payload"}, {Attribute: "archive.path", BaseURL: "https://archive.example.com/files"}}
	got := ForSpan(attrs, map[string]string{"archive.path": "a.pdf", "io.payload.url": "https://x.example.com/a.pdf", "other": "y"})
	if len(got) != 2 || got[0].Label != "Payload" || got[1].Label != "archive.path" || got[1].URL != "https://archive.example.com/files/a.pdf" {
		t.Fatalf("%+v", got)
	}
}
