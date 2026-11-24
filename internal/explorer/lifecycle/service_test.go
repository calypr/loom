package lifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	explorerpublication "github.com/calypr/loom/internal/explorer/publication"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/population"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/rowsource"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	explorerfixtures "github.com/calypr/loom/internal/testkit/explorerfixtures"
)

func TestCompileMapsPivotReducerTypeDiagnosticToUnprocessable(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Rows: ws.RecordsRowDefinition()}},
		Tabs:      []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	config := testConfig(snapshot)
	config.CompileReceipt = func(context.Context, ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		return nil, &explorercompilation.Error{Stage: "construction", Code: "PIVOT_REDUCER_REQUIRES_NUMERIC", Message: "Choose a numeric value field for this Pivot duplicate rule, or choose Show an error."}
	}
	service := newTestService(t, &fakeStore{}, config)
	_, err := service.core.Compile(context.Background(), authoringcore.CompileRequest{Project: "project-a", ExplorerID: "patients", Workspace: workspace, SnapshotToken: snapshot.Token})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassUnprocessable || lifecycleErr.Code != "PIVOT_REDUCER_REQUIRES_NUMERIC" {
		t.Fatalf("compile error = %v, want unprocessable Pivot reducer diagnostic", err)
	}
}

type fakeRevisionStore struct{}

func (fakeRevisionStore) InsertCompilationReceipt(context.Context, receiptmodel.CompilationReceipt) (*receiptmodel.CompilationReceipt, error) {
	return nil, errors.New("revision store is not configured")
}

func (fakeRevisionStore) GetCompilationReceiptForExplorer(context.Context, string, string, string) (*receiptmodel.CompilationReceipt, error) {
	return nil, errors.New("revision store is not configured")
}

func (fakeRevisionStore) PublishAuthoring(context.Context, receiptmodel.CompilationReceipt, explorer.Revision, dataset.ProjectRelease, int64) (*explorer.Revision, error) {
	return nil, errors.New("revision store is not configured")
}

func (fakeRevisionStore) InsertRevision(context.Context, explorer.Revision) (*explorer.Revision, error) {
	return nil, errors.New("revision store is not configured")
}

func (fakeRevisionStore) GetRevision(context.Context, string) (*explorer.Revision, error) {
	return nil, errors.New("revision store is not configured")
}

func (fakeRevisionStore) FailRevision(context.Context, string, []viewer.Diagnostic) (*explorer.Revision, error) {
	return nil, errors.New("revision store is not configured")
}

func (fakeRevisionStore) ActivateRepositoryGeneration(context.Context, string, string, string) error {
	return errors.New("revision store is not configured")
}

type fakeSelectionStore struct{}

func (fakeSelectionStore) BeginSelection(context.Context, explorerselection.SelectionRevision, string) (*explorerselection.SelectionRevision, error) {
	return nil, errors.New("selection staging is not configured")
}

func (fakeSelectionStore) AppendSelectionMembers(context.Context, string, string, []explorerselection.SelectionMember) ([]explorerselection.SelectionMember, error) {
	return nil, errors.New("selection staging is not configured")
}

func (fakeSelectionStore) DigestSelectionMembers(context.Context, string, string) (string, int64, int64, error) {
	return "", 0, 0, errors.New("selection staging is not configured")
}

func (fakeSelectionStore) CompleteSelection(context.Context, string, string, string, int64, int64, time.Time) (*explorerselection.SelectionRevision, error) {
	return nil, errors.New("selection staging is not configured")
}

func (fakeSelectionStore) AbortSelection(context.Context, string, string) error {
	return errors.New("selection staging is not configured")
}

func (fakeSelectionStore) CleanupSelectionStaging(context.Context, time.Time, int) error {
	return errors.New("selection staging is not configured")
}

type fakeStore struct {
	explorer.ExplorerStore
	fakeSelectionStore
	listValues         []explorer.Explorer
	state              viewer.ExplorerStateV1
	created            *explorer.Explorer
	applyErr           error
	receipt            *receiptmodel.CompilationReceipt
	publishErr         error
	published          bool
	repositoryOwner    *explorer.Explorer
	repositoryRevision *explorer.Revision
	revisions          map[string]*explorer.Revision
	activationErrors   []error
	release            dataset.ProjectRelease
	revision           int64
	saveDraftCalls     int
	saveDraftHook      func(explorer.Explorer) error
	copyGet            bool
	order              *[]string
	selection          *explorerselection.SelectionRevision
	selectionMembers   []explorerselection.SelectionMember
	memberVisits       int
	draftRevisions     map[string]explorer.DraftRevision
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
		if f.copyGet {
			value := *f.created
			value.DraftConfig = append([]byte(nil), f.created.DraftConfig...)
			value.LastAuthoringCommandResults = append([]byte(nil), f.created.LastAuthoringCommandResults...)
			return &value, nil
		}
		return f.created, nil
	}
	if f.state.ExplorerID != "" {
		return &explorer.Explorer{Project: f.state.Project, ExplorerID: f.state.ExplorerID, Title: f.state.Title, ManagementMode: f.state.Management}, nil
	}
	return &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients"}, nil
}

func (f *fakeStore) SaveDraft(_ context.Context, value explorer.Explorer, _ int64, _ ...string) (*explorer.Explorer, error) {
	f.saveDraftCalls++
	if f.saveDraftHook != nil {
		if err := f.saveDraftHook(value); err != nil {
			return nil, err
		}
	}
	if f.applyErr != nil {
		return nil, f.applyErr
	}
	if f.created != nil {
		previous := *f.created
		revisionID := fmt.Sprintf("draft-revision-%d", previous.DraftVersion)
		if f.draftRevisions == nil {
			f.draftRevisions = map[string]explorer.DraftRevision{}
		}
		f.draftRevisions[revisionID] = explorer.DraftRevision{
			ID: revisionID, Project: previous.Project, ExplorerID: previous.ExplorerID, DraftVersion: previous.DraftVersion,
			DraftDigest: previous.DraftDigest, DraftConfig: append([]byte(nil), previous.DraftConfig...), Title: previous.Title,
			SnapshotToken: previous.DraftSnapshotToken, SourceGeneration: previous.DraftSourceGeneration,
			AuthorizationScopeDigest: previous.DraftAuthorizationScopeDigest,
		}
		value.PreviousDraftRevisionID = revisionID
	}
	value.DraftVersion++
	if value.ExplorerID == "default" {
		f.repositoryOwner = &value
	}
	f.created = &value
	return &value, nil
}

