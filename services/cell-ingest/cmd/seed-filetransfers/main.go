// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Seeds the file-transfer story into a local cell: a week of nightly
// arrivals for several scheduled transfers, watched the way the
// OpenTelemetry Collector's filestats receiver watches a drop folder.
//
// It emits only what that receiver emits — file.mtime, file.count,
// file.size, per directory — so the screenshots show the same shape a
// real file server would produce, not a bespoke demo metric.
//
// One transfer is deliberately broken: its file never landed last night,
// so file.mtime stops advancing while the others keep stepping forward.
// That gap is the whole argument, and it is what the "expected file not
// present by deadline" check fires on.
//
// Local demo tooling; not part of the product.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
)

type transfer struct {
	service string
	slug    string
	dropAt  int   // hour of day the file is expected
	missing []int // nights ago with no delivery (0 = last night)
}

var transfers = []transfer{
	// Two missed nights, and the pair is deliberate. Night 0 is what
	// keeps the health check FIRING while a screenshot is taken: a check
	// that is red right now is, by definition, a file that is missing
	// right now, so the current failure can only ever sit at the right
	// edge of a chart. Night 4 gives the same chart a gap with delivered
	// files on both sides, which is what reads as "one night missing"
	// rather than "the data stops here".
	//
	// -missing overrides this pair. A story that says "one night failed"
	// wants -missing 0: a single drop to zero at the right edge, which
	// file.count survives better than file.mtime does (the count keeps
	// reporting, it just reports 0, so the line falls rather than stops).
	{"filetransfer-prices-retailer-x", "prices-retailer-x", 2, []int{0, 4}},
	{"filetransfer-orders-logistics", "orders-logistics", 1, nil},
	{"filetransfer-invoices-finance", "invoices-finance", 4, nil},
	{"filetransfer-stock-warehouse", "stock-warehouse", 3, nil},
}

func str(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

// parseNights turns "0,4" into []int{0, 4}. An empty string means the
// broken transfer delivered every night, which is how you seed a week
// with nothing wrong in it.
func parseNights(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []int
	for _, part := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%q is not a night count", part)
		}
		out = append(out, n)
	}
	return out, nil
}

func gauge(name, unit string, dps []*metricspb.NumberDataPoint) *metricspb.Metric {
	return &metricspb.Metric{Name: name, Unit: unit, Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: dps}}}
}

func main() {
	endpoint := flag.String("endpoint", "http://localhost:4318/v1/metrics", "cell-ingest OTLP metrics endpoint")
	days := flag.Int("days", 7, "how many days of history to emit")
	missed := flag.String("missing", "0,4", "nights ago the broken transfer delivered nothing (0 = last night)")
	flag.Parse()

	nights, err := parseNights(*missed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "-missing:", err)
		os.Exit(1)
	}
	transfers[0].missing = nights

	now := time.Now().UTC()
	req := &colmetricspb.ExportMetricsServiceRequest{}
	points := 0

	for _, t := range transfers {
		dir := "/srv/sftp/" + t.slug + "/out"
		missing := map[int]bool{}
		for _, m := range t.missing {
			missing[m] = true
		}

		var mtime, count, size []*metricspb.NumberDataPoint
		// One scrape an hour, which is what a filestats receiver on a
		// sensible interval produces over a week.
		// Down to h=0 so the newest point is NOW: an evaluation window
		// of an hour needs a sample inside it, and stopping an hour back
		// left every check with no data to judge.
		for h := *days * 24; h >= 0; h-- {
			ts := now.Add(-time.Duration(h) * time.Hour)
			nightsAgo := int(now.Sub(ts).Hours()) / 24

			// The newest file in the folder: today's drop once the hour
			// has passed, otherwise yesterday's. A missed night simply
			// never advances it — no error, no log, just an mtime that
			// stops moving. That is the case the post calls unloggable.
			drop := time.Date(ts.Year(), ts.Month(), ts.Day(), t.dropAt, 4, 0, 0, time.UTC)
			if ts.Before(drop) || missing[nightsAgo] {
				drop = drop.Add(-24 * time.Hour)
			}
			for missing[int(now.Sub(drop).Hours())/24] {
				drop = drop.Add(-24 * time.Hour)
			}

			attrs := []*commonpb.KeyValue{
				str("file.name", fmt.Sprintf("%s-%s.csv", t.slug, drop.Format("20060102"))),
				str("directory", dir),
			}
			at := uint64(ts.UnixNano())
			mtime = append(mtime, &metricspb.NumberDataPoint{
				TimeUnixNano: at, Attributes: attrs,
				Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(drop.Unix())}})
			present := 1.0
			if missing[nightsAgo] && !ts.Before(time.Date(ts.Year(), ts.Month(), ts.Day(), t.dropAt, 0, 0, 0, time.UTC)) {
				present = 0
			}
			count = append(count, &metricspb.NumberDataPoint{
				TimeUnixNano: at, Attributes: attrs,
				Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: present}})
			size = append(size, &metricspb.NumberDataPoint{
				TimeUnixNano: at, Attributes: attrs,
				Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 3_100_000 + float64((h%9)*11_000)}})
			points += 3
		}

		req.ResourceMetrics = append(req.ResourceMetrics, &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{str("service.name", t.service)}},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Scope: &commonpb.InstrumentationScope{Name: "otelcol/filestatsreceiver", Version: "0.1.0"},
				Metrics: []*metricspb.Metric{
					gauge("file.mtime", "s", mtime),
					gauge("file.count", "{files}", count),
					gauge("file.size", "By", size),
				},
			}},
		})
	}

	body, err := proto.Marshal(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal:", err)
		os.Exit(1)
	}
	r, err := http.Post(*endpoint, "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "post:", err)
		os.Exit(1)
	}
	defer r.Body.Close()
	if r.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "ingest returned %s\n", r.Status)
		os.Exit(1)
	}
	fmt.Printf("sent %d points for %d transfers to %s\n", points, len(transfers), *endpoint)
}
