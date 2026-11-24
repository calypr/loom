package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/publication"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func repositoryPublishIntoCompilation(t *testing.T, receipt *receiptmodel.CompilationReceipt, compileErr error) (*ports.Error, *fakeStore, error) {
	t.Helper()
	visible := true
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
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
	store := &fakeStore{}
	config := testConfig(snapshot)
	config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }
	config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	config.CompileReceipt = func(context.Context, ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		return receipt, compileErr
	}
	config.MaterializeReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) (ports.Execution, error) {
		return successfulTestExecution(base, "PUBLISHED"), nil
	}
	service := newTestService(t, store, config)
	_, err := service.OwnerServices().Publication.PublishRepository(context.Background(), publication.RepositoryPublishRequest{Project: "project-a", Generation: "generation-a", Workspace: workspace, Commit: "commit-a", Actor: "alice"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("repository publish did not surface a lifecycle error: %v", err)
	}
	return lifecycleErr, store, err
}

func TestRepositoryPublishUntypedCompileFaultIsUnavailable(t *testing.T) {
	fault := errors.New("compile: dial tcp 127.0.0.1:8529: connect: connection refused")
	lifecycleErr, store, err := repositoryPublishIntoCompilation(t, nil, fault)
	if lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "COMPILATION_UNAVAILABLE" {
		t.Fatalf("untyped compile fault classified %q/%q, want unavailable/COMPILATION_UNAVAILABLE", lifecycleErr.Class, lifecycleErr.Code)
	}
	if !errors.Is(err, fault) {
		t.Fatalf("untyped compile fault lost its cause: %v", err)
	}
	if strings.Contains(lifecycleErr.Message, "dial tcp") || strings.Contains(lifecycleErr.Message, "connection refused") {
		t.Fatalf("compile fault republishes transport text: %q", lifecycleErr.Message)
	}
	if store.repositoryRevision != nil || store.published {
		t.Fatalf("compile fault persisted a revision: revision=%#v published=%v", store.repositoryRevision, store.published)
	}
}

func TestRepositoryPublishTypedCompileErrorPassesThrough(t *testing.T) {
	typed := &ports.Error{Class: ports.ClassForbidden, Stage: "compile", Code: "TYPED_COMPILE_DENIED", Message: "compile is forbidden"}
	lifecycleErr, _, err := repositoryPublishIntoCompilation(t, nil, typed)
	if lifecycleErr != typed {
		t.Fatalf("typed compile error was replaced: got %#v, want the original %#v", lifecycleErr, typed)
	}
	if lifecycleErr.Class != ports.ClassForbidden || lifecycleErr.Code != "TYPED_COMPILE_DENIED" {
		t.Fatalf("typed compile error classified %q/%q, want unchanged forbidden/TYPED_COMPILE_DENIED", lifecycleErr.Class, lifecycleErr.Code)
	}
	if !errors.Is(err, typed) {
		t.Fatalf("typed compile error lost its cause: %v", err)
	}
}

func TestRepositoryPublishCompileCancellationAndDeadlineAreUnavailable(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "cancelled", err: fmt.Errorf("compile repository workspace: %w", context.Canceled)},
		{name: "deadline", err: fmt.Errorf("compile repository workspace: %w", context.DeadlineExceeded)},
	} {
		t.Run(test.name, func(t *testing.T) {
			lifecycleErr, _, err := repositoryPublishIntoCompilation(t, nil, test.err)
			if lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "COMPILATION_UNAVAILABLE" {
				t.Fatalf("%s classified %q/%q, want unavailable/COMPILATION_UNAVAILABLE", test.name, lifecycleErr.Class, lifecycleErr.Code)
			}
			if lifecycleErr.Class == ports.ClassUnprocessable {
				t.Fatalf("%s surfaced as a client validation failure", test.name)
			}
			if !errors.Is(err, test.err) || !errors.Is(err, errors.Unwrap(test.err)) {
				t.Fatalf("%s lost its cancellation/deadline cause: %v", test.name, err)
			}
		})
	}
}