func (f *fakeStore) GetDraftRevision(_ context.Context, project, explorerID, revisionID string) (*explorer.DraftRevision, error) {
	revision, ok := f.draftRevisions[revisionID]
	if !ok || revision.Project != project || revision.ExplorerID != explorerID {
		return nil, explorer.ErrNotFound
	}
	copy := revision
	copy.DraftConfig = append([]byte(nil), revision.DraftConfig...)
	return &copy, nil
}

func (f *fakeStore) InsertCompilationReceipt(_ context.Context, value receiptmodel.CompilationReceipt) (*receiptmodel.CompilationReceipt, error) {
	if f.receipt != nil {
		return f.receipt, nil
	}
	f.receipt = &value
	return &value, nil
}

func (f *fakeStore) InsertRevision(_ context.Context, value explorer.Revision) (*explorer.Revision, error) {
	if f.revisions == nil {
		f.revisions = map[string]*explorer.Revision{}
	}
	stored, ok := f.revisions[value.ID]
	if !ok {
		stored = &value
		f.revisions[value.ID] = stored
	}
	if f.repositoryRevision == nil {
		f.repositoryRevision = stored
	}
	return stored, nil
}

func (f *fakeStore) GetCompilationReceiptForExplorer(context.Context, string, string, string) (*receiptmodel.CompilationReceipt, error) {
	if f.receipt == nil {
		return nil, explorer.ErrNotFound
	}
	return f.receipt, nil
}

func (f *fakeStore) GetSelection(context.Context, string, string) (*explorerselection.SelectionRevision, error) {
	if f.selection == nil {
		return nil, explorerselection.ErrSelectionNotFound
	}
	selection := *f.selection
	return &selection, nil
}

func (f *fakeStore) VisitSelectionMembers(_ context.Context, _, _ string, after string, limit int, visit func(explorerselection.SelectionMember) error) (string, error) {
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

func (f *fakeStore) PublishAuthoring(_ context.Context, _ receiptmodel.CompilationReceipt, revision explorer.Revision, release dataset.ProjectRelease, expectedRevision int64) (*explorer.Revision, error) {
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

func (f *fakeStore) FailRevision(_ context.Context, id string, diagnostics []viewer.Diagnostic) (*explorer.Revision, error) {
	target := f.repositoryRevision
	if target == nil || target.ID != id {
		target = f.revisions[id]
	}
	if target == nil || target.ID != id {
		return nil, explorer.ErrNotFound
	}
	target.Status = explorer.RevisionFailed
	target.Diagnostics = append([]viewer.Diagnostic(nil), diagnostics...)
	failedAt := time.Now().UTC()
	target.FailedAt = &failedAt
	target.Publication.State = string(explorer.RevisionFailed)
	target.Publication.UpdatedAt = time.Unix(0, 0).UTC()
	return target, nil
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
	target := f.repositoryRevision
	if target == nil || target.ID != revisionID {
		target = f.revisions[revisionID]
	}
	if target != nil && target.ID == revisionID {
		target.Status = explorer.RevisionActive
		target.Diagnostics = nil
		target.FailedAt = nil
		target.Publication.State = string(explorer.RevisionActive)
		target.Publication.RevisionID = revisionID
		target.Publication.UpdatedAt = time.Now().UTC()
	}
	if f.repositoryOwner != nil {
		f.repositoryOwner.ActiveRevisionID = revisionID
	}
	return nil
}

type testServiceSetup struct {
	Config    *Config
	Store     *fakeStore
	Snapshot  capability.Snapshot
	Catalog   ws.CatalogSnapshot
	Workspace ws.Workspace
}

type testServiceOption func(*testServiceSetup)

func newTestService(t *testing.T, store *fakeStore, config Config, options ...testServiceOption) *Service {
	t.Helper()
	config.ExplorerOwners = store
	config.DraftStore = store
	config.DraftRevisions = store
	config.Revisions = store
	config.RevisionWriter = store
	config.Receipts = store
	config.AuthoringPublisher = store
	config.RepositoryActivator = store
	return newTestServiceFromSetup(t, testServiceSetup{Config: &config, Store: store}, options...)
}

func newTestServiceFromSetup(t *testing.T, setup testServiceSetup, options ...testServiceOption) *Service {
	t.Helper()
	for _, option := range options {
		option(&setup)
	}
	domain, err := explorerfixtures.NewService(setup.Store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, *setup.Config)
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

func lifecycleTestFieldChoice(snapshotToken, candidateID, nodeID, resourceType, path, cardinality string, projection capability.ProjectionMode) *capability.ConstructionChoice {
	choice, err := capability.NewFieldConstructionChoice(snapshotToken, capability.Candidate{
		ID: candidateID, NodeID: nodeID, ResourceType: resourceType, FieldPath: path,
		LogicalType: "string", Cardinality: cardinality, ProjectionModes: []capability.ProjectionMode{projection},
	})
	if err != nil {
		panic(err)
	}
	return &choice
}

func testConfig(snapshot capability.Snapshot) Config {
	cursorCodec, _ := population.NewHMACPopulationMappingCursorCodec("population-mapping-test-secret")
	return Config{Capability: ports.CapabilityResolver{
		Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		},
		ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		},
		Catalog: func(capability.Snapshot, string) ws.CatalogSnapshot {
			return ws.CatalogSnapshot{APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: snapshot.Identity.Project, ExplorerID: "patients", SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true}, Nodes: []ws.CatalogNode{{ID: "node", ResourceType: "Patient", RowRootEligible: true}}}
		},
	}, PopulationMappingCursorCodec: cursorCodec}
}

