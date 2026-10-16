package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

func TestSemanticCoverageCollectionHasEnvelopeAndCertificateIndexes(t *testing.T) {
	spec := SemanticCoverageCollectionSpec()
	if spec.Name != "fhir_semantic_coverage" || spec.Truncate {
		t.Fatalf("semantic coverage collection = %#v, want persistent fhir_semantic_coverage", spec)
	}
	for _, required := range [][]string{
		{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "resource_type", "_key"},
		{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "summary_kind", "resource_type", "canonical_path", "resource_boundary_signature", "auth_resource_path"},
		{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "resource_type", "auth_resource_path", "_key"},
		{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "_key"},
	} {
		if !containsSemanticCoverageIndex(spec.Indexes, required) {
			t.Fatalf("coverage indexes %#v do not include %#v", spec.Indexes, required)
		}
	}
}

func TestPrepareSemanticInventoryBackfillBootstrapsCoverageIndexes(t *testing.T) {
	client := &semanticCoverageTestClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.PrepareSemanticInventoryBackfill(context.Background()); err != nil {
		t.Fatalf("prepare semantic inventory backfill: %v", err)
	}
	var found bool
	for _, collection := range client.bootstrapSpec.Collections {
		if collection.Name == SemanticCoverageCollection {
			found = true
			if collection.Truncate {
				t.Fatal("coverage bootstrap requested truncation")
			}
			break
		}
	}
	if !found {
		t.Fatalf("backfill bootstrap omitted %s: %#v", SemanticCoverageCollection, client.bootstrapSpec.Collections)
	}
}

