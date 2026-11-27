// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Masked replaces a secret value wherever a configuration is shown.
const Masked = "********"

// Every configuration is treated as if it held credentials, whatever the
// documentation recommends: a recommendation is not a guarantee. The API
// only ever returns masked configurations; the plaintext leaves this
// package only towards the collector, and that is a later stage.

// sensitiveKeyParts match a mapping key after lowercasing and dropping
// '_', '-' and '.', so client_secret, x-api-key and sasToken all count.
var sensitiveKeyParts = []string{
	"password", "passwd", "secret", "token", "apikey", "authorization",
	"credential", "privatekey", "bearer", "cookie", "accesskey",
	"connectionstring", "signature", "passphrase",
}

// sensitiveKeysExact are too short to match as parts without masking
// harmless keys (keyfile, dsnresolver).
var sensitiveKeysExact = map[string]bool{"pwd": true, "dsn": true, "key": true, "pass": true, "auth": true, "keypem": true}

// envRef is a value that only points at an environment variable. It
// carries no secret and is exactly what the documentation recommends, so
// it stays readable.
var envRef = regexp.MustCompile(`^\$\{(env:)?[A-Za-z_][A-Za-z0-9_]*(:-[^}]*)?\}$`)

func sensitiveKey(k string) bool {
	n := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(k))
	if sensitiveKeysExact[n] {
		return true
	}
	for _, p := range sensitiveKeyParts {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}

// MaskConfig returns body with every secret-looking value masked.
//
// It fails closed: a body that does not parse as YAML (JSON is YAML too)
// is not shown at all rather than shown unmasked.
func MaskConfig(body string) (string, bool) {
	if strings.TrimSpace(body) == "" {
		return body, true
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		return "", false
	}
	maskNode(&doc, false)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", false
	}
	_ = enc.Close()
	return buf.String(), true
}

// maskNode walks the tree. underSecret is set below a sensitive key, so a
// secret given as a list or a map (headers: {authorization: [...]}) is
// masked all the way down.
func maskNode(n *yaml.Node, underSecret bool) {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			maskNode(c, underSecret)
		}
	case yaml.MappingNode:
		// A pair spelled as {name: authorization, value: ...}, which is how
		// the collector reports headers in its effective config: the
		// secret is the value whose sibling names a sensitive key.
		namedSecret := false
		for i := 0; i+1 < len(n.Content); i += 2 {
			if k := n.Content[i].Value; (k == "name" || k == "key") && n.Content[i+1].Kind == yaml.ScalarNode && sensitiveKey(n.Content[i+1].Value) {
				namedSecret = true
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			maskNode(n.Content[i+1], underSecret || sensitiveKey(k) || (namedSecret && k == "value"))
		}
	case yaml.ScalarNode:
		maskScalar(n, underSecret)
	case yaml.AliasNode:
		// The anchor it points at is masked where it is defined.
	}
}

func maskScalar(n *yaml.Node, secret bool) {
	v := n.Value
	if v == "" || envRef.MatchString(strings.TrimSpace(v)) {
		return
	}
	// A private key is a secret under whatever name it is given.
	if secret || (strings.Contains(v, "-----BEGIN") && strings.Contains(v, "PRIVATE KEY")) {
		n.Value, n.Tag, n.Style = Masked, "!!str", 0
		return
	}
	// Credentials in a URL's query (?api_key=..., ?sig=...) are secrets
	// too, by the parameter's name.
	if strings.Contains(v, "://") && strings.Contains(v, "?") {
		if u, err := url.Parse(v); err == nil && u.RawQuery != "" {
			q := u.Query()
			changed := false
			for k := range q {
				if sensitiveKey(k) || k == "sig" || k == "code" {
					q.Set(k, Masked)
					changed = true
				}
			}
			if changed {
				u.RawQuery = strings.ReplaceAll(q.Encode(), url.QueryEscape(Masked), Masked)
				v = u.String()
				n.Value = v
			}
		}
	}
	// Credentials embedded in a URL (https://user:pass@host) are secrets
	// whatever key they sit under.
	if strings.Contains(v, "://") && strings.Contains(v, "@") {
		if u, err := url.Parse(v); err == nil && u.User != nil {
			if _, hasPass := u.User.Password(); hasPass {
				u.User = url.UserPassword(u.User.Username(), "xxxxx")
				n.Value = strings.Replace(u.String(), "xxxxx", Masked, 1)
			} else {
				u.User = url.User(Masked)
				n.Value = u.String()
			}
		}
	}
}

var urlUserinfo = regexp.MustCompile(`(\b[a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s@]+@`)

// MaskText masks URL credentials inside free text such as a health error
// ("Post https://user:pass@host: ..."), which is stored and shown as is.
func MaskText(s string) string {
	return urlUserinfo.ReplaceAllString(s, "${1}"+Masked+"@")
}
