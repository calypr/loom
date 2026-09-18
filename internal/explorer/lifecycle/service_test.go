package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type fakeStore struct {
	explorer.Store
	listValues         []explorer.Explorer
	state              explorer.ExplorerStateV1
	created            *explorer.Explorer
	applyErr           error
	receipt            *explorer.CompilationReceipt
	publishErr         error
	published          bool
	repositoryOwner    *explorer.Explorer
	repositoryRevision *explorer.Revision
	activationErrors   []error
	release            dataset.ProjectRelease
	revision           int64
	order              *[]string
	selection          *explorer.SelectionRevision
	selectionMembers   []explorer.SelectionMember
	memberVisits       int
}

func (f *fakeStore) List(context.Context, string) ([]explorer.Explorer, error) {
	return append([]explorer.Explorer(nil), f.listValues...), nil
}

func (f *fakeStore) Create(_ context.Context, value explorer.Explorer) (*explorer.Explorer, error) {
	if value.ExplorerID == "default" {
		f.repositoryOwner = &value
	}
	f.created = &value
	return &value, nil
}

func (f *fakeStore) Get(_ context.Context, _, id string) (*explorer.Explorer, error) {
	if f.repositoryOwner != nil && id == "default" {
		return f.repositoryOwner, nil
	}
	if id == "default" {
		return nil, explorer.ErrNotFound
	}
	if f.created != nil {
		return f.created, nil
	}
	if f.state.ExplorerID != "" {
		return &explorer.Explorer{Project: f.state.Project, ExplorerID: f.state.ExplorerID, Title: f.state.Title, ManagementMode: f.state.Management}, nil
	}
	return &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients"}, nil
}

func (f *fakeStore) SaveDraft(_ context.Context, value explorer.Explorer, _ int64, _ ...string) (*explorer.Explorer, error) {
	if f.applyErr != nil {
		return nil, f.applyErr
	}
	value.DraftVersion++
	if value.ExplorerID == "default" {
		f.repositoryOwner = &value
	}
	f.created = &value
	return &value, nil
}

func (f *fakeStore) InsertCompilationReceipt(_ context.Context, value explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
	if f.receipt != nil {
		return f.receipt, nil
	}
	f.receipt = &value
	return &value, nil
}

func (f *fakeStore) InsertRevision(_ context.Context, value explorer.Revision) (*explorer.Revision, error) {
	if f.repositoryRevision != nil {
		return f.repositoryRevision, nil
	}
	f.repositoryRevision = &value
	return &value, nil
}

func (f *fakeStore) GetCompilationReceiptForExplorer(context.Context, string, string, string) (*explorer.CompilationReceipt, error) {
	if f.receipt == nil {
		return nil, explorer.ErrNotFound
	}
	return f.receipt, nil
}

func (f *fakeStore) GetSelection(context.Context, string, string) (*explorer.SelectionRevision, error) {
	if f.selection == nil {
		return nil, explorer.ErrSelectionNotFound
	}
	selection := *f.selection
	return &selection, nil
}

func (f *fakeStore) VisitSelectionMembers(_ context.Context, _, _ string, after string, limit int, visit func(explorer.SelectionMember) error) (string, error) {
	f.memberVisits++
	if limit <= 0 {
		limit = len(f.selectionMembers)
	}
	start := 0
	for start < len(f.selectionMembers) && f.selectionMembers[start].Ref.ID <= after {
		start++
	}
	last := after
	count := 0
	for _, member := range f.selectionMembers[start:] {
		if count == limit {
			break
		}
		if err := visit(member); err != nil {
			return last, err
		}
		last = member.Ref.ID
		count++
	}
	return last, nil
}

func (f *fakeStore) GetRevision(context.Context, string) (*explorer.Revision, error) {
	return nil, explorer.ErrNotFound
}

func (f *fakeStore) PublishAuthoring(_ context.Context, _ explorer.CompilationReceipt, revision explorer.Revision, release dataset.ProjectRelease, expectedRevision int64) (*explorer.Revision, error) {
	if f.order != nil {
		*f.order = append(*f.order, "persist")
	}
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	f.release = release
	f.revision = expectedRevision
	f.published = true
	return &revision, nil
}

func (f *fakeStore) FailRevision(_ context.Context, id string, diagnostics []explorer.Diagnostic) (*explorer.Revision, error) {
	if f.repositoryRevision == nil || f.repositoryRevision.ID != id {
		return nil, explorer.ErrNotFound
	}
	f.repositoryRevision.Status = explorer.RevisionFailed
	f.repositoryRevision.Diagnostics = append([]explorer.Diagnostic(nil), diagnostics...)
	failedAt := time.Now().UTC()
	f.repositoryRevision.FailedAt = &failedAt
	f.repositoryRevision.Publication.State = string(explorer.RevisionFailed)
	f.repositoryRevision.Publication.UpdatedAt = time.Unix(0, 0).UTC()
	return f.repositoryRevision, nil
}

