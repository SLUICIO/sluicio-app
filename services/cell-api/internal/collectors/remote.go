// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Editing a configuration happens on its masked form: nobody sees a secret
// to change it. RestoreSecrets puts the stored values back where the
// edited text still carries a masked one, so a push sends the real
// credentials without anyone having read them.
//
// A masked value is restored only when it stands at the same place and is
// exactly what masking the original produced there. Anything else (a
// masked value moved to a new place, half of a masked URL edited) is
// refused: guessing which secret was meant would send the wrong one.

// ErrMaskedValue is returned when the edited configuration carries a
// masked value that cannot be traced back to a stored one.
type ErrMaskedValue struct{ Path string }

func (e ErrMaskedValue) Error() string {
	return fmt.Sprintf("%s still holds a masked value that does not match the stored configuration there; type the real value or remove it", e.Path)
}

// collectorRedacted is what a collector reports in place of an opaque
// value (a header, a password): checked on 0.166.0, a literal and an
// embedded reference ("Bearer ${env:TOKEN}") both read [REDACTED] in the
// configuration as written; only a bare ${env:NAME} is reported as is.
const collectorRedacted = "[REDACTED]"

// ErrHiddenValue is returned when a value the collector hides (it
// reports [REDACTED] there) would have to be sent, inside a list, and
// Sluicio has no copy of it.
type ErrHiddenValue struct{ Path string }

func (e ErrHiddenValue) Error() string {
	return fmt.Sprintf("the collector hides the value at %s (it reports [REDACTED]) and Sluicio has no copy of it; type the value, or a ${env:NAME} reference", e.Path)
}

// RestoreSecrets returns edited with every masked value replaced by the
// stored value at the same place in original.
//
// A collector reports an opaque value (a header, a password) as
// [REDACTED], so the stored value there is no value at all (see
// secrets.go). Such a value is taken from applied, the configuration the
// collector last applied from Sluicio, as it was sent, when that has
// one there. Otherwise it comes from the collector's local files: it is
// left out of what is sent, and since the remote configuration is merged
// over those files, the local value stays in effect. Inside a list,
// leaving it out would replace the list without it, so that is refused.
func RestoreSecrets(edited, original, applied string) (string, error) {
	if !strings.Contains(edited, Masked) && !strings.Contains(edited, collectorRedacted) {
		return edited, nil
	}
	maskedOriginal, ok := MaskConfig(original)
	if !ok {
		return "", errors.New("the stored configuration cannot be read to restore its secrets")
	}
	var ed, orig, morig, app yaml.Node
	if err := yaml.Unmarshal([]byte(edited), &ed); err != nil {
		return "", fmt.Errorf("the configuration is not valid YAML: %w", err)
	}
	_ = yaml.Unmarshal([]byte(original), &orig)
	_ = yaml.Unmarshal([]byte(maskedOriginal), &morig)
	_ = yaml.Unmarshal([]byte(applied), &app)
	origAt, maskedAt, appliedAt := map[string]*yaml.Node{}, map[string]*yaml.Node{}, map[string]*yaml.Node{}
	index(&orig, "", origAt)
	index(&morig, "", maskedAt)
	index(&app, "", appliedAt)

	var failed error
	leaveOut := map[string]bool{}
	walk(&ed, "", func(n *yaml.Node, path string) {
		if failed != nil || n.Kind != yaml.ScalarNode {
			return
		}
		var o *yaml.Node
		switch {
		case strings.Contains(n.Value, Masked):
			m := maskedAt[path]
			o = origAt[path]
			if m == nil || o == nil || m.Value != n.Value {
				failed = ErrMaskedValue{Path: displayPath(path)}
				return
			}
		case n.Value == collectorRedacted:
			// Shown as reported where the key does not look secret.
			if o = origAt[path]; o == nil || o.Value != collectorRedacted {
				return
			}
		default:
			return
		}
		if o.Value == collectorRedacted {
			a := appliedAt[path]
			switch {
			case a != nil && a.Value != collectorRedacted:
				o = a
			case strings.Contains(path, "["):
				failed = ErrHiddenValue{Path: displayPath(path)}
				return
			default:
				leaveOut[path] = true
				return
			}
		}
		n.Value, n.Tag, n.Style = o.Value, o.Tag, o.Style
	})
	if failed != nil {
		return "", failed
	}
	if len(leaveOut) > 0 {
		walk(&ed, "", func(n *yaml.Node, path string) {
			if n.Kind != yaml.MappingNode {
				return
			}
			for i := 0; i+1 < len(n.Content); {
				if leaveOut[path+"/"+n.Content[i].Value] {
					n.Content = append(n.Content[:i], n.Content[i+2:]...)
					continue
				}
				i += 2
			}
		})
	}
	return encode(&ed)
}

// PrepareRemote turns a configuration as written into what is sent to a
// supervisor. The supervisor merges a remote configuration last, over its
// own OpAMP extension for the collector it runs, and lists are replaced,
// not merged. So the extension's own settings are left out (they would
// point the collector at a port that no longer exists), and "opamp" stays
// in service.extensions (without it the collector loses its supervisor).
func PrepareRemote(config string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(config), &doc); err != nil {
		return "", fmt.Errorf("the configuration is not valid YAML: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return "", errors.New("the configuration must be a YAML mapping")
	}
	top := doc.Content[0]
	if ext := mapGet(top, "extensions"); ext != nil && ext.Kind == yaml.MappingNode {
		mapDelete(ext, "opamp")
		if len(ext.Content) == 0 {
			mapDelete(top, "extensions")
		}
	}
	if svc := mapGet(top, "service"); svc != nil && svc.Kind == yaml.MappingNode {
		if list := mapGet(svc, "extensions"); list != nil && list.Kind == yaml.SequenceNode {
			has := false
			for _, item := range list.Content {
				if item.Value == "opamp" {
					has = true
				}
			}
			if !has {
				list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "opamp"})
			}
		}
	}
	return encode(&doc)
}

func encode(n *yaml.Node) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return "", err
	}
	_ = enc.Close()
	return buf.String(), nil
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapDelete(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// A path names a node by mapping keys and, in a list, by the item's name
// or key field when it has one (headers, attributes), so reordering a list
// does not move a secret onto another item; otherwise by position.
func itemID(item *yaml.Node, i int) string {
	if item.Kind == yaml.MappingNode {
		for _, k := range []string{"name", "key"} {
			if v := mapGet(item, k); v != nil && v.Kind == yaml.ScalarNode {
				return "[" + k + "=" + v.Value + "]"
			}
		}
	}
	return "[" + strconv.Itoa(i) + "]"
}

func walk(n *yaml.Node, path string, visit func(*yaml.Node, string)) {
	visit(n, path)
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			walk(c, path, visit)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			walk(n.Content[i+1], path+"/"+n.Content[i].Value, visit)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			walk(c, path+itemID(c, i), visit)
		}
	}
}

func index(n *yaml.Node, path string, into map[string]*yaml.Node) {
	walk(n, path, func(x *yaml.Node, p string) {
		if x.Kind == yaml.ScalarNode {
			into[p] = x
		}
	})
}

func displayPath(p string) string {
	return strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", ".")
}
