// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// SyntaxView — Prism-backed read-only viewer for schema content. Maps
// the schema's declared `format` onto a Prism language and renders the
// highlighted markup. Falls back to plain monospace text when the
// format is unknown.

import { useMemo } from "react";

import Prism from "prismjs";
// Languages we actually use across the catalogue. Adding more is one
// import line each.
import "prismjs/components/prism-markup"; // xml / html / svg / xslt-as-xml
import "prismjs/components/prism-json";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-liquid";
import "prismjs/components/prism-protobuf";
import "prismjs/components/prism-bash"; // used by openapi-as-yaml diffs etc.
import "prismjs/themes/prism.css";

const FORMAT_TO_LANGUAGE: Record<string, string> = {
  json: "json",
  yaml: "yaml",
  yml: "yaml",
  xml: "markup",
  xslt: "markup",
  html: "markup",
  liquid: "liquid",
  protobuf: "protobuf",
  proto: "protobuf",
  avro: "json", // Avro schemas are JSON in practice
  openapi: "yaml", // most teams write OpenAPI in YAML
  text: "none",
  other: "none",
};

export function formatToLanguage(format: string | undefined | null): string {
  if (!format) return "none";
  return FORMAT_TO_LANGUAGE[format.toLowerCase()] ?? "none";
}

interface Props {
  content: string;
  format: string;
  // When set, lines longer than the panel scroll horizontally instead
  // of wrapping. Defaults to true — code wants to keep its shape.
  noWrap?: boolean;
  // Optional max height; defaults to 360px.
  maxHeight?: number;
}

export default function SyntaxView({ content, format, noWrap = true, maxHeight = 360 }: Props) {
  const lang = formatToLanguage(format);
  const html = useMemo(() => {
    if (lang === "none" || !Prism.languages[lang]) {
      return escapeHtml(content);
    }
    try {
      return Prism.highlight(content, Prism.languages[lang], lang);
    } catch {
      return escapeHtml(content);
    }
  }, [content, lang]);

  return (
    <pre
      className={`language-${lang}`}
      style={{
        margin: 0,
        padding: 12,
        fontSize: 12.5,
        lineHeight: 1.5,
        background: "var(--surface-3)",
        border: "1px solid var(--border)",
        borderRadius: 6,
        maxHeight,
        overflow: "auto",
        whiteSpace: noWrap ? "pre" : "pre-wrap",
        wordBreak: noWrap ? "normal" : "break-word",
      }}
    >
      <code
        className={`language-${lang}`}
        // Prism's output is safe to inject — it escapes the source before
        // wrapping tokens in spans.
        dangerouslySetInnerHTML={{ __html: html }}
      />
    </pre>
  );
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

// SyntaxStatus is a tiny indicator that lets pages confirm Prism loaded
// — useful when a format the user picked isn't actually supported.
export function isSupportedFormat(format: string | undefined | null): boolean {
  const lang = formatToLanguage(format);
  return lang !== "none" && !!Prism.languages[lang];
}

