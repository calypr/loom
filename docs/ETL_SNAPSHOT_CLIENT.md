# Snapshot ETL client

The public Go package `github.com/calypr/loom/pkg/loometl` is the integration
boundary for an external ETL job. The external ETL repository was not present
when this package was added, so Loom does not pretend to patch or ship that
repository. The package implements the frozen wire contract and can be imported
by it directly.

## Safe workflow

`loometl.Workflow.Run` performs these operations in order:

1. Observe the active project release revision (or use an explicitly supplied
   revision).
2. Create or resume `(project, gitCommit)` and upload each resource type with
   `X-Content-SHA256`.
3. Finalize the immutable generation and require `STAGED`.
4. Start exact `{recipe, translationVersion, output}` materializations and poll
   their durable executions through `/graphql/graph` until `PUBLISHED` or
   `FAILED` (`READY` remains readable during migration).
5. Verify every configured required output belongs to the staged generation.
6. Create an immutable project release without moving visibility.
7. Compare-and-swap activate that release, accepting success only after the
   returned or reconciled active release identifies the new release.

No earlier step moves the active release. A checksum, finalization,
materialization, verification, or release-creation failure therefore leaves the
previous graph and dataframes active. Activation uses a deterministic release
ID, an expected revision, idempotent retry, and `GET .../releases/active`
reconciliation for a lost response.

## Integration sketch

```go
client, err := loometl.NewClient(loometl.ClientConfig{
    BaseURL: "http://loom:8080",
    Headers: http.Header{"Authorization": {"Bearer " + token}},
})
if err != nil { /* fail */ }

legacy, err := loometl.LegacyMutableUploadEnabled()
if err != nil { /* invalid environment */ }
workflow, err := loometl.NewWorkflow(loometl.WorkflowConfig{
    API: client,
    Diagnostics: loometl.SlogSink{Logger: logger},
    LegacyMutableUpload: legacy,
    Legacy: existingMutableUploadAdapter, // required only when legacy is true
})
if err != nil { /* fail */ }

patient, err := loometl.FileResource("Patient", patientNDJSONPath)
if err != nil { /* fail */ }
result, err := workflow.Run(ctx, loometl.RunRequest{
    Project: project,
    GitCommit: gitCommit,
    Resources: []loometl.ResourceSource{patient},
    RequiredSelectors: []loometl.DataframeSelector{{
        Recipe: "calypr-meta-default", TranslationVersion: "v1", Output: "Patient",
    }},
})
```

`LOOM_ETL_LEGACY_MUTABLE_UPLOAD` defaults to `false`. When explicitly enabled,
the caller must provide its existing mutable uploader as `LegacyRunner`; the
new client never silently falls back after a snapshot failure.

## Retry and diagnostics

The HTTP client retries bounded transport failures and Loom errors only when
the structured payload explicitly sets `retryable: true`. It never infers
retryability from an HTTP status or error message. Request bodies and request
IDs are recreated deterministically, so a lost response can repeat create,
upload, finalize, materialization start/poll, release creation, and activation
without changing content.

Diagnostics expose project, generation, exact selector, execution ID, phase,
output, Loom request ID, stable error code, redacted structured details when
the operation supplies them, and explicit retryability. Durable publication
polling intentionally does not expose arbitrary persisted backend details;
it uses the safe code, phase, output, request ID, and retryability fields.
