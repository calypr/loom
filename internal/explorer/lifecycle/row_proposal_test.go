package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type rowProposalChoiceResolver struct{}

type rowProposalChoicePlanner struct{}

func (rowProposalChoicePlanner) ListRowChoices(_ context.Context, _ capability.Snapshot, _ authoringv2.Document) ([]capability.RowChoice, error) {
	return []capability.RowChoice{
		{
			ChoiceID: "field-choice", Kind: capability.RowChoiceFieldGroupKey, Label: "Patient.id", Path: "id",
			Description: "Patient identifier", ValueType: "STRING", OccurrenceID: authoringv2.RootOccurrenceID,
		},
		{
			ChoiceID: "expanded-choice", Kind: capability.RowChoiceExpandedScope, Label: "Patient.name[]",
			Path:        "component[].code.coding[]",
			Description: "Patient name occurrences", ValueType: "ARRAY", OccurrenceID: authoringv2.RootOccurrenceID,
		},
	}, nil
}

func (rowProposalChoiceResolver) ResolveRowChoiceID(_ context.Context, request RowChoiceResolveRequest) (ResolvedRowChoice, error) {
	if request.Route.OccurrenceID != authoringv2.RootOccurrenceID {
		return ResolvedRowChoice{}, context.Canceled
	}
	switch request.ExpectedKind {
	case RowChoiceFieldGroup:
		return ResolvedRowChoice{Kind: RowChoiceFieldGroup, OccurrenceID: authoringv2.RootOccurrenceID, FieldPath: "id"}, nil
	case RowChoiceExpanded:
		return ResolvedRowChoice{Kind: RowChoiceExpanded, OccurrenceID: authoringv2.RootOccurrenceID, ScopePath: "component[]"}, nil
	default:
		return ResolvedRowChoice{}, context.Canceled
	}
}

type rowProposalExplicitGroupResolver struct {
	proof       ExplicitGroupRevisionProof
	listReqs    []ExplicitGroupRevisionListRequest
	resolveReqs []ExplicitGroupRevisionResolveRequest
	receipts    []*explorer.CompilationReceipt
}

func (r *rowProposalExplicitGroupResolver) ListExplicitGroupRevisions(_ context.Context, request ExplicitGroupRevisionListRequest) ([]ExplicitGroupRevisionChoice, error) {
	r.listReqs = append(r.listReqs, request)
	return []ExplicitGroupRevisionChoice{{
		RevisionID: "group-revision-1", SourceSelectionRevisionID: "selection-1",
		GroupCount: 2, MemberCount: 4, UnassignedMemberPolicies: []authoringv2.UnassignedMemberPolicy{
			authoringv2.UnassignedMemberError, authoringv2.UnassignedMemberExclude, authoringv2.UnassignedMemberGroupAsUnassigned,
		},
	}}, nil
}

func (r *rowProposalExplicitGroupResolver) ResolveExplicitGroupRevision(_ context.Context, request ExplicitGroupRevisionResolveRequest) (ExplicitGroupRevisionProof, error) {
	r.resolveReqs = append(r.resolveReqs, request)
	proof := r.proof
	if proof.RevisionID == "" {
		proof = ExplicitGroupRevisionProof{
			RevisionID: request.RevisionID, Project: request.Project,
			SourceGeneration:         request.Snapshot.Identity.Generation,
			AuthorizationScopeDigest: request.Snapshot.Identity.AuthorizationScopeDigest,
			RootResourceType:         request.RootResourceType, SelectionRevisionID: "selection-1",
			SelectionMembershipDigest: "sha256:selection-members", DefinitionDigest: "sha256:definition",
			MembershipDigest: "sha256:membership", Complete: true,
		}
	}
	return proof, nil
}

func (r *rowProposalExplicitGroupResolver) ValidateCompilationReceipt(_ context.Context, proof ExplicitGroupRevisionProof, receipt *explorer.CompilationReceipt) error {
	r.receipts = append(r.receipts, receipt)
	if proof.RevisionID == "" || receipt == nil {
		return fmt.Errorf("explicit group receipt binding is missing")
	}
	workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		return err
	}
	document := proposalDocument(workspace, "patients")
	if document == nil || document.Rows.Kind != authoringv2.RowDefinitionGroups || document.Rows.Groups == nil ||
		document.Rows.Groups.Source.Kind != authoringv2.GroupSourceExplicit || document.Rows.Groups.Source.Explicit == nil ||
		document.Rows.Groups.Source.Explicit.RevisionID != proof.RevisionID {
		return fmt.Errorf("compiled workspace does not contain the resolved explicit group revision")
	}
	return nil
}

