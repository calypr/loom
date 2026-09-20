package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestApplySemanticSelectionsPersistsAuthorizedFactsAndPreservesWorkspace(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}}
	snapshot := semanticAuthoringSnapshot(scope)
	entry := semanticAuthoringEntry("concept-a", "binding-a", "urn:system:a", "code-a", "")
	resolverCalls := 0
	store := semanticAuthoringStore(t)
	service := semanticAuthoringService(t, store, snapshot, scope, func(_ context.Context, opts catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
		resolverCalls++
		if opts.Project != "project-a" || opts.DatasetGeneration != "generation-a" || opts.AuthResourcePathsUnrestricted == nil || *opts.AuthResourcePathsUnrestricted || !reflect.DeepEqual(opts.AuthResourcePaths, []string{"/allowed"}) || !reflect.DeepEqual(opts.References, []catalog.SemanticInventoryReference{{ConceptID: "concept-a", BindingID: "binding-a"}}) {
			t.Fatalf("semantic lookup did not preserve authorized exact selection: %+v", opts)
		}
		result := semanticAuthoringInventory(snapshot, []catalog.SemanticInventoryEntry{*entry})
		result.Build.SourceAvailability = catalog.SemanticInventorySourceAvailabilityUnproven
		return result, nil
	})
	request := semanticAuthoringRequest(snapshot, "add-a", "concept-a", "binding-a", "VALUE")
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 1 || store.saveDraftCalls != 1 || response.DraftVersion != 2 || len(response.Results) != 1 || len(response.Results[0].SemanticSelections) != 1 || response.Results[0].SemanticSelections[0].Status != authoringv2.SemanticSelectionAdded {
		t.Fatalf("response=%#v resolver calls=%d saves=%d", response, resolverCalls, store.saveDraftCalls)
	}
	document := response.Workspace.Documents[0]
	if len(document.Columns) != 2 || document.Columns[0].Column != "patient_id" || len(document.Route.Children) != 2 || document.Route.Children[0].OccurrenceID != "encounter-authored" || document.Route.Children[0].ResourceType != "Encounter" || document.Route.Children[1].ResourceType != "Observation" || len(document.FixedFilters) != 1 || document.Actions[0].Title != "Export" {
		t.Fatalf("existing graph-authored configuration was not preserved: %#v", document)
	}
	semanticColumn := document.Columns[1]
	if semanticColumn.Source.Lookup == nil || semanticColumn.Source.Lookup.Key == nil || semanticColumn.Source.Lookup.Key.System != "urn:system:a" || semanticColumn.Source.Lookup.Key.Code != "code-a" || semanticColumn.OccurrenceID == authoringv2.RootOccurrenceID {
		t.Fatalf("server-resolved semantic column = %#v", semanticColumn)
	}
}

func TestApplySemanticSelectionsRejectsStaleScopeAndInventoryContext(t *testing.T) {
	t.Run("scope mismatch", func(t *testing.T) {
		scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
		badScope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/restricted"}}
		snapshot := semanticAuthoringSnapshot(scope)
		store := semanticAuthoringStore(t)
		resolverCalls := 0
		service := semanticAuthoringServiceWithResolverScope(t, store, snapshot, badScope, func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
			resolverCalls++
			return catalog.SemanticInventoryResolveResult{}, nil
		})
		_, err := service.ApplyCommands(context.Background(), "project-a", "patients", semanticAuthoringRequest(snapshot, "scope-mismatch", "concept-a", "binding-a", "VALUE"), "alice")
		assertLifecycleError(t, err, ClassConflict, "STALE_AUTHORIZATION_SCOPE")
		if resolverCalls != 0 || store.saveDraftCalls != 0 {
			t.Fatalf("scope mismatch reached inventory or draft save: resolver=%d saves=%d", resolverCalls, store.saveDraftCalls)
		}
	})
	t.Run("stale context", func(t *testing.T) {
		scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
		snapshot := semanticAuthoringSnapshot(scope)
		store := semanticAuthoringStore(t)
		service := semanticAuthoringService(t, store, snapshot, scope, func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
			return semanticAuthoringInventory(snapshot, []catalog.SemanticInventoryEntry{*semanticAuthoringEntry("concept-a", "binding-a", "urn:system", "code", "")}), nil
		})
		request := semanticAuthoringRequest(snapshot, "stale-context", "concept-a", "binding-a", "VALUE")
		request.Commands[0].ContextToken = "old-context-token"
		_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
		assertLifecycleError(t, err, ClassConflict, "STALE_SEMANTIC_CONTEXT")
		if store.saveDraftCalls != 0 {
			t.Fatal("stale semantic context reached draft save")
		}
	})
	t.Run("unknown build", func(t *testing.T) {
		scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
		snapshot := semanticAuthoringSnapshot(scope)
		store := semanticAuthoringStore(t)
		service := semanticAuthoringService(t, store, snapshot, scope, func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
			return catalog.SemanticInventoryResolveResult{State: catalog.SemanticInventoryUnknown, Entries: []catalog.SemanticInventoryEntry{}}, nil
		})
		_, err := service.ApplyCommands(context.Background(), "project-a", "patients", semanticAuthoringRequest(snapshot, "unknown-build", "concept-a", "binding-a", "VALUE"), "alice")
		assertLifecycleError(t, err, ClassConflict, "SEMANTIC_INVENTORY_UNAVAILABLE")
		if store.saveDraftCalls != 0 {
			t.Fatal("unknown inventory reached draft save")
		}
	})
}

