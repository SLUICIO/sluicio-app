// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package messageviews

import (
	"fmt"
	"strings"
)

// SQL is the result of translating a UI filter list into the bits a
// ClickHouse query needs: clause fragments to AND inside the matching
// CTE, scalar hints for the surrounding driver (time preset,
// integration name to resolve), and the post-aggregation status
// filter. Service-name literals and the integration name are kept
// separate so the caller can resolve the integration to service names
// and intersect with the literal allowlist itself — the messageviews
// package has no Postgres / matcher dependency.
type SQL struct {
	// Clauses are ANDed inside the matching CTE.
	Clauses []string
	Args    []any
	// OnlyFailed and StatusOK control the post-aggregation filter on
	// the summary CTE. Exactly one may be true.
	OnlyFailed bool
	StatusOK   bool
	// ServiceNameLiterals lists the service names mentioned by
	// service filters; the caller intersects this with whatever
	// the integration filter resolves to.
	ServiceNameLiterals []string
	// TimePreset, when non-empty, is the relative-duration token
	// ("1h", "24h", etc.) the time filter requests. Empty means
	// the global window from the request URL stands.
	TimePreset string
	// IntegrationName, when non-empty, is the literal integration
	// name selected by the user (the FilterEditor stores names, not
	// IDs). The caller resolves this to a service-name allowlist.
	IntegrationName string
}

// Build translates the UI filter list into ClickHouse predicates and
// per-request hints (time range override, integration name, etc.).
//
// The translation is intentionally loose where the UI is loose: pickers
// like "err only" or "last 24 hours" produce friendly English strings,
// and Build accepts the patterns the picker can produce.
func Build(filters []Filter) (SQL, error) {
	out := SQL{}
	for _, f := range filters {
		// Optional rows are UI-only "reminders" — the user has muted
		// them but kept them visible. The search engine treats them
		// as a no-op.
		if f.Optional {
			continue
		}
		switch f.Field {
		case FieldTime:
			out.TimePreset = parseTimePreset(f.Value)

		case FieldStatus:
			switch normStatus(f.Value) {
			case "err":
				out.OnlyFailed = true
			case "ok":
				out.StatusOK = true
			case "warn-or-err":
				// We don't model "warn" yet; treat as error-only so
				// the user sees the strongest signal rather than
				// nothing.
				out.OnlyFailed = true
			case "any", "":
				// no filter
			default:
				// Unknown status string is non-fatal; leave open.
			}

		case FieldService:
			vals := splitList(f.Value)
			if len(vals) > 0 && !(len(vals) == 1 && strings.EqualFold(vals[0], "any")) {
				out.ServiceNameLiterals = append(out.ServiceNameLiterals, vals...)
			}

		case FieldIntegration:
			v := strings.TrimSpace(f.Value)
			if v != "" && !strings.EqualFold(v, "any") {
				out.IntegrationName = v
			}

		case FieldTraceID:
			// Ids are hex; compare case-insensitively so a pasted
			// upper/lower id still hits. `contains` matches a fragment —
			// handy when only part of an id survived a log line or a
			// truncated copy-paste.
			if v := strings.ToLower(strings.TrimSpace(f.Value)); v != "" {
				if f.Op == OpContains {
					out.Clauses = append(out.Clauses, "positionCaseInsensitive(TraceId, ?) > 0")
				} else {
					out.Clauses = append(out.Clauses, "lower(TraceId) = ?")
				}
				out.Args = append(out.Args, v)
			}

		case FieldSpanID:
			// Same semantics as trace id — the message is grouped by
			// trace, so a span match surfaces its containing trace.
			if v := strings.ToLower(strings.TrimSpace(f.Value)); v != "" {
				if f.Op == OpContains {
					out.Clauses = append(out.Clauses, "positionCaseInsensitive(SpanId, ?) > 0")
				} else {
					out.Clauses = append(out.Clauses, "lower(SpanId) = ?")
				}
				out.Args = append(out.Args, v)
			}

		case FieldErrorType:
			// Match against StatusMessage and the conventional
			// exception.type attribute on the span.
			c, args, err := errorTypeClause(f.Op, f.Value)
			if err != nil {
				return SQL{}, err
			}
			if c != "" {
				out.Clauses = append(out.Clauses, c)
				out.Args = append(out.Args, args...)
			}

		case FieldPayload:
			// An incomplete row — no attribute key picked yet. The
			// FilterEditor's freshly added row is exactly this shape;
			// treat it as a no-op (like Optional) instead of failing the
			// whole search.
			if strings.TrimSpace(f.FieldPath) == "" {
				continue
			}
			if !SafeAttributeKey(f.FieldPath) {
				return SQL{}, fmt.Errorf("invalid payload field path: %q", f.FieldPath)
			}
			c, args, err := payloadClause(f.FieldPath, f.Op, f.Value)
			if err != nil {
				return SQL{}, err
			}
			out.Clauses = append(out.Clauses, c)
			out.Args = append(out.Args, args...)
		}
	}
	return out, nil
}

