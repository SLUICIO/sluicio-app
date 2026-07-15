// SPDX-License-Identifier: Apache-2.0
//
// Minimal OTLP/HTTP protobuf encoder — just enough of
// ExportTraceServiceRequest to emit spans from a test (cell-ingest
// accepts protobuf only; JSON-encoded OTLP is deliberately rejected).
// Field numbers follow opentelemetry-proto trace/v1/trace.proto.
import crypto from "node:crypto";

function varint(n: bigint): number[] {
  const out: number[] = [];
  let v = n;
  for (;;) {
    const b = Number(v & 0x7fn);
    v >>= 7n;
    if (v === 0n) {
      out.push(b);
      return out;
    }
    out.push(b | 0x80);
  }
}

const tag = (field: number, wire: number): number[] => varint(BigInt((field << 3) | wire));
const lenDelim = (field: number, payload: Uint8Array): Uint8Array =>
  Buffer.concat([Buffer.from(tag(field, 2)), Buffer.from(varint(BigInt(payload.length))), payload]);
const str = (field: number, s: string): Uint8Array => lenDelim(field, Buffer.from(s, "utf8"));
const uvarintField = (field: number, n: bigint): Uint8Array =>
  Buffer.concat([Buffer.from(tag(field, 0)), Buffer.from(varint(n))]);
function fixed64(field: number, n: bigint): Uint8Array {
  const b = Buffer.alloc(8);
  b.writeBigUInt64LE(n);
  return Buffer.concat([Buffer.from(tag(field, 1)), b]);
}

// KeyValue{key=1, value=2:AnyValue{string_value=1 | int_value=3}}
function kvString(key: string, value: string): Uint8Array {
  return Buffer.concat([str(1, key), lenDelim(2, str(1, value))]);
}
function kvInt(key: string, value: bigint): Uint8Array {
  return Buffer.concat([str(1, key), lenDelim(2, uvarintField(3, value))]);
}

export interface SpanInput {
  name: string;
  error?: boolean;
  attrs?: Record<string, string | number>;
  /** epoch ms; defaults to now */
  atMs?: number;
}

// Builds an ExportTraceServiceRequest with one resource (service.name)
// carrying `spans`, each as its own root span/trace.
export function encodeTraceExport(service: string, spans: SpanInput[]): Buffer {
  // ResourceSpans.resource(1) → Resource{attributes(1): KeyValue} — two
  // nesting levels: the Resource message itself, then its field slot.
  const resource = lenDelim(1, lenDelim(1, kvString("service.name", service)));
  const encoded = spans.map((s) => {
    const at = BigInt(s.atMs ?? Date.now()) * 1_000_000n;
    const parts: Uint8Array[] = [
      lenDelim(1, crypto.randomBytes(16)), // trace_id
      lenDelim(2, crypto.randomBytes(8)), // span_id
      str(5, s.name),
      uvarintField(6, 2n), // kind = SERVER
      fixed64(7, at - 25_000_000n), // start
      fixed64(8, at), // end
    ];
    for (const [k, v] of Object.entries(s.attrs ?? {})) {
      parts.push(lenDelim(9, typeof v === "number" ? kvInt(k, BigInt(v)) : kvString(k, v)));
    }
    if (s.error) {
      // Status{message=2, code=3=STATUS_CODE_ERROR}
      parts.push(lenDelim(15, Buffer.concat([str(2, "e2e-induced failure"), uvarintField(3, 2n)])));
    }
    return lenDelim(2, Buffer.concat(parts)); // ScopeSpans.spans = 2
  });
  const scopeSpans = lenDelim(2, Buffer.concat([lenDelim(1, str(1, "e2e-otlp")), ...encoded]));
  const resourceSpans = lenDelim(1, Buffer.concat([resource, scopeSpans]));
  return Buffer.from(resourceSpans);
}
