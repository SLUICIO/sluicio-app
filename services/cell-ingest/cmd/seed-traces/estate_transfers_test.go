// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The transfers estate exists so integrations can be put on file moves,
// one service per transfer. Its shape is asserted: every trace is one
// service, picks up at the source and delivers at the target, and every
// failure it promises happens.

package main

import (
	mrand "math/rand"
	"strings"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func attr(attrs []*commonpb.KeyValue, key string) string {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value.GetStringValue()
		}
	}
	return ""
}

func TestTransfersEstateShape(t *testing.T) {
	services, scenarios, standalone := demoServices, demoScenarios, standaloneSpans
	t.Cleanup(func() { demoServices, demoScenarios, standaloneSpans = services, scenarios, standalone })
	useTransfersEstate(defaultTransfers)
	if len(demoScenarios) != len(defaultTransfers) || len(demoServices) != len(defaultTransfers) {
		t.Fatalf("one scenario and one service per transfer: %d, %d", len(demoScenarios), len(demoServices))
	}
	seen := map[string]bool{}
	for _, ft := range defaultTransfers {
		if seen[ft.service] {
			t.Fatalf("two transfers share the service %s", ft.service)
		}
		seen[ft.service] = true
	}

	rng := mrand.New(mrand.NewSource(1))
	failures := map[string]int{}
	for _, ft := range defaultTransfers {
		for i := 0; i < 1500; i++ {
			trace := ft.scenario(rng)
			if len(trace) != 1 || trace[0].serviceName != ft.service || trace[0].environment != "production" {
				t.Fatalf("%s: a file move is one service in production, got %+v", ft.service, trace)
			}
			var root, pickup, deliver *tracepb.Span
			failed := ""
			for _, sp := range trace[0].spans {
				switch {
				case len(sp.ParentSpanId) == 0:
					root = sp
				case attr(sp.Attributes, "io.role") == "input":
					pickup = sp
				case attr(sp.Attributes, "io.role") == "output":
					deliver = sp
				}
				for _, ev := range sp.Events {
					if ev.Name == "exception" {
						failed = attr(ev.Attributes, "exception.type")
					}
				}
			}
			if root == nil || pickup == nil {
				t.Fatalf("%s: no root or no pickup", ft.service)
			}
			if !strings.HasPrefix(attr(pickup.Attributes, "io.endpoint"), ft.from.location+"/") ||
				attr(pickup.Attributes, "io.system") != ft.from.protocol {
				t.Fatalf("%s: pickup from %q over %q", ft.service, attr(pickup.Attributes, "io.endpoint"), attr(pickup.Attributes, "io.system"))
			}
			if attr(root.Attributes, "transfer.source") != ft.from.system || attr(root.Attributes, "transfer.target") != ft.to.system {
				t.Fatalf("%s: root names %q to %q", ft.service, attr(root.Attributes, "transfer.source"), attr(root.Attributes, "transfer.target"))
			}
			if failed == "SourceFileLockedException" {
				if deliver != nil {
					t.Fatalf("%s: a file that could not be read was delivered", ft.service)
				}
			} else if deliver == nil || !strings.HasPrefix(attr(deliver.Attributes, "io.endpoint"), ft.to.location+"/") {
				t.Fatalf("%s: no delivery to %s", ft.service, ft.to.location)
			}
			if (failed != "") != (root.Status.GetCode() == tracepb.Status_STATUS_CODE_ERROR) {
				t.Fatalf("%s: the transfer's status does not follow its failure %q", ft.service, failed)
			}
			if failed != "" {
				failures[ft.service+"/"+failed]++
			}
		}
	}
	for _, ft := range defaultTransfers {
		for _, kind := range []string{"SourceFileLockedException", "TargetUnavailableException", "ChecksumMismatchException"} {
			if failures[ft.service+"/"+kind] == 0 {
				t.Errorf("%s never fails with %s", ft.service, kind)
			}
		}
	}
}

func TestParseTransfers(t *testing.T) {
	list, err := parseTransfers("mft-crm-to-erp=CRM>ERP, mft-a-to-b = Legacy app > New app")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].service != "mft-crm-to-erp" || list[0].from.system != "CRM" || list[0].to.system != "ERP" {
		t.Fatalf("parsed %+v", list)
	}
	if list[1].service != "mft-a-to-b" || list[1].from.system != "Legacy app" || list[1].to.location != "sftp.new-app.example/inbound" {
		t.Fatalf("parsed %+v", list[1])
	}
	for _, bad := range []string{"", "x", "x=a", "x=a>", "=a>b"} {
		if _, err := parseTransfers(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
