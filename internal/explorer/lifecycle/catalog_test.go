package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog/fieldprofile"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/semanticauthoring"
	"github.com/calypr/loom/internal/explorer/semanticbrowse"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/semantic"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"
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
	entry := semantic.Entry{
		ConceptID: conceptID, BindingID: bindingID,
		Observation: semanticFrameCompatibilityObservation("quantity"),
	}
	family := semanticFrameCompatibilityFamily(t, entry)
	frame, err := ws.NewFrameDefinition(
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
	catalogSnapshot := ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: project, ExplorerID: "explorer",
		SourceGeneration: generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true,
		Nodes: []ws.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}},
		Candidates: []ws.CatalogCandidate{{
			ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
			LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
			ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &fieldChoice,
		}}, RoutePolicy: ws.RoutePolicy{Unbounded: true},
	}
	newBuildID := semantic.BuildID(project, generation)
	if previousBuildID == newBuildID {
		t.Fatalf("coverage-versioned inventory build ID did not change: %q", newBuildID)
	}
	currentBuildID := previousBuildID
	service := newTestService(t, store, Config{
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) ws.CatalogSnapshot { return catalogSnapshot },
		},
		SemanticInventory: func(_ context.Context, options semantic.PageOptions) (semantic.Page, error) {
			if options.Project != project || options.DatasetGeneration != generation || options.ResourceType != "Observation" || options.BindingID != bindingID {
				t.Fatalf("saved-frame browse scope changed: %#v", options)
			}
			return semantic.Page{
				State:   semantic.Complete,
				Build:   semantic.Build{BuildID: currentBuildID, State: semantic.Complete},
				Entries: []semantic.Entry{entry},
			}, nil
		},
		ResolveSemanticInventorySelections: func(_ context.Context, options semantic.ResolveOptions) (semantic.ResolveResult, error) {
			if len(options.References) != 1 || options.References[0] != (semantic.Reference{ConceptID: conceptID, BindingID: bindingID}) {
				t.Fatalf("saved selection identity changed during reauthorization: %#v", options.References)
			}
			return semanticAuthoringInventory(snapshot, []semantic.Entry{entry}), nil
		},
	})
	request := semanticbrowse.BrowseSemanticInventoryRequest{
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
		CommandID: "saved-frame-after-coverage-version", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionChoice, OutputID: "out", InitialPresentation: authoringv2.InitialPresentationTable, Payload: &authoringv2.ConstructionChoicePayload{ConstructionChoice: &authoringv2.ConstructionChoiceSelection{
			ChoiceID: after.Entries[0].ConstructionChoice.ChoiceID, Form: capability.ConstructionChoiceValue, FrameID: frame.ID,
		}}}},
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

func TestFieldPresentationRequestValidationAndAuthorization(t *testing.T) {
	t.Run("malformed batches stop before authorization and reads", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*semanticbrowse.BrowseSemanticInventoryRequest)
		}{
			{name: "duplicate candidate ID", mutate: func(request *semanticbrowse.BrowseSemanticInventoryRequest) {
				request.FieldPresentation.CandidateIDs = []string{"opaque-candidate", "opaque-candidate"}
			}},
			{name: "empty supplied stage", mutate: func(request *semanticbrowse.BrowseSemanticInventoryRequest) {
				stageID := ""
				request.FieldPresentation.StageID = &stageID
			}},
			{name: "too many candidates", mutate: func(request *semanticbrowse.BrowseSemanticInventoryRequest) {
				request.FieldPresentation.CandidateIDs = make([]string, semanticbrowse.FieldPresentationCandidateLimit+1)
				for index := range request.FieldPresentation.CandidateIDs {
					request.FieldPresentation.CandidateIDs[index] = "opaque-" + strconv.Itoa(index)
				}
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				fixture := newFieldPresentationTestFixture(t, false)
				request := fixture.request()
				test.mutate(&request)
				service := newTestService(t, fixture.store, fixture.config)
				_, err := service.BrowseSemanticInventory(context.Background(), request)
				assertLifecycleError(t, err, ports.ClassMalformed, "MALFORMED_REQUEST")
				if fixture.authorizationReads != 0 || fixture.inventoryReads != 0 || fixture.coverageReads != 0 {
					t.Fatalf("malformed request reached authorization or data reads: auth=%d inventory=%d coverage=%d", fixture.authorizationReads, fixture.inventoryReads, fixture.coverageReads)
				}
			})
		}
	})

	t.Run("stale draft is rejected before inventory or coverage reads", func(t *testing.T) {
		fixture := newFieldPresentationTestFixture(t, false)
		request := fixture.request()
		request.FieldPresentation.ExpectedDraftVersion++
		service := newTestService(t, fixture.store, fixture.config)
		_, err := service.BrowseSemanticInventory(context.Background(), request)
		assertLifecycleError(t, err, ports.ClassConflict, "STALE_FIELD_PRESENTATION_CONTEXT")
		if fixture.authorizationReads != 1 || fixture.inventoryReads != 0 || fixture.coverageReads != 0 {
			t.Fatalf("stale draft reached inventory or coverage reads: auth=%d inventory=%d coverage=%d", fixture.authorizationReads, fixture.inventoryReads, fixture.coverageReads)
		}
	})

	t.Run("candidate outside current source is rejected before inventory read", func(t *testing.T) {
		fixture := newFieldPresentationTestFixture(t, false)
		request := fixture.request()
		request.FieldPresentation.CandidateIDs = []string{"not-in-current-snapshot"}
		service := newTestService(t, fixture.store, fixture.config)
		_, err := service.BrowseSemanticInventory(context.Background(), request)
		assertLifecycleError(t, err, ports.ClassConflict, "STALE_FIELD_PRESENTATION_CONTEXT")
		if fixture.inventoryReads != 0 || fixture.coverageReads != 0 {
			t.Fatalf("unauthorized candidate reached inventory or coverage reads: inventory=%d coverage=%d", fixture.inventoryReads, fixture.coverageReads)
		}
	})

	t.Run("unknown supplied stage remains a structured conflict", func(t *testing.T) {
		fixture := newFieldPresentationTestFixture(t, false)
		request := fixture.request()
		stageID := "not-a-current-stage"
		request.FieldPresentation.StageID = &stageID
		service := newTestService(t, fixture.store, fixture.config)
		_, err := service.BrowseSemanticInventory(context.Background(), request)
		assertLifecycleError(t, err, ports.ClassConflict, "STALE_FIELD_PRESENTATION_CONTEXT")
		if fixture.inventoryReads != 0 || fixture.coverageReads != 0 {
			t.Fatalf("stale stage reached inventory or coverage reads: inventory=%d coverage=%d", fixture.inventoryReads, fixture.coverageReads)
		}
	})
}

