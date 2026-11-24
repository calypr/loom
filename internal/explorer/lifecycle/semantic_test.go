package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/semanticbrowse"
	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/semantic"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"
)

func TestApplySemanticSelectionsPersistsAuthorizedFactsAndPreservesWorkspace(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}}
	snapshot := semanticAuthoringSnapshot(scope)
	entry := capabilityfixtures.SemanticSelectionEntry("concept-a", "binding-a", "urn:system:a", "code-a", "")
	resolverCalls := 0
	store := semanticAuthoringStore(t)
	service := semanticAuthoringService(t, store, snapshot, scope, func(_ context.Context, opts semantic.ResolveOptions) (semantic.ResolveResult, error) {
		resolverCalls++
		if opts.Project != "project-a" || opts.DatasetGeneration != "generation-a" || opts.AuthResourcePathsUnrestricted == nil || *opts.AuthResourcePathsUnrestricted || !reflect.DeepEqual(opts.AuthResourcePaths, []string{"/allowed"}) || !reflect.DeepEqual(opts.References, []semantic.Reference{{ConceptID: "concept-a", BindingID: "binding-a"}}) {
			t.Fatalf("semantic lookup did not preserve authorized exact selection: %+v", opts)
		}
		result := semanticAuthoringInventory(snapshot, []semantic.Entry{*entry})
		result.Build.SourceAvailability = semantic.SourceAvailabilityUnproven
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
	if semanticColumn.Source.Lookup == nil || semanticColumn.Source.Lookup.Key == nil || semanticColumn.Source.Lookup.Key.System != "urn:system:a" || semanticColumn.Source.Lookup.Key.Code != "code-a" || semanticColumn.OccurrenceID == ws.RootOccurrenceID {
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
		service := semanticAuthoringServiceWithResolverScope(t, store, snapshot, badScope, func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error) {
			resolverCalls++
			return semantic.ResolveResult{}, nil
		})
		_, err := service.ApplyCommands(context.Background(), "project-a", "patients", semanticAuthoringRequest(snapshot, "scope-mismatch", "concept-a", "binding-a", "VALUE"), "alice")
		assertLifecycleError(t, err, ports.ClassConflict, "STALE_AUTHORIZATION_SCOPE")
		if resolverCalls != 0 || store.saveDraftCalls != 0 {
			t.Fatalf("scope mismatch reached inventory or draft save: resolver=%d saves=%d", resolverCalls, store.saveDraftCalls)
		}
	})
	t.Run("stale context", func(t *testing.T) {
		scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
		snapshot := semanticAuthoringSnapshot(scope)
		store := semanticAuthoringStore(t)
		service := semanticAuthoringService(t, store, snapshot, scope, func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error) {
			return semanticAuthoringInventory(snapshot, []semantic.Entry{*capabilityfixtures.SemanticSelectionEntry("concept-a", "binding-a", "urn:system", "code", "")}), nil
		})
		request := semanticAuthoringRequest(snapshot, "stale-context", "concept-a", "binding-a", "VALUE")
		request.Commands[0].Payload.(*authoringv2.SemanticPayload).ContextToken = "old-context-token"
		_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
		assertLifecycleError(t, err, ports.ClassConflict, "STALE_SEMANTIC_CONTEXT")
		if store.saveDraftCalls != 0 {
			t.Fatal("stale semantic context reached draft save")
		}
	})
	t.Run("unknown build", func(t *testing.T) {
		scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
		snapshot := semanticAuthoringSnapshot(scope)
		store := semanticAuthoringStore(t)
		service := semanticAuthoringService(t, store, snapshot, scope, func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error) {
			return semantic.ResolveResult{State: semantic.Unknown, Entries: []semantic.Entry{}}, nil
		})
		_, err := service.ApplyCommands(context.Background(), "project-a", "patients", semanticAuthoringRequest(snapshot, "unknown-build", "concept-a", "binding-a", "VALUE"), "alice")
		assertLifecycleError(t, err, ports.ClassConflict, "SEMANTIC_INVENTORY_UNAVAILABLE")
		if store.saveDraftCalls != 0 {
			t.Fatal("unknown inventory reached draft save")
		}
	})
}

