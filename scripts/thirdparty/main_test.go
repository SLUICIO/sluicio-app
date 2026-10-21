// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"strings"
	"testing"
)

const mitText = `MIT License

Copyright (c) 2020 Someone

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`

func TestTheCheckAllowsPermissiveAndRefusesTheRest(t *testing.T) {
	mit := identify([]File{{Name: "LICENSE", Text: mitText}})
	if len(mit) != 1 || mit[0] != "MIT" {
		t.Fatalf("MIT text identified as %v", mit)
	}
	rep := Report{Components: []Component{
		{Name: "ok", Version: "v1", Licenses: mit},
		{Name: "copyleft", Version: "v2", Licenses: []string{"GPL-3.0"}},
		{Name: "unknown", Version: "v3"},
	}}
	bad := violations(rep)
	if len(bad) != 2 {
		t.Fatalf("want the GPL and the unidentified component refused, got %v", bad)
	}
	if !strings.Contains(bad[0], "GPL-3.0") || !strings.Contains(bad[1], "no licence identified") {
		t.Errorf("unexpected violations: %v", bad)
	}
}

func TestTheNoticesCarryTheTextsVerbatim(t *testing.T) {
	out := text(Report{Product: "P", Components: []Component{{Name: "m", Version: "v1", Licenses: []string{"MIT"}, Files: []File{{Name: "LICENSE", Text: mitText}}}}})
	if !strings.Contains(out, "m v1  (MIT)") || !strings.Contains(out, "Permission is hereby granted") {
		t.Errorf("notices are missing the component line or the licence text:\n%s", out[:200])
	}
}
