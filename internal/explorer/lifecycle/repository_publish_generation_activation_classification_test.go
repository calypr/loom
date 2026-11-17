package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func repositoryPublishIntoGenerationActivation(t *testing.T, generationErr error) (*Error, *fakeStore, error) {
	t.Helper()
	visible := true
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
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
	store := &fakeStore{activationErrors: []error{generationErr}}
	config := testConfig(snapshot)
	config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }
	config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	config.CompileReceipt = func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error) { return base, nil }
	config.MaterializeReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (Execution, error) {
		return successfulTestExecution(base, "PUBLISHED"), nil
	}
	config.ActivateRelease = func(context.Context, string, string, []dataset.DataframeSelector) error { return nil }
	service := newTestService(t, store, config)
	_, err := service.PublishRepository(context.Background(), RepositoryPublishRequest{Project: "project-a", Generation: "generation-a", Workspace: workspace, Commit: "commit-a", Actor: "alice"})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("repository publish did not surface a lifecycle error: %v", err)
	}
	return lifecycleErr, store, err
}

func TestRepositoryPublishGenerationActivationFaultStaysRetryableAndFixed(t *testing.T) {
	fault := errors.New("activate generation: arangodb transaction aborted: dial tcp 127.0.0.1:8529: connect: connection refused")
	lifecycleErr, store, err := repositoryPublishIntoGenerationActivation(t, fault)
	if lifecycleErr.Class != ClassUnavailable {
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
	if lifecycleErr.Class != ClassConflict {
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