func completedTestSelection(snapshot capability.Snapshot, resourceType string) *explorerselection.SelectionRevision {
	completedAt := time.Now().UTC()
	return &explorerselection.SelectionRevision{
		ID: "selection-1", Project: snapshot.Identity.Project, Generation: snapshot.Identity.Generation, ResourceType: resourceType,
		Rule: explorerselection.SelectionRule{Kind: explorerselection.SelectionRuleExplicit}, Source: explorerselection.SelectionSource{Kind: explorerselection.SelectionSourceExplicit},
		ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RuleDigest: "rule-digest", MembershipDigest: "membership-digest", MemberCount: 1, MemberBytes: 1,
		Complete: true, CreatedAt: completedAt, CompletedAt: &completedAt,
	}
}

func nativeReceipt(snapshot capability.Snapshot) *receiptmodel.CompilationReceipt {
	return constructionfixtures.NativeReceipt(snapshot)
}

func successfulTestExecution(receipt *receiptmodel.CompilationReceipt, state string) ports.Execution {
	scopeDigest := "execution-scope"
	outputs := make([]ports.ExecutionOutput, 0, len(receipt.Bundle.Outputs))
	reports := make([]publication.QualityReport, 0, len(receipt.Bundle.Outputs))
	for _, output := range receipt.Bundle.Outputs {
		outputs = append(outputs, ports.ExecutionOutput{Name: output.Name, State: state})
		reports = append(reports, publication.QualityReport{
			ID: "quality-" + output.Name, ReceiptID: receipt.ID, Project: receipt.Project,
			DatasetGeneration: receipt.SourceGeneration, ScopeDigest: scopeDigest,
			Output: output.Name, PolicyVersion: publication.DefaultQualityPolicyVersion,
			Completeness: publication.QualityComplete, Verdict: publication.QualityPassed,
		})
	}
	return ports.Execution{
		ID: "execution-a", Project: receipt.Project, SourceGeneration: receipt.SourceGeneration,
		ScopeDigest: scopeDigest, Outputs: outputs, QualityReports: reports,
	}
}

func TestListGetCreateUseApplicationStore(t *testing.T) {
	store := &fakeStore{listValues: []explorer.Explorer{{ExplorerID: "patients", Title: "Patients", ManagementMode: viewer.ManagementInteractive}}}
	service := newTestService(t, store, Config{})
	list, err := service.List(context.Background(), "project-a")
	if err != nil || len(list.Summaries) != 1 || list.Summaries[0].ExplorerID != "patients" {
		t.Fatalf("list = %#v, err=%v", list, err)
	}
	store.state = viewer.ExplorerStateV1{Project: "project-a", ExplorerID: "patients"}
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
	request := authoringv2.ApplyCommandsRequest{CommandID: "command-a", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: "token", Commands: []authoringv2.Command{{Type: authoringv2.CommandCreateTable, Title: "Patients", Payload: &authoringv2.TablePayload{RootNodeID: "node"}}}}
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
			var value *ports.Error
			if !errors.As(err, &value) || value.Class != ports.ClassConflict || value.Code != test.code {
				t.Fatalf("err=%v, want conflict %s", err, test.code)
			}
		})
	}
}

func TestApplyCommandsAcceptsCorrelatedLookupWithoutLegacyPathThroughService(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	catalog := ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "project-a", ExplorerID: "observations",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true},
		Nodes: []ws.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}},
	}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Observations"},
		Documents: []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "observations", Title: "Observations"}, RootResourceType: "Observation", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Observation"}, Rows: ws.RecordsRowDefinition()}},
		Tabs:      []ws.Tab{{ID: "observations", Title: "Observations", OutputID: "observations", Order: 0, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
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
		Project: "project-a", ExplorerID: "observations", Title: "Observations", ManagementMode: viewer.ManagementInteractive,
		DraftConfig: draft, DraftVersion: 4, DraftDigest: digest,
	}}
	config := testConfig(snapshot)
	config.Capability.Catalog = func(capability.Snapshot, string) ws.CatalogSnapshot { return catalog }
	service := newTestService(t, store, config)
	request := authoringv2.ApplyCommandsRequest{
		CommandID: "add-correlated", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 4, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandAddColumnSource, OutputID: "observations", Payload: &authoringv2.ColumnPayload{OccurrenceID: ws.RootOccurrenceID, Source: &ws.ColumnSource{
			Kind: ws.SourceCodedValue,
			Lookup: &ws.LookupSource{
				Binding: &fhirbinding.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"},
				Key:     &fhirbinding.CorrelatedKey{System: "urn:study:A", Code: "shared"},
			},
		}}}},
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
	catalog := ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "project-a", ExplorerID: "patients",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token,
		Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true}, Nodes: []ws.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}},
	}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Rows: ws.RecordsRowDefinition()}},
		Tabs:      []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: viewer.ManagementInteractive, DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}, selection: completedTestSelection(snapshot, "Patient")}
	config := testConfig(snapshot)
	config.SelectionMembersCollection = "loom_explorer_selection_members"
	config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	config.Capability.Catalog = func(capability.Snapshot, string) ws.CatalogSnapshot { return catalog }
	service := newTestService(t, store, config)
	routes, err := service.OwnerServices().Population.SearchPopulationRoutes(context.Background(), population.PopulationRoutesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		OutputID: "patients", SelectionRevisionID: "selection-1",
	})
	if err != nil || len(routes.Choices) != 1 || len(routes.Choices[0].Route) != 0 {
		t.Fatalf("zero-hop population route choices = %#v, %v", routes, err)
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "set-population", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", Payload: &authoringv2.TablePayload{SelectionRevisionID: "selection-1", RouteChoiceID: routes.Choices[0].RouteChoiceID}}},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || response.Workspace.Documents[0].Population == nil || store.created.DraftVersion != 2 {
		t.Fatalf("population draft = %#v persisted=%#v", response, store.created)
	}

	var compileRequestSeen ports.CompileReceiptRequest
	config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
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
	catalog := ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "project-a", ExplorerID: "patients", SourceGeneration: snapshot.Identity.Generation,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true},
		Nodes: []ws.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}},
	}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Rows: ws.RecordsRowDefinition()}},
		Tabs:      []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
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
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: viewer.ManagementInteractive, DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}, selection: selection}
	config := testConfig(snapshot)
	config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	config.Capability.Catalog = func(capability.Snapshot, string) ws.CatalogSnapshot { return catalog }
	service := newTestService(t, store, config)
	choiceID, err := capability.NewPopulationRouteChoiceID(capability.PopulationRouteChoiceIdentity{
		Version: 1, SnapshotToken: snapshot.Token, OutputID: "patients", SelectionRevisionID: "selection-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "set-incomplete-population", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", Payload: &authoringv2.TablePayload{SelectionRevisionID: "selection-1", RouteChoiceID: choiceID}}},
	}, "alice")
	if err == nil || store.created.DraftVersion != 1 {
		t.Fatalf("incomplete selection err=%v persisted=%#v", err, store.created)
	}
}

