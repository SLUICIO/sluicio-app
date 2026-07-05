// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Store writes converted span rows into ClickHouse.
type Store struct {
	conn driver.Conn
}

// NewStore returns a Store backed by the given ClickHouse connection.
func NewStore(conn driver.Conn) *Store {
	return &Store{conn: conn}
}

// InsertSpans appends the given rows in a single ClickHouse batch.
// The columns are listed explicitly so the order does not depend on
// the table's declaration order.
func (s *Store) InsertSpans(ctx context.Context, rows []SpanRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, `
		INSERT INTO traces (
			Timestamp,
			TraceId,
			SpanId,
			ParentSpanId,
			SpanName,
			SpanKind,
			ServiceName,
			ServiceNamespace,
			OrganizationId,
			ResourceAttributes,
			SpanAttributes,
			DurationNs,
			StatusCode,
			StatusMessage
		)
	`)
	if err != nil {
		return fmt.Errorf("prepare batch: %w", err)
	}

	for _, r := range rows {
		if err := batch.Append(
			r.Timestamp,
			r.TraceID,
			r.SpanID,
			r.ParentSpanID,
			r.SpanName,
			r.SpanKind,
			r.ServiceName,
			r.ServiceNamespace,
			r.OrganizationID,
			r.ResourceAttributes,
			r.SpanAttributes,
			r.DurationNs,
			r.StatusCode,
			r.StatusMessage,
		); err != nil {
			return fmt.Errorf("append row: %w", err)
		}
	}

	return batch.Send()
}

// InsertLogs appends the given log rows in a single ClickHouse batch.
func (s *Store) InsertLogs(ctx context.Context, rows []LogRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, `
		INSERT INTO logs (
			Timestamp,
			ObservedTimestamp,
			TraceId,
			SpanId,
			SeverityNumber,
			SeverityText,
			ServiceName,
			ServiceNamespace,
			OrganizationId,
			ScopeName,
			Body,
			ResourceAttributes,
			LogAttributes
		)
	`)
	if err != nil {
		return fmt.Errorf("prepare batch: %w", err)
	}

	for _, r := range rows {
		if err := batch.Append(
			r.Timestamp,
			r.ObservedTimestamp,
			r.TraceID,
			r.SpanID,
			r.SeverityNumber,
			r.SeverityText,
			r.ServiceName,
			r.ServiceNamespace,
			r.OrganizationID,
			r.ScopeName,
			r.Body,
			r.ResourceAttributes,
			r.LogAttributes,
		); err != nil {
			return fmt.Errorf("append row: %w", err)
		}
	}

	return batch.Send()
}

// InsertMetrics appends the given metric data-point rows in a single
// ClickHouse batch.
func (s *Store) InsertMetrics(ctx context.Context, rows []MetricRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, `
		INSERT INTO metrics (
			Timestamp,
			StartTimestamp,
			MetricName,
			MetricType,
			ServiceName,
			ServiceNamespace,
			OrganizationId,
			Value,
			Count,
			IsMonotonic,
			Unit,
			ResourceAttributes,
			MetricAttributes
		)
	`)
	if err != nil {
		return fmt.Errorf("prepare batch: %w", err)
	}

	for _, r := range rows {
		if err := batch.Append(
			r.Timestamp,
			r.StartTimestamp,
			r.MetricName,
			r.MetricType,
			r.ServiceName,
			r.ServiceNamespace,
			r.OrganizationID,
			r.Value,
			r.Count,
			r.IsMonotonic,
			r.Unit,
			r.ResourceAttributes,
			r.MetricAttributes,
		); err != nil {
			return fmt.Errorf("append row: %w", err)
		}
	}

	return batch.Send()
}
