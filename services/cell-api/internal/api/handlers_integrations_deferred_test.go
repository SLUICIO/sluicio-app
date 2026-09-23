// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The list splits in two: names and members from Postgres, numbers from
// telemetry. The split is only safe while the two halves say the same
// thing, and while the half that has not arrived says nothing at all
// rather than zero.

package api

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tags"
)

func TestDeferredRowsOmitTheNumbers(t *testing.T) {
	id := uuid.New()
	rows := deferredRows([]IntegrationSummary{{
		Integration:    integrations.Integration{ID: id, Name: "ERP Adapter", Slug: "erp-adapter"},
		Status:         "quiet",
		ServiceCount:   2,
		Services:       []string{"a", "b"},
		TraceCount:     0,
		Tags:           []tags.Tag{},
		MetadataValues: map[string]string{"owner": "integration team"},
	}})
	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var back []map[string]any
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatal(err)
	}
	row := back[0]

	// A count of 0 on a row whose count has not been read is a claim
	// that nothing came through, and it is not true yet.
	for _, k := range []string{"status", "trace_count", "error_trace_count", "delayed_trace_count", "unhealthy_count"} {
		if _, present := row[k]; present {
			t.Errorf("%s travelled with a row whose numbers are not in yet: %v", k, row[k])
		}
	}
	// What Postgres knows does travel: the reader came for these.
	for _, k := range []string{"id", "name", "slug", "services", "service_count", "tags"} {
		if _, present := row[k]; !present {
			t.Errorf("missing %s: the fast half is what the page renders first", k)
		}
	}
	if row["name"] != "ERP Adapter" {
		t.Errorf("name: got %v", row["name"])
	}
}

// The batch cap is the reason the split works: an unbounded ids list
// would put the whole slow query back into one request.
func TestStatsBatchIsCapped(t *testing.T) {
	if maxStatsIDs <= 0 || maxStatsIDs > 50 {
		t.Errorf("a cap of %d is either no cap or no batching", maxStatsIDs)
	}
}

// Tags are a list in the client, and a null reads as an error rather
// than as "none".
func TestDeferredRowsCarryAnEmptyTagListNotNull(t *testing.T) {
	rows := deferredRows([]IntegrationSummary{{
		Integration: integrations.Integration{ID: uuid.New(), Name: "x"},
		Tags:        integTagsFor(map[uuid.UUID][]tags.Tag{}, uuid.New()),
	}})
	body, _ := json.Marshal(rows)
	var back []map[string]any
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatal(err)
	}
	if back[0]["tags"] == nil {
		t.Error("tags came back null")
	}
}
