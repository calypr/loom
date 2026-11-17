package benchmark

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// The writable wire is closed over aggregate predicates: AggregateSource
// strict-decodes a wire without "where", so a published workspace must
// carry catalog-bound Column.Contributor intent. These tests pin both sides:
// the legacy template is refused by the same unmarshal the server's body
// bind performs, and the migrated workspace is accepted.

func preparedTestCatalog() authoringv2.CatalogSnapshot {
	return authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind,
		Project: "proj", ExplorerID: "default", SourceGeneration: "gen1",
		AuthorizationScopeDigest: "scope-digest-1", SnapshotToken: "snap-1",
		Complete: true,
		Nodes: []authoringv2.CatalogNode{
			{ID: "patient", ResourceType: "Patient"},
			{ID: "observation", ResourceType: "Observation"},
			{ID: "specimen", ResourceType: "Specimen"},
			{ID: "condition", ResourceType: "Condition"},
		},
		Candidates: []authoringv2.CatalogCandidate{
			{ID: "cand-obs-age-code", NodeID: "observation", FieldPath: "code.coding[].code",
				LogicalType: "code", Repeated: true,
				RepeatedBoundaries: []authoringv2.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 20}}},
			{ID: "cand-spec-type-code", NodeID: "specimen", FieldPath: "type.coding[].code",
				LogicalType: "code", Repeated: true,
				RepeatedBoundaries: []authoringv2.RepeatedBoundary{{Path: "type.coding[]", MaxItems: 20}}},
		},
	}
}

// strictWorkspaceBind mirrors the server's explorer-config body bind: the
// generated request body is the authoringv2 workspace decoded closed.
func strictWorkspaceBind(t *testing.T, raw []byte) authoringv2.Workspace {
	t.Helper()
	var workspace authoringv2.Workspace
	if err := json.Unmarshal(raw, &workspace); err != nil {
		t.Fatalf("server-equivalent bind must accept the published workspace: %v", err)
	}
	return workspace
}

func TestAggregateWhereTemplateIsRefusedByTheServerBind(t *testing.T) {
	raw, err := BuildWorkspace(WorkloadAggregateSumAgeByPatient)
	if err != nil {
		t.Fatal(err)
	}
	var workspace authoringv2.Workspace
	err = json.Unmarshal(raw, &workspace)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("legacy aggregate.where must fail the closed bind, got %v", err)
	}
}

func TestPreparePublicationWorkspaceMigratesAggregateWhereToContributor(t *testing.T) {
	raw, err := BuildWorkspace(WorkloadAggregateSumAgeByPatient)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := PreparePublicationWorkspace(raw, "gen1", preparedTestCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(prepared), `"where"`) {
		t.Fatalf("migrated workspace still carries the legacy where wire: %s", prepared)
	}
	workspace := strictWorkspaceBind(t, prepared)
	if workspace.SemanticsVersion < contributorSemanticsVersionForTest {
		t.Fatalf("migrated semanticsVersion=%d must be at least the contributor contract v%d", workspace.SemanticsVersion, contributorSemanticsVersionForTest)
	}
	columns := workspace.Documents[0].Columns
	if len(columns) != 5 {
		t.Fatalf("columns=%d want 5", len(columns))
	}
	for _, column := range columns[1:] {
		if column.Contributor == nil {
			t.Fatalf("column %s lost its predicate: %s", column.Column, prepared)
		}
		if column.Contributor.CandidateID != "cand-obs-age-code" || column.Contributor.Operator != authoringv2.ContributorEquals {
			t.Fatalf("column %s contributor=%#v", column.Column, column.Contributor)
		}
		if column.Contributor.Quantifier != authoringv2.ContributorAny {
			t.Fatalf("repeated candidate requires the ANY quantifier: %#v", column.Contributor)
		}
		if column.Contributor.Value == nil || column.Contributor.Value.Kind != authoringv2.ContributorValueCode ||
			column.Contributor.Value.Code == nil || column.Contributor.Value.Code.Code != ObservationCodeAge {
			t.Fatalf("column %s contributor value=%#v want CODE %s", column.Column, column.Contributor.Value, ObservationCodeAge)
		}
		if column.Source.Aggregate == nil || column.Source.Aggregate.Where != nil {
			t.Fatalf("column %s aggregate still holds a where: %#v", column.Column, column.Source.Aggregate)
		}
	}
	if columns[0].Contributor != nil {
		t.Fatalf("the id column must not gain a contributor: %#v", columns[0].Contributor)
	}
}

