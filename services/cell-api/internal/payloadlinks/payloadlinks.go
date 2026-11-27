// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package payloadlinks turns span attributes into links to a message's
// payload: the file that was picked up, the invoice that was posted.
// Sluicio never stores or fetches a payload. Content is often personal or
// commercially sensitive, it is large, and it already has a home with its
// own access control, so Sluicio shows the address and the archive decides
// who may open it.
//
// An organization lists its link attributes. Each names the attribute and,
// optionally, a base URL the value is appended to, for estates that record
// a path or an id within a known archive rather than a full address.
package payloadlinks

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// DefaultAttribute is in an organization's list until an admin removes
// it: a span that carries a full address gets a link without any setup.
// It extends the documented io.* family; OpenTelemetry has no convention
// for where a payload is kept.
const DefaultAttribute = "io.payload.url"

// MaxAttributes bounds the list. Each one is a column in every message
// list, and more than a handful would crowd out the columns people chose.
const MaxAttributes = 10

// MaxLinksPerMessage bounds the links listed per attribute per message,
// so a message that touches thousands of files does not slow the list.
const MaxLinksPerMessage = 20

// Attribute is one configured link attribute.
type Attribute struct {
	// Attribute is the span attribute key, e.g. io.payload.url.
	Attribute string `json:"attribute"`
	// Label is the column header; empty shows the attribute key.
	Label string `json:"label,omitempty"`
	// BaseURL, when set, is the prefix the value is appended to. Empty
	// means the value must itself be an address.
	BaseURL string `json:"base_url,omitempty"`
}

// Defaults is the list an organization starts with.
func Defaults() []Attribute {
	return []Attribute{{Attribute: DefaultAttribute, Label: "Payload"}}
}

// Keys returns the attribute keys, in order.
func Keys(attrs []Attribute) []string {
	out := make([]string, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, a.Attribute)
	}
	return out
}

// Normalize trims every field and validates the list as a whole.
func Normalize(attrs []Attribute) ([]Attribute, error) {
	if len(attrs) > MaxAttributes {
		return nil, fmt.Errorf("at most %d link attributes", MaxAttributes)
	}
	out := make([]Attribute, 0, len(attrs))
	seen := map[string]bool{}
	for _, a := range attrs {
		a.Attribute = strings.TrimSpace(a.Attribute)
		a.Label = strings.TrimSpace(a.Label)
		a.BaseURL = strings.TrimSpace(a.BaseURL)
		if a.Attribute == "" {
			return nil, fmt.Errorf("every link needs an attribute")
		}
		if len(a.Attribute) > 200 || strings.ContainsAny(a.Attribute, " \t\r\n") {
			return nil, fmt.Errorf("%q is not an attribute key", a.Attribute)
		}
		if seen[a.Attribute] {
			return nil, fmt.Errorf("%s is listed twice", a.Attribute)
		}
		seen[a.Attribute] = true
		if len(a.Label) > 60 {
			return nil, fmt.Errorf("the label of %s is longer than 60 characters", a.Attribute)
		}
		if a.BaseURL != "" {
			if err := validateBase(a.BaseURL); err != nil {
				return nil, fmt.Errorf("base URL of %s: %w", a.Attribute, err)
			}
		}
		out = append(out, a)
	}
	return out, nil
}

func validateBase(base string) error {
	u, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("not a URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("must start with https:// or http://")
	}
	if u.Host == "" {
		return fmt.Errorf("has no host")
	}
	if u.Fragment != "" || strings.Contains(base, "#") {
		return fmt.Errorf("may not contain #")
	}
	if u.User != nil {
		return fmt.Errorf("may not carry a user name or password")
	}
	return nil
}

// Resolve builds the address a value points at. ok is false when the
// value does not make a link: without a base URL it must itself be an
// http or https address, so javascript:, file: and data: values, or a bare
// file name, stay plain text.
//
// With a base URL the value is appended with exactly one "/" between them,
// whatever slashes either side carries, unless the base ends in "=" or "?",
// where the value is a query value and goes on as it is. Characters not
// allowed in a URL are percent-encoded; "/" is kept so a value can be a
// path. The value can never change the host.
func Resolve(a Attribute, value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if a.BaseURL == "" {
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return "", false
		}
		return value, true
	}
	if validateBase(a.BaseURL) != nil {
		return "", false
	}
	base := a.BaseURL
	if strings.HasSuffix(base, "=") || strings.HasSuffix(base, "?") {
		return base + url.QueryEscape(value), true
	}
	out := strings.TrimRight(base, "/") + "/" + escapePath(strings.TrimLeft(value, "/"))
	// A value of ".." segments must not climb out of the base's path.
	u, err := url.Parse(out)
	b, _ := url.Parse(base)
	if err != nil || u.Host != b.Host || u.Scheme != b.Scheme {
		return "", false
	}
	for _, seg := range strings.Split(strings.TrimLeft(value, "/"), "/") {
		if seg == ".." {
			return "", false
		}
	}
	return out, true
}

