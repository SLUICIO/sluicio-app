// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Command thirdparty lists the third-party components built into Sluicio's
// Go binaries, with their licences and the licence and notice texts that
// have to travel with them (issue #41).
//
// It reads the module graph of the given packages - what is actually
// linked, not what go.mod mentions - so the list is exactly the release's,
// and differs between releases when the dependencies do. That is why it
// is generated at build time rather than kept by hand.
//
//	go run ./scripts/thirdparty -json out.json -text out.txt ./services/cell-api/cmd/cell-api
//	go run ./scripts/thirdparty -check ./services/...
//
// -check fails when a component's licence is not on the allowlist or
// cannot be identified, so a dependency that could not ship under
// Sluicio's licence is caught in the change that adds it.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/licensecheck"
)

// allowed is the licences a component built into Sluicio may carry:
// permissive ones, and MPL-2.0, whose file-level copyleft does not reach
// code that only links it.
var allowed = map[string]bool{
	"MIT": true, "MIT-0": true, "ISC": true, "0BSD": true, "Zlib": true,
	"BSD-2-Clause": true, "BSD-3-Clause": true, "BSD-2-Clause-Views": true,
	"Apache-2.0": true, "MPL-2.0": true,
	"Unlicense": true, "CC0-1.0": true,
	// Google's PATENTS file (Go, golang.org/x, protobuf): an additional
	// patent GRANT on top of BSD-3-Clause, not a restriction.
	"GooglePatentsFile": true,
}

// licenceFiles are the files collected from a module's root: the licence
// and anything a licence asks to be passed on with it.
func isLicenceFile(name string) bool {
	n := strings.ToUpper(name)
	for _, p := range []string{"LICENSE", "LICENCE", "COPYING", "NOTICE", "PATENTS", "COPYRIGHT"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// Component is one third-party module and what must travel with it.
type Component struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Licenses []string `json:"licenses"`
	Files    []File   `json:"files"`
}

// File is a licence or notice file, verbatim.
type File struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// Report is the generator's output.
type Report struct {
	Product    string      `json:"product"`
	Components []Component `json:"components"`
}

type goPackage struct {
	Standard bool
	Module   *struct {
		Path    string
		Version string
		Dir     string
		Main    bool
		Replace *struct {
			Path    string
			Version string
			Dir     string
		}
	}
}

func main() {
	jsonOut := flag.String("json", "", "write the report as JSON to this file")
	textOut := flag.String("text", "", "write the notices as plain text to this file")
	check := flag.Bool("check", false, "fail if a licence is not allowed or cannot be identified")
	product := flag.String("product", "Sluicio server components", "what the list is for, in its heading")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: thirdparty [-json f] [-text f] [-check] packages...")
		os.Exit(2)
	}
	rep, err := build(*product, flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "thirdparty:", err)
		os.Exit(1)
	}
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*jsonOut, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "thirdparty:", err)
			os.Exit(1)
		}
	}
	if *textOut != "" {
		if err := os.WriteFile(*textOut, []byte(text(rep)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "thirdparty:", err)
			os.Exit(1)
		}
	}
	if *check {
		if bad := violations(rep); len(bad) > 0 {
			fmt.Fprintln(os.Stderr, "thirdparty: licences not allowed or not identified:")
			for _, b := range bad {
				fmt.Fprintln(os.Stderr, "  "+b)
			}
			os.Exit(1)
		}
		fmt.Printf("thirdparty: %d components, all licences allowed\n", len(rep.Components))
	}
}

func build(product string, patterns []string) (Report, error) {
	cmd := exec.Command("go", append([]string{"list", "-deps", "-json"}, patterns...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return Report{}, fmt.Errorf("go list: %v: %s", err, stderr.String())
	}
	type mod struct{ path, version, dir string }
	seen := map[string]mod{}
	stdlib := false
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p goPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return Report{}, fmt.Errorf("decode go list: %w", err)
		}
		if p.Standard {
			stdlib = true
			continue
		}
		if p.Module == nil || p.Module.Main {
			continue
		}
		m := mod{p.Module.Path, p.Module.Version, p.Module.Dir}
		if r := p.Module.Replace; r != nil {
			// A replacement inside this repository is our own code.
			if r.Version == "" && !strings.Contains(r.Dir, string(filepath.Separator)+"pkg"+string(filepath.Separator)+"mod"+string(filepath.Separator)) {
				continue
			}
			m = mod{p.Module.Path, r.Version, r.Dir}
		}
		// Our own workspace modules (plugins) are not third-party.
		if strings.HasPrefix(m.path, "github.com/sluicio/") {
			continue
		}
		seen[m.path] = m
	}

	var comps []Component
	if stdlib {
		c, err := goStdlib()
		if err != nil {
			return Report{}, err
		}
		comps = append(comps, c)
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		m := seen[p]
		files, err := licenceFilesIn(m.dir)
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", p, err)
		}
		comps = append(comps, Component{Name: m.path, Version: m.version, Licenses: identify(files), Files: files})
	}
	return Report{Product: product, Components: comps}, nil
}

// goStdlib is the Go standard library and runtime, linked into every
// binary under Go's own licence.
func goStdlib() (Component, error) {
	out, err := exec.Command("go", "env", "GOROOT", "GOVERSION").Output()
	if err != nil {
		return Component{}, fmt.Errorf("go env: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return Component{}, errors.New("go env: unexpected output")
	}
	files, err := licenceFilesIn(lines[0])
	if err != nil {
		return Component{}, fmt.Errorf("go standard library: %w", err)
	}
	return Component{Name: "Go standard library", Version: lines[1], Licenses: identify(files), Files: files}, nil
}

func licenceFilesIn(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range entries {
		if e.IsDir() || !isLicenceFile(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, File{Name: e.Name(), Text: string(b)})
	}
	return out, nil
}

// identify names the licences in a component's files. A file that is
// mostly one known licence counts as that licence; notice and patent
// files identify nothing and are carried along regardless.
func identify(files []File) []string {
	ids := map[string]bool{}
	for _, f := range files {
		cov := licensecheck.Scan([]byte(f.Text))
		if cov.Percent < 75 {
			continue
		}
		for _, m := range cov.Match {
			ids[m.ID] = true
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func violations(rep Report) []string {
	var bad []string
	for _, c := range rep.Components {
		if len(c.Licenses) == 0 {
			bad = append(bad, fmt.Sprintf("%s %s: no licence identified", c.Name, c.Version))
			continue
		}
		for _, l := range c.Licenses {
			if !allowed[l] {
				bad = append(bad, fmt.Sprintf("%s %s: %s", c.Name, c.Version, l))
			}
		}
	}
	return bad
}

func text(rep Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Third-party components: %s\n\n", rep.Product)
	b.WriteString("These components are built into the software, each under its own licence.\nTheir licence and notice texts follow, verbatim.\n\n")
	for _, c := range rep.Components {
		fmt.Fprintf(&b, "  %s %s  (%s)\n", c.Name, c.Version, strings.Join(c.Licenses, ", "))
	}
	for _, c := range rep.Components {
		fmt.Fprintf(&b, "\n%s\n%s %s\n%s\n", strings.Repeat("=", 78), c.Name, c.Version, strings.Repeat("=", 78))
		for _, f := range c.Files {
			fmt.Fprintf(&b, "\n--- %s ---\n\n%s\n", f.Name, strings.TrimRight(f.Text, "\n"))
		}
	}
	return b.String()
}
