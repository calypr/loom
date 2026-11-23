package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	explorerselection "github.com/calypr/loom/internal/explorer/selection"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

type formalRemovalCase struct {
	ID                    string                   `json:"id"`
	ReceiptMatches        *bool                    `json:"receiptMatches"`
	ProvenanceMatches     *bool                    `json:"provenanceMatches"`
	AuthorizationMatches  *bool                    `json:"authorizationMatches"`
	PreviewCompletes      *bool                    `json:"previewCompletes"`
	ConcurrentDraftChange *bool                    `json:"concurrentDraftChange"`
	Expected              formalRemovalExpectation `json:"expected"`
}

type formalRemovalExpectation struct {
	Accepted        bool `json:"accepted"`
	PreviewCalls    int  `json:"previewCalls"`
	SaveAttempts    int  `json:"saveAttempts"`
	SuccessfulSaves int  `json:"successfulSaves"`
	PreservedHead   bool `json:"preservedHead"`
	ExactCandidate  bool `json:"exactCandidate"`
	Conflict        bool `json:"conflict"`
}
type formalRemovalResult struct {
	ID              string `json:"id"`
	Accepted        bool   `json:"accepted"`
	PreviewCalls    int    `json:"previewCalls"`
	SaveAttempts    int    `json:"saveAttempts"`
	SuccessfulSaves int    `json:"successfulSaves"`
	PreservedHead   bool   `json:"preservedHead"`
	ExactCandidate  bool   `json:"exactCandidate"`
	Conflict        bool   `json:"conflict"`
	Error           string `json:"error"`
}
type formalRemovalStore struct {
	*populationProposalTestStore
	attempts int
	writes   int
}