func TestProposeExplicitGroupCompilesPinnedRevisionAndReceiptProof(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	resolver := &rowProposalExplicitGroupResolver{}
	service.config.ExplicitGroupResolver = resolver
	previews := 0
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previews++
		if receipt == nil || !bindings.IncludeRowIdentity {
			t.Fatal("explicit-group preview did not execute a receipt with stable row identities")
		}
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		document := proposalDocument(workspace, "patients")
		if document == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("preview receipt has no patients output")
		}
		rowID := "patient:1"
		if document.Rows.Kind == authoringv2.RowDefinitionGroups && document.Rows.Groups != nil && document.Rows.Groups.Source.Explicit != nil {
			rowID = string(document.Rows.Groups.Source.Explicit.RevisionID) + ":group-a"
		}
		if err := visit(map[string]any{"__loom_row_id": rowID, "patient_id": "patient-1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	request := rowProposalRequest(store.created, snapshot)
	request.Selection = RowDefinitionSelection{Kind: RowDefinitionSelectionExplicitGroup, ExplicitGroup: &ExplicitGroupSelection{
		RevisionID: "group-revision-1", UnassignedMemberPolicy: authoringv2.UnassignedMemberError,
	}}
	proposal, err := service.ProposeRowDefinition(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolver.resolveReqs) != 1 || len(resolver.receipts) != 1 {
		t.Fatalf("resolver calls: resolve=%d receipt=%d", len(resolver.resolveReqs), len(resolver.receipts))
	}
	resolved := resolver.resolveReqs[0]
	if resolved.Project != request.Project || resolved.ExplorerID != request.ExplorerID || resolved.OutputID != request.OutputID || resolved.Snapshot.Token != snapshot.Token || resolved.RevisionID != "group-revision-1" || resolved.RootResourceType != "Patient" {
		t.Fatalf("explicit group resolver received unbound request: %#v", resolved)
	}
	if proposal.ProposalID == "" || proposal.Mode != RowDefinitionSelectionExplicitGroup || proposal.Comparison.Status != RowDefinitionComparisonAvailable || proposal.Comparison.Base == nil || proposal.Comparison.Candidate == nil || proposal.Comparison.Base.RowCount != 1 || proposal.Comparison.Candidate.RowCount != 1 || len(proposal.Comparison.Examples) != 2 || previews != 2 {
		t.Fatalf("explicit group proposal did not compile its pinned revision: %#v", proposal)
	}
	if !containsRowDefinitionExample(proposal.Comparison.Examples, "patient:1", true, false) || !containsRowDefinitionExample(proposal.Comparison.Examples, "group-revision-1:group-a", false, true) {
		t.Fatalf("explicit group preview did not compare the exact pinned revision's row identity: %#v", proposal.Comparison.Examples)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("proposal persisted a draft %d times", store.saveDraftCalls)
	}
}

func containsRowDefinitionExample(examples []RowDefinitionComparisonExample, rowIdentity string, base, candidate bool) bool {
	for _, example := range examples {
		if example.RowIdentity == rowIdentity && example.BasePresent == base && example.CandidatePresent == candidate {
			return true
		}
	}
	return false
}

func TestListRowDefinitionChoicesReturnsServerExplicitGroupRevisions(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	resolver := &rowProposalExplicitGroupResolver{}
	service.config.RowChoicePlanner = rowProposalChoicePlanner{}
	service.config.ExplicitGroupResolver = resolver

	response, err := service.ListRowDefinitionChoices(context.Background(), RowDefinitionChoicesRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID,
		SnapshotToken: snapshot.Token, OutputID: "patients",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Kind != RowChoiceExpanded || response.Choices[0].FieldPath != "component[].code.coding[]" || len(response.ExplicitGroups) != 1 || len(resolver.listReqs) != 1 {
		t.Fatalf("row-definition choices omitted server-authorized options: %#v", response)
	}
	listRequest := resolver.listReqs[0]
	if listRequest.Project != store.created.Project || listRequest.Snapshot.Token != snapshot.Token || listRequest.RootResourceType != "Patient" || listRequest.PinnedRevisionID != "" {
		t.Fatalf("explicit group listing was not server-filtered to the authorized table root: %#v", listRequest)
	}
	group := response.ExplicitGroups[0]
	if group.RevisionID != "group-revision-1" || group.SourceSelectionRevisionID != "selection-1" ||
		group.GroupCount != 2 || group.MemberCount != 4 || len(group.UnassignedMemberPolicies) != 3 {
		t.Fatalf("explicit group choice was not returned as a typed server option: %#v", group)
	}
}

func TestListRowDefinitionChoicesReturnsExplicitGroupsForAuthoredConstruction(t *testing.T) {
	service, store, snapshot, workspace := rowProposalService(t)
	addRowProposalSourceFilter(t, store, workspace)
	resolver := &rowProposalExplicitGroupResolver{}
	service.config.RowChoicePlanner = rowProposalChoicePlanner{}
	service.config.ExplicitGroupResolver = resolver

	response, err := service.ListRowDefinitionChoices(context.Background(), RowDefinitionChoicesRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID,
		SnapshotToken: snapshot.Token, OutputID: "patients",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ExplicitGroups) != 1 || len(resolver.listReqs) != 1 {
		t.Fatalf("authored construction did not advertise authorized explicit groups: choices=%#v resolver calls=%d", response.ExplicitGroups, len(resolver.listReqs))
	}
	if len(response.Choices) != 1 || response.Choices[0].Kind != RowChoiceExpanded {
		t.Fatalf("authored construction lost executable source expansion choices: %#v", response.Choices)
	}
}

func TestProposeExplicitGroupPreservesAuthoredConstruction(t *testing.T) {
	service, store, snapshot, workspace := rowProposalService(t)
	addRowProposalSourceFilter(t, store, workspace)
	resolver := &rowProposalExplicitGroupResolver{}
	service.config.ExplicitGroupResolver = resolver
	previews := 0
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previews++
		if receipt == nil || !bindings.IncludeRowIdentity {
			t.Fatal("explicit-group preview did not execute a receipt with stable row identities")
		}
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		document := proposalDocument(workspace, "patients")
		if document == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("preview receipt has no patients output")
		}
		if err := visit(map[string]any{"__loom_row_id": "patient:1", "patient_id": "patient-1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}

	request := rowProposalRequest(store.created, snapshot)
	request.Selection = RowDefinitionSelection{Kind: RowDefinitionSelectionExplicitGroup, ExplicitGroup: &ExplicitGroupSelection{
		RevisionID: "group-revision-1", UnassignedMemberPolicy: authoringv2.UnassignedMemberError,
	}}
	proposal, err := service.ProposeRowDefinition(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Comparison.Status != RowDefinitionComparisonAvailable || previews != 2 || len(resolver.resolveReqs) != 1 || len(resolver.receipts) != 1 {
		t.Fatalf("cohort proposal did not compile and preview both workspaces: proposal=%#v previews=%d resolves=%d receipts=%d", proposal, previews, len(resolver.resolveReqs), len(resolver.receipts))
	}
	candidate, err := authoringv2.DecodeWorkspace(resolver.receipts[0].NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	document := proposalDocument(candidate, "patients")
	if document == nil || document.Construction == nil || len(document.Construction.Steps) != 1 ||
		document.Construction.Steps[0].ID != "filter_source" || document.Rows.Groups == nil || document.Rows.Groups.AfterStepID != "filter_source" {
		t.Fatalf("cohort proposal lost the authored source filter or persisted insertion anchor: %#v", document)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("proposal persisted a draft %d times", store.saveDraftCalls)
	}
}

func TestExplicitGroupPolicyEditPreservesAnchorAndMemberValues(t *testing.T) {
	service, store, snapshot, workspace := rowProposalService(t)
	addRowProposalSourceFilter(t, store, workspace)
	resolver := &rowProposalExplicitGroupResolver{}
	service.config.ExplicitGroupResolver = resolver
	document := proposalDocument(workspace, "patients")
	if document == nil || len(document.Columns) == 0 {
		t.Fatal("row proposal fixture has no source columns")
	}
	document.Rows = authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionGroups, Groups: &authoringv2.GroupedRows{
		Source: authoringv2.GroupSource{Kind: authoringv2.GroupSourceExplicit, Explicit: &authoringv2.ExplicitGroupSource{
			RevisionID: "group-revision-old", UnassignedMemberPolicy: authoringv2.UnassignedMemberGroupAsUnassigned,
		}},
		AfterStepID: "filter_source",
		RowValues:   []authoringv2.ExplicitGroupRowValue{{ColumnID: document.Columns[0].ColumnID, Policy: authoringv2.ConstructionRowValueAll}},
	}}
	request := rowProposalRequest(store.created, snapshot)
	request.Selection = RowDefinitionSelection{Kind: RowDefinitionSelectionExplicitGroup, ExplicitGroup: &ExplicitGroupSelection{
		RevisionID: "group-revision-1", UnassignedMemberPolicy: authoringv2.UnassignedMemberExclude,
	}}
	rows, _, err := service.resolveRowDefinitionSelection(context.Background(), request, snapshot, *document)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Groups == nil || rows.Groups.Source.Explicit == nil || rows.Groups.Source.Explicit.RevisionID != "group-revision-1" ||
		rows.Groups.AfterStepID != "filter_source" || len(rows.Groups.RowValues) != 1 ||
		rows.Groups.RowValues[0].ColumnID != document.Columns[0].ColumnID || rows.Groups.RowValues[0].Policy != authoringv2.ConstructionRowValueAll {
		t.Fatalf("policy edit did not change only the pinned source intent: %#v", rows)
	}
	document.Rows = rows
	if err := document.Validate(); err != nil {
		t.Fatalf("policy-edited cohort document is invalid: %v", err)
	}
}

func addRowProposalSourceFilter(t *testing.T, store *fakeStore, workspace authoringv2.Workspace) {
	t.Helper()
	document := workspace.Documents[0]
	if len(document.Columns) == 0 {
		t.Fatal("row proposal fixture has no source columns")
	}
	outputs := make([]authoringv2.StageColumn, 0, len(document.Columns))
	for index := range document.Columns {
		column := &document.Columns[index]
		if column.ColumnID == "" {
			column.ColumnID = fmt.Sprintf("row_proposal_source_%d", index)
		}
		outputs = append(outputs, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "filter_source", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
			ColumnID: document.Columns[0].ColumnID, Operator: authoringv2.ConstructionFilterExists,
		}},
		Outputs: outputs,
	}}}
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate authored construction fixture: %v", err)
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig = canonical
	store.created.DraftDigest = digest
}

func TestProposeFieldGroupRejectsUnsupportedSelection(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	request := rowProposalRequest(store.created, snapshot)
	request.Selection = RowDefinitionSelection{Kind: RowDefinitionSelectionFieldGroup, FieldGroup: &FieldGroupSelection{
		RowChoiceID: "opaque-choice-1", MissingKeyPolicy: authoringv2.MissingKeyError,
	}}
	_, err := service.ProposeRowDefinition(context.Background(), request)
	if lifecycleErrorCode(err) != "FIELD_GROUP_UNSUPPORTED" {
		t.Fatalf("field-group proposal error = %v, want FIELD_GROUP_UNSUPPORTED", err)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("field-group proposal mutated the draft %d times", store.saveDraftCalls)
	}
}

func TestProposeExplicitGroupRejectsStaleRevisionProof(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	service.config.ExplicitGroupResolver = &rowProposalExplicitGroupResolver{proof: ExplicitGroupRevisionProof{
		RevisionID: "group-revision-1", Project: "project-a", SourceGeneration: "generation-stale",
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RootResourceType: "Patient",
		SelectionRevisionID: "selection-1", SelectionMembershipDigest: "sha256:selection-members",
		DefinitionDigest: "sha256:definition", MembershipDigest: "sha256:membership", Complete: true,
	}}
	request := rowProposalRequest(store.created, snapshot)
	request.Selection = RowDefinitionSelection{Kind: RowDefinitionSelectionExplicitGroup, ExplicitGroup: &ExplicitGroupSelection{
		RevisionID: "group-revision-1", UnassignedMemberPolicy: authoringv2.UnassignedMemberError,
	}}
	if _, err := service.ProposeRowDefinition(context.Background(), request); err == nil {
		t.Fatal("accepted explicit group revision from a stale source generation")
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("stale explicit group rejection persisted a draft %d times", store.saveDraftCalls)
	}
}

func TestProposeRowDefinitionBindsCandidateReceiptWithoutSavingDraft(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	enableRowDefinitionPreview(service)
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	compileBindings := make([]*explorer.RowDefinitionProposalBinding, 0, 2)
	compile := service.config.CompileReceipt
	service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		compileBindings = append(compileBindings, cloneRowDefinitionProposalBinding(request.RowDefinitionProposal))
		return compile(ctx, request)
	}
	proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == "" || proposal.CandidateWorkspaceDigest == "" || proposal.Mode != RowDefinitionSelectionExpanded {
		t.Fatalf("proposal identity = %#v", proposal)
	}
	if proposal.Comparison.Status != RowDefinitionComparisonAvailable {
		t.Fatalf("comparison status = %#v", proposal.Comparison)
	}
	if len(compileBindings) != 2 || compileBindings[0] != nil || compileBindings[1] == nil {
		t.Fatalf("compile proposal bindings = %#v, want base unbound and candidate bound", compileBindings)
	}
	binding := store.receipt.RowDefinitionProposal
	if binding == nil || *binding != *compileBindings[1] || binding.DraftVersion != beforeVersion || binding.DraftDigest != beforeDigest || binding.OutputID != "patients" || binding.SnapshotToken != snapshot.Token || binding.CandidateWorkspaceDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("candidate receipt binding = %#v, proposal = %#v", binding, proposal)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig) {
		t.Fatalf("proposal mutated the saved draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestProposeRowDefinitionRequiresPreviewConfiguration(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Service)
	}{
		{name: "preview executor is absent"},
		{name: "execution authorization is absent", configure: func(service *Service) {
			enableRowDefinitionPreview(service)
			service.config.Capability.ForExecution = nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot, _ := rowProposalService(t)
			if test.configure != nil {
				test.configure(service)
			}
			compileCalls := 0
			compile := service.config.CompileReceipt
			service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
				compileCalls++
				return compile(ctx, request)
			}
			before := append([]byte(nil), store.created.DraftConfig...)
			version, digest := store.created.DraftVersion, store.created.DraftDigest
			_, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
			if lifecycleErrorCode(err) != "PREVIEW_UNAVAILABLE" {
				t.Fatalf("missing preview configuration error = %v, want PREVIEW_UNAVAILABLE", err)
			}
			if compileCalls != 0 || store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
				t.Fatalf("missing preview configuration compiled or mutated state: compiles=%d saves=%d owner=%#v", compileCalls, store.saveDraftCalls, store.created)
			}
		})
	}
}

