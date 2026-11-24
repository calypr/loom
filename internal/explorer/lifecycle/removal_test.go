package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	constructionpkg "github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	explorerfixtures "github.com/calypr/loom/internal/testkit/explorerfixtures"
)

func TestProposeConstructionCascadesRemovedRelatedExpandDependencies(t *testing.T) {
	for _, test := range []struct {
		name         string
		removeStepID string
		wantRemoved  []string
		wantKept     []string
	}{
		{
			name:         "first expansion removes dependent chain but keeps independent later filter",
			removeStepID: "expand_observations",
			wantRemoved:  []string{"expand_observations", "expand_conditions", "expand_medications"},
			wantKept:     []string{"keep_root_filter"},
		},
		{
			name:         "middle expansion removes its dependent suffix but keeps independent later filter",
			removeStepID: "expand_conditions",
			wantRemoved:  []string{"expand_conditions", "expand_medications"},
			wantKept:     []string{"expand_observations", "keep_root_filter"},
		},
		{
			name:         "leaf expansion removes only itself",
			removeStepID: "expand_medications",
			wantRemoved:  []string{"expand_medications"},
			wantKept:     []string{"expand_observations", "expand_conditions", "keep_root_filter"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			setup := constructionProposalSetup(t)
			store, snapshot := setup.Store, setup.Snapshot
			candidateCompileCalls := 0
			var candidateBinding *receiptmodel.ConstructionProposalBinding
			setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
				if request.ConstructionProposal != nil {
					candidateCompileCalls++
					candidateBinding = authoringcore.CloneConstructionProposalBinding(request.ConstructionProposal)
				}
				receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
				receipt.ConstructionProposal = authoringcore.CloneConstructionProposalBinding(request.ConstructionProposal)
				receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{
					"patients": relatedExpandRemovalTestStages(t, request.Workspace, "patients"),
				}
				var err error
				receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
				if err != nil {
					return nil, err
				}
				receipt.ID, err = receiptmodel.ReceiptID(*receipt)
				if err != nil {
					return nil, err
				}
				store.receipt = receipt
				return receipt, nil
			}
			workspace, construction := seedRelatedExpandRemovalConstruction(t, store)
			candidate := constructionWithoutStep(construction, test.removeStepID)

			if test.removeStepID == "expand_observations" {
				setup.Config.PreviewReceipt = func(_ context.Context, _ *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
					if err := visit(map[string]any{"patient_id": "p1"}); err != nil {
						return dataframeexecution.PreviewSummary{}, err
					}
					return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
				}
			}
			service := newTestServiceFromSetup(t, setup)
			proposal, err := service.OwnerServices().Construction.ProposeConstruction(context.Background(), constructionpkg.ConstructionProposalRequest{
				Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
				ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
				OutputID: "patients", RemoveStepIDs: []string{test.removeStepID}, CandidateConstruction: candidate,
			})
			if err != nil {
				t.Fatalf("propose removal: %v", err)
			}
			if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" {
				t.Fatalf("removal proposal = status %q, proposal ID %q; want a preview proposal", proposal.PreviewStatus, proposal.ProposalID)
			}
			if candidateCompileCalls != 1 || candidateBinding == nil {
				t.Fatalf("candidate compilation calls = %d, binding = %#v; want one bound candidate", candidateCompileCalls, candidateBinding)
			}
			if len(proposal.DependencyImpact.MissingInputs) != 0 {
				t.Fatalf("removal still requires repair: %#v", proposal.DependencyImpact.MissingInputs)
			}
			if !slices.Equal(proposal.DependencyImpact.RemovedStepIDs, test.wantRemoved) {
				t.Fatalf("removed steps = %#v, want %#v", proposal.DependencyImpact.RemovedStepIDs, test.wantRemoved)
			}
			if !slices.Equal(constructionStepIDs(proposal.CandidateConstruction.Steps), test.wantKept) {
				t.Fatalf("candidate steps = %#v, want %#v", constructionStepIDs(proposal.CandidateConstruction.Steps), test.wantKept)
			}
			if !slices.Equal(candidateBinding.RemoveStepIDs, test.wantRemoved) {
				t.Fatalf("receipt binding removals = %#v, want %#v", candidateBinding.RemoveStepIDs, test.wantRemoved)
			}
			if len(workspace.Documents) != 1 || len(store.created.DraftConfig) == 0 {
				t.Fatal("proposal mutated or lost the seeded workspace")
			}

			if test.removeStepID == "expand_observations" {
				version, digest := store.created.DraftVersion, store.created.DraftDigest
				_, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
					CommandID: "apply-cascading-removal", SemanticsVersion: ws.CurrentSemanticsVersion,
					SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
					Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
				}, "alice")
				if err != nil {
					t.Fatalf("apply cascade-bound proposal: %v", err)
				}
			}
		})
	}
}