func TestPreparePublicationWorkspacePassesThroughWithoutPredicates(t *testing.T) {
	raw, err := BuildWorkspace(WorkloadGroupCountByPatient)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := PreparePublicationWorkspace(raw, "gen1", preparedTestCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if string(prepared) != string(raw) {
		t.Fatalf("predicate-free workspace must publish byte-identical:\n%s\n%s", raw, prepared)
	}
}

func TestPreparePublicationWorkspaceRefusesUnresolvablePredicate(t *testing.T) {
	raw, err := BuildWorkspace(WorkloadAggregateSumAgeByPatient)
	if err != nil {
		t.Fatal(err)
	}
	catalog := preparedTestCatalog()
	catalog.Candidates = catalog.Candidates[1:] // drop the observation code candidate
	if _, err := PreparePublicationWorkspace(raw, "gen1", catalog); err == nil ||
		!strings.Contains(err.Error(), "not present in the pinned catalog") {
		t.Fatalf("an unresolvable predicate path must fail closed, got %v", err)
	}
}

func TestPreparePublicationWorkspaceRefusesWrongGeneration(t *testing.T) {
	raw, err := BuildWorkspace(WorkloadAggregateSumAgeByPatient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PreparePublicationWorkspace(raw, "gen2", preparedTestCatalog()); err == nil ||
		!strings.Contains(err.Error(), "does not match benchmark generation") {
		t.Fatalf("a catalog from another generation must fail closed, got %v", err)
	}
}

func TestFetchContributorCatalogShape(t *testing.T) {
	f := newFakeArm(BackendArango)
	defer f.close()
	arm := f.arm(BackendArango)
	catalog, err := arm.FetchContributorCatalog(context.Background(), "Bearer t", "proj", "default")
	if err != nil {
		t.Fatal(err)
	}
	if catalog.SourceGeneration != "gen1" || !catalog.Complete || catalog.Truncated {
		t.Fatalf("catalog scope: %+v", catalog)
	}
	if len(catalog.Candidates) != 2 || catalog.Candidates[0].ID != "cand-obs-age-code" {
		t.Fatalf("candidates: %+v", catalog.Candidates)
	}
	if len(catalog.Nodes) != 4 {
		t.Fatalf("nodes: %+v", catalog.Nodes)
	}
}

func TestFetchContributorCatalogFailsClosed(t *testing.T) {
	cases := map[string]func(state map[string]any){
		"incomplete":      func(s map[string]any) { s["catalog"].(map[string]any)["complete"] = false },
		"truncated":       func(s map[string]any) { s["catalog"].(map[string]any)["truncated"] = true },
		"no-token":        func(s map[string]any) { delete(s["catalog"].(map[string]any), "snapshotToken") },
		"no-digest":       func(s map[string]any) { delete(s["catalog"].(map[string]any), "authorizationScopeDigest") },
		"no-generation":   func(s map[string]any) { delete(s["catalog"].(map[string]any), "generation") },
		"catalog-omitted": func(s map[string]any) { delete(s, "catalog") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			state := map[string]any{"draftVersion": 1, "catalog": contributorCatalogFixture()}
			mutate(state)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(state)
			}))
			defer server.Close()
			arm := &Arm{Backend: BackendArango, BaseURL: server.URL, Client: server.Client()}
			if _, err := arm.FetchContributorCatalog(context.Background(), "Bearer t", "proj", "default"); err == nil {
				t.Fatalf("%s: an unusable catalog must fail closed", name)
			}
		})
	}
}

// contributorSemanticsVersionForTest is the v4 contributor contract the
// migration must produce; pinned numerically so a silent constant bump in
// authoringv2 fails here loudly.
const contributorSemanticsVersionForTest = 4
