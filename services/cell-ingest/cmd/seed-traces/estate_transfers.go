// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The file-transfer estate (-estate transfers): file moves from one
// system to another, the bread and butter of a managed file transfer
// (MFT) tool. Each transfer is its own service, so an integration can be
// put on any one of them, or on several together:
//
//	mft-erp-to-bank        ERP ──payment files──▶ bank
//	mft-bank-to-erp        bank ──account statements──▶ ERP
//	mft-wms-to-carrier     warehouse ──shipping manifests──▶ carrier
//	mft-supplier-to-ap     supplier ──invoices──▶ accounts payable
//	mft-hr-to-payroll      HR ──salary files──▶ payroll provider
//	mft-erp-to-datalake    ERP ──nightly extracts──▶ data lake
//
// -transfers replaces the list with your own, "name=from>to" separated by
// commas (mft-crm-to-erp=crm>erp), each moved over SFTP.
//
// One trace per file: the transfer, the pickup at the source, the
// delivery at the target and, on success, the checksum check. The pickup
// and delivery carry io.* attributes, so the flow reads source to target.
// Failures are seeded where a file move really fails, each with an
// exception event: the source file is locked, the target refuses the
// connection or the upload, or the checksum after delivery does not match.

package main

import (
	"fmt"
	mrand "math/rand"
	"strings"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// transferEnd is one side of a file transfer.
type transferEnd struct {
	system   string // the system's name, as an operator says it: "ERP", "Bank"
	protocol string // io.system: sftp, ftps, smb, azure-blob
	location string // host and directory, without the file name
}

// fileTransfer is one A to B transfer, emitted as one service.
type fileTransfer struct {
	service  string
	from, to transferEnd
	// file is the file name for one transfer; %s is a running reference.
	file   string
	sizeKB [2]int // smallest, and the spread above it
}

var defaultTransfers = []fileTransfer{
	{
		service: "mft-erp-to-bank",
		from:    transferEnd{"ERP", "smb", `erp01.corp.example/export/payments`},
		to:      transferEnd{"Bank", "sftp", "sftp.bank.example.com/inbound/pain001"},
		file:    "PAIN001_%s.xml", sizeKB: [2]int{8, 120},
	},
	{
		service: "mft-bank-to-erp",
		from:    transferEnd{"Bank", "sftp", "sftp.bank.example.com/outbound/camt054"},
		to:      transferEnd{"ERP", "smb", `erp01.corp.example/import/statements`},
		file:    "CAMT054_%s.xml", sizeKB: [2]int{4, 60},
	},
	{
		service: "mft-wms-to-carrier",
		from:    transferEnd{"Warehouse", "sftp", "wms.corp.example/outbound/manifests"},
		to:      transferEnd{"Carrier", "ftps", "ftp.carrier.example.com/manifests"},
		file:    "MANIFEST_%s.csv", sizeKB: [2]int{2, 40},
	},
	{
		service: "mft-supplier-to-ap",
		from:    transferEnd{"Supplier", "sftp", "sftp.supplier.example.com/outbound/invoices"},
		to:      transferEnd{"Accounts payable", "azure-blob", "apinvoices/incoming"},
		file:    "INV_%s.pdf", sizeKB: [2]int{40, 400},
	},
	{
		service: "mft-hr-to-payroll",
		from:    transferEnd{"HR", "smb", `hr01.corp.example/payroll/out`},
		to:      transferEnd{"Payroll provider", "sftp", "sftp.payroll.example.com/in"},
		file:    "SALARY_%s.txt", sizeKB: [2]int{20, 80},
	},
	{
		service: "mft-erp-to-datalake",
		from:    transferEnd{"ERP", "smb", `erp01.corp.example/export/nightly`},
		to:      transferEnd{"Data lake", "azure-blob", "datalake/raw/erp"},
		file:    "ERP_EXTRACT_%s.parquet", sizeKB: [2]int{20000, 180000},
	},
}

// parseTransfers reads -transfers: "name=from>to", comma separated.
func parseTransfers(spec string) ([]fileTransfer, error) {
	var out []fileTransfer
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, route, ok := strings.Cut(part, "=")
		from, to, ok2 := strings.Cut(route, ">")
		name, from, to = strings.TrimSpace(name), strings.TrimSpace(from), strings.TrimSpace(to)
		if !ok || !ok2 || name == "" || from == "" || to == "" {
			return nil, fmt.Errorf("%q is not name=from>to", part)
		}
		host := func(s string) string { return "sftp." + strings.ToLower(strings.ReplaceAll(s, " ", "-")) + ".example" }
		out = append(out, fileTransfer{
			service: name,
			from:    transferEnd{from, "sftp", host(from) + "/outbound"},
			to:      transferEnd{to, "sftp", host(to) + "/inbound"},
			file:    strings.ToUpper(strings.ReplaceAll(from, " ", "_")) + "_TO_" + strings.ToUpper(strings.ReplaceAll(to, " ", "_")) + "_%s.dat",
			sizeKB:  [2]int{4, 200},
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no transfers in %q", spec)
	}
	return out, nil
}

// useTransfersEstate switches the seeder to file transfers: one service
// and one scenario per transfer, and no standalone spans.
func useTransfersEstate(list []fileTransfer) {
	demoServices = nil
	demoScenarios = nil
	for _, ft := range list {
		demoServices = append(demoServices, serviceSpec{name: ft.service, namespace: "production", environment: "production"})
		demoScenarios = append(demoScenarios, ft.scenario)
	}
	standaloneSpans = false
}

// Failure rates per file. Low enough that a transfer is mostly green,
// high enough that every one of them fails now and then within an hour.
const (
	transferSourceLocked = 0.02
	transferTargetDown   = 0.03
	transferChecksum     = 0.01
)

// scenario emits one file moved from ft.from to ft.to.
func (ft fileTransfer) scenario(rng *mrand.Rand) []serviceSpans {
	traceID := randomID(16)
	ref := time.Now().UTC().Format("20060102") + fmt.Sprintf("_%05d", rng.Intn(99999))
	file := fmt.Sprintf(ft.file, ref)
	size := int64(ft.sizeKB[0]+rng.Intn(ft.sizeKB[1]+1)) * 1024
	// Roughly 2 MB/s, so a data-lake extract takes a while and a manifest
	// is instant: the latency view then has something to say.
	moveTime := time.Duration(size/2048)*time.Millisecond + ms(rng, 40, 200)

	common := []*commonpb.KeyValue{
		stringAttr("file.name", file),
		intAttr("file.size", size),
		stringAttr("transfer.source", ft.from.system),
		stringAttr("transfer.target", ft.to.system),
	}
	attrs := func(extra ...*commonpb.KeyValue) []*commonpb.KeyValue {
		return append(append([]*commonpb.KeyValue{}, common...), extra...)
	}

	rootID, pickupID, deliverID := randomID(8), randomID(8), randomID(8)
	end := time.Now().UTC()
	start := end.Add(-(moveTime + ms(rng, 150, 300)))
	pickupStart := start.Add(ms(rng, 5, 20))
	pickupEnd := pickupStart.Add(moveTime / 2)
	deliverStart := pickupEnd.Add(ms(rng, 5, 20))
	deliverEnd := deliverStart.Add(moveTime / 2)
	verifyStart := deliverEnd.Add(ms(rng, 2, 10))
	verifyEnd := verifyStart.Add(ms(rng, 5, 40))

	root := spanAt(traceID, rootID, nil, "transfer "+ft.from.system+" to "+ft.to.system, tracepb.Span_SPAN_KIND_INTERNAL, start, end, attrs())
	pickup := spanAt(traceID, pickupID, rootID, "pickup "+file, tracepb.Span_SPAN_KIND_CLIENT, pickupStart, pickupEnd,
		attrs(ioAttrs("input", "file", ft.from.protocol, ft.from.location+"/"+file)...))
	out := serviceSpans{serviceName: ft.service, serviceNamespace: "production", environment: "production", spans: []*tracepb.Span{root, pickup}}
	failRoot := func(msg string) { root.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: msg} }

	roll := rng.Float64()
	if roll < transferSourceLocked {
		msg := fmt.Sprintf("Could not read %s at %s: the file is locked by another process", file, ft.from.system)
		fail(pickup, pickupEnd, "SourceFileLockedException", msg,
			"SourceFileLockedException: "+msg+"\n   at Mft.Agents.SourceReader.Open(String path) in /src/SourceReader.cs:line 88\n   at Mft.Transfers.TransferJob.Run() in /src/TransferJob.cs:line 41")
		failRoot(msg)
		root.EndTimeUnixNano = pickup.EndTimeUnixNano
		return []serviceSpans{out}
	}

	deliver := spanAt(traceID, deliverID, rootID, "deliver "+file, tracepb.Span_SPAN_KIND_CLIENT, deliverStart, deliverEnd,
		attrs(ioAttrs("output", "file", ft.to.protocol, ft.to.location+"/"+file)...))
	out.spans = append(out.spans, deliver)
	if roll < transferSourceLocked+transferTargetDown {
		msg := fmt.Sprintf("Could not deliver %s to %s: connection to %s timed out after 30 s", file, ft.to.system, hostOf(ft.to.location))
		if rng.Intn(2) == 0 {
			msg = fmt.Sprintf("Could not deliver %s to %s: 550 permission denied on %s", file, ft.to.system, ft.to.location)
		}
		fail(deliver, deliverEnd, "TargetUnavailableException", msg,
			"TargetUnavailableException: "+msg+"\n   at Mft.Agents.TargetWriter.Upload(Stream file, String path) in /src/TargetWriter.cs:line 132\n   at Mft.Transfers.TransferJob.Run() in /src/TransferJob.cs:line 57")
		failRoot(msg)
		root.EndTimeUnixNano = deliver.EndTimeUnixNano
		return []serviceSpans{out}
	}

	verify := spanAt(traceID, randomID(8), rootID, "verify checksum", tracepb.Span_SPAN_KIND_INTERNAL, verifyStart, verifyEnd,
		attrs(stringAttr("checksum.algorithm", "SHA-256")))
	out.spans = append(out.spans, verify)
	if roll < transferSourceLocked+transferTargetDown+transferChecksum {
		msg := fmt.Sprintf("Checksum of %s at %s does not match the source: the file arrived incomplete", file, ft.to.system)
		fail(verify, verifyEnd, "ChecksumMismatchException", msg,
			"ChecksumMismatchException: "+msg+"\n   at Mft.Transfers.Integrity.Verify(String expected, String actual) in /src/Integrity.cs:line 23\n   at Mft.Transfers.TransferJob.Run() in /src/TransferJob.cs:line 66")
		failRoot(msg)
	}
	root.EndTimeUnixNano = uint64(verifyEnd.Add(ms(rng, 1, 5)).UnixNano())
	return []serviceSpans{out}
}

// hostOf is the host part of a location such as "sftp.bank.example.com/inbound".
func hostOf(location string) string {
	h, _, _ := strings.Cut(location, "/")
	return h
}
