package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
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

func TestFieldPresentationRequestValidationAndAuthorization(t *testing.T) {
	t.Run("malformed batches stop before authorization and reads", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*BrowseSemanticInventoryRequest)
		}{
			{name: "duplicate candidate ID", mutate: func(request *BrowseSemanticInventoryRequest) {
				request.FieldPresentation.CandidateIDs = []string{"opaque-candidate", "opaque-candidate"}
			}},
			{name: "empty supplied stage", mutate: func(request *BrowseSemanticInventoryRequest) {
				stageID := ""
				request.FieldPresentation.StageID = &stageID
			}},
			{name: "too many candidates", mutate: func(request *BrowseSemanticInventoryRequest) {
				request.FieldPresentation.CandidateIDs = make([]string, fieldPresentationCandidateLimit+1)
				for index := range request.FieldPresentation.CandidateIDs {
					request.FieldPresentation.CandidateIDs[index] = "opaque-" + strconv.Itoa(index)
				}
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				fixture := newFieldPresentationTestFixture(t, false)
				request := fixture.request()
				test.mutate(&request)
				_, err := fixture.service.BrowseSemanticInventory(context.Background(), request)
				assertLifecycleError(t, err, ClassMalformed, "MALFORMED_REQUEST")
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
		_, err := fixture.service.BrowseSemanticInventory(context.Background(), request)
		assertLifecycleError(t, err, ClassConflict, "STALE_FIELD_PRESENTATION_CONTEXT")
		if fixture.authorizationReads != 1 || fixture.inventoryReads != 0 || fixture.coverageReads != 0 {
			t.Fatalf("stale draft reached inventory or coverage reads: auth=%d inventory=%d coverage=%d", fixture.authorizationReads, fixture.inventoryReads, fixture.coverageReads)
		}
	})

	t.Run("candidate outside current source is rejected before inventory read", func(t *testing.T) {
		fixture := newFieldPresentationTestFixture(t, false)
		request := fixture.request()
		request.FieldPresentation.CandidateIDs = []string{"not-in-current-snapshot"}
		_, err := fixture.service.BrowseSemanticInventory(context.Background(), request)
		assertLifecycleError(t, err, ClassConflict, "STALE_FIELD_PRESENTATION_CONTEXT")
		if fixture.inventoryReads != 0 || fixture.coverageReads != 0 {
			t.Fatalf("unauthorized candidate reached inventory or coverage reads: inventory=%d coverage=%d", fixture.inventoryReads, fixture.coverageReads)
		}
	})

	t.Run("unknown supplied stage remains a structured conflict", func(t *testing.T) {
		fixture := newFieldPresentationTestFixture(t, false)
		request := fixture.request()
		stageID := "not-a-current-stage"
		request.FieldPresentation.StageID = &stageID
		_, err := fixture.service.BrowseSemanticInventory(context.Background(), request)
		assertLifecycleError(t, err, ClassConflict, "STALE_FIELD_PRESENTATION_CONTEXT")
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
	fixture.service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		fixture.authorizationReads++
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	fixture.service.config.SemanticInventory = func(_ context.Context, options catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
		fixture.inventoryReads++
		if options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted ||
			!reflect.DeepEqual(options.AuthResourcePaths, scope.AuthResourcePaths) {
			t.Fatalf("semantic page escaped the current authorization path: %#v", options)
		}
		return catalog.SemanticInventoryPage{
			State: catalog.SemanticInventoryComplete,
			Build: catalog.SemanticInventoryBuild{
				BuildID: fixture.buildID, State: catalog.SemanticInventoryComplete, SourceKind: catalog.SemanticInventorySourceFile,
				SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified,
			},
			Entries: []catalog.SemanticInventoryEntry{fixture.entry},
		}, nil
	}
	fixture.service.config.SemanticCoverage = func(_ context.Context, options catalogarango.SemanticCoverageLookupOptions) (catalogarango.SemanticCoverageLookupResult, error) {
		fixture.coverageReads++
		if options.AuthResourcePathsUnrestricted || !reflect.DeepEqual(options.AuthResourcePaths, scope.AuthResourcePaths) {
			t.Fatalf("coverage lookup escaped the current authorization path: %#v", options)
		}
		return fixture.coverageResult, nil
	}
	fixture.service.config.ResolveSemanticInventorySelections = func(_ context.Context, options catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
		if options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted ||
			!reflect.DeepEqual(options.AuthResourcePaths, scope.AuthResourcePaths) {
			t.Fatalf("replacement read escaped the current authorization path: %#v", options)
		}
		return fixture.resolvedInventory, nil
	}

	response, err := fixture.service.BrowseSemanticInventory(context.Background(), fixture.request())
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
		fixture.service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		}
		_, err := fixture.service.BrowseSemanticInventory(context.Background(), fixture.request())
		assertLifecycleError(t, err, ClassInternal, "CONSTRUCTION_ROUTE_PROOF_FAILED")
		if fixture.coverageReads != 0 {
			t.Fatalf("compiler proof failure reached coverage lookup %d times", fixture.coverageReads)
		}
	})

	t.Run("coverage read failure", func(t *testing.T) {
		fixture := newFieldPresentationTestFixture(t, false)
		fixture.service.config.SemanticCoverage = func(context.Context, catalogarango.SemanticCoverageLookupOptions) (catalogarango.SemanticCoverageLookupResult, error) {
			return catalogarango.SemanticCoverageLookupResult{}, errors.New("injected coverage read failure")
		}
		_, err := fixture.service.BrowseSemanticInventory(context.Background(), fixture.request())
		assertLifecycleError(t, err, ClassInternal, "FIELD_PRESENTATION_LOOKUP_FAILED")
	})
}

