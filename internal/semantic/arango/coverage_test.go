package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog/fieldprofile"
	"github.com/calypr/loom/internal/semantic"
	store "github.com/calypr/loom/internal/store/arango"
)

func legacySourceEnvelopeFixture(t *testing.T, name string) []json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("testdata/legacy_source_envelopes/" + name)
	if err != nil {
		t.Fatalf("read legacy source envelope fixture %q: %v", name, err)
	}
	var documents []json.RawMessage
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if !json.Valid(line) {
			t.Fatalf("legacy source envelope fixture %q contains invalid JSON: %s", name, line)
		}
		documents = append(documents, append(json.RawMessage(nil), line...))
	}
	if len(documents) == 0 {
		t.Fatalf("legacy source envelope fixture %q is empty", name)
	}
	return documents
}

func seedLegacySourceEnvelopeFixture(t *testing.T, client interface {
	InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error
}, name string) {
	t.Helper()
	if err := client.InsertBatchRaw(context.Background(), SemanticCoverageCollection, legacySourceEnvelopeFixture(t, name), true, ""); err != nil {
		t.Fatalf("seed legacy source envelope fixture %q: %v", name, err)
	}
}

func TestLegacySourceEnvelopeFixturesPreservePhysicalKeys(t *testing.T) {
	want := map[string][]struct{ sourceID, authPath, key string }{
		"arango-partial-initial.jsonl": {
			{"retained:Specimen/physical-a/logical-shared", "Patient/scope-a", "scv1_bc2c209d2e52ddeab0ca791e7d1e00c3d03c858733cf6c17506ebcb2b7f1ad3b"},
			{"retained:Specimen/physical-b/logical-shared", "Patient/scope-b", "scv1_815870dbba2c8a4e212ff62dbc80c6cb6af3927610aa15b7fc2ebd71beb621a7"},
		},
		"arango-partial-replay.jsonl": {
			{"retained:Specimen/physical-a/logical-shared", "Patient/scope-c", "scv1_bc2c209d2e52ddeab0ca791e7d1e00c3d03c858733cf6c17506ebcb2b7f1ad3b"},
			{"retained:Specimen/physical-b/logical-shared", "Patient/scope-b", "scv1_815870dbba2c8a4e212ff62dbc80c6cb6af3927610aa15b7fc2ebd71beb621a7"},
		},
		"arango-mismatch.jsonl": {
			{"retained:Specimen/source-a", "Patient/scope-a", "scv1_ccab36e3113a0f9c04b21e83265d5e55af9890e2dc86289e1da2f69196c1fa9c"},
		},
	}
	for name, records := range want {
		documents := legacySourceEnvelopeFixture(t, name)
		if len(documents) != len(records) {
			t.Fatalf("fixture %s rows=%d want %d", name, len(documents), len(records))
		}
		for i, raw := range documents {
			var stored struct {
				Key               string `json:"_key"`
				Project           string `json:"project"`
				DatasetGeneration string `json:"dataset_generation"`
				BuildID           string `json:"build_id"`
				SchemaIdentity    string `json:"schema_identity"`
				SourceKind        string `json:"source_kind"`
				ResourceType      string `json:"resource_type"`
				SourceID          string `json:"source_id"`
				AuthResourcePath  string `json:"auth_resource_path"`
			}
			if err := json.Unmarshal(raw, &stored); err != nil {
				t.Fatal(err)
			}
			if stored.Key != records[i].key || stored.SourceID != records[i].sourceID || stored.AuthResourcePath != records[i].authPath {
				t.Fatalf("fixture %s record %d identity=(%s,%s,%s), want (%s,%s,%s)", name, i, stored.Key, stored.SourceID, stored.AuthResourcePath, records[i].key, records[i].sourceID, records[i].authPath)
			}
			identity := fieldprofile.SemanticCoverageSourceIdentity{Project: stored.Project, DatasetGeneration: stored.DatasetGeneration, SchemaIdentity: stored.SchemaIdentity, SourceKind: stored.SourceKind, ResourceType: stored.ResourceType, SourceID: stored.SourceID, AuthResourcePath: stored.AuthResourcePath}
			if got := semanticCoverageSourceEnvelopeKey(identity, stored.BuildID); got != stored.Key {
				t.Fatalf("legacy stored key %q does not match current stable key %q", stored.Key, got)
			}
		}
	}
	if want["arango-partial-initial.jsonl"][0].key != want["arango-partial-replay.jsonl"][0].key {
		t.Fatal("auth-path replay changed the historical physical envelope key")
	}
}

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

