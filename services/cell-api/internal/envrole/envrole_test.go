// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package envrole

import "testing"

func TestSuggest(t *testing.T) {
	for env, want := range map[string]string{
		"production": Production, "prod": Production, "PRD": Production, "prod-eu": Production,
		"eu_prd_1": Production, "Live": Production, "us.prod.2": Production,
		"preprod": NonProduction, "nonprod": NonProduction, "dev": NonProduction, "test": NonProduction,
		"staging": NonProduction, "Blablablbu": NonProduction, "productionlike": NonProduction,
	} {
		if got := Suggest(env); got != want {
			t.Errorf("Suggest(%q) = %s, want %s", env, got, want)
		}
	}
}

func TestConfirmedBeatsSuggested(t *testing.T) {
	t.Cleanup(func() { Set(Config{}) })
	Set(Config{Roles: map[string]string{"Blablablbu": Production, "prod-sandbox": NonProduction, "x": "bogus"}})
	if !IsProduction("Blablablbu") || IsProduction("prod-sandbox") || !IsProduction("prod-eu") || IsProduction("") {
		t.Error("roles")
	}
	if _, ok := Confirmed("x"); ok {
		t.Error("an unknown role is dropped")
	}
	if !Listed([]string{ProductionToken}, "Blablablbu") || Listed([]string{ProductionToken}, "dev") || !Listed([]string{"dev"}, "dev") {
		t.Error("Listed")
	}
}