func TestFieldPresentationRequiresStageWhenOutputHasConstructionSteps(t *testing.T) {
	fixture := newFieldPresentationTestFixture(t, false)
	workspace, err := authoringv2.DecodeWorkspace(fixture.store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Columns = []authoringv2.Column{{
		ColumnID: "observation-id", Column: "observation_id", Label: "Observation ID", LogicalType: "string",
		OccurrenceID: authoringv2.RootOccurrenceID,
		Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
	}}
	workspace.Documents[0].Construction = &authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{{
			ID: "filter-stage", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
				ColumnID: "observation-id", Operator: authoringv2.ConstructionFilterExists,
			}},
			Outputs: []authoringv2.StageColumn{{ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string"}},
		}},
	}
	persistTestWorkspace(t, fixture.store, workspace)

	response, err := fixture.service.BrowseSemanticInventory(context.Background(), fixture.request())
	if err != nil {
		t.Fatal(err)
	}
	unavailableResult, ok := response.FieldPresentation.(FieldPresentationUnavailable)
	if !ok || unavailableResult.Reason != FieldPresentationUnsupportedCurrentStage {
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
			fixture.service.config.SemanticCoverage = nil
		}},
		{name: "build still building", mutate: func(fixture *fieldPresentationTestFixture) {
			fixture.coverageResult.State = catalogarango.SemanticCoverageBuildBuilding
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
			response, err := fixture.service.BrowseSemanticInventory(context.Background(), fixture.request())
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
			response, err := fixture.service.BrowseSemanticInventory(context.Background(), fixture.request())
			if err != nil {
				t.Fatal(err)
			}
			ready, ok := response.FieldPresentation.(FieldPresentationReady)
			if !ok || len(ready.Decisions) != 1 {
				t.Fatalf("presentation=%#v, want one READY raw-retention decision", response.FieldPresentation)
			}
			decision, ok := ready.Decisions[0].(FieldPresentationKeepRaw)
			if !ok || decision.CandidateID != fixture.candidate.ID || decision.Reason != FieldPresentationReplacementUnavailable {
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
	fixture.coverageResult.Candidates[0].Status = catalogarango.SemanticCoverageCandidateKeepRaw
	fixture.coverageResult.Candidates[0].Reason = catalog.SemanticCoverageReasonMissingSystem
	fixture.coverageResult.Candidates[0].References = nil
	response, err := fixture.service.BrowseSemanticInventory(context.Background(), fixture.request())
	if err != nil {
		t.Fatal(err)
	}
	ready, ok := response.FieldPresentation.(FieldPresentationReady)
	if !ok || len(ready.Decisions) != 1 {
		t.Fatalf("presentation=%#v, want one READY raw-retention decision", response.FieldPresentation)
	}
	decision, ok := ready.Decisions[0].(FieldPresentationKeepRaw)
	if !ok || decision.Reason != FieldPresentationMissingSystem || decision.CandidateID != fixture.candidate.ID {
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
		wantReason FieldPresentationReason
	}{
		{name: "candidate not populated", mutate: func(candidate *capability.Candidate) {
			candidate.Populated = false
		}, wantReason: FieldPresentationCandidateNotPopulated},
		{name: "candidate has no supported construction form", mutate: func(candidate *capability.Candidate) {
			candidate.ProjectionModes = nil
		}, wantReason: FieldPresentationCandidateNotAddable},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFieldPresentationTestFixture(t, false)
			snapshot := fixture.snapshot.Clone()
			test.mutate(&snapshot.Candidates[0])
			fixture.snapshot = snapshot
			fixture.service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			}
			response, err := fixture.service.BrowseSemanticInventory(context.Background(), fixture.request())
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
	const (
		project    = "project-a"
		generation = "generation-a"
	)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot(project, generation, "field-presentation-family-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	codeCandidate := capability.Candidate{
		ID: "opaque-coding-code", NodeID: "observation", ResourceType: "Observation", FieldPath: "code.coding[].code",
		Label: "Observation coding code", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 8}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionArray}, Populated: true,
	}
	displayCandidate := capability.Candidate{
		ID: "observation-coding-display", NodeID: "observation", ResourceType: "Observation", FieldPath: "code.coding[].display",
		Label: "Observation coding display", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 8}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray}, Populated: true,
	}
	snapshot.Candidates = []capability.Candidate{codeCandidate, displayCandidate}
	family := testCodedCategoryFamily(catalog.SemanticInventorySourceFile, "Observation", "Observation.code.coding[]", "urn:example:observation-code", "")
	buildID := catalog.SemanticInventoryBuildID(project, generation)
	build := catalog.SemanticInventoryBuild{
		BuildID: buildID, State: catalog.SemanticInventoryComplete, SourceKind: catalog.SemanticInventorySourceFile,
		SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified,
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
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document := &workspace.Documents[0]
	document.RootResourceType = "Observation"
	document.Route = authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation", Children: []authoringv2.RouteNode{}}
	document.Columns = []authoringv2.Column{}
	document.FixedFilters = []authoringv2.FixedFilter{}
	document.Actions = []authoringv2.Action{}
	persistTestWorkspace(t, store, workspace)
	catalogSnapshot := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: project, ExplorerID: "patients",
		SourceGeneration: generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
		Nodes: []authoringv2.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}},
		Candidates: []authoringv2.CatalogCandidate{
			{ID: codeCandidate.ID, NodeID: codeCandidate.NodeID, FieldPath: codeCandidate.FieldPath, Label: codeCandidate.Label, LogicalType: codeCandidate.LogicalType, Cardinality: codeCandidate.Cardinality, Repeated: true, ProjectionModes: []string{"ARRAY"}, DefaultProjectionMode: "ARRAY", ConstructionChoice: &codeChoice},
			{ID: displayCandidate.ID, NodeID: displayCandidate.NodeID, FieldPath: displayCandidate.FieldPath, Label: displayCandidate.Label, LogicalType: displayCandidate.LogicalType, Cardinality: displayCandidate.Cardinality, Repeated: true, ProjectionModes: []string{"FIRST", "ARRAY"}, DefaultProjectionMode: "FIRST", ConstructionChoice: &displayChoice},
		},
	}
	coverage := catalogarango.SemanticCoverageCandidateResult{
		ResourceType: codeCandidate.ResourceType, CanonicalPath: codeCandidate.FieldPath, CoverageID: "coding-code-coverage",
		Status: catalogarango.SemanticCoverageCandidateGroupable,
		References: []catalog.SemanticCoverageReference{{
			Kind: catalog.SemanticCoverageFamilyReference, BindingID: family.BindingID,
			Family: family.SemanticInventoryFamilyReference, CandidatePath: codeCandidate.FieldPath, ResourceType: codeCandidate.ResourceType,
		}},
	}
	service := newTestService(t, store, Config{
		Capability: CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalogSnapshot },
		},
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			return catalog.SemanticInventoryPage{State: catalog.SemanticInventoryComplete, Build: build, Families: []catalog.SemanticInventoryFamily{family}}, nil
		},
		SemanticCoverage: func(_ context.Context, options catalogarango.SemanticCoverageLookupOptions) (catalogarango.SemanticCoverageLookupResult, error) {
			if len(options.Candidates) != 1 || options.Candidates[0] != (catalogarango.SemanticCoverageCandidatePath{ResourceType: codeCandidate.ResourceType, CanonicalPath: codeCandidate.FieldPath}) {
				t.Fatalf("coverage lookup did not use the exact Coding leaf facet: %#v", options)
			}
			return catalogarango.SemanticCoverageLookupResult{State: catalogarango.SemanticCoverageBuildComplete, Candidates: []catalogarango.SemanticCoverageCandidateResult{coverage}}, nil
		},
		ResolveSemanticInventorySelections: func(_ context.Context, options catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
			if len(options.FamilyReferences) != 1 || options.FamilyReferences[0] != family.SemanticInventoryFamilyReference || len(options.References) != 0 {
				t.Fatalf("family replacement was not resolved by its exact existing identity: %#v", options)
			}
			return catalog.SemanticInventoryResolveResult{Build: build, State: catalog.SemanticInventoryComplete, Families: []catalog.SemanticInventoryFamily{family}}, nil
		},
	})
	request := BrowseSemanticInventoryRequest{
		Project: project, ExplorerID: "patients", SnapshotToken: snapshot.Token, RowRoot: "Observation", OutputID: "patients",
		FieldPresentation: &FieldPresentationRequest{
			ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
			SourceNodeID: "observation", OccurrenceID: authoringv2.RootOccurrenceID, CandidateIDs: []string{codeCandidate.ID},
		},
	}
	response, err := service.BrowseSemanticInventory(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	ready, ok := response.FieldPresentation.(FieldPresentationReady)
	if !ok || len(ready.Decisions) != 1 || len(ready.Replacements) != 1 {
		t.Fatalf("presentation=%#v, want one existing category-family replacement", response.FieldPresentation)
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
	if !ok || replacement.SelectionMode != string(catalog.SemanticInventorySystemVersionCategory) ||
		replacement.FamilyID != family.FamilyID || choiceSource.RuleHint != catalog.SemanticRuleHintCodedCategoryV1 ||
		choiceSource.SourceKind != family.SourceKind || choiceSource.System != family.System || choiceSource.Version != family.Version {
		t.Fatalf("replacement is not a rendered existing coded category: %#v", replacement)
	}
}

func assertFieldPresentationGroupsAndApplies(t *testing.T, related bool) {
	t.Helper()
	fixture := newFieldPresentationTestFixture(t, related)
	response, err := fixture.service.BrowseSemanticInventory(context.Background(), fixture.request())
	if err != nil {
		t.Fatal(err)
	}
	ready, ok := response.FieldPresentation.(FieldPresentationReady)
	if !ok || len(ready.Decisions) != 1 || len(ready.Replacements) != 1 {
		t.Fatalf("presentation=%#v, want one ready decision and one replacement", response.FieldPresentation)
	}
	group, ok := ready.Decisions[0].(FieldPresentationGroupUnderSemantic)
	if !ok || group.CandidateID != fixture.candidate.ID || group.CoverageID != "coverage-id" || group.Access != FieldPresentationAccessSourceFields || len(group.ReplacementIDs) != 1 {
		t.Fatalf("decision=%#v, want bounded semantic group with Source fields access", ready.Decisions[0])
	}
	identity, ok := group.ReplacementIDs[0].(ExactSemanticInventoryItemIdentity)
	if !ok || identity.ConceptID != fixture.entry.ConceptID || identity.BindingID != fixture.entry.BindingID {
		t.Fatalf("replacement identity=%#v, want exact current semantic item", group.ReplacementIDs[0])
	}
	if ready.Context.OutputID != "patients" || ready.Context.DraftVersion != fixture.store.created.DraftVersion ||
		ready.Context.DraftDigest != fixture.store.created.DraftDigest || ready.Context.SourceNodeID != "observation" ||
		ready.Context.OccurrenceID != fixture.occurrenceID || ready.Context.CandidateBatchDigest == "" ||
		ready.Context.PresentationRevision != semanticFieldPresentationRevision || ready.Context.CoverageBuildID != fixture.buildID {
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
	applied, err := fixture.service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatalf("apply current presentation replacement: %v", err)
	}
	if len(applied.Workspace.Documents) != 1 || len(applied.Workspace.Documents[0].Columns) != 1 {
		t.Fatalf("semantic replacement add produced workspace=%#v", applied.Workspace)
	}
	column := applied.Workspace.Documents[0].Columns[0]
	wantOccurrence := authoringv2.RootOccurrenceID
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
	service            *Service
	snapshot           capability.Snapshot
	candidate          capability.Candidate
	entry              catalog.SemanticInventoryEntry
	buildID            string
	occurrenceID       string
	rootResourceType   string
	coverageResult     catalogarango.SemanticCoverageLookupResult
	resolvedInventory  catalog.SemanticInventoryResolveResult
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
	occurrenceID := authoringv2.RootOccurrenceID
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
	buildID := catalog.SemanticInventoryBuildID(project, generation)
	build := catalog.SemanticInventoryBuild{
		BuildID: buildID, State: catalog.SemanticInventoryComplete, SourceKind: catalog.SemanticInventorySourceFile,
		SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified,
	}
	fieldChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
	if err != nil {
		t.Fatal(err)
	}
	catalogNodes := []authoringv2.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: !related}}
	catalogEdges := []authoringv2.CatalogEdge{}
	if related {
		catalogNodes = []authoringv2.CatalogNode{
			{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
			{ID: "observation", ResourceType: "Observation"},
		}
		catalogEdges = []authoringv2.CatalogEdge{{ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation", Label: "subject_Patient"}}
	}
	catalogSnapshot := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: project, ExplorerID: "patients",
		SourceGeneration: generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
		Nodes: catalogNodes, Edges: catalogEdges,
		Candidates: []authoringv2.CatalogCandidate{{
			ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
			LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality, ProjectionModes: []string{"VALUE"},
			DefaultProjectionMode: "VALUE", ConstructionChoice: &fieldChoice,
		}},
	}
	store := semanticAuthoringStore(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document := &workspace.Documents[0]
	document.RootResourceType = rootResourceType
	document.Columns = []authoringv2.Column{}
	document.FixedFilters = []authoringv2.FixedFilter{}
	document.Actions = []authoringv2.Action{}
	if related {
		document.Route = authoringv2.RouteNode{
			OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient",
			Children: []authoringv2.RouteNode{{
				OccurrenceID: occurrenceID, ResourceType: "Observation", Relationship: "subject_Patient", MatchMode: authoringv2.RouteMatchOptional,
			}},
		}
	} else {
		document.Route = authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation", Children: []authoringv2.RouteNode{}}
	}
	persistTestWorkspace(t, store, workspace)
	coverage := catalogarango.SemanticCoverageCandidateResult{
		ResourceType: candidate.ResourceType, CanonicalPath: candidate.FieldPath, CoverageID: "coverage-id",
		Status: catalogarango.SemanticCoverageCandidateGroupable,
		References: []catalog.SemanticCoverageReference{{
			Kind: catalog.SemanticCoverageExactReference, ConceptID: entry.ConceptID, BindingID: entry.BindingID,
			CandidatePath: candidate.FieldPath, ResourceType: candidate.ResourceType,
		}},
	}
	fixture := &fieldPresentationTestFixture{
		store: store, snapshot: snapshot, candidate: candidate, entry: entry, buildID: buildID,
		occurrenceID: occurrenceID, rootResourceType: rootResourceType,
		coverageResult: catalogarango.SemanticCoverageLookupResult{
			State: catalogarango.SemanticCoverageBuildComplete, Candidates: []catalogarango.SemanticCoverageCandidateResult{coverage},
		},
		resolvedInventory: semanticAuthoringInventory(snapshot, []catalog.SemanticInventoryEntry{entry}),
	}
	fixture.resolvedInventory.Build = build
	fixture.service = newTestService(t, store, Config{
		Capability: CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				fixture.authorizationReads++
				return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalogSnapshot },
		},
		SemanticInventory: func(_ context.Context, options catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			fixture.inventoryReads++
			if options.Project != project || options.DatasetGeneration != generation {
				t.Fatalf("semantic page lost immutable scope: %#v", options)
			}
			return catalog.SemanticInventoryPage{State: catalog.SemanticInventoryComplete, Build: build, Entries: []catalog.SemanticInventoryEntry{entry}}, nil
		},
		SemanticCoverage: func(_ context.Context, options catalogarango.SemanticCoverageLookupOptions) (catalogarango.SemanticCoverageLookupResult, error) {
			fixture.coverageReads++
			if options.Project != project || options.DatasetGeneration != generation || options.BuildID != buildID ||
				options.SourceKind != catalog.SemanticInventorySourceFile || !options.AuthResourcePathsUnrestricted ||
				len(options.Candidates) != 1 || options.Candidates[0] != (catalogarango.SemanticCoverageCandidatePath{ResourceType: candidate.ResourceType, CanonicalPath: candidate.FieldPath}) {
				t.Fatalf("coverage lookup lost exact bounded scope: %#v", options)
			}
			return fixture.coverageResult, nil
		},
		ResolveSemanticInventorySelections: func(_ context.Context, options catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
			if len(options.References) != 1 || options.References[0] != (catalog.SemanticInventoryReference{ConceptID: entry.ConceptID, BindingID: entry.BindingID}) {
				t.Fatalf("replacement resolver did not receive the exact bounded reference: %#v", options)
			}
			return fixture.resolvedInventory, nil
		},
	})
	return fixture
}

func (fixture *fieldPresentationTestFixture) request() BrowseSemanticInventoryRequest {
	return BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: fixture.snapshot.Token,
		RowRoot: fixture.rootResourceType, OutputID: "patients",
		FieldPresentation: &FieldPresentationRequest{
			ExpectedDraftVersion: fixture.store.created.DraftVersion, ExpectedDraftDigest: fixture.store.created.DraftDigest,
			SourceNodeID: "observation", OccurrenceID: fixture.occurrenceID, CandidateIDs: []string{fixture.candidate.ID},
		},
	}
}