func TestApplySemanticSelectionsRejectsUnavailableAndUnsupportedRowsAtomically(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []catalog.SemanticInventoryEntry
		want    string
	}{
		{name: "selection outside scope", entries: []catalog.SemanticInventoryEntry{}, want: "INVALID_SEMANTIC_SELECTION"},
		{name: "version-specific binding", entries: []catalog.SemanticInventoryEntry{*semanticAuthoringEntry("concept-a", "binding-a", "urn:system", "code", "v1")}, want: "version-specific"},
	} {
		t.Run(test.name, func(t *testing.T) {
			scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
			snapshot := semanticAuthoringSnapshot(scope)
			store := semanticAuthoringStore(t)
			service := semanticAuthoringService(t, store, snapshot, scope, func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
				return semanticAuthoringInventory(snapshot, test.entries), nil
			})
			_, err := service.ApplyCommands(context.Background(), "project-a", "patients", semanticAuthoringRequest(snapshot, "invalid-row", "concept-a", "binding-a", "VALUE"), "alice")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
			if store.saveDraftCalls != 0 || len(store.created.DraftConfig) == 0 {
				t.Fatalf("invalid semantic request changed draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
			}
		})
	}
}

func TestApplySemanticSelectionsReplaysAndDeduplicatesByIntent(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := semanticAuthoringSnapshot(scope)
	entry := semanticAuthoringEntry("concept-a", "binding-a", "urn:system", "code", "")
	resolverCalls := 0
	store := semanticAuthoringStore(t)
	service := semanticAuthoringService(t, store, snapshot, scope, func(_ context.Context, opts catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
		resolverCalls++
		return semanticAuthoringInventory(snapshot, []catalog.SemanticInventoryEntry{*entry}), nil
	})
	request := semanticAuthoringRequest(snapshot, "semantic-command", "concept-a", "binding-a", "VALUE")
	first, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 1 || store.saveDraftCalls != 1 || replay.DraftDigest != first.DraftDigest || !reflect.DeepEqual(replay.Results, first.Results) {
		t.Fatalf("same-command retry changed the result or reran lookup: resolver=%d saves=%d first=%#v replay=%#v", resolverCalls, store.saveDraftCalls, first, replay)
	}
	changedBody := request
	changedBody.Commands = append([]authoringv2.Command(nil), request.Commands...)
	changedBody.Commands[0].SemanticSelections = append([]authoringv2.SemanticSelection(nil), request.Commands[0].SemanticSelections...)
	changedBody.Commands[0].SemanticSelections[0].Title = "different display"
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", changedBody, "alice")
	assertLifecycleError(t, err, ClassConflict, "COMMAND_ID_CONFLICT")

	secondRequest := semanticAuthoringRequest(snapshot, "second-semantic-command", "concept-a", "binding-a", "VALUE")
	secondRequest.ExpectedDraftVersion = first.DraftVersion
	secondRequest.ExpectedDraftDigest = first.DraftDigest
	secondRequest.Commands[0].SemanticSelections[0].Title = "new label"
	second, err := service.ApplyCommands(context.Background(), "project-a", "patients", secondRequest, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 2 || store.saveDraftCalls != 2 || second.Results[0].SemanticSelections[0].Status != authoringv2.SemanticSelectionAlreadyPresent || len(second.Workspace.Documents[0].Columns) != 2 {
		t.Fatalf("same semantic intent produced a duplicate: resolver=%d saves=%d response=%#v", resolverCalls, store.saveDraftCalls, second)
	}
}

func TestApplySemanticSelectionsReturnsCASConflictAfterConcurrentWrite(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := semanticAuthoringSnapshot(scope)
	store := semanticAuthoringStore(t)
	service := semanticAuthoringService(t, store, snapshot, scope, func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
		return semanticAuthoringInventory(snapshot, []catalog.SemanticInventoryEntry{*semanticAuthoringEntry("concept-a", "binding-a", "urn:system", "code", "")}), nil
	})
	store.copyGet = true
	baseline := semanticAuthoringStore(t).created
	originalConfig := append([]byte(nil), baseline.DraftConfig...)
	store.saveDraftHook = func(_ explorer.Explorer) error {
		concurrent := *store.created
		concurrent.DraftConfig = append([]byte(nil), originalConfig...)
		concurrent.DraftDigest = baseline.DraftDigest
		concurrent.DraftVersion++
		concurrent.LastAuthoringCommandID = "concurrent-write"
		store.created = &concurrent
		return explorer.ErrDraftConflict
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", semanticAuthoringRequest(snapshot, "losing-semantic-write", "concept-a", "binding-a", "VALUE"), "alice")
	assertLifecycleError(t, err, ClassConflict, "DRAFT_CONFLICT")
	if response != nil || store.created.DraftVersion != 2 || store.created.LastAuthoringCommandID != "concurrent-write" {
		t.Fatalf("CAS loser returned or persisted its selections: response=%#v owner=%#v", response, store.created)
	}
}

func TestApplySemanticSelectionsRejectsMoreThanOneHundredBeforeLookup(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := semanticAuthoringSnapshot(scope)
	store := semanticAuthoringStore(t)
	resolverCalls := 0
	service := semanticAuthoringService(t, store, snapshot, scope, func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
		resolverCalls++
		return catalog.SemanticInventoryResolveResult{}, nil
	})
	request := semanticAuthoringRequest(snapshot, "too-many", "concept-0", "binding", "VALUE")
	for index := 1; index < 101; index++ {
		request.Commands[0].SemanticSelections = append(request.Commands[0].SemanticSelections, authoringv2.SemanticSelection{ConceptID: "concept-" + string(rune('a'+index%26)), BindingID: "binding", RouteEdgeIDs: []string{}, ProjectionMode: "VALUE"})
	}
	_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err == nil || !strings.Contains(err.Error(), "between 1 and 100") || resolverCalls != 0 || store.saveDraftCalls != 0 {
		t.Fatalf("oversized request error=%v resolver=%d saves=%d", err, resolverCalls, store.saveDraftCalls)
	}
}

