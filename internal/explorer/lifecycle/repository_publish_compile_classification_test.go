package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
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
	_, err := service.PublishRepository(context.Background(), RepositoryPublishRequest{Project: "project-a", Generation: "generation-a", Workspace: workspace, Commit: "commit-a", Actor: "alice"})
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
