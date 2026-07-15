# syntax=docker/dockerfile:1.7
FROM golang:1.26.5-alpine3.22 AS builder
RUN apk add --no-cache git ca-certificates tzdata

ENV CGO_ENABLED=0

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
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
ENTRYPOINT ["/app/arango-fhir-server"]