func TestCompareRowDefinitionReceiptsRejectsMissingOutputContract(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	enableRowDefinitionPreview(service)
	request := rowProposalRequest(store.created, snapshot)
	request.OutputID = "missing-output"
	base, candidate := nativeReceipt(snapshot), nativeReceipt(snapshot)
	_, err := service.compareRowDefinitionReceipts(context.Background(), request, snapshot, base, candidate, 25)
	var lifecycleErr *Error
	if lifecycleErrorCode(err) != "PREVIEW_OUTPUT_INVALID" || !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassInternal || lifecycleErr.Cause == nil {
		t.Fatalf("missing output contract error = %v, want caused internal receipt failure", err)
	}
}

func TestProposeRowDefinitionPreviewFailuresPropagate(t *testing.T) {
	tests := []struct {
		name         string
		failAt       string
		identityFail bool
		duplicateID  bool
		wantCode     string
		wantCause    bool
	}{
		{name: "base executor failure", failAt: "base", wantCode: "BASE_PREVIEW_FAILED", wantCause: true},
		{name: "candidate executor failure", failAt: "candidate", wantCode: "CANDIDATE_PREVIEW_FAILED", wantCause: true},
		{name: "candidate identity failure", failAt: "candidate", identityFail: true, wantCode: "CANDIDATE_PREVIEW_FAILED"},
		{name: "candidate duplicate identity", failAt: "candidate", duplicateID: true, wantCode: "CANDIDATE_PREVIEW_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot, _ := rowProposalService(t)
			executorFailure := errors.New("preview backend failed")
			service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
				workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
				if err != nil {
					return dataframeexecution.PreviewSummary{}, err
				}
				document := proposalDocument(workspace, "patients")
				if document == nil {
					return dataframeexecution.PreviewSummary{}, fmt.Errorf("row-definition preview receipt has no patients output")
				}
				stage := "base"
				if document.Rows.Kind == authoringv2.RowDefinitionExpanded {
					stage = "candidate"
				}
				summary := dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}
				if stage == test.failAt {
					if test.identityFail {
						return summary, visit(map[string]any{"patient_id": "patient-1"})
					}
					if test.duplicateID {
						row := map[string]any{"__loom_row_id": "candidate:patient-1", "patient_id": "patient-1"}
						if err := visit(row); err != nil {
							return summary, err
						}
						return summary, visit(row)
					}
					return summary, fmt.Errorf("preview execution failed: %w", executorFailure)
				}
				if err := visit(map[string]any{"__loom_row_id": stage + ":patient-1", "patient_id": "patient-1"}); err != nil {
					return summary, err
				}
				return summary, nil
			}
			before := append([]byte(nil), store.created.DraftConfig...)
			version, digest := store.created.DraftVersion, store.created.DraftDigest
			proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
			if lifecycleErrorCode(err) != test.wantCode {
				t.Fatalf("preview failure error = %v, want %s", err, test.wantCode)
			}
			if proposal.ProposalID != "" {
				t.Fatalf("preview failure issued proposal %q", proposal.ProposalID)
			}
			var lifecycleErr *Error
			if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassInternal || lifecycleErr.Cause == nil {
				t.Fatalf("preview failure did not preserve its cause: %#v", err)
			}
			if test.wantCause && !errors.Is(err, executorFailure) {
				t.Fatalf("preview failure lost executor cause: %v", err)
			}
			if test.identityFail && !strings.Contains(lifecycleErr.Cause.Error(), "stable identity") {
				t.Fatalf("preview identity cause = %v", lifecycleErr.Cause)
			}
			if test.duplicateID && !strings.Contains(lifecycleErr.Cause.Error(), "duplicate stable row identity") {
				t.Fatalf("preview duplicate-identity cause = %v", lifecycleErr.Cause)
			}
			if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
				t.Fatalf("preview failure mutated the draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
			}
		})
	}
}

