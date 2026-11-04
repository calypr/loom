package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

type formalRemovalCase struct {
	ID                    string `json:"id"`
	ReceiptMatches        *bool  `json:"receiptMatches"`
	ProvenanceMatches     *bool  `json:"provenanceMatches"`
	AuthorizationMatches  *bool  `json:"authorizationMatches"`
	PreviewCompletes      *bool  `json:"previewCompletes"`
	ConcurrentDraftChange *bool  `json:"concurrentDraftChange"`
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
	removed := explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
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
	expectedWorkspace, err := authoringv2.DecodeWorkspace(originalHead.DraftConfig)
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
		receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
		if err != nil {
			t.Fatal(err)
		}
		receipt.ID, err = explorer.ReceiptID(*receipt)
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
		service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) { return authorized, nil }
	}
	previewCalls := 0
	service.config.PreviewReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
	request := authoringv2.ApplyCommandsRequest{CommandID: "formal-removal-" + c.ID, SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: originalHead.DraftVersion, ExpectedDraftDigest: originalHead.DraftDigest, Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", ProposalID: proposal.ProposalID}}}
	response, applyErr := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	preserved := reflect.DeepEqual(*store.created, expectedHead)
	exact := response != nil && string(store.created.DraftConfig) == string(expectedBytes) && store.created.DraftDigest == expectedDigest && store.created.DraftVersion == originalHead.DraftVersion+1 && response.Workspace.Documents[0].Population.SelectionRevisionID == candidateID && len(store.members[candidateID]) == 1 && store.members[candidateID][0].Ref.ID == "patient-1" && base.selection.ID == "selection-base" && base.selection.MemberCount == 2
	result := formalRemovalResult{ID: c.ID, Accepted: applyErr == nil, PreviewCalls: previewCalls, SaveAttempts: store.attempts, SuccessfulSaves: store.writes, PreservedHead: preserved, ExactCandidate: exact}
	if applyErr != nil {
		result.Error = applyErr.Error()
		var lifecycleErr *Error
		result.Conflict = errors.As(applyErr, &lifecycleErr) && lifecycleErr.Code == "DRAFT_CONFLICT"
	}
	return result
}
func TestPopulationRemovalModelReplay(t *testing.T) {
	input, output := os.Getenv("LOOM_FORMAL_REMOVAL_INPUT"), os.Getenv("LOOM_FORMAL_REMOVAL_OUTPUT")
	if input == "" && output == "" {
		yes, no := true, false
		valid := formalRemovalCase{ID: "valid", ReceiptMatches: &yes, ProvenanceMatches: &yes, AuthorizationMatches: &yes, PreviewCompletes: &yes, ConcurrentDraftChange: &no}
		if row := formalRemovalObserve(t, valid); !row.Accepted || !row.ExactCandidate || row.SuccessfulSaves != 1 {
			t.Fatalf("valid removal: %#v", row)
		}
		valid.ID = "conflict"
		valid.ConcurrentDraftChange = &yes
		if row := formalRemovalObserve(t, valid); row.Accepted || !row.Conflict || !row.PreservedHead || row.SuccessfulSaves != 0 {
			t.Fatalf("competing removal: %#v", row)
		}
		return
	}
	if input == "" || output == "" {
		t.Fatal("removal paths must be paired")
	}
	info, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 4*1024*1024 {
		t.Fatal("removal input exceeds bound")
	}
	file, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var request struct {
		SchemaVersion int                 `json:"schemaVersion"`
		Cases         []formalRemovalCase `json:"cases"`
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("trailing removal input: %v", err)
	}
	if request.SchemaVersion != 1 || len(request.Cases) == 0 || len(request.Cases) > 1024 {
		t.Fatal("invalid removal inventory")
	}
	seen := map[string]bool{}
	results := make([]formalRemovalResult, 0, len(request.Cases))
	for _, c := range request.Cases {
		if seen[c.ID] {
			t.Fatal("duplicate removal ID")
		}
		seen[c.ID] = true
		results = append(results, formalRemovalObserve(t, c))
	}
	encoded, err := json.Marshal(struct {
		SchemaVersion int                   `json:"schemaVersion"`
		Results       []formalRemovalResult `json:"results"`
	}{1, results})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, encoded, 0600); err != nil {
		t.Fatal(fmt.Errorf("write removal observations: %w", err))
	}
}