func TestPrepareSemanticInventoryBackfillDoesNotBootstrapCoverage(t *testing.T) {
	client := &semanticCoverageTestClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.PrepareSemanticInventoryBackfill(context.Background()); err != nil {
		t.Fatalf("prepare semantic inventory backfill: %v", err)
	}
	for _, collection := range client.bootstrapSpec.Collections {
		if collection.Name == SemanticCoverageCollection {
			t.Fatalf("semantic inventory backfill bootstrapped optional coverage collection: %#v", collection)
		}
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

func TestSemanticCoverageBuildMaterializesExactAuthAndBuildSummaries(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation", BuildID: buildID,
		SchemaIdentity: "schema-identity", SourceKind: semantic.SourceRetained,
	}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatalf("begin coverage build: %v", err)
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.DiagnosticLocatorsTruncated = true
	seedLegacySourceEnvelopeFixture(t, client, "build-complete.jsonl")
	completeSemanticCoverageMemoryBuild(t, adapter, identity, contribution)
	marker := client.documents[semanticCoverageBuildKey(identity)]
	if marker.BuildState != semantic.SemanticCoverageBuildComplete || marker.MaterializedSourceCount != 1 || marker.SummaryCount != 4 {
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
	if lookup.State != semantic.SemanticCoverageBuildComplete || len(lookup.Candidates) != 1 || lookup.Candidates[0].Status != SemanticCoverageCandidateGroupable || len(lookup.Candidates[0].References) != 2 {
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
	if err != nil || lookup.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || lookup.Candidates[0].Reason != fieldprofile.SemanticCoverageReasonUnrepresentedMetadata {
		t.Fatalf("proven absent facet lookup = %+v err=%v, want KEEP_RAW after complete source partition", lookup, err)
	}

	metadata := options
	metadata.Candidates = []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: "coding[].userSelected"}}
	lookup, err = adapter.LookupSemanticCoverage(context.Background(), metadata)
	if err != nil || lookup.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || lookup.Candidates[0].Reason != fieldprofile.SemanticCoverageReasonUnrepresentedMetadata || len(lookup.Candidates[0].References) != 0 {
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
	if err != nil || lookup.State != semantic.SemanticCoverageBuildUnavailable || lookup.Reason != "BUILD_ID_MISMATCH" || lookup.Candidates[0].Status != SemanticCoverageCandidateUnavailable {
		t.Fatalf("stale build lookup = %+v err=%v, want unavailable exact-build result", lookup, err)
	}
}

func TestSemanticCoverageIncompleteReplayCannotReuseCompletedMarkerOrSummary(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation", BuildID: buildID,
		SchemaIdentity: "schema-identity", SourceKind: semantic.SourceRetained,
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	seedLegacySourceEnvelopeFixture(t, client, "build-complete.jsonl")
	completeSemanticCoverageMemoryBuild(t, adapter, identity, contribution)

	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatalf("begin replay: %v", err)
	}
	client.events = nil
	marker := client.documents[semanticCoverageBuildKey(identity)]
	if marker.BuildState != semantic.SemanticCoverageBuildBuilding {
		t.Fatalf("marker after interrupted replay = %q, want BUILDING", marker.BuildState)
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID,
		SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind,
		AuthResourcePaths: []string{contribution.Identity.AuthResourcePath}, Candidates: []SemanticCoverageCandidatePath{{
			ResourceType: contribution.Identity.ResourceType, CanonicalPath: "component[].code",
		}},
	})
	if err != nil || lookup.State != semantic.SemanticCoverageBuildBuilding || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("lookup during replay = %+v err=%v, want BUILDING with no replacement refs", lookup, err)
	}
	page, err := adapter.readSemanticCoveragePage(context.Background(), semanticCoveragePageRequest{
		Identity:   SemanticCoverageBuildIdentity{Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SourceKind: identity.SourceKind, SchemaIdentity: identity.SchemaIdentity},
		RecordKind: semanticCoverageEnvelopeRecordKind, AuthResourcePaths: []string{contribution.Identity.AuthResourcePath}, Limit: 10,
	})
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
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: semantic.SourceRetained}
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
	unauthorized.Paths[0].Disposition = fieldprofile.SemanticCoverageRawRetainedDisposition
	unauthorized.Paths[0].Reason = fieldprofile.SemanticCoverageReasonUnrepresentedMetadata
	for index := range unauthorized.Paths[0].Facets {
		unauthorized.Paths[0].Facets[index].Disposition = fieldprofile.SemanticCoverageRawRetainedDisposition
		unauthorized.Paths[0].Facets[index].Reason = fieldprofile.SemanticCoverageReasonUnrepresentedMetadata
		unauthorized.Paths[0].Facets[index].References = nil
	}
	seedLegacySourceEnvelopeFixture(t, client, "auth-partitions.jsonl")
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
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: semantic.SourceRetained}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	contribution.Paths[0].Facets[0].ReferencesTruncated = true
	seedLegacySourceEnvelopeFixture(t, client, "facet-truncated.jsonl")
	completeSemanticCoverageMemoryBuild(t, adapter, identity, contribution)
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{contribution.Identity.AuthResourcePath},
		Candidates: []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: "coding[].code"}},
	})
	if err != nil || lookup.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || lookup.Candidates[0].Reason != fieldprofile.SemanticCoverageReasonTruncatedEvidence || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("truncated exact facet lookup = %+v err=%v, want KEEP_RAW/TRUNCATED_EVIDENCE without refs", lookup, err)
	}
}

