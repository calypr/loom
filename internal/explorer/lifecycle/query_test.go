package lifecycle

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestBuilderPresentsMigratedDraftWithoutWritingIt(t *testing.T) {
	visible := true
	workspace := authoringv2.Workspace{
		APIVersion:       authoringv2.APIVersion,
		Kind:             authoringv2.WorkspaceKind,
		SemanticsVersion: authoringv2.CurrentSemanticsVersion - 1,
		Explorer:         authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{
			Kind:             authoringv2.Kind,
			Output:           authoringv2.Output{ID: "patients", Title: "Patients"},
			RootResourceType: "Patient",
			Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
			Rows:             authoringv2.RecordsRowDefinition(),
			Columns: []authoringv2.Column{
				{Column: "raw_name", Label: "Raw identifier value", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "identifier[].value", ProjectionMode: "ALL"}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
				{Column: "birth_date", Label: "Birth date", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "birthDate", ProjectionMode: "VALUE"}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
			},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "patient-birth-date", NodeID: "patient", ResourceType: "Patient", FieldPath: "birthDate", Label: "Birth date", LogicalType: "date", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}}
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", DraftVersion: 7, DraftDigest: "stored-digest", DraftConfig: raw}}
	config := testConfig(snapshot)
	config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }
	config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot {
		return authoringv2.CatalogSnapshot{
			APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: "patients", SourceGeneration: "generation-a", AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
			Nodes:      []authoringv2.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}},
			Candidates: []authoringv2.CatalogCandidate{{ID: "patient-birth-date", NodeID: "patient", FieldPath: "birthDate", Label: "Birth date", LogicalType: "date", Cardinality: "optional_one", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: lifecycleTestFieldChoice(snapshot.Token, "patient-birth-date", "patient", "Patient", "birthDate", "optional_one", capability.ProjectionScalar)}},
		}
	}
	state, err := newTestService(t, store, config).Builder(context.Background(), BuilderRequest{Project: "project-a", ExplorerID: "patients"})
	if err != nil {
		t.Fatal(err)
	}
	if state.Workspace == nil || state.Workspace.SemanticsVersion != authoringv2.CurrentSemanticsVersion || len(state.Workspace.Documents[0].Columns) != 1 || state.Workspace.Documents[0].Columns[0].Column != "birth_date" {
		t.Fatalf("Builder workspace = %#v", state.Workspace)
	}
	if state.DraftVersion != 7 || state.DraftDigest != "stored-digest" || store.saveDraftCalls != 0 || string(store.created.DraftConfig) != string(raw) {
		t.Fatalf("Builder mutated storage: state=%#v calls=%d", state, store.saveDraftCalls)
	}
}
