package arango

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestSemanticCoverageResourceArangoPartialScopeEmptyAndReplay(t *testing.T) {
	ctx, client, adapter, identity := openSemanticCoverageResourceIntegration(t)
	if err := adapter.BeginSemanticCoverageBuild(ctx, identity); err != nil {
		t.Fatalf("begin resource coverage build failed (%T)", err)
	}

	// These physical source locators represent retained records with the same
	// logical FHIR id. The source envelope key must keep both physical records.
	specimenA := semanticCoverageResourceContribution(identity, "Specimen", "Patient/scope-a", "retained:Specimen/physical-a/logical-shared", "type", "type.coding[].code", true)
	specimenB := semanticCoverageResourceContribution(identity, "Specimen", "Patient/scope-b", "retained:Specimen/physical-b/logical-shared", "type", "type.coding[].code", false)
	specimenContributions := []catalog.SemanticCoverageSourceContribution{specimenA, specimenB}
	if _, err := adapter.WriteSemanticCoverageContributions(ctx, identity.BuildID, specimenContributions, 2); err != nil {
		t.Fatalf("write Specimen source contributions failed (%T)", err)
	}
	assertSemanticCoverageEnvelopes(t, ctx, client, identity, specimenContributions)
	specimenExpectation := semanticCoverageResourceExpectation(t, "Specimen", specimenContributions)
	if err := adapter.CompleteSemanticCoverageResource(ctx, identity, specimenExpectation, 2); err != nil {
		t.Fatalf("complete Specimen resource certificate failed (%T)", err)
	}
	if err := adapter.MarkSemanticCoverageResourceMissing(ctx, identity, "Task"); err != nil {
		t.Fatalf("mark missing Task collection failed (%T)", err)
	}
	emptyPatient := NewSemanticCoverageSourceSetAccumulator("Patient", true)
	patientExpectation, err := emptyPatient.Snapshot(true)
	if err != nil {
		t.Fatalf("record empty Patient source set failed (%T)", err)
	}
	if err := adapter.CompleteSemanticCoverageResource(ctx, identity, patientExpectation, 2); err != nil {
		t.Fatalf("complete existing empty Patient resource failed (%T)", err)
	}
	expectedResources := []string{"Patient", "Specimen", "Task"}
	if err := adapter.FinalizeSemanticCoverageBuild(ctx, identity, expectedResources); err != nil {
		t.Fatalf("finalize partial global coverage audit failed (%T)", err)
	}

	specimenCandidate := SemanticCoverageCandidatePath{ResourceType: "Specimen", CanonicalPath: "type.coding[].code"}
	lookup := func(paths []string, unrestricted bool, candidate SemanticCoverageCandidatePath) SemanticCoverageLookupResult {
		t.Helper()
		result, err := adapter.LookupSemanticCoverage(ctx, SemanticCoverageLookupOptions{
			Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: identity.BuildID,
			SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind,
			AuthResourcePaths: paths, AuthResourcePathsUnrestricted: unrestricted,
			Candidates: []SemanticCoverageCandidatePath{candidate},
		})
		if err != nil {
			t.Fatalf("lookup resource-scoped coverage failed (%T)", err)
		}
		return result
	}

	aOnly := lookup([]string{"Patient/scope-a"}, false, specimenCandidate)
	if aOnly.State != SemanticCoverageBuildPartial || aOnly.Candidates[0].ResourceState != SemanticCoverageBuildComplete ||
		aOnly.Candidates[0].Status != SemanticCoverageCandidateGroupable || len(aOnly.Candidates[0].References) == 0 {
		t.Fatalf("complete Specimen auth-A candidate under partial audit = %+v, want exact groupable proof", aOnly)
	}
	for _, test := range []struct {
		name         string
		paths        []string
		unrestricted bool
	}{
		{name: "auth A plus B", paths: []string{"Patient/scope-a", "Patient/scope-b"}},
		{name: "unrestricted", unrestricted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := lookup(test.paths, test.unrestricted, specimenCandidate)
			if result.State != SemanticCoverageBuildPartial || result.Candidates[0].ResourceState != SemanticCoverageBuildComplete ||
				result.Candidates[0].Status == SemanticCoverageCandidateGroupable || len(result.Candidates[0].References) != 0 {
				t.Fatalf("combined Specimen auth candidate = %+v, want no groupable proof", result)
			}
		})
	}

	task := lookup([]string{"Patient/scope-a"}, false, SemanticCoverageCandidatePath{ResourceType: "Task", CanonicalPath: "code.coding[].code"})
	if task.State != SemanticCoverageBuildPartial || task.Candidates[0].ResourceState != SemanticCoverageBuildMissing ||
		task.Candidates[0].ResourceReason != "SOURCE_COLLECTION_MISSING" || task.Candidates[0].Status != SemanticCoverageCandidateUnavailable || len(task.Candidates[0].References) != 0 {
		t.Fatalf("missing Task resource candidate = %+v, want distinct MISSING certificate and no refs", task)
	}
	patient := lookup(nil, true, SemanticCoverageCandidatePath{ResourceType: "Patient", CanonicalPath: "identifier.type.coding[].code"})
	if patient.State != SemanticCoverageBuildPartial || patient.Candidates[0].ResourceState != SemanticCoverageBuildComplete ||
		patient.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || len(patient.Candidates[0].References) != 0 {
		t.Fatalf("existing empty Patient resource candidate = %+v, want COMPLETE certificate with raw absence", patient)
	}

	staleBuild, err := adapter.LookupSemanticCoverage(ctx, SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: "stale-build",
		SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind,
		AuthResourcePaths: []string{"Patient/scope-a"}, Candidates: []SemanticCoverageCandidatePath{specimenCandidate},
	})
	if err != nil || staleBuild.State != SemanticCoverageBuildUnavailable || staleBuild.Reason != "BUILD_ID_MISMATCH" || len(staleBuild.Candidates[0].References) != 0 {
		t.Fatalf("stale build lookup = %+v, err=%T; want unavailable identity proof", staleBuild, err)
	}
	staleSchema, err := adapter.LookupSemanticCoverage(ctx, SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: identity.BuildID,
		SchemaIdentity: strings.Repeat("c", 64), SourceKind: identity.SourceKind,
		AuthResourcePaths: []string{"Patient/scope-a"}, Candidates: []SemanticCoverageCandidatePath{specimenCandidate},
	})
	if err != nil || staleSchema.State != SemanticCoverageBuildUnavailable || len(staleSchema.Candidates[0].References) != 0 {
		t.Fatalf("stale schema lookup = %+v, err=%T; want unavailable identity proof", staleSchema, err)
	}

	if err := adapter.BeginSemanticCoverageBuild(ctx, identity); err != nil {
		t.Fatalf("begin resource replay failed (%T)", err)
	}
	building, err := adapter.LookupSemanticCoverage(ctx, SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: identity.BuildID,
		SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind,
		AuthResourcePaths: []string{"Patient/scope-a"}, Candidates: []SemanticCoverageCandidatePath{specimenCandidate},
	})
	if err != nil || building.State != SemanticCoverageBuildBuilding || len(building.Candidates[0].References) != 0 {
		t.Fatalf("in-progress resource replay lookup = %+v, err=%T; want BUILDING and no refs", building, err)
	}
	if err := adapter.BeginSemanticCoverageResource(ctx, identity, "Specimen"); err != nil {
		t.Fatalf("invalidate Specimen certificate before envelope replacement failed (%T)", err)
	}
	replacedSpecimenA := specimenA
	replacedSpecimenA.Identity.AuthResourcePath = "Patient/scope-c"
	if oldKey, newKey := semanticCoverageSourceEnvelopeKey(specimenA.Identity, identity.BuildID), semanticCoverageSourceEnvelopeKey(replacedSpecimenA.Identity, identity.BuildID); oldKey != newKey {
		t.Fatalf("auth-path drift changed the physical envelope key: before=%q after=%q", oldKey, newKey)
	}
	replayedContributions := []catalog.SemanticCoverageSourceContribution{replacedSpecimenA, specimenB}
	firstReplay, err := adapter.WriteSemanticCoverageContributions(ctx, identity.BuildID, replayedContributions, 2)
	if err != nil {
		t.Fatalf("replace Specimen source envelope after auth-path drift failed (%T)", err)
	}
	secondReplay, err := adapter.WriteSemanticCoverageContributions(ctx, identity.BuildID, replayedContributions, 2)
	if err != nil {
		t.Fatalf("replay replaced Specimen source envelopes failed (%T)", err)
	}
	if firstReplay.Rows != 2 || secondReplay.Rows != 2 {
		t.Fatalf("source envelope replacement/replay rows = (%d, %d), want two physical sources each time", firstReplay.Rows, secondReplay.Rows)
	}
	replayedExpectation := semanticCoverageResourceExpectation(t, "Specimen", replayedContributions)
	if err := adapter.CompleteSemanticCoverageResource(ctx, identity, replayedExpectation, 2); err != nil {
		t.Fatalf("republish Specimen resource certificate failed (%T)", err)
	}
	if err := adapter.FinalizeSemanticCoverageBuild(ctx, identity, expectedResources); err != nil {
		t.Fatalf("refinalize partial global audit after resource replay failed (%T)", err)
	}
	assertSemanticCoverageEnvelopes(t, ctx, client, identity, replayedContributions)
	oldPartition := lookup([]string{"Patient/scope-a"}, false, specimenCandidate)
	if oldPartition.State != SemanticCoverageBuildPartial || oldPartition.Candidates[0].ResourceState != SemanticCoverageBuildComplete ||
		oldPartition.Candidates[0].Status == SemanticCoverageCandidateGroupable || len(oldPartition.Candidates[0].References) != 0 {
		t.Fatalf("old authorization partition after source auth-path drift = %+v, want no old proof", oldPartition)
	}
	replayed := lookup([]string{"Patient/scope-c"}, false, specimenCandidate)
	if replayed.State != SemanticCoverageBuildPartial || replayed.Candidates[0].ResourceState != SemanticCoverageBuildComplete ||
		replayed.Candidates[0].Status != SemanticCoverageCandidateGroupable || len(replayed.Candidates[0].References) == 0 {
		t.Fatalf("replayed Specimen auth-A proof = %+v, want current groupable proof", replayed)
	}
	for _, test := range []struct {
		name         string
		paths        []string
		unrestricted bool
	}{
		{name: "scope C plus B", paths: []string{"Patient/scope-b", "Patient/scope-c"}},
		{name: "unrestricted after replacement", unrestricted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := lookup(test.paths, test.unrestricted, specimenCandidate)
			if result.Candidates[0].Status == SemanticCoverageCandidateGroupable || len(result.Candidates[0].References) != 0 {
				t.Fatalf("combined auth candidate after replacement = %+v, want no groupable proof", result)
			}
		})
	}
}