func TestSemanticCoverageExactFacetIgnoresRawSiblingAndOwnerDisposition(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation", BuildID: buildID,
		SchemaIdentity: "schema", SourceKind: semantic.SourceRetained,
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	contribution.Paths[0].Disposition = fieldprofile.SemanticCoverageRawRetainedDisposition
	contribution.Paths[0].Reason = fieldprofile.SemanticCoverageReasonUnrepresentedMetadata
	display := contribution.Paths[0].Facets[0]
	display.Path = "coding[].display"
	display.Kind = "string"
	contribution.Paths[0].Facets = append(contribution.Paths[0].Facets, display)
	seedLegacySourceEnvelopeFixture(t, client, "raw-sibling.jsonl")
	completeSemanticCoverageMemoryBuild(t, adapter, identity, contribution)

	lookup := func(path string) SemanticCoverageCandidateResult {
		t.Helper()
		result, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
			Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID,
			SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind,
			AuthResourcePaths: []string{contribution.Identity.AuthResourcePath},
			Candidates:        []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: path}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result.Candidates[0]
	}
	code := lookup("coding[].code")
	if code.Status != SemanticCoverageCandidateGroupable || len(code.References) == 0 {
		t.Fatalf("semantic code facet = %+v, want GROUPABLE with exact refs despite raw owner/sibling", code)
	}
	displayResult := lookup("coding[].display")
	if displayResult.Status != SemanticCoverageCandidateGroupable || len(displayResult.References) == 0 {
		t.Fatalf("semantic display facet = %+v, want GROUPABLE with exact refs despite raw owner/sibling", displayResult)
	}
	userSelected := lookup("coding[].userSelected")
	if userSelected.Status != SemanticCoverageCandidateKeepRaw || userSelected.Reason != fieldprofile.SemanticCoverageReasonUnrepresentedMetadata || len(userSelected.References) != 0 {
		t.Fatalf("raw userSelected sibling = %+v, want KEEP_RAW without inherited refs", userSelected)
	}
}

func TestIncompletePathlessPartitionCannotBorrowProofFromAnotherAuthorizedPartition(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: semantic.SourceRetained}
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
	incomplete.IncompleteGaps = []fieldprofile.SemanticCoverageCensusGap{fieldprofile.SemanticCoverageGapRawAvailability}
	seedLegacySourceEnvelopeFixture(t, client, "complete-and-pathless.jsonl")
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
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SourceKind: semantic.SourceRetained}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatalf("begin empty-schema build: %v", err)
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = ""
	contribution.Complete = false
	contribution.IncompleteGaps = []fieldprofile.SemanticCoverageCensusGap{fieldprofile.SemanticCoverageGapSchemaIdentity}
	seedLegacySourceEnvelopeFixture(t, client, "empty-schema.jsonl")
	if err := adapter.FinalizeSemanticCoverageBuild(context.Background(), identity, []string{"Observation"}); err != nil {
		t.Fatalf("finalize empty-schema build: %v", err)
	}
	marker := client.documents[semanticCoverageBuildKey(identity)]
	if marker.BuildState != semantic.SemanticCoverageBuildUnavailable || marker.BuildReason != "SOURCE_SCHEMA_IDENTITY_UNAVAILABLE" {
		t.Fatalf("empty-schema build marker = %+v, want exact unavailable reason", marker)
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID,
		SourceKind: identity.SourceKind, AuthResourcePathsUnrestricted: true,
		Candidates: []SemanticCoverageCandidatePath{{ResourceType: "Observation", CanonicalPath: "coding[].code"}},
	})
	if err != nil || lookup.State != semantic.SemanticCoverageBuildUnavailable || lookup.Reason != "SOURCE_SCHEMA_IDENTITY_UNAVAILABLE" || lookup.Candidates[0].Status != SemanticCoverageCandidateUnavailable || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("empty-schema lookup = %+v err=%v, want unavailable without references", lookup, err)
	}
}

