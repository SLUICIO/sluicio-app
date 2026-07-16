# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Multi-stage build for cell-ingest. Same shape as cell-api's
# Dockerfile — see that file for the rationale on workspace layout,
# CGO_ENABLED=0, distroless, etc.
#
#   docker build -f services/cell-ingest/Dockerfile -t sluicio/cell-ingest .

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
      -X github.com/integration-monitor/integration-monitor/pkg/version.Version=${VERSION} \
      -X github.com/integration-monitor/integration-monitor/pkg/version.Commit=${COMMIT} \
      -X github.com/integration-monitor/integration-monitor/pkg/version.BuildDate=${BUILD_DATE}" \
      -o /out/cell-ingest ./services/cell-ingest/cmd/cell-ingest

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/cell-ingest /cell-ingest

# 4318 is the OTLP/HTTP convention port (4317 is gRPC, which we don't
# expose). 4318 maps to the public ingest endpoint in deploy/server/.
EXPOSE 4318

# Numeric so Kubernetes runAsNonRoot can verify the user without a chart-side runAsUser pin.
USER 65532:65532

ENTRYPOINT ["/cell-ingest"]
