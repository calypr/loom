package arango

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestRefreshSemanticInventorySourceAvailabilityUsesExactCompletedBuildCAS(t *testing.T) {
	expected := catalog.NewSemanticInventoryBuild("project", "generation", "")
	expected.State = catalog.SemanticInventoryComplete
	expected.SourceKind = catalog.SemanticInventorySourceRetained
	expected.SourceAvailability = catalog.SemanticInventorySourceAvailabilityUnproven
	expected.ScannedResources = 17
	refreshed := expected
	refreshed.SourceAvailability = catalog.SemanticInventorySourceAvailabilityVerified
	client := &evidenceClient{rows: map[string][]map[string]any{
		refreshSemanticInventorySourceAvailabilityAQL: {semanticInventoryTestRow(t, refreshed)},
	}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	got, updated, err := adapter.RefreshSemanticInventorySourceAvailability(context.Background(), expected)
	if err != nil || !updated || got.SourceAvailability != catalog.SemanticInventorySourceAvailabilityVerified {
		t.Fatalf("refreshed build = %+v updated=%t err=%v", got, updated, err)
	}
	if len(client.queries) != 1 || client.queries[0] != refreshSemanticInventorySourceAvailabilityAQL {
		t.Fatalf("queries = %v, want one source-availability CAS", client.queries)
	}
	vars := client.vars[0]
	if vars["key"] != expected.Key || vars["project"] != expected.Project || vars["dataset_generation"] != expected.DatasetGeneration || vars["build_id"] != expected.BuildID || vars["auth_resource_path"] != "" || vars["scanned_resources"] != int64(17) {
		t.Fatalf("CAS scope = %#v", vars)
	}
	for _, fragment := range []string{
		`FILTER d.state == "complete"`,
		`FILTER d.source_kind == "retained_vertex"`,
		`FILTER NOT_NULL(d.auth_resource_path, "") == @auth_resource_path`,
		`FILTER d.source_availability == "unproven"`,
		`FILTER LENGTH(NOT_NULL(d.diagnostics, [])) == 0`,
		`FILTER NOT_NULL(d.scanned_resources, 0) == @scanned_resources`,
	} {
		if !strings.Contains(client.queries[0], fragment) {
			t.Fatalf("CAS query missing %q: %s", fragment, client.queries[0])
		}
	}
}

func TestRefreshSemanticInventorySourceAvailabilityRejectsScopedBuild(t *testing.T) {
	expected := catalog.NewSemanticInventoryBuild("project", "generation", "scope-a")
	expected.State = catalog.SemanticInventoryComplete
	expected.SourceKind = catalog.SemanticInventorySourceRetained
	expected.SourceAvailability = catalog.SemanticInventorySourceAvailabilityUnproven
	client := &evidenceClient{rows: map[string][]map[string]any{}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	got, updated, err := adapter.RefreshSemanticInventorySourceAvailability(context.Background(), expected)
	if err != nil || updated || got.SourceAvailability != catalog.SemanticInventorySourceAvailabilityUnproven || len(client.queries) != 0 {
		t.Fatalf("scoped build CAS result = %+v updated=%t err=%v queries=%v", got, updated, err, client.queries)
	}
}

func TestRefreshSemanticInventorySourceAvailabilityLeavesConcurrentChangeAlone(t *testing.T) {
	expected := catalog.NewSemanticInventoryBuild("project", "generation", "")
	expected.State = catalog.SemanticInventoryComplete
	expected.SourceKind = catalog.SemanticInventorySourceRetained
	expected.SourceAvailability = catalog.SemanticInventorySourceAvailabilityUnproven
	client := &evidenceClient{rows: map[string][]map[string]any{}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	got, updated, err := adapter.RefreshSemanticInventorySourceAvailability(context.Background(), expected)
	if err != nil || updated || got.SourceAvailability != catalog.SemanticInventorySourceAvailabilityUnproven {
		t.Fatalf("concurrent CAS result = %+v updated=%t err=%v", got, updated, err)
	}
}

func TestRefreshSemanticInventorySourceAvailabilityRejectsDiagnosticBuildBeforeWrite(t *testing.T) {
	expected := catalog.NewSemanticInventoryBuild("project", "generation", "")
	expected.State = catalog.SemanticInventoryComplete
	expected.SourceKind = catalog.SemanticInventorySourceRetained
	expected.SourceAvailability = catalog.SemanticInventorySourceAvailabilityUnproven
	expected.Diagnostics = []string{"source scan diagnostic"}
	client := &evidenceClient{rows: map[string][]map[string]any{}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	got, updated, err := adapter.RefreshSemanticInventorySourceAvailability(context.Background(), expected)
	if err != nil || updated || got.SourceAvailability != catalog.SemanticInventorySourceAvailabilityUnproven || len(client.queries) != 0 {
		t.Fatalf("diagnostic build CAS result = %+v updated=%t err=%v queries=%v", got, updated, err, client.queries)
	}
}
