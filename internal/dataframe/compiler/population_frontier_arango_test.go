package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestPopulationMappingRepeatedRouteDeduplicatesScopedFrontiersAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	observations := "loom_population_frontier_observations_" + suffix
	patients := "loom_population_frontier_patients_" + suffix
	edges := "loom_population_frontier_edges_" + suffix
	members := "loom_population_frontier_members_" + suffix
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: observations}, {Name: patients}, {Name: edges, Edge: true}, {Name: members},
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := dropPopulationFrontierCollections(cleanupCtx, os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE"), edges, members, patients, observations); err != nil {
			t.Errorf("remove owned population frontier collections: %v", err)
		}
	})

	project := "population-frontier-" + suffix
	generation := "generation-" + suffix
	selectionID := "selection-" + suffix
	type resource struct {
		collection, key, id, resourceType, project, generation, authPath string
	}
	insertResource := func(value resource) {
		t.Helper()
		payload := map[string]any{"resourceType": value.resourceType, "id": value.id}
		document, err := json.Marshal(map[string]any{
			"_key": value.key, "id": value.id, "project": value.project, "project_id": value.project,
			"dataset_generation": value.generation, "resourceType": value.resourceType,
			"auth_resource_path": value.authPath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, value.collection, []json.RawMessage{document}, false, "document"); err != nil {
			t.Fatalf("insert %s fixture %s: %v", value.resourceType, value.id, err)
		}
	}
	type edgeScope struct{ project, generation, authPath string }
	insertEdge := func(key, observationKey, patientKey string, scope edgeScope) {
		t.Helper()
		document, err := json.Marshal(map[string]any{
			"_key": key, "_from": observations + "/" + observationKey, "_to": patients + "/" + patientKey,
			"project": scope.project, "project_id": scope.project, "dataset_generation": scope.generation,
			"auth_resource_path": scope.authPath, "label": "subject_Patient",
			"from_type": "Observation", "to_type": "Patient",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, edges, []json.RawMessage{document}, false, "document"); err != nil {
			t.Fatalf("insert population route edge %s: %v", key, err)
		}
	}
	allowed := edgeScope{project: project, generation: generation, authPath: "/allowed"}
	observationIDs := make([]string, 35)
	for index := range observationIDs {
		key := fmt.Sprintf("observation-%02d", index)
		observationIDs[index] = key
		insertResource(resource{observations, key, key, "Observation", project, generation, "/allowed"})
	}
	patientIDs := []string{"patient-0", "patient-1"}
	for _, key := range patientIDs {
		insertResource(resource{patients, key, key, "Patient", project, generation, "/allowed"})
	}
	for _, observationID := range observationIDs {
		for _, patientID := range patientIDs {
			insertEdge("core-"+observationID+"-"+patientID, observationID, patientID, allowed)
		}
	}

	// Each bad resource is attached by correctly scoped edges. If the resource
	// scope is dropped, it leads to an otherwise disconnected root observation.
	for index, mismatch := range []resource{
		{patients, "patient-wrong-project", "patient-wrong-project", "Patient", "other-" + project, generation, "/allowed"},
		{patients, "patient-wrong-generation", "patient-wrong-generation", "Patient", project, "other-" + generation, "/allowed"},
		{patients, "patient-denied", "patient-denied", "Patient", project, generation, "/denied"},
	} {
		observationID := fmt.Sprintf("observation-resource-scope-%d", index)
		insertResource(resource{observations, observationID, observationID, "Observation", project, generation, "/allowed"})
		insertResource(mismatch)
		insertEdge("resource-scope-selected-"+mismatch.key, observationIDs[0], mismatch.key, allowed)
		insertEdge("resource-scope-witness-"+mismatch.key, observationID, mismatch.key, allowed)
	}

	// Each bad edge is the only path from a selected source to its disconnected
	// bridge. Its valid bridge edge makes an omitted edge-scope filter observable.
	for index, mismatch := range []edgeScope{
		{project: "other-" + project, generation: generation, authPath: "/allowed"},
		{project: project, generation: "other-" + generation, authPath: "/allowed"},
		{project: project, generation: generation, authPath: "/denied"},
	} {
		patientID := fmt.Sprintf("patient-edge-scope-%d", index)
		observationID := fmt.Sprintf("observation-edge-scope-%d", index)
		insertResource(resource{patients, patientID, patientID, "Patient", project, generation, "/allowed"})
		insertResource(resource{observations, observationID, observationID, "Observation", project, generation, "/allowed"})
		insertEdge("edge-scope-selected-"+patientID, observationIDs[0], patientID, mismatch)
		insertEdge("edge-scope-witness-"+patientID, observationID, patientID, allowed)
	}

	missingObservationID := "missing-member"
	deniedObservationID := "observation-denied-member"
	insertResource(resource{observations, deniedObservationID, deniedObservationID, "Observation", project, generation, "/denied"})
	for _, patientID := range patientIDs {
		insertEdge("denied-member-"+patientID, deniedObservationID, patientID, allowed)
	}
	memberIDs := []string{observationIDs[0], observationIDs[1], deniedObservationID, missingObservationID}
	memberDocuments := make([]json.RawMessage, 0, len(memberIDs))
	for index, id := range memberIDs {
		document, err := json.Marshal(map[string]any{
			"_key": fmt.Sprintf("member-%d", index), "selectionId": selectionID, "project": project,
			"generation": generation, "resourceType": "Observation", "id": id,
		})
		if err != nil {
			t.Fatal(err)
		}
		memberDocuments = append(memberDocuments, document)
	}
	if err := client.InsertBatchRaw(ctx, members, memberDocuments, false, "document"); err != nil {
		t.Fatalf("insert population members: %v", err)
	}

	route := make([]recipe.PopulationRouteStep, 8)
	for index := range route {
		resourceType := "Observation"
		if index%2 == 0 {
			resourceType = "Patient"
		}
		route[index] = recipe.PopulationRouteStep{ResourceType: resourceType, Relationship: "subject_Patient"}
	}
	outputDefinition := recipe.Output{
		Name: "Observations", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: selectionID, MembershipDigest: "sha256:" + suffix,
			MemberCount: 4, ResourceType: "Observation", Route: route,
		},
	}
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		SelectionMembersCollection: members, AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"/allowed"},
	}
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population-frontier-regression",
		TranslationVersion: "population-frontier-regression", Outputs: []recipe.Output{outputDefinition},
	}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-"+suffix, generation)
	if err != nil {
		t.Fatal(err)
	}
	compiledRecipe, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	output := compiledRecipe.Outputs[0]
	compiled, err := CompilePopulationMappingOutputWithPolicy(output, bindings, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	compiled.BindVars["@root_collection"] = observations
	compiled.BindVars["@population_source_collection"] = observations
	compiled.BindVars["@population_members_collection"] = members
	for index := range route {
		compiled.BindVars[fmt.Sprintf("@population_root_route_%d_edge_collection", index)] = edges
	}

	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	profile, err := client.Profile(queryCtx, store.ProfileRequest{
		Query: compiled.Query, BindVars: compiled.BindVars, BatchSize: 256,
		Count: true, Options: store.ProfileOptions{Profile: 2},
	})
	if err != nil {
		t.Fatalf("execute scoped 8-hop population mapping query: %v\n%s", err, compiled.Query)
	}
	if profile.HasMore || profile.Count != 70 || len(profile.Result) != 70 {
		t.Fatalf("scoped route returned count=%d rows=%d hasMore=%v stats=%#v binds=%#v, want 70 member/root witnesses:\n%s", profile.Count, len(profile.Result), profile.HasMore, profile.Extra.Stats, compiled.BindVars, compiled.Query)
	}
	wantRoots := make(map[string]struct{}, len(observationIDs))
	for _, id := range observationIDs {
		wantRoots[id] = struct{}{}
	}
	witnesses := make(map[string]map[string]struct{}, 2)
	for _, raw := range profile.Result {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatal(err)
		}
		memberID, ok := row[compiled.MemberColumn].(string)
		if !ok || (memberID != observationIDs[0] && memberID != observationIDs[1]) {
			t.Fatalf("unexpected selected-member witness: %#v", row)
		}
		parts, ok := row[compiled.IdentityPartsColumn].([]any)
		if !ok || len(parts) != 2 || parts[0] != project {
			t.Fatalf("root identity parts = %#v, want project and resource key", row[compiled.IdentityPartsColumn])
		}
		rootKey, ok := parts[1].(string)
		if !ok {
			t.Fatalf("root key identity part = %#v", parts[1])
		}
		if _, allowed := wantRoots[rootKey]; !allowed {
			t.Fatalf("route crossed an inaccessible or disconnected resource to root %q", rootKey)
		}
		if witnesses[memberID] == nil {
			witnesses[memberID] = make(map[string]struct{})
		}
		if _, duplicate := witnesses[memberID][rootKey]; duplicate {
			t.Fatalf("duplicate witness for selected member %q and root %q", memberID, rootKey)
		}
		witnesses[memberID][rootKey] = struct{}{}
	}
	for _, memberID := range observationIDs[:2] {
		if got := len(witnesses[memberID]); got != len(wantRoots) {
			t.Fatalf("selected member %q reaches %d distinct roots, want all %d overlapping roots", memberID, got, len(wantRoots))
		}
	}
	if len(witnesses) != 2 {
		t.Fatalf("mapped member count = %d, want two accessible selected members", len(witnesses))
	}
	stats := store.SummarizeProfile(profile)
	t.Logf("8-hop overlapping-member query scanned index=%d full=%d peak=%d bytes (%.1f MiB) profile-runtime=%.3fs", stats.ScannedIndex, stats.ScannedFull, stats.PeakMemory, float64(stats.PeakMemory)/(1024*1024), stats.RuntimeSeconds)
	scanned := stats.ScannedIndex + stats.ScannedFull
	if scanned > 50000 {
		t.Fatalf("per-hop frontier work was not bounded: scanned %d index and %d full entries", stats.ScannedIndex, stats.ScannedFull)
	}
	const peakMemoryBudget = uint64(32 << 20)
	if stats.PeakMemory > peakMemoryBudget {
		t.Fatalf("per-hop frontier memory = %d bytes, want at most %d", stats.PeakMemory, peakMemoryBudget)
	}
}

func dropPopulationFrontierCollections(ctx context.Context, baseURL, database string, names ...string) error {
	for _, name := range names {
		endpoint := strings.TrimRight(baseURL, "/") + "/_db/" + url.PathEscape(database) + "/_api/collection/" + url.PathEscape(name)
		request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
		if err != nil {
			return fmt.Errorf("create drop request for %s: %w", name, err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return fmt.Errorf("drop owned collection %s: %w", name, err)
		}
		_ = response.Body.Close()
		if response.StatusCode >= http.StatusBadRequest && response.StatusCode != http.StatusNotFound {
			return fmt.Errorf("drop owned collection %s returned HTTP %d", name, response.StatusCode)
		}
	}
	return nil
}
