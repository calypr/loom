package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestRepositoryPublishReleaseCASConflictKeepsPriorPointerAndFailsLosingRevision(t *testing.T) {
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
	activateErr := error(nil)
	config.ActivateRelease = func(context.Context, string, string, []dataset.DataframeSelector) error { return activateErr }
	service := newTestService(t, store, config)

	publish := func(commit string) (RepositoryPublishResult, error) {
		return service.PublishRepository(context.Background(), RepositoryPublishRequest{Project: "project-a", Generation: "generation-a", Workspace: workspace, Commit: commit, Actor: "alice"})
	}

	first, err := publish("commit-prior")
	if err != nil || first.Revision == nil || first.Revision.Status != explorer.RevisionActive {
		t.Fatalf("baseline publish = %#v, err = %v, want an active prior revision", first, err)
	}
	priorID := store.repositoryOwner.ActiveRevisionID
	if priorID == "" || store.repositoryOwner.ActiveRevisionID != first.Revision.ID {
		t.Fatalf("baseline owner active pointer = %q, want %q", store.repositoryOwner.ActiveRevisionID, first.Revision.ID)
	}

	activateErr = fmt.Errorf("activate release: arango preconditions emptied: a stale expected revision never wins the CAS: %w", dataset.ErrReleaseActivationConflict)
	_, err = publish("commit-conflict")
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("conflicted publish did not surface a lifecycle error: %v", err)
	}
	if lifecycleErr.Class != ClassConflict || lifecycleErr.Code != "PUBLICATION_ACTIVATION_CONFLICT" {
		t.Fatalf("conflicted publish classified %q/%q, want conflict/PUBLICATION_ACTIVATION_CONFLICT", lifecycleErr.Class, lifecycleErr.Code)
	}

	if len(store.revisions) != 2 {
		t.Fatalf("revisions = %d, want the retained prior plus the retained losing candidate", len(store.revisions))
	}
	priorRevision := store.revisions[priorID]
	if priorRevision == nil || priorRevision.Status != explorer.RevisionActive || len(priorRevision.Diagnostics) != 0 {
		t.Fatalf("prior revision was not retained active and clean: %#v", priorRevision)
	}
	if store.repositoryOwner.ActiveRevisionID != priorID {
		t.Fatalf("conflicted publish moved the active pointer to %q, want %q", store.repositoryOwner.ActiveRevisionID, priorID)
	}
	var losing *explorer.Revision
	for id, revision := range store.revisions {
		if id != priorID {
			losing = revision
		}
	}
	if losing == nil {
		t.Fatal("losing candidate revision was not retained")
	}
	if losing.Status != explorer.RevisionFailed || losing.Publication.State != string(explorer.RevisionFailed) || losing.FailedAt == nil {
		t.Fatalf("losing candidate revision = %#v, want retained fail-marked status", losing)
	}
	if len(losing.Diagnostics) != 1 || losing.Diagnostics[0].Code != "PUBLICATION_ACTIVATION_CONFLICT" || losing.Diagnostics[0].Retryable {
		t.Fatalf("losing candidate diagnostics = %#v, want one non-retryable PUBLICATION_ACTIVATION_CONFLICT", losing.Diagnostics)
	}
	if !errors.Is(err, dataset.ErrReleaseActivationConflict) {
		t.Fatalf("wire error lost the store conflict sentinel: %v", err)
	}
}
