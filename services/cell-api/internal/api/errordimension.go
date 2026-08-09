// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Choosing what an integration's errors are broken down BY (issue #12).
//
// "Which service is failing" is a good question when an integration
// spans several services. It is close to useless when one service
// carries many integrations, which is what a Node-RED runtime, a Camel
// context or a shared iPaaS worker produces. An integration defined as
// `service.name = <runtime> AND node_red.flow.id = <flow>` has exactly
// one member service, so its service breakdown has exactly one row and
// always reads "100% of failures come from <the runtime>". True, and it
// tells the operator nothing they did not already know.
//
// So the dimension is chosen rather than fixed:
//
//   - SEVERAL member services: break down by service. The existing
//     question is the right one, and the existing answer is useful.
//   - ONE member service: the service row is pure noise. Break down by
//     the attribute that DEFINES the integration when that attribute
//     discriminates, and otherwise by span name.
//
// The second fallback matters more than it looks. Most attribute-defined
// integrations pin their attribute to a single value, so grouping by it
// would reproduce the one-row breakdown in a new costume. Span name is
// what is left, and it is genuinely actionable: "pickup_file" versus
// "write_archive_file" tells you where to look, where the runtime's name
// does not.
//
// Which dimension was used is reported to the caller rather than
// inferred by it. A breakdown whose meaning changes silently between
// integrations would be worse than one that is always unhelpful.

package api

import (
	"sort"
	"strings"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
)

// Error-breakdown dimensions, as they appear in the API.
const (
	// ErrorDimService groups by member service name.
	ErrorDimService = "service"
	// ErrorDimAttribute groups by the values of the integration's
	// defining attribute.
	ErrorDimAttribute = "attribute"
	// ErrorDimSpan groups by span name, i.e. the operation that failed.
	ErrorDimSpan = "span"
)

// serviceNameAttr is the matcher attribute that defines MEMBERSHIP
// rather than a row-level predicate.
const serviceNameAttr = "service.name"

// DefiningAttributes returns the attribute keys an integration's
// matchers constrain, excluding service.name, in a stable order.
//
// Sorted rather than left in matcher order so the same integration
// always breaks down by the same key: matcher rows come back in
// whatever order the database returns them, and a breakdown that
// silently regrouped between page loads would be untrustworthy.
func DefiningAttributes(matchers []integrations.Matcher) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, m := range matchers {
		key := strings.TrimSpace(m.Attribute)
		if key == "" || key == serviceNameAttr {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// ErrorDimensionChoice is what a breakdown will group by, and why.
type ErrorDimensionChoice struct {
	// Kind is one of the ErrorDim* constants.
	Kind string
	// AttributeKey is set only for ErrorDimAttribute.
	AttributeKey string
	// Reason is shown to the reader. A breakdown that changes what it
	// means between integrations has to say which meaning is on screen,
	// or the number is not interpretable.
	Reason string
}

// ChooseErrorDimension decides how to attribute an integration's errors.
//
// memberCount is how many services belong to the integration.
// attributeDiscriminates says whether the defining attribute actually
// takes more than one value among the integration's failing traces —
// which the caller establishes by asking, because a matcher using a
// regex or an `in` list can match many values while an `equals` matcher
// matches exactly one.
func ChooseErrorDimension(memberCount int, definingAttrs []string, attributeDiscriminates bool) ErrorDimensionChoice {
	if memberCount > 1 {
		return ErrorDimensionChoice{
			Kind:   ErrorDimService,
			Reason: "this integration spans several services, so which one failed is the useful split",
		}
	}
	if len(definingAttrs) > 0 && attributeDiscriminates {
		key := definingAttrs[0]
		return ErrorDimensionChoice{
			Kind:         ErrorDimAttribute,
			AttributeKey: key,
			Reason:       "one service carries this integration, so failures are split by " + key,
		}
	}
	return ErrorDimensionChoice{
		Kind:   ErrorDimSpan,
		Reason: "one service carries this integration, so failures are split by the operation that failed",
	}
}