func containsSemanticCoverageIndex(indexes [][]string, want []string) bool {
	for _, index := range indexes {
		if len(index) != len(want) {
			continue
		}
		matches := true
		for position, field := range want {
			if index[position] != field {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func TestWriteSemanticCoverageDocumentsUsesBoundedReplaceBatches(t *testing.T) {
	documents := semanticCoverageFixtureDocuments(t, 5)
	client := &semanticCoverageTestClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	stats, err := adapter.writeSemanticCoverageDocuments(context.Background(), documents, 2)
	if err != nil {
		t.Fatalf("write semantic coverage: %v", err)
	}
	if len(client.insertCalls) != 3 {
		t.Fatalf("insert calls = %d, want 3", len(client.insertCalls))
	}
	var rows, storedBytes int64
	for _, call := range client.insertCalls {
		if call.collection != SemanticCoverageCollection || !call.overwrite || call.writeAPI != "" {
			t.Fatalf("insert call = %#v, want replace into coverage collection", call)
		}
		if len(call.documents) == 0 || len(call.documents) > 2 {
			t.Fatalf("batch size = %d, want 1..2", len(call.documents))
		}
		rows += int64(len(call.documents))
		for _, document := range call.documents {
			storedBytes += int64(len(document))
		}
	}
	if stats.Rows != rows || stats.Rows != 5 || stats.SerializedBytes != storedBytes {
		t.Fatalf("write stats = %#v, want rows=%d bytes=%d", stats, rows, storedBytes)
	}
}

func TestWriteSemanticCoverageDocumentsStopsOnFailedBatch(t *testing.T) {
	documents := semanticCoverageFixtureDocuments(t, 5)
	client := &semanticCoverageTestClient{failInsertAt: 2}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	stats, err := adapter.writeSemanticCoverageDocuments(context.Background(), documents, 2)
	if err == nil || !strings.Contains(err.Error(), "write semantic coverage rows starting at 2") {
		t.Fatalf("write error = %v, want failed second batch context", err)
	}
	if len(client.insertCalls) != 2 || stats.Rows != 2 {
		t.Fatalf("calls=%d stats=%#v, want two calls and only first batch counted", len(client.insertCalls), stats)
	}
}

func TestWriteSemanticCoverageDocumentsValidatesBeforeWriting(t *testing.T) {
	client := &semanticCoverageTestClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	for name, documents := range map[string][]json.RawMessage{
		"malformed JSON": {json.RawMessage(`{"_key":`)},
		"missing key":    {json.RawMessage(`{"project":"project"}`)},
		"oversized row":  {json.RawMessage(fmt.Sprintf(`{"_key":"large","payload":%q}`, strings.Repeat("x", semanticCoverageMaximumRowBytes)))},
	} {
		t.Run(name, func(t *testing.T) {
			client.insertCalls = nil
			if _, err := adapter.writeSemanticCoverageDocuments(context.Background(), documents, 1); err == nil {
				t.Fatal("invalid document was accepted")
			}
			if len(client.insertCalls) != 0 {
				t.Fatalf("invalid input partially wrote %d batches", len(client.insertCalls))
			}
		})
	}
}

func TestEncodeSemanticCoverageContributionCreatesOneNestedEnvelope(t *testing.T) {
	contribution := semanticCoverageFixtureContribution()
	contribution.DiagnosticLocatorsTruncated = true
	documents, err := EncodeSemanticCoverageContribution(contribution.BuildID, contribution)
	if err != nil {
		t.Fatalf("encode semantic coverage: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("encoded documents = %d, want one source envelope", len(documents))
	}

	var source semanticCoverageSourceEnvelope
	if err := json.Unmarshal(documents[0], &source); err != nil {
		t.Fatal(err)
	}
	if source.RecordKind != semanticCoverageEnvelopeRecordKind || source.CoverageStorageFormatVersion != catalog.SemanticCoverageStorageFormatVersion || source.SourceRecordCount != 1 || source.BuildID != contribution.BuildID || source.SourceID != contribution.Identity.SourceID {
		t.Fatalf("source envelope = %#v", source)
	}
	if source.Counts["codeable_concept_owners"] != 2 || source.Counts["coding_occurrences"] != 3 || !source.DiagnosticLocatorsTruncated {
		t.Fatalf("source counters/truncation = %#v", source)
	}
	if len(source.Paths) != 1 || source.Paths[0].CanonicalPath != "component[].code" || !source.Paths[0].Groupable {
		t.Fatalf("nested path aggregate = %#v, want complete semantic grouping despite diagnostic-only truncation", source.Paths)
	}
	if source.Paths[0].Path == nil || len(source.Paths[0].Path.References) != 2 || source.Paths[0].Path.References[0].Kind != string(catalog.SemanticCoverageExactReference) || source.Paths[0].Path.References[0].BindingID != "binding-1" {
		t.Fatalf("path references = %#v", source.Paths[0].Path)
	}
	if got := source.Paths[0].Path.References[1].Family; got == nil || got.Version != "v2" || got.System != "urn:family" || got.BindingID != "binding-family" {
		t.Fatalf("family reference = %#v, want full exact-version identity", got)
	}
	if len(source.Paths[0].Path.Facets) != 2 || source.Paths[0].Path.Facets[0].Path != "coding[].code" {
		t.Fatalf("nested facet aggregates = %#v", source.Paths[0].Path.Facets)
	}
	serialized := string(documents[0])
	for _, forbidden := range []string{"clinical-value", "secret-code-value"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("coverage persistence retained raw value %q: %s", forbidden, serialized)
		}
	}
	if strings.Contains(serialized, "owner_path_witnesses") || strings.Contains(serialized, "explanation") {
		t.Fatalf("coverage envelope retained diagnostic witnesses or explanations: %s", serialized)
	}
}

func TestEncodeSemanticCoverageContributionBlocksGroupingForProofTruncation(t *testing.T) {
	for name, mutate := range map[string]func(*catalog.SemanticCoverageSourceContribution){
		"source counts": func(contribution *catalog.SemanticCoverageSourceContribution) { contribution.CountsTruncated = true },
		"source facets": func(contribution *catalog.SemanticCoverageSourceContribution) { contribution.FacetsTruncated = true },
		"source references": func(contribution *catalog.SemanticCoverageSourceContribution) {
			contribution.ReferencesTruncated = true
		},
		"path counts": func(contribution *catalog.SemanticCoverageSourceContribution) {
			contribution.Paths[0].CountsTruncated = true
		},
		"path facets": func(contribution *catalog.SemanticCoverageSourceContribution) {
			contribution.Paths[0].FacetsTruncated = true
		},
		"path references": func(contribution *catalog.SemanticCoverageSourceContribution) {
			contribution.Paths[0].ReferencesTruncated = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			contribution := semanticCoverageFixtureContribution()
			mutate(&contribution)
			documents, err := EncodeSemanticCoverageContribution(contribution.BuildID, contribution)
			if err != nil {
				t.Fatalf("encode semantic coverage: %v", err)
			}
			var envelope semanticCoverageSourceEnvelope
			if err := json.Unmarshal(documents[0], &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope.Paths) != 1 || envelope.Paths[0].Groupable {
				t.Fatal("truncated path proof still allowed grouping")
			}
		})
	}
}

func TestEncodeSemanticCoverageContributionRequiresExactBuildAndSourceIdentity(t *testing.T) {
	contribution := semanticCoverageFixtureContribution()
	if _, err := EncodeSemanticCoverageContribution("other-build", contribution); err == nil {
		t.Fatal("contribution from another build was accepted")
	}
	contribution.SourceRecordCount = 0
	if _, err := EncodeSemanticCoverageContribution(contribution.BuildID, contribution); err == nil {
		t.Fatal("source contribution without one source record was accepted")
	}
}

func TestEmptySchemaIdentityPersistsIncompleteSourceWithoutGrouping(t *testing.T) {
	contribution := semanticCoverageFixtureContribution()
	contribution.Identity.SchemaIdentity = ""
	contribution.Complete = false
	contribution.IncompleteGaps = []catalog.SemanticCoverageCensusGap{catalog.SemanticCoverageGapSchemaIdentity}
	documents, err := EncodeSemanticCoverageContribution(contribution.BuildID, contribution)
	if err != nil {
		t.Fatalf("encode incomplete empty-schema contribution: %v", err)
	}
	var source semanticCoverageSourceEnvelope
	if err := json.Unmarshal(documents[0], &source); err != nil {
		t.Fatal(err)
	}
	if source.SchemaIdentity != "" || source.Complete || len(source.Paths) != 1 || source.Paths[0].Groupable {
		t.Fatalf("empty-schema source envelope = %+v, want persisted incomplete/unusable source", source)
	}
}

func TestEncodeSemanticCoverageContributionKeepsZeroObservationSource(t *testing.T) {
	contribution := semanticCoverageFixtureContribution()
	contribution.Paths = nil
	documents, err := EncodeSemanticCoverageContribution(contribution.BuildID, contribution)
	if err != nil {
		t.Fatalf("encode empty semantic coverage source: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("zero-observation source documents = %d, want one sentinel", len(documents))
	}
	var source semanticCoverageSourceEnvelope
	if err := json.Unmarshal(documents[0], &source); err != nil {
		t.Fatal(err)
	}
	if source.RecordKind != semanticCoverageEnvelopeRecordKind || source.SourceRecordCount != 1 || source.SourceID != contribution.Identity.SourceID || len(source.Paths) != 0 {
		t.Fatalf("zero-observation sentinel = %#v", source)
	}
}

func TestWriteSemanticCoverageContributionsWritesStableReplaceRows(t *testing.T) {
	client := &semanticCoverageTestClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	contribution := semanticCoverageFixtureContribution()
	first, err := adapter.WriteSemanticCoverageContributions(context.Background(), contribution.BuildID, []catalog.SemanticCoverageSourceContribution{contribution}, 1)
	if err != nil {
		t.Fatalf("write coverage contribution: %v", err)
	}
	if first.Rows != 1 || len(client.insertCalls) != 1 {
		t.Fatalf("first write stats=%#v batches=%d, want one source envelope", first, len(client.insertCalls))
	}
	if first.SourceSetsReplaced != 1 || len(client.executeCalls) != 0 {
		t.Fatalf("source replacement calls=%d stats=%#v, want one atomic envelope replacement without cleanup", len(client.executeCalls), first)
	}
	firstKeys := make([]string, 0, 1)
	for _, call := range client.insertCalls {
		var document semanticCoverageStoredDocument
		if err := json.Unmarshal(call.documents[0], &document); err != nil {
			t.Fatal(err)
		}
		firstKeys = append(firstKeys, document.Key)
	}
	client.insertCalls = nil
	second, err := adapter.WriteSemanticCoverageContributions(context.Background(), contribution.BuildID, []catalog.SemanticCoverageSourceContribution{contribution}, 1)
	if err != nil {
		t.Fatalf("replay coverage contribution: %v", err)
	}
	if second.Rows != first.Rows || second.SerializedBytes != first.SerializedBytes || len(client.insertCalls) != 1 {
		t.Fatalf("replay stats=%#v batches=%d, want deterministic one-document replacement", second, len(client.insertCalls))
	}
	for index, call := range client.insertCalls {
		var document semanticCoverageStoredDocument
		if err := json.Unmarshal(call.documents[0], &document); err != nil {
			t.Fatal(err)
		}
		if document.Key != firstKeys[index] || !call.overwrite {
			t.Fatalf("replay row %d = %#v overwrite=%v, want same key and replace", index, document, call.overwrite)
		}
	}
}

func TestWriteSemanticCoverageBatchesAtomicSourceEnvelopes(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	first := semanticCoverageFixtureContribution()
	first.Identity.SourceID = "retained:Observation/key-a"
	second := semanticCoverageFixtureContribution()
	second.Identity.SourceID = "retained:Observation/key-b"
	second.Identity.AuthResourcePath = "Patient/other"

	stats, err := adapter.WriteSemanticCoverageContributions(context.Background(), first.BuildID, []catalog.SemanticCoverageSourceContribution{first, second}, 10)
	if err != nil {
		t.Fatalf("write two-source batch: %v", err)
	}
	wantEvents := []string{semanticCoverageEnvelopeRecordKind, semanticCoverageEnvelopeRecordKind}
	if !reflect.DeepEqual(client.events, wantEvents) {
		t.Fatalf("batched write rows = %v, want one envelope per physical source", client.events)
	}
	if stats.SourceSetsReplaced != 2 || stats.Rows != 2 || client.insertAttempts != 1 {
		t.Fatalf("batched write stats = %+v insert batches=%d, want two envelopes in one batch", stats, client.insertAttempts)
	}

	interrupted := newSemanticCoverageMemoryClient()
	interrupted.failInsertAt = 2
	interruptedAdapter, err := New(interrupted)
	if err != nil {
		t.Fatal(err)
	}
	stats, err = interruptedAdapter.WriteSemanticCoverageContributions(context.Background(), first.BuildID, []catalog.SemanticCoverageSourceContribution{first, second}, 1)
	if err == nil || !strings.Contains(err.Error(), "replace semantic coverage source envelopes") || stats.Rows != 1 {
		t.Fatalf("interrupted bounded envelope batch stats=%+v err=%v, want one committed envelope before failure", stats, err)
	}
	if len(interrupted.documents) != 1 {
		t.Fatalf("partial envelope batch stored %d docs, want the first source only", len(interrupted.documents))
	}
	expectation := semanticCoverageTestResourceExpectation(t, "Observation", first, second)
	if err := interruptedAdapter.CompleteSemanticCoverageResource(context.Background(), SemanticCoverageBuildIdentity{
		Project: first.Identity.Project, DatasetGeneration: first.Identity.DatasetGeneration,
		BuildID: first.BuildID, SchemaIdentity: first.Identity.SchemaIdentity, SourceKind: first.Identity.SourceKind,
	}, expectation, 10); err == nil {
		t.Fatal("resource certification accepted a partial envelope batch")
	}
	for _, document := range interrupted.documents {
		if document.RecordKind == "resource" && document.ResourceState == SemanticCoverageBuildComplete {
			t.Fatalf("partial envelope batch published COMPLETE resource marker: %+v", document)
		}
	}
}

func TestWriteSemanticCoverageReplacesChangedSourcePathSetAndAuthorization(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	old := semanticCoverageFixtureContribution()
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), old.BuildID, []catalog.SemanticCoverageSourceContribution{old}, 10); err != nil {
		t.Fatalf("write original source: %v", err)
	}
	if len(client.documents) != 1 {
		t.Fatalf("original source rows = %d, want one source envelope", len(client.documents))
	}
	if got := client.events; !reflect.DeepEqual(got, []string{semanticCoverageEnvelopeRecordKind}) {
		t.Fatalf("original source write rows = %v", got)
	}

	updated := semanticCoverageFixtureContribution()
	updated.Identity.AuthResourcePath = "Patient/new-authorized"
	updated.Paths[0].CanonicalPath = "valueCodeableConcept"
	updated.Paths[0].Facets[0].Path = "valueCodeableConcept.coding[].code"
	updated.Paths[0].Facets[1].Path = "valueCodeableConcept.coding[].userSelected"
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), updated.BuildID, []catalog.SemanticCoverageSourceContribution{updated}, 10); err != nil {
		t.Fatalf("replace changed source shape/scope: %v", err)
	}
	if len(client.documents) != 1 {
		t.Fatalf("replacement source rows = %d, want one atomic envelope", len(client.documents))
	}
	for _, document := range client.documents {
		if document.AuthResourcePath != updated.Identity.AuthResourcePath {
			t.Fatalf("replacement did not update authorization partition: %#v", document)
		}
		if len(document.Paths) != 1 || document.Paths[0].CanonicalPath != "valueCodeableConcept" || len(document.Paths[0].Path.Facets) != 2 {
			t.Fatalf("stale nested path/facet survived replay: %#v", document.Paths)
		}
		for _, facet := range document.Paths[0].Path.Facets {
			if !strings.HasPrefix(facet.Path, "valueCodeableConcept.") {
				t.Fatalf("stale facet path survived replay: %#v", document.Paths[0].Path.Facets)
			}
		}
	}
	if got := client.events; !reflect.DeepEqual(got, []string{semanticCoverageEnvelopeRecordKind, semanticCoverageEnvelopeRecordKind}) {
		t.Fatalf("replacement source rows = %v, want direct atomic upserts", got)
	}
}