func TestApplyCommandsAfterStoredMigrationIsIdempotentWithoutReceiptMutation(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	legacyDraft := []byte(`{"apiVersion":"loom.calypr.org/explorer-authoring/v2","kind":"ExplorerBuilderWorkspace","semanticsVersion":2,"explorer":{"title":"Patients"},"documents":[{"kind":"ExplorerBuilderDocument","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient","children":[{"occurrenceId":"encounter","resourceType":"Encounter","relationship":"encounters"}]},"columns":[{"column":"encounter__code","label":"Encounter code","occurrenceId":"encounter","source":{"kind":"field","fieldPath":"code","projectionMode":"VALUE"}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`)
	oldReceipt := nativeReceipt(snapshot)
	oldReceiptJSON, err := json.Marshal(oldReceipt)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		created: &explorer.Explorer{
			Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: viewer.ManagementInteractive,
			DraftConfig: legacyDraft, DraftVersion: 4, DraftDigest: "legacy-digest",
		},
		receipt: oldReceipt,
	}
	migration := migrateStoredWorkspaceFixture(t, legacyDraft, ws.CatalogFromCapability(snapshot, "patients"))
	store.created.DraftConfig, store.created.DraftDigest = migration.CanonicalJSON, migration.MigratedCanonicalDigest
	service := newTestService(t, store, testConfig(snapshot))
	request := authoringv2.ApplyCommandsRequest{
		CommandID: "migrate-legacy", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 4, ExpectedDraftDigest: migration.MigratedCanonicalDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandRenameTable, OutputID: "patients", Title: "Renamed", Payload: &authoringv2.TablePayload{}}},
	}
	first, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if first.Workspace.SemanticsVersion != ws.CurrentSemanticsVersion || first.Workspace.Documents[0].Columns[0].Source.Field == nil || first.Workspace.Documents[0].Columns[0].Source.Field.RelatedSelection == nil || first.Workspace.Documents[0].Columns[0].Source.Field.RelatedSelection.Acknowledged {
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
	stale.Commands = []authoringv2.Command{{Type: authoringv2.CommandRenameTable, OutputID: "patients", Title: "Stale", Payload: &authoringv2.TablePayload{}}}
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", stale, "alice")
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "DRAFT_CONFLICT" {
		t.Fatalf("stale draft error = %v, want DRAFT_CONFLICT", err)
	}
	unsupported := request
	unsupported.CommandID = "unsupported-semantics"
	unsupported.SemanticsVersion = ws.CurrentSemanticsVersion - 1
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", unsupported, "alice")
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassConflict || lifecycleErr.Code != "UNSUPPORTED_SEMANTICS_VERSION" {
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
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Rows: ws.RecordsRowDefinition()}},
		Tabs:      []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	base := nativeReceipt(snapshot)
	tests := []struct {
		name   string
		mutate func(*receiptmodel.CompilationReceipt)
		code   string
	}{
		{name: "wrong project", mutate: func(receipt *receiptmodel.CompilationReceipt) { receipt.Project = "other-project" }, code: "INVALID_COMPILATION_RECEIPT"},
		{name: "wrong explorer", mutate: func(receipt *receiptmodel.CompilationReceipt) { receipt.ExplorerID = "other" }, code: "INVALID_COMPILATION_RECEIPT"},
		{name: "wrong snapshot token", mutate: func(receipt *receiptmodel.CompilationReceipt) { receipt.SnapshotToken = "other-token" }, code: "INVALID_COMPILATION_RECEIPT"},
		{name: "wrong scope digest", mutate: func(receipt *receiptmodel.CompilationReceipt) { receipt.AuthorizationScopeDigest = "other-scope" }, code: "INVALID_COMPILATION_RECEIPT"},
		{name: "invalid compilation key", mutate: func(receipt *receiptmodel.CompilationReceipt) { receipt.CompilationKey = "key_invalid" }, code: "INVALID_COMPILATION_RECEIPT"},
		{name: "invalid receipt id", mutate: func(receipt *receiptmodel.CompilationReceipt) { receipt.ID = "receipt_invalid" }, code: "INVALID_COMPILATION_RECEIPT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := *base
			test.mutate(&receipt)
			store := &fakeStore{}
			config := testConfig(snapshot)
			config.CompileReceipt = func(context.Context, ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
				return &receipt, nil
			}
			service := newTestService(t, store, config)
			_, err := service.core.Compile(context.Background(), authoringcore.CompileRequest{Project: "project-a", ExplorerID: "patients", Workspace: workspace, SnapshotToken: snapshot.Token})
			var lifecycleErr *ports.Error
			if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != test.code {
				t.Fatalf("compile error = %v, want %s", err, test.code)
			}
		})
	}

	store := &fakeStore{}
	config := testConfig(snapshot)
	config.CompileReceipt = func(context.Context, ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		return base, nil
	}
	service := newTestService(t, store, config)
	_, err := service.core.Compile(context.Background(), authoringcore.CompileRequest{Project: "project-a", ExplorerID: "patients", Workspace: workspace, SnapshotToken: snapshot.Token})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "COMPILATION_RECEIPT_NOT_PERSISTED" {
		t.Fatalf("unpersisted compile error = %v, want COMPILATION_RECEIPT_NOT_PERSISTED", err)
	}

	store.receipt = base
	compiled, err := service.core.Compile(context.Background(), authoringcore.CompileRequest{Project: "project-a", ExplorerID: "patients", Workspace: workspace, SnapshotToken: snapshot.Token})
	if err != nil || compiled == nil || compiled.ID != base.ID {
		t.Fatalf("persisted compile = %#v, err=%v", compiled, err)
	}
}