func TestSemanticCoverageResourceArangoRejectsMismatchedSourceEnvelope(t *testing.T) {
	ctx, client, adapter, identity := openSemanticCoverageResourceIntegration(t)
	if err := adapter.BeginSemanticCoverageBuild(ctx, identity); err != nil {
		t.Fatalf("begin mismatched-sentinel coverage build failed (%T)", err)
	}
	contribution := semanticCoverageResourceContribution(identity, "Specimen", "Patient/scope-a", "retained:Specimen/source-a", "type", "type.coding[].code", true)
	if _, err := adapter.WriteSemanticCoverageContributions(ctx, identity.BuildID, []catalog.SemanticCoverageSourceContribution{contribution}, 2); err != nil {
		t.Fatalf("write Specimen source envelope through the adapter failed (%T)", err)
	}
	const forgedSourceID = "retained:Specimen/forged-source"
	mutateEnvelope := `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.source_kind == @source_kind
  FILTER d.schema_identity == @schema_identity
  FILTER d.resource_type == @resource_type
  FILTER d.source_id == @source_id
  FILTER d.record_kind == @record_kind
  UPDATE d WITH {source_id: @forged_source_id, source_record_count: 0, complete: false} IN fhir_semantic_coverage`
	if err := client.ExecuteAQL(ctx, mutateEnvelope, map[string]interface{}{
		"project": identity.Project, "dataset_generation": identity.DatasetGeneration,
		"build_id": identity.BuildID, "source_kind": identity.SourceKind,
		"schema_identity": identity.SchemaIdentity, "resource_type": "Specimen",
		"source_id": contribution.Identity.SourceID, "record_kind": semanticCoverageEnvelopeRecordKind,
		"forged_source_id": forgedSourceID,
	}); err != nil {
		t.Fatalf("corrupt isolated source envelope identity failed (%T)", err)
	}
	envelopeKey := semanticCoverageSourceEnvelopeKey(contribution.Identity, identity.BuildID)
	mutatedEnvelopeFound := false
	inspectEnvelope := `FOR d IN fhir_semantic_coverage FILTER d._key == @key RETURN {source_id: d.source_id, source_record_count: d.source_record_count, complete: d.complete}`
	if err := client.QueryRows(ctx, inspectEnvelope, 1, map[string]interface{}{"key": envelopeKey}, func(row map[string]any) error {
		mutatedEnvelopeFound = true
		if row["source_id"] != forgedSourceID || row["source_record_count"] != float64(0) || row["complete"] != false {
			t.Errorf("stored source envelope after corruption = %#v", row)
		}
		return nil
	}); err != nil {
		t.Fatalf("inspect corrupted source envelope failed (%T)", err)
	}
	if !mutatedEnvelopeFound {
		t.Fatal("the adapter-written source envelope was not found for corruption")
	}
	expectation := semanticCoverageResourceExpectation(t, "Specimen", []catalog.SemanticCoverageSourceContribution{contribution})
	if err := adapter.CompleteSemanticCoverageResource(ctx, identity, expectation, 2); err != nil {
		t.Fatalf("finalize mismatched source envelope should publish a failed certificate, got (%T)", err)
	}
	if err := adapter.FinalizeSemanticCoverageBuild(ctx, identity, []string{"Specimen"}); err != nil {
		t.Fatalf("finalize global audit around failed Specimen resource failed (%T)", err)
	}
	result, err := adapter.LookupSemanticCoverage(ctx, SemanticCoverageLookupOptions{
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: identity.BuildID,
		SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind,
		AuthResourcePaths: []string{"Patient/scope-a"},
		Candidates:        []SemanticCoverageCandidatePath{{ResourceType: "Specimen", CanonicalPath: "type.coding[].code"}},
	})
	if err != nil || result.State != SemanticCoverageBuildPartial || result.Candidates[0].ResourceState != SemanticCoverageBuildFailed ||
		result.Candidates[0].ResourceReason == "" ||
		result.Candidates[0].Status != SemanticCoverageCandidateUnavailable || len(result.Candidates[0].References) != 0 {
		t.Fatalf("mismatched Specimen source-envelope lookup = %+v, err=%T; want FAILED candidate with no refs", result, err)
	}
}

