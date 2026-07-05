// SPDX-License-Identifier: LicenseRef-Sluicio-Enterprise
//
// Copyright (c) ROMA IT AB. All rights reserved.
// Part of Sluicio Enterprise Edition — see ee/LICENSE.md.

// Command sluicio-license is the internal tool for minting + inspecting
// Sluicio Enterprise license keys. It is NOT shipped to customers.
//
//	sluicio-license keygen  -out <priv-path>            # new Ed25519 keypair (prints public key to embed)
//	sluicio-license mint     -key <priv-path> -customer "Acme AB" \
//	    -features sso,rbac_advanced,audit_log,retention_long \
//	    -days 365 [-max-retention-days 365]              # prints a signed license token
//	sluicio-license inspect  -token <token>             # verifies against the embedded public key + prints claims
//
// The private key is the crown jewel: keep it out of the repo and out of any
// shared location. The public counterpart is embedded in the app at
// pkg/license/sluicio_license_ed25519.pub.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/license"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "keygen":
		keygen(os.Args[2:])
	case "mint":
		mint(os.Args[2:])
	case "inspect":
		inspect(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sluicio-license <keygen|mint|inspect> [flags]")
	os.Exit(2)
}

func keygen(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "path to write the base64 Ed25519 private key (chmod 600)")
	_ = fs.Parse(args)
	if *out == "" {
		fail("keygen: -out is required")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fail("keygen: %v", err)
	}
	if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		fail("keygen: write private key: %v", err)
	}
	fmt.Printf("private key written to %s (keep it out of the repo)\n", *out)
	fmt.Printf("public key (write this to pkg/license/sluicio_license_ed25519.pub):\n%s\n", base64.StdEncoding.EncodeToString(pub))
}

func mint(args []string) {
	fs := flag.NewFlagSet("mint", flag.ExitOnError)
	keyPath := fs.String("key", "", "path to the base64 Ed25519 private key")
	customer := fs.String("customer", "", "customer / organisation name")
	plan := fs.String("plan", "enterprise", "plan name")
	features := fs.String("features", "sso,rbac_advanced,audit_log,retention_long,mfa_policy", "comma-separated entitlements")
	days := fs.Int("days", 365, "validity in days from now (0 = perpetual)")
	maxRetentionDays := fs.Int("max-retention-days", 0, "optional retention cap to embed (0 = unlimited)")
	maxIntegrations := fs.Int("max-integrations", 0, "integration cap to embed (Pro 25, Business 75; 0 = unlimited/Enterprise)")
	_ = fs.Parse(args)
	if *keyPath == "" || *customer == "" {
		fail("mint: -key and -customer are required")
	}

	priv := readPrivateKey(*keyPath)

	now := time.Now()
	claims := license.Claims{
		LicenseID:    uuid.NewString(),
		Customer:     *customer,
		Plan:         *plan,
		Entitlements: splitCSV(*features),
		Limits:       license.Limits{MaxRetentionDays: *maxRetentionDays, MaxIntegrations: *maxIntegrations},
		IssuedAt:     now.Unix(),
		NotBefore:    now.Unix(),
	}
	if *days > 0 {
		claims.ExpiresAt = now.AddDate(0, 0, *days).Unix()
	}

	payload, err := json.Marshal(&claims)
	if err != nil {
		fail("mint: marshal: %v", err)
	}
	sig := ed25519.Sign(priv, payload)
	token := "sluicio_lic_" +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig)

	fmt.Println(token)
	fmt.Fprintf(os.Stderr, "\nminted for %q · features=%s · ", *customer, *features)
	if claims.ExpiresAt == 0 {
		fmt.Fprintln(os.Stderr, "perpetual")
	} else {
		fmt.Fprintf(os.Stderr, "expires %s\n", time.Unix(claims.ExpiresAt, 0).Format("2006-01-02"))
	}
}

func inspect(args []string) {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	token := fs.String("token", "", "license token to verify")
	_ = fs.Parse(args)
	if *token == "" {
		fail("inspect: -token is required")
	}
	mgr, err := license.NewManager()
	if err != nil {
		fail("inspect: %v", err)
	}
	if err := mgr.Load(*token); err != nil {
		fail("inspect: %v", err)
	}
	out, _ := json.MarshalIndent(mgr.Status(), "", "  ")
	fmt.Println(string(out))
}

func readPrivateKey(path string) ed25519.PrivateKey {
	b, err := os.ReadFile(path)
	if err != nil {
		fail("read private key: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		fail("decode private key: %v", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		fail("private key is %d bytes, want %d", len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw)
}

func splitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
