// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// httpStatusAttrKeys are the semantic-convention attribute keys carrying
// an HTTP response status on a span: the current key first, then the
// pre-1.21 legacy one still emitted by older instrumentations.
var httpStatusAttrKeys = [...]string{"http.response.status_code", "http.status_code"}

// MappedStatusAttrKey is stamped onto spans whose status this cell
// normalized, so the original instrumentation's intent stays visible.
const MappedStatusAttrKey = "sluicio.status_mapped"

// MapHTTP5xxToError normalizes span status toward the OTel semantic
// conventions: a span that carries an HTTP response status >= 500 but
// whose instrumentation left the span status Unset/Ok is stored as an
// Error span. Some emitters (KrakenD's gateway spans, for one) record
// the 5xx only as an attribute — without this, their failures are
// invisible to everything that keys on span status: service health,
// error counts, failed-trace alert rules. Returns how many spans were
// mapped. Spans already marked Error are never touched.
func MapHTTP5xxToError(rows []SpanRow) int {
	mapped := 0
	for i := range rows {
		if rows[i].StatusCode == "Error" {
			continue
		}
		code := 0
		for _, k := range httpStatusAttrKeys {
			if v, ok := rows[i].SpanAttributes[k]; ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					code = n
					break
				}
			}
		}
		if code < 500 {
			continue
		}
		rows[i].StatusCode = "Error"
		if rows[i].StatusMessage == "" {
			rows[i].StatusMessage = fmt.Sprintf("HTTP %d (span status mapped from http.response.status_code)", code)
		}
		if rows[i].SpanAttributes == nil {
			rows[i].SpanAttributes = map[string]string{}
		}
		rows[i].SpanAttributes[MappedStatusAttrKey] = "true"
		mapped++
	}
	return mapped
}

// CellFlags is a tiny TTL-cached reader for the cell_settings flags the
// ingest hot path consults per request. A miss or read error reads as
// "off" — ingest must never fail because a settings read did.
type CellFlags struct {
	pool *pgxpool.Pool
	ttl  time.Duration

	mu        sync.Mutex
	mapped5xx bool
	expiresAt time.Time
}

// mapHTTP5xxSettingKey is the cell_settings key backing the toggle;
// value shape {"enabled": bool}. Managed via the System settings API.
const mapHTTP5xxSettingKey = "ingest.map_http_5xx_to_error"

// NewCellFlags builds a flag cache over the given pool. ttl <= 0
// defaults to 30s (same cadence as the ingest key cache).
func NewCellFlags(pool *pgxpool.Pool, ttl time.Duration) *CellFlags {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &CellFlags{pool: pool, ttl: ttl}
}

// MapHTTP5xx reports whether 5xx→Error span normalization is enabled on
// this cell. Nil-safe (nil receiver = off).
func (f *CellFlags) MapHTTP5xx(ctx context.Context) bool {
	if f == nil || f.pool == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if now.Before(f.expiresAt) {
		return f.mapped5xx
	}
	var enabled bool
	err := f.pool.QueryRow(ctx, `
		SELECT COALESCE((value->>'enabled')::boolean, false)
		FROM cell_settings WHERE key = $1`, mapHTTP5xxSettingKey).Scan(&enabled)
	if err != nil {
		enabled = false // no row / read hiccup → off
	}
	f.mapped5xx = enabled
	f.expiresAt = now.Add(f.ttl)
	return enabled
}
