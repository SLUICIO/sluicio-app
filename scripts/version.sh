#!/usr/bin/env bash
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# version.sh — the single source of truth for "what version is this
# build?". Every build path (publish.docker.sh, the Makefile, local
# `go build`) derives the embedded version from here so they never drift.
#
# Scheme: SemVer from git tags.
#   - Tag a release:           git tag v0.1.0   (then push: git push --tags)
#   - On a tagged commit:      v0.1.0
#   - 4 commits past v0.1.0:   v0.1.0-4-gab12cd3
#   - dirty working tree:      …-dirty
#   - no tags yet:             the short commit SHA (e.g. ab12cd3)
#
# Usage:
#   scripts/version.sh            # print the version string
#   scripts/version.sh --commit   # print the short commit SHA
#   scripts/version.sh --date     # print the RFC-3339 UTC build date
#
# An explicit VERSION env var overrides the git-derived value (useful
# for reproducible/CI builds): VERSION=v1.2.3 scripts/version.sh

set -euo pipefail
cd "$(dirname "$0")/.."

case "${1:-version}" in
  --commit) git rev-parse --short HEAD 2>/dev/null || echo unknown ;;
  --date)   date -u +%Y-%m-%dT%H:%M:%SZ ;;
  *)
    if [ -n "${VERSION:-}" ]; then
      printf '%s\n' "$VERSION"
    else
      git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev
    fi
    ;;
esac