func TestWriteSemanticCoverageKeepsDistinctRetainedKeysWithSameLogicalFHIRIDSeparate(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	first := semanticCoverageFixtureContribution()
	first.Identity.AuthResourcePath = "Patient/scope-a"
	first.Identity.SourceID = "retained:Observation/arango-key-a"
	second := semanticCoverageFixtureContribution()
	second.Identity.AuthResourcePath = "Patient/scope-b"
	second.Identity.SourceID = "retained:Observation/arango-key-b"
	// These retained rows intentionally model distinct Arango _keys whose FHIR
	// payloads share the same logical `id`; source identity must use _key only.
	sharedLogicalFHIRID := "observation-duplicate"
	if sharedLogicalFHIRID == "" {
		t.Fatal("fixture requires a shared logical FHIR id")
	}
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), first.BuildID, []catalog.SemanticCoverageSourceContribution{first, second}, 10); err != nil {
		t.Fatalf("write distinct retained sources: %v", err)
	}
	if len(client.documents) != 2 {
		t.Fatalf("rows for two retained keys = %d, want two source envelopes", len(client.documents))
	}

	first.Paths[0].CanonicalPath = "valueCodeableConcept"
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), first.BuildID, []catalog.SemanticCoverageSourceContribution{first}, 10); err != nil {
		t.Fatalf("replace first retained source: %v", err)
	}
	if len(client.documents) != 2 {
		t.Fatalf("rows after one source replacement = %d, want both source sets preserved", len(client.documents))
	}
	foundOther := false
	for _, document := range client.documents {
		if document.SourceID == second.Identity.SourceID && document.AuthResourcePath == second.Identity.AuthResourcePath {
			foundOther = true
		}
		if document.SourceID == first.Identity.SourceID && (len(document.Paths) != 1 || document.Paths[0].CanonicalPath != "valueCodeableConcept") {
			t.Fatalf("stale path survived replacement: %#v", document.Paths)
		}
	}
	if !foundOther {
		t.Fatal("replacing one same-id retained row removed the other auth partition")
	}
}

func TestWriteSemanticCoverageReplaceFailurePreservesPriorEnvelope(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	contribution := semanticCoverageFixtureContribution()
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), contribution.BuildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err != nil {
		t.Fatalf("write initial source: %v", err)
	}
	before := make(map[string]semanticCoverageStoredDocument, len(client.documents))
	for key, document := range client.documents {
		before[key] = document
	}
	client.events = nil
	client.insertAttempts = 0
	client.failInsertAt = 1
	contribution.Paths[0].CanonicalPath = "changedPath"
	stats, err := adapter.WriteSemanticCoverageContributions(context.Background(), contribution.BuildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10)
	if err == nil || !strings.Contains(err.Error(), "replace semantic coverage source envelopes") || stats.Rows != 0 {
		t.Fatalf("replace failure stats=%#v err=%v, want no acknowledged writes", stats, err)
	}
	if client.insertAttempts != 1 || len(client.documents) != len(before) {
		t.Fatalf("replace failure attempted calls=%d rows=%d, want failed atomic write and prior row preserved", client.insertAttempts, len(client.documents))
	}
	for key, document := range before {
		if !reflect.DeepEqual(client.documents[key], document) {
			t.Fatalf("prior source row %q changed after failed cleanup: got %#v want %#v", key, client.documents[key], document)
		}
	}
}

func TestSemanticCoverageBuildMaterializesExactAuthAndBuildSummaries(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation", BuildID: buildID,
		SchemaIdentity: "schema-identity", SourceKind: catalog.SemanticInventorySourceRetained,
	}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatalf("begin coverage build: %v", err)
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.DiagnosticLocatorsTruncated = true
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err != nil {
		t.Fatalf("write source coverage: %v", err)
	}
	completeSemanticCoverageMemoryBuild(t, adapter, identity, contribution)
	marker := client.documents[semanticCoverageBuildKey(identity)]
	if marker.BuildState != SemanticCoverageBuildComplete || marker.MaterializedSourceCount != 1 || marker.SummaryCount != 4 {
		t.Fatalf("coverage marker = %+v, want complete owner and exact leaf summaries for one source", marker)
	}
	options := SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID,
		SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind,
		AuthResourcePaths: []string{contribution.Identity.AuthResourcePath}, Candidates: []SemanticCoverageCandidatePath{{
			ResourceType: contribution.Identity.ResourceType, CanonicalPath: "coding[].code",
		}},
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), options)
	if err != nil {
		t.Fatalf("lookup exact coverage: %v", err)
	}
	if lookup.State != SemanticCoverageBuildComplete || len(lookup.Candidates) != 1 || lookup.Candidates[0].Status != SemanticCoverageCandidateGroupable || len(lookup.Candidates[0].References) != 2 {
		t.Fatalf("exact lookup = %+v, want one groupable candidate with typed refs", lookup)
	}
	if lookup.Candidates[0].CanonicalPath != "coding[].code" {
		t.Fatalf("candidate path = %q, want exact raw leaf path", lookup.Candidates[0].CanonicalPath)
	}
	for _, reference := range lookup.Candidates[0].References {
		if reference.CandidatePath != "coding[].code" || reference.ResourceType != contribution.Identity.ResourceType {
			t.Fatalf("exact candidate reference identity = %+v, want leaf and resource type stamped from matched facet", reference)
		}
	}

	absent := options
	absent.Candidates = []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: "optional.absent"}}
	lookup, err = adapter.LookupSemanticCoverage(context.Background(), absent)
	if err != nil || lookup.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || lookup.Candidates[0].Reason != catalog.SemanticCoverageReasonUnrepresentedMetadata {
		t.Fatalf("proven absent facet lookup = %+v err=%v, want KEEP_RAW after complete source partition", lookup, err)
	}

	metadata := options
	metadata.Candidates = []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: "coding[].userSelected"}}
	lookup, err = adapter.LookupSemanticCoverage(context.Background(), metadata)
	if err != nil || lookup.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || lookup.Candidates[0].Reason != catalog.SemanticCoverageReasonUnrepresentedMetadata || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("raw metadata facet lookup = %+v err=%v, want KEEP_RAW without inherited owner refs", lookup, err)
	}

	unauthorized := options
	unauthorized.AuthResourcePaths = []string{"Patient/other"}
	lookup, err = adapter.LookupSemanticCoverage(context.Background(), unauthorized)
	if err != nil || len(lookup.Candidates) != 1 || lookup.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("source-free authorized scope lookup = %+v err=%v, want proven KEEP_RAW without refs", lookup, err)
	}

	otherBuild := options
	otherBuild.BuildID = "stale-build"
	lookup, err = adapter.LookupSemanticCoverage(context.Background(), otherBuild)
	if err != nil || lookup.State != SemanticCoverageBuildUnavailable || lookup.Reason != "BUILD_ID_MISMATCH" || lookup.Candidates[0].Status != SemanticCoverageCandidateUnavailable {
		t.Fatalf("stale build lookup = %+v err=%v, want unavailable exact-build result", lookup, err)
	}
}

func TestSemanticCoverageIncompleteReplayCannotReuseCompletedMarkerOrSummary(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation", BuildID: buildID,
		SchemaIdentity: "schema-identity", SourceKind: catalog.SemanticInventorySourceRetained,
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err != nil {
		t.Fatal(err)
	}
	completeSemanticCoverageMemoryBuild(t, adapter, identity, contribution)

	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatalf("begin replay: %v", err)
	}
	client.events = nil
	client.insertAttempts = 0
	client.failInsertAt = 1 // The atomic envelope replacement fails before it can be acknowledged.
	contribution.Paths[0].CanonicalPath = "changedPath"
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err == nil {
		t.Fatal("interrupted source-set replay returned nil")
	}
	marker := client.documents[semanticCoverageBuildKey(identity)]
	if marker.BuildState != SemanticCoverageBuildBuilding {
		t.Fatalf("marker after interrupted replay = %q, want BUILDING", marker.BuildState)
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID,
		SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind,
		AuthResourcePaths: []string{contribution.Identity.AuthResourcePath}, Candidates: []SemanticCoverageCandidatePath{{
			ResourceType: contribution.Identity.ResourceType, CanonicalPath: "component[].code",
		}},
	})
	if err != nil || lookup.State != SemanticCoverageBuildBuilding || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("lookup during replay = %+v err=%v, want BUILDING with no replacement refs", lookup, err)
	}
	page, err := adapter.readSemanticCoveragePage(context.Background(), identity.Project, identity.DatasetGeneration, buildID, identity.SourceKind, identity.SchemaIdentity, semanticCoverageEnvelopeRecordKind, []string{contribution.Identity.AuthResourcePath}, false, "", 10)
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("prior source envelope after failed replace rows = %d err=%v, want the original atomic envelope preserved", len(page.Rows), err)
	}
}

