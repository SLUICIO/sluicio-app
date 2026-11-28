// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Validator checks a configuration with the cell's own OTelFlow (its
// /api/validate), so a configuration is checked before it is sent and
// never leaves the cell to be checked. The collector checks it again when
// it applies it; this catches mistakes before anything is sent.
type Validator struct {
	// BaseURL is OTelFlow as cell-api reaches it, prefix included
	// (http://otelflow:7317/otelflow).
	BaseURL string
	Client  *http.Client
}

// Diagnostic is one finding.
type Diagnostic struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

// Validation is OTelFlow's answer plus the version it checked against,
// which is its default when it does not know the one asked for.
type Validation struct {
	Valid          bool         `json:"valid"`
	Diagnostics    []Diagnostic `json:"diagnostics"`
	CheckedVersion string       `json:"checked_version,omitempty"`
}

// Errors counts the findings that would stop a collector from starting.
func (v Validation) Errors() int {
	n := 0
	for _, d := range v.Diagnostics {
		if d.Severity == "error" {
			n++
		}
	}
	return n
}

// Validate checks config against version (contrib), falling back to
// OTelFlow's default version when it does not know that one.
func (v *Validator) Validate(ctx context.Context, config, version string) (Validation, error) {
	version = strings.TrimPrefix(version, "v")
	res, status, err := v.call(ctx, config, version)
	if err == nil && status == http.StatusBadRequest && version != "" {
		res, status, err = v.call(ctx, config, "")
		version = ""
	}
	if err != nil {
		return Validation{}, err
	}
	if status != http.StatusOK {
		return Validation{}, fmt.Errorf("otelflow: validate answered %d", status)
	}
	res.CheckedVersion = version
	return res, nil
}

func (v *Validator) call(ctx context.Context, config, version string) (Validation, int, error) {
	body, _ := json.Marshal(map[string]string{"config": config, "version": version, "distribution": "contrib"})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(v.BaseURL, "/")+"/api/validate", bytes.NewReader(body))
	if err != nil {
		return Validation{}, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	c := v.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return Validation{}, 0, fmt.Errorf("otelflow: %w", err)
	}
	defer resp.Body.Close()
	var out Validation
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return Validation{}, 0, fmt.Errorf("otelflow: %w", err)
		}
	}
	return out, resp.StatusCode, nil
}