func TestSemanticCoverageCandidateLookupConsidersEveryBoundarySignature(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: semantic.SourceRetained}
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
	seedLegacySourceEnvelopeFixture(t, client, "boundary-signatures.jsonl")
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
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation", BuildID: buildID,
		SchemaIdentity: "schema", SourceKind: semantic.SourceRetained,
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	duplicate := contribution.Paths[0].Facets[0]
	duplicate.Kind = "primitive-code-alias"
	duplicate.OccurrenceCount = 2
	contribution.Paths[0].Facets = append(contribution.Paths[0].Facets, duplicate)
	seedLegacySourceEnvelopeFixture(t, client, "overlapping-facets.jsonl")
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
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation", BuildID: buildID,
		SchemaIdentity: "schema", SourceKind: semantic.SourceRetained,
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	seedLegacySourceEnvelopeFixture(t, client, "format-mismatch.jsonl")
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
	if err != nil || lookup.State != semantic.SemanticCoverageBuildUnavailable || lookup.Reason != "COVERAGE_FORMAT_VERSION_MISMATCH" || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("format-mismatch lookup = %+v err=%v, want unavailable with no references", lookup, err)
	}
}

func TestSemanticCoverageReadFailureCannotPublishComplete(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: semantic.SourceRetained}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	seedLegacySourceEnvelopeFixture(t, client, "read-failure.jsonl")
	client.queryErr = errors.New("injected coverage source read failure")
	expectation := semanticCoverageTestResourceExpectation(t, "Observation", contribution)
	if err := adapter.CompleteSemanticCoverageResource(context.Background(), identity, expectation, 10); err == nil || !strings.Contains(err.Error(), "injected coverage source read failure") {
		t.Fatalf("coverage read failure = %v, want propagated source read error", err)
	}
	client.queryErr = nil
	marker := client.documents[semanticCoverageBuildKey(identity)]
	if marker.BuildState != semantic.SemanticCoverageBuildBuilding {
		t.Fatalf("marker after failed materialization read = %q, want BUILDING", marker.BuildState)
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{contribution.Identity.AuthResourcePath},
		Candidates: []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: "coding[].code"}},
	})
	if err != nil || lookup.State != semantic.SemanticCoverageBuildBuilding || len(lookup.Candidates[0].References) != 0 {
		t.Fatalf("lookup after failed materialization read = %+v err=%v, want BUILDING without refs", lookup, err)
	}
}