func TestFieldPresentationReadsStayInsideCurrentAuthorizationScope(t *testing.T) {
	fixture := newFieldPresentationTestFixture(t, false)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}}
	snapshot := readySnapshot("project-a", "generation-a", fixture.snapshot.Token, scope)
	snapshot.Nodes = append([]capability.Node(nil), fixture.snapshot.Nodes...)
	snapshot.Candidates = append([]capability.Candidate(nil), fixture.snapshot.Candidates...)
	fixture.snapshot = snapshot
	fixture.config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		fixture.authorizationReads++
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	fixture.config.SemanticInventory = func(_ context.Context, options semantic.PageOptions) (semantic.Page, error) {
		fixture.inventoryReads++
		if options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted ||
			!reflect.DeepEqual(options.AuthResourcePaths, scope.AuthResourcePaths) {
			t.Fatalf("semantic page escaped the current authorization path: %#v", options)
		}
		return semantic.Page{
			State: semantic.Complete,
			Build: semantic.Build{
				BuildID: fixture.buildID, State: semantic.Complete, SourceKind: semantic.SourceFile,
				SourceAvailability: semantic.SourceAvailabilityVerified,
			},
			Entries: []semantic.Entry{fixture.entry},
		}, nil
	}
	fixture.config.SemanticCoverage = func(_ context.Context, options semanticarango.SemanticCoverageLookupOptions) (semanticarango.SemanticCoverageLookupResult, error) {
		fixture.coverageReads++
		if options.AuthResourcePathsUnrestricted || !reflect.DeepEqual(options.AuthResourcePaths, scope.AuthResourcePaths) {
			t.Fatalf("coverage lookup escaped the current authorization path: %#v", options)
		}
		return fixture.coverageResult, nil
	}
	fixture.config.ResolveSemanticInventorySelections = func(_ context.Context, options semantic.ResolveOptions) (semantic.ResolveResult, error) {
		if options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted ||
			!reflect.DeepEqual(options.AuthResourcePaths, scope.AuthResourcePaths) {
			t.Fatalf("replacement read escaped the current authorization path: %#v", options)
		}
		return fixture.resolvedInventory, nil
	}

	service := newTestService(t, fixture.store, fixture.config)
	response, err := service.BrowseSemanticInventory(context.Background(), fixture.request())
	if err != nil {
		t.Fatal(err)
	}
	ready, ok := response.FieldPresentation.(FieldPresentationReady)
	if !ok || len(ready.Decisions) != 1 {
		t.Fatalf("presentation=%#v, want one authorized presentation decision", response.FieldPresentation)
	}
	if _, ok := ready.Decisions[0].(FieldPresentationGroupUnderSemantic); !ok {
		t.Fatalf("restricted-scope decision=%#v, want exact authorized replacement", ready.Decisions[0])
	}
}

func TestFieldPresentationDoesNotCollapseCompilerOrCoverageReadErrors(t *testing.T) {
	t.Run("compiler proof failure", func(t *testing.T) {
		fixture := newFieldPresentationTestFixture(t, true)
		snapshot := fixture.snapshot.Clone()
		snapshot.Edges[0].StorageDirection = "OUTBOUND"
		fixture.snapshot = snapshot
		fixture.config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		}
		service := newTestService(t, fixture.store, fixture.config)
		_, err := service.BrowseSemanticInventory(context.Background(), fixture.request())
		assertLifecycleError(t, err, ports.ClassInternal, "CONSTRUCTION_ROUTE_PROOF_FAILED")
		if fixture.coverageReads != 0 {
			t.Fatalf("compiler proof failure reached coverage lookup %d times", fixture.coverageReads)
		}
	})

	t.Run("coverage read failure", func(t *testing.T) {
		fixture := newFieldPresentationTestFixture(t, false)
		fixture.config.SemanticCoverage = func(context.Context, semanticarango.SemanticCoverageLookupOptions) (semanticarango.SemanticCoverageLookupResult, error) {
			return semanticarango.SemanticCoverageLookupResult{}, errors.New("injected coverage read failure")
		}
		service := newTestService(t, fixture.store, fixture.config)
		_, err := service.BrowseSemanticInventory(context.Background(), fixture.request())
		assertLifecycleError(t, err, ports.ClassUnavailable, "PERSISTENCE_UNAVAILABLE")
	})
}

func TestFieldPresentationRequiresStageWhenOutputHasConstructionSteps(t *testing.T) {
	fixture := newFieldPresentationTestFixture(t, false)
	workspace, err := ws.DecodeWorkspace(fixture.store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Columns = []ws.Column{{
		ColumnID: "observation-id", Column: "observation_id", Label: "Observation ID", LogicalType: "string",
		OccurrenceID: ws.RootOccurrenceID,
		Source:       ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
	}}
	workspace.Documents[0].Construction = &ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{{
			ID: "filter-stage", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
				ColumnID: "observation-id", Operator: ws.ConstructionFilterExists,
			}},
			Outputs: []ws.StageColumn{{ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string"}},
		}},
	}
	persistTestWorkspace(t, fixture.store, workspace)

	service := newTestService(t, fixture.store, fixture.config)
	response, err := service.BrowseSemanticInventory(context.Background(), fixture.request())
	if err != nil {
		t.Fatal(err)
	}
	unavailableResult, ok := response.FieldPresentation.(FieldPresentationUnavailable)
	if !ok || unavailableResult.Reason != semanticbrowse.FieldPresentationUnsupportedCurrentStage {
		t.Fatalf("presentation=%#v, want unsupported-stage fallback when stageId is omitted", response.FieldPresentation)
	}
	if fixture.coverageReads != 0 || len(response.Entries) != 1 || response.Entries[0].ConstructionChoice == nil {
		t.Fatalf("ambiguous stage queried coverage or removed normal browse addability: reads=%d entries=%#v", fixture.coverageReads, response.Entries)
	}
}

func TestFieldPresentationKeepsRawWhenCoverageProofIsMissingPartialOrStale(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fieldPresentationTestFixture)
	}{
		{name: "coverage reader missing", mutate: func(fixture *fieldPresentationTestFixture) {
			fixture.config.SemanticCoverage = nil
		}},
		{name: "build still building", mutate: func(fixture *fieldPresentationTestFixture) {
			fixture.coverageResult.State = semanticarango.SemanticCoverageBuildBuilding
		}},
		{name: "requested facet missing", mutate: func(fixture *fieldPresentationTestFixture) {
			fixture.coverageResult.Candidates = nil
		}},
		{name: "replacement inventory build is stale", mutate: func(fixture *fieldPresentationTestFixture) {
			fixture.resolvedInventory.Build.BuildID = "old-inventory-build"
		}},
		{name: "reference names a different leaf facet", mutate: func(fixture *fieldPresentationTestFixture) {
			fixture.coverageResult.Candidates[0].References[0].CandidatePath = "code.coding[].userSelected"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFieldPresentationTestFixture(t, false)
			test.mutate(fixture)
			service := newTestService(t, fixture.store, fixture.config)
			response, err := service.BrowseSemanticInventory(context.Background(), fixture.request())
			if err != nil {
				t.Fatal(err)
			}
			unavailableResult, ok := response.FieldPresentation.(FieldPresentationUnavailable)
			if !ok || unavailableResult.Reason == "" {
				t.Fatalf("presentation=%#v, want structured UNAVAILABLE fallback", response.FieldPresentation)
			}
			if len(response.Entries) != 1 || response.Entries[0].ConstructionChoice == nil {
				t.Fatalf("coverage fallback removed the ordinary semantic item or its current choice: %#v", response.Entries)
			}
		})
	}
}