func (f *fakeStore) ActivateRepositoryGeneration(_ context.Context, _, _, revisionID string) error {
	if f.order != nil {
		*f.order = append(*f.order, "activate-generation")
	}
	if len(f.activationErrors) > 0 {
		err := f.activationErrors[0]
		f.activationErrors = f.activationErrors[1:]
		if err != nil {
			return err
		}
	}
	if f.repositoryRevision != nil && f.repositoryRevision.ID == revisionID {
		f.repositoryRevision.Status = explorer.RevisionActive
		f.repositoryRevision.Diagnostics = nil
		f.repositoryRevision.FailedAt = nil
		f.repositoryRevision.Publication.State = string(explorer.RevisionActive)
		f.repositoryRevision.Publication.RevisionID = revisionID
		f.repositoryRevision.Publication.UpdatedAt = time.Now().UTC()
	}
	if f.repositoryOwner != nil {
		f.repositoryOwner.ActiveRevisionID = revisionID
	}
	return nil
}

func newTestService(t *testing.T, store *fakeStore, config Config) *Service {
	t.Helper()
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, config)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func readySnapshot(project, generation, token string, scope authscope.ReadScope) capability.Snapshot {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	return capability.Snapshot{Identity: capability.SnapshotIdentity{Project: project, Generation: generation, AuthorizationScopeDigest: hex.EncodeToString(sum[:]), SchemaDigest: "schema", ShapeDigest: "shape"}, Status: capability.StatusReady, Complete: true, Token: token}
}

func testConfig(snapshot capability.Snapshot) Config {
	cursorCodec, _ := NewHMACPopulationMappingCursorCodec("population-mapping-test-secret")
	return Config{Capability: CapabilityResolver{
		Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		ForExecution: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		},
		Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot {
			return authoringv2.CatalogSnapshot{APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: snapshot.Identity.Project, ExplorerID: "patients", SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true}, Nodes: []authoringv2.CatalogNode{{ID: "node", ResourceType: "Patient", RowRootEligible: true}}}
		},
	}, PopulationMappingCursorCodec: cursorCodec}
}

func completedTestSelection(snapshot capability.Snapshot, resourceType string) *explorer.SelectionRevision {
	completedAt := time.Now().UTC()
	return &explorer.SelectionRevision{
		ID: "selection-1", Project: snapshot.Identity.Project, Generation: snapshot.Identity.Generation, ResourceType: resourceType,
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RuleDigest: "rule-digest", MembershipDigest: "membership-digest", MemberCount: 1, MemberBytes: 1,
		Complete: true, CreatedAt: completedAt, CompletedAt: &completedAt,
	}
}

