package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

type interpretationContextRepository struct {
	libraries []explorer.InterpretationLibrary
	revisions map[explorer.InterpretationRevisionID]explorer.InterpretationRevision
	reads     map[explorer.InterpretationRevisionID]int
}

func (repository *interpretationContextRepository) ListInterpretationLibraries(context.Context, string) ([]explorer.InterpretationLibrary, error) {
	return append([]explorer.InterpretationLibrary(nil), repository.libraries...), nil
}

func (repository *interpretationContextRepository) GetInterpretationRevision(_ context.Context, project string, id explorer.InterpretationRevisionID) (*explorer.InterpretationRevision, error) {
	if repository.reads == nil {
		repository.reads = make(map[explorer.InterpretationRevisionID]int)
	}
	repository.reads[id]++
	revision, ok := repository.revisions[id]
	if !ok || revision.Project != project {
		return nil, explorer.ErrNotFound
	}
	copy := revision
	return &copy, nil
}

func (*interpretationContextRepository) CreateInterpretationRevision(context.Context, explorer.InterpretationRevision, *explorer.InterpretationRevisionID) (*explorer.InterpretationRevision, error) {
	return nil, errors.New("not used")
}

func TestConfiguredColumnContextProjectsServerOwnedResolutionWithoutMutation(t *testing.T) {
	revision := lifecyclePrepareInterpretation(t)
	workspace := lifecycleInterpretationWorkspace(string(revision.ID), false)
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns,
		authoringv2.Column{Column: "project", Label: "Project", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}},
		authoringv2.Column{Column: "missing", Label: "Missing", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "name.family", ProjectionMode: "FIRST"}}},
	)
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	owner := &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", DraftConfig: draft, DraftVersion: 7, DraftDigest: digest}
	before := *owner
	before.DraftConfig = append([]byte(nil), owner.DraftConfig...)
	store := &fakeStore{created: owner, copyGet: true}
	repository := &interpretationContextRepository{
		libraries: []explorer.InterpretationLibrary{{ID: revision.LibraryID, Project: revision.Project, HeadRevisionID: revision.ID, HeadDigest: revision.ContentDigest}},
		revisions: map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{revision.ID: revision},
	}
	snapshot, scope := interpretationContextSnapshot()
	service := newTestService(t, store, Config{
		InterpretationRepository: repository,
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
	})

	result, err := service.ConfiguredColumnContext(context.Background(), ConfiguredColumnContextRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SnapshotToken != snapshot.Token || result.DraftVersion != 7 || result.DraftDigest != digest {
		t.Fatalf("freshness = %#v", result)
	}
	if len(result.Libraries) != 1 || result.Libraries[0].Head == nil || result.Libraries[0].Head.ID != string(revision.ID) {
		t.Fatalf("libraries = %#v", result.Libraries)
	}
	if len(result.PinnedRevisions) != 1 || result.PinnedRevisions[0].ID != string(revision.ID) {
		t.Fatalf("pinned revisions = %#v", result.PinnedRevisions)
	}
	if repository.reads[revision.ID] != 1 {
		t.Fatalf("revision reads = %#v, want one deduplicated load", repository.reads)
	}
	states := make(map[string]explorercompilation.InterpretationCandidateResolutionState, len(result.Columns))
	for _, column := range result.Columns {
		states[column.Column] = column.Resolution.State()
		if column.Resolution.State() != explorercompilation.InterpretationCandidateReady {
			continue
		}
		ready, ok := column.Resolution.Ready()
		if !ok || !reflect.DeepEqual(ready.CapabilityCandidateIDs, []string{"candidate-id"}) || !reflect.DeepEqual(ready.ApplicableRevisionIDs, []string{string(revision.ID)}) {
			t.Fatalf("ready resolution for %s = %#v", column.Column, column.Resolution)
		}
	}
	if states["patient_id"] != explorercompilation.InterpretationCandidateReady || states["project"] != explorercompilation.InterpretationCandidateUnsupported || states["missing"] != explorercompilation.InterpretationCandidateMissing {
		t.Fatalf("column states = %#v", states)
	}
	if !reflect.DeepEqual(*owner, before) || store.saveDraftCalls != 0 {
		t.Fatalf("context read mutated draft: owner=%#v before=%#v saves=%d", owner, before, store.saveDraftCalls)
	}
}

