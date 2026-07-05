// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package mapexec runs a Map's transformation against sample input
// and (optionally) validates the result against a pinned schema. The
// "Test" panel on the MapsPage editor sits on top of this — paste an
// input, press Run, see the output plus a validation status.
//
// v1 covers the two formats that come up most often in BizTalk-style
// migrations: XSLT (via the system `xsltproc` binary, libxslt 1.0)
// and Liquid (via osteele/liquid, a pure-Go port). Other formats
// from the editor's dropdown (jq, JSONata, Mustache, Handlebars) are
// rejected with a clear error pointing at what's coming next.
//
// The runtime is intentionally process-isolated for XSLT: every Run
// shells out to xsltproc with the stylesheet and input written to
// temp files, with a hard timeout, so a malformed stylesheet can't
// take the server down. Liquid runs in-process — the osteele/liquid
// engine is sandboxed by design (no file I/O, no network, no shell).
package mapexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/osteele/liquid"
)

// Errors callers can match on.
var (
	// ErrUnsupportedFormat is returned when the map's `format` isn't
	// one of the runtimes we ship. The handler maps this to 400.
	ErrUnsupportedFormat = errors.New("mapexec: format not supported yet")
	// ErrToolMissing is returned when XSLT execution is requested but
	// xsltproc isn't on PATH. Handler maps to 503 with a hint.
	ErrToolMissing = errors.New("mapexec: required external tool not installed")
)

// Result is what every runtime returns: the transformed output, or
// an error string captured from the runtime (parser error, missing
// variable, etc.) when execution failed without a Go-level error.
//
// We separate "the runtime ran and produced an output" from "the
// runtime had a parse / runtime error and produced a message" so
// the API can surface engine-level errors inline in the UI without
// turning them into 500s.
type Result struct {
	Output      string `json:"output"`
	EngineError string `json:"engine_error,omitempty"`
}

// Execute dispatches by format. The `source` is the transformation
// itself (XSLT stylesheet, Liquid template); `input` is the sample
// document the user pasted.
func Execute(ctx context.Context, format, source, input string) (Result, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "xslt":
		return runXSLT(ctx, source, input)
	case "liquid":
		return runLiquid(ctx, source, input)
	default:
		return Result{}, fmt.Errorf("%w: %q (v1 supports xslt and liquid)",
			ErrUnsupportedFormat, format)
	}
}

// runXSLT shells out to xsltproc. We write the stylesheet and the
// input to temp files in a per-call temp dir, then invoke xsltproc
// with a 10s timeout. Both stdout (the transformation result) and
// stderr (parse / runtime diagnostics from libxslt) are captured —
// stderr becomes EngineError so the UI can surface the line numbers
// libxslt emits.
func runXSLT(ctx context.Context, stylesheet, input string) (Result, error) {
	tool, err := exec.LookPath("xsltproc")
	if err != nil {
		return Result{}, fmt.Errorf("%w: xsltproc — install via `brew install libxslt` or your distro package manager",
			ErrToolMissing)
	}

	dir, err := os.MkdirTemp("", "mapexec-xslt-*")
	if err != nil {
		return Result{}, fmt.Errorf("mkdir temp: %w", err)
	}
	defer os.RemoveAll(dir)

	stylePath := filepath.Join(dir, "stylesheet.xslt")
	inputPath := filepath.Join(dir, "input.xml")
	if err := os.WriteFile(stylePath, []byte(stylesheet), 0o600); err != nil {
		return Result{}, fmt.Errorf("write stylesheet: %w", err)
	}
	if err := os.WriteFile(inputPath, []byte(input), 0o600); err != nil {
		return Result{}, fmt.Errorf("write input: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, tool, "--nonet", "--novalid", stylePath, inputPath)
	// Sandboxing: empty env so the child can't pick up surprise vars,
	// fixed cwd in the temp dir, no stdin (libxslt would block).
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Dir = dir

	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, runErr := cmd.Output()

	out := Result{Output: string(stdout)}
	if runErr != nil {
		// xsltproc exits non-zero on any parse / runtime error. The
		// diagnostic text on stderr is what users care about — surface
		// it. The Go-level error itself ("exit status 4") is noise.
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		out.EngineError = msg
		// Don't return a Go error — the caller will see EngineError and
		// render it inline in the Test panel.
	}
	return out, nil
}

// runLiquid renders a Liquid template against a JSON input parsed as
// the template's `bindings`. The user's sample document is expected
// to be a JSON object whose top-level keys become the variables the
// template can reference (`{{ name }}`, `{% for o in orders %}`,…).
//
// If the input doesn't parse as JSON the engine error is "expected
// JSON object for Liquid input — got: <prefix>". This is intentional
// over silently treating the input as a single string variable.
func runLiquid(_ context.Context, template, input string) (Result, error) {
	engine := liquid.NewEngine()

	bindings, perr := parseLiquidBindings(input)
	if perr != nil {
		return Result{EngineError: perr.Error()}, nil
	}

	rendered, rerr := engine.ParseAndRenderString(template, bindings)
	if rerr != nil {
		return Result{EngineError: rerr.Error()}, nil
	}
	return Result{Output: rendered}, nil
}

// parseLiquidBindings decodes the sample input as JSON. An empty
// input becomes an empty binding map so a template with no
// references still renders.
func parseLiquidBindings(input string) (map[string]any, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return map[string]any{}, nil
	}
	// Liquid bindings are arbitrary nested data; we trust the user's
	// sample. Use json.Decoder so we can reject non-object roots with
	// a useful message rather than the default "json: cannot unmarshal
	// array into map" cryptic error.
	var anyVal any
	if err := json.Unmarshal([]byte(s), &anyVal); err != nil {
		return nil, fmt.Errorf("input is not valid JSON: %v", err)
	}
	m, ok := anyVal.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("liquid input must be a JSON object whose keys are the variables the template references; got %T",
			anyVal)
	}
	return m, nil
}