func nativeReceipt(snapshot capability.Snapshot) *explorer.CompilationReceipt {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "native-test",
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}},
	}
	emitted := []explorer.EmittedColumn{{OutputID: "patients", EmissionID: "em_patient", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string", Filterable: true, Chartable: true}}
	contract := json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"patient_id","label":"Patient ID","logicalType":"string","filterable":true,"chartable":true}]}]}`)
	bundleDigest, _ := bundle.Digest()
	receipt := &explorer.CompilationReceipt{
		ReceiptFormatVersion:     explorer.CurrentReceiptFormatVersion,
		CompilerContractVersion:  explorer.CurrentCompilerContractVersion,
		Project:                  snapshot.Identity.Project,
		ExplorerID:               "patients",
		IntentDigest:             "intent",
		SnapshotToken:            snapshot.Token,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		CapabilitySchemaDigest:   snapshot.Identity.SchemaDigest,
		ShapeDigest:              snapshot.Identity.ShapeDigest,
		SourceGeneration:         snapshot.Identity.Generation,
		RecipeDigest:             bundleDigest,
		ResolvedRecipeDigest:     bundleDigest,
		ResolvedSchemaDigest:     "resolved-schema",
		CompiledConfig:           json.RawMessage(`{}`),
		PublicOutputContract:     contract,
		Bundle:                   bundle,
		EmittedColumns:           emitted,
		OutputColumnProvenance:   map[string]map[string]string{"patients": {"patient_id": "EXPLICIT"}},
	}
	receipt.OutputContractDigest, _ = explorer.CompilationArtifactDigest(contract)
	receipt.CompilationKey, _ = explorer.CompilationKey(*receipt)
	receipt.ID, _ = explorer.ReceiptID(*receipt)
	return receipt
}

func TestListGetCreateUseApplicationStore(t *testing.T) {
	store := &fakeStore{listValues: []explorer.Explorer{{ExplorerID: "patients", Title: "Patients", ManagementMode: explorer.ManagementInteractive}}}
	service := newTestService(t, store, Config{})
	list, err := service.List(context.Background(), "project-a")
	if err != nil || len(list.Summaries) != 1 || list.Summaries[0].ExplorerID != "patients" {
		t.Fatalf("list = %#v, err=%v", list, err)
	}
	store.state = explorer.ExplorerStateV1{Project: "project-a", ExplorerID: "patients"}
	state, err := service.Get(context.Background(), "project-a", "patients")
	if err != nil || state.ExplorerID != "patients" {
		t.Fatalf("get = %#v, err=%v", state, err)
	}
	created, err := service.Create(context.Background(), CreateRequest{Project: "project-a", Name: "Sales", Actor: "alice"})
	if err != nil || created.ExplorerID != "sales" || store.created == nil {
		t.Fatalf("create = %#v, err=%v", created, err)
	}
}

func TestApplyCommandsMapsCASFailures(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	request := authoringv2.ApplyCommandsRequest{CommandID: "command-a", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: "token", Commands: []authoringv2.Command{{Type: authoringv2.CommandCreateTable, Title: "Patients", RootNodeID: "node"}}}
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "draft", err: explorer.ErrDraftConflict, code: "DRAFT_CONFLICT"},
		{name: "command", err: explorer.ErrAuthoringCommandConflict, code: "COMMAND_ID_CONFLICT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{applyErr: test.err}
			service := newTestService(t, store, testConfig(snapshot))
			_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
			var value *Error
			if !errors.As(err, &value) || value.Class != ClassConflict || value.Code != test.code {
				t.Fatalf("err=%v, want conflict %s", err, test.code)
			}
		})
	}
}

func TestApplyCommandsAcceptsCorrelatedLookupWithoutLegacyPathThroughService(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	catalog := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: "observations",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
		Nodes: []authoringv2.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}},
	}
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Observations"},
		Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "observations", Title: "Observations"}, RootResourceType: "Observation", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"}}},
		Tabs:      []authoringv2.Tab{{ID: "observations", Title: "Observations", OutputID: "observations", Order: 0, Visible: true}},
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{
		Project: "project-a", ExplorerID: "observations", Title: "Observations", ManagementMode: explorer.ManagementInteractive,
		DraftConfig: draft, DraftVersion: 4, DraftDigest: digest,
	}}
	config := testConfig(snapshot)
	config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalog }
	service := newTestService(t, store, config)
	request := authoringv2.ApplyCommandsRequest{
		CommandID: "add-correlated", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 4, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandAddColumnSource, OutputID: "observations", OccurrenceID: authoringv2.RootOccurrenceID, Source: &authoringv2.ColumnSource{
			Kind: authoringv2.SourceObservationComponentByCode,
			Lookup: &authoringv2.LookupSource{
				Binding: &fhirschema.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"},
				Key:     &fhirschema.CorrelatedKey{System: "urn:study:A", Code: "shared"},
			},
		}}},
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "observations", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Workspace.Documents) != 1 || len(response.Workspace.Documents[0].Columns) != 1 {
		t.Fatalf("response workspace = %#v", response)
	}
	column := response.Workspace.Documents[0].Columns[0]
	if column.Source.Lookup == nil || column.Source.Lookup.Binding == nil || column.Source.Lookup.Path != "" || column.LogicalType != "decimal" {
		t.Fatalf("correlated source was not preserved through lifecycle ApplyCommands: %#v", column)
	}
	if store.created == nil || store.created.DraftVersion != 5 {
		t.Fatalf("persisted draft version = %#v, want 5", store.created)
	}
}

func TestPopulationLifecycleChecksSelectionBeforeDraftAndCompile(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}
	catalog := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: "patients",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token,
		Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true}, Nodes: []authoringv2.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}},
	}
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}}},
		Tabs:      []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: explorer.ManagementInteractive, DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}, selection: completedTestSelection(snapshot, "Patient")}
	config := testConfig(snapshot)
	config.SelectionMembersCollection = "loom_explorer_selection_members"
	config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalog }
	service := newTestService(t, store, config)
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "set-population", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", SelectionRevisionID: "selection-1"}},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || response.Workspace.Documents[0].Population == nil || store.created.DraftVersion != 2 {
		t.Fatalf("population draft = %#v persisted=%#v", response, store.created)
	}

	var compileRequestSeen CompileReceiptRequest
	config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		compileRequestSeen = request
		receipt := nativeReceipt(snapshot)
		store.receipt = receipt
		return receipt, nil
	}
	service = newTestService(t, store, config)
	compiled, err := service.Reconcile(context.Background(), ReconcileRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: store.created.DraftVersion, DraftDigest: store.created.DraftDigest})
	if err != nil {
		t.Fatal(err)
	}
	if compiled == nil || len(compileRequestSeen.ResolvedInputs.Populations) != 1 {
		t.Fatalf("compiled=%#v request=%#v", compiled, compileRequestSeen)
	}
	resolved := compileRequestSeen.ResolvedInputs.Populations[0]
	if resolved.OutputID != "patients" || resolved.SelectionRevisionID != "selection-1" || resolved.ResourceType != "Patient" || resolved.MemberCount != 1 {
		t.Fatalf("resolved population = %#v", resolved)
	}
	if compileRequestSeen.SelectionMembersCollection != "loom_explorer_selection_members" {
		t.Fatalf("compile runtime binding = %q", compileRequestSeen.SelectionMembersCollection)
	}
}

func TestPopulationLifecycleRejectsIncompleteSelectionBeforeDraftWrite(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}
	catalog := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: "patients", SourceGeneration: snapshot.Identity.Generation,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
		Nodes: []authoringv2.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}},
	}
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}}},
		Tabs:      []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	selection := completedTestSelection(snapshot, "Patient")
	selection.Complete = false
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: explorer.ManagementInteractive, DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}, selection: selection}
	config := testConfig(snapshot)
	config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalog }
	service := newTestService(t, store, config)
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "set-incomplete-population", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", SelectionRevisionID: "selection-1"}},
	}, "alice")
	if err == nil || store.created.DraftVersion != 1 {
		t.Fatalf("incomplete selection err=%v persisted=%#v", err, store.created)
	}
}

func TestApplyCommandsPersistsLegacyMigrationIdempotentlyWithoutReceiptMutation(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	legacyDraft := []byte(`{"apiVersion":"loom.calypr.org/explorer-authoring/v2","kind":"ExplorerBuilderWorkspace","semanticsVersion":2,"explorer":{"title":"Patients"},"documents":[{"kind":"ExplorerBuilderDocument","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient","children":[{"occurrenceId":"encounter","resourceType":"Encounter","relationship":"encounters"}]},"columns":[{"column":"encounter__code","label":"Encounter code","occurrenceId":"encounter","source":{"kind":"field","fieldPath":"code","projectionMode":"VALUE"}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`)
	oldReceipt := nativeReceipt(snapshot)
	oldReceiptJSON, err := json.Marshal(oldReceipt)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		created: &explorer.Explorer{
			Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: explorer.ManagementInteractive,
			DraftConfig: legacyDraft, DraftVersion: 4, DraftDigest: "legacy-digest",
		},
		receipt: oldReceipt,
	}
	service := newTestService(t, store, testConfig(snapshot))
	request := authoringv2.ApplyCommandsRequest{
		CommandID: "migrate-legacy", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 4, ExpectedDraftDigest: "legacy-digest",
		Commands: []authoringv2.Command{{Type: authoringv2.CommandRenameTable, OutputID: "patients", Title: "Renamed"}},
	}
	first, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if first.Workspace.SemanticsVersion != authoringv2.CurrentSemanticsVersion || first.Workspace.Documents[0].Columns[0].Source.Field == nil || first.Workspace.Documents[0].Columns[0].Source.Field.RelatedSelection == nil || first.Workspace.Documents[0].Columns[0].Source.Field.RelatedSelection.Acknowledged {
		t.Fatalf("migrated workspace = %#v", first.Workspace)
	}
	if store.created.DraftVersion != 5 || store.created.LastAuthoringCommandID != request.CommandID {
		t.Fatalf("persisted draft metadata = %#v", store.created)
	}
	persistedDraft := append([]byte(nil), store.created.DraftConfig...)

	replay, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	replayedDraft, err := replay.Workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	canonicalFirst, err := first.Workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(canonicalFirst) != string(replayedDraft) || string(store.created.DraftConfig) != string(persistedDraft) || store.created.DraftVersion != 5 {
		t.Fatalf("replay changed migration result or draft: first=%s replay=%s stored=%s", canonicalFirst, replayedDraft, store.created.DraftConfig)
	}

	stale := request
	stale.CommandID = "stale-command"
	stale.Commands = []authoringv2.Command{{Type: authoringv2.CommandRenameTable, OutputID: "patients", Title: "Stale"}}
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", stale, "alice")
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "DRAFT_CONFLICT" {
		t.Fatalf("stale draft error = %v, want DRAFT_CONFLICT", err)
	}
	unsupported := request
	unsupported.CommandID = "unsupported-semantics"
	unsupported.SemanticsVersion = authoringv2.CurrentSemanticsVersion - 1
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", unsupported, "alice")
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassConflict || lifecycleErr.Code != "UNSUPPORTED_SEMANTICS_VERSION" {
		t.Fatalf("unsupported semantics error = %v, want conflict UNSUPPORTED_SEMANTICS_VERSION", err)
	}
	newReceiptJSON, err := json.Marshal(store.receipt)
	if err != nil {
		t.Fatal(err)
	}
	if string(oldReceiptJSON) != string(newReceiptJSON) {
		t.Fatal("legacy compilation receipt changed during draft migration")
	}
}

func TestCompileRejectsUnboundOrUnpersistedReceipt(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}}},
		Tabs:      []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	base := nativeReceipt(snapshot)
	tests := []struct {
		name   string
		mutate func(*explorer.CompilationReceipt)
		code   string
	}{
		{name: "wrong project", mutate: func(receipt *explorer.CompilationReceipt) { receipt.Project = "other-project" }, code: "INVALID_COMPILATION_RECEIPT"},
		{name: "wrong explorer", mutate: func(receipt *explorer.CompilationReceipt) { receipt.ExplorerID = "other" }, code: "INVALID_COMPILATION_RECEIPT"},
		{name: "wrong snapshot token", mutate: func(receipt *explorer.CompilationReceipt) { receipt.SnapshotToken = "other-token" }, code: "INVALID_COMPILATION_RECEIPT"},
		{name: "wrong scope digest", mutate: func(receipt *explorer.CompilationReceipt) { receipt.AuthorizationScopeDigest = "other-scope" }, code: "INVALID_COMPILATION_RECEIPT"},
		{name: "invalid compilation key", mutate: func(receipt *explorer.CompilationReceipt) { receipt.CompilationKey = "key_invalid" }, code: "INVALID_COMPILATION_RECEIPT"},
		{name: "invalid receipt id", mutate: func(receipt *explorer.CompilationReceipt) { receipt.ID = "receipt_invalid" }, code: "INVALID_COMPILATION_RECEIPT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := *base
			test.mutate(&receipt)
			store := &fakeStore{}
			config := testConfig(snapshot)
			config.CompileReceipt = func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
				return &receipt, nil
			}
			service := newTestService(t, store, config)
			_, err := service.compile(context.Background(), compileRequest{Project: "project-a", ExplorerID: "patients", Workspace: workspace, SnapshotToken: snapshot.Token})
			var lifecycleErr *Error
			if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != test.code {
				t.Fatalf("compile error = %v, want %s", err, test.code)
			}
		})
	}

	store := &fakeStore{}
	config := testConfig(snapshot)
	config.CompileReceipt = func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error) { return base, nil }
	service := newTestService(t, store, config)
	_, err := service.compile(context.Background(), compileRequest{Project: "project-a", ExplorerID: "patients", Workspace: workspace, SnapshotToken: snapshot.Token})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "COMPILATION_RECEIPT_NOT_PERSISTED" {
		t.Fatalf("unpersisted compile error = %v, want COMPILATION_RECEIPT_NOT_PERSISTED", err)
	}

	store.receipt = base
	compiled, err := service.compile(context.Background(), compileRequest{Project: "project-a", ExplorerID: "patients", Workspace: workspace, SnapshotToken: snapshot.Token})
	if err != nil || compiled == nil || compiled.ID != base.ID {
		t.Fatalf("persisted compile = %#v, err=%v", compiled, err)
	}
}

func TestPublishRepositoryMarksActivationFailuresRetryable(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	visible := true
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Default"},
		Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Columns: []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &authoringv2.TablePresentation{Visible: &visible}}}}},
		Tabs:      []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	base := nativeReceipt(snapshot)
	base.ExplorerID = "default"
	base.NormalizedBundle, _ = workspace.CanonicalJSON()
	base.IntentDigest, _ = workspace.Digest()
	base.CompilationKey, _ = explorer.CompilationKey(*base)
	base.ID, _ = explorer.ReceiptID(*base)

	for _, test := range []struct {
		name             string
		releaseErrors    []error
		activationErrors []error
		wantDiagnostic   string
	}{
		{name: "release activation", releaseErrors: []error{errors.New("release unavailable"), nil}, wantDiagnostic: "RELEASE_ACTIVATION_FAILED"},
		{name: "generation activation", activationErrors: []error{errors.New("generation unavailable"), nil}, wantDiagnostic: "GENERATION_ACTIVATION_FAILED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{activationErrors: append([]error(nil), test.activationErrors...)}
			config := testConfig(snapshot)
			config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }
			config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			}
			config.CompileReceipt = func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error) { return base, nil }
			config.MaterializeReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (Execution, error) {
				return Execution{ID: "execution-a", Outputs: []ExecutionOutput{{Name: "patients", State: "PUBLISHED"}}}, nil
			}
			releaseErrors := append([]error(nil), test.releaseErrors...)
			config.ActivateRelease = func(context.Context, string, string, []dataset.DataframeSelector) error {
				if len(releaseErrors) == 0 {
					return nil
				}
				err := releaseErrors[0]
				releaseErrors = releaseErrors[1:]
				return err
			}
			service := newTestService(t, store, config)
			request := RepositoryPublishRequest{Project: "project-a", Generation: "generation-a", Workspace: workspace, Commit: "commit-a", Actor: "alice"}
			if _, err := service.PublishRepository(context.Background(), request); err == nil || !strings.Contains(err.Error(), "RELEASE_ACTIVATION_FAILED") {
				t.Fatalf("first publish error = %v, want RELEASE_ACTIVATION_FAILED", err)
			}
			if store.repositoryRevision == nil || store.repositoryRevision.Status != explorer.RevisionFailed || len(store.repositoryRevision.Diagnostics) != 1 || store.repositoryRevision.Diagnostics[0].Code != test.wantDiagnostic {
				t.Fatalf("failed revision = %#v, want retryable diagnostic %s", store.repositoryRevision, test.wantDiagnostic)
			}
			result, err := service.PublishRepository(context.Background(), request)
			if err != nil || result.Revision == nil || result.Revision.Status != explorer.RevisionActive {
				t.Fatalf("retry result = %#v, err=%v", result, err)
			}
			if store.repositoryOwner == nil || store.repositoryOwner.ActiveRevisionID != store.repositoryRevision.ID {
				t.Fatalf("retry did not converge owner pointer: owner=%#v revision=%#v", store.repositoryOwner, store.repositoryRevision)
			}
			if store.repositoryRevision.Publication.UpdatedAt.IsZero() || store.repositoryRevision.Publication.UpdatedAt.Equal(time.Unix(0, 0).UTC()) {
				t.Fatalf("retry left stale publication timestamp: %#v", store.repositoryRevision.Publication)
			}
		})
	}
}

func TestPreviewRejectsStaleGenerationAndScope(t *testing.T) {
	receipt := nativeReceipt(readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}))
	for _, test := range []struct {
		name     string
		snapshot capability.Snapshot
		wantCode string
	}{
		{name: "generation", snapshot: readySnapshot("project-a", "generation-b", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}), wantCode: "RECEIPT_STALE"},
		{name: "token", snapshot: readySnapshot("project-a", "generation-a", "other-token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}), wantCode: "RECEIPT_STALE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{receipt: receipt}
			config := testConfig(test.snapshot)
			config.PreviewReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
				return dataframeexecution.PreviewSummary{}, nil
			}
			service := newTestService(t, store, config)
			_, err := service.Preview(context.Background(), PreviewRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", Limit: 10})
			var value *Error
			if !errors.As(err, &value) || value.Code != test.wantCode {
				t.Fatalf("err=%v, want %s", err, test.wantCode)
			}
		})
	}
}

func TestValidateReceiptRouteAcceptsEquivalentProjectIdentities(t *testing.T) {
	receipt := nativeReceipt(readySnapshot("HTAN_INT/BForePC", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}))
	for _, project := range []string{"HTAN_INT/BForePC", "HTAN_INT%2FBForePC", "HTAN_INT-BForePC"} {
		t.Run(project, func(t *testing.T) {
			if err := (&Service{}).validateReceiptRoute(receipt, project, "patients"); err != nil {
				t.Fatalf("equivalent project identity %q was rejected: %v", project, err)
			}
		})
	}

	err := (&Service{}).validateReceiptRoute(receipt, "HTAN_INT/OtherProject", "patients")
	var value *Error
	if !errors.As(err, &value) || value.Code != "COMPILE_RECEIPT_NOT_FOUND" {
		t.Fatalf("different project error = %v, want COMPILE_RECEIPT_NOT_FOUND", err)
	}
}

func TestAuthorizedReceiptExecutionPreservesRestrictedEmptyScope(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	receipt := &explorer.CompilationReceipt{Project: "project-a", SnapshotToken: "token", SourceGeneration: "generation-a", CapabilitySchemaDigest: "schema", AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest}
	if err := validateAuthorizedReceiptExecution(receipt, AuthorizedCapability{Snapshot: snapshot, Scope: scope}); err != nil {
		t.Fatal(err)
	}
	if err := validateAuthorizedReceiptExecution(receipt, AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{}}); err == nil {
		t.Fatal("empty scope mode widened restricted receipt")
	}
}

func TestPublishCommitsReleaseAndRevisionTogether(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	order := []string{}
	store := &fakeStore{receipt: receipt, order: &order}
	config := testConfig(snapshot)
	config.ValidateReleaseGeneration = func(context.Context, string, string) error { order = append(order, "validate-generation"); return nil }
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		order = append(order, "prepare-release")
		return dataset.ProjectRelease{ID: "release-a", Project: "project-a", Generation: "generation-a", GitCommit: "generation-a"}, 7, nil
	}
	config.MaterializeReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (Execution, error) {
		order = append(order, "materialize")
		return Execution{ID: "execution-a", Outputs: []ExecutionOutput{{Name: "patients", State: "READY"}}}, nil
	}
	config.PersistPublishedWorkspace = func(_ context.Context, project, explorerID string, workspace []byte) error {
		order = append(order, "writeback")
		if project != "project-a" || explorerID != "patients" || len(workspace) != 0 {
			t.Fatalf("writeback = %s/%s %s", project, explorerID, workspace)
		}
		return nil
	}
	service := newTestService(t, store, config)
	result, err := service.Publish(context.Background(), PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	if err != nil || result.Revision == nil || !store.published {
		t.Fatalf("publish = %#v, err=%v", result, err)
	}
	if got, want := fmt.Sprint(order), "[validate-generation materialize prepare-release persist writeback]"; got != want {
		t.Fatalf("ordering=%s, want %s", got, want)
	}
	if store.release.ID != "release-a" || store.revision != 7 {
		t.Fatalf("atomic commit input = release %q at revision %d", store.release.ID, store.revision)
	}

	store.publishErr = errors.New("revision write failed")
	_, err = service.Publish(context.Background(), PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	if err == nil || !strings.Contains(err.Error(), "revision write failed") {
		t.Fatalf("publish failure=%v", err)
	}

	store.publishErr = nil
	store.published = false
	config.PersistPublishedWorkspace = func(context.Context, string, string, []byte) error {
		return errors.New("disk full")
	}
	service = newTestService(t, store, config)
	_, err = service.Publish(context.Background(), PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var writebackFailure *Error
	if !errors.As(err, &writebackFailure) || writebackFailure.Code != "LOCAL_WORKSPACE_WRITEBACK_FAILED" || !store.published {
		t.Fatalf("writeback failure published=%v, err=%v", store.published, err)
	}

	store.published = false
	config.PersistPublishedWorkspace = nil
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		return dataset.ProjectRelease{}, 0, errors.New("release preparation failed")
	}
	service = newTestService(t, store, config)
	_, err = service.Publish(context.Background(), PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	if err == nil || store.published {
		t.Fatalf("preparation failure published=%v, err=%v", store.published, err)
	}
}

func TestPublishPreservesPublicationInProgress(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	store := &fakeStore{receipt: receipt}
	config := testConfig(snapshot)
	config.ValidateReleaseGeneration = func(context.Context, string, string) error { return nil }
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		return dataset.ProjectRelease{}, 0, nil
	}
	config.MaterializeReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (Execution, error) {
		return Execution{}, dataframeerrors.NewError(
			dataframeerrors.CodePublicationInProgress,
			"an identical publication is already in progress",
			dataframeerrors.WithDetails(map[string]any{"executionId": "execution-a"}),
			dataframeerrors.WithRetryable(true),
		)
	}
	service := newTestService(t, store, config)

	_, err := service.Publish(context.Background(), PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("Publish() error = %v, want lifecycle error", err)
	}
	if lifecycleErr.Class != ClassConflict || lifecycleErr.Code != "PUBLICATION_IN_PROGRESS" {
		t.Fatalf("Publish() error = %#v, want conflict PUBLICATION_IN_PROGRESS", lifecycleErr)
	}
	if got := lifecycleErr.Details["executionId"]; got != "execution-a" {
		t.Fatalf("executionId = %v, want execution-a", got)
	}
	if got := lifecycleErr.Details["retryable"]; got != true {
		t.Fatalf("retryable = %v, want true", got)
	}
}

func TestPublishKeepsMaterializationFailuresUnavailable(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	config := testConfig(snapshot)
	config.ValidateReleaseGeneration = func(context.Context, string, string) error { return nil }
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		return dataset.ProjectRelease{}, 0, nil
	}
	config.MaterializeReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (Execution, error) {
		return Execution{}, errors.New("ClickHouse insert failed")
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)

	_, err := service.Publish(context.Background(), PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("Publish() error = %v, want lifecycle error", err)
	}
	if lifecycleErr.Class != ClassUnavailable || lifecycleErr.Code != "MATERIALIZATION_FAILED" {
		t.Fatalf("Publish() error = %#v, want unavailable MATERIALIZATION_FAILED", lifecycleErr)
	}
}

func TestPublishRejectsRelationshipCardinalityViolationWithoutPublishing(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	store := &fakeStore{receipt: receipt}
	config := testConfig(snapshot)
	config.ValidateReleaseGeneration = func(context.Context, string, string) error { return nil }
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		return dataset.ProjectRelease{}, 0, nil
	}
	config.MaterializeReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (Execution, error) {
		return Execution{}, dataframeerrors.NewError(
			dataframeerrors.CodeRelationshipCardinalityViolation,
			"related feature matched more than one value",
			dataframeerrors.WithDetails(map[string]any{"feature": "height"}),
		)
	}
	service := newTestService(t, store, config)

	_, err := service.Publish(context.Background(), PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("Publish() error = %v, want lifecycle error", err)
	}
	if lifecycleErr.Class != ClassUnprocessable || lifecycleErr.Code != "RELATIONSHIP_CARDINALITY_VIOLATION" {
		t.Fatalf("Publish() error = %#v, want unprocessable RELATIONSHIP_CARDINALITY_VIOLATION", lifecycleErr)
	}
	if got := lifecycleErr.Details["feature"]; got != "height" {
		t.Fatalf("feature = %v, want height", got)
	}
	if store.published {
		t.Fatal("Publish() persisted a revision after a cardinality violation")
	}
}

func TestClassifyMaterializationErrorPreservesTemporalResolutionFailures(t *testing.T) {
	for _, code := range []dataframeerrors.ErrorCode{
		dataframeerrors.CodeTemporalAnchorInvalid,
		dataframeerrors.CodeTemporalPrecisionUnsupported,
		dataframeerrors.CodeTemporalTieAmbiguous,
	} {
		t.Run(string(code), func(t *testing.T) {
			err := classifyMaterializationError("materialize", dataframeerrors.NewError(code, "private"))
			var lifecycleErr *Error
			if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassUnprocessable || lifecycleErr.Code != string(code) {
				t.Fatalf("classified error = %#v, want unprocessable %s", lifecycleErr, code)
			}
			if !strings.Contains(lifecycleErr.Message, "active revision was retained") {
				t.Fatalf("message = %q", lifecycleErr.Message)
			}
		})
	}
}

func TestPublishReportsQueryMemoryLimit(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	config := testConfig(snapshot)
	config.ValidateReleaseGeneration = func(context.Context, string, string) error { return nil }
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		return dataset.ProjectRelease{}, 0, nil
	}
	config.MaterializeReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (Execution, error) {
		return Execution{}, dataframeerrors.NewError(
			dataframeerrors.CodeQueryMemoryLimitExceeded,
			"",
			dataframeerrors.WithDetails(map[string]any{"backend": "arangodb"}),
		)
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)

	_, err := service.Publish(context.Background(), PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("Publish() error = %v, want lifecycle error", err)
	}
	if lifecycleErr.Class != ClassUnavailable || lifecycleErr.Code != "QUERY_MEMORY_LIMIT_EXCEEDED" {
		t.Fatalf("Publish() error = %#v, want unavailable QUERY_MEMORY_LIMIT_EXCEEDED", lifecycleErr)
	}
	if !strings.Contains(lifecycleErr.Message, "ArangoDB query memory limit") || !strings.Contains(lifecycleErr.Message, "active revision was retained") {
		t.Fatalf("Publish() message = %q, want cause and retained-revision guidance", lifecycleErr.Message)
	}
	if got := lifecycleErr.Details["backend"]; got != "arangodb" {
		t.Fatalf("backend = %v, want arangodb", got)
	}
}

func TestMaterializationResourceErrorDistinguishesDatabaseOutOfMemory(t *testing.T) {
	cause := dataframeerrors.NewError(
		dataframeerrors.CodeQueryBackendOutOfMemory,
		"",
		dataframeerrors.WithDetails(map[string]any{"backend": "arangodb"}),
	)

	err := classifyMaterializationError("repository_publish", cause)
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("materializationMemoryError() = %v, want lifecycle error", err)
	}
	if lifecycleErr.Class != ClassUnavailable || lifecycleErr.Code != "QUERY_BACKEND_OUT_OF_MEMORY" {
		t.Fatalf("error = %#v, want unavailable QUERY_BACKEND_OUT_OF_MEMORY", lifecycleErr)
	}
	if !strings.Contains(lifecycleErr.Message, "ran out of memory") || strings.Contains(lifecycleErr.Message, "query-memory-limit") {
		t.Fatalf("message = %q, want database OOM guidance without query-limit advice", lifecycleErr.Message)
	}
}

func TestMaterializationResourceErrorReportsOtherResourceLimitWithoutMemoryAdvice(t *testing.T) {
	cause := dataframeerrors.NewError(dataframeerrors.CodeQueryResourceLimitExceeded, "")

	err := classifyMaterializationError("materialize", cause)
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "QUERY_RESOURCE_LIMIT_EXCEEDED" {
		t.Fatalf("classifyMaterializationError() = %#v, want QUERY_RESOURCE_LIMIT_EXCEEDED", lifecycleErr)
	}
	if !strings.Contains(lifecycleErr.Message, "configured resource limit") || strings.Contains(lifecycleErr.Message, "query-memory-limit") {
		t.Fatalf("message = %q, want generic resource-limit guidance", lifecycleErr.Message)
	}
}

func TestServiceUsesConfiguredClock(t *testing.T) {
	clock := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	service := newTestService(t, &fakeStore{}, Config{Now: func() time.Time { return clock }})
	if service.now() != clock {
		t.Fatalf("clock=%v", service.now())
	}
}
