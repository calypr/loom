package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestFieldPresentationCoverageBuildVersionPreservesSavedFrameIdentity(t *testing.T) {
	const (
		project         = "project-a"
		generation      = "generation-a"
		conceptID       = "concept-stable-across-coverage-builds"
		bindingID       = "binding-stable-across-coverage-builds"
		previousBuildID = "semantic-inventory-build-before-coverage-v1"
	)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot(project, generation, "coverage-version-frame-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true, Populated: true}}
	entry := catalog.SemanticInventoryEntry{
		ConceptID: conceptID, BindingID: bindingID,
		Observation: semanticFrameCompatibilityObservation("quantity"),
	}
	family := semanticFrameCompatibilityFamily(t, entry)
	frame, err := authoringv2.NewFrameDefinition(
		"Measurement values", "Measurements from the saved exact semantic binding.", family, nil, capability.ConstructionChoiceValue,
	)
	if err != nil {
		t.Fatal(err)
	}
	store := legacyFrameCompatibilityStore(t, frame)
	candidate := capability.Candidate{
		ID: "observation-quantity-value", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueQuantity.value", Label: "Value", LogicalType: "decimal", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Populated: true,
	}
	snapshot.Candidates = []capability.Candidate{candidate}
	fieldChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: project, ExplorerID: "explorer",
		SourceGeneration: generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true,
		Nodes: []authoringv2.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}},
		Candidates: []authoringv2.CatalogCandidate{{
			ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
			LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
			ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &fieldChoice,
		}}, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
	}
	newBuildID := catalog.SemanticInventoryBuildID(project, generation)
	if previousBuildID == newBuildID {
		t.Fatalf("coverage-versioned inventory build ID did not change: %q", newBuildID)
	}
	currentBuildID := previousBuildID
	service := newTestService(t, store, Config{
		Capability: CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalogSnapshot },
		},
		SemanticInventory: func(_ context.Context, options catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			if options.Project != project || options.DatasetGeneration != generation || options.ResourceType != "Observation" || options.BindingID != bindingID {
				t.Fatalf("saved-frame browse scope changed: %#v", options)
			}
			return catalog.SemanticInventoryPage{
				State:   catalog.SemanticInventoryComplete,
				Build:   catalog.SemanticInventoryBuild{BuildID: currentBuildID, State: catalog.SemanticInventoryComplete},
				Entries: []catalog.SemanticInventoryEntry{entry},
			}, nil
		},
		ResolveSemanticInventorySelections: func(_ context.Context, options catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
			if len(options.References) != 1 || options.References[0] != (catalog.SemanticInventoryReference{ConceptID: conceptID, BindingID: bindingID}) {
				t.Fatalf("saved selection identity changed during reauthorization: %#v", options.References)
			}
			return semanticAuthoringInventory(snapshot, []catalog.SemanticInventoryEntry{entry}), nil
		},
	})
	request := BrowseSemanticInventoryRequest{
		Project: project, ExplorerID: "explorer", SnapshotToken: snapshot.Token, RowRoot: "Observation",
		OutputID: "out", FrameID: frame.ID,
	}
	before, err := service.BrowseSemanticInventory(context.Background(), request)
	if err != nil || len(before.Entries) != 1 || before.Entries[0].ConstructionChoice == nil {
		t.Fatalf("saved frame browse before coverage build changed: response=%#v err=%v", before, err)
	}
	if before.BuildID != previousBuildID || before.FrameSource == nil || before.FrameSource.ID != frame.ID ||
		before.FrameSource.Source.BindingID != bindingID || before.Entries[0].ConceptID != conceptID || before.Entries[0].BindingID != bindingID {
		t.Fatalf("pre-change browse did not preserve the explicit saved identity: %#v", before)
	}

	currentBuildID = newBuildID
	after, err := service.BrowseSemanticInventory(context.Background(), request)
	if err != nil || after.BuildID != newBuildID || len(after.Entries) != 1 || after.Entries[0].ConstructionChoice == nil {
		t.Fatalf("saved frame did not resolve against the coverage-versioned inventory build: response=%#v err=%v", after, err)
	}
	choiceSource, ok := after.Entries[0].ConstructionChoice.Source.(capability.SemanticBindingChoiceSource)
	if !ok || choiceSource.ConceptID != conceptID || choiceSource.BindingID != bindingID ||
		after.FrameSource == nil || after.FrameSource.ID != frame.ID || !reflect.DeepEqual(after.FrameSource.Source, family) {
		t.Fatalf("coverage build changed the saved semantic identity: source=%#v frame=%#v", after.Entries[0].ConstructionChoice.Source, after.FrameSource)
	}

	response, err := service.ApplyCommands(context.Background(), project, "explorer", authoringv2.ApplyCommandsRequest{
		CommandID: "saved-frame-after-coverage-version", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{
			Type: authoringv2.CommandApplyConstructionChoice, OutputID: "out", InitialPresentation: authoringv2.InitialPresentationTable,
			ConstructionChoice: &authoringv2.ConstructionChoiceSelection{
				ChoiceID: after.Entries[0].ConstructionChoice.ChoiceID, Form: capability.ConstructionChoiceValue, FrameID: frame.ID,
			},
		}},
	}, "alice")
	if err != nil {
		t.Fatalf("same saved semantic binding did not add under the coverage-versioned build: %v", err)
	}
	if len(response.Workspace.Documents) != 1 || len(response.Workspace.Documents[0].Columns) != 1 {
		t.Fatalf("saved frame add produced workspace=%#v", response.Workspace)
	}
	added := response.Workspace.Documents[0]
	if added.Columns[0].FrameID != frame.ID || added.Columns[0].Source.Lookup == nil || added.Columns[0].Source.Lookup.Binding == nil ||
		added.Columns[0].Source.Lookup.Binding.ValuePath != family.ValuePath || len(added.Frames) != 1 ||
		added.Frames[0].ID != frame.ID || added.Frames[0].Source.BindingID != bindingID {
		t.Fatalf("saved frame add changed its durable semantic binding: %#v", added)
	}
}
