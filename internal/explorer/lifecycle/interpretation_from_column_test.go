package lifecycle

import (
	"context"
	"reflect"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/interpretation"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
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
	repository := &libraryRepository{revisions: map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision{}}
	snapshot, scope := interpretationContextSnapshot()
	service := newTestService(t, store, Config{
		InterpretationRepository: repository,
		Capability: ports.CapabilityResolver{ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
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
	wantApplicability := interpretation.InterpretationApplicability{ResourceTypes: []string{"Patient"}, LogicalTypes: []string{"string"}, Cardinalities: []string{"optional_one"}}
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
		workspace ws.Workspace
		wantCode  string
	}{
		{name: "pinned", workspace: lifecycleInterpretationWorkspace(string(revision.ID), false), wantCode: "INTERPRETATION_ALREADY_PINNED"},
		{name: "unsupported", workspace: func() ws.Workspace {
			workspace := lifecycleInterpretationWorkspace("", true)
			workspace.Documents[0].Columns[0].Source = ws.ColumnSource{Kind: ws.SourceProjectID}
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
			repository := &libraryRepository{revisions: map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision{revision.ID: revision}}
			service := newTestService(t, &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}}, Config{
				InterpretationRepository: repository,
				Capability: ports.CapabilityResolver{ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
					return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
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
