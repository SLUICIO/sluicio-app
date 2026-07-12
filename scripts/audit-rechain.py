#!/usr/bin/env python3
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
"""Repair an audit_log hash chain broken by the pre-v0.11.12 canonicalization bug.

Audit entries whose metadata embedded a struct (the config-import report:
action `config.imported`) were hashed at write time from the struct's
field-declaration order, while Verify re-derives the payload from JSONB
with sorted keys — so verification reported a false "content hash
mismatch" at that entry forever after. v0.11.12 fixes the write side;
this script repairs chains already written by older builds.

It re-walks every org's chain exactly like Verify (seeded from the
pruning anchor, skipping the legacy unhashed prefix), recomputes each
entry's hash from the stored row using the canonical sorted-key JSON,
and rewrites entry_hash/prev_hash where they differ — one transaction.

Dry run by default; pass --apply to write.

Environment (defaults fit the dev/demo compose):
  SLUICIO_RUNTIME       podman | docker      (auto-detected if unset)
  SLUICIO_PG_CONTAINER  Postgres container name
  SLUICIO_PG_USER       database user  (default: controlplane)
  SLUICIO_PG_DB         database name  (default: controlplane)

Only run this against a cell you operate, after confirming the mismatch
is this bug (verify fails at a `config.imported` entry): rewriting audit
hashes is exactly what the chain exists to detect, so treat this script
as an incident tool, not routine maintenance.
"""
import hashlib
import json
import os
import shutil
import subprocess
import sys


def runtime() -> str:
    rt = os.environ.get("SLUICIO_RUNTIME")
    if rt:
        return rt
    for cand in ("podman", "docker"):
        if shutil.which(cand):
            return cand
    sys.exit("neither podman nor docker found; set SLUICIO_RUNTIME")


def pg_container(rt: str) -> str:
    if os.environ.get("SLUICIO_PG_CONTAINER"):
        return os.environ["SLUICIO_PG_CONTAINER"]
    out = subprocess.run([rt, "ps", "--format", "{{.Names}}"], capture_output=True, text=True)
    names = [n for n in out.stdout.splitlines() if "postgres" in n]
    if len(names) != 1:
        sys.exit(f"could not pick a Postgres container from {names or 'none'}; set SLUICIO_PG_CONTAINER")
    return names[0]


RT = runtime()
PSQL = [
    RT, "exec", "-i", pg_container(RT),
    "psql", "-U", os.environ.get("SLUICIO_PG_USER", "controlplane"),
    "-d", os.environ.get("SLUICIO_PG_DB", "controlplane"),
]


def psql(sql: str) -> str:
    out = subprocess.run(PSQL + ["-Atc", sql], capture_output=True, text=True)
    if out.returncode != 0:
        raise RuntimeError(out.stderr)
    return out.stdout


def go_json(obj) -> str:
    """json.Marshal-compatible: sorted keys, no spaces, HTML escaping."""
    s = json.dumps(obj, separators=(",", ":"), sort_keys=True, ensure_ascii=False)
    # Go's encoder escapes these inside strings; in serialized JSON they
    # can only occur inside strings, so a global replace is equivalent.
    return (
        s.replace("&", "\\u0026").replace("<", "\\u003c").replace(">", "\\u003e")
        .replace(" ", "\\u2028").replace(" ", "\\u2029")
    )


def rfc3339nano(ts_us: str) -> str:
    """'YYYY-MM-DDTHH:MM:SS.UUUUUU' (UTC) → Go time.RFC3339Nano."""
    base, frac = ts_us.split(".")
    frac = frac.rstrip("0")
    return f"{base}.{frac}Z" if frac else f"{base}Z"


def chain_hash(parts) -> str:
    h = hashlib.sha256()
    for p in parts:
        h.update(p.encode("utf-8"))
        h.update(b"\x1f")  # unit separator, matches ee/audit chainHash
    return h.hexdigest()


def main():
    dry = "--apply" not in sys.argv
    orgs = [o for o in psql("SELECT DISTINCT organization_id FROM audit_log ORDER BY 1;").splitlines() if o]
    updates = []
    for org in orgs:
        prev = psql(f"SELECT last_hash FROM audit_chain_anchor WHERE organization_id = '{org}';").strip()
        rows_raw = psql(
            "SELECT json_build_object("
            "'id', id, 'actor', COALESCE(actor_user_id::text, ''), 'name', actor_name, "
            "'email', actor_email, 'action', action, 'ttype', resource_type, "
            "'tid', COALESCE(resource_id, ''), 'payload', payload, 'ip', ip, "
            "'at', to_char(occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US'), "
            "'eh', entry_hash) "
            f"FROM audit_log WHERE organization_id = '{org}' ORDER BY id ASC;"
        )
        changed = 0
        for line in rows_raw.splitlines():
            if not line:
                continue
            r = json.loads(line)
            if r["eh"] == "":
                prev = ""  # legacy unhashed prefix restarts the chain
                continue
            payload = r["payload"] if isinstance(r["payload"], dict) else {}
            canonical = go_json(payload) if payload else "{}"
            want = chain_hash([
                prev, org, r["actor"], r["name"], r["email"], r["action"],
                r["ttype"], r["tid"], canonical, r["ip"], rfc3339nano(r["at"]),
            ])
            if want != r["eh"]:
                updates.append(
                    f"UPDATE audit_log SET entry_hash = '{want}', prev_hash = '{prev}' WHERE id = {r['id']};"
                )
                changed += 1
            prev = want
        print(f"org {org}: {changed} row(s) need re-chaining")
    if not updates:
        print("chain already consistent — nothing to do")
        return
    if dry:
        print(f"DRY RUN: {len(updates)} update(s) pending — re-run with --apply to write")
        return
    script = "BEGIN;\n" + "\n".join(updates) + "\nCOMMIT;\n"
    out = subprocess.run(PSQL + ["-v", "ON_ERROR_STOP=1"], input=script, capture_output=True, text=True)
    if out.returncode != 0:
        raise RuntimeError(out.stderr)
    print(f"applied {len(updates)} update(s)")


if __name__ == "__main__":
    main()