func TestSemanticCoverageMaterializationKeepsUnauthorizedPartitionSeparate(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: catalog.SemanticInventorySourceRetained}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	allowed := semanticCoverageFixtureContribution()
	allowed.BuildID = buildID
	allowed.Identity.SchemaIdentity = identity.SchemaIdentity
	allowed.Identity.AuthResourcePath = "Patient/allowed"
	unauthorized := semanticCoverageFixtureContribution()
	unauthorized.BuildID = buildID
	unauthorized.Identity.SchemaIdentity = identity.SchemaIdentity
	unauthorized.Identity.AuthResourcePath = "Patient/not-authorized"
	unauthorized.Identity.SourceID = "Observation/other-source"
	unauthorized.Paths[0].Disposition = catalog.SemanticCoverageRawRetainedDisposition
	unauthorized.Paths[0].Reason = catalog.SemanticCoverageReasonUnrepresentedMetadata
	for index := range unauthorized.Paths[0].Facets {
		unauthorized.Paths[0].Facets[index].Disposition = catalog.SemanticCoverageRawRetainedDisposition
		unauthorized.Paths[0].Facets[index].Reason = catalog.SemanticCoverageReasonUnrepresentedMetadata
		unauthorized.Paths[0].Facets[index].References = nil
	}
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{allowed, unauthorized}, 10); err != nil {
		t.Fatal(err)
	}
	completeSemanticCoverageMemoryBuild(t, adapter, identity, allowed, unauthorized)
	candidate := SemanticCoverageCandidatePath{ResourceType: "Observation", CanonicalPath: "coding[].code"}
	restricted, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{"Patient/allowed"}, Candidates: []SemanticCoverageCandidatePath{candidate},
	})
	if err != nil || restricted.Candidates[0].Status != SemanticCoverageCandidateGroupable || len(restricted.Candidates[0].References) == 0 {
		t.Fatalf("restricted authorized lookup = %+v err=%v, want only allowed groupable proof", restricted, err)
	}
	unrestricted, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePathsUnrestricted: true, Candidates: []SemanticCoverageCandidatePath{candidate},
	})
	if err != nil || unrestricted.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || len(unrestricted.Candidates[0].References) != 0 {
		t.Fatalf("unrestricted mixed partition lookup = %+v err=%v, want KEEP_RAW with no cross-partition refs", unrestricted, err)
	}
}

func TestFacetProofTruncationKeepsExactCandidateRaw(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: catalog.SemanticInventorySourceRetained}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	contribution.Paths[0].Facets[0].ReferencesTruncated = true
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err != nil {
		t.Fatalf("write truncated facet source: %v", err)
	}
	completeSemanticCoverageMemoryBuild(t, adapter, identity, contribution)
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{contribution.Identity.AuthResourcePath},
		Candidates: []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: "coding[].code"}},
	})
	if err != nil || lookup.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || lookup.Candidates[0].Reason != catalog.SemanticCoverageReasonTruncatedEvidence || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("truncated exact facet lookup = %+v err=%v, want KEEP_RAW/TRUNCATED_EVIDENCE without refs", lookup, err)
	}
}

func TestIncompletePathlessPartitionCannotBorrowProofFromAnotherAuthorizedPartition(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: catalog.SemanticInventorySourceRetained}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	complete := semanticCoverageFixtureContribution()
	complete.BuildID = buildID
	complete.Identity.SchemaIdentity = identity.SchemaIdentity
	complete.Identity.AuthResourcePath = "Patient/authorized-complete"
	incomplete := semanticCoverageFixtureContribution()
	incomplete.BuildID = buildID
	incomplete.Identity.SchemaIdentity = identity.SchemaIdentity
	incomplete.Identity.AuthResourcePath = "Patient/authorized-incomplete"
	incomplete.Identity.SourceID = "Observation/pathless-incomplete"
	incomplete.Paths = nil
	incomplete.Complete = false
	incomplete.IncompleteGaps = []catalog.SemanticCoverageCensusGap{catalog.SemanticCoverageGapRawAvailability}
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{complete, incomplete}, 10); err != nil {
		t.Fatalf("write source partitions: %v", err)
	}
	completeSemanticCoverageMemoryBuild(t, adapter, identity, complete, incomplete)
	candidate := SemanticCoverageCandidatePath{ResourceType: complete.Identity.ResourceType, CanonicalPath: "coding[].code"}
	restricted, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{complete.Identity.AuthResourcePath}, Candidates: []SemanticCoverageCandidatePath{candidate},
	})
	if err != nil || restricted.Candidates[0].Status != SemanticCoverageCandidateGroupable || len(restricted.Candidates[0].References) == 0 {
		t.Fatalf("complete auth partition lookup = %+v err=%v, want isolated complete proof", restricted, err)
	}
	unrestricted, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePathsUnrestricted: true, Candidates: []SemanticCoverageCandidatePath{candidate},
	})
	if err != nil || unrestricted.Candidates[0].Status != SemanticCoverageCandidateUnavailable || len(unrestricted.Candidates[0].References) != 0 {
		t.Fatalf("mixed complete/pathless partition lookup = %+v err=%v, want unavailable without borrowed proof", unrestricted, err)
	}
}

func TestEmptySchemaCoverageBuildAndLookupRemainUnavailable(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SourceKind: catalog.SemanticInventorySourceRetained}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatalf("begin empty-schema build: %v", err)
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = ""
	contribution.Complete = false
	contribution.IncompleteGaps = []catalog.SemanticCoverageCensusGap{catalog.SemanticCoverageGapSchemaIdentity}
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err != nil {
		t.Fatalf("persist empty-schema source: %v", err)
	}
	if err := adapter.FinalizeSemanticCoverageBuild(context.Background(), identity, []string{"Observation"}); err != nil {
		t.Fatalf("finalize empty-schema build: %v", err)
	}
	marker := client.documents[semanticCoverageBuildKey(identity)]
	if marker.BuildState != SemanticCoverageBuildUnavailable || marker.BuildReason != "SOURCE_SCHEMA_IDENTITY_UNAVAILABLE" {
		t.Fatalf("empty-schema build marker = %+v, want exact unavailable reason", marker)
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID,
		SourceKind: identity.SourceKind, AuthResourcePathsUnrestricted: true,
		Candidates: []SemanticCoverageCandidatePath{{ResourceType: "Observation", CanonicalPath: "coding[].code"}},
	})
	if err != nil || lookup.State != SemanticCoverageBuildUnavailable || lookup.Reason != "SOURCE_SCHEMA_IDENTITY_UNAVAILABLE" || lookup.Candidates[0].Status != SemanticCoverageCandidateUnavailable || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("empty-schema lookup = %+v err=%v, want unavailable without references", lookup, err)
	}
}

