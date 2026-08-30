<!-- SPDX-License-Identifier: Apache-2.0 -->

# Webhook body templates

A webhook notification channel posts Sluicio's canonical JSON by default. Some receivers dictate their own shape instead: a transactional email API wants `from` / `recipients` / `content`, a ticket system wants its own field names. Setting the channel's **Payload format** to **Custom body** (`config.format: "template"`) lets you write that shape yourself, in `config.body_template`.

## The template is JSON, not text

The template is a JSON **object**, and references appear inside it as values:

```json
{
  "from": { "email": "alerts@example.com" },
  "recipients": [{ "email": "oncall@example.com" }],
  "content": {
    "subject": "[${alert.severity}] ${rule.name}",
    "text_body": "${alert.summary}\n\n${alert.link}"
  },
  "retries": 3
}
```

Two forms, and the difference matters:

- A string that is **exactly** one reference - `"$check.value"` - is replaced by the value **with its type**. A number stays a number, a list stays a list, an absent value becomes `null`.
- A string that **contains** references - `"[${alert.severity}] ${rule.name}"` - is built as text. Absent values render as an empty string.

Both spellings work in both positions (`$alert.summary` and `${alert.summary}` are the same reference); the braces are only needed when a reference is followed by a character that could continue the name.

Object **keys** are never substituted, and literals you write are passed through untouched, so a receiver's required constants (`"retries": 3`) stay exactly as typed.

## Why not a text template

Because a text template that produces JSON breaks on the first alert whose summary contains a quote or a newline. The receiver answers `400`, and you find out when an alert does not arrive. Building the body structurally means values are encoded by the JSON serialiser, and any string can appear in any field.

## Variables

The available paths are the same ones the email and Slack templates use - `alert.*`, `rule.*`, `check.*`, `service.*`, `integration.*`, `org.*` - and the editor lists them with sample values beside the field. `GET /api/v1/alerting/template-context-schema` returns the same list. The paths are an additive-only contract: existing ones keep working.

A path that does not exist is rejected when the channel is saved, with the name of the offending reference. This is deliberate: the alternative is a body that delivers `null` forever and looks fine.

## Preview

The editor renders the template against a representative firing and shows the exact payload, using the same renderer delivery uses. **Send test** on the channel posts a real request through the same path, so the receiver's own validation gets a say before an alert depends on it.

## Limits and behaviour

- The template must parse as JSON with an object at the root, and is capped at 32 KB.
- A template that fails to render at delivery time falls back to the canonical payload rather than dropping the notification.
- Signing (`config.secret`) covers the rendered body, exactly as it covers the default one - see [webhook signing](webhook-signing.md).