func TestFieldPresentationKeepsRawWhenReplacementIsUnavailableOrNotInCodedView(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fieldPresentationTestFixture)
	}{
		{name: "semantic item no longer resolves", mutate: func(fixture *fieldPresentationTestFixture) {
			fixture.resolvedInventory.Entries = nil
		}},
		{name: "semantic item is not rendered in coded view", mutate: func(fixture *fieldPresentationTestFixture) {
			fixture.resolvedInventory.Entries[0].Observation.RuleHint = "IDENTIFIER_SYSTEM_VALUE"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFieldPresentationTestFixture(t, false)
			test.mutate(fixture)
			service := newTestService(t, fixture.store, fixture.config)
			response, err := service.BrowseSemanticInventory(context.Background(), fixture.request())
			if err != nil {
				t.Fatal(err)
			}
			ready, ok := response.FieldPresentation.(FieldPresentationReady)
			if !ok || len(ready.Decisions) != 1 {
				t.Fatalf("presentation=%#v, want one READY raw-retention decision", response.FieldPresentation)
			}
			decision, ok := ready.Decisions[0].(FieldPresentationKeepRaw)
			if !ok || decision.CandidateID != fixture.candidate.ID || decision.Reason != semanticbrowse.FieldPresentationReplacementUnavailable {
				t.Fatalf("decision=%#v, want KEEP_RAW / REPLACEMENT_UNAVAILABLE", ready.Decisions[0])
			}
			if len(ready.Replacements) != 0 || len(response.Entries) != 1 || response.Entries[0].ConstructionChoice == nil {
				t.Fatalf("unavailable replacement was returned or ordinary browse lost addability: ready=%#v entries=%#v", ready, response.Entries)
			}
		})
	}
}

func TestFieldPresentationKeepsRawForClosedCoverageDisposition(t *testing.T) {
	fixture := newFieldPresentationTestFixture(t, false)
	fixture.coverageResult.Candidates[0].Status = semanticarango.SemanticCoverageCandidateKeepRaw
	fixture.coverageResult.Candidates[0].Reason = fieldprofile.SemanticCoverageReasonMissingSystem
	fixture.coverageResult.Candidates[0].References = nil
	service := newTestService(t, fixture.store, fixture.config)
	response, err := service.BrowseSemanticInventory(context.Background(), fixture.request())
	if err != nil {
		t.Fatal(err)
	}
	ready, ok := response.FieldPresentation.(FieldPresentationReady)
	if !ok || len(ready.Decisions) != 1 {
		t.Fatalf("presentation=%#v, want one READY raw-retention decision", response.FieldPresentation)
	}
	decision, ok := ready.Decisions[0].(FieldPresentationKeepRaw)
	if !ok || decision.Reason != semanticbrowse.FieldPresentationMissingSystem || decision.CandidateID != fixture.candidate.ID {
		t.Fatalf("decision=%#v, want closed MISSING_SYSTEM reason for the requested raw candidate", ready.Decisions[0])
	}
	if len(ready.Replacements) != 0 || fixture.coverageReads != 1 {
		t.Fatalf("raw coverage disposition produced replacements or skipped lookup: presentation=%#v reads=%d", ready, fixture.coverageReads)
	}
}

func TestFieldPresentationDoesNotBypassRawPopulatedOrAddableChecks(t *testing.T) {
	for _, test := range []struct {
		name       string
		mutate     func(*capability.Candidate)
		wantReason semanticbrowse.FieldPresentationReason
	}{
		{name: "candidate not populated", mutate: func(candidate *capability.Candidate) {
			candidate.Populated = false
		}, wantReason: semanticbrowse.FieldPresentationCandidateNotPopulated},
		{name: "candidate has no supported construction form", mutate: func(candidate *capability.Candidate) {
			candidate.ProjectionModes = nil
		}, wantReason: semanticbrowse.FieldPresentationCandidateNotAddable},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFieldPresentationTestFixture(t, false)
			snapshot := fixture.snapshot.Clone()
			test.mutate(&snapshot.Candidates[0])
			fixture.snapshot = snapshot
			fixture.config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			}
			service := newTestService(t, fixture.store, fixture.config)
			response, err := service.BrowseSemanticInventory(context.Background(), fixture.request())
			if err != nil {
				t.Fatal(err)
			}
			ready, ok := response.FieldPresentation.(FieldPresentationReady)
			if !ok || len(ready.Decisions) != 1 || len(ready.Replacements) != 0 {
				t.Fatalf("presentation=%#v, want one bounded raw fallback", response.FieldPresentation)
			}
			decision, ok := ready.Decisions[0].(FieldPresentationKeepRaw)
			if !ok || decision.CandidateID != fixture.candidate.ID || decision.Reason != test.wantReason {
				t.Fatalf("decision=%#v, want KEEP_RAW / %s", ready.Decisions[0], test.wantReason)
			}
		})
	}
}

func TestFieldPresentationGroupsRootCandidateAndAppliesCurrentChoice(t *testing.T) {
	assertFieldPresentationGroupsAndApplies(t, false)
}

func TestFieldPresentationGroupsExactRelatedOccurrenceAndAppliesCurrentChoice(t *testing.T) {
	assertFieldPresentationGroupsAndApplies(t, true)
}

func TestFieldPresentationGroupsRawCodingFacetUnderExistingCategoryFamily(t *testing.T) {
	assertFieldPresentationCodedFamilyProof(
		t, "Observation", "code", semanticarango.SemanticCoverageBuildComplete,
		semanticarango.SemanticCoverageBuildComplete, semanticarango.SemanticCoverageCandidateGroupable, true,
	)
}

func TestFieldPresentationGroupsSpecimenCandidateDuringPartialAudit(t *testing.T) {
	assertFieldPresentationCodedFamilyProof(
		t, "Specimen", "type", semanticarango.SemanticCoverageBuildPartial,
		semanticarango.SemanticCoverageBuildComplete, semanticarango.SemanticCoverageCandidateGroupable, true,
	)
}

func TestFieldPresentationKeepsTaskCandidateRawWithoutResourceCertificate(t *testing.T) {
	assertFieldPresentationCodedFamilyProof(
		t, "Task", "code", semanticarango.SemanticCoverageBuildPartial,
		semanticarango.SemanticCoverageBuildMissing, semanticarango.SemanticCoverageCandidateUnavailable, false,
	)
}