func openSemanticCoverageResourceIntegration(t *testing.T) (context.Context, *arangostore.Client, *Store, SemanticCoverageBuildIdentity) {
	t.Helper()
	if os.Getenv("LOOM_SC02_COVERAGE_ARANGO_INTEGRATION") != "1" {
		t.Skip("set LOOM_SC02_COVERAGE_ARANGO_INTEGRATION=1 for isolated real-Arango resource-certificate verification")
	}
	endpoint := strings.TrimSpace(os.Getenv("LOOM_SC02_COVERAGE_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_SC02_COVERAGE_ARANGO_DATABASE"))
	if endpoint == "" || database != "loom_qa_semantic_cov_resource_20261016" || strings.ContainsAny(database, "/?#%") {
		t.Fatal("the exact pre-created loom_qa_semantic_cov_resource_20261016 isolated database is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	if !semanticCoverageResourceIntegrationDatabaseExists(ctx, endpoint, database) {
		t.Fatal("isolated SC02 database is unavailable; refusing Open because it may create a database")
	}
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("open isolated SC02 database failed (%T)", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	if err := client.Bootstrap(ctx, arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{SemanticCoverageCollectionSpec()}}); err != nil {
		t.Fatalf("bootstrap coverage collection failed (%T)", err)
	}
	adapter, err := New(client)
	if err != nil {
		t.Fatalf("construct coverage adapter failed (%T)", err)
	}
	project := "sc02-resource-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	identity := SemanticCoverageBuildIdentity{
		Project: project, DatasetGeneration: generation,
		BuildID:        catalog.SemanticInventoryBuildID(project, generation),
		SchemaIdentity: strings.Repeat("b", 64), SourceKind: catalog.SemanticInventorySourceRetained,
	}
	t.Cleanup(func() {
		cleanup := `FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  REMOVE d IN fhir_semantic_coverage`
		if err := client.ExecuteAQL(context.Background(), cleanup, map[string]interface{}{
			"project": project, "dataset_generation": generation,
		}); err != nil {
			t.Errorf("cleanup isolated resource coverage rows failed (%T)", err)
			return
		}
		remaining := 0
		query := `FOR d IN fhir_semantic_coverage FILTER d.project == @project AND d.dataset_generation == @dataset_generation LIMIT 1 RETURN d`
		if err := client.QueryRows(context.Background(), query, 1, map[string]interface{}{
			"project": project, "dataset_generation": generation,
		}, func(map[string]any) error {
			remaining++
			return nil
		}); err != nil || remaining != 0 {
			t.Errorf("isolated resource coverage cleanup was not confirmed (query error %T, remaining %d)", err, remaining)
		}
	})
	return ctx, client, adapter, identity
}

