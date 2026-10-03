package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestBrowseSemanticInventoryScopesAndContext(t *testing.T) {
	for _, scope := range []authscope.ReadScope{
		{Mode: authscope.ReadScopeUnrestricted},
		{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}},
		{Mode: authscope.ReadScopeRestricted},
	} {
		t.Run(fmt.Sprintf("%s-%d", scope.Mode, len(scope.AuthResourcePaths)), func(t *testing.T) {
			snapshot := readySnapshot("project-a", "generation-a", "token", scope)
			snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}, {ResourceType: "Specimen", RowRootEligible: true}}
			snapshot.Candidates = []capability.Candidate{{
				ID: "observation-value", NodeID: "n_observation", ResourceType: "Observation",
				FieldPath: "valueQuantity.value", LogicalType: "decimal", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
			}}
			calls := 0
			buildID := "build-a"
			readyObservation := semanticAuthoringEntry("ready-concept", "ready-binding", "system", "code", "").Observation
			readyObservation.Population = 3
			readyObservation.Examples = []string{"2.5 mg", "4 mg"}
			readyObservation.ExamplesTruncated = true
			readyObservation.ObservedUnits = []string{"mg", "mcg"}
			readyObservation.ObservedUnitsTruncated = true
			readyObservation.Completeness = catalog.SemanticComplete
			service := &Service{config: Config{
				Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
					return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
				}},
				SemanticInventory: func(_ context.Context, opts catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
					calls++
					if opts.Project != "project-a" || opts.DatasetGeneration != "generation-a" || opts.AuthResourcePathsUnrestricted == nil || *opts.AuthResourcePathsUnrestricted != (scope.Mode == authscope.ReadScopeUnrestricted) || len(opts.AuthResourcePaths) != len(scope.AuthResourcePaths) {
						t.Fatalf("scope lost: %#v", opts)
					}
					if opts.Cursor != "" && opts.Cursor != "inner-page" {
						t.Fatalf("cursor was not unwrapped: %q", opts.Cursor)
					}
					return catalog.SemanticInventoryPage{State: catalog.SemanticInventoryComplete, Build: catalog.SemanticInventoryBuild{BuildID: buildID, SourceAvailability: catalog.SemanticInventorySourceAvailabilityUnproven, Checkpoint: "private", ScannedResources: 9000}, Entries: []catalog.SemanticInventoryEntry{
						{ConceptID: "ready-concept", BindingID: "ready-binding", Observation: readyObservation},
						{ConceptID: "unsupported-concept", BindingID: "unsupported-binding", Observation: catalog.SemanticObservation{Key: catalog.SemanticObservationKey{System: "system", Code: "code", Version: "v1"}, Population: 3}},
					}, NextCursor: "inner-page"}, nil
				},
			}}
			req := BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation", Limit: 50}
			first, err := service.BrowseSemanticInventory(context.Background(), req)
			if err != nil || len(first.Entries) != 2 || first.Entries[0].Occurrences != 3 || len(first.Entries[0].Examples) != 2 || !first.Entries[0].ExamplesTruncated || len(first.Entries[0].ObservedUnits) != 2 || !first.Entries[0].ObservedUnitsTruncated || first.Entries[0].Completeness != catalog.SemanticComplete || first.Entries[0].Readiness.Status != authoringv2.SemanticReadinessReady || first.Entries[0].Readiness.Code != "READY" || first.Entries[0].Readiness.Message == "" || first.Entries[0].ConstructionChoice == nil || first.Entries[1].Readiness.Status != authoringv2.SemanticReadinessUnsupported || first.Entries[1].Readiness.Code == "" || first.Entries[1].Readiness.Message == "" || first.Entries[1].ConstructionChoice != nil || first.SourceAvailability != catalog.SemanticInventorySourceAvailabilityUnproven || first.ContextToken == "" || first.NextCursor == "" {
				t.Fatalf("first=%#v err=%v", first, err)
			}
			semanticSource, ok := first.Entries[0].ConstructionChoice.Source.(capability.SemanticBindingChoiceSource)
			if !ok || semanticSource.ConceptID != "ready-concept" || semanticSource.BindingID != "ready-binding" || semanticSource.ResourceType != "Observation" || semanticSource.FieldPath != "valueQuantity.value" || semanticSource.ValueSelector != "valueQuantity.value" {
				t.Fatalf("semantic construction source = %#v", first.Entries[0].ConstructionChoice.Source)
			}
			wire, err := json.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Entries []struct {
					Examples               []string `json:"examples"`
					ExamplesTruncated      bool     `json:"examplesTruncated"`
					ObservedUnits          []string `json:"observedUnits"`
					ObservedUnitsTruncated bool     `json:"observedUnitsTruncated"`
					Completeness           string   `json:"completeness"`
					ConstructionChoice     *struct {
						ChoiceID string                                `json:"choiceId"`
						Source   json.RawMessage                       `json:"source"`
						Options  []capability.ConstructionChoiceOption `json:"options"`
					} `json:"constructionChoice"`
					Readiness struct {
						Status  string `json:"status"`
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"readiness"`
				} `json:"entries"`
			}
			if err := json.Unmarshal(wire, &decoded); err != nil || len(decoded.Entries) != 2 || len(decoded.Entries[0].Examples) != 2 || !decoded.Entries[0].ExamplesTruncated || len(decoded.Entries[0].ObservedUnits) != 2 || !decoded.Entries[0].ObservedUnitsTruncated || decoded.Entries[0].Completeness != "complete" || decoded.Entries[0].ConstructionChoice == nil || decoded.Entries[0].ConstructionChoice.ChoiceID == "" || len(decoded.Entries[0].ConstructionChoice.Options) != 1 || decoded.Entries[0].ConstructionChoice.Options[0].Form != capability.ConstructionChoiceValue || decoded.Entries[0].Readiness.Status != "READY" || decoded.Entries[0].Readiness.Code != "READY" || decoded.Entries[0].Readiness.Message == "" || decoded.Entries[1].Readiness.Status != "UNSUPPORTED" {
				t.Fatalf("serialized readiness = %s decoded=%#v err=%v", wire, decoded, err)
			}
			var serializedSource struct {
				Kind      string `json:"kind"`
				ConceptID string `json:"conceptId"`
				FieldPath string `json:"fieldPath"`
			}
			if err := json.Unmarshal(decoded.Entries[0].ConstructionChoice.Source, &serializedSource); err != nil || serializedSource.Kind != "SEMANTIC" || serializedSource.ConceptID != "ready-concept" || serializedSource.FieldPath != "valueQuantity.value" {
				t.Fatalf("serialized semantic choice source=%#v err=%v", serializedSource, err)
			}
			req.Cursor = first.NextCursor
			second, err := service.BrowseSemanticInventory(context.Background(), req)
			if err != nil || second.ContextToken != first.ContextToken || calls != 2 {
				t.Fatalf("second=%#v calls=%d err=%v", second, calls, err)
			}
			req.RowRoot = "Specimen"
			if _, err := service.BrowseSemanticInventory(context.Background(), req); err == nil {
				t.Fatal("cursor accepted after changing row root")
			}
			req.RowRoot = "Observation"
			buildID = "build-b"
			if _, err := service.BrowseSemanticInventory(context.Background(), req); err == nil {
				t.Fatal("cursor accepted after changing inventory build")
			}
		})
	}
}

func TestBrowseSemanticInventoryUsesOutputToResolveDuplicateFrameIDs(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}}
	frame, err := authoringv2.NewFrameDefinition("Observation component values", "Codes and their paired values on Observation records.", capability.SemanticFrameFamily{
		BindingID: "binding-component", ResourceType: "Observation", SourcePath: "component[]",
		OwningScope: "component[]", KeyPath: "component[].code.coding[]", ValuePath: "valueInteger",
		LogicalType: "integer", RuleVersion: "semantic-rule-v1", SchemaVersion: 1,
	}, nil, capability.ConstructionChoiceValue)
	if err != nil {
		t.Fatal(err)
	}
	document := func(outputID string) authoringv2.Document {
		return authoringv2.Document{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: outputID, Title: outputID}, RootResourceType: "Observation",
			Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
			Rows:  authoringv2.RecordsRowDefinition(), Frames: []authoringv2.FrameDefinition{frame}, Columns: []authoringv2.Column{},
		}
	}
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Builder"},
		Documents: []authoringv2.Document{document("out_a"), document("out_b")},
		Tabs:      []authoringv2.Tab{{ID: "tab_a", Title: "out_a", OutputID: "out_a", Visible: true}, {ID: "tab_b", Title: "out_b", OutputID: "out_b", Order: 1, Visible: true}},
	}
	config, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "explorer", DraftConfig: config}}
	var browse catalog.SemanticInventoryPageOptions
	service := newTestService(t, store, Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(_ context.Context, options catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			browse = options
			return catalog.SemanticInventoryPage{
				State:   catalog.SemanticInventoryComplete,
				Build:   catalog.SemanticInventoryBuild{BuildID: catalog.SemanticInventoryBuildID("project-a", "generation-a"), State: catalog.SemanticInventoryComplete},
				Entries: []catalog.SemanticInventoryEntry{},
			}, nil
		},
	})
	result, err := service.BrowseSemanticInventory(context.Background(), BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: snapshot.Token, RowRoot: "Observation",
		OutputID: "out_b", FrameID: frame.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FrameID != frame.ID || result.FrameSource == nil || browse.BindingID != frame.Source.BindingID || browse.ResourceType != frame.Source.ResourceType {
		t.Fatalf("framed browse did not resolve the exact output source: result=%#v options=%#v", result, browse)
	}
	_, err = service.BrowseSemanticInventory(context.Background(), BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: snapshot.Token, RowRoot: "Observation", FrameID: frame.ID,
	})
	assertLifecycleError(t, err, ClassMalformed, "MALFORMED_REQUEST")
}

func TestBrowseSemanticInventoryAcceptsNestedSchemaDiscoveredCodedValue(t *testing.T) {
	profiler := catalog.NewProfilerForGenerationWithLimits("project-a", "generation-a", "scope", "MedicationRequest", nil, catalog.DefaultProfileLimits())
	profiler.ObservePayload(map[string]any{
		"resourceType": "MedicationRequest",
		"dosageInstruction": []any{map[string]any{
			"doseAndRate": []any{map[string]any{
				"type": map[string]any{"coding": []any{map[string]any{
					"system": "http://example.org/dose-types", "code": "daily-dose", "display": "Daily dose",
				}}},
				"doseQuantity": map[string]any{"value": 12.5, "unit": "mg"},
			}},
		}},
	}, map[string]float64{})
	var observation catalog.SemanticObservation
	for _, document := range profiler.Documents() {
		for _, candidate := range document.SemanticObservations {
			if candidate.RuleHint == catalog.SemanticRuleHintCodedValueV1 && candidate.Key.Code == "daily-dose" {
				observation = candidate
			}
		}
	}
	if observation.RuleHint == "" {
		t.Fatal("schema profiler did not emit the nested coded value")
	}
	if observation.OwningScope != "dosageInstruction[].doseAndRate[]" || observation.Key.Selector != "type.coding[]" || observation.Value.Selector != "doseQuantity.value" {
		t.Fatalf("schema profiler emitted an unexpected owner-relative contract: %#v", observation)
	}

	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ResourceType: "MedicationRequest", RowRootEligible: true}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "daily-dose", NodeID: "n_medication_request", ResourceType: "MedicationRequest",
		FieldPath: "dosageInstruction[].doseAndRate[].doseQuantity.value", Cardinality: "optional_many",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray}, LogicalType: "decimal",
	}}
	buildID := catalog.SemanticInventoryBuildID("project-a", "generation-a")
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			return catalog.SemanticInventoryPage{
				State:   catalog.SemanticInventoryComplete,
				Build:   catalog.SemanticInventoryBuild{BuildID: buildID, State: catalog.SemanticInventoryComplete},
				Entries: []catalog.SemanticInventoryEntry{{ConceptID: "daily-dose", BindingID: "dose-binding", Observation: observation}},
			}, nil
		},
	}}
	result, err := service.BrowseSemanticInventory(context.Background(), BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: snapshot.Token, RowRoot: "MedicationRequest",
	})
	if err != nil || len(result.Entries) != 1 || result.Entries[0].Readiness.Status != authoringv2.SemanticReadinessReady || result.Entries[0].ConstructionChoice == nil {
		t.Fatalf("nested coded value was not compiler-proved: result=%#v error=%v", result, err)
	}
	source, ok := result.Entries[0].ConstructionChoice.Source.(capability.SemanticBindingChoiceSource)
	if !ok || source.FieldPath != "dosageInstruction[].doseAndRate[].doseQuantity.value" || source.OwningScope != observation.OwningScope || source.ValueSelector != source.FieldPath {
		t.Fatalf("nested construction source = %#v", result.Entries[0].ConstructionChoice.Source)
	}
}

func TestBrowseSemanticInventoryMarksMissingCompilerProofUnsupported(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}}
	observation := semanticAuthoringEntry("concept", "binding", "system", "code", "").Observation
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			return catalog.SemanticInventoryPage{Build: catalog.SemanticInventoryBuild{BuildID: "build"}, Entries: []catalog.SemanticInventoryEntry{{ConceptID: "concept", BindingID: "binding", Observation: observation}}}, nil
		},
	}}
	result, err := service.BrowseSemanticInventory(context.Background(), BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation"})
	if err != nil || len(result.Entries) != 1 || result.Entries[0].ConstructionChoice != nil || result.Entries[0].Readiness.Status != authoringv2.SemanticReadinessUnsupported || result.Entries[0].Readiness.Code != "COMPILER_PROOF_UNAVAILABLE" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestBrowseSemanticInventoryRejectsAmbiguousCompilerProof(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}}
	for _, id := range []string{"candidate-a", "candidate-b"} {
		snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
			ID: id, NodeID: id, ResourceType: "Observation", FieldPath: "valueQuantity.value", Cardinality: "optional_one",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		})
	}
	observation := semanticAuthoringEntry("concept", "binding", "system", "code", "").Observation
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			return catalog.SemanticInventoryPage{Build: catalog.SemanticInventoryBuild{BuildID: "build"}, Entries: []catalog.SemanticInventoryEntry{{ConceptID: "concept", BindingID: "binding", Observation: observation}}}, nil
		},
	}}
	result, err := service.BrowseSemanticInventory(context.Background(), BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation"})
	if err != nil || len(result.Entries) != 1 || result.Entries[0].ConstructionChoice != nil || result.Entries[0].Readiness.Code != "COMPILER_PROOF_UNAVAILABLE" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestBrowseSemanticInventoryRejectsInvalidScopeBeforeRead(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}}
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		}},
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			t.Fatal("inventory queried with mismatched scope")
			return catalog.SemanticInventoryPage{}, nil
		},
	}}
	_, err := service.BrowseSemanticInventory(context.Background(), BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation"})
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "STALE_AUTHORIZATION_SCOPE" {
		t.Fatalf("error=%v", err)
	}
}

func TestBrowseSemanticInventoryUnknownRemainsUnknown(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}}
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			return catalog.SemanticInventoryPage{State: catalog.SemanticInventoryUnknown}, nil
		},
	}}
	result, err := service.BrowseSemanticInventory(context.Background(), BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation"})
	if err != nil || result.State != catalog.SemanticInventoryUnknown || result.SourceAvailability != catalog.SemanticInventorySourceAvailabilityUnknown || result.Entries == nil || len(result.Entries) != 0 {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}