func (s *formalRemovalStore) SaveDraft(ctx context.Context, value explorer.Explorer, version int64, digests ...string) (*explorer.Explorer, error) {
	s.attempts++
	if len(digests) != 1 || version != s.created.DraftVersion || digests[0] != s.created.DraftDigest {
		return nil, explorer.ErrDraftConflict
	}
	result, err := s.fakeStore.SaveDraft(ctx, value, version, digests...)
	if err == nil {
		s.writes++
	}
	return result, err
}
func formalRemovalObserve(t *testing.T, c formalRemovalCase) formalRemovalResult {
	t.Helper()
	for _, field := range []*bool{c.ReceiptMatches, c.ProvenanceMatches, c.AuthorizationMatches, c.PreviewCompletes, c.ConcurrentDraftChange} {
		if field == nil {
			t.Fatal("missing removal choice")
		}
	}
	if c.ID == "" {
		t.Fatal("missing removal case ID")
	}
	service, base, snapshot, before := populationMemberProposalFixture(t)
	store := &formalRemovalStore{populationProposalTestStore: base}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err = New(domain, service.config)
	if err != nil {
		t.Fatal(err)
	}
	removed := explorerselection.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
	proposal, err := service.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(base, snapshot, removed))
	if err != nil {
		t.Fatal(err)
	}
	if store.attempts != 0 {
		t.Fatal("proposal changed draft")
	}
	originalHead := *store.created
	originalHead.DraftConfig = append([]byte(nil), store.created.DraftConfig...)
	expectedHead := originalHead
	candidateID := proposal.CandidateSelection.ID
	expectedWorkspace, err := ws.DecodeWorkspace(originalHead.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	expectedWorkspace.Documents[0].Population.SelectionRevisionID = candidateID
	expectedBytes, err := expectedWorkspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	expectedDigest, err := expectedWorkspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	members := store.members[candidateID]
	if len(members) != 1 || members[0].Ref.ID != "patient-1" {
		t.Fatal("proposal did not produce literal survivor")
	}
	if !*c.ReceiptMatches {
		receipt := store.receipt
		binding := *receipt.PopulationMemberRemovalProposal
		binding.DraftVersion++
		receipt.PopulationMemberRemovalProposal = &binding
		receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
		if err != nil {
			t.Fatal(err)
		}
		receipt.ID, err = receiptmodel.ReceiptID(*receipt)
		if err != nil {
			t.Fatal(err)
		}
		proposal.ProposalID = receipt.ID
	}
	if !*c.ProvenanceMatches {
		candidate := store.selections[candidateID]
		candidate.Source.RevisionID = "foreign-base"
		store.selections[candidateID] = candidate
	}
	if !*c.AuthorizationMatches {
		authorized, authErr := service.config.Capability.ForCompilation(context.Background(), "project-a", "patients")
		if authErr != nil {
			t.Fatal(authErr)
		}
		authorized.Scope = authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/foreign"}}
		service.config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) { return authorized, nil }
	}
	previewCalls := 0
	service.config.PreviewReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if *c.ConcurrentDraftChange {
			competing := before
			competing.Explorer.Title = "Competing author"
			competingBytes, e := competing.CanonicalJSON()
			if e != nil {
				t.Fatal(e)
			}
			competingDigest, e := competing.Digest()
			if e != nil {
				t.Fatal(e)
			}
			head := originalHead
			head.DraftConfig = competingBytes
			head.DraftDigest = competingDigest
			head.DraftVersion = originalHead.DraftVersion + 1
			head.Title = "Competing author"
			store.created = &head
			expectedHead = head
		}
		if !*c.PreviewCompletes {
			return dataframeexecution.PreviewSummary{}, errors.New("controlled preview failure")
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	request := authoringv2.ApplyCommandsRequest{CommandID: "formal-removal-" + c.ID, SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: originalHead.DraftVersion, ExpectedDraftDigest: originalHead.DraftDigest, Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}}}
	response, applyErr := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	preserved := reflect.DeepEqual(*store.created, expectedHead)
	exact := response != nil && string(store.created.DraftConfig) == string(expectedBytes) && store.created.DraftDigest == expectedDigest && store.created.DraftVersion == originalHead.DraftVersion+1 && response.Workspace.Documents[0].Population.SelectionRevisionID == candidateID && len(store.members[candidateID]) == 1 && store.members[candidateID][0].Ref.ID == "patient-1" && base.selection.ID == "selection-base" && base.selection.MemberCount == 2
	result := formalRemovalResult{ID: c.ID, Accepted: applyErr == nil, PreviewCalls: previewCalls, SaveAttempts: store.attempts, SuccessfulSaves: store.writes, PreservedHead: preserved, ExactCandidate: exact}
	if applyErr != nil {
		result.Error = applyErr.Error()
		var lifecycleErr *ports.Error
		result.Conflict = errors.As(applyErr, &lifecycleErr) && lifecycleErr.Code == "DRAFT_CONFLICT"
	}
	return result
}

func TestPopulationRemovalModelReplay(t *testing.T) {
	const scheduleFile = "testdata/population_removal_cases.json"
	data, err := os.ReadFile(scheduleFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 4<<20 {
		t.Fatalf("schedule file %s exceeds 4 MiB", scheduleFile)
	}
	var request struct {
		SchemaVersion int                 `json:"schemaVersion"`
		Cases         []formalRemovalCase `json:"cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("trailing removal input: %v", err)
	}
	if request.SchemaVersion != 1 || len(request.Cases) == 0 || len(request.Cases) > 200 {
		t.Fatal("invalid frozen removal schedules")
	}
	seen := map[string]bool{}
	for _, c := range request.Cases {
		if c.ID == "" || seen[c.ID] {
			t.Fatalf("duplicate or missing removal ID: %q", c.ID)
		}
		seen[c.ID] = true
		got := formalRemovalObserve(t, c)
		actual := formalRemovalExpectation{Accepted: got.Accepted, PreviewCalls: got.PreviewCalls, SaveAttempts: got.SaveAttempts, SuccessfulSaves: got.SuccessfulSaves, PreservedHead: got.PreservedHead, ExactCandidate: got.ExactCandidate, Conflict: got.Conflict}
		if !reflect.DeepEqual(actual, c.Expected) {
			t.Errorf("schedule %s: got %+v, want frozen behavior %+v", c.ID, actual, c.Expected)
		}
	}
}
