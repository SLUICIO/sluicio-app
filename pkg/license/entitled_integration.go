// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

package license

import "time"

// NewEntitledForTest returns a Manager entitled to features, so
// integration tests in other packages can exercise Enterprise paths
// through the real handlers. It exists only under the integration build
// tag: the shipped binary trusts nothing but a token signed for the
// embedded public key.
func NewEntitledForTest(features ...Feature) *Manager {
	ents := make([]string, len(features))
	for i, f := range features {
		ents[i] = string(f)
	}
	now := time.Now()
	return &Manager{claims: &Claims{
		LicenseID: "integration-test", Customer: "integration test", Plan: "enterprise",
		Entitlements: ents, IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	}}
}
