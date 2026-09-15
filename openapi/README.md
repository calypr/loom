# Loom OpenAPI contract

[`openapi.yaml`](openapi.yaml) is the canonical contract for every Loom HTTP
route. It currently defines all 22 REST, health, GraphQL transport, and browser
UI operations registered by the server.

The files in this directory are source files:

- `openapi.yaml` — methods, paths, parameters, request bodies, responses, and
  wire models;
- `oapi-codegen.yaml` — generation settings for models, the Fiber v3 server,
  the strict server interface, and the embedded specification.

Regenerate the checked-in Go package after either file changes:

```bash
make generate-openapi
```

Generated output belongs in `generated/loomapi/`. Handwritten operation
implementations belong in `internal/server/openapi_routes.go`; production route
registration belongs only in the generated registrar invoked by
`internal/server/routes.go`.

Every operation declares its expected HTTP statuses explicitly. Error
responses include a human-readable explanation, media type, and schema; the
strict implementation returns the corresponding generated response union
member. Do not replace these with `default` responses: an undocumented status
is a contract bug and should fail loudly until the route and specification are
reconciled.

Validation and ownership checks:

```bash
make openapi-check
go test ./internal/server -run TestGeneratedRoutesExactlyMatchOpenAPISpec
```

Do not hand-edit `generated/loomapi/api.gen.go` or register a production Fiber
route directly.

## Stable-read schema migration

Generation status, generation activation, and recipe-execution success
responses use explicit schemas. Their fields and JSON names preserve the
pre-schema wire format, so existing clients do not need a version change for
this cutover. Regenerate the client before adopting the named Go response
types. Additive changes to these responses require an OpenAPI source change,
regeneration, and a literal response fixture. A removal, rename, or type change
requires a versioned route and a coordinated client migration before the old
contract is removed.

Routes whose successful result is intentionally extensible, including GraphQL
documents and health dependency maps, continue to use `RawJSON`. Do not replace
those schemas by analogy with fixed status payloads.
