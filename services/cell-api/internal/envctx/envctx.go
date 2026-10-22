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
	return context.WithValue(ctx, key{}, env)
}

// From returns the environment ctx is narrowed to. ok is false for "all
// environments", which is also what every context means unless With was
// called on it.
func From(ctx context.Context) (env string, ok bool) {
	env, ok = ctx.Value(key{}).(string)
	return env, ok
}

// Display names an environment for people: "(no environment)" for the
// empty one.
func Display(env string) string {
	if env == "" {
		return "(no environment)"
	}
	return env
}
