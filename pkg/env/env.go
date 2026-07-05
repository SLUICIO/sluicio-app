// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package env provides tiny helpers for reading typed values out of
// environment variables with defaults. It deliberately stays smaller
// than viper or koanf — config arrives as env vars, and that's enough.
package env

import (
	"os"
	"strconv"
	"time"
)

// String returns the value of key, or fallback if unset / empty.
func String(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Int returns the integer value of key, or fallback if unset / unparseable.
func Int(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// Duration returns the duration value of key, or fallback if unset / unparseable.
func Duration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

// Bool returns the boolean value of key, or fallback if unset.
// Accepts: "1", "true", "yes", "on" (case insensitive) as true.
func Bool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes", "on", "ON":
		return true
	case "0", "false", "FALSE", "False", "no", "NO", "No", "off", "OFF":
		return false
	}
	return fallback
}