func TestSemanticCoverageSummaryWriteFailureCannotPublishComplete(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: buildID, SchemaIdentity: "schema", SourceKind: semantic.SourceRetained}
	if err := adapter.BeginSemanticCoverageBuild(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	seedLegacySourceEnvelopeFixture(t, client, "summary-write-failure.jsonl")
	client.insertAttempts = 0
	client.failInsertAt = 2
	expectation := semanticCoverageTestResourceExpectation(t, "Observation", contribution)
	if err := adapter.CompleteSemanticCoverageResource(context.Background(), identity, expectation, 1); err == nil || !strings.Contains(err.Error(), "injected source-set insert error") {
		t.Fatalf("summary write failure = %v, want propagated summary insert error", err)
	}
	client.failInsertAt = 0
	client.insertAttempts = 0
	marker := client.documents[semanticCoverageBuildKey(identity)]
	if marker.BuildState != semantic.SemanticCoverageBuildBuilding {
		t.Fatalf("marker after failed summary write = %q, want BUILDING", marker.BuildState)
	}
	lookup, err := adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{contribution.Identity.AuthResourcePath},
		Candidates: []SemanticCoverageCandidatePath{{ResourceType: contribution.Identity.ResourceType, CanonicalPath: "coding[].code"}},
	})
	if err != nil || lookup.State != semantic.SemanticCoverageBuildBuilding || len(lookup.Candidates[0].References) != 0 {
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
	buildID := semantic.BuildID("project", "generation")
	identity := SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation", BuildID: buildID,
		SchemaIdentity: "schema", SourceKind: semantic.SourceRetained,
	}
	contribution := semanticCoverageFixtureContribution()
	contribution.BuildID = buildID
	contribution.Identity.SchemaIdentity = identity.SchemaIdentity
	contribution.Identity.SourceID = "retained:Observation/source-1"
	seedLegacySourceEnvelopeFixture(t, client, "retained-source.jsonl")
	certificationRows, err := adapter.CompleteRetainedSemanticCoverageResource(context.Background(), identity, "Observation", 10, 10)
	if err != nil || certificationRows != 1 {
		t.Fatalf("retained certification rows=%d err=%v, want one enumerated source row", certificationRows, err)
	}
	marker := client.documents[semanticCoverageResourceKey(identity, "Observation")]
	if marker.ResourceState != semantic.SemanticCoverageBuildComplete || marker.MaterializedSourceCount != certificationRows {
		t.Fatalf("retained resource marker = %+v, want one source and COMPLETE", marker)
	}
}

func TestSemanticCoverageLookupPropagatesErrorsAndSeparatesBuildStates(t *testing.T) {
	client := newSemanticCoverageMemoryClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	buildID := semantic.BuildID("project", "generation")
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
	if err != nil || lookup.State != semantic.SemanticCoverageBuildBuilding || lookup.Candidates[0].Status != SemanticCoverageCandidateUnavailable {
		t.Fatalf("building lookup = %+v err=%v", lookup, err)
	}
	if err := adapter.FailSemanticCoverageBuild(context.Background(), identity, "injected source read failure"); err != nil {
		t.Fatal(err)
	}
	lookup, err = adapter.LookupSemanticCoverage(context.Background(), options)
	if err != nil || lookup.State != semantic.SemanticCoverageBuildFailed || lookup.Reason != "injected source read failure" {
		t.Fatalf("failed lookup = %+v err=%v", lookup, err)
	}
	client.queryErr = errors.New("injected query error")
	if _, err := adapter.LookupSemanticCoverage(context.Background(), options); err == nil || !strings.Contains(err.Error(), "injected query error") {
		t.Fatalf("query failure = %v, want propagated error", err)
	}
}

func TestSemanticCoverageUnrestrictedLookupBindsAnEmptyAuthArray(t *testing.T) {
	client := &semanticCoverageTestClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	project, generation := "project", "generation"
	_, err = adapter.LookupSemanticCoverage(context.Background(), SemanticCoverageLookupOptions{
		Project: project, DatasetGeneration: generation,
		BuildID:        semantic.BuildID(project, generation),
		SchemaIdentity: "schema", SourceKind: semantic.SourceRetained,
		AuthResourcePathsUnrestricted: true,
		Candidates:                    []SemanticCoverageCandidatePath{{ResourceType: "Specimen", CanonicalPath: "status"}},
	})
	if err != nil {
		t.Fatalf("unrestricted lookup: %v", err)
	}
	paths, ok := client.queryVars["auth_resource_paths"].([]string)
	if !ok || paths == nil || len(paths) != 0 {
		t.Fatalf("unrestricted auth bind = %#v, want non-nil empty string array", client.queryVars["auth_resource_paths"])
	}
}

