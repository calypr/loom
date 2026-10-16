package arango

import (
	"context"
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

func TestSemanticCoverageArangoReplayAuthBuildAndSentinelVisibility(t *testing.T) {
	if os.Getenv("LOOM_SC02_COVERAGE_ARANGO_INTEGRATION") != "1" {
		t.Skip("set LOOM_SC02_COVERAGE_ARANGO_INTEGRATION=1 for isolated real-Arango coverage verification")
	}
	endpoint := strings.TrimSpace(os.Getenv("LOOM_SC02_COVERAGE_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_SC02_COVERAGE_ARANGO_DATABASE"))
	if endpoint == "" || !strings.HasPrefix(database, "sc02_") || strings.ContainsAny(database, "/?#%") {
		t.Fatal("an endpoint and pre-created sc02_-prefixed isolated database are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if !semanticCoverageIntegrationDatabaseExists(ctx, endpoint, database) {
		t.Fatal("isolated SC02 database is unavailable; refusing Open because it may create a database")
	}
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("open isolated SC02 database failed (%T)", err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{SemanticCoverageCollectionSpec()}}); err != nil {
		t.Fatalf("bootstrap coverage collection failed (%T)", err)
	}
	adapter, err := New(client)
	if err != nil {
		t.Fatalf("construct coverage adapter failed (%T)", err)
	}

	project := "sc02-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	buildID := catalog.SemanticInventoryBuildID(project, generation)
	identity := SemanticCoverageBuildIdentity{
		Project: project, DatasetGeneration: generation, BuildID: buildID,
		SchemaIdentity: strings.Repeat("b", 64), SourceKind: catalog.SemanticInventorySourceRetained,
	}
	defer func() {
		cleanup := `FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  REMOVE d IN fhir_semantic_coverage`
		if err := client.ExecuteAQL(context.Background(), cleanup, map[string]interface{}{"project": project, "dataset_generation": generation}); err != nil {
			t.Errorf("cleanup isolated SC02 project rows failed (%T)", err)
			return
		}
		remaining := 0
		query := `FOR d IN fhir_semantic_coverage FILTER d.project == @project AND d.dataset_generation == @dataset_generation LIMIT 1 RETURN d`
		if err := client.QueryRows(context.Background(), query, 1, map[string]interface{}{"project": project, "dataset_generation": generation}, func(map[string]any) error {
			remaining++
			return nil
		}); err != nil || remaining != 0 {
			t.Errorf("isolated SC02 project cleanup was not confirmed (query error %T, remaining %d)", err, remaining)
		}
	}()

	if err := adapter.BeginSemanticCoverageBuild(ctx, identity); err != nil {
		t.Fatalf("begin exact coverage build failed (%T)", err)
	}
	first := semanticCoverageFixtureContribution()
	first.BuildID = buildID
	first.Identity.Project = project
	first.Identity.DatasetGeneration = generation
	first.Identity.SchemaIdentity = identity.SchemaIdentity
	first.Identity.AuthResourcePath = "Patient/scope-a"
	first.Identity.SourceID = "retained:Observation/source-key-a"
	second := first
	second.Identity.AuthResourcePath = "Patient/scope-b"
	second.Identity.SourceID = "retained:Observation/source-key-b"
	second.Paths = append([]catalog.SemanticCoveragePathAggregate(nil), first.Paths...)
	second.Paths[0].Disposition = catalog.SemanticCoverageRawRetainedDisposition
	second.Paths[0].Reason = catalog.SemanticCoverageReasonUnsupportedSelector
	second.Paths[0].References = nil
	second.Paths[0].Facets = append([]catalog.SemanticCoverageFacetAggregate(nil), first.Paths[0].Facets...)
	for index := range second.Paths[0].Facets {
		second.Paths[0].Facets[index].Disposition = catalog.SemanticCoverageRawRetainedDisposition
		second.Paths[0].Facets[index].Reason = catalog.SemanticCoverageReasonUnsupportedSelector
		second.Paths[0].Facets[index].References = nil
	}
	if _, err := adapter.WriteSemanticCoverageContributions(ctx, buildID, []catalog.SemanticCoverageSourceContribution{first, second}, 1); err != nil {
		t.Fatalf("write exact authorized source sets failed (%T)", err)
	}
	if err := adapter.CompleteSemanticCoverageBuild(ctx, identity, 2, true, "", 1); err != nil {
		t.Fatalf("complete exact coverage build failed (%T)", err)
	}
	candidate := SemanticCoverageCandidatePath{ResourceType: "Observation", CanonicalPath: "coding[].code"}
	lookup := func(authPath string) SemanticCoverageLookupResult {
		t.Helper()
		result, err := adapter.LookupSemanticCoverage(ctx, SemanticCoverageLookupOptions{
			Project: project, DatasetGeneration: generation, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
			SourceKind: identity.SourceKind, AuthResourcePaths: []string{authPath}, Candidates: []SemanticCoverageCandidatePath{candidate},
		})
		if err != nil {
			t.Fatalf("lookup exact authorized coverage failed (%T)", err)
		}
		return result
	}
	if result := lookup(first.Identity.AuthResourcePath); result.State != SemanticCoverageBuildComplete || result.Candidates[0].Status != SemanticCoverageCandidateGroupable || len(result.Candidates[0].References) == 0 {
		t.Fatalf("scope-a real lookup state=%s candidate=%+v, want exact groupable proof", result.State, result.Candidates[0])
	}
	if result := lookup(second.Identity.AuthResourcePath); result.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || len(result.Candidates[0].References) != 0 {
		t.Fatalf("scope-b real lookup candidate=%+v, want isolated KEEP_RAW", result.Candidates[0])
	}
	stale, err := adapter.LookupSemanticCoverage(ctx, SemanticCoverageLookupOptions{
		Project: project, DatasetGeneration: generation, BuildID: "stale-build", SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{first.Identity.AuthResourcePath}, Candidates: []SemanticCoverageCandidatePath{candidate},
	})
	if err != nil || stale.State != SemanticCoverageBuildUnavailable || stale.Reason != "BUILD_ID_MISMATCH" || len(stale.Candidates[0].References) != 0 {
		t.Fatalf("stale exact-build real lookup state=%s reason=%q err=%T, want unavailable with no refs", stale.State, stale.Reason, err)
	}

	if err := adapter.BeginSemanticCoverageBuild(ctx, identity); err != nil {
		t.Fatalf("begin source replay failed (%T)", err)
	}
	updated := first
	updated.Paths = append([]catalog.SemanticCoveragePathAggregate(nil), first.Paths...)
	updated.Paths[0].CanonicalPath = "valueCodeableConcept"
	updated.Paths[0].Facets = append([]catalog.SemanticCoverageFacetAggregate(nil), first.Paths[0].Facets...)
	updated.Paths[0].Facets[0].Path = "valueCodeableConcept.coding[].code"
	updated.Paths[0].Facets[1].Path = "valueCodeableConcept.coding[].userSelected"
	partialRows, err := EncodeSemanticCoverageContribution(buildID, updated)
	if err != nil {
		t.Fatalf("encode interrupted source replay failed (%T)", err)
	}
	if err := client.ExecuteAQL(ctx, deleteSemanticCoverageSourceSetAQL, map[string]interface{}{
		"project": project, "dataset_generation": generation, "build_id": buildID,
		"source_kind": identity.SourceKind, "schema_identity": identity.SchemaIdentity,
		"resource_type": updated.Identity.ResourceType, "source_id": updated.Identity.SourceID,
	}); err != nil {
		t.Fatalf("remove prior source set failed (%T)", err)
	}
	if err := client.InsertBatchRaw(ctx, SemanticCoverageCollection, partialRows[:len(partialRows)-1], true, ""); err != nil {
		t.Fatalf("write interrupted path/facet rows failed (%T)", err)
	}
	partialPage, err := adapter.readSemanticCoveragePage(ctx, project, generation, buildID, identity.SourceKind, identity.SchemaIdentity, "path", []string{first.Identity.AuthResourcePath}, false, "", semanticCoverageMaximumPageSize)
	if err != nil || len(partialPage.Rows) != 0 {
		t.Fatalf("partial real path rows=%d err=%T, want hidden without source sentinel", len(partialPage.Rows), err)
	}
	building, err := adapter.LookupSemanticCoverage(ctx, SemanticCoverageLookupOptions{
		Project: project, DatasetGeneration: generation, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{first.Identity.AuthResourcePath}, Candidates: []SemanticCoverageCandidatePath{candidate},
	})
	if err != nil || building.State != SemanticCoverageBuildBuilding || len(building.Candidates[0].References) != 0 {
		t.Fatalf("partial real replay lookup state=%s refs=%d err=%T, want BUILDING and no refs", building.State, len(building.Candidates[0].References), err)
	}
	ordinarySibling := first
	ordinarySibling.Identity.SourceID = "retained:Observation/source-key-a2"
	boundary := first
	boundary.Identity.SourceID = "retained:Observation/source-key-boundary"
	boundary.Paths = append([]catalog.SemanticCoveragePathAggregate(nil), first.Paths...)
	boundary.Paths[0].Facets = append([]catalog.SemanticCoverageFacetAggregate(nil), first.Paths[0].Facets...)
	boundarySignature := "sc02-boundary-v1:Observation.component[]"
	boundary.Paths[0].ResourceBoundarySignature = boundarySignature
	for index := range boundary.Paths[0].Facets {
		boundary.Paths[0].Facets[index].ResourceBoundarySignature = boundarySignature
	}
	if _, err := adapter.WriteSemanticCoverageContributions(ctx, buildID, []catalog.SemanticCoverageSourceContribution{ordinarySibling, boundary}, 1); err != nil {
		t.Fatalf("write real ordinary and subtype boundary sources failed (%T)", err)
	}
	if _, err := adapter.WriteSemanticCoverageContributions(ctx, buildID, []catalog.SemanticCoverageSourceContribution{updated}, 1); err != nil {
		t.Fatalf("replay changed source set failed (%T)", err)
	}
	storedFacets, err := adapter.readSemanticCoveragePage(ctx, project, generation, buildID, identity.SourceKind, identity.SchemaIdentity, "facet", []string{first.Identity.AuthResourcePath}, false, "", semanticCoverageMaximumPageSize)
	if err != nil {
		t.Fatalf("read source facet rows before real materialization failed (%T)", err)
	}
	storedBoundarySignatures := map[string]bool{}
	var storedCandidateFacets []semanticCoverageStoredDocument
	for _, row := range storedFacets.Rows {
		var facet semanticCoverageStoredDocument
		if err := decodeSemanticCoverageRow(row, &facet); err != nil {
			t.Fatalf("decode source facet before materialization failed (%T)", err)
		}
		if facet.CanonicalPath == candidate.CanonicalPath {
			storedCandidateFacets = append(storedCandidateFacets, facet)
			storedBoundarySignatures[facet.ResourceBoundarySignature] = true
		}
	}
	if len(storedBoundarySignatures) != 2 || !storedBoundarySignatures[""] || !storedBoundarySignatures[boundarySignature] {
		t.Fatalf("real source facet signatures before materialization = %#v, rows=%+v, want ordinary and exact subtype rows", storedBoundarySignatures, storedCandidateFacets)
	}
	if err := adapter.CompleteSemanticCoverageBuild(ctx, identity, 4, true, "", 1); err != nil {
		t.Fatalf("complete replayed coverage build failed (%T)", err)
	}
	oldPath, err := adapter.LookupSemanticCoverage(ctx, SemanticCoverageLookupOptions{
		Project: project, DatasetGeneration: generation, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{first.Identity.AuthResourcePath}, Candidates: []SemanticCoverageCandidatePath{candidate},
	})
	if err != nil || oldPath.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || oldPath.Candidates[0].Reason != catalog.SemanticCoverageReasonUnsupportedSelector || len(oldPath.Candidates[0].References) != 0 {
		t.Fatalf("old path with real subtype boundary summary candidate=%+v err=%T, want KEEP_RAW/UNSUPPORTED_SELECTOR", oldPath.Candidates[0], err)
	}
	summaryPage, err := adapter.readSemanticCoveragePage(ctx, project, generation, buildID, identity.SourceKind, identity.SchemaIdentity, "summary", []string{first.Identity.AuthResourcePath}, false, "", semanticCoverageMaximumPageSize)
	if err != nil {
		t.Fatalf("read real boundary summary rows failed (%T)", err)
	}
	var boundaryFacetSummaries []semanticCoverageStoredDocument
	for _, row := range summaryPage.Rows {
		var summary semanticCoverageStoredDocument
		if err := decodeSemanticCoverageRow(row, &summary); err != nil {
			t.Fatalf("decode real boundary summary row failed (%T)", err)
		}
		if summary.RecordKind == "summary" && summary.SummaryKind == "facet" && summary.CanonicalPath == candidate.CanonicalPath {
			boundaryFacetSummaries = append(boundaryFacetSummaries, summary)
		}
	}
	if len(boundaryFacetSummaries) != 2 || boundaryFacetSummaries[0].Key == boundaryFacetSummaries[1].Key {
		t.Fatalf("real same-path facet summaries = %#v, want independent empty/nonempty boundary keys", boundaryFacetSummaries)
	}
	seenSignatures := map[string]bool{}
	for _, summary := range boundaryFacetSummaries {
		seenSignatures[summary.ResourceBoundarySignature] = true
	}
	if len(seenSignatures) != 2 || !seenSignatures[""] || !seenSignatures[boundarySignature] {
		t.Fatalf("real boundary signatures = %#v, want ordinary and exact subtype rows", seenSignatures)
	}
	sourcePage, err := adapter.readSemanticCoveragePage(ctx, project, generation, buildID, identity.SourceKind, identity.SchemaIdentity, "path", []string{first.Identity.AuthResourcePath}, false, "", semanticCoverageMaximumPageSize)
	if err != nil {
		t.Fatalf("read replaced source path rows failed (%T)", err)
	}
	currentSourcePathFound := false
	for _, row := range sourcePage.Rows {
		var sourcePath semanticCoverageStoredDocument
		if err := decodeSemanticCoverageRow(row, &sourcePath); err != nil {
			t.Fatalf("decode replaced source path failed (%T)", err)
		}
		if sourcePath.SourceID != first.Identity.SourceID {
			continue
		}
		if sourcePath.CanonicalPath == "component[].code" {
			t.Fatal("old path remains in replaced source's real path set")
		}
		if sourcePath.CanonicalPath == "valueCodeableConcept" {
			currentSourcePathFound = true
		}
	}
	if !currentSourcePathFound {
		t.Fatal("replaced source's new path row was not present")
	}
	updatedPath, err := adapter.LookupSemanticCoverage(ctx, SemanticCoverageLookupOptions{
		Project: project, DatasetGeneration: generation, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{first.Identity.AuthResourcePath}, Candidates: []SemanticCoverageCandidatePath{{ResourceType: "Observation", CanonicalPath: "valueCodeableConcept.coding[].code"}},
	})
	if err != nil || updatedPath.Candidates[0].Status != SemanticCoverageCandidateGroupable || len(updatedPath.Candidates[0].References) == 0 {
		t.Fatalf("new path after real replacement candidate=%+v err=%T, want current groupable proof", updatedPath.Candidates[0], err)
	}
	otherScope, err := adapter.LookupSemanticCoverage(ctx, SemanticCoverageLookupOptions{
		Project: project, DatasetGeneration: generation, BuildID: buildID, SchemaIdentity: identity.SchemaIdentity,
		SourceKind: identity.SourceKind, AuthResourcePaths: []string{second.Identity.AuthResourcePath}, Candidates: []SemanticCoverageCandidatePath{candidate},
	})
	if err != nil || otherScope.Candidates[0].Status != SemanticCoverageCandidateKeepRaw || len(otherScope.Candidates[0].References) != 0 {
		t.Fatalf("other source after real replacement candidate=%+v err=%T, want preserved isolated partition", otherScope.Candidates[0], err)
	}
}

func semanticCoverageIntegrationDatabaseExists(ctx context.Context, endpoint, database string) bool {
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