func semanticAuthoringService(t *testing.T, store *fakeStore, snapshot capability.Snapshot, scope authscope.ReadScope, resolver func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error)) *Service {
	return semanticAuthoringServiceWithResolverScope(t, store, snapshot, scope, resolver)
}

func semanticAuthoringServiceWithResolverScope(t *testing.T, store *fakeStore, snapshot capability.Snapshot, resolverScope authscope.ReadScope, resolver func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error)) *Service {
	t.Helper()
	snapshot.Nodes = []capability.Node{{ResourceType: "Patient", RowRootEligible: true}, {ResourceType: "Observation"}}
	catalogSnapshot := semanticAuthoringCatalog(snapshot)
	return newTestService(t, store, Config{
		Capability: CapabilityResolver{
			Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: resolverScope}, nil
			},
			Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalogSnapshot },
		},
		ResolveSemanticInventorySelections: resolver,
	})
}

func semanticAuthoringSnapshot(scope authscope.ReadScope) capability.Snapshot {
	return readySnapshot("project-a", "generation-a", "token", scope)
}

func semanticAuthoringCatalog(snapshot capability.Snapshot) authoringv2.CatalogSnapshot {
	return authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: snapshot.Identity.Project, ExplorerID: "patients",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true,
		Nodes:       []authoringv2.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "encounter", ResourceType: "Encounter"}, {ID: "observation", ResourceType: "Observation"}},
		Edges:       []authoringv2.CatalogEdge{{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"}, {ID: "patient-observation", FromNodeID: "patient", ToNodeID: "observation", Label: "observations"}},
		Candidates:  []authoringv2.CatalogCandidate{{ID: "patient-id", NodeID: "patient", FieldPath: "id", Cardinality: "optional_one", Label: "Patient ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: lifecycleTestFieldChoice(snapshot.Token, "patient-id", "patient", "Patient", "id", "optional_one", capability.ProjectionScalar)}},
		RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
	}
}

func semanticAuthoringStore(t *testing.T) *fakeStore {
	t.Helper()
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Builder"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route:        authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient", Children: []authoringv2.RouteNode{{OccurrenceID: "encounter-authored", ResourceType: "Encounter", Relationship: "encounters"}}},
			Columns:      []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &authoringv2.TablePresentation{Visible: boolPointer(true), Order: intPointer(0)}}},
			FixedFilters: []authoringv2.FixedFilter{{Column: "patient_id", Values: []string{"active"}}},
			Actions:      []authoringv2.Action{{Type: "export", Title: "Export", Columns: []authoringv2.ActionColumn{{Column: "patient_id"}}}},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients-tab", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: explorer.ManagementInteractive, DraftConfig: encoded, DraftVersion: 1, DraftDigest: digest}}
}

