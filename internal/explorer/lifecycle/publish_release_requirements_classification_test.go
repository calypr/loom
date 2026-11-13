package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func publishIntoReleasePreparation(t *testing.T, prepareErr error) (*Error, *fakeStore, error) {
	t.Helper()
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	store := &fakeStore{receipt: receipt}
	config := testConfig(snapshot)
	config.ValidateReleaseGeneration = func(context.Context, string, string) error { return nil }
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		return dataset.ProjectRelease{}, 0, prepareErr
	}
	config.MaterializeReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (Execution, error) {
		return successfulTestExecution(receipt, "PUBLISHED"), nil
	}
	service := newTestService(t, store, config)
	_, err := service.Publish(context.Background(), PublishRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID, Actor: "operator"})
	var lifecycleErr *Error
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
	if lifecycleErr.Class != ClassUnprocessable {
		t.Fatalf("F-B14 RED: permanent requirements miss classified %q (want %q); indistinguishable from a transient outage", lifecycleErr.Class, ClassUnprocessable)
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
	if lifecycleErr.Class != ClassUnavailable {
		t.Fatalf("genuine preparation failure classified %q, want %q", lifecycleErr.Class, ClassUnavailable)
	}
	if lifecycleErr.Code != "MATERIALIZATION_ACTIVATION_FAILED" {
		t.Fatalf("genuine preparation failure code = %q, want MATERIALIZATION_ACTIVATION_FAILED", lifecycleErr.Code)
	}
	if store.published {
		t.Fatal("preparation failure published a revision")
	}
}

func repositoryPublishIntoActivation(t *testing.T, activateErr error) (*Error, *fakeStore, error) {
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
	store := &fakeStore{}
	config := testConfig(snapshot)
	config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }
	config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	config.CompileReceipt = func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error) { return base, nil }
	config.MaterializeReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (Execution, error) {
		return successfulTestExecution(base, "PUBLISHED"), nil
	}
	config.ActivateRelease = func(context.Context, string, string, []dataset.DataframeSelector) error { return activateErr }
	service := newTestService(t, store, config)
	_, err := service.PublishRepository(context.Background(), RepositoryPublishRequest{Project: "project-a", Generation: "generation-a", Workspace: workspace, Commit: "commit-a", Actor: "alice"})
	var lifecycleErr *Error
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
	if lifecycleErr.Class != ClassConflict {
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
	if lifecycleErr.Class != ClassUnavailable {
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