func TestConfiguredColumnContextRejectsStaleDraftAndScope(t *testing.T) {
	revision := lifecyclePrepareInterpretation(t)
	workspace := lifecycleInterpretationWorkspace(string(revision.ID), false)
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, scope := interpretationContextSnapshot()
	repository := &interpretationContextRepository{revisions: map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{revision.ID: revision}}
	request := ConfiguredColumnContextRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, ExpectedDraftVersion: 7, ExpectedDraftDigest: digest}

	t.Run("draft", func(t *testing.T) {
		service := newTestService(t, &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 8, DraftDigest: digest}}, Config{
			InterpretationRepository: repository,
			Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			}},
		})
		_, err := service.ConfiguredColumnContext(context.Background(), request)
		assertLifecycleCode(t, err, "DRAFT_CONFLICT")
	})

	t.Run("authorization scope", func(t *testing.T) {
		service := newTestService(t, &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 7, DraftDigest: digest}}, Config{
			InterpretationRepository: repository,
			Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeRestricted}}, nil
			}},
		})
		_, err := service.ConfiguredColumnContext(context.Background(), request)
		assertLifecycleCode(t, err, "STALE_AUTHORIZATION_SCOPE")
	})

	t.Run("snapshot", func(t *testing.T) {
		service := newTestService(t, &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 7, DraftDigest: digest}}, Config{
			InterpretationRepository: repository,
			Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{}, errors.New("snapshot expired")
			}},
		})
		_, err := service.ConfiguredColumnContext(context.Background(), request)
		assertLifecycleCode(t, err, "STALE_CATALOG_SNAPSHOT")
	})
}

func TestConfiguredColumnContextRejectsInvalidRoute(t *testing.T) {
	revision := lifecyclePrepareInterpretation(t)
	workspace := lifecycleInterpretationWorkspace(string(revision.ID), false)
	workspace.Documents[0].Route.Children = []authoringv2.RouteNode{{OccurrenceID: "observation", ResourceType: "Observation", Relationship: "subject"}}
	workspace.Documents[0].Columns[0].OccurrenceID = "observation"
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, scope := interpretationContextSnapshot()
	repository := &interpretationContextRepository{revisions: map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{revision.ID: revision}}
	service := newTestService(t, &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}}, Config{
		InterpretationRepository: repository,
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
	})
	_, err = service.ConfiguredColumnContext(context.Background(), ConfiguredColumnContextRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
	})
	assertLifecycleCode(t, err, "STALE_COLUMN_ROUTE")
}

func TestConfiguredColumnContextRejectsWorkAboveExplicitLimits(t *testing.T) {
	snapshot, scope := interpretationContextSnapshot()
	capabilityResolver := CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}}

	t.Run("columns", func(t *testing.T) {
		workspace := lifecycleInterpretationWorkspace("", true)
		workspace.Documents = workspace.Documents[:1]
		workspace.Tabs = workspace.Tabs[:1]
		workspace.Documents[0].Columns = make([]authoringv2.Column, maxInterpretationContextColumns+1)
		for index := range workspace.Documents[0].Columns {
			workspace.Documents[0].Columns[index] = authoringv2.Column{
				Column: fmt.Sprintf("column_%03d", index), Label: fmt.Sprintf("Column %d", index), OccurrenceID: authoringv2.RootOccurrenceID,
				Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID},
			}
		}
		draft, err := workspace.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		digest, err := workspace.Digest()
		if err != nil {
			t.Fatal(err)
		}
		service := newTestService(t, &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}}, Config{
			InterpretationRepository: &interpretationContextRepository{}, Capability: capabilityResolver,
		})
		_, err = service.ConfiguredColumnContext(context.Background(), ConfiguredColumnContextRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest})
		assertLifecycleCode(t, err, "INTERPRETATION_CONTEXT_LIMIT")
	})

	t.Run("libraries", func(t *testing.T) {
		workspace := lifecycleInterpretationWorkspace("", true)
		draft, err := workspace.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		digest, err := workspace.Digest()
		if err != nil {
			t.Fatal(err)
		}
		libraries := make([]explorer.InterpretationLibrary, maxInterpretationContextLibraries+1)
		for index := range libraries {
			libraries[index] = explorer.InterpretationLibrary{ID: explorer.InterpretationLibraryID(fmt.Sprintf("library-%03d", index)), Project: "project-a"}
		}
		service := newTestService(t, &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}}, Config{
			InterpretationRepository: &interpretationContextRepository{libraries: libraries}, Capability: capabilityResolver,
		})
		_, err = service.ConfiguredColumnContext(context.Background(), ConfiguredColumnContextRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest})
		assertLifecycleCode(t, err, "INTERPRETATION_CONTEXT_LIMIT")
	})
}

func interpretationContextSnapshot() (capability.Snapshot, authscope.ReadScope) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	identity := readySnapshot("project-a", "generation-a", "unused", scope).Identity
	snapshot := capability.NewSnapshot(identity,
		capability.Policy{Projection: capability.ProjectionPolicy{Modes: []capability.ProjectionMode{capability.ProjectionScalar}}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"}}, nil,
		[]capability.Candidate{{ID: "candidate-id", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}}}, nil,
	)
	return snapshot, scope
}

func assertLifecycleCode(t *testing.T, err error, code string) {
	t.Helper()
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != code {
		t.Fatalf("error = %v, want lifecycle code %s", err, code)
	}
}
