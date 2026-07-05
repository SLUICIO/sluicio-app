#!/usr/bin/env bash
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# release.sh — cut a versioned release in one step.
#
# The build version itself is ALREADY automatic: publish.docker.sh stamps
# every image from scripts/version.sh (git describe --tags). You never
# have to "set a version" by hand. This script wraps the release
# ceremony so the git tag and the internal CHANGELOG.md stay in lockstep.
#
#   scripts/release.sh v0.2.0     # refresh changelog, commit it, tag v0.2.0
#   scripts/release.sh            # just refresh + commit the changelog (no new tag)
#
# Flow (with a version): regenerate CHANGELOG.md → commit it → tag the
# commit, so the tagged HEAD is clean and `./publish.docker.sh` stamps
# the images exactly v0.2.0. (The committed changelog lists the new
# commits under "Unreleased"; the next regenerate relabels them under the
# tag — the live CHANGELOG.md is always correct.)
#
# After it finishes:
#   git push && git push --tags        # publish the tag + changelog commit
#   ./publish.docker.sh                # build + push the versioned images
#
set -euo pipefail
cd "$(dirname "$0")/.."

VER="${1:-}"

if [ -n "$VER" ]; then
  case "$VER" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *) echo "error: version must look like vMAJOR.MINOR.PATCH (e.g. v0.2.0)" >&2; exit 1 ;;
  esac
  if git rev-parse -q --verify "refs/tags/$VER" >/dev/null; then
    echo "error: tag $VER already exists" >&2; exit 1
  fi
fi

# Start clean so the only thing we commit is the changelog refresh.
if [ -n "$(git status --porcelain)" ]; then
  echo "error: working tree is not clean — commit or stash your changes first" >&2
  exit 1
fi

./scripts/changelog.sh
if [ -n "$(git status --porcelain CHANGELOG.md)" ]; then
  git add CHANGELOG.md
  git commit -q -m "${VER:+release $VER — }refresh internal changelog"
  echo "==> committed changelog refresh"
else
  echo "==> changelog already up to date"
fi

if [ -n "$VER" ]; then
  git tag -a "$VER" -m "Release $VER"
  echo "==> tagged $VER (HEAD is clean — images will stamp exactly $VER)"
fi

echo
echo "next:"
echo "  git push && git push --tags"
echo "  ./publish.docker.sh"
