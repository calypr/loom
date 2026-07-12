// Package arango persists the immutable dataset generation lifecycle in
// ArangoDB.
//
// The adapter deliberately owns only manifests and the selected active
// generation. It does not load FHIR resources, build a catalog, resolve
// authorization, execute dataframes, or expose a public API. In particular it
// never stores raw authorization paths, tokens, subjects, or claims:
// authorization scope is a per-read concern, not persistent generation
// metadata.
//
// A manifest record and its project's active-generation record live in one
// physical collection, distinguished by an internal record type. ArangoDB AQL
// permits one data-modification operation per statement. Keeping these two
// lifecycle records together lets Activate use one UPDATE statement to
// supersede the old READY manifest and select the new one atomically on a
// single-server deployment.
package arango
