// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Per-type documentation links (issue #8, WS4).
//
// A system type payload says what Sluicio detects and which checks it
// applies. It doesn't say what the thing IS, what its metrics mean, or
// how to set up the collector — that lives on docs.sluicio.com. Carrying
// the link means an agent can cite the reference instead of paraphrasing
// half-remembered knowledge about RabbitMQ, and a human reading the same
// payload gets one click to the page.
//
// Only types with a page get a link. A custom org-defined type has no
// entry on the public docs site, and emitting a plausible-looking URL
// for it would send both agents and people to a 404 — worse than saying
// nothing, because a broken link still reads as authoritative.
//
// The set below is explicit rather than derived from the key, and
// TestEveryBuiltInTypeHasADocsDecision fails when a built-in is missing
// from it. Adding a system type is already meant to include a docs page
// (there's a checklist for it); this makes forgetting a build failure
// instead of a dead link discovered by a customer.

package api

// docsBaseURL is the public documentation site. Deliberately not
// configurable: it's the same public site for every cell, self-hosted or
// not, and a per-cell override would only create ways to point at a page
// that doesn't exist.
const docsBaseURL = "https://docs.sluicio.com"

// systemTypeDocsPages lists built-in type keys that have a reference
// page at /system-types/<key>/. Verified against the docs site's
// content directory; keep in step when adding or removing a type.
var systemTypeDocsPages = map[string]bool{
	"rabbitmq":         true,
	"artemis":          true,
	"azure-servicebus": true,
	"krakend":          true,
	"wso2-apim":        true,
	"kafka":            true,
	"confluent-kafka":  true,
	"nats":             true,
	"debezium":         true,
	"otel-collector":   true,
	"dotnet-service":   true,
	"paperless-ngx":    true,
}

// docsURLForSystemType returns the reference page for a type key, or ""
// when there isn't one.
//
// An org may override a built-in by defining a type with the same key.
// That still resolves to the built-in's page: the override changes the
// org's detection and checks, not what the underlying technology is.
func docsURLForSystemType(key string) string {
	if key == "" || !systemTypeDocsPages[key] {
		return ""
	}
	return docsBaseURL + "/system-types/" + key + "/"
}