// escapePath percent-encodes each segment of a path, keeping the slashes.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// Span is one span's values of the link attributes, as read from storage.
type Span struct {
	TraceID     string
	SpanID      string
	ServiceName string
	SpanName    string
	Role        string // io.role: input (received) or output (sent), if set
	Time        time.Time
	Values      map[string]string // attribute key -> value
}

// Carrier is a span that carried a link.
type Carrier struct {
	SpanID   string    `json:"span_id"`
	Service  string    `json:"service"`
	SpanName string    `json:"span_name"`
	Role     string    `json:"role,omitempty"`
	Time     time.Time `json:"time"`
}

// Link is one distinct address of an attribute within a message, with the
// spans that carried it, earliest first.
type Link struct {
	// URL is the resolved address; empty when the value does not make a
	// link, and the value is then shown as plain text.
	URL   string    `json:"url,omitempty"`
	Value string    `json:"value"`
	Spans []Carrier `json:"spans"`
}

// Links is one attribute's links in one message, earliest first.
type Links struct {
	Links []Link `json:"links"`
	// More counts the distinct links beyond MaxLinksPerMessage.
	More int `json:"more,omitempty"`
}

// Collect groups spans by trace and attribute. Identical addresses count
// once and list every span that carried them; the earliest span's link is
// first, which for a flow is where the message entered.
func Collect(attrs []Attribute, spans []Span) map[string]map[string]Links {
	sorted := append([]Span(nil), spans...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].TraceID != sorted[j].TraceID {
			return sorted[i].TraceID < sorted[j].TraceID
		}
		return sorted[i].Time.Before(sorted[j].Time)
	})
	type key struct{ trace, attr, addr string }
	index := map[key]int{}
	out := map[string]map[string]Links{}
	for _, sp := range sorted {
		for _, a := range attrs {
			v := strings.TrimSpace(sp.Values[a.Attribute])
			if v == "" {
				continue
			}
			u, _ := Resolve(a, v)
			addr := u
			if addr == "" {
				addr = "value:" + v
			}
			byAttr := out[sp.TraceID]
			if byAttr == nil {
				byAttr = map[string]Links{}
				out[sp.TraceID] = byAttr
			}
			ls := byAttr[a.Attribute]
			carrier := Carrier{SpanID: sp.SpanID, Service: sp.ServiceName, SpanName: sp.SpanName, Role: sp.Role, Time: sp.Time}
			k := key{sp.TraceID, a.Attribute, addr}
			if i, ok := index[k]; ok {
				if i >= 0 && len(ls.Links[i].Spans) < MaxLinksPerMessage {
					ls.Links[i].Spans = append(ls.Links[i].Spans, carrier)
				}
			} else if len(ls.Links) < MaxLinksPerMessage {
				index[k] = len(ls.Links)
				ls.Links = append(ls.Links, Link{URL: u, Value: v, Spans: []Carrier{carrier}})
			} else {
				index[k] = -1
				ls.More++
			}
			byAttr[a.Attribute] = ls
		}
	}
	return out
}

// ForSpan resolves one span's own links, in the configured order. Trace
// detail shows them on every span that has one.
func ForSpan(attrs []Attribute, values map[string]string) []SpanLink {
	var out []SpanLink
	for _, a := range attrs {
		v := strings.TrimSpace(values[a.Attribute])
		if v == "" {
			continue
		}
		u, _ := Resolve(a, v)
		out = append(out, SpanLink{Attribute: a.Attribute, Label: labelOf(a), URL: u, Value: v})
	}
	return out
}

// SpanLink is one link on one span.
type SpanLink struct {
	Attribute string `json:"attribute"`
	Label     string `json:"label"`
	URL       string `json:"url,omitempty"`
	Value     string `json:"value"`
}

func labelOf(a Attribute) string {
	if a.Label != "" {
		return a.Label
	}
	return a.Attribute
}