func TestSemanticCoverageCandidateLookupConsidersEveryBoundarySignature(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: catalog.SemanticInventorySourceRetained}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	ordinary := semanticCoverageFixtureContribution()
	ordinary.BuildID = buildID
	ordinary.Identity.SchemaIdentity = identity.SchemaIdentity
	ordinary.Identity.AuthResourcePath = "Patient/authorized"
	ordinary.Identity.SourceID = "retained:Observation/source-ordinary"
	subtype := semanticCoverageFixtureContribution()
	subtype.BuildID = buildID
	subtype.Identity.SchemaIdentity = identity.SchemaIdentity
	subtype.Identity.AuthResourcePath = ordinary.Identity.AuthResourcePath
	subtype.Identity.SourceID = "retained:Observation/source-subtype"
	subtype.Paths[0].ResourceBoundarySignature = "boundary-chain-v1:Observation.component[]"
	for index := range subtype.Paths[0].Facets {
		subtype.Paths[0].Facets[index].ResourceBoundarySignature = subtype.Paths[0].ResourceBoundarySignature
	}
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{ordinary, subtype}, 10); err != nil {
		t.Fatalf("write boundary-specific source sets: %v", err)
	}
	completeSemanticCoverageMemoryBuild(t, adapter, identity, ordinary, subtype)
	var matchingFacetSummaries []semanticCoverageStoredDocument
	for _, document := range client.documents {
		if document.RecordKind == "summary" && document.SummaryKind == "facet" && document.ResourceType == "Observation" && document.CanonicalPath == "coding[].code" {
			matchingFacetSummaries = append(matchingFacetSummaries, document)
		}
	}
	if len(matchingFacetSummaries) != 2 || matchingFacetSummaries[0].Key == matchingFacetSummaries[1].Key {
		t.Fatalf("same leaf summary rows across boundary signatures = %#v, want two independent keys", matchingFacetSummaries)
	}
	seenBoundarySignatures := map[string]bool{}
	for _, summary := range matchingFacetSummaries {
		seenBoundarySignatures[summary.ResourceBoundarySignature] = true
	}
	if len(seenBoundarySignatures) != 2 || !seenBoundarySignatures[""] || !seenBoundarySignatures[subtype.Paths[0].ResourceBoundarySignature] {
		t.Fatalf("facet summary boundary signatures = %#v, want ordinary and subtype scopes", seenBoundarySignatures)
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{ordinary.Identity.AuthResourcePath},
		Candidates: []SemanticCoverageCandidatePath{{ResourceType: "Observation", CanonicalPath: "coding[].code"}},
	})
	if err != nil || lookup.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("candidate lookup across ordinary and subtype summaries = %+v err=%v, want nonempty-boundary signature to block inherited grouping", lookup, err)
	}
}

func TestSemanticCoverageMaterializerCountsOverlappingFacetsOncePerSource(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation", BuildID: buildID,
		SchemaIdentity: "schema", SourceKind: catalog.SemanticInventorySourceRetained,
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	duplicate := contribution.Paths[0].Facets[0]
	duplicate.Kind = "primitive-code-alias"
	duplicate.OccurrenceCount = 2
	contribution.Paths[0].Facets = append(contribution.Paths[0].Facets, duplicate)
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err != nil {
		t.Fatal(err)
	}
	completeSemanticCoverageMemoryBuild(t, adapter, identity, contribution)

	var facetSummary *semanticCoverageStoredDocument
	for _, document := range client.documents {
		if document.RecordKind == "summary" && document.SummaryKind == "facet" && document.ResourceType == "Observation" && document.CanonicalPath == "coding[].code" {
			copy := document
			facetSummary = &copy
		}
	}
	if facetSummary == nil {
		t.Fatal("exact overlapping facet summary was not materialized")
	}
	if facetSummary.SourceRecordCount != 1 || facetSummary.Path == nil || facetSummary.Path.OccurrenceCount != 5 {
		t.Fatalf("folded facet summary = %+v, want one source and five occurrences", facetSummary)
	}
}

func TestSemanticCoverageLookupRejectsStorageFormatMismatch(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation", BuildID: buildID,
		SchemaIdentity: "schema", SourceKind: catalog.SemanticInventorySourceRetained,
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err != nil {
		t.Fatal(err)
	}
	completeSemanticCoverageMemoryBuild(t, adapter, identity, contribution)
	marker := client.documents[semanticCoverageBuildKey(identity)]
	marker.CoverageStorageFormatVersion++
	client.documents[semanticCoverageBuildKey(identity)] = marker

	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID,
		SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind,
		AuthResourcePaths: []string{contribution.Identity.AuthResourcePath},
		Candidates:        []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: "coding[].code"}},
	})
	if err != nil || lookup.State != SemanticCoverageBuildUnavailable || lookup.Reason != "COVERAGE_FORMAT_VERSION_MISMATCH" || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("format-mismatch lookup = %+v err=%v, want unavailable with no references", lookup, err)
	}
}

func TestSemanticCoverageReadFailureCannotPublishComplete(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: catalog.SemanticInventorySourceRetained}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err != nil {
		t.Fatal(err)
	}
	client.queryErr = errors.New("injected coverage source read failure")
	expectation := semanticCoverageTestResourceExpectation(t, "Observation", contribution)
	if err := adapter.CompleteSemanticCoverageResource(context.Background(), identity, expectation, 10); err == nil || !strings.Contains(err.Error(), "injected coverage source read failure") {
		t.Fatalf("coverage read failure = %v, want propagated source read error", err)
	}
	client.queryErr = nil
	marker := client.documents[semanticCoverageBuildKey(identity)]
	if marker.BuildState != SemanticCoverageBuildBuilding {
		t.Fatalf("marker after failed materialization read = %q, want BUILDING", marker.BuildState)
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{contribution.Identity.AuthResourcePath},
		Candidates: []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: "coding[].code"}},
	})
	if err != nil || lookup.State != SemanticCoverageBuildBuilding || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("lookup after failed materialization read = %+v err=%v, want BUILDING without refs", lookup, err)
	}
}

func TestSemanticCoverageSummaryWriteFailureCannotPublishComplete(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: catalog.SemanticInventorySourceRetained}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err != nil {
		t.Fatal(err)
	}
	client.insertAttempts = 0
	client.failInsertAt = 2
	expectation := semanticCoverageTestResourceExpectation(t, "Observation", contribution)
	if err := adapter.CompleteSemanticCoverageResource(context.Background(), identity, expectation, 1); err == nil || !strings.Contains(err.Error(), "injected source-set insert error") {
		t.Fatalf("summary write failure = %v, want propagated summary insert error", err)
	}
	client.failInsertAt = 0
	client.insertAttempts = 0
	marker := client.documents[semanticCoverageBuildKey(identity)]
	if marker.BuildState != SemanticCoverageBuildBuilding {
		t.Fatalf("marker after failed summary write = %q, want BUILDING", marker.BuildState)
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{contribution.Identity.AuthResourcePath},
		Candidates: []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: "coding[].code"}},
	})
	if err != nil || lookup.State != SemanticCoverageBuildBuilding || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("lookup after failed summary write = %+v err=%v, want BUILDING without refs", lookup, err)
	}
}

func TestCompleteRetainedSemanticCoverageResourceReturnsCertificationSourceRows(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	authPath := "Patient/authorized"
	client.retainedSources["Observation"] = []RetainedSemanticInventoryRow{{
		Key: "source-1", AuthResourcePath: &authPath, Payload: json.RawMessage(`{"id":"observation-1"}`),
	}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation", BuildID: buildID,
		SchemaIdentity: "schema", SourceKind: catalog.SemanticInventorySourceRetained,
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	contribution.Identity.SourceID = "retained:Observation/source-1"
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), buildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err != nil {
		t.Fatalf("write retained source envelope: %v", err)
	}
	certificationRows, err := adapter.CompleteRetainedSemanticCoverageResource(context.Background(), identity, "Observation", 10, 10)
	if err != nil || certificationRows != 1 {
		t.Fatalf("retained certification rows=%d err=%v, want one enumerated source row", certificationRows, err)
	}
	marker := client.documents[semanticCoverageResourceKey(identity, "Observation")]
	if marker.ResourceState != SemanticCoverageBuildComplete || marker.MaterializedSourceCount != certificationRows {
		t.Fatalf("retained resource marker = %+v, want one source and COMPLETE", marker)
	}
}

func TestSemanticCoverageLookupPropagatesErrorsAndSeparatesBuildStates(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: "file"}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	options := SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID,
		SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind, AuthResourcePathsUnrestricted: true,
		Candidates: []SemanticCoverageCandidatePath{{ResourceType: "Observation", CanonicalPath: "code"}},
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), options)
	if err != nil || lookup.State != SemanticCoverageBuildBuilding || lookup.Candidates[0].Status != SemanticCoverageCandidateUnavailable {
		t.Fatalf("building lookup = %+v err=%v", lookup, err)
	}
	if err := adapter.FailSemanticCoverageBuild(context.Background(), identity, "injected source read failure"); err != nil {
		t.Fatal(err)
	}
	lookup, err = adapter.LookupSemanticCoverage(context.Background(), options)
	if err != nil || lookup.State != SemanticCoverageBuildFailed || lookup.Reason != "injected source read failure" {
		t.Fatalf("failed lookup = %+v err=%v", lookup, err)
	}
	client.queryErr = errors.New("injected query error")
	if _, err := adapter.LookupSemanticCoverage(context.Background(), options); err == nil || !strings.Contains(err.Error(), "injected query error") {
		t.Fatalf("query failure = %v, want propagated error", err)
	}
}

