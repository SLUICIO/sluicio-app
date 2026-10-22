// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package envctx

import (
	"context"
	"testing"
)

func TestSets(t *testing.T) {
	ctx := context.Background()
	if _, ok := From(ctx); ok || !In(ctx, "x") {
		t.Error("an unnarrowed context reads all environments")
	}
	one := With(ctx, "production")
	if env, ok := From(one); !ok || env != "production" || !In(one, "production") || In(one, "test") {
		t.Error("one environment")
	}
	two := WithSet(ctx, []string{"production", "test"})
	if _, ok := From(two); ok {
		t.Error("From is about one environment, never a set")
	}
	if set, ok := Set(two); !ok || len(set) != 2 || !In(two, "test") || In(two, "dev") {
		t.Errorf("set: %v", set)
	}
	if WithSet(ctx, nil) != ctx {
		t.Error("an empty set narrows nothing")
	}
}
