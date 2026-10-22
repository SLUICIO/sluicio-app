// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package envctx carries the environment a read or an evaluation is
// about (#40) through a context. Its own package so the API, the alert
// engine, the notification profiles and the store adapters can all read
// it without depending on each other.
package envctx

import "context"

type key struct{}

// With narrows ctx to one environment: its canonical name, or "" for
// telemetry with no environment.
func With(ctx context.Context, env string) context.Context {
	return context.WithValue(ctx, key{}, []string{env})
}

// WithSet narrows ctx to several environments, for a dashboard that
// reads production and test together. An empty set narrows nothing.
func WithSet(ctx context.Context, envs []string) context.Context {
	if len(envs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, key{}, append([]string(nil), envs...))
}

// From returns the ONE environment ctx is narrowed to. ok is false for
// "all environments", which is also what every context means unless With
// was called on it, and for a set of several: what is about one
// environment (an alert, a profile) is about none of a set's.
func From(ctx context.Context) (env string, ok bool) {
	set, _ := ctx.Value(key{}).([]string)
	if len(set) != 1 {
		return "", false
	}
	return set[0], true
}

// Set returns the environments ctx is narrowed to, one or several. ok is
// false for all environments.
func Set(ctx context.Context) ([]string, bool) {
	set, _ := ctx.Value(key{}).([]string)
	return set, len(set) > 0
}

// In reports whether env is among the ones ctx is narrowed to; true when
// it is narrowed to none.
func In(ctx context.Context, env string) bool {
	set, ok := Set(ctx)
	if !ok {
		return true
	}
	for _, e := range set {
		if e == env {
			return true
		}
	}
	return false
}

// Display names an environment for people: "(no environment)" for the
// empty one.
func Display(env string) string {
	if env == "" {
		return "(no environment)"
	}
	return env
}