// payloadClause builds a predicate that checks the named attribute key
// against the given value, looking at both SpanAttributes and
// ResourceAttributes so users don't have to know which one carried it
// (the OTel model treats both as "attributes on the span").
//
// The map key is interpolated as a literal because the ClickHouse Go
// driver can't bind a map index expression. We validate the key first.
func payloadClause(key string, op Operator, value string) (string, []any, error) {
	// Two lookups: span-level and resource-level. Either match wins.
	keyExpr := fmt.Sprintf("SpanAttributes['%s']", key)
	resExpr := fmt.Sprintf("ResourceAttributes['%s']", key)

	switch op {
	case OpEquals, OpIs:
		// IS is treated as exact-equal for payload — the UI's "is"
		// picker is only offered for closed-set fields, but accept
		// it for symmetry.
		clause := fmt.Sprintf("(%s = ? OR %s = ?)", keyExpr, resExpr)
		return clause, []any{value, value}, nil
	case OpContains:
		clause := fmt.Sprintf("(positionCaseInsensitive(%s, ?) > 0 OR positionCaseInsensitive(%s, ?) > 0)", keyExpr, resExpr)
		return clause, []any{value, value}, nil
	case OpMatches:
		// ClickHouse regex match — anchored if the user supplied
		// anchors. We sanity-cap the pattern length in Validate.
		clause := fmt.Sprintf("(match(%s, ?) OR match(%s, ?))", keyExpr, resExpr)
		return clause, []any{value, value}, nil
	case OpIn:
		vals := splitList(value)
		if len(vals) == 0 {
			return "1 = 1", nil, nil
		}
		placeholders := strings.Repeat("?,", len(vals))
		placeholders = placeholders[:len(placeholders)-1] // strip trailing comma
		clause := fmt.Sprintf("(%s IN (%s) OR %s IN (%s))", keyExpr, placeholders, resExpr, placeholders)
		args := make([]any, 0, len(vals)*2)
		for _, v := range vals {
			args = append(args, v)
		}
		for _, v := range vals {
			args = append(args, v)
		}
		return clause, args, nil
	}
	return "", nil, fmt.Errorf("unsupported operator %q for payload", op)
}

func errorTypeClause(op Operator, value string) (string, []any, error) {
	switch op {
	case OpEquals, OpIs:
		clause := "(StatusMessage = ? OR SpanAttributes['exception.type'] = ?)"
		return clause, []any{value, value}, nil
	case OpContains:
		clause := "(positionCaseInsensitive(StatusMessage, ?) > 0 OR positionCaseInsensitive(SpanAttributes['exception.type'], ?) > 0)"
		return clause, []any{value, value}, nil
	case OpMatches:
		clause := "(match(StatusMessage, ?) OR match(SpanAttributes['exception.type'], ?))"
		return clause, []any{value, value}, nil
	case OpIn:
		vals := splitList(value)
		if len(vals) == 0 {
			return "", nil, nil
		}
		placeholders := strings.Repeat("?,", len(vals))
		placeholders = placeholders[:len(placeholders)-1]
		clause := fmt.Sprintf("(StatusMessage IN (%s) OR SpanAttributes['exception.type'] IN (%s))", placeholders, placeholders)
		args := make([]any, 0, len(vals)*2)
		for _, v := range vals {
			args = append(args, v)
		}
		for _, v := range vals {
			args = append(args, v)
		}
		return clause, args, nil
	}
	return "", nil, fmt.Errorf("unsupported operator %q for errorType", op)
}

// parseTimePreset accepts the friendly strings the time picker
// produces ("last 15 minutes", "last 24 hours", …) and returns the
// equivalent Go duration token ("15m", "24h", …) that the existing
// ParseRange handles. Unknown values fall through as empty so the
// global window wins.
func parseTimePreset(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimPrefix(v, "last ")
	switch v {
	case "15 minutes", "15m":
		return "15m"
	case "1 hour", "1h":
		return "1h"
	case "24 hours", "24h", "1 day", "1d":
		return "24h"
	case "7 days", "7d", "1 week":
		return "168h"
	}
	return ""
}

func normStatus(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.HasPrefix(v, "err"):
		return "err"
	case strings.HasPrefix(v, "ok"):
		return "ok"
	case strings.Contains(v, "warn"):
		return "warn-or-err"
	case strings.HasPrefix(v, "any"):
		return "any"
	}
	return ""
}

// splitList parses a CSV-ish value into trimmed tokens. The FilterEditor
// pictures users typing "1323, 1419, 0991" for an `in` filter; this
// turns that into individual literals. Empty tokens are dropped.
func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
