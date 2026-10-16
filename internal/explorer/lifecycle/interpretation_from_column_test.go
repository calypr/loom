package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestCreateInterpretationRevisionFromColumnDerivesServerOwnedRule(t *testing.T) {
	workspace := lifecycleInterpretationWorkspace("", true)
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	owner := &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 3, DraftDigest: digest}
	before := *owner
	before.DraftConfig = append([]byte(nil), owner.DraftConfig...)
	store := &fakeStore{created: owner, copyGet: true}
	repository := &libraryRepository{revisions: map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{}}
	snapshot, scope := interpretationContextSnapshot()
	service := newTestService(t, store, Config{
		InterpretationRepository: repository,
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
	})
	request := CreateInterpretationRevisionFromColumnRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 3, ExpectedDraftDigest: digest, OutputID: "patients_a", Column: "patient_id",
		LibraryID: "library-a", Explanation: "Use the saved Patient identifier", Author: "researcher",
	}

	created, err := service.CreateInterpretationRevisionFromColumn(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if repository.createSeen == nil || created.ID == "" || created.ID != repository.createSeen.ID {
		t.Fatalf("created revision = %#v; repository saw %#v", created, repository.createSeen)
	}
	wantApplicability := explorer.InterpretationApplicability{ResourceTypes: []string{"Patient"}, LogicalTypes: []string{"string"}, Cardinalities: []string{"optional_one"}}
	if !reflect.DeepEqual(created.Applicability, wantApplicability) {
		t.Fatalf("applicability = %#v, want %#v", created.Applicability, wantApplicability)
	}
	if len(created.Rules) != 1 || created.Rules[0].Match.ResourceType != "Patient" || created.Rules[0].Match.LogicalType != "string" || created.Rules[0].Match.Cardinality != "optional_one" {
		t.Fatalf("derived rule = %#v", created.Rules)
	}
	if got := created.Rules[0].Definition.Source.FieldPath(); got != "id" {
		t.Fatalf("derived definition source = %q", got)
	}
	retried, err := service.CreateInterpretationRevisionFromColumn(context.Background(), request)
	if err != nil || retried.ID != created.ID {
		t.Fatalf("idempotent retry = %#v, err=%v", retried, err)
	}
	if !reflect.DeepEqual(*owner, before) || store.saveDraftCalls != 0 {
		t.Fatalf("create-from-column mutated draft: owner=%#v before=%#v saves=%d", owner, before, store.saveDraftCalls)
	}
}

func TestCreateInterpretationRevisionFromColumnRejectsPinnedAndUnavailableSources(t *testing.T) {
	revision := lifecyclePrepareInterpretation(t)
	snapshot, scope := interpretationContextSnapshot()

	tests := []struct {
		name      string
		workspace authoringv2.Workspace
		wantCode  string
	}{
		{name: "pinned", workspace: lifecycleInterpretationWorkspace(string(revision.ID), false), wantCode: "INTERPRETATION_ALREADY_PINNED"},
		{name: "unsupported", workspace: func() authoringv2.Workspace {
			workspace := lifecycleInterpretationWorkspace("", true)
			workspace.Documents[0].Columns[0].Source = authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}
			return workspace
		}(), wantCode: "INTERPRETATION_SOURCE_UNAVAILABLE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft, err := test.workspace.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
			digest, err := test.workspace.Digest()
			if err != nil {
				t.Fatal(err)
			}
			repository := &libraryRepository{revisions: map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{revision.ID: revision}}
			service := newTestService(t, &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}}, Config{
				InterpretationRepository: repository,
				Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
					return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
				}},
			})
			_, err = service.CreateInterpretationRevisionFromColumn(context.Background(), CreateInterpretationRevisionFromColumnRequest{
				Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
				ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, OutputID: "patients_a", Column: "patient_id",
				LibraryID: "library-a", Explanation: "test", Author: "researcher",
			})
			assertLifecycleCode(t, err, test.wantCode)
			if repository.createSeen != nil {
				t.Fatalf("rejected request created revision %#v", repository.createSeen)
			}
		})
	}
}
