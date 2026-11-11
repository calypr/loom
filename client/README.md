# Loom ingest client

This standalone Go module uploads selected repository-owned META files through
Loom's durable source-load API. `Publish` keeps one caller-facing entry point and
internally begins or resumes a load, checks its completed-file receipts,
uploads each missing file in order, and requests final publication. It uses only
the Go standard library and does not depend on Loom server packages.

```go
client, err := ingest.New(baseURL, bearerToken, nil)
if err != nil {
	return err
}
receipt, err := client.Publish(ctx, ingest.Request{
	Project:          project,
	AuthResourcePath: authResourcePath,
	OperationID:      operationID,
	Manifest: ingest.Manifest{
		Repository: repository,
		Commit:     immutableCommit,
		Files:      []ingest.ManifestFile{{Path: "META/Patient.ndjson"}},
	},
	Files: []ingest.File{{Path: "META/Patient.ndjson"}},
	AcquireFile: func(ctx context.Context, sourcePath string) (string, func(), error) {
		stagedPath, err := hydrateOneFile(ctx, sourcePath)
		if err != nil {
			return "", nil, err
		}
		return stagedPath, func() { _ = os.Remove(stagedPath) }, nil
	},
})
```

`AcquireFile` runs only for a file absent from Loom's status response. The
client keeps that local file available across retries for that upload, then calls its
cleanup before acquiring the next file. Callers can also supply `LocalPath`
values for already-staged files. Keep the operation ID and source identity
stable across task retries. Loom computes file digests from the streamed bytes;
the client does not hash or copy file contents.

HTTP 422 validation failures are returned as `*ingest.Error`. Its `Error()`
string includes the safe server message, request ID, bounded row issues, total
error count, and truncation state, so wrapping it with `%w` keeps the details in
worker logs. The bearer token is redacted from exposed error text.

Retryable ordinary transport or server failures are attempted up to four times.
The retryable `PUBLICATION_IN_PROGRESS` response is polled every 15 seconds for
up to five minutes, and the caller's context cancels that wait. Permanent
publication conflicts fail immediately.

Selected files replace their prior contents, including deletions; omitted files
stay in the published generation. Partial updates require an immutable base
with file ownership metadata. Legacy data that cannot be copied safely returns
a migration-required conflict. Abandoned candidate generations remain inactive;
automatic staging-data retention cleanup is not implemented yet.