func TestProposeConstructionGroupEditCascadesMissingKeyFilterAndApplyBindsRemoval(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	candidateCompileCalls := 0
	var candidateBinding *receiptmodel.ConstructionProposalBinding
	setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		if request.ConstructionProposal != nil {
			candidateCompileCalls++
			candidateBinding = authoringcore.CloneConstructionProposalBinding(request.ConstructionProposal)
		}
		receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
		receipt.ConstructionProposal = authoringcore.CloneConstructionProposalBinding(request.ConstructionProposal)
		receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{
			"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
		}
		var err error
		receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = receiptmodel.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	setup.Config.PreviewReceipt = func(_ context.Context, _ *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if err := visit(map[string]any{"row_count": int64(1)}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"row_count"}, RowCount: 1, Complete: true}, nil
	}
	workspace, construction := seedGroupKeyEditConstruction(t, store)
	candidate := construction
	candidate.Steps = append([]ws.ConstructionStep(nil), construction.Steps...)
	candidate.Steps[0].Operation.Group.Keys = nil
	candidate.Steps[0].Outputs = []ws.StageColumn{{ID: "row_count", Name: "row_count", Label: "Rows", Type: "integer"}}
	service := newTestServiceFromSetup(t, setup)
	proposal, err := service.OwnerServices().Construction.ProposeConstruction(context.Background(), constructionpkg.ConstructionProposalRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", ChangedStepID: "group-records", CandidateConstruction: candidate,
	})
	if err != nil {
		t.Fatalf("propose GROUP edit with dependent filter: %v", err)
	}
	wantSteps := []string{"group-records", "independent-count-filter"}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || !slices.Equal(proposal.DependencyImpact.RemovedStepIDs, []string{"dependent-id-filter"}) ||
		!slices.Equal(constructionStepIDs(proposal.CandidateConstruction.Steps), wantSteps) {
		t.Fatalf("GROUP edit proposal did not cascade only the missing-key filter: status=%s impact=%#v steps=%#v", proposal.PreviewStatus, proposal.DependencyImpact, constructionStepIDs(proposal.CandidateConstruction.Steps))
	}
	if candidateCompileCalls != 1 || candidateBinding == nil || !slices.Equal(candidateBinding.RemoveStepIDs, []string{"dependent-id-filter"}) {
		t.Fatalf("candidate binding = %#v, compile calls=%d; want cascade removal bound for Apply", candidateBinding, candidateCompileCalls)
	}
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "apply-group-cascade", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
	}, "alice")
	if err != nil {
		t.Fatalf("apply GROUP edit cascade: %v", err)
	}
	if response.Workspace.Documents[0].Construction == nil || !slices.Equal(constructionStepIDs(response.Workspace.Documents[0].Construction.Steps), wantSteps) {
		t.Fatalf("applied construction = %#v, want GROUP and independent count filter", response.Workspace.Documents[0].Construction)
	}
	if len(workspace.Documents) != 1 {
		t.Fatal("GROUP edit proposal changed the saved workspace before Apply")
	}
}

func seedGroupKeyEditConstruction(t *testing.T, store *fakeStore) (ws.Workspace, ws.Construction) {
	t.Helper()
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Columns) == 0 {
		t.Fatal("group-key test document has no source columns")
	}
	inputID := document.Columns[0].ColumnID
	groupOutputs := []ws.StageColumn{
		{ID: "group_id", Name: "group_id", Label: "Group ID", Type: "string"},
		{ID: "row_count", Name: "row_count", Label: "Rows", Type: "integer"},
	}
	construction := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
		{
			ID: "group-records", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
				ConstructionID: "group-records", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
				Keys:       []ws.ConstructionGroupKey{{InputColumnID: inputID, OutputColumnID: "group_id"}},
				Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
			}},
			Outputs: groupOutputs,
		},
		{
			ID: "dependent-id-filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "group-records"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
				ColumnID: "group_id", Operator: ws.ConstructionFilterExists,
			}},
			Outputs: append([]ws.StageColumn(nil), groupOutputs...),
		},
		{
			ID: "independent-count-filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "dependent-id-filter"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
				ColumnID: "row_count", Operator: ws.ConstructionFilterExists,
			}},
			Outputs: append([]ws.StageColumn(nil), groupOutputs...),
		},
	}}
	document.Construction = &construction
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate GROUP key dependency fixture: %v", err)
	}
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig = encoded
	store.created.DraftDigest = digest
	return workspace, construction
}

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
	_, config, base, snapshot, before := populationMemberProposalSetup(t)
	store := &formalRemovalStore{populationProposalTestStore: base}
	domain, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	authorize := config.Capability.ForCompilation
	config.Capability.ForCompilation = func(ctx context.Context, project, token string) (ports.AuthorizedCapability, error) {
		return authorize(ctx, project, token)
	}
	preview := config.PreviewReceipt
	config.PreviewReceipt = func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		return preview(ctx, receipt, bindings, visit)
	}
	service := newPopulationMemberProposalService(t, domain, config)
	removed := explorerselection.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
	proposal, err := service.OwnerServices().Population.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(base, snapshot, removed))
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
		authorized, authErr := authorize(context.Background(), "project-a", "patients")
		if authErr != nil {
			t.Fatal(authErr)
		}
		authorized.Scope = authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/foreign"}}
		authorize = func(context.Context, string, string) (ports.AuthorizedCapability, error) { return authorized, nil }
	}
	previewCalls := 0
	preview = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