func assertFieldPresentationCodedFamilyProof(
	t *testing.T,
	resourceType, ownerField string,
	globalState, resourceState semanticarango.SemanticCoverageBuildState,
	candidateStatus semanticarango.SemanticCoverageCandidateStatus,
	wantGroup bool,
) {
	t.Helper()
	const (
		project    = "project-a"
		generation = "generation-a"
	)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot(project, generation, "field-presentation-family-snapshot", scope)
	const sourceNodeID = "semantic-source"
	ownerPath := ownerField + ".coding[]"
	codePath := ownerPath + ".code"
	displayPath := ownerPath + ".display"
	snapshot.Nodes = []capability.Node{{ID: sourceNodeID, ResourceType: resourceType, RowRootEligible: true}}
	codeCandidate := capability.Candidate{
		ID: "opaque-coding-code", NodeID: sourceNodeID, ResourceType: resourceType, FieldPath: codePath,
		Label: resourceType + " coding code", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: ownerPath, MaxItems: 8}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionArray}, Populated: true,
	}
	displayCandidate := capability.Candidate{
		ID: "semantic-coding-display", NodeID: sourceNodeID, ResourceType: resourceType, FieldPath: displayPath,
		Label: resourceType + " coding display", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: ownerPath, MaxItems: 8}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray}, Populated: true,
	}
	snapshot.Candidates = []capability.Candidate{codeCandidate, displayCandidate}
	family := testCodedCategoryFamily(semantic.SourceFile, resourceType, resourceType+"."+ownerPath, "urn:example:"+strings.ToLower(resourceType)+"-code", "")
	family.KeySelector = ownerPath
	family.Value.Selector = displayPath
	family.Value.FallbackSelectors = []string{codePath}
	buildID := semantic.BuildID(project, generation)
	build := semantic.Build{
		BuildID: buildID, State: semantic.Complete, SourceKind: semantic.SourceFile,
		SourceAvailability: semantic.SourceAvailabilityVerified,
	}
	codeChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, codeCandidate)
	if err != nil {
		t.Fatal(err)
	}
	displayChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, displayCandidate)
	if err != nil {
		t.Fatal(err)
	}
	store := semanticAuthoringStore(t)
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document := &workspace.Documents[0]
	document.RootResourceType = resourceType
	document.Route = ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: resourceType, Children: []ws.RouteNode{}}
	document.Columns = []ws.Column{}
	document.FixedFilters = []ws.FixedFilter{}
	document.Actions = []ws.Action{}
	persistTestWorkspace(t, store, workspace)
	catalogSnapshot := ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: project, ExplorerID: "patients",
		SourceGeneration: generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true},
		Nodes: []ws.CatalogNode{{ID: sourceNodeID, ResourceType: resourceType, RowRootEligible: true}},
		Candidates: []ws.CatalogCandidate{
			{ID: codeCandidate.ID, NodeID: codeCandidate.NodeID, FieldPath: codeCandidate.FieldPath, Label: codeCandidate.Label, LogicalType: codeCandidate.LogicalType, Cardinality: codeCandidate.Cardinality, Repeated: true, ProjectionModes: []string{"ARRAY"}, DefaultProjectionMode: "ARRAY", ConstructionChoice: &codeChoice},
			{ID: displayCandidate.ID, NodeID: displayCandidate.NodeID, FieldPath: displayCandidate.FieldPath, Label: displayCandidate.Label, LogicalType: displayCandidate.LogicalType, Cardinality: displayCandidate.Cardinality, Repeated: true, ProjectionModes: []string{"FIRST", "ARRAY"}, DefaultProjectionMode: "FIRST", ConstructionChoice: &displayChoice},
		},
	}
	coverage := semanticarango.SemanticCoverageCandidateResult{
		ResourceType: codeCandidate.ResourceType, CanonicalPath: codeCandidate.FieldPath, CoverageID: "coding-code-coverage",
		ResourceState: resourceState, Status: candidateStatus, Reason: fieldprofile.SemanticCoverageReasonIncompleteSource,
	}
	if candidateStatus == semanticarango.SemanticCoverageCandidateGroupable {
		coverage.References = []fieldprofile.SemanticCoverageReference{{
			Kind: fieldprofile.SemanticCoverageFamilyReference, BindingID: family.BindingID,
			Family: family.FamilyReference, CandidatePath: codeCandidate.FieldPath, ResourceType: codeCandidate.ResourceType,
		}}
	}
	service := newTestService(t, store, Config{
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) ws.CatalogSnapshot { return catalogSnapshot },
		},
		SemanticInventory: func(context.Context, semantic.PageOptions) (semantic.Page, error) {
			return semantic.Page{State: semantic.Complete, Build: build, Families: []semantic.Family{family}}, nil
		},
		SemanticCoverage: func(_ context.Context, options semanticarango.SemanticCoverageLookupOptions) (semanticarango.SemanticCoverageLookupResult, error) {
			if len(options.Candidates) != 1 || options.Candidates[0] != (semanticarango.SemanticCoverageCandidatePath{ResourceType: codeCandidate.ResourceType, CanonicalPath: codeCandidate.FieldPath}) {
				t.Fatalf("coverage lookup did not use the exact Coding leaf facet: %#v", options)
			}
			return semanticarango.SemanticCoverageLookupResult{State: globalState, Candidates: []semanticarango.SemanticCoverageCandidateResult{coverage}}, nil
		},
		ResolveSemanticInventorySelections: func(_ context.Context, options semantic.ResolveOptions) (semantic.ResolveResult, error) {
			if len(options.FamilyReferences) != 1 || options.FamilyReferences[0] != family.FamilyReference || len(options.References) != 0 {
				t.Fatalf("family replacement was not resolved by its exact existing identity: %#v", options)
			}
			return semantic.ResolveResult{Build: build, State: semantic.Complete, Families: []semantic.Family{family}}, nil
		},
	})
	request := semanticbrowse.BrowseSemanticInventoryRequest{
		Project: project, ExplorerID: "patients", SnapshotToken: snapshot.Token, RowRoot: resourceType, OutputID: "patients",
		FieldPresentation: &semanticbrowse.FieldPresentationRequest{
			ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
			SourceNodeID: sourceNodeID, OccurrenceID: ws.RootOccurrenceID, CandidateIDs: []string{codeCandidate.ID},
		},
	}
	response, err := service.BrowseSemanticInventory(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	ready, ok := response.FieldPresentation.(FieldPresentationReady)
	if !ok || len(ready.Decisions) != 1 {
		t.Fatalf("presentation=%#v, want one exact resource-certificate decision", response.FieldPresentation)
	}
	if wantGroup {
		if len(ready.Replacements) != 1 {
			t.Fatalf("presentation=%#v, want one existing category-family replacement", ready)
		}
		group, ok := ready.Decisions[0].(FieldPresentationGroupUnderSemantic)
		if !ok || len(group.ReplacementIDs) != 1 {
			t.Fatalf("decision=%#v, want one category-family identity", ready.Decisions[0])
		}
		familyIdentity, ok := group.ReplacementIDs[0].(FamilySemanticInventoryItemIdentity)
		if !ok || familyIdentity.FamilyID != family.FamilyID || familyIdentity.BindingID != family.BindingID {
			t.Fatalf("replacement identity=%#v, want existing system-version family", group.ReplacementIDs[0])
		}
		replacement := ready.Replacements[0]
		choiceSource, ok := replacement.ConstructionChoice.Source.(capability.SemanticBindingChoiceSource)
		if !ok || replacement.SelectionMode != string(semantic.SystemVersionCategory) ||
			replacement.FamilyID != family.FamilyID || choiceSource.RuleHint != semantic.RuleHintCodedCategoryV1 ||
			choiceSource.SourceKind != family.SourceKind || choiceSource.System != family.System || choiceSource.Version != family.Version {
			t.Fatalf("replacement is not a rendered existing coded category: %#v", replacement)
		}
	} else {
		if len(ready.Replacements) != 0 {
			t.Fatalf("presentation=%#v, incomplete Task certificate must not return replacements", ready.Replacements)
		}
		decision, ok := ready.Decisions[0].(FieldPresentationKeepRaw)
		if !ok || decision.CandidateID != codeCandidate.ID || decision.Reason != semanticbrowse.FieldPresentationIncompleteCoverage {
			t.Fatalf("decision=%#v, want KEEP_RAW / INCOMPLETE_COVERAGE", ready.Decisions[0])
		}
		if len(response.Entries) != 1 || response.Entries[0].ConstructionChoice == nil {
			t.Fatalf("incomplete Task certificate removed the existing coded browse choice: %#v", response.Entries)
		}
	}
}

