# syntax=docker/dockerfile:1.7
FROM golang:1.26.3-alpine3.22 AS builder
RUN apk add --no-cache git ca-certificates tzdata

ENV CGO_ENABLED=0

WORKDIR /src

COPY go.mod go.sum ./
# Local development may replace these modules with sibling checkouts. The
# server image uses the published module graph; ingest's generic path is kept
# on the published APIs so this image is reproducible from this repository
# alone.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod edit -dropreplace=github.com/bmeg/jsonschema/v6 \
      -dropreplace=github.com/bmeg/jsonschemagraph && \
    go mod download

COPY cmd ./cmd
COPY fhirschema ./fhirschema
COPY fhirstructs ./fhirstructs
COPY graphqlapi ./graphqlapi
COPY internal ./internal
COPY schemas ./schemas

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -mod=mod \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/arango-fhir-server ./cmd/arango-fhir-server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S arango-fhir && \
    adduser -S -G arango-fhir -h /app arango-fhir

WORKDIR /app

COPY --from=builder /out/arango-fhir-server /app/arango-fhir-server
COPY --from=builder /src/schemas /app/schemas

USER arango-fhir
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=6 \
  CMD wget -q -O - http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/app/arango-fhir-server"]
