// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integrations

import (
	"regexp"
	"strings"
	"sync"
)

// ServiceNameAttribute is the matcher attribute that selects which
// services belong to an integration (its membership). Every other
// attribute is a row-level predicate (producer, consumer, …) AND-ed onto
// the integration's telemetry queries rather than affecting membership.
const ServiceNameAttribute = "service.name"

// IsServiceMatcher reports whether this matcher contributes to integration
// MEMBERSHIP (matches on the service name) vs. being a row-level attribute
// predicate. A blank attribute is treated as service.name for backward
// compatibility (older matchers were created before the attribute column
// was populated).
func (m Matcher) IsServiceMatcher() bool {
	return m.Attribute == "" || m.Attribute == ServiceNameAttribute
}

// Match returns true if the supplied service name satisfies the matcher.
// Regex compilation errors fail closed (no match). Only meaningful for
// service matchers (see IsServiceMatcher); attribute matchers are applied
// at query time, not here.
func (m Matcher) Match(serviceName string) bool {
	switch m.Operator {
	case OperatorEquals:
		return serviceName == m.Value
	case OperatorPrefix:
		return strings.HasPrefix(serviceName, m.Value)
	case OperatorSuffix:
		return strings.HasSuffix(serviceName, m.Value)
	case OperatorContains:
		return strings.Contains(serviceName, m.Value)
	case OperatorRegex:
		re, err := compileRegex(m.Value)
		if err != nil {
			return false
		}
		return re.MatchString(serviceName)
	}
	return false
}

// Validate checks that the operator is recognized and that regex values
// are well-formed. Empty values are rejected — an empty matcher would
// match every service.
func (m Matcher) Validate() error {
	if m.Value == "" {
		return errInvalid("value must not be empty")
	}
	switch m.Operator {
	case OperatorEquals, OperatorPrefix, OperatorSuffix, OperatorContains:
		// nothing more to check
	case OperatorRegex:
		if _, err := regexp.Compile(m.Value); err != nil {
			return errInvalid("invalid regex: " + err.Error())
		}
	default:
		return errInvalid("unknown operator " + string(m.Operator))
	}
	return nil
}

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func errInvalid(s string) error          { return &validationError{msg: s} }

// IsValidationError reports whether err originated from Matcher.Validate.
func IsValidationError(err error) bool {
	_, ok := err.(*validationError)
	return ok
}

// --- regex cache ---
//
// Recompiling the same regex on every span is wasteful; the resolver
// can hit thousands of services per request. The cache is small,
// bounded by the number of distinct regex matcher values, and is safe
// for concurrent use.

var (
	regexCacheMu sync.RWMutex
	regexCache   = map[string]*regexp.Regexp{}
)

func compileRegex(pattern string) (*regexp.Regexp, error) {
	regexCacheMu.RLock()
	re, ok := regexCache[pattern]
	regexCacheMu.RUnlock()
	if ok {
		return re, nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCacheMu.Lock()
	regexCache[pattern] = compiled
	regexCacheMu.Unlock()
	return compiled, nil
}