func semanticAuthoringRequest(snapshot capability.Snapshot, commandID, conceptID, bindingID, projectionMode string) authoringv2.ApplyCommandsRequest {
	buildID := catalog.SemanticInventoryBuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
	contextToken, err := semanticInventoryContextToken(snapshot, "patients", "Patient", buildID)
	if err != nil {
		panic(err)
	}
	return authoringv2.ApplyCommandsRequest{
		CommandID: commandID, SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1,
		ExpectedDraftDigest: "", Commands: []authoringv2.Command{{Type: authoringv2.CommandAddSemanticSelections, OutputID: "patients", ContextToken: contextToken, SemanticSelections: []authoringv2.SemanticSelection{{ConceptID: conceptID, BindingID: bindingID, RouteEdgeIDs: []string{"patient-observation"}, ProjectionMode: projectionMode}}}},
	}
}

func semanticAuthoringEntry(conceptID, bindingID, system, code, version string) *catalog.SemanticInventoryEntry {
	return &catalog.SemanticInventoryEntry{ConceptID: conceptID, BindingID: bindingID, Observation: catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "code"},
		Key:           catalog.SemanticObservationKey{Selector: "code.coding[]", System: system, Version: version, Code: code, Display: "Observed label"},
		Value:         catalog.SemanticObservationValue{Selector: "valueQuantity.value", Type: "decimal"},
		ChoiceArm:     "valueQuantity", LogicalType: "decimal", Completeness: catalog.SemanticComplete, Status: "SUPPORTED", RuleHint: catalog.SemanticRuleHintCodedValueV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}}
}

func semanticAuthoringInventory(snapshot capability.Snapshot, entries []catalog.SemanticInventoryEntry) catalog.SemanticInventoryResolveResult {
	build := catalog.NewSemanticInventoryBuild(snapshot.Identity.Project, snapshot.Identity.Generation, "")
	build.State = catalog.SemanticInventoryComplete
	build.SourceAvailability = catalog.SemanticInventorySourceAvailabilityVerified
	return catalog.SemanticInventoryResolveResult{Build: build, State: catalog.SemanticInventoryComplete, Entries: entries}
}

func assertLifecycleError(t *testing.T, err error, class ErrorClass, code string) {
	t.Helper()
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != class || lifecycleErr.Code != code {
		t.Fatalf("error=%v, want %s %s", err, class, code)
	}
}

func boolPointer(value bool) *bool { return &value }
func intPointer(value int) *int    { return &value }
