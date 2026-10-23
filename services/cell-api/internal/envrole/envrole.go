// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package envrole says which environments are production (#40).
//
// An environment's name is free text: OpenTelemetry's
// deployment.environment.name carries no notion of production, and one
// customer's production is "prd-eu-1" and another's "Blablablbu". So
// production is a ROLE an environment has, confirmed by an operator per
// environment, and suggested from the name until then: a name with a
// word such as prod, production, prd or live in it is suggested as
// production, everything else as not.
//
// The role is what a page opens on (an integration's production
// environments), what a dashboard can show ("production environments",
// right as environments come and go) and what a check or a notification
// profile can be scoped to, so adding prod-us needs no rule edited.
//
// Its own package, holding the cell's setting in memory like the
// environment expression does, so the API, the alert engine and the
// notification profiles can all ask without depending on each other.
package envrole

import (
	"strings"
	"sync/atomic"
	"unicode"
)

// ProductionToken stands for "every production environment" in a list
// of environments: a dashboard's, a check's, a notification profile's.
// Not a name an environment can have, as names never start with @ here.
const ProductionToken = "@production"

// Role values.
const (
	Production    = "production"
	NonProduction = "non-production"
)

// Config is the cell setting: confirmed roles by environment name.
type Config struct {
	Roles map[string]string
}

var current atomic.Pointer[Config]

// Set replaces the confirmed roles.
func Set(c Config) {
	clean := map[string]string{}
	for env, role := range c.Roles {
		if role == Production || role == NonProduction {
			clean[env] = role
		}
	}
	current.Store(&Config{Roles: clean})
}

// Confirmed returns an environment's confirmed role, ok false when an
// operator has not decided it.
func Confirmed(env string) (string, bool) {
	c := current.Load()
	if c == nil {
		return "", false
	}
	role, ok := c.Roles[env]
	return role, ok
}

// productionWords are what production tends to be called.
var productionWords = map[string]bool{"prod": true, "production": true, "prd": true, "live": true}

// Suggest is the role a name suggests: production when one of its words
// is a production word ("prod", "prod-eu", "eu_prd_1", "Live"), not for
// "preprod", "nonprod" or "production-like" spellings glued to another
// word.
func Suggest(env string) string {
	for _, w := range strings.FieldsFunc(strings.ToLower(env), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if productionWords[w] {
			return Production
		}
	}
	return NonProduction
}

// Of returns an environment's role: confirmed, else suggested.
// "(no environment)" is never production.
func Of(env string) string {
	if env == "" {
		return NonProduction
	}
	if role, ok := Confirmed(env); ok {
		return role
	}
	return Suggest(env)
}

// IsProduction reports whether env has the production role.
func IsProduction(env string) bool { return Of(env) == Production }

// Listed reports whether env is in a list of environments that may hold
// ProductionToken.
func Listed(list []string, env string) bool {
	for _, e := range list {
		if e == env || (e == ProductionToken && IsProduction(env)) {
			return true
		}
	}
	return false
}

// HasToken reports whether a list holds ProductionToken.
func HasToken(list []string) bool {
	for _, e := range list {
		if e == ProductionToken {
			return true
		}
	}
	return false
}