func TestProposeExpandedEmptyCollectionErrorReturnsValidationError(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	driverDiagnostic := errors.New("AQL: CONSTRUCTION_EXPANSION_EMPTY: construction expand_tags has no items for row patient-1")
	emptyCollection := dataframeerrors.Wrap(driverDiagnostic, dataframeerrors.CodeConstructionExpansionEmpty, "")
	backendWrapped := dataframeerrors.Wrap(emptyCollection, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		document := proposalDocument(workspace, "patients")
		if document == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("row-definition preview receipt has no patients output")
		}
		if document.Rows.Kind == authoringv2.RowDefinitionExpanded {
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, Complete: true}, backendWrapped
		}
		if !bindings.IncludeRowIdentity {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("row-definition preview requires stable row identities")
		}
		if err := visit(map[string]any{"__loom_row_id": "records:patient-1", "patient_id": "patient-1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	request := rowProposalRequest(store.created, snapshot)
	request.Selection.Expanded.EmptyCollectionPolicy = authoringv2.EmptyCollectionError
	_, err := service.ProposeRowDefinition(context.Background(), request)
	var lifecycleErr *Error
	if lifecycleErrorCode(err) != "EMPTY_COLLECTION_ERROR" || !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassUnprocessable || !errors.Is(err, emptyCollection) || !errors.Is(err, driverDiagnostic) {
		t.Fatalf("ERROR expansion failure = %v, want a caused EMPTY_COLLECTION_ERROR validation failure", err)
	}
	if lifecycleErr.Message != "Some records have no values for this field. Choose \"Leave out records with no values\" or \"Keep records with no values as one empty row\", or choose another field." {
		t.Fatalf("ERROR expansion repair message = %q", lifecycleErr.Message)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("ERROR expansion failure mutated draft: saves=%d", store.saveDraftCalls)
	}
}

func TestProposeExpandedGenericBackendUnavailableIsNotValidationError(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	backendFailure := dataframeerrors.Wrap(errors.New("query transport reset"), dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		document := proposalDocument(workspace, "patients")
		if document == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("row-definition preview receipt has no patients output")
		}
		if document.Rows.Kind == authoringv2.RowDefinitionExpanded {
			return dataframeexecution.PreviewSummary{}, backendFailure
		}
		if err := visit(map[string]any{"__loom_row_id": "records:patient-1", "patient_id": "patient-1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	request := rowProposalRequest(store.created, snapshot)
	request.Selection.Expanded.EmptyCollectionPolicy = authoringv2.EmptyCollectionError
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	proposal, err := service.ProposeRowDefinition(context.Background(), request)
	var lifecycleErr *Error
	if proposal.ProposalID != "" || lifecycleErrorCode(err) != "CANDIDATE_PREVIEW_FAILED" || !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassInternal || !errors.Is(err, backendFailure) {
		t.Fatalf("generic backend error = proposal %#v, error %v; want caused internal preview failure", proposal, err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("generic backend preview error mutated the draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestProposeRowDefinitionCandidateFeatureErrorReturnsValidationError(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	featureFailure := dataframeerrors.NewError(dataframeerrors.CodeConstructionRowValueMultipleValues, "")
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		document := proposalDocument(workspace, "patients")
		if document == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("row-definition preview receipt has no patients output")
		}
		if document.Rows.Kind == authoringv2.RowDefinitionExpanded {
			return dataframeexecution.PreviewSummary{}, featureFailure
		}
		if err := visit(map[string]any{"__loom_row_id": "records:patient-1", "patient_id": "patient-1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
	var lifecycleErr *Error
	if proposal.ProposalID != "" || lifecycleErrorCode(err) != string(dataframeerrors.CodeConstructionRowValueMultipleValues) || !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassUnprocessable || lifecycleErr.Message != dataframeerrors.PublicMessage(featureFailure) || !errors.Is(err, featureFailure) {
		t.Fatalf("candidate feature error = proposal %#v, error %v; want caused unprocessable feature error", proposal, err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("candidate feature error mutated the draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestProposeExplicitGroupUnassignedErrorReturnsValidationError(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	service.config.ExplicitGroupResolver = &rowProposalExplicitGroupResolver{}
	driverDiagnostic := errors.New("AQL: EXPLICIT_GROUP_UNASSIGNED_MEMBER (while executing)")
	unassigned := dataframeerrors.Wrap(driverDiagnostic, dataframeerrors.CodeExplicitGroupUnassignedMember, "")
	backendWrapped := dataframeerrors.Wrap(unassigned, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		document := proposalDocument(workspace, "patients")
		if document == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("row-definition preview receipt has no patients output")
		}
		summary := dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}
		if document.Rows.Kind == authoringv2.RowDefinitionGroups && document.Rows.Groups != nil && document.Rows.Groups.Source.Kind == authoringv2.GroupSourceExplicit {
			return summary, backendWrapped
		}
		if err := visit(map[string]any{"__loom_row_id": "records:patient-1", "patient_id": "patient-1"}); err != nil {
			return summary, err
		}
		return summary, nil
	}
	request := rowProposalRequest(store.created, snapshot)
	request.Selection = RowDefinitionSelection{Kind: RowDefinitionSelectionExplicitGroup, ExplicitGroup: &ExplicitGroupSelection{
		RevisionID: "group-revision-1", UnassignedMemberPolicy: authoringv2.UnassignedMemberError,
	}}
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	_, err := service.ProposeRowDefinition(context.Background(), request)
	var lifecycleErr *Error
	wantMessage := "Some records do not belong to a group. Choose \"Leave out records without a group\" or \"Put records without a group in their own group\", or assign them to a group."
	if lifecycleErrorCode(err) != "EXPLICIT_GROUP_UNASSIGNED_MEMBER" || !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassUnprocessable || !errors.Is(err, unassigned) || !errors.Is(err, driverDiagnostic) || lifecycleErr.Message != wantMessage {
		t.Fatalf("unassigned explicit-group preview error = %v, want caused actionable validation failure", err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("unassigned preview error mutated the draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestProposeRowDefinitionAllowsMetadataNormalization(t *testing.T) {
	service, store, snapshot, workspace := rowProposalService(t)
	enableRowDefinitionPreview(service)
	workspace.SemanticsVersion = authoringv2.CurrentSemanticsVersion - 2
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig = draft
	store.created.DraftDigest = digest

	proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
	if err != nil {
		t.Fatalf("row-definition proposal rejected the command's metadata normalization: %v", err)
	}
	if proposal.CandidateWorkspaceDigest == "" {
		t.Fatal("row-definition proposal omitted the normalized candidate workspace digest")
	}
	candidate, err := authoringv2.DecodeWorkspace(store.receipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.SemanticsVersion != authoringv2.CurrentSemanticsVersion || candidate.Documents[0].Rows.Kind != authoringv2.RowDefinitionExpanded {
		t.Fatalf("candidate did not preserve metadata normalization and the requested rows: %#v", candidate)
	}
	request := rowProposalApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-normalized-row-definition")
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err != nil {
		t.Fatalf("apply rejected a row-definition proposal after metadata normalization: %v", err)
	}
}

func TestApplyRowDefinitionProposalAllowsCanonicalSourceMetadata(t *testing.T) {
	service, store, snapshot, workspace := rowProposalService(t)
	enableRowDefinitionPreview(service)
	workspace.Documents[0].Columns[0].Source.Field.ProjectionMode = ""
	draft, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig = draft
	store.created.DraftDigest = digest

	proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
	if err != nil {
		t.Fatalf("row-definition proposal rejected canonical source metadata: %v", err)
	}
	candidate, err := authoringv2.DecodeWorkspace(store.receipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	if got := candidate.Documents[0].Columns[0].Source.Field.ProjectionMode; got != "FIRST" {
		t.Fatalf("candidate receipt projection mode = %q, want canonical FIRST", got)
	}
	request := rowProposalApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-canonical-row-definition")
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err != nil {
		t.Fatalf("apply rejected canonical source metadata in the candidate receipt: %v", err)
	}
}

func TestApplyRowDefinitionProposalSavesOnceAndReplaysIdempotently(t *testing.T) {
	service, store, snapshot, workspace := rowProposalService(t)
	enableRowDefinitionPreview(service)
	proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	baseVersion := store.created.DraftVersion
	request := rowProposalApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-row-definition")
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || store.saveDraftCalls != 1 || store.created.DraftVersion != baseVersion+1 || store.created.DraftDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("apply response=%#v saves=%d owner=%#v", response, store.saveDraftCalls, store.created)
	}
	if got := store.created.DraftConfig; string(got) == string(mustCanonical(t, workspace)) {
		t.Fatal("apply did not change the saved row definition")
	}
	replayed, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if replayed == nil || replayed.DraftDigest != proposal.CandidateWorkspaceDigest || store.saveDraftCalls != 1 {
		t.Fatalf("replay=%#v saves=%d, want same committed result and no second write", replayed, store.saveDraftCalls)
	}
}

func TestApplyRowDefinitionProposalRejectsTamperingAndNewCASReplay(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeStore, *authoringv2.Workspace, *authoringv2.ApplyCommandsRequest) error
	}{
		{
			name: "tampered receipt binding",
			mutate: func(store *fakeStore, _ *authoringv2.Workspace, _ *authoringv2.ApplyCommandsRequest) error {
				store.receipt.RowDefinitionProposal.BaseDocumentDigest = "sha256:tampered"
				return nil
			},
		},
		{
			name: "replay with new CAS after row-only write",
			mutate: func(store *fakeStore, workspace *authoringv2.Workspace, request *authoringv2.ApplyCommandsRequest) error {
				workspace.Documents[0].Rows = authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionExpanded, Expanded: &authoringv2.ExpandedRows{
					OccurrenceID: authoringv2.RootOccurrenceID, ScopePath: "component", EmptyCollectionPolicy: authoringv2.EmptyCollectionPreserveParent,
				}}
				canonical, err := workspace.CanonicalJSON()
				if err != nil {
					return err
				}
				digest, err := workspace.Digest()
				if err != nil {
					return err
				}
				store.created.DraftConfig = canonical
				store.created.DraftDigest = digest
				store.created.DraftVersion++
				request.ExpectedDraftVersion = store.created.DraftVersion
				request.ExpectedDraftDigest = store.created.DraftDigest
				request.CommandID = "apply-after-row-only-write"
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot, workspace := rowProposalService(t)
			enableRowDefinitionPreview(service)
			proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
			if err != nil {
				t.Fatal(err)
			}
			request := rowProposalApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-row-definition")
			beforeConfig := append([]byte(nil), store.created.DraftConfig...)
			beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
			if err := test.mutate(store, &workspace, &request); err != nil {
				t.Fatal(err)
			}
			conflictConfig := append([]byte(nil), store.created.DraftConfig...)
			conflictVersion, conflictDigest := store.created.DraftVersion, store.created.DraftDigest
			if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
				t.Fatal("accepted stale or tampered proposal")
			}
			if store.saveDraftCalls != 0 || store.created.DraftVersion != conflictVersion || store.created.DraftDigest != conflictDigest || string(store.created.DraftConfig) != string(conflictConfig) {
				t.Fatalf("rejected apply mutated current draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
			}
			if test.name == "tampered receipt binding" && (store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig)) {
				t.Fatalf("tampered receipt rejection changed draft: %#v", store.created)
			}
		})
	}
}

func TestApplyRowDefinitionProposalLeavesDraftUntouchedOnCASConflict(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	enableRowDefinitionPreview(service)
	proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	store.applyErr = explorer.ErrDraftConflict
	request := rowProposalApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-conflict")
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
		t.Fatal("accepted compare-and-swap conflict")
	}
	if store.saveDraftCalls != 1 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig) {
		t.Fatalf("conflicted apply mutated draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func enableRowDefinitionPreview(service *Service) {
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil || !bindings.IncludeRowIdentity {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("row-definition preview requires a receipt and stable row identities")
		}
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		document := proposalDocument(workspace, "patients")
		if document == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("row-definition preview receipt has no patients output")
		}
		identity := "records:patient-1"
		if document.Rows.Kind == authoringv2.RowDefinitionExpanded && document.Rows.Expanded != nil {
			identity = "expanded:" + document.Rows.Expanded.OccurrenceID + ":" + document.Rows.Expanded.ScopePath
		}
		if err := visit(map[string]any{"__loom_row_id": identity, "patient_id": "patient-1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
}

func rowProposalService(t *testing.T) (*Service, *fakeStore, capability.Snapshot, authoringv2.Workspace) {
	t.Helper()
	snapshot := readySnapshot("project-a", "generation-a", "snapshot-row-proposal", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	workspace := lifecycleCandidatePreviewWorkspace()
	workspace.SemanticsVersion = authoringv2.CurrentSemanticsVersion
	workspace.Documents[0].Rows = authoringv2.RecordsRowDefinition()
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{
		Project: "project-a", ExplorerID: "patients", Title: "Patients", DraftConfig: draft,
		DraftVersion: 7, DraftDigest: digest,
	}, copyGet: true}
	config := testConfig(snapshot)
	config.Capability.Catalog = func(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		return lifecycleInterpretationCatalog(snapshot, explorerID)
	}
	config.RowChoiceResolver = rowProposalChoiceResolver{}
	config.RowChoicePlanner = rowProposalChoicePlanner{}
	config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := nativeReceipt(snapshot)
		receipt.IntentDigest, err = request.Workspace.Digest()
		if err != nil {
			return nil, err
		}
		receipt.NormalizedBundle, err = request.Workspace.CanonicalJSON()
		if err != nil {
			return nil, err
		}
		receipt.RowDefinitionProposal = cloneRowDefinitionProposalBinding(request.RowDefinitionProposal)
		receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = explorer.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	return newTestService(t, store, config), store, snapshot, workspace
}

func rowProposalRequest(owner *explorer.Explorer, snapshot capability.Snapshot) RowDefinitionProposalRequest {
	return RowDefinitionProposalRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest, OutputID: "patients",
		Selection: RowDefinitionSelection{Kind: RowDefinitionSelectionExpanded, Expanded: &ExpandedSelection{
			RowChoiceID: "opaque-choice-1", EmptyCollectionPolicy: authoringv2.EmptyCollectionPreserveParent,
		}},
	}
}

func rowProposalApplyRequest(owner *explorer.Explorer, snapshot capability.Snapshot, proposalID, commandID string) authoringv2.ApplyCommandsRequest {
	return authoringv2.ApplyCommandsRequest{
		CommandID: commandID, SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyRowDefinitionProposal, OutputID: "patients", ProposalID: proposalID}},
	}
}

func mustCanonical(t *testing.T, workspace authoringv2.Workspace) []byte {
	t.Helper()
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
