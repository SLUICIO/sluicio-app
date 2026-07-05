// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// CodeEditor — thin React wrapper around @uiw/react-codemirror used for
// schema content authoring. Picks a CodeMirror language extension from
// the schema's format. Loaded lazily by SchemasPage so the bundle hit
// is paid only when someone actually opens the editor.

import { useState } from "react";
import CodeMirror, { type ReactCodeMirrorProps } from "@uiw/react-codemirror";
import { json } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import { xml } from "@codemirror/lang-xml";
import { EditorView } from "@codemirror/view";
import { load as yamlLoad, dump as yamlDump } from "js-yaml";

interface Props {
  value: string;
  onChange: (v: string) => void;
  format: string;
  // Pixel height (number) or any CSS height string — pass "100%" to fill
  // a flex/grid parent (the editor stretches and scrolls internally).
  height?: number | string;
  readOnly?: boolean;
}

// ── format-document helpers ─────────────────────────────────────────────

// canFormat reports whether the "Format document" button has a formatter
// for the given format. Other formats see a disabled button + tooltip.
function canFormat(format: string): boolean {
  const f = (format || "").toLowerCase();
  return [
    "json",
    "avro",
    "yaml",
    "yml",
    "openapi",
    "xml",
    "xslt",
    "html",
    "protobuf",
    "proto",
  ].includes(f);
}

// reformat parses + serialises the content with consistent indentation.
// Returns either { ok } with the new content or { error } with the
// parser's message — surfaced inline so the user can fix the source.
function reformat(content: string, format: string): { ok: string } | { error: string } {
  const f = (format || "").toLowerCase();
  if (f === "json" || f === "avro") {
    try {
      return { ok: JSON.stringify(JSON.parse(content), null, 2) };
    } catch (e) {
      return { error: `JSON: ${(e as Error).message}` };
    }
  }
  if (f === "yaml" || f === "yml" || f === "openapi") {
    try {
      const parsed = yamlLoad(content);
      const dumped = yamlDump(parsed, {
        indent: 2,
        lineWidth: 120,
        noRefs: true, // emit values inline rather than YAML refs
      });
      return { ok: dumped };
    } catch (e) {
      return { error: `YAML: ${(e as Error).message}` };
    }
  }
  if (f === "xml" || f === "xslt" || f === "html") {
    try {
      return { ok: formatXml(content) };
    } catch (e) {
      return { error: `XML: ${(e as Error).message}` };
    }
  }
  if (f === "protobuf" || f === "proto") {
    return { ok: formatProto(content) };
  }
  return { error: `Formatting not supported for "${format}".` };
}

// ── XML / XSLT / HTML pretty-printer ────────────────────────────────────
//
// Walks the parsed DOM tree and emits indented serialisation. We use
// the browser's DOMParser to validate up front — any malformed input
// surfaces via a <parsererror> node which we lift into a thrown Error.
// Doing a tree walk (rather than regex on a flat XMLSerializer string)
// keeps attribute escaping correct and handles comments / CDATA /
// processing instructions cleanly.

function formatXml(content: string): string {
  const parser = new DOMParser();
  const doc = parser.parseFromString(content, "application/xml");
  const errEl = doc.querySelector("parsererror");
  if (errEl) {
    // Firefox uses an <sourcetext> child + extra chrome; the textContent
    // is still the most useful signal for the user.
    throw new Error((errEl.textContent || "parse error").trim());
  }
  const parts: string[] = [];
  // Preserve the original XML declaration if present — DOM doesn't
  // expose it as a node, so we lift it verbatim.
  const declMatch = content.match(/^\s*<\?xml[^?]*\?>/);
  if (declMatch) parts.push(declMatch[0].trim());
  for (const child of Array.from(doc.childNodes)) {
    const rendered = renderXmlNode(child, 0);
    if (rendered) parts.push(rendered);
  }
  return parts.join("\n");
}

function renderXmlNode(node: Node, depth: number): string {
  const PAD = "  ";
  const indent = PAD.repeat(depth);
  switch (node.nodeType) {
    case Node.TEXT_NODE: {
      const text = (node.textContent ?? "").trim();
      return text ? indent + text : "";
    }
    case Node.COMMENT_NODE:
      return indent + `<!--${node.nodeValue ?? ""}-->`;
    case Node.CDATA_SECTION_NODE:
      return indent + `<![CDATA[${node.nodeValue ?? ""}]]>`;
    case Node.PROCESSING_INSTRUCTION_NODE: {
      const pi = node as ProcessingInstruction;
      return indent + `<?${pi.target} ${pi.data}?>`;
    }
    case Node.ELEMENT_NODE: {
      const el = node as Element;
      const attrs = Array.from(el.attributes)
        .map((a) => ` ${a.name}="${escapeAttr(a.value)}"`)
        .join("");
      const children = Array.from(el.childNodes).filter((c) => {
        // Drop pure-whitespace text nodes so we don't double-emit
        // indentation. Real text content (with non-space chars) is kept.
        if (c.nodeType === Node.TEXT_NODE) {
          return (c.textContent ?? "").trim().length > 0;
        }
        return true;
      });
      if (children.length === 0) {
        return indent + `<${el.tagName}${attrs}/>`;
      }
      // Inline form when the only child is a single text node — keeps
      // <xsl:text>hello</xsl:text> on one line rather than fanning out.
      if (children.length === 1 && children[0].nodeType === Node.TEXT_NODE) {
        const text = (children[0].textContent ?? "").trim();
        return indent + `<${el.tagName}${attrs}>${escapeText(text)}</${el.tagName}>`;
      }
      const inner = children
        .map((c) => renderXmlNode(c, depth + 1))
        .filter((s) => s.length > 0)
        .join("\n");
      return `${indent}<${el.tagName}${attrs}>\n${inner}\n${indent}</${el.tagName}>`;
    }
    default:
      return "";
  }
}