func assertFieldPresentationGroupsAndApplies(t *testing.T, related bool) {
	t.Helper()
	fixture := newFieldPresentationTestFixture(t, related)
	service := newTestService(t, fixture.store, fixture.config)
	response, err := service.BrowseSemanticInventory(context.Background(), fixture.request())
	if err != nil {
		t.Fatal(err)
	}
	ready, ok := response.FieldPresentation.(FieldPresentationReady)
	if !ok || len(ready.Decisions) != 1 || len(ready.Replacements) != 1 {
		t.Fatalf("presentation=%#v, want one ready decision and one replacement", response.FieldPresentation)
	}
	group, ok := ready.Decisions[0].(FieldPresentationGroupUnderSemantic)
	if !ok || group.CandidateID != fixture.candidate.ID || group.CoverageID != "coverage-id" || group.Access != semanticbrowse.FieldPresentationAccessSourceFields || len(group.ReplacementIDs) != 1 {
		t.Fatalf("decision=%#v, want bounded semantic group with Source fields access", ready.Decisions[0])
	}
	identity, ok := group.ReplacementIDs[0].(ExactSemanticInventoryItemIdentity)
	if !ok || identity.ConceptID != fixture.entry.ConceptID || identity.BindingID != fixture.entry.BindingID {
		t.Fatalf("replacement identity=%#v, want exact current semantic item", group.ReplacementIDs[0])
	}
	if ready.Context.OutputID != "patients" || ready.Context.DraftVersion != fixture.store.created.DraftVersion ||
		ready.Context.DraftDigest != fixture.store.created.DraftDigest || ready.Context.SourceNodeID != "observation" ||
		ready.Context.OccurrenceID != fixture.occurrenceID || ready.Context.CandidateBatchDigest == "" ||
		ready.Context.PresentationRevision != semanticbrowse.SemanticFieldPresentationRevision || ready.Context.CoverageBuildID != fixture.buildID {
		t.Fatalf("presentation context omitted current output/route identity: %#v", ready.Context)
	}
	if related {
		if len(ready.Context.Route) != 1 || ready.Context.Route[0].EdgeID != "subject-patient" {
			t.Fatalf("presentation context did not bind the exact related route: %#v", ready.Context.Route)
		}
	} else if len(ready.Context.Route) != 0 {
		t.Fatalf("root presentation context unexpectedly has a related route: %#v", ready.Context.Route)
	}
	if ready.Replacements[0].ConstructionChoice == nil || len(response.Entries) != 1 || response.Entries[0].ConstructionChoice == nil {
		t.Fatalf("grouped semantic item or current browse entry has no addable choice: replacement=%#v entries=%#v", ready.Replacements, response.Entries)
	}
	choiceSource, ok := ready.Replacements[0].ConstructionChoice.Source.(capability.SemanticBindingChoiceSource)
	expectedRouteLength := 0
	if related {
		expectedRouteLength = 1
	}
	if !ok || choiceSource.ConceptID != fixture.entry.ConceptID || choiceSource.BindingID != fixture.entry.BindingID ||
		choiceSource.NodeID != "observation" || len(ready.Replacements[0].ConstructionChoice.Route) != expectedRouteLength {
		t.Fatalf("replacement choice does not bind the exact semantic source and selected route: %#v", ready.Replacements[0].ConstructionChoice)
	}
	if related && ready.Replacements[0].ConstructionChoice.Route[0].EdgeID != "subject-patient" {
		t.Fatalf("related replacement chose a route other than the saved occurrence: %#v", ready.Replacements[0].ConstructionChoice.Route)
	}

	request := constructionChoiceRequest(fixture.snapshot, "field-presentation-add", ready.Replacements[0].ConstructionChoice.ChoiceID, capability.ConstructionChoiceValue)
	request.ExpectedDraftVersion = fixture.store.created.DraftVersion
	applied, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatalf("apply current presentation replacement: %v", err)
	}
	if len(applied.Workspace.Documents) != 1 || len(applied.Workspace.Documents[0].Columns) != 1 {
		t.Fatalf("semantic replacement add produced workspace=%#v", applied.Workspace)
	}
	column := applied.Workspace.Documents[0].Columns[0]
	wantOccurrence := ws.RootOccurrenceID
	if related {
		wantOccurrence = fixture.occurrenceID
	}
	if column.OccurrenceID != wantOccurrence || column.Source.Lookup == nil || column.Source.Lookup.Binding == nil ||
		column.Source.Lookup.Binding.ValuePath != fixture.entry.Observation.Value.Selector {
		t.Fatalf("applied replacement lost its exact occurrence or semantic binding: %#v", column)
	}
}

type fieldPresentationTestFixture struct {
	store              *fakeStore
	config             Config
	snapshot           capability.Snapshot
	candidate          capability.Candidate
	entry              semantic.Entry
	buildID            string
	occurrenceID       string
	rootResourceType   string
	coverageResult     semanticarango.SemanticCoverageLookupResult
	resolvedInventory  semantic.ResolveResult
	authorizationReads int
	inventoryReads     int
	coverageReads      int
}

