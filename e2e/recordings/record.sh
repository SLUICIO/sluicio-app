#!/usr/bin/env bash
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Records every walkthrough (or the ones whose file names match the
# arguments) and leaves an MP4 per take in e2e/recordings-output/.
#
#   recordings/record.sh                        all walkthroughs
#   recordings/record.sh integration-to-message one of them
#   RECORDING_CAPTIONS=1 recordings/record.sh   with on-screen captions
#   RECORDING_HEADED=1 recordings/record.sh     visible browser, for a
#                                               screen recorder of your own
set -euo pipefail

e2e="$(cd "$(dirname "$0")/.." && pwd)"
out="$e2e/recordings-output"
cd "$e2e"
rm -rf "$out/raw"

npx playwright test -c playwright.recording.config.ts "$@"

command -v ffmpeg >/dev/null || { echo "no ffmpeg: the raw takes are in $out/raw (WebM)"; exit 0; }

stamp="$(date +%Y%m%d-%H%M)"
suffix=""
[ "${RECORDING_CAPTIONS:-}" = "1" ] && suffix="-captions"
find "$out/raw" -name video.webm | while read -r webm; do
  take="$(basename "$(dirname "$webm")" | sed -E 's/\.rec-[0-9a-f]+-.*//')"
  mp4="$out/$take-$stamp$suffix.mp4"
  # H.264 in yuv420p plays everywhere, from a phone to a slide deck.
  ffmpeg -v error -y -i "$webm" -c:v libx264 -preset slow -crf 18 -pix_fmt yuv420p -movflags +faststart "$mp4"
  echo "wrote $mp4"
done