function escapeAttr(v: string): string {
  return v
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function escapeText(v: string): string {
  return v.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// ── Protobuf pretty-printer ─────────────────────────────────────────────
//
// .proto has a regular brace-block structure (message / service / enum
// / oneof / rpc body), so a brace-depth re-indenter gets us 95% of the
// way without needing a real protobuf parser. We re-tab each line to
// match the running depth and collapse runs of blank lines to one.

function formatProto(content: string): string {
  const PAD = "  ";
  const lines = content.split(/\r?\n/);
  let depth = 0;
  const out: string[] = [];
  let lastBlank = false;
  for (const raw of lines) {
    const trimmed = raw.trim();
    if (!trimmed) {
      // Collapse multiple blank lines to one; never start with a blank.
      if (!lastBlank && out.length > 0) out.push("");
      lastBlank = true;
      continue;
    }
    lastBlank = false;
    // Lines starting with `}` belong to the parent depth.
    const startsWithClose = trimmed.startsWith("}");
    const effectiveDepth = startsWithClose ? Math.max(0, depth - 1) : depth;
    out.push(PAD.repeat(effectiveDepth) + trimmed);
    // Update depth by net braces on this line — handles same-line
    // `} else {` style cases and single-line `enum Foo { A = 0; }`.
    const opens = (trimmed.match(/\{/g) ?? []).length;
    const closes = (trimmed.match(/\}/g) ?? []).length;
    depth = Math.max(0, depth + opens - closes);
  }
  // Drop any trailing blank line.
  while (out.length > 0 && out[out.length - 1] === "") out.pop();
  return out.join("\n") + "\n";
}

// Map our schema format strings onto CodeMirror language extensions.
// Anything we don't have an explicit extension for falls back to plain
// text (no highlighting, still editable).
function extensionsFor(format: string): ReactCodeMirrorProps["extensions"] {
  const f = (format || "").toLowerCase();
  switch (f) {
    case "json":
    case "avro": // Avro schemas are JSON in practice
      return [json()];
    case "yaml":
    case "yml":
    case "openapi": // most OpenAPI docs are YAML
      return [yaml()];
    case "xml":
    case "xslt":
    case "html":
      return [xml()];
    default:
      return [];
  }
}

export default function CodeEditor({
  value,
  onChange,
  format,
  height = 380,
  readOnly = false,
}: Props) {
  const [formatError, setFormatError] = useState<string | null>(null);
  const formattable = canFormat(format);
  // A string height ("100%", "60vh") means "fill my parent": the root
  // becomes a flex column so the CodeMirror pane stretches + scrolls.
  const fill = typeof height === "string";
  const cmHeight = typeof height === "number" ? `${height}px` : height;

  const onFormat = () => {
    if (!formattable) return;
    setFormatError(null);
    const result = reformat(value, format);
    if ("ok" in result) {
      // Only push through if it actually changed something — avoids a
      // redundant onChange (and the undo-history entry that comes with
      // it) when the document is already canonical.
      if (result.ok !== value) {
        onChange(result.ok);
      }
    } else {
      setFormatError(result.error);
    }
  };

  return (
    <div style={fill ? { height: "100%", display: "flex", flexDirection: "column", minHeight: 0 } : undefined}>
      {!readOnly && (
        <div
          className="flex items-center justify-between gap-2"
          style={{
            padding: "4px 8px",
            border: "1px solid var(--border)",
            borderTopLeftRadius: 6,
            borderTopRightRadius: 6,
            borderBottom: "none",
            background: "var(--surface-2)",
            fontSize: 12,
          }}
        >
          <span className="muted">{format || "text"}</span>
          <button
            type="button"
            className="btn btn--link"
            onClick={onFormat}
            disabled={!formattable}
            title={
              formattable
                ? "Reformat the document with canonical indentation (2 spaces)"
                : `No standard formatter for "${format}" — supported: JSON / Avro, YAML / OpenAPI, XML / XSLT / HTML, Protobuf`
            }
            style={{ padding: 0 }}
          >
            ⟲ Format document
          </button>
        </div>
      )}
      {formatError && !readOnly && (
        <div
          className="alert alert--error"
          style={{ marginTop: 0, borderTopLeftRadius: 0, borderTopRightRadius: 0 }}
        >
          {formatError}
        </div>
      )}
      <CodeMirror
        value={value}
        height={fill ? "100%" : cmHeight}
        extensions={[...(extensionsFor(format) ?? []), EditorView.lineWrapping]}
        onChange={onChange}
        readOnly={readOnly}
        basicSetup={{
          lineNumbers: true,
          highlightActiveLine: !readOnly,
          highlightActiveLineGutter: !readOnly,
          foldGutter: true,
          bracketMatching: true,
          closeBrackets: !readOnly,
          autocompletion: !readOnly,
          // Indentation that respects the language's bracket structure.
          indentOnInput: !readOnly,
        }}
        style={{
          border: "1px solid var(--border)",
          // Round only the bottom corners when the toolbar is rendered
          // above; the editor reads as one panel.
          borderTopLeftRadius: readOnly ? 6 : 0,
          borderTopRightRadius: readOnly ? 6 : 0,
          borderBottomLeftRadius: 6,
          borderBottomRightRadius: 6,
          fontSize: 12.5,
          background: "var(--surface)",
          // In fill mode, stretch within the flex-column root + scroll.
          ...(fill ? { flex: 1, minHeight: 0, overflow: "auto" } : {}),
        }}
      />
    </div>
  );
}
