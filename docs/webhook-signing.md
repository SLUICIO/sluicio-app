<!-- SPDX-License-Identifier: Apache-2.0 -->

# Webhook signing

Webhook notification channels can optionally sign every request so your
receiver can verify **authenticity** (this really came from your Sluicio
cell), **integrity** (the payload wasn't modified in transit), and
**freshness** (it isn't a replay of an old request).

Signing is off by default. Set a **Signing secret** on the webhook
channel (Alerts → Notification channels, or `config.secret` via the
API) to enable it. Generate a high-entropy secret, e.g.
`openssl rand -hex 32`.

## The scheme

Signed requests carry two extra headers:

```
X-Sluicio-Timestamp: 1783948210
X-Sluicio-Signature: sha256=6c2f…e41b
```

The signature is `HMAC-SHA256(secret, "<timestamp>.<raw body>")`,
hex-encoded, prefixed with `sha256=`. The timestamp is unix seconds and
is part of the signed content — that's what makes replays detectable.

To verify, a receiver:

1. Reads `X-Sluicio-Timestamp`; rejects if it differs from the current
   time by more than a tolerance you choose (5 minutes is typical).
2. Computes `HMAC-SHA256(secret, timestamp + "." + rawBody)` over the
   **raw request bytes** (before any JSON parsing — re-serialised JSON
   will not match).
3. Compares against `X-Sluicio-Signature` using a **constant-time**
   comparison.

The signature deliberately lives in custom headers rather than
`Authorization`: that slot belongs to your endpoint's own
authentication (many receivers require their own bearer token there),
and auth middleware/gateways commonly consume or reject unknown
`Authorization` schemes before your code runs.

## Verifying — Go

```go
func verify(r *http.Request, secret string, body []byte) error {
	ts := r.Header.Get("X-Sluicio-Timestamp")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || math.Abs(time.Since(time.Unix(sec, 0)).Seconds()) > 300 {
		return errors.New("stale or missing timestamp")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(r.Header.Get("X-Sluicio-Signature"))) {
		return errors.New("signature mismatch")
	}
	return nil
}
```

## Verifying — Node.js

```js
const crypto = require("node:crypto");

function verify(req, secret, rawBody) {
  const ts = req.headers["x-sluicio-timestamp"];
  if (!ts || Math.abs(Date.now() / 1000 - Number(ts)) > 300) {
    throw new Error("stale or missing timestamp");
  }
  const want =
    "sha256=" +
    crypto.createHmac("sha256", secret).update(`${ts}.`).update(rawBody).digest("hex");
  const got = req.headers["x-sluicio-signature"] ?? "";
  if (!crypto.timingSafeEqual(Buffer.from(want), Buffer.from(got))) {
    throw new Error("signature mismatch");
  }
}
```

(With Express, capture the raw body via `express.json({ verify: (req, _res, buf) => { req.rawBody = buf; } })`.)

## Payload formats

Signing is independent of the payload format: the HMAC always covers
the raw request body. Webhook channels default to Sluicio's canonical
JSON; setting the channel's **Payload format** to CloudEvents 1.0
(`config.format: "cloudevents"`) wraps the same payload in a
CNCF-standard envelope for CE-aware receivers — see
docs/outbound-events-design.md.

## Notes

- Slack and PagerDuty channels are not signed — their platforms carry
  authenticity in the webhook URL / routing key.
- Rotating the secret: update the channel; in-flight retries of already
  rendered deliveries sign with the config at send time, so run
  receivers dual-secret briefly during a rotation if you can't tolerate
  a few rejects.
- The audit-log forwarder (`SLUICIO_AUDIT_SINK_SECRET`) uses the same
  HMAC-SHA256 primitive but signs the body only, without a timestamp
  (header `X-Sluicio-Signature`, no timestamp header).
