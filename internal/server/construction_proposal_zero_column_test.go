package server

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func TestConstructionProposalFromZeroColumnPopulationAppliesRelatedExpansion(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("proposal panicked: %v\n%s", recovered, debug.Stack())
		}
	}()

	baseSnapshot := testAuthoringV2CapabilitySnapshot()
	resources := []string{"Specimen", "Observation", "Condition", "Patient", "MedicationAdministration", "Medication"}
	nodes := make([]capability.Node, len(resources))
	for index, resource := range resources {
		nodes[index] = capability.Node{ID: strings.ToLower(resource), ResourceType: resource, Populated: true}
	}
	nodes[0].RowRootEligible = true
	nodes[0].RowGrain = "RESOURCE"
	hopMetadata := []struct{ relationship, direction string }{
		{"focus_Specimen", "INBOUND"},
		{"stage_assessment_Observation", "INBOUND"},
		{"subject_Patient", "OUTBOUND"},
		{"subject_Patient", "INBOUND"},
		{"medication_reference_Medication", "OUTBOUND"},
	}
	route := make([]capability.ConstructionRouteStep, len(hopMetadata))
	edges := make([]capability.Edge, len(hopMetadata))
	for index, hop := range hopMetadata {
		edgeID := "edge-" + string(rune('1'+index))
		route[index] = capability.ConstructionRouteStep{
			EdgeID: edgeID, FromNodeID: nodes[index].ID, ToNodeID: nodes[index+1].ID,
			FromResourceType: resources[index], ToResourceType: resources[index+1],
			Relationship: hop.relationship, StorageDirection: hop.direction, MatchMode: "OPTIONAL",
		}
		edges[index] = capability.Edge{
			ID: edgeID, FromNodeID: nodes[index].ID, ToNodeID: nodes[index+1].ID,
			SourceResourceType: resources[index], TargetResourceType: resources[index+1],
			Label: hop.relationship, StorageDirection: hop.direction,
		}
	}
	snapshot := capability.NewSnapshot(baseSnapshot.Identity, baseSnapshot.Policy, capability.StatusReady, true, false, nodes, edges, nil, nil)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	authorized := lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}

	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.RootResourceType = "Specimen"
	document.Route = authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Specimen"}
	document.Columns = []authoringv2.Column{}
	document.Population = &authoringv2.Population{SelectionRevisionID: "selection-zero-column"}
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate base population workspace: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}

	store := &populationProposalHTTPStore{
		testExplorerStore: newTestExplorerStore(),
		selections:        map[string]explorer.SelectionRevision{},
		members:           map[string][]explorer.SelectionMember{},
	}
	if _, err := store.create(explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Specimens",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	members := []explorer.SelectionMember{
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: snapshot.Identity.Generation, ResourceType: "Specimen", ID: "root-a"}},
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: snapshot.Identity.Generation, ResourceType: "Specimen", ID: "root-b"}},
	}
	completedAt := time.Now().UTC()
	store.selections["selection-zero-column"] = explorer.SelectionRevision{
		ID: "selection-zero-column", Project: "project-a", Generation: snapshot.Identity.Generation, ResourceType: "Specimen",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RuleDigest: "wave104-rule",
		MembershipDigest: explorer.MembershipDigest(members), MemberCount: int64(len(members)), MemberBytes: 256,
		Complete: true, CreatedAt: completedAt, CompletedAt: &completedAt,
	}
	store.members["selection-zero-column"] = members

	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{}, ScopeDigest: recipeScopeDigest,
		QueryRows:    func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		RootPageRows: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceStage := explorer.ReceiptConstructionStage{
		ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
		Columns:      []explorer.ReceiptConstructionStageColumn{},
		Capabilities: []explorer.ReceiptConstructionOperationChoice{{Kind: "RELATED_EXPAND", Supported: true}},
		RelatedExpandAnchors: []explorer.ReceiptConstructionRelatedExpandAnchor{{
			AnchorColumnID: "_key", Kind: "root", ResourceType: "Specimen", Label: "Original Specimen",
		}},
	}
	proposalID := ""
	previewCalls := 0
	service, err := lifecycle.New(domain, lifecycle.Config{
		SelectionMembersCollection: "loom_explorer_selection_members",
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) { return authorized, nil },
			ForExecution:   func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) { return authorized, nil },
			Catalog:        authoringV2Catalog,
		},
		ConstructionSourceStage: func(context.Context, lifecycle.ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error) {
			return sourceStage, nil
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, engine, domain, nil, nil)
		},
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			previewCalls++
			if receipt == nil || receipt.ID != proposalID || receipt.ConstructionProposal == nil ||
				len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "patients" ||
				!bindings.IncludeRowIdentity || !bindings.IncludeSourceIdentity {
				t.Fatalf("apply preview was not bound to exact candidate receipt: receipt=%#v bindings=%#v", receipt, bindings)
			}
			resolved, err := compileValidatedReceiptResolution(ctx, engine, receipt, bindings)
			if err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{
				Output: bindings.OutputNames[0], Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity,
			}, visit)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	choice, err := capability.NewConstructionRelatedResourceRouteChoiceFromAnchor(
		snapshot.Token, recipe.ConstructionSourceProjectionID, "_key", "root", nodes[0].ID, "Specimen",
		nodes[5].ID, "Medication", route,
	)
	if err != nil {
		t.Fatal(err)
	}
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "expand_medications", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedExpand, RelatedExpand: &authoringv2.ConstructionRelatedExpand{
			AnchorColumnID: "_key", ChoiceID: choice.ChoiceID, TargetNodeID: choice.TargetNodeID, TargetResourceType: choice.TargetResource,
			Route: route, ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches},
			EmptyPolicy: authoringv2.ConstructionExpandEmptyPreserveParent, RelatedRecordColumnID: "medication-id",
		}},
		Outputs: []authoringv2.StageColumn{{ID: "medication-id", Name: "related_medication_id", Label: "Medication FHIR resource ID", Type: "string", Nullable: true}},
	}}}
	response, err := service.ProposeConstruction(context.Background(), lifecycle.ConstructionProposalRequest{
		Project: "project-a", ExplorerID: "custom", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, OutputID: "patients", ChangedStepID: "expand_medications",
		CandidateConstruction: candidate,
	})
	if err != nil {
		t.Fatalf("propose five-hop related expansion: %v", err)
	}
	if response.PreviewStatus != "PREVIEW_PENDING" || response.ProposalID == "" {
		t.Fatalf("proposal result = %#v; want persisted preview-pending candidate", response)
	}
	if response.BaseReceiptID != "" {
		t.Fatalf("zero-column single-output source unexpectedly has base receipt %q", response.BaseReceiptID)
	}
	proposalReceipt, err := store.GetCompilationReceipt(context.Background(), response.ProposalID)
	if err != nil || proposalReceipt == nil || proposalReceipt.ConstructionProposal == nil {
		t.Fatalf("candidate receipt binding = %#v, error = %v", proposalReceipt, err)
	}
	if proposalReceipt.ID != response.ProposalID || proposalReceipt.ConstructionProposal.DraftDigest != digest ||
		proposalReceipt.ConstructionProposal.BaseDocumentDigest != response.BaseDocumentDigest ||
		proposalReceipt.ConstructionProposal.CandidateWorkspaceDigest != response.CandidateWorkspaceDigest ||
		proposalReceipt.ConstructionProposal.SnapshotToken != snapshot.Token ||
		proposalReceipt.ConstructionProposal.OutputID != "patients" ||
		proposalReceipt.ConstructionProposal.ChangedStepID != "expand_medications" {
		t.Fatalf("candidate proposal is not bound to exact zero-column base and candidate: %#v", proposalReceipt.ConstructionProposal)
	}

	proposalID = response.ProposalID
	preview, err := service.Preview(context.Background(), lifecycle.PreviewRequest{
		Project: "project-a", ExplorerID: "custom", ReceiptID: response.ProposalID, OutputID: "patients", Limit: 25,
		SinkFactory: func(_ *explorer.CompilationReceipt, _ []explorer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	})
	if err != nil {
		t.Fatalf("preview exact five-hop related expansion candidate: %v", err)
	}
	if preview.Receipt == nil || preview.Receipt.ID != response.ProposalID || preview.Summary.Output != "patients" || preview.Summary.RowCount != 0 || previewCalls != 1 {
		t.Fatalf("proposal preview = %#v; calls = %d, want the candidate output with the stub's zero rows", preview, previewCalls)
	}
	applyRequest := authoringv2.ApplyCommandsRequest{
		CommandID: "apply-zero-column-construction", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", ProposalID: response.ProposalID}},
	}
	staleRequest := applyRequest
	staleRequest.CommandID = "apply-zero-column-construction-stale"
	staleRequest.ExpectedDraftDigest = "sha256:" + strings.Repeat("b", 64)
	if _, err := service.ApplyCommands(context.Background(), "project-a", "custom", staleRequest, "wave104-test"); err == nil {
		t.Fatal("stale draft digest applied the zero-column candidate")
	} else {
		var lifecycleErr *lifecycle.Error
		if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "DRAFT_CONFLICT" {
			t.Fatalf("stale proposal apply error = %v, want DRAFT_CONFLICT", err)
		}
	}
	if previewCalls != 1 {
		t.Fatalf("stale proposal reached apply preview; total preview calls = %d, want only the earlier proposal preview", previewCalls)
	}
	apply, err := service.ApplyCommands(context.Background(), "project-a", "custom", applyRequest, "wave104-test")
	if err != nil {
		t.Fatalf("apply five-hop related expansion proposal: %v", err)
	}
	if previewCalls != 2 || apply == nil || apply.DraftVersion != 2 || apply.DraftDigest != response.CandidateWorkspaceDigest {
		t.Fatalf("apply result = %#v; preview calls = %d, want proposal preview plus exact apply preview and CAS save", apply, previewCalls)
	}
	if len(apply.Workspace.Documents) != 1 || apply.Workspace.Documents[0].Construction == nil ||
		len(apply.Workspace.Documents[0].Construction.Steps) != 1 ||
		apply.Workspace.Documents[0].Construction.Steps[0].Operation.Kind != authoringv2.ConstructionOperationRelatedExpand {
		t.Fatalf("applied zero-column source construction = %#v", apply.Workspace.Documents)
	}
}