func TestPublishRepositoryMarksActivationFailuresRetryable(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	visible := true
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Default"},
		Documents: []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Columns: []ws.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &ws.TablePresentation{Visible: &visible}}}, Rows: ws.RecordsRowDefinition()}},
		Tabs:      []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	base := nativeReceipt(snapshot)
	base.ExplorerID = "default"
	base.NormalizedBundle, _ = workspace.CanonicalJSON()
	base.IntentDigest, _ = workspace.Digest()
	base.CompilationKey, _ = receiptmodel.CompilationKey(*base)
	base.ID, _ = receiptmodel.ReceiptID(*base)

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
			config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			}
			config.CompileReceipt = func(context.Context, ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
				return base, nil
			}
			config.MaterializeReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) (ports.Execution, error) {
				return successfulTestExecution(base, "PUBLISHED"), nil
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
			request := explorerpublication.RepositoryPublishRequest{Project: "project-a", Generation: "generation-a", Workspace: workspace, Commit: "commit-a", Actor: "alice"}
			if _, err := service.OwnerServices().Publication.PublishRepository(context.Background(), request); err == nil || !strings.Contains(err.Error(), test.wantDiagnostic) {
				t.Fatalf("first publish error = %v, want %s", err, test.wantDiagnostic)
			}
			if store.repositoryRevision == nil || store.repositoryRevision.Status != explorer.RevisionFailed || len(store.repositoryRevision.Diagnostics) != 1 || store.repositoryRevision.Diagnostics[0].Code != test.wantDiagnostic {
				t.Fatalf("failed revision = %#v, want retryable diagnostic %s", store.repositoryRevision, test.wantDiagnostic)
			}
			result, err := service.OwnerServices().Publication.PublishRepository(context.Background(), request)
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
			config.PreviewReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
				return dataframeexecution.PreviewSummary{}, nil
			}
			service := newTestService(t, store, config)
			_, err := service.OwnerServices().Core.Preview(context.Background(), authoringcore.PreviewRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", Limit: 10})
			var value *ports.Error
			if !errors.As(err, &value) || value.Code != test.wantCode {
				t.Fatalf("err=%v, want %s", err, test.wantCode)
			}
		})
	}
}

func TestPreviewIncludesStableRowIdentityForRecordsAndGroups(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		name := "records"
		if grouped {
			name = "groups"
		}
		t.Run(name, func(t *testing.T) {
			snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
			receipt := nativeReceipt(snapshot)
			if grouped {
				receipt.Bundle.Outputs[0].RowGrain = "groups"
				receipt.Bundle.Outputs[0].GroupRows = &recipe.GroupRows{RevisionID: "grouprev_pinned", UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED"}
				receipt.RecipeDigest, _ = receipt.Bundle.Digest()
				receipt.ResolvedRecipeDigest = receipt.RecipeDigest
				var err error
				receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
				if err != nil {
					t.Fatal(err)
				}
				receipt.ID, err = receiptmodel.ReceiptID(*receipt)
				if err != nil {
					t.Fatal(err)
				}
			}
			config := testConfig(snapshot)
			var previewBindings recipe.RuntimeBindings
			config.PreviewReceipt = func(_ context.Context, _ *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
				previewBindings = bindings
				return dataframeexecution.PreviewSummary{}, nil
			}
			service := newTestService(t, &fakeStore{receipt: receipt}, config)
			_, err := service.OwnerServices().Core.Preview(context.Background(), authoringcore.PreviewRequest{
				Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", Limit: 10,
				SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
					return func(map[string]any) error { return nil }, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !previewBindings.IncludeRowIdentity {
				t.Fatal("preview did not request its stable row identity")
			}
		})
	}
}

func TestValidateReceiptRouteAcceptsEquivalentProjectIdentities(t *testing.T) {
	receipt := nativeReceipt(readySnapshot("HTAN_INT/BForePC", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}))
	for _, project := range []string{"HTAN_INT/BForePC", "HTAN_INT%2FBForePC", "HTAN_INT-BForePC"} {
		t.Run(project, func(t *testing.T) {
			if err := (&authoringcore.Core{}).ValidateReceiptRoute(receipt, project, "patients"); err != nil {
				t.Fatalf("equivalent project identity %q was rejected: %v", project, err)
			}
		})
	}

	err := (&authoringcore.Core{}).ValidateReceiptRoute(receipt, "HTAN_INT/OtherProject", "patients")
	var value *ports.Error
	if !errors.As(err, &value) || value.Code != "COMPILE_RECEIPT_NOT_FOUND" {
		t.Fatalf("different project error = %v, want COMPILE_RECEIPT_NOT_FOUND", err)
	}
}

func TestAuthorizedReceiptExecutionPreservesRestrictedEmptyScope(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	receipt := &receiptmodel.CompilationReceipt{Project: "project-a", SnapshotToken: "token", SourceGeneration: "generation-a", CapabilitySchemaDigest: "schema", AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest}
	if err := support.ValidateAuthorizedReceiptExecution(receipt, ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}); err != nil {
		t.Fatal(err)
	}
	if err := support.ValidateAuthorizedReceiptExecution(receipt, ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{}}); err == nil {
		t.Fatal("empty scope mode widened restricted receipt")
	}
}

