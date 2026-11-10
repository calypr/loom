# Loom ingest client

This standalone Go module streams selected repository-owned META files to Loom's
`sync-and-publish` endpoint. It uses only the Go standard library and does not
depend on Loom server packages.

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
	Files: []ingest.File{{Path: "META/Patient.ndjson", LocalPath: stagedPath}},
})
```

Each attempt reopens the local files and streams them as multipart parts. Keep
the operation ID and file contents stable across retries; Loom computes the
file digests and returns the durable publication receipt.