func semanticCoverageFixtureContribution() fieldprofile.SemanticCoverageSourceContribution {
	return fieldprofile.SemanticCoverageSourceContribution{
		Identity: fieldprofile.SemanticCoverageSourceIdentity{
			Project: "project", DatasetGeneration: "generation", AuthResourcePath: "Patient/authorized",
			SchemaIdentity: "schema-identity", SourceKind: semantic.SourceRetained,
			SourceID: "Observation/source-1", ResourceType: "Observation",
		},
		BuildID: "build-1", SourceRecordCount: 1,
		Counts: fieldprofile.SemanticCoverageCounts{CodeableConceptOwners: 2, CodingOccurrences: 3},
		Paths: []fieldprofile.SemanticCoveragePathAggregate{{
			CanonicalPath: "component[].code", Kind: fieldprofile.SemanticCoverageCodeableConcept,
			Disposition: fieldprofile.SemanticCoverageSemanticDisposition, OccurrenceCount: 2,
			OwnerCount: 2, CodingCount: 3,
			PresenceCounts: map[fieldprofile.SemanticCoveragePresence]int64{fieldprofile.SemanticCoveragePopulated: 2},
			Facets: []fieldprofile.SemanticCoverageFacetAggregate{
				{Path: "coding[].code", Kind: "code", Disposition: fieldprofile.SemanticCoverageSemanticDisposition, OccurrenceCount: 3, References: []fieldprofile.SemanticCoverageReference{
					{Kind: fieldprofile.SemanticCoverageExactReference, ConceptID: "concept-1", BindingID: "binding-1"},
					{Kind: fieldprofile.SemanticCoverageFamilyReference, Family: semantic.FamilyReference{FamilyID: "family-1", BindingID: "binding-family", SourceKind: "retained_vertex", ResourceType: "Observation", SourcePath: "component[].code", System: "urn:family", Version: "v2"}},
				}},
				{Path: "coding[].userSelected", Kind: "boolean", Disposition: fieldprofile.SemanticCoverageRawRetainedDisposition, Reason: fieldprofile.SemanticCoverageReasonUnrepresentedMetadata, OccurrenceCount: 1},
			},
			References: []fieldprofile.SemanticCoverageReference{
				{Kind: fieldprofile.SemanticCoverageExactReference, ConceptID: "concept-1", BindingID: "binding-1"},
				{Kind: fieldprofile.SemanticCoverageFamilyReference, Family: semantic.FamilyReference{
					FamilyID: "family-1", BindingID: "binding-family", SourceKind: "retained_vertex", ResourceType: "Observation",
					SourcePath: "component[].code", System: "urn:family", Version: "v2",
				}},
			},
			OwnerPathWitnesses: []string{"component[0].code", "component[1].code"},
		}},
		Diagnostics: []fieldprofile.SemanticCoverageDiagnostic{{Code: "fixture", CanonicalPath: "component[].code", OwnerPath: "component[0].code", Explanation: "unknown FHIR member"}},
		Complete:    true,
	}
}

func semanticCoverageTestResourceExpectation(t *testing.T, resourceType string, contributions ...fieldprofile.SemanticCoverageSourceContribution) semantic.ResourceExpectation {
	t.Helper()
	accumulator := semantic.NewSourceSetAccumulator(resourceType, true)
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

func completeSemanticCoverageMemoryBuild(t *testing.T, adapter *Store, identity SemanticCoverageBuildIdentity, contributions ...fieldprofile.SemanticCoverageSourceContribution) {
	t.Helper()
	byResource := make(map[string][]fieldprofile.SemanticCoverageSourceContribution)
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
	first := semantic.CoverageDocumentKey("project", "generation", "source", "path|part")
	if got := semantic.CoverageDocumentKey("project", "generation", "source", "path|part"); got != first {
		t.Fatalf("same identity key changed: %q != %q", got, first)
	}
	if got := semantic.CoverageDocumentKey("project", "generation", "source|path", "part"); got == first {
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

	page, err := adapter.readSemanticCoveragePage(context.Background(), semanticCoveragePageRequest{
		Identity:   SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: "build-2", SourceKind: "retained_vertex", SchemaIdentity: "schema"},
		RecordKind: semanticCoverageEnvelopeRecordKind, AuthResourcePaths: []string{"Patient/allowed"}, Limit: 2,
	})
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
	if _, err := adapter.readSemanticCoveragePage(context.Background(), semanticCoveragePageRequest{
		Identity:   SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", SourceKind: "retained_vertex", SchemaIdentity: "schema"},
		RecordKind: "source", AuthResourcePathsUnrestricted: true, Limit: 2,
	}); !errors.Is(err, ErrInvalidSemanticCoverageRead) {
		t.Fatalf("missing-build error = %v, want invalid read request", err)
	}
	if client.queryCalls != 0 {
		t.Fatalf("invalid request executed %d queries", client.queryCalls)
	}

	client.queryErr = errors.New("injected read error")
	_, err = adapter.readSemanticCoveragePage(context.Background(), semanticCoveragePageRequest{
		Identity:   SemanticCoverageBuildIdentity{Project: "project", DatasetGeneration: "generation", BuildID: "build", SourceKind: "file", SchemaIdentity: "schema"},
		RecordKind: "summary", AuthResourcePathsUnrestricted: true, AfterKey: "cursor", Limit: 2,
	})
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