func TestPublishCommitsReleaseAndRevisionTogether(t *testing.T) {
	t.Run("plain project", func(t *testing.T) {
		testPublishCommitsReleaseAndRevisionTogether(t, "project-a", "project-a")
	})
	t.Run("slash project", func(t *testing.T) {
		testPublishCommitsReleaseAndRevisionTogether(t, "HTAN/Project", "HTAN-Project")
	})
}

func testPublishCommitsReleaseAndRevisionTogether(t *testing.T, project, storageProject string) {
	snapshot := readySnapshot(project, "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	order := []string{}
	store := &fakeStore{receipt: receipt, order: &order}
	config := testConfig(snapshot)
	config.ValidateReleaseGeneration = func(context.Context, string, string) error { order = append(order, "validate-generation"); return nil }
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		order = append(order, "prepare-release")
		return dataset.ProjectRelease{ID: "release-a", Project: storageProject, Generation: "generation-a", GitCommit: "generation-a"}, 7, nil
	}
	config.MaterializeReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) (ports.Execution, error) {
		order = append(order, "materialize")
		return successfulTestExecution(receipt, "READY"), nil
	}
	config.PersistPublishedWorkspace = func(_ context.Context, writeBackProject, explorerID string, workspace []byte) error {
		order = append(order, "writeback")
		if writeBackProject != project || explorerID != "patients" || string(workspace) != string(receipt.NormalizedBundle) {
			t.Fatalf("writeback = %s/%s %s", writeBackProject, explorerID, workspace)
		}
		return nil
	}
	projectStore := func(setup *testServiceSetup) {
		setup.Config.AuthoringPublisher = publicationProjectStore{fakeStore: store, t: t, project: project, storageProject: storageProject}
	}
	service := newTestService(t, store, config, projectStore)
	result, err := service.OwnerServices().Publication.Publish(context.Background(), explorerpublication.PublishRequest{Project: project, ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
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
	_, err = service.OwnerServices().Publication.Publish(context.Background(), explorerpublication.PublishRequest{Project: project, ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var persistenceFailure *ports.Error
	if !errors.As(err, &persistenceFailure) || persistenceFailure.Class != ports.ClassUnavailable || persistenceFailure.Code != "PERSISTENCE_UNAVAILABLE" || !errors.Is(err, store.publishErr) {
		t.Fatalf("publish failure=%v, want unavailable PERSISTENCE_UNAVAILABLE", err)
	}

	store.publishErr = dataset.ErrReleaseActivationConflict
	_, err = service.OwnerServices().Publication.Publish(context.Background(), explorerpublication.PublishRequest{Project: project, ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var activationFailure *ports.Error
	if !errors.As(err, &activationFailure) || activationFailure.Class != ports.ClassConflict || activationFailure.Code != "PUBLICATION_ACTIVATION_CONFLICT" {
		t.Fatalf("activation failure=%#v, want PUBLICATION_ACTIVATION_CONFLICT", err)
	}

	store.publishErr = nil
	store.published = false
	config.PersistPublishedWorkspace = func(context.Context, string, string, []byte) error {
		return errors.New("disk full")
	}
	service = newTestService(t, store, config, projectStore)
	_, err = service.OwnerServices().Publication.Publish(context.Background(), explorerpublication.PublishRequest{Project: project, ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var writebackFailure *ports.Error
	if !errors.As(err, &writebackFailure) || writebackFailure.Code != "LOCAL_WORKSPACE_WRITEBACK_FAILED" || !store.published {
		t.Fatalf("writeback failure published=%v, err=%v", store.published, err)
	}

	store.published = false
	config.PersistPublishedWorkspace = nil
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		return dataset.ProjectRelease{}, 0, errors.New("release preparation failed")
	}
	service = newTestService(t, store, config, projectStore)
	_, err = service.OwnerServices().Publication.Publish(context.Background(), explorerpublication.PublishRequest{Project: project, ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	if err == nil || store.published {
		t.Fatalf("preparation failure published=%v, err=%v", store.published, err)
	}
}

func TestPublishRejectsInvalidQualityEvidenceBeforeReleasePreparation(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	for _, test := range []struct {
		name   string
		mutate func(*ports.Execution)
	}{
		{name: "missing", mutate: func(execution *ports.Execution) { execution.QualityReports = nil }},
		{name: "incomplete", mutate: func(execution *ports.Execution) {
			execution.QualityReports[0].Completeness = publication.QualityIncomplete
		}},
		{name: "wrong receipt", mutate: func(execution *ports.Execution) { execution.QualityReports[0].ReceiptID = "receipt-other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{receipt: receipt}
			prepared := false
			config := testConfig(snapshot)
			config.ValidateReleaseGeneration = func(context.Context, string, string) error { return nil }
			config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
				prepared = true
				return dataset.ProjectRelease{}, 0, nil
			}
			config.MaterializeReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) (ports.Execution, error) {
				execution := successfulTestExecution(receipt, "PUBLISHED")
				test.mutate(&execution)
				return execution, nil
			}
			service := newTestService(t, store, config)
			_, err := service.OwnerServices().Publication.Publish(context.Background(), explorerpublication.PublishRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID})
			var lifecycleErr *ports.Error
			if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "QUALITY_EVIDENCE_INVALID" {
				t.Fatalf("quality gate error = %v", err)
			}
			if prepared || store.published {
				t.Fatalf("invalid quality crossed activation boundary: prepared=%v published=%v", prepared, store.published)
			}
		})
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
	config.MaterializeReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) (ports.Execution, error) {
		return ports.Execution{}, dataframeerrors.NewError(
			dataframeerrors.CodePublicationInProgress,
			"an identical publication is already in progress",
			dataframeerrors.WithDetails(map[string]any{"executionId": "execution-a"}),
			dataframeerrors.WithRetryable(true),
		)
	}
	service := newTestService(t, store, config)

	_, err := service.OwnerServices().Publication.Publish(context.Background(), explorerpublication.PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("Publish() error = %v, want lifecycle error", err)
	}
	if lifecycleErr.Class != ports.ClassConflict || lifecycleErr.Code != "PUBLICATION_IN_PROGRESS" {
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
	config.MaterializeReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) (ports.Execution, error) {
		return ports.Execution{}, errors.New("ClickHouse insert failed")
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)

	_, err := service.OwnerServices().Publication.Publish(context.Background(), explorerpublication.PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("Publish() error = %v, want lifecycle error", err)
	}
	if lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "MATERIALIZATION_FAILED" {
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
	config.MaterializeReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) (ports.Execution, error) {
		return ports.Execution{}, dataframeerrors.NewError(
			dataframeerrors.CodeRelationshipCardinalityViolation,
			"related feature matched more than one value",
			dataframeerrors.WithDetails(map[string]any{"feature": "height"}),
		)
	}
	service := newTestService(t, store, config)

	_, err := service.OwnerServices().Publication.Publish(context.Background(), explorerpublication.PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("Publish() error = %v, want lifecycle error", err)
	}
	if lifecycleErr.Class != ports.ClassUnprocessable || lifecycleErr.Code != "RELATIONSHIP_CARDINALITY_VIOLATION" {
		t.Fatalf("Publish() error = %#v, want unprocessable RELATIONSHIP_CARDINALITY_VIOLATION", lifecycleErr)
	}
	if got := lifecycleErr.Details["feature"]; got != "height" {
		t.Fatalf("feature = %v, want height", got)
	}
	if store.published {
		t.Fatal("Publish() persisted a revision after a cardinality violation")
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
	config.MaterializeReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) (ports.Execution, error) {
		return ports.Execution{}, dataframeerrors.NewError(
			dataframeerrors.CodeQueryMemoryLimitExceeded,
			"",
			dataframeerrors.WithDetails(map[string]any{"backend": "arangodb"}),
		)
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)

	_, err := service.OwnerServices().Publication.Publish(context.Background(), explorerpublication.PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("Publish() error = %v, want lifecycle error", err)
	}
	if lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "QUERY_MEMORY_LIMIT_EXCEEDED" {
		t.Fatalf("Publish() error = %#v, want unavailable QUERY_MEMORY_LIMIT_EXCEEDED", lifecycleErr)
	}
	if !strings.Contains(lifecycleErr.Message, "ArangoDB query memory limit") || !strings.Contains(lifecycleErr.Message, "active revision was retained") {
		t.Fatalf("Publish() message = %q, want cause and retained-revision guidance", lifecycleErr.Message)
	}
	if got := lifecycleErr.Details["backend"]; got != "arangodb" {
		t.Fatalf("backend = %v, want arangodb", got)
	}
}

func TestServiceUsesConfiguredClock(t *testing.T) {
	clock := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	service := newTestService(t, &fakeStore{}, Config{Now: func() time.Time { return clock }})
	if service.now() != clock {
		t.Fatalf("clock=%v", service.now())
	}
}

func TestRowDefinitionProposalProjectsExactCandidateWithoutSaving(t *testing.T) {
	setup := rowProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	enableRowDefinitionPreview(setup.Config)
	service := newTestServiceFromSetup(t, setup)
	before := append([]byte(nil), store.created.DraftConfig...)
	request := rowProposalRequest(store.created, snapshot)
	proposal, err := service.OwnerServices().RowDefinition.ProposeRowDefinition(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := ws.DecodeWorkspace(store.receipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(proposal.CandidateDocument, verified.Documents[0]) {
		t.Fatal("candidate document did not come from the verified receipt")
	}
	if proposal.CandidateDocument.Rows.Kind != ws.RowDefinitionExpanded || proposal.CandidateNetwork.SourceRows.Kind != ws.RowDefinitionExpanded || proposal.CandidateNetwork.OutputID != request.OutputID || proposal.CandidateNetwork.Owner != "DOCUMENT" {
		t.Fatalf("candidate source rows missing or stale: %#v", proposal)
	}
	identity := proposal.CandidateRowIdentity
	if identity.OutputID != request.OutputID || len(identity.Stages) != 1 || identity.Stages[0].Identity.Kind != "SOURCE_ITEM" || identity.Stages[0].Identity.ScopePath != "component[]" || identity.Stages[0].Identity.MissingPolicy != string(ws.EmptyCollectionPreserveParent) {
		t.Fatalf("candidate item identity was guessed from mode: %#v", identity)
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(store.created.DraftConfig, before) {
		t.Fatal("projecting a row proposal saved or rewrote the draft")
	}
	applied, err := service.ApplyCommands(context.Background(), "project-a", "patients", rowProposalApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-projection"), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applied.Workspace.Documents[0], proposal.CandidateDocument) {
		t.Fatal("projected document differs from receipt-applied document")
	}
}

func TestRowDefinitionProposalProjectsNamedGroupAfterSavedFilter(t *testing.T) {
	setup := rowProposalSetup(t)
	store, snapshot, workspace := setup.Store, setup.Snapshot, setup.Workspace
	addRowProposalSourceFilter(t, store, workspace)
	enableRowDefinitionPreview(setup.Config)
	setup.Config.ExplicitGroupResolver = &rowProposalExplicitGroupResolver{}
	service := newTestServiceFromSetup(t, setup)
	request := rowProposalRequest(store.created, snapshot)
	request.Selection = rowdef.RowDefinitionSelection{Kind: rowdef.RowDefinitionSelectionExplicitGroup, ExplicitGroup: &rowdef.ExplicitGroupSelection{RevisionID: "group-revision-1", UnassignedMemberPolicy: ws.UnassignedMemberExclude}}
	proposal, err := service.OwnerServices().RowDefinition.ProposeRowDefinition(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.CandidateDocument.Construction == nil || len(proposal.CandidateDocument.Construction.Steps) != 1 || proposal.CandidateDocument.Construction.Steps[0].ID != "filter_source" || proposal.CandidateDocument.Rows.Groups == nil || proposal.CandidateDocument.Rows.Groups.AfterStepID != "filter_source" {
		t.Fatalf("candidate dropped downstream work or changed insertion anchor: %#v", proposal.CandidateDocument)
	}
	stages := proposal.CandidateRowIdentity.Stages
	if len(stages) != 3 || stages[1].OperationID != "filter_source" || stages[2].Identity.Kind != "NAMED_GROUP" || stages[2].Identity.InputStageID != "filter_source" || stages[2].Identity.GroupRevisionID != "group-revision-1" || stages[2].Identity.MissingPolicy != string(ws.UnassignedMemberExclude) {
		t.Fatalf("named group projection lost exact scope or empty policy: %#v", stages)
	}
	if store.saveDraftCalls != 0 {
		t.Fatal("candidate projection saved the draft")
	}
}

type importedSourceChoicePlanner struct{ calls int }

func (planner *importedSourceChoicePlanner) ListRowChoices(context.Context, capability.Snapshot, ws.Document) ([]capability.RowChoice, error) {
	planner.calls++
	return nil, errors.New("generic FHIR route planner must not run for imported source choices")
}

func TestImportedSourceCapabilityHelpersSkipGenericRowChoicePlanner(t *testing.T) {
	tests := []struct {
		name string
		call func(*rowsource.Service, rowsource.Base) (bool, string, string, int, error)
	}{

		{name: "filter", call: func(service *rowsource.Service, base rowsource.Base) (bool, string, string, int, error) {
			got, err := service.ConstructionFilterSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
			return got.Supported, got.ReasonCode, got.Reason, len(got.Choices), err
		}},
		{name: "derive", call: func(service *rowsource.Service, base rowsource.Base) (bool, string, string, int, error) {
			got, err := service.ConstructionDeriveSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
			return got.Supported, got.ReasonCode, got.Reason, len(got.Choices), err
		}},
		{name: "unpivot", call: func(service *rowsource.Service, base rowsource.Base) (bool, string, string, int, error) {
			got, err := service.ConstructionUnpivotSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
			return got.Supported, got.ReasonCode, got.Reason, len(got.Choices), err
		}},
		{name: "expand", call: func(service *rowsource.Service, base rowsource.Base) (bool, string, string, int, error) {
			got, err := service.ConstructionExpandSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
			return got.Supported, got.ReasonCode, got.Reason, len(got.Choices), err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planner := &importedSourceChoicePlanner{}
			service := newTestService(t, &fakeStore{}, Config{RowChoicePlanner: planner})
			base := constructionBase{Document: ws.Document{ImportedRecipe: &ws.ImportedRecipeRef{
				Name: "seed", TranslationVersion: "v1", Digest: "0000000000000000000000000000000000000000000000000000000000000000", Output: "DocumentReference",
			}, Rows: ws.RecordsRowDefinition(),
			}}
			supported, code, reason, choices, err := test.call(service.rowSourceService, base)
			if err != nil {
				t.Fatalf("capability helper: %v", err)
			}
			if !supported || code != "" || reason != "" || choices != 0 {
				t.Fatalf("imported capability = supported %t code %q reason %q choices %d, want supported with no live-catalog choices", supported, code, reason, choices)
			}
			if planner.calls != 0 {
				t.Fatalf("generic row-choice planner calls = %d, want 0", planner.calls)
			}
		})
	}
}

func TestImportedSourceRowProposalRejectsProjectedRowsWithoutCompilingOrSaving(t *testing.T) {
	for _, selection := range []rowdef.RowDefinitionSelection{
		{Kind: rowdef.RowDefinitionSelectionRecords},
		{Kind: rowdef.RowDefinitionSelectionExpanded, Expanded: &rowdef.ExpandedSelection{RowChoiceID: "opaque-choice-1", EmptyCollectionPolicy: ws.EmptyCollectionPreserveParent}},
		{Kind: rowdef.RowDefinitionSelectionExplicitGroup, ExplicitGroup: &rowdef.ExplicitGroupSelection{RevisionID: "group-v1", UnassignedMemberPolicy: ws.UnassignedMemberExclude}},
	} {
		t.Run(string(selection.Kind), func(t *testing.T) {
			setup := rowProposalSetup(t)
			store, snapshot, workspace := setup.Store, setup.Snapshot, setup.Workspace
			workspace.Documents[0].ImportedRecipe = &ws.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"}
			for i := range workspace.Documents[0].Columns {
				column := &workspace.Documents[0].Columns[i]
				column.Source = ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: column.Column}}
			}
			raw, err := workspace.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
			digest, err := workspace.Digest()
			if err != nil {
				t.Fatal(err)
			}
			store.created.DraftConfig = raw
			store.created.DraftDigest = digest
			original := string(raw)
			version := store.created.DraftVersion
			compileCalls := 0
			setup.Config.CompileReceipt = func(context.Context, ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
				compileCalls++
				t.Error("rejected projected source row edit reached compiler")
				return nil, nil
			}
			request := rowProposalRequest(store.created, snapshot)
			request.Selection = selection
			service := newTestServiceFromSetup(t, setup)
			_, err = service.OwnerServices().RowDefinition.ProposeRowDefinition(context.Background(), request)
			if lifecycleErrorCode(err) != "IMPORTED_SOURCE_ROW_OVERLAY_REQUIRED" {
				t.Fatalf("got %v; want precise source ownership rejection", err)
			}
			if compileCalls != 0 || store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(store.created.DraftConfig) != original {
				t.Fatal("rejected source rows edit changed saved state")
			}
		})
	}
}