func semanticCoverageResourceIntegrationDatabaseExists(ctx context.Context, endpoint, database string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/_db/" + database + "/_api/version"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return false
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func semanticCoverageResourceExpectation(
	t *testing.T,
	resourceType string,
	contributions []catalog.SemanticCoverageSourceContribution,
) SemanticCoverageResourceExpectation {
	t.Helper()
	accumulator := NewSemanticCoverageSourceSetAccumulator(resourceType, true)
	for _, contribution := range contributions {
		if err := accumulator.AddSource(contribution.Identity.SourceID, contribution.Identity.AuthResourcePath); err != nil {
			t.Fatalf("add expected %s source identity failed (%T)", resourceType, err)
		}
	}
	expectation, err := accumulator.Snapshot(true)
	if err != nil {
		t.Fatalf("snapshot expected %s source set failed (%T)", resourceType, err)
	}
	return expectation
}

func assertSemanticCoverageEnvelopes(
	t *testing.T,
	ctx context.Context,
	client *arangostore.Client,
	identity SemanticCoverageBuildIdentity,
	contributions []catalog.SemanticCoverageSourceContribution,
) {
	t.Helper()
	keys := make([]string, 0, len(contributions))
	wantAuthPaths := make(map[string]string, len(contributions))
	for _, contribution := range contributions {
		keys = append(keys, semanticCoverageSourceEnvelopeKey(contribution.Identity, identity.BuildID))
		wantAuthPaths[contribution.Identity.SourceID] = contribution.Identity.AuthResourcePath
	}
	if len(keys) != len(wantAuthPaths) {
		t.Fatalf("fixture has %d physical envelope keys for %d distinct source locators", len(keys), len(wantAuthPaths))
	}
	seen := make(map[string]bool, len(wantAuthPaths))
	query := `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.schema_identity == @schema_identity
  FILTER d.source_kind == @source_kind
  FILTER d.record_kind == @record_kind
  FILTER d._key IN @keys
  RETURN {key: d._key, source_id: d.source_id, auth_resource_path: d.auth_resource_path}`
	if err := client.QueryRows(ctx, query, len(keys)+1, map[string]interface{}{
		"project": identity.Project, "dataset_generation": identity.DatasetGeneration,
		"build_id": identity.BuildID, "schema_identity": identity.SchemaIdentity,
		"source_kind": identity.SourceKind, "record_kind": semanticCoverageEnvelopeRecordKind, "keys": keys,
	}, func(row map[string]any) error {
		sourceID, _ := row["source_id"].(string)
		authPath, _ := row["auth_resource_path"].(string)
		if _, expected := wantAuthPaths[sourceID]; !expected || wantAuthPaths[sourceID] != authPath {
			return fmt.Errorf("unexpected source envelope row: %#v", row)
		}
		if seen[sourceID] {
			return fmt.Errorf("duplicate physical source envelope for %q", sourceID)
		}
		seen[sourceID] = true
		return nil
	}); err != nil {
		t.Fatalf("read source envelopes after replace/replay failed (%T)", err)
	}
	if len(seen) != len(wantAuthPaths) {
		t.Fatalf("materialized %d source envelopes for %d physical source locators", len(seen), len(wantAuthPaths))
	}
}

func semanticCoverageResourceContribution(
	identity SemanticCoverageBuildIdentity,
	resourceType, authPath, sourceID, ownerPath, candidatePath string,
	groupable bool,
) catalog.SemanticCoverageSourceContribution {
	disposition := catalog.SemanticCoverageSemanticDisposition
	var reason catalog.SemanticCoverageReason
	var references []catalog.SemanticCoverageReference
	if groupable {
		references = []catalog.SemanticCoverageReference{{
			Kind: catalog.SemanticCoverageExactReference, ConceptID: "concept-" + strings.ToLower(resourceType),
			BindingID: "binding-" + strings.ToLower(resourceType), CandidatePath: candidatePath, ResourceType: resourceType,
		}}
	} else {
		disposition = catalog.SemanticCoverageRawRetainedDisposition
		reason = catalog.SemanticCoverageReasonUnsupportedSelector
	}
	return catalog.SemanticCoverageSourceContribution{
		Identity: catalog.SemanticCoverageSourceIdentity{
			Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, AuthResourcePath: authPath,
			SchemaIdentity: identity.SchemaIdentity, SourceKind: identity.SourceKind, SourceID: sourceID, ResourceType: resourceType,
		},
		BuildID: identity.BuildID, SourceRecordCount: 1, Complete: true,
		Counts: catalog.SemanticCoverageCounts{
			CodeableConceptOwners: 1, PopulatedCodeableConceptValues: 1,
			CodingOccurrences: 1, PopulatedCodingValues: 1,
		},
		Paths: []catalog.SemanticCoveragePathAggregate{{
			CanonicalPath: ownerPath, Kind: catalog.SemanticCoverageCodeableConcept, Disposition: disposition, Reason: reason,
			OccurrenceCount: 1, OwnerCount: 1, CodingCount: 1,
			PresenceCounts: map[catalog.SemanticCoveragePresence]int64{catalog.SemanticCoveragePopulated: 1},
			References:     references,
			Facets: []catalog.SemanticCoverageFacetAggregate{{
				Path: candidatePath, Kind: "code", Disposition: disposition, Reason: reason, OccurrenceCount: 1,
				PresenceCounts: map[catalog.SemanticCoveragePresence]int64{catalog.SemanticCoveragePopulated: 1},
				References:     references,
			}},
		}},
	}
}
