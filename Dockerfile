# syntax=docker/dockerfile:1.7
FROM golang:1.26.5-bookworm AS builder
RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

ENV CGO_ENABLED=0

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY cmd ./cmd
COPY generated ./generated
COPY internal ./internal
COPY schemas ./schemas

ARG TARGETOS=linux
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -mod=mod \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/arango-fhir-server ./cmd/arango-fhir-server
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -mod=mod \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/arango-fhir-proto ./cmd/arango-fhir-proto
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -mod=mod \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/loom-acceptance ./cmd/loom-acceptance

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S arango-fhir && \
    adduser -S -G arango-fhir -h /app arango-fhir && \
    mkdir -p /var/cache/loom /var/lib/loom/artifacts && \
    chown -R arango-fhir:arango-fhir /var/cache/loom /var/lib/loom

WORKDIR /app

COPY --from=builder /out/arango-fhir-server /app/arango-fhir-server
COPY --from=builder /out/arango-fhir-proto /app/arango-fhir-proto
COPY --from=builder /out/loom-acceptance /app/loom-acceptance
COPY --from=builder /src/schemas /app/schemas

USER arango-fhir
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/arango-fhir-server"]
