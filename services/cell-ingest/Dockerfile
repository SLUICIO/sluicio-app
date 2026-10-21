# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Multi-stage build for cell-ingest. Same shape as cell-api's
# Dockerfile — see that file for the rationale on workspace layout,
# CGO_ENABLED=0, distroless, etc.
#
#   docker build -f services/cell-ingest/Dockerfile -t sluicio/cell-ingest .

# Multi-arch: the build stage pins itself to the BUILDER's architecture
# and cross-compiles for the target, instead of letting buildx emulate a
# whole toolchain under QEMU. CGO is already off and the Go toolchain
# cross-compiles natively, so an arm64 image costs the same as an amd64
# one — emulating it would have cost several times more and is a known
# source of flaky, slow CI.
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build

WORKDIR /src

COPY go.work go.work.sum ./
COPY go.mod go.sum ./
COPY plugins/go.mod ./plugins/

COPY pkg ./pkg
COPY services ./services
COPY plugins ./plugins
COPY scripts/thirdparty ./scripts/thirdparty

# Build metadata stamped into pkg/version (see scripts/version.sh).
ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# TARGETOS/TARGETARCH are populated by BuildKit for every build.
# Deliberately NOT defaulted: a default of amd64 would make a plain
# local build on an arm64 machine emit an amd64 binary that then
# needs emulation to run. Unset expands to empty, which the Go
# toolchain treats as "host default" — the old behaviour exactly.
ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}
RUN go build -ldflags="-s -w \
      -X github.com/sluicio/sluicio-app/pkg/version.Version=${VERSION} \
      -X github.com/sluicio/sluicio-app/pkg/version.Commit=${COMMIT} \
      -X github.com/sluicio/sluicio-app/pkg/version.BuildDate=${BUILD_DATE}" \
      -o /out/cell-ingest ./services/cell-ingest/cmd/cell-ingest

# Third-party notices (issue #41): the components linked into the binary,
# with their licence and notice texts, generated from the module graph of
# what was just built. The generator is built for the BUILD machine (GOOS
# and GOARCH emptied) and then asks `go list` about the TARGET, so a
# cross-compiled arm64 image lists arm64's dependencies. -check fails the
# build on a licence that is not allowed.
RUN GOOS= GOARCH= go build -o /tmp/thirdparty ./scripts/thirdparty && \
    /tmp/thirdparty -check -product "Sluicio cell-ingest" \
      -json /out/third-party.json -text /out/THIRD-PARTY-NOTICES.txt \
      ./services/cell-ingest/cmd/cell-ingest

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/cell-ingest /cell-ingest
COPY --from=build /out/third-party.json /out/THIRD-PARTY-NOTICES.txt /usr/share/sluicio/

# 4318 is the OTLP/HTTP convention port (4317 is gRPC, which we don't
# expose). 4318 maps to the public ingest endpoint in deploy/server/.
EXPOSE 4318

# Numeric so Kubernetes runAsNonRoot can verify the user without a chart-side runAsUser pin.
USER 65532:65532

ENTRYPOINT ["/cell-ingest"]
