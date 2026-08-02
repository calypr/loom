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
// Manifest and active-pointer records share one collection so activation is a
// single revision-checked pointer update.
package arango
