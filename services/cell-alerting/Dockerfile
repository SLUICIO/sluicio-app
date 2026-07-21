# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Multi-stage build for cell-alerting. Same shape as cell-api's
# Dockerfile — see that file for the rationale on workspace layout,
# CGO_ENABLED=0, distroless, etc. cell-alerting is a background worker
# (no HTTP port), so it exposes nothing.
#
#   docker build -f services/cell-alerting/Dockerfile -t sluicio/cell-alerting .

FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY go.work go.work.sum ./
COPY go.mod go.sum ./
COPY plugins/go.mod ./plugins/

COPY pkg ./pkg
COPY services ./services
COPY plugins ./plugins

# Build metadata stamped into pkg/version (see scripts/version.sh).
ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

ENV CGO_ENABLED=0 GOFLAGS=-trimpath
RUN go build -ldflags="-s -w \
      -X github.com/sluicio/sluicio-app/pkg/version.Version=${VERSION} \
      -X github.com/sluicio/sluicio-app/pkg/version.Commit=${COMMIT} \
      -X github.com/sluicio/sluicio-app/pkg/version.BuildDate=${BUILD_DATE}" \
      -o /out/cell-alerting ./services/cell-alerting/cmd/cell-alerting

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/cell-alerting /cell-alerting

# Numeric so Kubernetes runAsNonRoot can verify the user without a chart-side runAsUser pin.
USER 65532:65532

ENTRYPOINT ["/cell-alerting"]
