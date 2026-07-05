#!/usr/bin/env bash
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# changelog.sh — regenerate CHANGELOG.md from git history. INTERNAL only:
# this file lives in the repo and is never shipped in Sluicio's UI.
#
# Sections are cut at version tags (vMAJOR.MINOR.PATCH), newest first.
# Commits made since the latest tag appear under "Unreleased". Each entry
# is a commit subject + its short SHA. Merge commits are skipped.
#
#   scripts/changelog.sh            # rewrite CHANGELOG.md
#   scripts/changelog.sh --check    # fail if CHANGELOG.md is out of date
#
# Tag a release first (git tag v0.1.0) to start a new dated section.

set -euo pipefail
cd "$(dirname "$0")/.."

OUT="CHANGELOG.md"
LOG_FMT="- %s (%h)"
LOG_OPTS=(--no-merges --pretty="$LOG_FMT")

# redact scrubs internal-only infrastructure from commit subjects so the
# (committed, now-public) changelog never leaks it: the personal registry
# hostname and the LAN IP. The commit messages remain the source of truth;
# this only cleans what gets rendered into CHANGELOG.md. Kept portable — no
# \b (BSD sed rejects it); the matched prefixes don't occur in version strings.
redact() {
  sed -E \
    -e 's#([A-Za-z0-9-]+\.)*dickbauch\.io(:[0-9]+)?#internal-registry#g' \
    -e 's#192\.168(\.[0-9]{1,3}){2}(:[0-9]+)?#internal-host#g' \
    -e 's#172\.(1[6-9]|2[0-9]|3[01])(\.[0-9]{1,3}){2}(:[0-9]+)?#internal-host#g'
}

render() {
  echo "# Changelog"
  echo
  echo "_Generated from git history by \`scripts/changelog.sh\` — do not edit by hand._"
  echo "_Internal: not shown anywhere in the Sluicio product._"
  echo

  # Version tags, newest first. --sort=-v:refname orders SemVer correctly.
  local tags
  tags="$(git tag --list 'v*' --sort=-v:refname)"

  local newest=""
  [ -n "$tags" ] && newest="$(printf '%s\n' "$tags" | head -1)"

  # Unreleased: commits after the newest tag (or all commits if untagged).
  local range
  if [ -n "$newest" ]; then range="${newest}..HEAD"; else range="HEAD"; fi
  if [ -n "$(git log "$range" --no-merges --oneline)" ]; then
    echo "## Unreleased"
    echo
    git log "$range" "${LOG_OPTS[@]}"
    echo
  fi

  # One section per tag, each covering the commits since the previous tag.
  [ -z "$tags" ] && return 0
  local prev=""
  while IFS= read -r tag; do
    [ -z "$tag" ] && continue
    local date span
    date="$(git log -1 --format=%ad --date=short "$tag")"
    echo "## ${tag} — ${date}"
    echo
    # Previous (older) tag is the next line; compute the commit span.
    prev="$(printf '%s\n' "$tags" | grep -A1 -x "$tag" | tail -1 || true)"
    if [ -n "$prev" ] && [ "$prev" != "$tag" ]; then
      span="${prev}..${tag}"
    else
      span="$tag"
    fi
    git log "$span" "${LOG_OPTS[@]}"
    echo
  done <<< "$tags"
}

render_all() {
  render
  # Pre-public history: the public repo starts from a fresh root commit,
  # so sections for the old tags can't be regenerated from git. They're
  # preserved verbatim in CHANGELOG.archive.md and appended here.
  if [ -f CHANGELOG.archive.md ]; then
    # Skip the archive's own H1 + preamble up to the first section.
    awk 'f || /^## /{f=1} f' CHANGELOG.archive.md
  fi
}

if [ "${1:-}" = "--check" ]; then
  if ! diff -q <(render_all | redact) "$OUT" >/dev/null 2>&1; then
    echo "CHANGELOG.md is out of date — run scripts/changelog.sh" >&2
    exit 1
  fi
  echo "CHANGELOG.md is up to date."
  exit 0
fi

render_all | redact > "$OUT"
echo "wrote $OUT ($(grep -c '^- ' "$OUT") entries)"
