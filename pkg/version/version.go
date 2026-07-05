// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package version exposes the build metadata that every binary embeds.
// The values are populated at link time via -ldflags from the build
// system.
package version

var (
	// Version is the semantic version of the build (e.g. "0.1.0").
	Version = "0.0.0-dev"

	// Commit is the git commit the build was produced from.
	Commit = "unknown"

	// BuildDate is the RFC 3339 build timestamp.
	BuildDate = "unknown"
)

// Info bundles version metadata for logging and the /version endpoint.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

// Get returns the current build's version info.
func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
	}
}
