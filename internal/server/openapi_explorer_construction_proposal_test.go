package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]authoringv2.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	document.Construction.Steps = []authoringv2.ConstructionStep{{
		ID: "only_step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind:   authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterExists},
		},
		Outputs: columns,
	}}
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate initial staged workspace: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.create(explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Patients",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil || receipt.ConstructionProposal == nil || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "patients" {
				t.Fatalf("unexpected exact candidate preview receipt/bindings: %#v / %#v", receipt, bindings)
			}
			if err := visit(map[string]any{"c_patient": "patient-1"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"c_patient"}, RowCount: 1, Complete: true}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"

	capabilitiesHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection"}`,
		snapshot.Token, digest,
	))
	if capabilitiesHTTP.StatusCode != http.StatusOK {
		t.Fatalf("construction capabilities status=%d body=%s", capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	var capabilities loomapi.ConstructionCapabilitiesResponse
	if err := json.Unmarshal([]byte(capabilitiesHTTP.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.SelectedStage.Id != "source_projection" || len(capabilities.Stages) != 2 || len(capabilities.SelectedStage.Columns) != 1 {
		t.Fatalf("capabilities did not return exact public source stage: %#v", capabilities)
	}

	proposalHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","removeStepIds":["only_step"],"candidateConstruction":{"version":1,"steps":[]}}`,
		snapshot.Token, digest,
	))
	if proposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("remove-only construction proposal status=%d body=%s", proposalHTTP.StatusCode, proposalHTTP.Body)
	}
	var proposal loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(proposalHTTP.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalId == nil || *proposal.ProposalId == "" || proposal.ChangedStepId != "" || proposal.PreviewStatus != "READY" || proposal.Preview == nil || proposal.Preview.RowCount != 1 || proposal.PreviewDurationMs < 0 {
		t.Fatalf("proposal omitted exact preview evidence or removal identity: %#v", proposal)
	}
	if proposal.Preview.ReceiptId != *proposal.ProposalId || proposal.Preview.OutputId != "patients" {
		t.Fatalf("preview is not bound to the proposal receipt/output: %#v", proposal.Preview)
	}

	applyHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-last-step-removal","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, digest, *proposal.ProposalId,
	))
	if applyHTTP.StatusCode != http.StatusOK {
		t.Fatalf("apply remove-only construction status=%d body=%s", applyHTTP.StatusCode, applyHTTP.Body)
	}
	updated, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := authoringv2.DecodeWorkspace(updated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DraftVersion != 2 || accepted.Documents[0].Construction == nil || len(accepted.Documents[0].Construction.Steps) != 0 {
		t.Fatalf("remove-only proposal was not atomically applied: draft=%#v workspace=%#v", updated, accepted)
	}
}

func TestEmptyConstructionBootstrapAddFirstColumnAndProposeOperation(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Nodes = []capability.Node{{ID: "n_observation", ResourceType: "Observation", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 1}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "c_observation_status", NodeID: "n_observation", ResourceType: "Observation",
		FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "OPTIONAL_ONE",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}}
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.RootResourceType = "Observation"
	document.Route.ResourceType = "Observation"
	document.Columns = nil
	document.Construction = nil
	document.TableShape = nil
	workspace.Documents[0] = document
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.create(explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Observations",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil)
		},
		ConstructionSourceStage: func(ctx context.Context, request lifecycle.ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error) {
			return compileConstructionSourceStage(ctx, request, recipeEngine)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil || len(receipt.EmittedColumns) != 1 {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("expected one exact candidate output column")
			}
			column := receipt.EmittedColumns[0]
			if err := visit(map[string]any{column.PublicColumn: "final"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{column.PublicColumn}, RowCount: 1, Complete: true}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"

	capabilitiesHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection"}`,
		snapshot.Token, digest,
	))
	if capabilitiesHTTP.StatusCode != http.StatusOK {
		t.Fatalf("empty source capabilities status=%d body=%s", capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	var capabilities loomapi.ConstructionCapabilitiesResponse
	if err := json.Unmarshal([]byte(capabilitiesHTTP.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.SelectedStage.Id != "source_projection" || capabilities.SelectedStage.RowIdentityColumn == nil || *capabilities.SelectedStage.RowIdentityColumn == "" || len(capabilities.SelectedStage.Columns) != 0 {
		t.Fatalf("empty source descriptor is not compiler-resolved: %#v", capabilities.SelectedStage)
	}
	for _, operation := range capabilities.SelectedStage.Capabilities {
		if operation.Supported {
			t.Fatalf("zero-column source unexpectedly supports %s: %#v", operation.Kind, operation)
		}
	}

	choice, err := capability.NewFieldConstructionChoice(snapshot.Token, snapshot.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	addHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"bootstrap-add-first-column","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_CHOICE","outputId":"patients","constructionChoice":{"choiceId":%q,"form":"VALUE"}}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, digest, choice.ChoiceID,
	))
	if addHTTP.StatusCode != http.StatusOK {
		t.Fatalf("add first source column status=%d body=%s", addHTTP.StatusCode, addHTTP.Body)
	}
	updated, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = authoringv2.DecodeWorkspace(updated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Documents[0].Columns) != 1 {
		t.Fatalf("first Add persisted %d columns (draft version %d): %s", len(workspace.Documents[0].Columns), updated.DraftVersion, addHTTP.Body)
	}
	if workspace.Documents[0].Columns[0].ColumnID == "" {
		t.Fatal("first Add persisted a source column without stable columnId")
	}
	digest = updated.DraftDigest
	capabilitiesHTTP = requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection"}`,
		snapshot.Token, updated.DraftVersion, digest,
	))
	if capabilitiesHTTP.StatusCode != http.StatusOK {
		t.Fatalf("post-Add source capabilities status=%d body=%s", capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	if err := json.Unmarshal([]byte(capabilitiesHTTP.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	if len(capabilities.SelectedStage.Columns) != 1 || capabilities.SelectedStage.Columns[0].Id != workspace.Documents[0].Columns[0].ColumnID {
		t.Fatalf("compiled source schema lost the added column ID: %#v", capabilities.SelectedStage)
	}
	column := workspace.Documents[0].Columns[0]
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "keep_observation_status", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: column.ColumnID, Operator: authoringv2.ConstructionFilterExists}},
		Outputs:   []authoringv2.StageColumn{{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType}},
	}}}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	proposalHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"keep_observation_status","candidateConstruction":%s}`,
		snapshot.Token, updated.DraftVersion, digest, candidateJSON,
	))
	if proposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("first typed proposal status=%d body=%s", proposalHTTP.StatusCode, proposalHTTP.Body)
	}
	var proposal loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(proposalHTTP.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalId == nil || proposal.PreviewStatus != "READY" || proposal.Preview == nil || proposal.Preview.RowCount != 1 {
		t.Fatalf("first typed proposal did not complete preview: %#v", proposal)
	}
	applyHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-first-operation","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, updated.DraftVersion, digest, *proposal.ProposalId,
	))
	if applyHTTP.StatusCode != http.StatusOK {
		t.Fatalf("apply first typed operation status=%d body=%s", applyHTTP.StatusCode, applyHTTP.Body)
	}
	updated, err = service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = authoringv2.DecodeWorkspace(updated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Documents[0].Construction.Steps) != 1 || workspace.Documents[0].Construction.Steps[0].ID != "keep_observation_status" {
		t.Fatalf("proposal did not atomically install its typed step: %#v", workspace.Documents[0].Construction)
	}
}