func TestRepositoryPublishDeterministicCompilerValidationStaysUnprocessable(t *testing.T) {
	compileErr := &explorercompilation.Error{
		Stage:   "intent",
		Code:    "STALE_ROOT_NODE",
		Path:    "documents[0].root",
		Message: "the selected root node no longer exists",
	}
	lifecycleErr, _, err := repositoryPublishIntoCompilation(t, nil, compileErr)
	if lifecycleErr.Class != ports.ClassUnprocessable || lifecycleErr.Code != "INVALID_ROOT_NODE" {
		t.Fatalf("deterministic compile validation classified %q/%q, want unprocessable/INVALID_ROOT_NODE", lifecycleErr.Class, lifecycleErr.Code)
	}
	if lifecycleErr.Message != compileErr.Message {
		t.Fatalf("compile validation message = %q, want %q", lifecycleErr.Message, compileErr.Message)
	}
	if !errors.Is(err, compileErr) {
		t.Fatalf("compile validation lost its cause: %v", err)
	}
}

func TestRepositoryPublishAuthoringErrorKeepsConflictClassAndCode(t *testing.T) {
	compileErr := &explorer.AuthoringError{
		Status: 409,
		Diagnostic: explorer.AuthoringDiagnostic{
			Severity: "ERROR", Stage: "compile", Code: "COMPILATION_CONTRACT_MISMATCH",
			Message: "the compiled output contract changed",
		},
	}
	lifecycleErr, _, err := repositoryPublishIntoCompilation(t, nil, compileErr)
	if lifecycleErr.Class != ports.ClassConflict || lifecycleErr.Code != "COMPILATION_CONTRACT_MISMATCH" {
		t.Fatalf("authoring error classified %q/%q, want conflict/COMPILATION_CONTRACT_MISMATCH", lifecycleErr.Class, lifecycleErr.Code)
	}
	if !errors.Is(err, compileErr) {
		t.Fatalf("authoring error lost its cause: %v", err)
	}
}

func TestRepositoryPublishStaleCapabilityIsConflict(t *testing.T) {
	lifecycleErr, _, err := repositoryPublishIntoCompilation(t, nil, capability.ErrStaleSnapshot)
	if lifecycleErr.Class != ports.ClassConflict || lifecycleErr.Code != "STALE_CATALOG_SNAPSHOT" {
		t.Fatalf("stale snapshot classified %q/%q, want conflict/STALE_CATALOG_SNAPSHOT", lifecycleErr.Class, lifecycleErr.Code)
	}
	if !errors.Is(err, capability.ErrStaleSnapshot) {
		t.Fatalf("stale snapshot lost its cause: %v", err)
	}
}

func TestRepositoryPublishNilCompileReceiptStaysCompilationFailedUnavailable(t *testing.T) {
	lifecycleErr, _, _ := repositoryPublishIntoCompilation(t, nil, nil)
	if lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "COMPILATION_FAILED" {
		t.Fatalf("nil receipt classified %q/%q, want unavailable/COMPILATION_FAILED", lifecycleErr.Class, lifecycleErr.Code)
	}
}

func publishIntoReleasePreparation(t *testing.T, prepareErr error) (*ports.Error, *fakeStore, error) {
	t.Helper()
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	store := &fakeStore{receipt: receipt}
	config := testConfig(snapshot)
	config.ValidateReleaseGeneration = func(context.Context, string, string) error { return nil }
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		return dataset.ProjectRelease{}, 0, prepareErr
	}
	config.MaterializeReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) (ports.Execution, error) {
		return successfulTestExecution(receipt, "PUBLISHED"), nil
	}
	service := newTestService(t, store, config)
	_, err := service.OwnerServices().Publication.Publish(context.Background(), publication.PublishRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID, Actor: "operator"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("publish did not surface a lifecycle error: %v", err)
	}
	return lifecycleErr, store, err
}

func TestPublishPermanentRequirementsMissIsUnprocessableAndExplicitlyNonRetryable(t *testing.T) {
	miss := &dataset.ReleaseRequirementsError{Verifications: []dataset.ContractVerification{{
		Selector:   dataset.DataframeSelector{Recipe: "patients", TranslationVersion: "v1", Output: "list"},
		Generation: "generation-a",
		State:      "PUBLISHED",
		ErrorCode:  "PUBLISHED_ROW_COUNT_ZERO",
	}}}
	lifecycleErr, store, err := publishIntoReleasePreparation(t, miss)
	if lifecycleErr.Class != ports.ClassUnprocessable {
		t.Fatalf("F-B14 RED: permanent requirements miss classified %q (want %q); indistinguishable from a transient outage", lifecycleErr.Class, ports.ClassUnprocessable)
	}
	if lifecycleErr.Code != "RELEASE_REQUIREMENTS_UNMET" {
		t.Fatalf("permanent miss code = %q, want RELEASE_REQUIREMENTS_UNMET (distinct from MATERIALIZATION_ACTIVATION_FAILED)", lifecycleErr.Code)
	}
	if lifecycleErr.Stage != "activation" {
		t.Fatalf("permanent miss stage = %q, want activation", lifecycleErr.Stage)
	}
	if retryable, ok := lifecycleErr.Details["retryable"]; !ok || retryable != false {
		t.Fatalf("permanent miss details lack explicit retryable=false: %v", lifecycleErr.Details)
	}
	var typed *dataset.ReleaseRequirementsError
	if !errors.As(err, &typed) || !errors.Is(err, dataset.ErrReleaseRequirementsUnmet) {
		t.Fatalf("typed requirements cause not preserved for server-side classification/log: %v", err)
	}
	if store.published {
		t.Fatal("requirements miss published a revision")
	}
}