func newFieldPresentationTestFixture(t *testing.T, related bool) *fieldPresentationTestFixture {
	t.Helper()
	const (
		project    = "project-a"
		generation = "generation-a"
	)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot(project, generation, "field-presentation-snapshot", scope)
	rootResourceType := "Observation"
	occurrenceID := ws.RootOccurrenceID
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	if related {
		rootResourceType = "Patient"
		occurrenceID = "saved-observation"
		snapshot.Nodes = []capability.Node{
			{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
			{ID: "observation", ResourceType: "Observation"},
		}
		snapshot.Edges = []capability.Edge{{
			ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
			SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND",
		}}
	}
	candidate := capability.Candidate{
		ID: "opaque-candidate", NodeID: "observation", ResourceType: "Observation", FieldPath: "valueQuantity.value",
		Label: "Observed value", LogicalType: "decimal", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Populated: true,
	}
	snapshot.Candidates = []capability.Candidate{candidate}
	entry := *semanticAuthoringEntry("concept-a", "binding-a", "urn:system:a", "code-a", "")
	entry.Observation.Population = 4
	buildID := semantic.BuildID(project, generation)
	build := semantic.Build{
		BuildID: buildID, State: semantic.Complete, SourceKind: semantic.SourceFile,
		SourceAvailability: semantic.SourceAvailabilityVerified,
	}
	fieldChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
	if err != nil {
		t.Fatal(err)
	}
	catalogNodes := []ws.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: !related}}
	catalogEdges := []ws.CatalogEdge{}
	if related {
		catalogNodes = []ws.CatalogNode{
			{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
			{ID: "observation", ResourceType: "Observation"},
		}
		catalogEdges = []ws.CatalogEdge{{ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation", Label: "subject_Patient"}}
	}
	catalogSnapshot := ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: project, ExplorerID: "patients",
		SourceGeneration: generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true},
		Nodes: catalogNodes, Edges: catalogEdges,
		Candidates: []ws.CatalogCandidate{{
			ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
			LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality, ProjectionModes: []string{"VALUE"},
			DefaultProjectionMode: "VALUE", ConstructionChoice: &fieldChoice,
		}},
	}
	store := semanticAuthoringStore(t)
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document := &workspace.Documents[0]
	document.RootResourceType = rootResourceType
	document.Columns = []ws.Column{}
	document.FixedFilters = []ws.FixedFilter{}
	document.Actions = []ws.Action{}
	if related {
		document.Route = ws.RouteNode{
			OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient",
			Children: []ws.RouteNode{{
				OccurrenceID: occurrenceID, ResourceType: "Observation", Relationship: "subject_Patient", MatchMode: ws.RouteMatchOptional,
			}},
		}
	} else {
		document.Route = ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Observation", Children: []ws.RouteNode{}}
	}
	persistTestWorkspace(t, store, workspace)
	coverage := semanticarango.SemanticCoverageCandidateResult{
		ResourceType: candidate.ResourceType, CanonicalPath: candidate.FieldPath, CoverageID: "coverage-id",
		ResourceState: semanticarango.SemanticCoverageBuildComplete, Status: semanticarango.SemanticCoverageCandidateGroupable,
		References: []fieldprofile.SemanticCoverageReference{{
			Kind: fieldprofile.SemanticCoverageExactReference, ConceptID: entry.ConceptID, BindingID: entry.BindingID,
			CandidatePath: candidate.FieldPath, ResourceType: candidate.ResourceType,
		}},
	}
	fixture := &fieldPresentationTestFixture{
		store: store, snapshot: snapshot, candidate: candidate, entry: entry, buildID: buildID,
		occurrenceID: occurrenceID, rootResourceType: rootResourceType,
		coverageResult: semanticarango.SemanticCoverageLookupResult{
			State: semanticarango.SemanticCoverageBuildComplete, Candidates: []semanticarango.SemanticCoverageCandidateResult{coverage},
		},
		resolvedInventory: semanticAuthoringInventory(snapshot, []semantic.Entry{entry}),
	}
	fixture.resolvedInventory.Build = build
	fixture.config = Config{
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				fixture.authorizationReads++
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) ws.CatalogSnapshot { return catalogSnapshot },
		},
		SemanticInventory: func(_ context.Context, options semantic.PageOptions) (semantic.Page, error) {
			fixture.inventoryReads++
			if options.Project != project || options.DatasetGeneration != generation {
				t.Fatalf("semantic page lost immutable scope: %#v", options)
			}
			return semantic.Page{State: semantic.Complete, Build: build, Entries: []semantic.Entry{entry}}, nil
		},
		SemanticCoverage: func(_ context.Context, options semanticarango.SemanticCoverageLookupOptions) (semanticarango.SemanticCoverageLookupResult, error) {
			fixture.coverageReads++
			if options.Project != project || options.DatasetGeneration != generation || options.BuildID != buildID ||
				options.SourceKind != semantic.SourceFile || !options.AuthResourcePathsUnrestricted ||
				len(options.Candidates) != 1 || options.Candidates[0] != (semanticarango.SemanticCoverageCandidatePath{ResourceType: candidate.ResourceType, CanonicalPath: candidate.FieldPath}) {
				t.Fatalf("coverage lookup lost exact bounded scope: %#v", options)
			}
			return fixture.coverageResult, nil
		},
		ResolveSemanticInventorySelections: func(_ context.Context, options semantic.ResolveOptions) (semantic.ResolveResult, error) {
			if len(options.References) != 1 || options.References[0] != (semantic.Reference{ConceptID: entry.ConceptID, BindingID: entry.BindingID}) {
				t.Fatalf("replacement resolver did not receive the exact bounded reference: %#v", options)
			}
			return fixture.resolvedInventory, nil
		},
	}
	return fixture
}

