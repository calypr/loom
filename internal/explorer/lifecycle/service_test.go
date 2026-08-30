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
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type fakeStore struct {
	Store
	listValues []explorer.Explorer
	state      explorer.ExplorerStateV1
	created    *explorer.Explorer
	applyErr   error
	receipt    *explorer.CompilationReceipt
	publishErr error
	published  bool
	release    dataset.ProjectRelease
	revision   int64
	order      *[]string
}

func (f *fakeStore) List(context.Context, string) ([]explorer.Explorer, error) {
	return append([]explorer.Explorer(nil), f.listValues...), nil
}

func (f *fakeStore) LoadExplorerState(context.Context, string, string) (explorer.ExplorerStateV1, error) {
	return f.state, nil
}

func (f *fakeStore) CreateInteractiveFrom(_ context.Context, project, id, title, source, actor string) (*explorer.Explorer, error) {
	value := &explorer.Explorer{Project: project, ExplorerID: id, Title: title, ManagementMode: explorer.ManagementInteractive, UpdatedBy: actor}
	f.created = value
	return value, nil
}

func (f *fakeStore) Get(context.Context, string, string) (*explorer.Explorer, error) {
	if f.created != nil {
		return f.created, nil
	}
	return &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients"}, nil
}

func (f *fakeStore) ApplyWorkspaceCommands(context.Context, string, string, authoringv2.CatalogSnapshot, authoringv2.ApplyCommandsRequest, string) (*authoringv2.ApplyCommandsResponse, error) {
	if f.applyErr != nil {
		return nil, f.applyErr
	}
	return &authoringv2.ApplyCommandsResponse{}, nil
}

func (f *fakeStore) CompilationReceiptForExplorer(context.Context, string, string, string) (*explorer.CompilationReceipt, error) {
	if f.receipt == nil {
		return nil, explorer.ErrNotFound
	}
	return f.receipt, nil
}

func (f *fakeStore) ActiveRevision(context.Context, string, string) (*explorer.Revision, error) {
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

func (f *fakeStore) UpsertRepositoryV2(context.Context, explorer.CompilationReceipt, string, string, []explorer.Materialization, explorer.DatasetMetadata, explorer.PublicationMetadata) (*explorer.Explorer, *explorer.Revision, error) {
	return nil, nil, errors.New("not used")
}

func (f *fakeStore) FailRevision(context.Context, string, []explorer.Diagnostic) (*explorer.Revision, error) {
	return nil, nil
}

func (f *fakeStore) ActivateRepositoryGeneration(context.Context, string, string, string) error {
	if f.order != nil {
		*f.order = append(*f.order, "activate-generation")
	}
	return nil
}

func (f *fakeStore) SaveRepositoryConfig(context.Context, explorer.RepositoryConfig) (*explorer.RepositoryConfig, error) {
	if f.order != nil {
		*f.order = append(*f.order, "save-config")
	}
	return &explorer.RepositoryConfig{}, nil
}

func readySnapshot(project, generation, token string, scope authscope.ReadScope) capability.Snapshot {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	return capability.Snapshot{Identity: capability.SnapshotIdentity{Project: project, Generation: generation, AuthorizationScopeDigest: hex.EncodeToString(sum[:]), SchemaDigest: "schema"}, Status: capability.StatusReady, Complete: true, Token: token}
}

func testConfig(snapshot capability.Snapshot) Config {
	return Config{Capability: CapabilityResolver{
		Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		ForExecution: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		},
		Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot {
			return authoringv2.CatalogSnapshot{SnapshotToken: snapshot.Token, Complete: true, Nodes: []authoringv2.CatalogNode{{ID: "node", ResourceType: "Patient", RowRootEligible: true}}}
		},
	}}
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
	service, err := New(store, Config{})
	if err != nil {
		t.Fatal(err)
	}
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
	request := authoringv2.ApplyCommandsRequest{CommandID: "command-a", SnapshotToken: "token", Commands: []authoringv2.Command{{Type: authoringv2.CommandCreateTable, Title: "Patients", RootNodeID: "node"}}}
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
			service, _ := New(store, testConfig(snapshot))
			_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
			var value *Error
			if !errors.As(err, &value) || value.Class != ClassConflict || value.Code != test.code {
				t.Fatalf("err=%v, want conflict %s", err, test.code)
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
			service, _ := New(store, config)
			_, err := service.Preview(context.Background(), PreviewRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", Limit: 10})
			var value *Error
			if !errors.As(err, &value) || value.Code != test.wantCode {
				t.Fatalf("err=%v, want %s", err, test.wantCode)
			}
		})
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
	service, _ := New(store, config)
	result, err := service.Publish(context.Background(), PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	if err != nil || result.Revision == nil || !store.published {
		t.Fatalf("publish = %#v, err=%v", result, err)
	}
	if got, want := fmt.Sprint(order), "[validate-generation materialize prepare-release persist]"; got != want {
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
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		return dataset.ProjectRelease{}, 0, errors.New("release preparation failed")
	}
	service, _ = New(store, config)
	_, err = service.Publish(context.Background(), PublishRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, Actor: "alice"})
	if err == nil || store.published {
		t.Fatalf("preparation failure published=%v, err=%v", store.published, err)
	}
}

func TestServiceUsesConfiguredClock(t *testing.T) {
	clock := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	service, err := New(&fakeStore{}, Config{Now: func() time.Time { return clock }})
	if err != nil || service.now() != clock {
		t.Fatalf("clock=%v, err=%v", service.now(), err)
	}
}