func TestWriteSemanticCoverageFailureKeepsBuildUnavailableAfterReplayStarts(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = catalog.SemanticInventoryBuildID(contribution.Identity.Project, contribution.Identity.DatasetGeneration)
	if _, err := adapter.WriteSemanticCoverageContributions(context.Background(), contribution.BuildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10); err != nil {
		t.Fatalf("write initial source: %v", err)
	}
	identity := SemanticCoverageBuildIdentity{
		Project: contribution.Identity.Project, DatasetGeneration: contribution.Identity.DatasetGeneration,
		BuildID: contribution.BuildID, SchemaIdentity: contribution.Identity.SchemaIdentity,
		SourceKind: contribution.Identity.SourceKind,
	}
	completeSemanticCoverageMemoryBuild(t, adapter, identity, contribution)
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatalf("begin source replay: %v", err)
	}

	client.events = nil
	client.insertAttempts = 0
	client.failInsertAt = 1
	contribution.Paths[0].CanonicalPath = "changedPath"
	stats, err := adapter.WriteSemanticCoverageContributions(context.Background(), contribution.BuildID, []catalog.SemanticCoverageSourceContribution{contribution}, 10)
	if err == nil || !strings.Contains(err.Error(), "replace semantic coverage source envelopes") || stats.Rows != 0 {
		t.Fatalf("failed source replacement stats=%#v err=%v, want atomic envelope failure", stats, err)
	}
	if marker := client.documents[semanticCoverageBuildKey(identity)]; marker.BuildState != SemanticCoverageBuildBuilding {
		t.Fatalf("build marker after failed replay = %+v, want BUILDING", marker)
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: identity.BuildID,
		SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind,
		AuthResourcePaths: []string{contribution.Identity.AuthResourcePath},
		Candidates:        []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: "coding[].code"}},
	})
	if err != nil || lookup.State != SemanticCoverageBuildBuilding || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("lookup after failed replay = %+v err=%v, want BUILDING and no refs", lookup, err)
	}
}

func semanticCoverageFixtureContribution() catalog.SemanticCoverageSourceContribution {
	return catalog.SemanticCoverageSourceContribution{
		Identity: catalog.SemanticCoverageSourceIdentity{
			Project: "project", DatasetGeneration: "generation", AuthResourcePath: "Patient/authorized",
			SchemaIdentity: "schema-identity", SourceKind: catalog.SemanticInventorySourceRetained,
			SourceID: "Observation/source-1", ResourceType: "Observation",
		},
		BuildID: "build-1", SourceRecordCount: 1,
		Counts: catalog.SemanticCoverageCounts{CodeableConceptOwners: 2, CodingOccurrences: 3},
		Paths: []catalog.SemanticCoveragePathAggregate{{
			CanonicalPath: "component[].code", Kind: catalog.SemanticCoverageCodeableConcept,
			Disposition: catalog.SemanticCoverageSemanticDisposition, OccurrenceCount: 2,
			OwnerCount: 2, CodingCount: 3,
			PresenceCounts: map[catalog.SemanticCoveragePresence]int64{catalog.SemanticCoveragePopulated: 2},
			Facets: []catalog.SemanticCoverageFacetAggregate{
				{Path: "coding[].code", Kind: "code", Disposition: catalog.SemanticCoverageSemanticDisposition, OccurrenceCount: 3, References: []catalog.SemanticCoverageReference{
					{Kind: catalog.SemanticCoverageExactReference, ConceptID: "concept-1", BindingID: "binding-1"},
					{Kind: catalog.SemanticCoverageFamilyReference, Family: catalog.SemanticInventoryFamilyReference{FamilyID: "family-1", BindingID: "binding-family", SourceKind: "retained_vertex", ResourceType: "Observation", SourcePath: "component[].code", System: "urn:family", Version: "v2"}},
				}},
				{Path: "coding[].userSelected", Kind: "boolean", Disposition: catalog.SemanticCoverageRawRetainedDisposition, Reason: catalog.SemanticCoverageReasonUnrepresentedMetadata, OccurrenceCount: 1},
			},
			References: []catalog.SemanticCoverageReference{
				{Kind: catalog.SemanticCoverageExactReference, ConceptID: "concept-1", BindingID: "binding-1"},
				{Kind: catalog.SemanticCoverageFamilyReference, Family: catalog.SemanticInventoryFamilyReference{
					FamilyID: "family-1", BindingID: "binding-family", SourceKind: "retained_vertex", ResourceType: "Observation",
					SourcePath: "component[].code", System: "urn:family", Version: "v2",
				}},
			},
			OwnerPathWitnesses: []string{"component[0].code", "component[1].code"},
		}},
		Diagnostics: []catalog.SemanticCoverageDiagnostic{{Code: "fixture", CanonicalPath: "component[].code", OwnerPath: "component[0].code", Explanation: "unknown FHIR member"}},
		Complete:    true,
	}
}

func semanticCoverageTestResourceExpectation(t *testing.T, resourceType string, contributions ...catalog.SemanticCoverageSourceContribution) SemanticCoverageResourceExpectation {
	t.Helper()
	accumulator := NewSemanticCoverageSourceSetAccumulator(resourceType, true)
	for _, contribution := range contributions {
		if contribution.Identity.ResourceType != resourceType {
			t.Fatalf("test source resource type %q, want %q", contribution.Identity.ResourceType, resourceType)
		}
		if err := accumulator.AddSource(contribution.Identity.SourceID, contribution.Identity.AuthResourcePath); err != nil {
			t.Fatalf("add expected source identity: %v", err)
		}
	}
	expectation, err := accumulator.Snapshot(true)
	if err != nil {
		t.Fatalf("snapshot expected resource source set: %v", err)
	}
	return expectation
}

func completeSemanticCoverageMemoryBuild(t *testing.T, adapter *Store, identity SemanticCoverageBuildIdentity, contributions ...catalog.SemanticCoverageSourceContribution) {
	t.Helper()
	byResource := make(map[string][]catalog.SemanticCoverageSourceContribution)
	for _, contribution := range contributions {
		byResource[contribution.Identity.ResourceType] = append(byResource[contribution.Identity.ResourceType], contribution)
	}
	resourceTypes := make([]string, 0, len(byResource))
	for resourceType, resourceContributions := range byResource {
		resourceTypes = append(resourceTypes, resourceType)
		expectation := semanticCoverageTestResourceExpectation(t, resourceType, resourceContributions...)
		if err := adapter.CompleteSemanticCoverageResource(context.Background(), identity, expectation, 10); err != nil {
			t.Fatalf("complete semantic coverage resource %s: %v", resourceType, err)
		}
	}
	if err := adapter.FinalizeSemanticCoverageBuild(context.Background(), identity, resourceTypes); err != nil {
		t.Fatalf("finalize semantic coverage build: %v", err)
	}
}

func TestSemanticCoverageDocumentKeyUsesUnambiguousStableParts(t *testing.T) {
	first := semanticCoverageDocumentKey("project", "generation", "source", "path|part")
	if got := semanticCoverageDocumentKey("project", "generation", "source", "path|part"); got != first {
		t.Fatalf("same identity key changed: %q != %q", got, first)
	}
	if got := semanticCoverageDocumentKey("project", "generation", "source|path", "part"); got == first {
		t.Fatalf("different identity parts collided: %q", got)
	}
}