func (fixture *fieldPresentationTestFixture) request() semanticbrowse.BrowseSemanticInventoryRequest {
	return semanticbrowse.BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: fixture.snapshot.Token,
		RowRoot: fixture.rootResourceType, OutputID: "patients",
		FieldPresentation: &semanticbrowse.FieldPresentationRequest{
			ExpectedDraftVersion: fixture.store.created.DraftVersion, ExpectedDraftDigest: fixture.store.created.DraftDigest,
			SourceNodeID: "observation", OccurrenceID: fixture.occurrenceID, CandidateIDs: []string{fixture.candidate.ID},
		},
	}
}

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
			readyObservation.Completeness = semantic.CompletenessComplete
			service := &Service{config: Config{
				Capability: ports.CapabilityResolver{ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
					return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
				}},
				SemanticInventory: func(_ context.Context, opts semantic.PageOptions) (semantic.Page, error) {
					calls++
					if opts.Project != "project-a" || opts.DatasetGeneration != "generation-a" || opts.AuthResourcePathsUnrestricted == nil || *opts.AuthResourcePathsUnrestricted != (scope.Mode == authscope.ReadScopeUnrestricted) || len(opts.AuthResourcePaths) != len(scope.AuthResourcePaths) {
						t.Fatalf("scope lost: %#v", opts)
					}
					if opts.Cursor != "" && opts.Cursor != "inner-page" {
						t.Fatalf("cursor was not unwrapped: %q", opts.Cursor)
					}
					return semantic.Page{State: semantic.Complete, Build: semantic.Build{BuildID: buildID, SourceAvailability: semantic.SourceAvailabilityUnproven, Checkpoint: "private", ScannedResources: 9000}, Entries: []semantic.Entry{
						{ConceptID: "ready-concept", BindingID: "ready-binding", Observation: readyObservation},
						{ConceptID: "unsupported-concept", BindingID: "unsupported-binding", Observation: semantic.Observation{Key: semantic.Key{System: "system", Code: "code", Version: "v1"}, Population: 3}},
					}, NextCursor: "inner-page"}, nil
				},
			}}
			req := semanticbrowse.BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation", Limit: 50}
			first, err := service.BrowseSemanticInventory(context.Background(), req)
			if err != nil || len(first.Entries) != 2 || first.Entries[0].Occurrences != 3 || len(first.Entries[0].Examples) != 2 || !first.Entries[0].ExamplesTruncated || len(first.Entries[0].ObservedUnits) != 2 || !first.Entries[0].ObservedUnitsTruncated || first.Entries[0].Completeness != semantic.CompletenessComplete || first.Entries[0].Readiness.Status != semanticauthoring.SemanticReadinessReady || first.Entries[0].Readiness.Code != "READY" || first.Entries[0].Readiness.Message == "" || first.Entries[0].ConstructionChoice == nil || first.Entries[1].Readiness.Status != semanticauthoring.SemanticReadinessUnsupported || first.Entries[1].Readiness.Code == "" || first.Entries[1].Readiness.Message == "" || first.Entries[1].ConstructionChoice != nil || first.SourceAvailability != semantic.SourceAvailabilityUnproven || first.ContextToken == "" || first.NextCursor == "" {
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

func TestBrowseSemanticInventoryOutputScopeKeepsOnlyCompilerProvenIdentifiers(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "z-observation", ResourceType: "Observation"},
		{ID: "a-specimen", ResourceType: "Specimen"},
		{ID: "substance", ResourceType: "Substance"},
		{ID: "substance-definition", ResourceType: "SubstanceDefinition"},
	}
	snapshot.Edges = []capability.Edge{
		{ID: "patient-observation", FromNodeID: "patient", ToNodeID: "z-observation", SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND"},
		{ID: "patient-specimen", FromNodeID: "patient", ToNodeID: "a-specimen", SourceResourceType: "Patient", TargetResourceType: "Specimen", Label: "subject_Patient", StorageDirection: "INBOUND"},
	}
	for _, target := range []struct{ id, resource string }{
		{"z-observation", "Observation"}, {"a-specimen", "Specimen"}, {"substance", "Substance"}, {"substance-definition", "SubstanceDefinition"},
	} {
		snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
			ID: target.id + "-identifier", NodeID: target.id, ResourceType: target.resource,
			FieldPath: "identifier[].value", Label: target.resource + " identifier value", LogicalType: "string", Cardinality: "many",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
		})
	}
	entries := make([]semantic.Entry, 0, 4)
	for _, resourceType := range []string{"SubstanceDefinition", "Substance", "Observation", "Specimen"} {
		entries = append(entries, semantic.Entry{
			ConceptID: resourceType + "-identifier", BindingID: resourceType + "-system",
			Observation: semantic.Observation{
				SchemaVersion: semantic.ObservationSchemaVersion,
				Source:        semantic.Source{Type: resourceType, Path: "identifier[]"},
				Key:           semantic.Key{Selector: "identifier[].system", System: "urn:example:" + resourceType, Display: resourceType + " identifier"},
				Value:         semantic.Value{Selector: "identifier[].value", Type: "string"},
				OwningScope:   "identifier[]", LogicalType: "string", Completeness: semantic.CompletenessComplete,
				Status: "SUPPORTED", RuleHint: "IDENTIFIER_SYSTEM_VALUE", RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
			},
		})
	}
	buildID := semantic.BuildID("project-a", "generation-a")
	store := semanticAuthoringStore(t)
	service := newTestService(t, store, Config{
		Capability: ports.CapabilityResolver{ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(_ context.Context, options semantic.PageOptions) (semantic.Page, error) {
			if options.Project != "project-a" || options.DatasetGeneration != "generation-a" || options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted || !reflect.DeepEqual(options.AuthResourcePaths, scope.AuthResourcePaths) {
				t.Fatalf("output-scoped browse changed authorization scope: %#v", options)
			}
			return semantic.Page{
				State:   semantic.Complete,
				Build:   semantic.Build{BuildID: buildID, State: semantic.Complete, SourceAvailability: semantic.SourceAvailabilityVerified},
				Entries: entries,
			}, nil
		},
	})

	global, err := service.BrowseSemanticInventory(context.Background(), semanticbrowse.BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, RowRoot: "Patient", Limit: 50,
	})
	if err != nil || len(global.Entries) != 4 {
		t.Fatalf("global semantic inventory changed: entries=%#v err=%v", global.Entries, err)
	}
	if global.ContextToken == "" || global.BuildID != buildID {
		t.Fatalf("global semantic context/build missing: %#v", global)
	}

	scoped, err := service.BrowseSemanticInventory(context.Background(), semanticbrowse.BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, RowRoot: "Patient", OutputID: "patients", Limit: 50,
	})
	if err != nil || len(scoped.Entries) != 2 {
		t.Fatalf("table-scoped inventory did not omit unreachable identifiers: entries=%#v err=%v", scoped.Entries, err)
	}
	if scoped.ContextToken != global.ContextToken || scoped.BuildID != buildID {
		t.Fatalf("table-scoped context/build changed: scoped=%#v global=%#v", scoped, global)
	}
	gotResources := make(map[string]bool)
	for _, item := range scoped.Entries {
		gotResources[item.ResourceType] = true
		if item.ConstructionChoice == nil {
			t.Fatalf("table-scoped item has no compiler-proven construction choice: %#v", item)
		}
		choiceSource, ok := item.ConstructionChoice.Source.(capability.SemanticBindingChoiceSource)
		if !ok || choiceSource.ConceptID != item.ConceptID || choiceSource.BindingID != item.BindingID || item.ConstructionChoice == nil || len(item.ConstructionChoice.Route) != 1 ||
			item.ConstructionChoice.Route[0].Relationship != "subject_Patient" || item.ConstructionChoice.Route[0].StorageDirection != "INBOUND" {
			t.Fatalf("table-scoped choice is not bound to an exact supported route: item=%#v", item)
		}
	}
	if !gotResources["Observation"] || !gotResources["Specimen"] || gotResources["Substance"] || gotResources["SubstanceDefinition"] {
		t.Fatalf("table-scoped identifier resources = %#v", gotResources)
	}
}

func TestBrowseSemanticInventoryUsesOutputToResolveDuplicateFrameIDs(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}}
	frame, err := ws.NewFrameDefinition("Observation component values", "Codes and their paired values on Observation records.", capability.SemanticFrameFamily{
		BindingID: "binding-component", ResourceType: "Observation", SourcePath: "component[]",
		OwningScope: "component[]", KeyPath: "component[].code.coding[]", ValuePath: "valueInteger",
		LogicalType: "integer", RuleVersion: "semantic-rule-v1", SchemaVersion: 1,
	}, nil, capability.ConstructionChoiceValue)
	if err != nil {
		t.Fatal(err)
	}
	document := func(outputID string) ws.Document {
		return ws.Document{
			Kind: ws.Kind, Output: ws.Output{ID: outputID, Title: outputID}, RootResourceType: "Observation",
			Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Observation"},
			Rows:  ws.RecordsRowDefinition(), Frames: []ws.FrameDefinition{frame}, Columns: []ws.Column{},
		}
	}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Builder"},
		Documents: []ws.Document{document("out_a"), document("out_b")},
		Tabs:      []ws.Tab{{ID: "tab_a", Title: "out_a", OutputID: "out_a", Visible: true}, {ID: "tab_b", Title: "out_b", OutputID: "out_b", Order: 1, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	config, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "explorer", DraftConfig: config}}
	var browse semantic.PageOptions
	service := newTestService(t, store, Config{
		Capability: ports.CapabilityResolver{ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(_ context.Context, options semantic.PageOptions) (semantic.Page, error) {
			browse = options
			return semantic.Page{
				State:   semantic.Complete,
				Build:   semantic.Build{BuildID: semantic.BuildID("project-a", "generation-a"), State: semantic.Complete},
				Entries: []semantic.Entry{},
			}, nil
		},
	})
	result, err := service.BrowseSemanticInventory(context.Background(), semanticbrowse.BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: snapshot.Token, RowRoot: "Observation",
		OutputID: "out_b", FrameID: frame.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FrameID != frame.ID || result.FrameSource == nil || browse.BindingID != frame.Source.BindingID || browse.ResourceType != frame.Source.ResourceType {
		t.Fatalf("framed browse did not resolve the exact output source: result=%#v options=%#v", result, browse)
	}
	_, err = service.BrowseSemanticInventory(context.Background(), semanticbrowse.BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: snapshot.Token, RowRoot: "Observation", FrameID: frame.ID,
	})
	assertLifecycleError(t, err, ports.ClassMalformed, "MALFORMED_REQUEST")
}