func TestPublishGenuinePreparationFailureRemainsUnavailable(t *testing.T) {
	lifecycleErr, store, _ := publishIntoReleasePreparation(t, errors.New("release preparation failed"))
	if lifecycleErr.Class != ports.ClassUnavailable {
		t.Fatalf("genuine preparation failure classified %q, want %q", lifecycleErr.Class, ports.ClassUnavailable)
	}
	if lifecycleErr.Code != "MATERIALIZATION_ACTIVATION_FAILED" {
		t.Fatalf("genuine preparation failure code = %q, want MATERIALIZATION_ACTIVATION_FAILED", lifecycleErr.Code)
	}
	if store.published {
		t.Fatal("preparation failure published a revision")
	}
}

func repositoryPublishIntoActivation(t *testing.T, activateErr error) (*ports.Error, *fakeStore, error) {
	t.Helper()
	visible := true
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
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
	store := &fakeStore{}
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
	config.ActivateRelease = func(context.Context, string, string, []dataset.DataframeSelector) error { return activateErr }
	service := newTestService(t, store, config)
	_, err := service.OwnerServices().Publication.PublishRepository(context.Background(), publication.RepositoryPublishRequest{Project: "project-a", Generation: "generation-a", Workspace: workspace, Commit: "commit-a", Actor: "alice"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("repository publish did not surface a lifecycle error: %v", err)
	}
	return lifecycleErr, store, err
}

func TestRepositoryPublishRequirementsMissKeepsConflictNonRetryable(t *testing.T) {
	miss := &dataset.ReleaseRequirementsError{Verifications: []dataset.ContractVerification{{
		Selector:   dataset.DataframeSelector{Recipe: "patients", TranslationVersion: "v1", Output: "list"},
		Generation: "generation-a",
		State:      "PUBLISHED",
		ErrorCode:  "RELEASE_REQUIREMENTS_UNMET",
	}}}
	lifecycleErr, store, err := repositoryPublishIntoActivation(t, miss)
	if lifecycleErr.Class != ports.ClassConflict {
		t.Fatalf("requirements miss classified %q, want conflict per activation contract", lifecycleErr.Class)
	}
	if lifecycleErr.Code != "RELEASE_ACTIVATION_FAILED" {
		t.Fatalf("miss code = %q, want RELEASE_ACTIVATION_FAILED", lifecycleErr.Code)
	}
	if retryable, ok := lifecycleErr.Details["retryable"]; !ok || retryable != false {
		t.Fatalf("miss lacks explicit retryable=false: %v", lifecycleErr.Details)
	}
	if store.repositoryRevision == nil || len(store.repositoryRevision.Diagnostics) != 1 || store.repositoryRevision.Diagnostics[0].Retryable {
		t.Fatalf("miss diagnostic should be non-retryable: %#v", store.repositoryRevision)
	}
	var typed *dataset.ReleaseRequirementsError
	if !errors.As(err, &typed) {
		t.Fatalf("typed cause lost: %v", err)
	}
}

func TestRepositoryPublishActivationFaultIsRetryableUnavailable(t *testing.T) {
	fault := errors.New("arangodb: connection reset by peer")
	lifecycleErr, store, err := repositoryPublishIntoActivation(t, fault)
	if lifecycleErr.Class != ports.ClassUnavailable {
		t.Fatalf("F-B14b RED: activation store fault classified %q (want unavailable); a conflict tells the client the request itself is wrong", lifecycleErr.Class)
	}
	if lifecycleErr.Code != "RELEASE_ACTIVATION_FAILED" {
		t.Fatalf("fault code = %q, want RELEASE_ACTIVATION_FAILED", lifecycleErr.Code)
	}
	if !errors.Is(err, fault) {
		t.Fatalf("store fault must stay errors.Is-visible: %v", err)
	}
	if store.repositoryRevision == nil || len(store.repositoryRevision.Diagnostics) != 1 || !store.repositoryRevision.Diagnostics[0].Retryable {
		t.Fatalf("fault diagnostic must be retryable: %#v", store.repositoryRevision)
	}
}

func repositoryPublishIntoGenerationActivation(t *testing.T, generationErr error) (*ports.Error, *fakeStore, error) {
	t.Helper()
	visible := true
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
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
	store := &fakeStore{activationErrors: []error{generationErr}}
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
	config.ActivateRelease = func(context.Context, string, string, []dataset.DataframeSelector) error { return nil }
	service := newTestService(t, store, config)
	_, err := service.OwnerServices().Publication.PublishRepository(context.Background(), publication.RepositoryPublishRequest{Project: "project-a", Generation: "generation-a", Workspace: workspace, Commit: "commit-a", Actor: "alice"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("repository publish did not surface a lifecycle error: %v", err)
	}
	return lifecycleErr, store, err
}

func TestRepositoryPublishGenerationActivationFaultStaysRetryableAndFixed(t *testing.T) {
	fault := errors.New("activate generation: arangodb transaction aborted: dial tcp 127.0.0.1:8529: connect: connection refused")
	lifecycleErr, store, err := repositoryPublishIntoGenerationActivation(t, fault)
	if lifecycleErr.Class != ports.ClassUnavailable {
		t.Fatalf("generation-activation store fault classified %q (want unavailable); a 409 on a transport fault tells the client the request itself is wrong and invites non-retryable handling", lifecycleErr.Class)
	}
	if lifecycleErr.Code != "GENERATION_ACTIVATION_FAILED" {
		t.Fatalf("fault code = %q, want GENERATION_ACTIVATION_FAILED", lifecycleErr.Code)
	}
	if strings.Contains(lifecycleErr.Message, "dial tcp") || strings.Contains(lifecycleErr.Message, "arangodb") || strings.Contains(lifecycleErr.Message, "activate generation") {
		t.Fatalf("generation-activation fault republishes wrapped transport text: %q", lifecycleErr.Message)
	}
	if !errors.Is(err, fault) {
		t.Fatalf("store fault must stay errors.Is-visible: %v", err)
	}
	if store.repositoryRevision == nil || len(store.repositoryRevision.Diagnostics) != 1 ||
		store.repositoryRevision.Diagnostics[0].Code != "GENERATION_ACTIVATION_FAILED" ||
		!store.repositoryRevision.Diagnostics[0].Retryable {
		t.Fatalf("fault diagnostic must be the stable retryable code: %#v", store.repositoryRevision)
	}
}

func TestRepositoryPublishGenerationActivationDraftConflictIsFixedTextConflict(t *testing.T) {
	lifecycleErr, store, err := repositoryPublishIntoGenerationActivation(t, explorer.ErrDraftConflict)
	if lifecycleErr.Class != ports.ClassConflict {
		t.Fatalf("deterministic lost generation race classified %q (want conflict)", lifecycleErr.Class)
	}
	if lifecycleErr.Code != "GENERATION_ACTIVATION_FAILED" {
		t.Fatalf("race code = %q, want GENERATION_ACTIVATION_FAILED", lifecycleErr.Code)
	}
	if strings.Contains(lifecycleErr.Message, "draft conflict") {
		t.Fatalf("race message echoes the sentinel text instead of fixed contract wording: %q", lifecycleErr.Message)
	}
	if !strings.Contains(strings.ToLower(lifecycleErr.Message), "retained") {
		t.Fatalf("race message must state what the client keeps: %q", lifecycleErr.Message)
	}
	if _, retryable := lifecycleErr.Details["retryable"]; retryable {
		t.Fatalf("deterministic lost race advertises a retry hint: %#v", lifecycleErr.Details)
	}
	if !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("wrapped cause no longer carries the guard sentinel: %v", err)
	}
	if store.repositoryRevision == nil || len(store.repositoryRevision.Diagnostics) != 1 ||
		store.repositoryRevision.Diagnostics[0].Code != "GENERATION_ACTIVATION_FAILED" ||
		store.repositoryRevision.Diagnostics[0].Retryable {
		t.Fatalf("lost-race diagnostic must be non-retryable (hammering cannot win): %#v", store.repositoryRevision)
	}
}
