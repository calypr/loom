package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/explorer/publication"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func TestRepositoryPublishReleaseCASConflictKeepsPriorPointerAndFailsLosingRevision(t *testing.T) {
	t.Run("plain project", func(t *testing.T) {
		testRepositoryPublishReleaseCASConflict(t, "project-a", "project-a")
	})
	t.Run("slash project", func(t *testing.T) {
		testRepositoryPublishReleaseCASConflict(t, "HTAN/Project", "HTAN-Project")
	})
}

type publicationProjectStore struct {
	*fakeStore
	t                       *testing.T
	project, storageProject string
}

func (s publicationProjectStore) ActivateRepositoryGeneration(ctx context.Context, release dataset.ProjectRelease, expectedRevision int64, revisionID string) error {
	s.t.Helper()
	if release.Project != s.storageProject {
		s.t.Fatalf("activation project = %q, want storage project %q", release.Project, s.storageProject)
	}
	return s.fakeStore.ActivateRepositoryGeneration(ctx, release, expectedRevision, revisionID)
}

func (s publicationProjectStore) PublishAuthoring(ctx context.Context, receipt receiptmodel.CompilationReceipt, revision explorer.Revision, release dataset.ProjectRelease, expectedReleaseRevision int64) (*explorer.Revision, error) {
	s.t.Helper()
	if receipt.Project != s.project || revision.Project != s.storageProject {
		s.t.Fatalf("authoring receipt/revision projects = (%q, %q), want (%q, %q)", receipt.Project, revision.Project, s.release.Project, s.storageProject)
	}
	return s.fakeStore.PublishAuthoring(ctx, receipt, revision, release, expectedReleaseRevision)
}

func testRepositoryPublishReleaseCASConflict(t *testing.T, project, storageProject string) {
	visible := true
	snapshot := readySnapshot(project, "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
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
	activateErr := error(nil)
	config.PrepareRepositoryRelease = prepareRepositoryRelease(func(context.Context, string, string, []dataset.DataframeSelector) error { return activateErr })
	service := newTestService(t, store, config, func(setup *testServiceSetup) {
		setup.Config.RepositoryActivator = publicationProjectStore{fakeStore: store, t: t, project: project, storageProject: storageProject}
	})

	publish := func(commit string) (publication.RepositoryPublishResult, error) {
		return service.OwnerServices().Publication.PublishRepository(context.Background(), publication.RepositoryPublishRequest{Project: project, Generation: "generation-a", Workspace: workspace, Commit: commit, Actor: "alice"})
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
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("conflicted publish did not surface a lifecycle error: %v", err)
	}
	if lifecycleErr.Class != ports.ClassConflict || lifecycleErr.Code != "PUBLICATION_ACTIVATION_CONFLICT" {
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

func prepareRepositoryRelease(next func(context.Context, string, string, []dataset.DataframeSelector) error) publication.ReleasePreparer {
	return func(ctx context.Context, project, generation string, selectors []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		return dataset.ProjectRelease{Project: project, Generation: generation}, 0, next(ctx, project, generation, selectors)
	}
}