func TestBrowseSemanticInventoryAcceptsNestedSchemaDiscoveredCodedValue(t *testing.T) {
	profiler := fieldprofile.NewProfilerForGenerationWithLimits("project-a", "generation-a", "scope", "MedicationRequest", nil, fieldprofile.DefaultProfileLimits())
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
	var observation semantic.Observation
	for _, document := range profiler.Documents() {
		for _, candidate := range document.SemanticObservations {
			if candidate.RuleHint == semantic.RuleHintCodedValueV1 && candidate.Key.Code == "daily-dose" {
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
	buildID := semantic.BuildID("project-a", "generation-a")
	service := &Service{config: Config{
		Capability: ports.CapabilityResolver{ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(context.Context, semantic.PageOptions) (semantic.Page, error) {
			return semantic.Page{
				State:   semantic.Complete,
				Build:   semantic.Build{BuildID: buildID, State: semantic.Complete},
				Entries: []semantic.Entry{{ConceptID: "daily-dose", BindingID: "dose-binding", Observation: observation}},
			}, nil
		},
	}}
	result, err := service.BrowseSemanticInventory(context.Background(), semanticbrowse.BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: snapshot.Token, RowRoot: "MedicationRequest",
	})
	if err != nil || len(result.Entries) != 1 || result.Entries[0].Readiness.Status != semanticauthoring.SemanticReadinessReady || result.Entries[0].ConstructionChoice == nil {
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
		Capability: ports.CapabilityResolver{ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(context.Context, semantic.PageOptions) (semantic.Page, error) {
			return semantic.Page{Build: semantic.Build{BuildID: "build"}, Entries: []semantic.Entry{{ConceptID: "concept", BindingID: "binding", Observation: observation}}}, nil
		},
	}}
	result, err := service.BrowseSemanticInventory(context.Background(), semanticbrowse.BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation"})
	if err != nil || len(result.Entries) != 1 || result.Entries[0].ConstructionChoice != nil || result.Entries[0].Readiness.Status != semanticauthoring.SemanticReadinessUnsupported || result.Entries[0].Readiness.Code != "COMPILER_PROOF_UNAVAILABLE" {
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
		Capability: ports.CapabilityResolver{ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(context.Context, semantic.PageOptions) (semantic.Page, error) {
			return semantic.Page{Build: semantic.Build{BuildID: "build"}, Entries: []semantic.Entry{{ConceptID: "concept", BindingID: "binding", Observation: observation}}}, nil
		},
	}}
	result, err := service.BrowseSemanticInventory(context.Background(), semanticbrowse.BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation"})
	if err != nil || len(result.Entries) != 1 || result.Entries[0].ConstructionChoice != nil || result.Entries[0].Readiness.Code != "COMPILER_PROOF_UNAVAILABLE" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestBrowseSemanticInventoryRejectsInvalidScopeBeforeRead(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}}
	service := &Service{config: Config{
		Capability: ports.CapabilityResolver{ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		}},
		SemanticInventory: func(context.Context, semantic.PageOptions) (semantic.Page, error) {
			t.Fatal("inventory queried with mismatched scope")
			return semantic.Page{}, nil
		},
	}}
	_, err := service.BrowseSemanticInventory(context.Background(), semanticbrowse.BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation"})
	var failure *ports.Error
	if !errors.As(err, &failure) || failure.Code != "STALE_AUTHORIZATION_SCOPE" {
		t.Fatalf("error=%v", err)
	}
}

func TestBrowseSemanticInventoryUnknownRemainsUnknown(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}}
	service := &Service{config: Config{
		Capability: ports.CapabilityResolver{ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(context.Context, semantic.PageOptions) (semantic.Page, error) {
			return semantic.Page{State: semantic.Unknown}, nil
		},
	}}
	result, err := service.BrowseSemanticInventory(context.Background(), semanticbrowse.BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation"})
	if err != nil || result.State != semantic.Unknown || result.SourceAvailability != semantic.SourceAvailabilityUnknown || result.Entries == nil || len(result.Entries) != 0 {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestConstructionChoiceSearchReauthorizesSignedCategoryFamily(t *testing.T) {
	store, _, snapshot, catalogSnapshot, _ := inboundPatientObservationRouteFixture(t)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	displayCandidate := capability.Candidate{
		ID: "observation-coded-display", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "code.coding[].display", Label: "Observation code display", LogicalType: "string", Cardinality: "many",
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionArray},
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 8}},
	}
	codeCandidate := displayCandidate
	codeCandidate.ID = "observation-coded-code"
	codeCandidate.FieldPath = "code.coding[].code"
	codeCandidate.Label = "Observation code"
	snapshot.Candidates = []capability.Candidate{displayCandidate, codeCandidate}
	family := testCodedCategoryFamily("file", "Observation", "Observation.code.coding[]", "https://example.org/codes", "v1")
	buildID := semantic.BuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
	contextToken, err := semanticInventoryContextToken(snapshot, "patients", "Patient", buildID)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := semanticbrowse.SemanticCategoryConstructionChoiceForRoute(snapshot, contextToken, buildID, nil, family, displayCandidate)
	if err != nil {
		t.Fatal(err)
	}
	service := newTestService(t, store, Config{
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) ws.CatalogSnapshot { return catalogSnapshot },
		},
		ResolveSemanticInventorySelections: func(_ context.Context, options semantic.ResolveOptions) (semantic.ResolveResult, error) {
			expected := semantic.FamilyReference{
				FamilyID: family.FamilyID, BindingID: family.BindingID, SourceKind: family.SourceKind,
				ResourceType: family.ResourceType, SourcePath: family.SourcePath, System: family.System, Version: family.Version,
			}
			if !reflect.DeepEqual(options.FamilyReferences, []semantic.FamilyReference{expected}) || options.AuthResourcePathsUnrestricted == nil || !*options.AuthResourcePathsUnrestricted {
				t.Fatalf("category resolution was not bound to the signed family and authorized scope: %#v", options)
			}
			return semantic.ResolveResult{
				Build: semantic.Build{
					Project: snapshot.Identity.Project, DatasetGeneration: snapshot.Identity.Generation,
					BuildID: buildID, State: semantic.Complete,
				},
				State: semantic.Complete, Families: []semantic.Family{family},
			}, nil
		},
	})
	result, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
		Project: snapshot.Identity.Project, ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: construction.ConstructionChoiceSearchSource{
			Kind: capability.ConstructionChoiceSourceSemantic, ContextToken: contextToken, BuildID: buildID,
			ConceptID: family.FamilyID, BindingID: family.BindingID, ChoiceID: signed.ChoiceID,
			SelectionMode: semantic.SystemVersionCategory,
		},
	})
	if err != nil || !result.Complete || len(result.Choices) != 1 {
		t.Fatalf("routed category choice search = %#v, %v", result, err)
	}
	choice := result.Choices[0]
	if len(choice.Route) != 1 || choice.Route[0].Relationship != "subject_Patient" || choice.Route[0].StorageDirection != "INBOUND" {
		t.Fatalf("category route was not reauthorized through the current graph: %#v", choice.Route)
	}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok || source.RuleHint != semantic.RuleHintCodedCategoryV1 || source.ConceptID != family.FamilyID || source.System != family.System || source.Version != family.Version || source.SourceKind != family.SourceKind {
		t.Fatalf("route search returned an exact-code or unsigned source: %#v", identity.Source)
	}
	allSupported := false
	for _, option := range choice.Options {
		if option.Form == capability.ConstructionChoiceAll && option.Support == capability.ConstructionChoiceSupported {
			allSupported = true
		}
	}
	if !allSupported {
		t.Fatalf("routed category choice lost its ALL projection form: %#v", choice.Options)
	}
}