func TestReadSemanticCoveragePageIsBuildAndAuthorizationScoped(t *testing.T) {
	client := &semanticCoverageTestClient{queryRows: []map[string]any{
		{"_key": "a", "record_kind": semanticCoverageEnvelopeRecordKind},
		{"_key": "b", "record_kind": semanticCoverageEnvelopeRecordKind},
		{"_key": "c", "record_kind": semanticCoverageEnvelopeRecordKind},
	}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	page, err := adapter.readSemanticCoveragePage(context.Background(), "project", "generation", "build-2", "retained_vertex", "schema", semanticCoverageEnvelopeRecordKind, []string{"Patient/allowed"}, false, "", 2)
	if err != nil {
		t.Fatalf("read coverage page: %v", err)
	}
	if len(page.Rows) != 2 || !page.HasMore {
		t.Fatalf("coverage page = %#v, want two rows and more", page)
	}
	if client.queryCalls != 1 || client.queryBatchSize != 3 || client.queryVars["limit"] != 3 {
		t.Fatalf("query calls=%d batch=%d vars=%#v, want a three-row bounded read", client.queryCalls, client.queryBatchSize, client.queryVars)
	}
	if client.queryVars["project"] != "project" || client.queryVars["dataset_generation"] != "generation" || client.queryVars["build_id"] != "build-2" || client.queryVars["source_kind"] != "retained_vertex" || client.queryVars["schema_identity"] != "schema" || client.queryVars["record_kind"] != semanticCoverageEnvelopeRecordKind || client.queryVars["auth_resource_paths_unrestricted"] != false {
		t.Fatalf("coverage query scope vars = %#v", client.queryVars)
	}
	paths, ok := client.queryVars["auth_resource_paths"].([]string)
	if !ok || len(paths) != 1 || paths[0] != "Patient/allowed" {
		t.Fatalf("coverage authorization paths = %#v", client.queryVars["auth_resource_paths"])
	}
	for _, expected := range []string{"d.build_id == @build_id", "d.dataset_generation == @dataset_generation", "d.source_kind == @source_kind", "d.schema_identity == @schema_identity", "d.auth_resource_path IN @auth_resource_paths", "SORT d._key"} {
		if !strings.Contains(client.query, expected) {
			t.Fatalf("coverage query missing %q: %s", expected, client.query)
		}
	}
}

func TestReadSemanticCoveragePageRejectsMissingScopeAndPropagatesErrors(t *testing.T) {
	client := &semanticCoverageTestClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.readSemanticCoveragePage(context.Background(), "project", "generation", "", "retained_vertex", "schema", "source", nil, true, "", 2); !errors.Is(err, ErrInvalidSemanticCoverageRead) {
		t.Fatalf("missing-build error = %v, want invalid read request", err)
	}
	if client.queryCalls != 0 {
		t.Fatalf("invalid request executed %d queries", client.queryCalls)
	}

	client.queryErr = errors.New("injected read error")
	_, err = adapter.readSemanticCoveragePage(context.Background(), "project", "generation", "build", "file", "schema", "summary", nil, true, "cursor", 2)
	if err == nil || !strings.Contains(err.Error(), "injected read error") {
		t.Fatalf("query error = %v, want injected read error", err)
	}
}

func semanticCoverageFixtureDocuments(t *testing.T, count int) []json.RawMessage {
	t.Helper()
	documents := make([]json.RawMessage, count)
	for index := range documents {
		document, err := json.Marshal(map[string]any{"_key": fmt.Sprintf("coverage-%d", index), "record_kind": "summary"})
		if err != nil {
			t.Fatal(err)
		}
		documents[index] = document
	}
	return documents
}

type semanticCoverageInsertCall struct {
	collection string
	documents  []json.RawMessage
	overwrite  bool
	writeAPI   string
}

type semanticCoverageTestClient struct {
	insertCalls    []semanticCoverageInsertCall
	failInsertAt   int
	queryRows      []map[string]any
	queryErr       error
	query          string
	queryVars      map[string]interface{}
	queryBatchSize int
	queryCalls     int
	bootstrapSpec  store.BootstrapSpec
	executeCalls   []string
	executeVars    []map[string]interface{}
	executeErr     error
}

func (c *semanticCoverageTestClient) QueryRows(_ context.Context, query string, batchSize int, vars map[string]interface{}, visit store.RowVisitor) error {
	c.queryCalls++
	c.query = query
	c.queryVars = vars
	c.queryBatchSize = batchSize
	if c.queryErr != nil {
		return c.queryErr
	}
	for _, row := range c.queryRows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func (c *semanticCoverageTestClient) InsertBatchRaw(_ context.Context, collection string, documents []json.RawMessage, overwrite bool, writeAPI string) error {
	c.insertCalls = append(c.insertCalls, semanticCoverageInsertCall{
		collection: collection,
		documents:  append([]json.RawMessage(nil), documents...),
		overwrite:  overwrite,
		writeAPI:   writeAPI,
	})
	if c.failInsertAt != 0 && len(c.insertCalls) == c.failInsertAt {
		return errors.New("injected batch write error")
	}
	return nil
}

func (c *semanticCoverageTestClient) ExecuteAQL(_ context.Context, query string, vars map[string]interface{}) error {
	c.executeCalls = append(c.executeCalls, query)
	c.executeVars = append(c.executeVars, vars)
	return c.executeErr
}

func (*semanticCoverageTestClient) CollectionExists(context.Context, string) (bool, error) {
	return true, nil
}

func (c *semanticCoverageTestClient) Bootstrap(_ context.Context, spec store.BootstrapSpec) error {
	c.bootstrapSpec = spec
	return nil
}

type semanticCoverageMemoryClient struct {
	documents       map[string]semanticCoverageStoredDocument
	retainedSources map[string][]RetainedSemanticInventoryRow
	events          []string
	insertAttempts  int
	failInsertAt    int
	queryErr        error
}

func newSemanticCoverageMemoryClient() *semanticCoverageMemoryClient {
	return &semanticCoverageMemoryClient{
		documents:       make(map[string]semanticCoverageStoredDocument),
		retainedSources: make(map[string][]RetainedSemanticInventoryRow),
	}
}

func (c *semanticCoverageMemoryClient) QueryRows(_ context.Context, query string, maxRows int, vars map[string]interface{}, visit store.RowVisitor) error {
	if c.queryErr != nil {
		return c.queryErr
	}
	if query == retainedSemanticInventorySourcePageAQL {
		collection := vars["@collection"].(string)
		afterKey := vars["after_key"].(string)
		limit := vars["limit"].(int)
		rows := append([]RetainedSemanticInventoryRow(nil), c.retainedSources[collection]...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
		count := 0
		for _, source := range rows {
			if source.Key <= afterKey {
				continue
			}
			if count >= limit || count >= maxRows {
				break
			}
			row := map[string]any{"key": source.Key, "payload": source.Payload}
			if source.AuthResourcePath != nil {
				row["auth_resource_path"] = *source.AuthResourcePath
			}
			if err := visit(row); err != nil {
				return err
			}
			count++
		}
		return nil
	}
	if query == semanticCoverageLookupSnapshotAQL {
		identity := SemanticCoverageBuildIdentity{
			Project: vars["project"].(string), DatasetGeneration: vars["dataset_generation"].(string),
			BuildID: vars["build_id"].(string), SourceKind: vars["source_kind"].(string), SchemaIdentity: vars["schema_identity"].(string),
		}
		resourceTypes, _ := vars["resource_types"].([]string)
		candidates, _ := vars["candidates"].([]map[string]string)
		authPaths, _ := vars["auth_resource_paths"].([]string)
		unrestricted, _ := vars["auth_resource_paths_unrestricted"].(bool)
		snapshot := semanticCoverageLookupSnapshot{}
		if document, ok := c.documents[semanticCoverageBuildKey(identity)]; ok {
			copy := document
			snapshot.Build = &copy
		}
		for _, document := range c.documents {
			if document.Project != identity.Project || document.DatasetGeneration != identity.DatasetGeneration || document.BuildID != identity.BuildID || document.SourceKind != identity.SourceKind || document.SchemaIdentity != identity.SchemaIdentity {
				continue
			}
			switch document.RecordKind {
			case "resource":
				if containsString(resourceTypes, document.ResourceType) {
					snapshot.Resources = append(snapshot.Resources, document)
				}
			case "partition":
				if containsString(resourceTypes, document.ResourceType) && (unrestricted || containsString(authPaths, document.AuthResourcePath)) {
					snapshot.Partitions = append(snapshot.Partitions, document)
				}
			case "summary":
				if document.SummaryKind != "facet" || (!unrestricted && !containsString(authPaths, document.AuthResourcePath)) {
					continue
				}
				for _, candidate := range candidates {
					if document.ResourceType == candidate["resource_type"] && document.CanonicalPath == candidate["canonical_path"] {
						snapshot.Facets = append(snapshot.Facets, document)
						break
					}
				}
			}
		}
		if unrestricted {
			for _, partition := range snapshot.Partitions {
				snapshot.SourceProof = append(snapshot.SourceProof, semanticCoverageSourcePresence{
					ResourceType: partition.ResourceType, AuthResourcePath: partition.AuthResourcePath,
					SourceExists: c.hasSourceForPartition(identity, partition.ResourceType, partition.AuthResourcePath),
				})
			}
		} else {
			for _, resourceType := range resourceTypes {
				for _, authPath := range authPaths {
					snapshot.SourceProof = append(snapshot.SourceProof, semanticCoverageSourcePresence{
						ResourceType: resourceType, AuthResourcePath: authPath,
						SourceExists: c.hasSourceForPartition(identity, resourceType, authPath),
					})
				}
			}
		}
		sort.Slice(snapshot.Resources, func(i, j int) bool { return snapshot.Resources[i].ResourceType < snapshot.Resources[j].ResourceType })
		sort.Slice(snapshot.Partitions, func(i, j int) bool {
			if snapshot.Partitions[i].ResourceType != snapshot.Partitions[j].ResourceType {
				return snapshot.Partitions[i].ResourceType < snapshot.Partitions[j].ResourceType
			}
			return snapshot.Partitions[i].AuthResourcePath < snapshot.Partitions[j].AuthResourcePath
		})
		sort.Slice(snapshot.Facets, func(i, j int) bool {
			if snapshot.Facets[i].CanonicalPath != snapshot.Facets[j].CanonicalPath {
				return snapshot.Facets[i].CanonicalPath < snapshot.Facets[j].CanonicalPath
			}
			if snapshot.Facets[i].ResourceBoundarySignature != snapshot.Facets[j].ResourceBoundarySignature {
				return snapshot.Facets[i].ResourceBoundarySignature < snapshot.Facets[j].ResourceBoundarySignature
			}
			return snapshot.Facets[i].AuthResourcePath < snapshot.Facets[j].AuthResourcePath
		})
		var row map[string]any
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(encoded, &row); err != nil {
			return err
		}
		return visit(row)
	}
	if query == semanticCoverageResourcePageAQL {
		resourceType, _ := vars["resource_type"].(string)
		recordKind, _ := vars["record_kind"].(string)
		afterKey, _ := vars["after_key"].(string)
		limit, _ := vars["limit"].(int)
		identity := SemanticCoverageBuildIdentity{
			Project: vars["project"].(string), DatasetGeneration: vars["dataset_generation"].(string),
			BuildID: vars["build_id"].(string), SourceKind: vars["source_kind"].(string), SchemaIdentity: vars["schema_identity"].(string),
		}
		rows := make([]semanticCoverageStoredDocument, 0)
		for _, document := range c.documents {
			if document.Project != identity.Project || document.DatasetGeneration != identity.DatasetGeneration || document.BuildID != identity.BuildID || document.SourceKind != identity.SourceKind || document.SchemaIdentity != identity.SchemaIdentity || document.ResourceType != resourceType || document.RecordKind != recordKind || document.Key <= afterKey {
				continue
			}
			rows = append(rows, document)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
		if limit > 0 && len(rows) > limit {
			rows = rows[:limit]
		}
		for _, document := range rows {
			row, err := semanticCoverageStoredDocumentMap(document)
			if err != nil {
				return err
			}
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}
	if query == semanticCoverageBuildMarkerAQL {
		identity := SemanticCoverageBuildIdentity{
			Project: vars["project"].(string), DatasetGeneration: vars["dataset_generation"].(string),
			BuildID: vars["build_id"].(string), SchemaIdentity: vars["schema_identity"].(string), SourceKind: vars["source_kind"].(string),
		}
		if document, ok := c.documents[semanticCoverageBuildKey(identity)]; ok {
			row, err := semanticCoverageStoredDocumentMap(document)
			if err != nil {
				return err
			}
			return visit(row)
		}
		return nil
	}
	rows := make([]semanticCoverageStoredDocument, 0, len(c.documents))
	if query == semanticCoveragePartitionLookupAQL {
		paths, _ := vars["auth_resource_paths"].([]string)
		resourceTypes, _ := vars["resource_types"].([]string)
		unrestricted, _ := vars["auth_resource_paths_unrestricted"].(bool)
		for _, document := range c.documents {
			if document.Project != vars["project"] || document.DatasetGeneration != vars["dataset_generation"] || document.BuildID != vars["build_id"] || document.SourceKind != vars["source_kind"] || document.SchemaIdentity != vars["schema_identity"] || document.RecordKind != "partition" {
				continue
			}
			if !unrestricted && !containsString(paths, document.AuthResourcePath) {
				continue
			}
			if !containsString(resourceTypes, document.ResourceType) {
				continue
			}
			rows = append(rows, document)
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].ResourceType != rows[j].ResourceType {
				return rows[i].ResourceType < rows[j].ResourceType
			}
			return rows[i].AuthResourcePath < rows[j].AuthResourcePath
		})
		limit, _ := vars["limit"].(int)
		if limit > 0 && len(rows) > limit {
			rows = rows[:limit]
		}
		for _, document := range rows {
			row, err := semanticCoverageStoredDocumentMap(document)
			if err != nil {
				return err
			}
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}
	if query == semanticCoverageCandidateLookupAQL {
		candidates, _ := vars["candidates"].([]map[string]string)
		paths, _ := vars["auth_resource_paths"].([]string)
		unrestricted, _ := vars["auth_resource_paths_unrestricted"].(bool)
		for _, document := range c.documents {
			if document.Project != vars["project"] || document.DatasetGeneration != vars["dataset_generation"] || document.BuildID != vars["build_id"] || document.SourceKind != vars["source_kind"] || document.SchemaIdentity != vars["schema_identity"] || document.RecordKind != "summary" || document.SummaryKind != "facet" {
				continue
			}
			if !unrestricted && !containsString(paths, document.AuthResourcePath) {
				continue
			}
			for _, candidate := range candidates {
				if document.ResourceType == candidate["resource_type"] && document.CanonicalPath == candidate["canonical_path"] {
					rows = append(rows, document)
					break
				}
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].ResourceType != rows[j].ResourceType {
				return rows[i].ResourceType < rows[j].ResourceType
			}
			if rows[i].CanonicalPath != rows[j].CanonicalPath {
				return rows[i].CanonicalPath < rows[j].CanonicalPath
			}
			return rows[i].AuthResourcePath < rows[j].AuthResourcePath
		})
		limit, _ := vars["limit"].(int)
		if limit > 0 && len(rows) > limit {
			rows = rows[:limit]
		}
		for _, document := range rows {
			row, err := semanticCoverageStoredDocumentMap(document)
			if err != nil {
				return err
			}
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}
	for _, document := range c.documents {
		if document.Project != vars["project"] || document.DatasetGeneration != vars["dataset_generation"] || document.BuildID != vars["build_id"] || document.SourceKind != vars["source_kind"] || document.SchemaIdentity != vars["schema_identity"] || document.RecordKind != vars["record_kind"] {
			continue
		}
		if afterKey, _ := vars["after_key"].(string); afterKey != "" && document.Key <= afterKey {
			continue
		}
		if unrestricted, _ := vars["auth_resource_paths_unrestricted"].(bool); !unrestricted {
			paths, _ := vars["auth_resource_paths"].([]string)
			if !containsString(paths, document.AuthResourcePath) {
				continue
			}
		}
		rows = append(rows, document)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	limit, _ := vars["limit"].(int)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	for _, document := range rows {
		encoded, err := json.Marshal(document)
		if err != nil {
			return err
		}
		var row map[string]any
		if err := json.Unmarshal(encoded, &row); err != nil {
			return err
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func semanticCoverageStoredDocumentMap(document semanticCoverageStoredDocument) (map[string]any, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var row map[string]any
	if err := json.Unmarshal(encoded, &row); err != nil {
		return nil, err
	}
	return row, nil
}

func (c *semanticCoverageMemoryClient) InsertBatchRaw(_ context.Context, collection string, documents []json.RawMessage, _ bool, _ string) error {
	if collection != SemanticCoverageCollection {
		return fmt.Errorf("unexpected insert collection %q", collection)
	}
	c.insertAttempts++
	if c.failInsertAt != 0 && c.insertAttempts == c.failInsertAt {
		return errors.New("injected source-set insert error")
	}
	for _, encoded := range documents {
		var document semanticCoverageStoredDocument
		if err := json.Unmarshal(encoded, &document); err != nil {
			return err
		}
		c.documents[document.Key] = document
		c.events = append(c.events, document.RecordKind)
	}
	return nil
}

func (c *semanticCoverageMemoryClient) ExecuteAQL(_ context.Context, query string, vars map[string]interface{}) error {
	if query != deleteSemanticCoverageResourceSummariesAQL {
		return fmt.Errorf("unexpected AQL %q", query)
	}
	c.events = append(c.events, "delete resource summaries")
	for key, document := range c.documents {
		if document.Project == vars["project"] && document.DatasetGeneration == vars["dataset_generation"] && document.BuildID == vars["build_id"] && document.SourceKind == vars["source_kind"] && document.SchemaIdentity == vars["schema_identity"] && document.ResourceType == vars["resource_type"] && (document.RecordKind == "summary" || document.RecordKind == "partition") {
			delete(c.documents, key)
		}
	}
	return nil
}

func (*semanticCoverageMemoryClient) CollectionExists(context.Context, string) (bool, error) {
	return true, nil
}

func (*semanticCoverageMemoryClient) CollectionRevision(context.Context, string) (string, error) {
	return "revision-1", nil
}
func (*semanticCoverageMemoryClient) Bootstrap(context.Context, store.BootstrapSpec) error {
	return nil
}

func (c *semanticCoverageMemoryClient) hasSourceForPartition(identity SemanticCoverageBuildIdentity, resourceType, authResourcePath string) bool {
	for _, document := range c.documents {
		if document.Project == identity.Project && document.DatasetGeneration == identity.DatasetGeneration && document.BuildID == identity.BuildID &&
			document.SourceKind == identity.SourceKind && document.SchemaIdentity == identity.SchemaIdentity && document.RecordKind == semanticCoverageEnvelopeRecordKind &&
			document.ResourceType == resourceType && document.AuthResourcePath == authResourcePath {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