func TestApplySemanticSelectionsRejectsUnavailableAndUnsupportedRowsAtomically(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []semantic.Entry
		want    string
	}{
		{name: "selection outside scope", entries: []semantic.Entry{}, want: "INVALID_SEMANTIC_SELECTION"},
		{name: "version-specific binding", entries: []semantic.Entry{*capabilityfixtures.SemanticSelectionEntry("concept-a", "binding-a", "urn:system", "code", "v1")}, want: "version-specific"},
	} {
		t.Run(test.name, func(t *testing.T) {
			scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
			snapshot := semanticAuthoringSnapshot(scope)
			store := semanticAuthoringStore(t)
			service := semanticAuthoringService(t, store, snapshot, scope, func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error) {
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
	entry := capabilityfixtures.SemanticSelectionEntry("concept-a", "binding-a", "urn:system", "code", "")
	resolverCalls := 0
	store := semanticAuthoringStore(t)
	service := semanticAuthoringService(t, store, snapshot, scope, func(_ context.Context, opts semantic.ResolveOptions) (semantic.ResolveResult, error) {
		resolverCalls++
		return semanticAuthoringInventory(snapshot, []semantic.Entry{*entry}), nil
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
	changedSelections := append([]authoringv2.SemanticSelection(nil), request.Commands[0].SemanticSelections()...)
	changedBody.Commands[0].Payload = &authoringv2.SemanticPayload{ContextToken: request.Commands[0].ContextToken(), SemanticSelections: changedSelections}
	changedBody.Commands[0].SemanticSelections()[0].Title = "different display"
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", changedBody, "alice")
	assertLifecycleError(t, err, ports.ClassConflict, "COMMAND_ID_CONFLICT")

	secondRequest := semanticAuthoringRequest(snapshot, "second-semantic-command", "concept-a", "binding-a", "VALUE")
	secondRequest.ExpectedDraftVersion = first.DraftVersion
	secondRequest.ExpectedDraftDigest = first.DraftDigest
	secondRequest.Commands[0].SemanticSelections()[0].Title = "new label"
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
	service := semanticAuthoringService(t, store, snapshot, scope, func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error) {
		return semanticAuthoringInventory(snapshot, []semantic.Entry{*capabilityfixtures.SemanticSelectionEntry("concept-a", "binding-a", "urn:system", "code", "")}), nil
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
	assertLifecycleError(t, err, ports.ClassConflict, "DRAFT_CONFLICT")
	if response != nil || store.created.DraftVersion != 2 || store.created.LastAuthoringCommandID != "concurrent-write" {
		t.Fatalf("CAS loser returned or persisted its selections: response=%#v owner=%#v", response, store.created)
	}
}

func TestApplySemanticSelectionsRejectsMoreThanOneHundredBeforeLookup(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := semanticAuthoringSnapshot(scope)
	store := semanticAuthoringStore(t)
	resolverCalls := 0
	service := semanticAuthoringService(t, store, snapshot, scope, func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error) {
		resolverCalls++
		return semantic.ResolveResult{}, nil
	})
	request := semanticAuthoringRequest(snapshot, "too-many", "concept-0", "binding", "VALUE")
	for index := 1; index < 101; index++ {
		{
			payload := request.Commands[0].Payload.(*authoringv2.SemanticPayload)
			payload.SemanticSelections = append(payload.SemanticSelections, authoringv2.SemanticSelection{ConceptID: "concept-" + string(rune('a'+index%26)), BindingID: "binding", RouteEdgeIDs: []string{}, ProjectionMode: "VALUE"})
		}
	}
	_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err == nil || !strings.Contains(err.Error(), "between 1 and 100") || resolverCalls != 0 || store.saveDraftCalls != 0 {
		t.Fatalf("oversized request error=%v resolver=%d saves=%d", err, resolverCalls, store.saveDraftCalls)
	}
}

func semanticAuthoringService(t *testing.T, store *fakeStore, snapshot capability.Snapshot, scope authscope.ReadScope, resolver func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error)) *Service {
	return semanticAuthoringServiceWithResolverScope(t, store, snapshot, scope, resolver)
}

func semanticAuthoringServiceWithResolverScope(t *testing.T, store *fakeStore, snapshot capability.Snapshot, resolverScope authscope.ReadScope, resolver func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error)) *Service {
	t.Helper()
	snapshot.Nodes = []capability.Node{{ResourceType: "Patient", RowRootEligible: true}, {ResourceType: "Observation"}}
	catalogSnapshot := semanticAuthoringCatalog(snapshot)
	return newTestService(t, store, Config{
		Capability: ports.CapabilityResolver{
			Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: resolverScope}, nil
			},
			Catalog: func(capability.Snapshot, string) ws.CatalogSnapshot { return catalogSnapshot },
		},
		ResolveSemanticInventorySelections: resolver,
	})
}

func semanticAuthoringSnapshot(scope authscope.ReadScope) capability.Snapshot {
	return readySnapshot("project-a", "generation-a", "token", scope)
}

func semanticAuthoringCatalog(snapshot capability.Snapshot) ws.CatalogSnapshot {
	return ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: snapshot.Identity.Project, ExplorerID: "patients",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true,
		Nodes:       []ws.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "encounter", ResourceType: "Encounter"}, {ID: "observation", ResourceType: "Observation"}},
		Edges:       []ws.CatalogEdge{{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"}, {ID: "patient-observation", FromNodeID: "patient", ToNodeID: "observation", Label: "observations"}},
		Candidates:  []ws.CatalogCandidate{{ID: "patient-id", NodeID: "patient", FieldPath: "id", Cardinality: "optional_one", Label: "Patient ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: lifecycleTestFieldChoice(snapshot.Token, "patient-id", "patient", "Patient", "id", "optional_one", capability.ProjectionScalar)}},
		RoutePolicy: ws.RoutePolicy{Unbounded: true},
	}
}

func semanticAuthoringStore(t *testing.T) *fakeStore {
	t.Helper()
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Builder"},
		Documents: []ws.Document{{
			Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route:        ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient", Children: []ws.RouteNode{{OccurrenceID: "encounter-authored", ResourceType: "Encounter", Relationship: "encounters"}}},
			Columns:      []ws.Column{{Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &ws.TablePresentation{Visible: boolPointer(true), Order: intPointer(0)}}},
			FixedFilters: []ws.FixedFilter{{Column: "patient_id", Values: []string{"active"}}},
			Actions:      []ws.Action{{Type: "export", Title: "Export", Columns: []ws.ActionColumn{{Column: "patient_id"}}}}, Rows: ws.RecordsRowDefinition(),
		}},
		Tabs: []ws.Tab{{ID: "patients-tab", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: viewer.ManagementInteractive, DraftConfig: encoded, DraftVersion: 1, DraftDigest: digest}}
}

func semanticAuthoringRequest(snapshot capability.Snapshot, commandID, conceptID, bindingID, projectionMode string) authoringv2.ApplyCommandsRequest {
	buildID := semantic.BuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
	contextToken, err := semanticbrowse.SemanticInventoryContextToken(snapshot, "patients", "Patient", buildID)
	if err != nil {
		panic(err)
	}
	return authoringv2.ApplyCommandsRequest{
		CommandID: commandID, SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1,
		ExpectedDraftDigest: "", Commands: []authoringv2.Command{{Type: authoringv2.CommandAddSemanticSelections, OutputID: "patients", Payload: &authoringv2.SemanticPayload{ContextToken: contextToken, SemanticSelections: []authoringv2.SemanticSelection{{ConceptID: conceptID, BindingID: bindingID, RouteEdgeIDs: []string{"patient-observation"}, ProjectionMode: projectionMode}}}}},
	}
}

func semanticAuthoringInventory(snapshot capability.Snapshot, entries []semantic.Entry) semantic.ResolveResult {
	build := semantic.NewBuild(snapshot.Identity.Project, snapshot.Identity.Generation, "")
	build.State = semantic.Complete
	build.SourceAvailability = semantic.SourceAvailabilityVerified
	return semantic.ResolveResult{Build: build, State: semantic.Complete, Entries: entries}
}

func assertLifecycleError(t *testing.T, err error, class ports.ErrorClass, code string) {
	t.Helper()
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != class || lifecycleErr.Code != code {
		t.Fatalf("error=%v, want %s %s", err, class, code)
	}
}

func boolPointer(value bool) *bool { return &value }
func intPointer(value int) *int    { return &value }
