package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/gofiber/fiber/v3"
)

func TestConstructionCodedGroupOpenAPIDTOPreservesPayloadAndChoices(t *testing.T) {
	requestJSON := []byte(`{"candidateConstruction":{"version":1,"steps":[{"id":"group_codes","inputs":[{"kind":"SOURCE_PROJECTION"}],"operation":{"kind":"CODED_GROUP","codedGroup":{"constructionId":"group_codes","choiceId":"fresh-choice","source":{"occurrenceId":"base","resourceType":"BodyStructure","codingPath":"includedStructure[].structure.coding[]","fhirType":"Coding","cardinality":"MANY","shape":"ARRAY","route":[]},"missingKeyPolicy":"GROUP","systemOutputColumnId":"system","versionOutputColumnId":"version","codeOutputColumnId":"code","distinctSourceCountOutputColumnId":"count"}},"outputs":[]}]}}`)
	var request loomapi.ConstructionProposalRequest
	if err := json.Unmarshal(requestJSON, &request); err != nil {
		t.Fatal(err)
	}
	construction, err := directAuthoringJSON[authoringv2.Construction](request.CandidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	if len(construction.Steps) != 1 || construction.Steps[0].Operation.CodedGroup == nil ||
		construction.Steps[0].Operation.CodedGroup.Source.CodingPath != "includedStructure[].structure.coding[]" ||
		construction.Steps[0].Operation.CodedGroup.ChoiceID != "fresh-choice" {
		t.Fatalf("OpenAPI request conversion dropped CODED_GROUP payload: %#v", construction)
	}

	stage := explorer.ReceiptConstructionStage{
		ID: recipe.ConstructionSourceProjectionID,
		CodedGroupChoices: []explorer.ReceiptConstructionCodedGroupChoice{{
			ChoiceID: "fresh-choice", OccurrenceID: "base", ResourceType: "BodyStructure",
			CodingPath: "includedStructure[].structure.coding[]", Label: "BodyStructure · includedStructure[] › structure › coding[]",
		}},
	}
	response, err := directAuthoringJSON[loomapi.ConstructionCapabilitiesResponse](lifecycle.ConstructionCapabilitiesResponse{
		Stages: []explorer.ReceiptConstructionStage{stage}, SelectedStage: stage,
		SourceProjectionAvailability: &lifecycle.ConstructionSourceProjectionAvailability{
			Supported: true, StageID: recipe.ConstructionSourceProjectionID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.SelectedStage.CodedGroupChoices == nil || len(*response.SelectedStage.CodedGroupChoices) != 1 ||
		(*response.SelectedStage.CodedGroupChoices)[0].CodingPath != "includedStructure[].structure.coding[]" {
		t.Fatalf("OpenAPI response conversion dropped codedGroupChoices: %#v", response.SelectedStage)
	}
	if response.SourceProjectionAvailability == nil || !response.SourceProjectionAvailability.Supported ||
		response.SourceProjectionAvailability.StageId != recipe.ConstructionSourceProjectionID {
		t.Fatalf("OpenAPI response conversion dropped stage-bound source projection availability: %#v", response.SourceProjectionAvailability)
	}
	unsupported, err := directAuthoringJSON[loomapi.ConstructionCapabilitiesResponse](lifecycle.ConstructionCapabilitiesResponse{
		SourceProjectionAvailability: &lifecycle.ConstructionSourceProjectionAvailability{
			StageID: "group_stage", ReasonCode: "ROOT_SOURCE_PROJECTION_UNAVAILABLE", Reason: "root fields are not carried to this stage",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if unsupported.SourceProjectionAvailability == nil || unsupported.SourceProjectionAvailability.Supported ||
		unsupported.SourceProjectionAvailability.StageId != "group_stage" ||
		unsupported.SourceProjectionAvailability.ReasonCode == nil || *unsupported.SourceProjectionAvailability.ReasonCode != "ROOT_SOURCE_PROJECTION_UNAVAILABLE" ||
		unsupported.SourceProjectionAvailability.Reason == nil || *unsupported.SourceProjectionAvailability.Reason != "root fields are not carried to this stage" {
		t.Fatalf("OpenAPI response conversion dropped root-projection unavailability: %#v", unsupported.SourceProjectionAvailability)
	}
}

func TestConstructionProposalPreviewBudgetExtendsOnlyPivot(t *testing.T) {
	pivotContext, pivotTimeout := constructionProposalPreviewBudget(context.Background(), authoringv2.Construction{
		Steps: []authoringv2.ConstructionStep{{Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot}}},
	})
	if pivotTimeout != 30*time.Second || previewQueryMaxRuntime(pivotContext) != pivotCategoryScanTimeout {
		t.Fatalf("Pivot proposal preview budget = %s / query max runtime %s, want 30s", pivotTimeout, previewQueryMaxRuntime(pivotContext))
	}

	parentDeadline := time.Now().Add(2 * time.Second)
	parentContext, cancel := context.WithDeadline(context.Background(), parentDeadline)
	defer cancel()
	boundedContext, _ := constructionProposalPreviewBudget(parentContext, authoringv2.Construction{
		Steps: []authoringv2.ConstructionStep{{Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot}}},
	})
	if got, ok := boundedContext.Deadline(); !ok || !got.Equal(parentDeadline) {
		t.Fatalf("Pivot proposal context deadline = %s (present %t), want caller deadline %s", got, ok, parentDeadline)
	}

	ordinaryContext, ordinaryTimeout := constructionProposalPreviewBudget(context.Background(), authoringv2.Construction{
		Steps: []authoringv2.ConstructionStep{{Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter}}},
	})
	if ordinaryTimeout != explorerPreviewTimeout || previewQueryMaxRuntime(ordinaryContext) != explorerPreviewTimeout {
		t.Fatalf("ordinary proposal preview budget = %s / query max runtime %s, want 10s", ordinaryTimeout, previewQueryMaxRuntime(ordinaryContext))
	}
}

func TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	sibling := workspace.Documents[0]
	sibling.Output = authoringv2.Output{ID: "sibling_patients", Title: "Sibling patients", RowLabel: "Sibling patients"}
	sibling.Columns = append([]authoringv2.Column(nil), sibling.Columns...)
	sibling.Columns[0].ColumnID = "sibling_patient_id"
	sibling.Columns[0].Column = "sibling_patient_id"
	sibling.Columns[0].Label = "Sibling patient ID"
	workspace.Documents = append(workspace.Documents, sibling)
	workspace.Tabs = append(workspace.Tabs, authoringv2.Tab{ID: "sibling-patients-tab", Title: "Sibling patients", OutputID: "sibling_patients", Order: 1, Visible: true})
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
	categoryScanCalls := 0
	proposalPreviewCalls := 0
	var proposalPreviewQueryRuntime time.Duration
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
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		ScanCategories: func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
			categoryScanCalls++
			return dataframeexecution.CategoryScanResult{}, nil
		},
		PreviewReceipt: func(previewCtx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			proposalPreviewCalls++
			proposalPreviewQueryRuntime = previewQueryMaxRuntime(previewCtx)
			if receipt == nil || receipt.ConstructionProposal == nil || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "patients" {
				t.Fatalf("unexpected exact candidate preview receipt/bindings: %#v / %#v", receipt, bindings)
			}
			if err := visit(map[string]any{"c_patient": "patient-1"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"c_patient"}, RowCount: 1, Complete: true, PartialValidation: true}, nil
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
	if capabilities.SnapshotToken != snapshot.Token || capabilities.DraftVersion != 1 || capabilities.DraftDigest != digest || capabilities.OutputId != "patients" {
		t.Fatalf("workspace input schemas were not returned under the requested snapshot and draft identity: %#v", capabilities)
	}
	if len(capabilities.WorkspaceInputs) != 1 || capabilities.WorkspaceInputs[0].OutputId != "sibling_patients" || capabilities.WorkspaceInputs[0].Title != "Sibling patients" {
		t.Fatalf("wire workspace inputs = %#v, want the independent sibling output", capabilities.WorkspaceInputs)
	}
	var siblingColumn *loomapi.ConstructionWorkspaceInputColumn
	for index := range capabilities.WorkspaceInputs[0].Columns {
		if capabilities.WorkspaceInputs[0].Columns[index].Id == "sibling_patient_id" {
			siblingColumn = &capabilities.WorkspaceInputs[0].Columns[index]
			break
		}
	}
	if siblingColumn == nil || siblingColumn.Name != "sibling_patient_id" || siblingColumn.LogicalType != "string" || siblingColumn.Cardinality != loomapi.ConstructionWorkspaceInputColumnCardinalityOptionalOne || !siblingColumn.Nullable {
		t.Fatalf("wire workspace input omitted exact compiler-stable sibling column schema: %#v", capabilities.WorkspaceInputs)
	}
	if siblingColumn.JoinCompatibilityKey == nil || *siblingColumn.JoinCompatibilityKey != "String" || siblingColumn.AppendCompatibilityKey == nil || *siblingColumn.AppendCompatibilityKey != "string:String" {
		t.Fatalf("wire workspace input omitted exact compatible scalar bases: %#v", siblingColumn)
	}
	columnCardinality := capabilities.SelectedStage.Columns[0].Cardinality
	if columnCardinality == nil || *columnCardinality != loomapi.ConstructionStageColumnDescriptorCardinalityOPTIONALONE {
		t.Fatalf("source stage cardinality = %v, want optional_one", columnCardinality)
	}
	stageCapabilities := make(map[loomapi.ConstructionOperationCapabilityKind]bool)
	for _, capability := range capabilities.SelectedStage.Capabilities {
		stageCapabilities[capability.Kind] = capability.Supported
	}
	if supported, exists := stageCapabilities[loomapi.ConstructionOperationCapabilityKindGROUP]; !exists || !supported {
		t.Fatalf("source stage does not support GROUP: %#v", capabilities.SelectedStage.Capabilities)
	}
	if supported, exists := stageCapabilities[loomapi.ConstructionOperationCapabilityKindEXPAND]; !exists || supported {
		t.Fatalf("scalar source stage should expose EXPAND as unsupported: %#v", capabilities.SelectedStage.Capabilities)
	}

	missingWorkspaceOutputID := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID:     "only_step",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputWorkspaceOutput}},
		Operation: authoringv2.ConstructionOperation{
			Kind:   authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterExists},
		},
		Outputs: columns,
	}}}
	missingWorkspaceOutputIDJSON, err := json.Marshal(missingWorkspaceOutputID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(missingWorkspaceOutputIDJSON, []byte(`"kind":"WORKSPACE_OUTPUT"`)) {
		t.Fatalf("invalid candidate fixture did not encode WORKSPACE_OUTPUT: %s", missingWorkspaceOutputIDJSON)
	}
	storedBefore, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	previewCallsBefore := proposalPreviewCalls
	malformedInputHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"only_step","candidateConstruction":%s}`,
		snapshot.Token, digest, missingWorkspaceOutputIDJSON,
	))
	if malformedInputHTTP.StatusCode != http.StatusBadRequest && malformedInputHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing workspace outputId status=%d, want a request validation error: %s", malformedInputHTTP.StatusCode, malformedInputHTTP.Body)
	}
	if !bytes.Contains([]byte(malformedInputHTTP.Body), []byte("outputId")) {
		t.Fatalf("missing workspace outputId response did not identify the invalid field: %s", malformedInputHTTP.Body)
	}
	if proposalPreviewCalls != previewCallsBefore {
		t.Fatalf("invalid workspace input reached receipt preview: calls before=%d after=%d", previewCallsBefore, proposalPreviewCalls)
	}
	storedAfter, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if storedAfter.DraftVersion != storedBefore.DraftVersion || storedAfter.DraftDigest != storedBefore.DraftDigest || !bytes.Equal(storedAfter.DraftConfig, storedBefore.DraftConfig) {
		t.Fatalf("invalid workspace input mutated the stored draft: before=%#v after=%#v", storedBefore, storedAfter)
	}

	unsupportedTableInput := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "only_step",
		Inputs: []authoringv2.ConstructionInputRef{{
			Kind: authoringv2.ConstructionInputTableRevision, TableID: "table-patients", RevisionID: "patients-r1", OutputID: "patients",
		}},
		Operation: authoringv2.ConstructionOperation{
			Kind:   authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterExists},
		},
		Outputs: columns,
	}}}
	unsupportedTableInputJSON, err := json.Marshal(unsupportedTableInput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(unsupportedTableInputJSON, []byte(`"kind":"TABLE_REVISION"`)) {
		t.Fatalf("pinned table input fixture did not encode TABLE_REVISION: %s", unsupportedTableInputJSON)
	}
	previewCallsBefore = proposalPreviewCalls
	unsupportedTableInputHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"only_step","candidateConstruction":%s}`,
		snapshot.Token, digest, unsupportedTableInputJSON,
	))
	if unsupportedTableInputHTTP.StatusCode != http.StatusBadRequest && unsupportedTableInputHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("TABLE_REVISION on Filter status=%d, want a request validation error: %s", unsupportedTableInputHTTP.StatusCode, unsupportedTableInputHTTP.Body)
	}
	if !bytes.Contains([]byte(unsupportedTableInputHTTP.Body), []byte("TABLE_REVISION")) {
		t.Fatalf("TABLE_REVISION response did not identify the unsupported input kind: %s", unsupportedTableInputHTTP.Body)
	}
	if proposalPreviewCalls != previewCallsBefore {
		t.Fatalf("unsupported table input reached receipt preview: calls before=%d after=%d", previewCallsBefore, proposalPreviewCalls)
	}
	storedAfter, err = service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if storedAfter.DraftVersion != storedBefore.DraftVersion || storedAfter.DraftDigest != storedBefore.DraftDigest || !bytes.Equal(storedAfter.DraftConfig, storedBefore.DraftConfig) {
		t.Fatalf("unsupported table input mutated the stored draft: before=%#v after=%#v", storedBefore, storedAfter)
	}

	staleDiscoveryHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-category-discoveries", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","stageId":"invented-stage","categoryColumnId":"category-id","valueColumnId":"value-id"}`,
		snapshot.Token, digest,
	))
	if staleDiscoveryHTTP.StatusCode != http.StatusConflict || categoryScanCalls != 0 {
		t.Fatalf("stale stage category discovery status=%d calls=%d body=%s", staleDiscoveryHTTP.StatusCode, categoryScanCalls, staleDiscoveryHTTP.Body)
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
	if proposalPreviewQueryRuntime != explorerPreviewTimeout {
		t.Fatalf("ordinary proposal inline preview max runtime = %s, want %s", proposalPreviewQueryRuntime, explorerPreviewTimeout)
	}
	if proposal.Preview.ReceiptId != *proposal.ProposalId || proposal.Preview.OutputId != "patients" {
		t.Fatalf("preview is not bound to the proposal receipt/output: %#v", proposal.Preview)
	}
	if proposal.Preview.PartialValidation == nil || !*proposal.Preview.PartialValidation {
		t.Fatalf("proposal preview lost the compiler partial-validation marker: %#v", proposal.Preview)
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

func TestRelatedExpandChoiceHTTPReturnsExactStageBoundRoute(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_observation", ResourceType: "Observation"})
	snapshot.Edges = append(snapshot.Edges, capability.Edge{
		ID: "e_patient_observation", FromNodeID: "n_patient", ToNodeID: "n_observation",
		SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND",
	})
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "c_observation_status", NodeID: "n_observation", ResourceType: "Observation",
		FieldPath: "status", Label: "Observation status", LogicalType: "code", Cardinality: "OPTIONAL_ONE",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SuggestedValues: []string{"final", "preliminary"},
		SuggestionsComplete: true,
	})
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate route-choice workspace: %v", err)
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
	engine, err := dataframeexecution.New(dataframeexecution.Config{
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
			return compileExplorerReceipt(ctx, request, nil, engine, service, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil || receipt.ConstructionProposal == nil {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("expected a related expansion proposal receipt")
			}
			if err := visit(map[string]any{"c_patient": "patient-1", "observation_id": "observation-1"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{
				Output: "patients", Columns: []string{"c_patient", "observation_id"}, RowCount: 1, Complete: true,
			}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"
	response := requestJSON(t, app, http.MethodPost, basePath+"/related-expand-choices", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection","anchorColumnId":"_key","targetResourceType":"Observation"}`,
		snapshot.Token, digest,
	))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("related expansion choices status=%d body=%s", response.StatusCode, response.Body)
	}
	var result loomapi.RelatedExpandChoiceSearchResponse
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.SnapshotToken != snapshot.Token || result.DraftVersion != 1 || result.DraftDigest != digest ||
		result.OutputId != "patients" || result.StageId != "source_projection" || !result.Complete || len(result.Choices) != 1 {
		t.Fatalf("related expansion choice identity = %#v", result)
	}
	choice := result.Choices[0]
	if choice.AnchorColumnId != "_key" || choice.Kind != "root" || choice.NodeId != "n_patient" ||
		choice.ResourceType != "Patient" || choice.Label != "Original Patient" {
		t.Fatalf("related expansion anchor identity = %#v", choice)
	}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceId)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := identity.Source.(capability.RelatedResourceChoiceSource)
	if !ok || source.StageID != result.StageId || source.NodeID != choice.TargetNodeId || source.ResourceType != choice.TargetResourceType ||
		len(choice.Route) != 1 || choice.Route[0].EdgeId != "e_patient_observation" {
		t.Fatalf("related expansion choice did not bind its exact stage and route: %#v source=%#v", choice, identity.Source)
	}
	contributorsResponse := requestJSON(t, app, http.MethodPost, basePath+"/related-expand-contributors", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection","routeChoiceId":%q,"query":"status"}`,
		snapshot.Token, digest, choice.ChoiceId,
	))
	if contributorsResponse.StatusCode != http.StatusOK {
		t.Fatalf("related expansion contributor choices status=%d body=%s", contributorsResponse.StatusCode, contributorsResponse.Body)
	}
	var contributors loomapi.RelatedExpandContributorChoiceSearchResponse
	if err := json.Unmarshal([]byte(contributorsResponse.Body), &contributors); err != nil {
		t.Fatal(err)
	}
	if contributors.SnapshotToken != snapshot.Token || contributors.DraftVersion != 1 || contributors.DraftDigest != digest ||
		contributors.OutputId != "patients" || contributors.StageId != "source_projection" || contributors.RouteChoiceId != choice.ChoiceId ||
		!contributors.Complete || contributors.Truncated || len(contributors.Choices) != 1 {
		t.Fatalf("related expansion contributor search envelope = %#v", contributors)
	}
	contributor := contributors.Choices[0]
	if contributor.Source.Kind != loomapi.RelatedExpandContributorSourceKindFIELD || contributor.Source.CandidateId != "c_observation_status" ||
		contributor.Source.NodeId != "n_observation" || contributor.Source.ResourceType != "Observation" || contributor.Source.Path != "status" ||
		contributor.Source.LogicalType != "string" || len(contributor.Operators) != 2 ||
		contributor.Operators[0] != loomapi.RelatedExpandContributorChoiceOperators("EXISTS") ||
		contributor.Operators[1] != loomapi.RelatedExpandContributorChoiceOperators("EQUALS") ||
		!contributor.SuggestionsComplete || contributor.SuggestionsSource != loomapi.RelatedExpandContributorChoiceSuggestionsSource("catalog") ||
		len(contributor.SuggestedValues) != 2 || contributor.SuggestedValues[0] != "final" || contributor.SuggestedValues[1] != "preliminary" {
		t.Fatalf("related expansion contributor choice = %#v", contributor)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	outputs := make([]authoringv2.StageColumn, 0, len(document.Columns)+1)
	for _, column := range document.Columns {
		outputs = append(outputs, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	outputs = append(outputs, authoringv2.StageColumn{ID: "observation_id", Name: "observation_id", Label: "FHIR resource ID", Type: "string"})
	authoredRoute := make([]capability.ConstructionRouteStep, 0, len(choice.Route))
	for _, hop := range choice.Route {
		authoredRoute = append(authoredRoute, capability.ConstructionRouteStep{
			EdgeID: hop.EdgeId, FromNodeID: hop.FromNodeId, ToNodeID: hop.ToNodeId,
			FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
			Relationship: hop.Relationship, StorageDirection: string(hop.StorageDirection), MatchMode: string(hop.MatchMode),
		})
	}
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "expand_observations", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedExpand, RelatedExpand: &authoringv2.ConstructionRelatedExpand{
			AnchorColumnID: "_key", ChoiceID: choice.ChoiceId, TargetNodeID: choice.TargetNodeId, TargetResourceType: choice.TargetResourceType,
			Route: authoredRoute, ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches},
			EmptyPolicy: authoringv2.ConstructionExpandEmptyExclude, RelatedRecordColumnID: "observation_id",
		}},
		Outputs: outputs,
	}}}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	proposalResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"expand_observations","candidateConstruction":%s}`,
		snapshot.Token, digest, candidateJSON,
	))
	if proposalResponse.StatusCode != http.StatusOK {
		t.Fatalf("related expansion proposal status=%d body=%s", proposalResponse.StatusCode, proposalResponse.Body)
	}
	var proposal loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(proposalResponse.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if len(proposal.CandidateConstruction.Steps) != 1 || proposal.CandidateConstruction.Steps[0].Operation.RelatedExpand == nil ||
		proposal.CandidateConstruction.Steps[0].Operation.RelatedExpand.ChoiceId != choice.ChoiceId ||
		proposal.CandidateConstruction.Steps[0].Operation.RelatedExpand.RelatedRecordColumnId != "observation_id" {
		t.Fatalf("strict proposal contract did not preserve RELATED_EXPAND payload: %#v", proposal.CandidateConstruction)
	}
}

func TestConstructionProposalSemanticGroupChoiceReconcilesCompiledOutputMetadata(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Nodes = []capability.Node{{
		ID: "n_specimen", ResourceType: "Specimen", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 12,
	}}
	snapshot.Edges = nil
	snapshot.Candidates = []capability.Candidate{
		{
			ID: "c_specimen_id", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "id", Label: "Specimen ID",
			LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true,
		},
		{
			ID: "c_specimen_subject", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "subject.reference", Label: "Subject reference",
			LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true,
		},
		{
			ID: "c_specimen_type_display", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "type.coding[].display", Label: "Specimen type display",
			LogicalType: "string", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "type.coding[]", MaxItems: 8}},
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
		},
		{
			ID: "c_specimen_type_code", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "type.coding[].code", Label: "Specimen type code",
			LogicalType: "string", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "type.coding[]", MaxItems: 8}},
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
		},
	}

	familyReference := catalog.SemanticInventoryFamilyReference{
		BindingID: "binding_specimen_type", SourceKind: catalog.SemanticInventorySourceRetained,
		ResourceType: "Specimen", SourcePath: "Specimen.type.coding[]", System: "urn:example:specimen-type",
	}
	familyReference.FamilyID = catalog.SemanticInventoryFamilyID(familyReference)
	family := catalog.SemanticInventoryFamily{
		SemanticInventoryFamilyReference: familyReference,
		Source:                           catalog.SemanticObservationSource{Type: "Specimen", Path: "Specimen.type.coding[]"},
		KeySelector:                      "type.coding[]",
		Value: catalog.SemanticObservationValue{
			Selector: "type.coding[].display", Type: "string",
			FallbackSelectors: []string{"type.coding[].code"}, FallbackOnBlankString: true,
		},
		LogicalType: "string", SchemaVersion: catalog.SemanticObservationSchemaVersion,
		RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion), Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", Population: 12,
	}
	buildID := catalog.SemanticInventoryBuildID("project-a", "generation-a")

	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].RootResourceType = "Specimen"
	workspace.Documents[0].Route = authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Specimen"}
	workspace.Documents[0].Output = authoringv2.Output{ID: "specimens", Title: "Specimens", RowLabel: "Specimens"}
	workspace.Documents[0].Columns = []authoringv2.Column{
		{Column: "specimen_id", Label: "Specimen ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
		{Column: "subject_reference", Label: "Subject reference", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "subject.reference", ProjectionMode: "VALUE"}}},
	}
	workspace.Tabs[0].OutputID = "specimens"
	workspace.Tabs[0].Title = "Specimens"
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0] = document
	catalogSnapshot := authoringV2Catalog(snapshot, "specimen-explorer")
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, catalogSnapshot).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate semantic Group source workspace: %v", err)
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
		Project: "project-a", ExplorerID: "specimen-explorer", Title: "Specimens",
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
	previewCalls := 0
	var previewContractErr error
	var resolvedFamilyReferences []catalog.SemanticInventoryFamilyReference
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			return catalog.SemanticInventoryPage{
				State:    catalog.SemanticInventoryComplete,
				Build:    catalog.SemanticInventoryBuild{BuildID: buildID, State: catalog.SemanticInventoryComplete},
				Families: []catalog.SemanticInventoryFamily{family},
			}, nil
		},
		ResolveSemanticInventorySelections: func(_ context.Context, options catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
			resolvedFamilyReferences = append([]catalog.SemanticInventoryFamilyReference(nil), options.FamilyReferences...)
			return catalog.SemanticInventoryResolveResult{
				State:    catalog.SemanticInventoryComplete,
				Build:    catalog.SemanticInventoryBuild{BuildID: buildID, State: catalog.SemanticInventoryComplete},
				Families: []catalog.SemanticInventoryFamily{family},
			}, nil
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			previewCalls++
			if receipt == nil || receipt.ConstructionProposal == nil || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "specimens" {
				previewContractErr = fmt.Errorf("semantic Group proposal did not reach compiled preview: receipt=%#v bindings=%#v", receipt, bindings)
				return dataframeexecution.PreviewSummary{}, previewContractErr
			}
			if err := visit(map[string]any{"specimen_id": "specimen-1", "subject_reference": "Patient/patient-1", "specimen_type": "Tissue", "row_count": 1}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{
				Output: "specimens", Columns: []string{"specimen_id", "subject_reference", "specimen_type", "row_count"},
				RowCount: 1, Complete: true, PartialValidation: true,
			}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/specimen-explorer/authoring/v2"
	capabilitiesResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"specimens","stageId":"source_projection"}`,
		snapshot.Token, digest,
	))
	if capabilitiesResponse.StatusCode != http.StatusOK {
		t.Fatalf("semantic Group capabilities status=%d body=%s", capabilitiesResponse.StatusCode, capabilitiesResponse.Body)
	}
	var capabilities struct {
		SemanticGroupChoices []struct {
			ChoiceID string `json:"choiceId"`
		} `json:"semanticGroupChoices"`
	}
	if err := json.Unmarshal([]byte(capabilitiesResponse.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	if len(capabilities.SemanticGroupChoices) != 1 || capabilities.SemanticGroupChoices[0].ChoiceID == "" {
		t.Fatalf("semantic Group capability choice = %#v, want one current family choice", capabilities.SemanticGroupChoices)
	}

	stepID := "semantic_group"
	var subjectColumnID string
	for _, column := range document.Columns {
		if column.Column == "subject_reference" {
			subjectColumnID = column.ColumnID
		}
	}
	if subjectColumnID == "" {
		t.Fatal("source workspace did not retain a stable Subject reference column ID")
	}
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		{
			ID: stepID, Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
				ConstructionID: stepID, MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
				Keys: []authoringv2.ConstructionGroupKey{
					{InputColumnID: subjectColumnID, OutputColumnID: "subject_reference_group"},
					{OutputColumnID: "specimen_type_group", SemanticCategory: &authoringv2.ConstructionGroupSemanticCategory{Kind: "CHOICE", ChoiceID: capabilities.SemanticGroupChoices[0].ChoiceID}},
				},
				Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
			}},
			Outputs: []authoringv2.StageColumn{
				{ID: "subject_reference_group", Name: "subject_reference_group", Label: "Subject reference", Type: "string"},
				{ID: "specimen_type_group", Name: "specimen_type_group", Label: "Specimen type", Type: "string"},
				{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
			},
		},
	}}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	proposalResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"specimens","changedStepId":%q,"candidateConstruction":%s,"limit":100}`,
		snapshot.Token, digest, stepID, candidateJSON,
	))
	if proposalResponse.StatusCode != http.StatusOK {
		t.Fatalf("semantic Group proposal status=%d body=%s", proposalResponse.StatusCode, proposalResponse.Body)
	}
	if len(resolvedFamilyReferences) != 1 {
		t.Fatalf("semantic Group proposal resolved unexpected inventory families: %#v", resolvedFamilyReferences)
	}
	resolvedFamily := resolvedFamilyReferences[0]
	if resolvedFamily.FamilyID != familyReference.FamilyID || resolvedFamily.BindingID != familyReference.BindingID ||
		resolvedFamily.SourceKind != familyReference.SourceKind || resolvedFamily.ResourceType != familyReference.ResourceType ||
		resolvedFamily.System != familyReference.System || resolvedFamily.Version != familyReference.Version {
		t.Fatalf("semantic Group proposal resolved unexpected inventory family: got %#v want stable scope %#v", resolvedFamily, familyReference)
	}
	var proposal loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(proposalResponse.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalId == nil || proposal.PreviewStatus != "READY" || proposal.Preview == nil || proposal.Preview.RowCount != 1 || previewCalls != 1 || previewContractErr != nil {
		t.Fatalf("semantic Group proposal did not complete compiled preview: response=%#v previewCalls=%d previewErr=%v", proposal, previewCalls, previewContractErr)
	}
	if len(proposal.CandidateConstruction.Steps) != 1 || proposal.CandidateConstruction.Steps[0].Operation.Group == nil ||
		proposal.CandidateConstruction.Steps[0].Operation.Group.Keys == nil ||
		len(*proposal.CandidateConstruction.Steps[0].Operation.Group.Keys) != 2 {
		t.Fatalf("semantic Group proposal did not retain exact server-resolved binding: %#v", proposal.CandidateConstruction)
	}
	semanticKey, err := (*proposal.CandidateConstruction.Steps[0].Operation.Group.Keys)[1].AsConstructionGroupKeySemanticCategory()
	if err != nil || string(semanticKey.SemanticCategory.Kind) != "BINDING" ||
		semanticKey.SemanticCategory.FamilyId == nil || *semanticKey.SemanticCategory.FamilyId != family.FamilyID {
		t.Fatalf("semantic Group proposal did not retain exact server-resolved binding: key=%#v err=%v", semanticKey, err)
	}
}

func TestConstructionProposalCurrentCodingChoiceReconcilesAndPreviewsHTTP(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Nodes = []capability.Node{{
		ID: "n_observation", ResourceType: "Observation", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 12,
	}}
	snapshot.Edges = nil
	snapshot.Candidates = []capability.Candidate{{
		ID: "c_observation_coding", NodeID: "n_observation", ResourceType: "Observation",
		FieldPath: "component[].code.coding[]", Label: "Observed coding", LogicalType: "Coding", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{
			{Path: "component[]", MaxItems: 8},
			{Path: "component[].code.coding[]", MaxItems: 8},
		},
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray}, SupportedOperations: []capability.Operation{capability.OperationSelect},
		Observed: true, Populated: true,
	}, {
		ID: "c_observation_component_value", NodeID: "n_observation", ResourceType: "Observation",
		FieldPath: "component[].valueQuantity.value", Label: "Component quantity", LogicalType: "decimal", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "component[]", MaxItems: 8}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
		Observed:           true, Populated: true,
	}, {
		ID: "c_observation_status", NodeID: "n_observation", ResourceType: "Observation",
		FieldPath: "status", Label: "Observation status", LogicalType: "string", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		Observed:        true, Populated: true,
	}}

	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	rowChoices, err := lifecycle.NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.RootResourceType = "Observation"
	document.Route = authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"}
	document.Rows = authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionExpanded, Expanded: &authoringv2.ExpandedRows{
		OccurrenceID: authoringv2.RootOccurrenceID, ScopePath: "component[].code.coding[]",
		EmptyCollectionPolicy: authoringv2.EmptyCollectionPreserveParent,
	}}
	document.Columns = []authoringv2.Column{}
	document.Output = authoringv2.Output{ID: "observations", Title: "Observations", RowLabel: "Observations"}
	document, err = authoringv2.UpgradeDocumentToConstruction(document)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0] = document
	workspace.Tabs[0].OutputID = "observations"
	workspace.Tabs[0].Title = "Observations"
	catalogSnapshot := authoringV2Catalog(snapshot, "observation-explorer")
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, catalogSnapshot).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate current Coding workspace: %v", err)
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
		Project: "project-a", ExplorerID: "observation-explorer", Title: "Observations",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	previewCalls := 0
	var previewContractErr error
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		RowChoicePlanner: rowChoices,
		ConstructionSourceStage: func(ctx context.Context, request lifecycle.ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error) {
			return compileConstructionSourceStage(ctx, request, recipeEngine)
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			previewCalls++
			if receipt == nil || receipt.ConstructionProposal == nil || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "observations" {
				previewContractErr = fmt.Errorf("scalar and Coding Group proposal did not reach compiled preview: receipt=%#v bindings=%#v", receipt, bindings)
				return dataframeexecution.PreviewSummary{}, previewContractErr
			}
			rowValuePresent := false
			for _, stage := range receipt.ConstructionStages["observations"] {
				for _, column := range stage.Columns {
					if column.ID == "component_values" {
						rowValuePresent = true
					}
				}
			}
			row := map[string]any{"status_group": "final", "coding_identity": "urn:loinc|2.74|1234-5", "row_count": 1}
			columns := []string{"status_group", "coding_identity", "row_count"}
			if rowValuePresent {
				row["component_values"] = []any{172.5}
				columns = append(columns, "component_values")
			}
			if err := visit(row); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{
				Output: "observations", Columns: columns, RowCount: 1, Complete: true, PartialValidation: true,
			}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/observation-explorer/authoring/v2"
	capabilitiesResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"observations","stageId":"source_projection"}`,
		snapshot.Token, digest,
	))
	if capabilitiesResponse.StatusCode != http.StatusOK {
		t.Fatalf("current Coding capabilities status=%d body=%s", capabilitiesResponse.StatusCode, capabilitiesResponse.Body)
	}
	var capabilities struct {
		GroupFieldChoices []struct {
			Kind        string `json:"kind"`
			ValueKind   string `json:"valueKind"`
			FieldSource struct {
				ChoiceID string `json:"choiceId"`
			} `json:"fieldSource"`
			SourceStage  string `json:"sourceStageId"`
			SourceShape  string `json:"sourceShapeId"`
			SourceRole   string `json:"sourceRole"`
			OccurrenceID string `json:"occurrenceId"`
			ResourceType string `json:"resourceType"`
			OwnerType    string `json:"ownerResourceType"`
			CandidateID  string `json:"candidateId"`
			Path         string `json:"canonicalPath"`
		} `json:"groupFieldChoices"`
		GroupRowValueChoices []struct {
			Kind        string `json:"kind"`
			ValueKind   string `json:"valueKind"`
			FieldSource struct {
				ChoiceID string `json:"choiceId"`
			} `json:"fieldSource"`
			SourceStage  string `json:"sourceStageId"`
			SourceShape  string `json:"sourceShapeId"`
			SourceRole   string `json:"sourceRole"`
			OccurrenceID string `json:"occurrenceId"`
			ResourceType string `json:"resourceType"`
			OwnerID      string `json:"ownerOccurrenceId"`
			OwnerType    string `json:"ownerResourceType"`
			CandidateID  string `json:"candidateId"`
			Path         string `json:"canonicalPath"`
			Boundary     string `json:"boundarySelector"`
		} `json:"groupRowValueChoices"`
	}
	if err := json.Unmarshal([]byte(capabilitiesResponse.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	if len(capabilities.GroupRowValueChoices) != 0 {
		t.Fatalf("new Group received post-Group Add-columns choices before it was saved: %#v", capabilities.GroupRowValueChoices)
	}
	var codingChoice *struct {
		Kind        string `json:"kind"`
		ValueKind   string `json:"valueKind"`
		FieldSource struct {
			ChoiceID string `json:"choiceId"`
		} `json:"fieldSource"`
		SourceStage  string `json:"sourceStageId"`
		SourceShape  string `json:"sourceShapeId"`
		SourceRole   string `json:"sourceRole"`
		OccurrenceID string `json:"occurrenceId"`
		ResourceType string `json:"resourceType"`
		OwnerType    string `json:"ownerResourceType"`
		CandidateID  string `json:"candidateId"`
		Path         string `json:"canonicalPath"`
	}
	var scalarChoice *struct {
		Kind        string `json:"kind"`
		ValueKind   string `json:"valueKind"`
		FieldSource struct {
			ChoiceID string `json:"choiceId"`
		} `json:"fieldSource"`
		SourceStage  string `json:"sourceStageId"`
		SourceShape  string `json:"sourceShapeId"`
		SourceRole   string `json:"sourceRole"`
		OccurrenceID string `json:"occurrenceId"`
		ResourceType string `json:"resourceType"`
		OwnerType    string `json:"ownerResourceType"`
		CandidateID  string `json:"candidateId"`
		Path         string `json:"canonicalPath"`
	}
	for index := range capabilities.GroupFieldChoices {
		choice := &capabilities.GroupFieldChoices[index]
		if choice.Kind == "FIELD_SOURCE" && choice.ValueKind == "SCALAR" && choice.Path == "status" {
			scalarChoice = choice
		}
		if choice.Kind == "FIELD_SOURCE" && choice.ValueKind == "CODING_IDENTITY" {
			codingChoice = choice
		}
	}
	if codingChoice == nil || codingChoice.FieldSource.ChoiceID == "" || codingChoice.SourceStage != "source_projection" || codingChoice.SourceShape == "" ||
		codingChoice.SourceRole != "CURRENT_ITEM" || codingChoice.OccurrenceID != authoringv2.RootOccurrenceID ||
		codingChoice.ResourceType != "Coding" || codingChoice.OwnerType != "Observation" || codingChoice.Path != "component[].code.coding[]" {
		t.Fatalf("current Coding group choice = %#v, want exact Coding item under Observation owner", capabilities.GroupFieldChoices)
	}
	if scalarChoice == nil || scalarChoice.FieldSource.ChoiceID == "" || scalarChoice.SourceStage != "source_projection" ||
		scalarChoice.SourceShape == "" || scalarChoice.SourceRole != "ROOT" || scalarChoice.OccurrenceID != authoringv2.RootOccurrenceID ||
		scalarChoice.ResourceType != "Observation" || scalarChoice.OwnerType != "Observation" || scalarChoice.CandidateID != "c_observation_status" {
		t.Fatalf("scalar Group field choice = %#v, want exact observed root scalar", capabilities.GroupFieldChoices)
	}

	stepID := "group_current_coding"
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: stepID, Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
			ConstructionID: stepID, MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
			Keys: []authoringv2.ConstructionGroupKey{{
				OutputColumnID: "status_group",
				FieldSource:    &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: scalarChoice.FieldSource.ChoiceID},
			}, {
				OutputColumnID: "coding_identity",
				FieldSource:    &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: codingChoice.FieldSource.ChoiceID},
			}},
			Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "status_group", Name: "status_group", Label: "Status", Type: "string"},
			{ID: "coding_identity", Name: "coding_identity", Label: "Coding", Type: "string"},
			{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
		},
	}}}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	proposalResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"observations","changedStepId":%q,"candidateConstruction":%s,"limit":100}`,
		snapshot.Token, digest, stepID, candidateJSON,
	))
	if proposalResponse.StatusCode != http.StatusOK {
		t.Fatalf("current Coding proposal status=%d body=%s", proposalResponse.StatusCode, proposalResponse.Body)
	}
	var proposal struct {
		ProposalID            *string         `json:"proposalId"`
		PreviewStatus         string          `json:"previewStatus"`
		CandidateConstruction json.RawMessage `json:"candidateConstruction"`
		Preview               *struct {
			RowCount int `json:"rowCount"`
		} `json:"preview"`
	}
	if err := json.Unmarshal([]byte(proposalResponse.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	var resolved authoringv2.Construction
	if err := json.Unmarshal(proposal.CandidateConstruction, &resolved); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == nil || proposal.PreviewStatus != "READY" || proposal.Preview == nil || proposal.Preview.RowCount != 1 || previewCalls != 1 || previewContractErr != nil {
		t.Fatalf("current Coding proposal did not complete preview: response=%#v calls=%d err=%v", proposal, previewCalls, previewContractErr)
	}
	if len(resolved.Steps) != 1 || resolved.Steps[0].Operation.Group == nil || len(resolved.Steps[0].Operation.Group.Keys) != 2 {
		t.Fatalf("resolved Coding construction = %#v", resolved)
	}
	scalarBinding := resolved.Steps[0].Operation.Group.Keys[0].FieldSource
	if scalarBinding == nil || scalarBinding.Kind != "BINDING" || scalarBinding.ValueKind != "SCALAR" ||
		scalarBinding.SourceStageID != "source_projection" || scalarBinding.SourceShapeID != scalarChoice.SourceShape ||
		scalarBinding.SourceRole != "ROOT" || scalarBinding.ResourceType != "Observation" ||
		scalarBinding.OwnerResourceType != "Observation" || scalarBinding.CandidateID != "c_observation_status" || scalarBinding.CanonicalPath != "status" {
		t.Fatalf("resolved scalar field binding = %#v, want exact observed root field", scalarBinding)
	}
	binding := resolved.Steps[0].Operation.Group.Keys[1].FieldSource
	if binding == nil || binding.Kind != "BINDING" || binding.ValueKind != "CODING_IDENTITY" || binding.SourceStageID != "source_projection" ||
		binding.SourceShapeID != codingChoice.SourceShape || binding.SourceRole != "CURRENT_ITEM" || binding.ResourceType != "Coding" ||
		binding.OwnerResourceType != "Observation" || binding.OccurrenceID != authoringv2.RootOccurrenceID ||
		binding.CanonicalPath != "component[].code.coding[]" || binding.CandidateID != "" {
		t.Fatalf("resolved Coding field binding = %#v, want exact current Coding identity", binding)
	}

	applyInitialGroup := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-current-coding-group","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"observations","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, digest, *proposal.ProposalID,
	))
	if applyInitialGroup.StatusCode != http.StatusOK {
		t.Fatalf("apply mixed-source Group status=%d body=%s", applyInitialGroup.StatusCode, applyInitialGroup.Body)
	}
	updated, err := service.Get(context.Background(), "project-a", "observation-explorer")
	if err != nil {
		t.Fatal(err)
	}
	acceptedWorkspace, err := authoringv2.DecodeWorkspace(updated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DraftVersion != 2 || len(acceptedWorkspace.Documents) != 1 || acceptedWorkspace.Documents[0].Construction == nil ||
		len(acceptedWorkspace.Documents[0].Construction.Steps) != 1 || len(acceptedWorkspace.Documents[0].Columns) != 0 {
		t.Fatalf("initial Group proposal was not durably applied: version=%d workspace=%#v", updated.DraftVersion, acceptedWorkspace)
	}
	acceptedConstruction := *acceptedWorkspace.Documents[0].Construction
	rowValueCapabilitiesResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"observations","stageId":"source_projection","groupStepId":%q}`,
		snapshot.Token, updated.DraftVersion, updated.DraftDigest, stepID,
	))
	if rowValueCapabilitiesResponse.StatusCode != http.StatusOK {
		t.Fatalf("post-Group Add-columns capabilities status=%d body=%s", rowValueCapabilitiesResponse.StatusCode, rowValueCapabilitiesResponse.Body)
	}
	var rowValueCapabilities struct {
		GroupRowValueChoices []struct {
			Kind        string `json:"kind"`
			ValueKind   string `json:"valueKind"`
			FieldSource struct {
				ChoiceID string `json:"choiceId"`
			} `json:"fieldSource"`
			SourceStage  string `json:"sourceStageId"`
			SourceShape  string `json:"sourceShapeId"`
			SourceRole   string `json:"sourceRole"`
			OccurrenceID string `json:"occurrenceId"`
			ResourceType string `json:"resourceType"`
			OwnerID      string `json:"ownerOccurrenceId"`
			OwnerType    string `json:"ownerResourceType"`
			CandidateID  string `json:"candidateId"`
			Path         string `json:"canonicalPath"`
			Boundary     string `json:"boundarySelector"`
		} `json:"groupRowValueChoices"`
	}
	if err := json.Unmarshal([]byte(rowValueCapabilitiesResponse.Body), &rowValueCapabilities); err != nil {
		t.Fatal(err)
	}
	var componentValueChoice *struct {
		Kind        string `json:"kind"`
		ValueKind   string `json:"valueKind"`
		FieldSource struct {
			ChoiceID string `json:"choiceId"`
		} `json:"fieldSource"`
		SourceStage  string `json:"sourceStageId"`
		SourceShape  string `json:"sourceShapeId"`
		SourceRole   string `json:"sourceRole"`
		OccurrenceID string `json:"occurrenceId"`
		ResourceType string `json:"resourceType"`
		OwnerID      string `json:"ownerOccurrenceId"`
		OwnerType    string `json:"ownerResourceType"`
		CandidateID  string `json:"candidateId"`
		Path         string `json:"canonicalPath"`
		Boundary     string `json:"boundarySelector"`
	}
	for index := range rowValueCapabilities.GroupRowValueChoices {
		choice := &rowValueCapabilities.GroupRowValueChoices[index]
		if choice.SourceRole == "ANCESTOR" && choice.CandidateID == "c_observation_component_value" {
			componentValueChoice = choice
			break
		}
	}
	if componentValueChoice == nil || componentValueChoice.Kind != "FIELD_SOURCE" || componentValueChoice.ValueKind != "SCALAR" ||
		componentValueChoice.FieldSource.ChoiceID == "" || componentValueChoice.SourceStage != "source_projection" ||
		componentValueChoice.SourceShape != scalarChoice.SourceShape || componentValueChoice.ResourceType != "ObservationComponent" ||
		componentValueChoice.OwnerID != authoringv2.RootOccurrenceID || componentValueChoice.OwnerType != "Observation" ||
		componentValueChoice.Path != "valueQuantity.value" || componentValueChoice.Boundary != "component[]" {
		t.Fatalf("post-Group ancestor row-value choice = %#v, want exact component[] source from the original Group input", rowValueCapabilities.GroupRowValueChoices)
	}
	if len(acceptedConstruction.Steps) != 1 {
		t.Fatalf("accepted Group construction has unexpected steps: %#v", acceptedConstruction)
	}
	rowValueCandidate := acceptedConstruction
	rowValueCandidate.Steps = append([]authoringv2.ConstructionStep(nil), acceptedConstruction.Steps...)
	rowValueCandidate.Steps[0].RowValues = []authoringv2.ConstructionRowValue{{
		FieldSource:    &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: componentValueChoice.FieldSource.ChoiceID},
		OutputColumnID: "component_values", Policy: authoringv2.ConstructionRowValueAll,
	}}
	rowValueCandidate.Steps[0].Outputs = append(append([]authoringv2.StageColumn(nil), rowValueCandidate.Steps[0].Outputs...),
		authoringv2.StageColumn{ID: "component_values", Name: "component_values", Label: "Component values", Type: "array"},
	)
	rowValueCandidateJSON, err := json.Marshal(rowValueCandidate)
	if err != nil {
		t.Fatal(err)
	}
	rowValueProposalResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"observations","changedStepId":%q,"candidateConstruction":%s,"limit":100}`,
		snapshot.Token, updated.DraftVersion, updated.DraftDigest, stepID, rowValueCandidateJSON,
	))
	if rowValueProposalResponse.StatusCode != http.StatusOK {
		t.Fatalf("post-Group Add-columns proposal status=%d body=%s", rowValueProposalResponse.StatusCode, rowValueProposalResponse.Body)
	}
	var rowValueProposal struct {
		ProposalID            *string         `json:"proposalId"`
		PreviewStatus         string          `json:"previewStatus"`
		CandidateConstruction json.RawMessage `json:"candidateConstruction"`
		Preview               *struct {
			RowCount int `json:"rowCount"`
		} `json:"preview"`
	}
	if err := json.Unmarshal([]byte(rowValueProposalResponse.Body), &rowValueProposal); err != nil {
		t.Fatal(err)
	}
	var rowValueResolved authoringv2.Construction
	if err := json.Unmarshal(rowValueProposal.CandidateConstruction, &rowValueResolved); err != nil {
		t.Fatal(err)
	}
	if rowValueProposal.ProposalID == nil || rowValueProposal.PreviewStatus != "READY" || rowValueProposal.Preview == nil ||
		rowValueProposal.Preview.RowCount != 1 || previewCalls != 3 || previewContractErr != nil {
		t.Fatalf("post-Group Add-columns proposal did not complete READY preview: response=%#v calls=%d err=%v", rowValueProposal, previewCalls, previewContractErr)
	}
	if len(rowValueResolved.Steps) != 1 || len(rowValueResolved.Steps[0].RowValues) != 1 ||
		rowValueResolved.Steps[0].RowValues[0].FieldSource == nil || rowValueResolved.Steps[0].RowValues[0].InputColumnID != "" {
		t.Fatalf("resolved Group row value = %#v", rowValueResolved.Steps)
	}
	rowBinding := rowValueResolved.Steps[0].RowValues[0].FieldSource
	if rowBinding.Kind != "BINDING" || rowBinding.ValueKind != "SCALAR" || rowBinding.SourceStageID != "source_projection" ||
		rowBinding.SourceShapeID != componentValueChoice.SourceShape || rowBinding.SourceRole != "ANCESTOR" ||
		rowBinding.OccurrenceID != authoringv2.RootOccurrenceID || rowBinding.ResourceType != "ObservationComponent" ||
		rowBinding.OwnerResourceType != "Observation" || rowBinding.CandidateID != "c_observation_component_value" ||
		rowBinding.CanonicalPath != "valueQuantity.value" || rowBinding.BoundarySelector != "component[]" {
		t.Fatalf("resolved post-Group ancestor binding = %#v, want exact component[] BINDING", rowBinding)
	}
	applyRowValueGroup := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-current-coding-group-add-columns","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"observations","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, updated.DraftVersion, updated.DraftDigest, *rowValueProposal.ProposalID,
	))
	if applyRowValueGroup.StatusCode != http.StatusOK {
		t.Fatalf("apply post-Group Add-columns proposal status=%d body=%s", applyRowValueGroup.StatusCode, applyRowValueGroup.Body)
	}
	finalExplorer, err := service.Get(context.Background(), "project-a", "observation-explorer")
	if err != nil {
		t.Fatal(err)
	}
	finalWorkspace, err := authoringv2.DecodeWorkspace(finalExplorer.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if finalExplorer.DraftVersion != 3 || finalWorkspace.Documents[0].Construction == nil ||
		len(finalWorkspace.Documents[0].Construction.Steps) != 1 || len(finalWorkspace.Documents[0].Construction.Steps[0].RowValues) != 1 ||
		finalWorkspace.Documents[0].Construction.Steps[0].RowValues[0].FieldSource == nil ||
		finalWorkspace.Documents[0].Construction.Steps[0].RowValues[0].FieldSource.Kind != "BINDING" || previewCalls != 4 {
		t.Fatalf("post-Group Add-columns binding was not durably applied and reauthorized: version=%d workspace=%#v previewCalls=%d", finalExplorer.DraftVersion, finalWorkspace, previewCalls)
	}
}

func TestConstructionProposalRelatedCodingGroupReconcilesReceiptMetadataHTTP(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Nodes = []capability.Node{
		{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 3},
		{ID: "n_specimen", ResourceType: "Specimen", Populated: true, DocumentCount: 3},
	}
	snapshot.Edges = []capability.Edge{{
		ID: "e_patient_specimen", FromNodeID: "n_patient", ToNodeID: "n_specimen",
		SourceResourceType: "Patient", TargetResourceType: "Specimen", Label: "subject_Patient", StorageDirection: "INBOUND",
	}}
	snapshot.Candidates = []capability.Candidate{
		{
			ID: "c_patient_id", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient ID",
			LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true,
		},
		{
			ID: "c_specimen_id", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "id", Label: "Specimen ID",
			LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true,
		},
		{
			ID: "c_specimen_type_code", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "type.coding[].code", Label: "Specimen type",
			LogicalType: "string", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "type.coding[]", MaxItems: 8}},
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionArray},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true,
		},
	}
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0] = document
	catalogSnapshot := authoringV2Catalog(snapshot, "patient-specimen-related-group")
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, catalogSnapshot).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate Patient source workspace: %v", err)
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
		Project: "project-a", ExplorerID: "patient-specimen-related-group", Title: "Patients",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		ConstructionSourceStage: func(ctx context.Context, request lifecycle.ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error) {
			return compileConstructionSourceStage(ctx, request, recipeEngine)
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil || receipt.ConstructionProposal == nil {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("related Group did not reach receipt preview")
			}
			row := map[string]any{"patient_id_group": "patient-1", "specimen_type": "tissue", "row_count": 1}
			columns := []string{"patient_id_group", "specimen_type", "row_count"}
			for _, stage := range receipt.ConstructionStages["patients"] {
				for _, column := range stage.Columns {
					if column.ID == "specimen_ids" {
						row["specimen_ids"] = []any{"specimen-1"}
						columns = append(columns, "specimen_ids")
					}
				}
			}
			if err := visit(row); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{
				Output: "patients", Columns: columns,
				RowCount: 1, Complete: true, PartialValidation: true,
			}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/patient-specimen-related-group/authoring/v2"
	capabilitiesResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection"}`,
		snapshot.Token, digest,
	))
	if capabilitiesResponse.StatusCode != http.StatusOK {
		t.Fatalf("related Group capabilities status=%d body=%s", capabilitiesResponse.StatusCode, capabilitiesResponse.Body)
	}
	var capabilities struct {
		GroupFieldChoices []struct {
			Kind          string `json:"kind"`
			ValueKind     string `json:"valueKind"`
			Label         string `json:"label"`
			SourceRole    string `json:"sourceRole"`
			CanonicalPath string `json:"canonicalPath"`
			FieldSource   struct {
				ChoiceID string `json:"choiceId"`
			} `json:"fieldSource"`
			RelatedOccurrence *struct {
				RouteID string `json:"routeId"`
			} `json:"relatedOccurrence"`
		} `json:"groupFieldChoices"`
	}
	if err := json.Unmarshal([]byte(capabilitiesResponse.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	var patientIDChoice, codingChoice *struct {
		Kind          string `json:"kind"`
		ValueKind     string `json:"valueKind"`
		Label         string `json:"label"`
		SourceRole    string `json:"sourceRole"`
		CanonicalPath string `json:"canonicalPath"`
		FieldSource   struct {
			ChoiceID string `json:"choiceId"`
		} `json:"fieldSource"`
		RelatedOccurrence *struct {
			RouteID string `json:"routeId"`
		} `json:"relatedOccurrence"`
	}
	for index := range capabilities.GroupFieldChoices {
		choice := &capabilities.GroupFieldChoices[index]
		if choice.Kind != "FIELD_SOURCE" {
			continue
		}
		if choice.SourceRole == "ROOT" && choice.CanonicalPath == "id" {
			patientIDChoice = choice
		}
		if choice.ValueKind == "CODING_IDENTITY" && choice.RelatedOccurrence != nil {
			codingChoice = choice
		}
	}
	if patientIDChoice == nil || patientIDChoice.FieldSource.ChoiceID == "" || codingChoice == nil || codingChoice.FieldSource.ChoiceID == "" ||
		codingChoice.CanonicalPath != "type.coding[]" || codingChoice.RelatedOccurrence.RouteID == "" {
		t.Fatalf("expected Patient.id and related Specimen Coding choices, got %#v", capabilities.GroupFieldChoices)
	}
	stepID := "group_patient_specimen"
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: stepID, Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
			ConstructionID: stepID, MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
			Keys: []authoringv2.ConstructionGroupKey{
				{OutputColumnID: "patient_id_group", FieldSource: &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: patientIDChoice.FieldSource.ChoiceID}},
				{OutputColumnID: "specimen_type", FieldSource: &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: codingChoice.FieldSource.ChoiceID}},
			},
			Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "patient_id_group", Name: "patient_id_group", Label: "Patient ID", Type: "string"},
			{ID: "specimen_type", Name: "specimen_type", Label: "Specimen type", Type: "string"},
			{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
		},
	}}}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	proposalResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":%q,"candidateConstruction":%s,"limit":100}`,
		snapshot.Token, digest, stepID, candidateJSON,
	))
	if proposalResponse.StatusCode != http.StatusOK {
		t.Fatalf("related Coding Group proposal status=%d body=%s", proposalResponse.StatusCode, proposalResponse.Body)
	}
	var proposal loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(proposalResponse.Body), &proposal); err != nil {
		t.Fatalf("parse ConstructionProposalResponse OpenAPI shape: %v", err)
	}
	if proposal.ProposalId == nil || string(proposal.PreviewStatus) != "READY" || proposal.Preview == nil || proposal.Preview.RowCount != 1 {
		t.Fatalf("related Coding Group proposal did not return READY receipt preview: %#v", proposal)
	}
	resolvedJSON, err := json.Marshal(proposal.CandidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	var resolved authoringv2.Construction
	if err := json.Unmarshal(resolvedJSON, &resolved); err != nil {
		t.Fatalf("parse returned candidate construction source bindings: %v", err)
	}
	if len(resolved.Steps) != 1 || resolved.Steps[0].Operation.Group == nil || len(resolved.Steps[0].Operation.Group.Keys) != 2 {
		t.Fatalf("returned related Group construction = %#v", resolved)
	}
	patientBinding := resolved.Steps[0].Operation.Group.Keys[0].FieldSource
	codingBinding := resolved.Steps[0].Operation.Group.Keys[1].FieldSource
	if patientBinding == nil || patientBinding.Kind != "BINDING" || patientBinding.ValueKind != "SCALAR" ||
		patientBinding.Input == nil || patientBinding.Input.Kind != "CURRENT_STAGE" || patientBinding.Input.CurrentStage == nil ||
		patientBinding.Input.CurrentStage.CanonicalPath != "id" {
		t.Fatalf("returned Patient.id BINDING = %#v", patientBinding)
	}
	if codingBinding == nil || codingBinding.Kind != "BINDING" || codingBinding.ValueKind != "CODING_IDENTITY" ||
		codingBinding.Input == nil || codingBinding.Input.Kind != "RELATED_OCCURRENCE" || codingBinding.Input.RelatedOccurrence == nil ||
		codingBinding.Input.RelatedOccurrence.TargetResourceType != "Specimen" ||
		codingBinding.Input.RelatedOccurrence.CodingSelector != "type.coding[]" ||
		codingBinding.Input.RelatedOccurrence.CodingEmptyPolicy != "GROUP" ||
		codingBinding.Input.RelatedOccurrence.OrdinalSemantics != recipe.ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition {
		t.Fatalf("returned related Specimen Coding BINDING = %#v", codingBinding)
	}

	applyGroup := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-related-coded-group","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, digest, *proposal.ProposalId,
	))
	if applyGroup.StatusCode != http.StatusOK {
		t.Fatalf("apply related Group status=%d body=%s", applyGroup.StatusCode, applyGroup.Body)
	}
	updated, err := service.Get(context.Background(), "project-a", "patient-specimen-related-group")
	if err != nil {
		t.Fatal(err)
	}
	acceptedWorkspace, err := authoringv2.DecodeWorkspace(updated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DraftVersion != 2 || len(acceptedWorkspace.Documents) != 1 || acceptedWorkspace.Documents[0].Construction == nil {
		t.Fatalf("related Group was not durably applied: version=%d workspace=%#v", updated.DraftVersion, acceptedWorkspace)
	}
	rowValueCapabilitiesResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection","groupStepId":%q}`,
		snapshot.Token, updated.DraftVersion, updated.DraftDigest, stepID,
	))
	if rowValueCapabilitiesResponse.StatusCode != http.StatusOK {
		t.Fatalf("saved Group row-value capabilities status=%d body=%s", rowValueCapabilitiesResponse.StatusCode, rowValueCapabilitiesResponse.Body)
	}
	var rowValueCapabilities struct {
		GroupRowValueChoices []struct {
			Kind              string `json:"kind"`
			ValueKind         string `json:"valueKind"`
			Label             string `json:"label"`
			SourceRole        string `json:"sourceRole"`
			SourceStageID     string `json:"sourceStageId"`
			SourceShapeID     string `json:"sourceShapeId"`
			OccurrenceID      string `json:"occurrenceId"`
			ResourceType      string `json:"resourceType"`
			OwnerOccurrenceID string `json:"ownerOccurrenceId"`
			OwnerResourceType string `json:"ownerResourceType"`
			CandidateID       string `json:"candidateId"`
			CanonicalPath     string `json:"canonicalPath"`
			FieldSource       struct {
				ChoiceID string `json:"choiceId"`
			} `json:"fieldSource"`
			RelatedOccurrence *struct {
				RouteID string `json:"routeId"`
			} `json:"relatedOccurrence"`
		} `json:"groupRowValueChoices"`
	}
	if err := json.Unmarshal([]byte(rowValueCapabilitiesResponse.Body), &rowValueCapabilities); err != nil {
		t.Fatal(err)
	}
	var specimenIDChoice *struct {
		Kind              string `json:"kind"`
		ValueKind         string `json:"valueKind"`
		Label             string `json:"label"`
		SourceRole        string `json:"sourceRole"`
		SourceStageID     string `json:"sourceStageId"`
		SourceShapeID     string `json:"sourceShapeId"`
		OccurrenceID      string `json:"occurrenceId"`
		ResourceType      string `json:"resourceType"`
		OwnerOccurrenceID string `json:"ownerOccurrenceId"`
		OwnerResourceType string `json:"ownerResourceType"`
		CandidateID       string `json:"candidateId"`
		CanonicalPath     string `json:"canonicalPath"`
		FieldSource       struct {
			ChoiceID string `json:"choiceId"`
		} `json:"fieldSource"`
		RelatedOccurrence *struct {
			RouteID string `json:"routeId"`
		} `json:"relatedOccurrence"`
	}
	for index := range rowValueCapabilities.GroupRowValueChoices {
		choice := &rowValueCapabilities.GroupRowValueChoices[index]
		if choice.SourceRole == "RELATED_OCCURRENCE" && choice.CandidateID == "c_specimen_id" && choice.CanonicalPath == "id" {
			specimenIDChoice = choice
			break
		}
	}
	if specimenIDChoice == nil || specimenIDChoice.Kind != "FIELD_SOURCE" || specimenIDChoice.ValueKind != "SCALAR" ||
		specimenIDChoice.FieldSource.ChoiceID == "" || specimenIDChoice.SourceStageID != "source_projection" ||
		specimenIDChoice.SourceShapeID == "" || specimenIDChoice.OccurrenceID == "" || specimenIDChoice.ResourceType != "string" ||
		specimenIDChoice.OwnerOccurrenceID != specimenIDChoice.OccurrenceID || specimenIDChoice.OwnerResourceType != "Specimen" ||
		specimenIDChoice.RelatedOccurrence == nil || specimenIDChoice.RelatedOccurrence.RouteID != codingChoice.RelatedOccurrence.RouteID {
		t.Fatalf("saved related Group row-value choices = %#v; want exact Specimen.id choice on saved Coding route", rowValueCapabilities.GroupRowValueChoices)
	}
	if len(rowValueCapabilities.GroupRowValueChoices) != 2 {
		t.Fatalf("saved Group row-value choices = %d, want Patient.id and Specimen.id only", len(rowValueCapabilities.GroupRowValueChoices))
	}

	rowValueCandidate := *acceptedWorkspace.Documents[0].Construction
	rowValueCandidate.Steps = append([]authoringv2.ConstructionStep(nil), rowValueCandidate.Steps...)
	rowValueCandidate.Steps[0].RowValues = []authoringv2.ConstructionRowValue{{
		FieldSource:    &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: specimenIDChoice.FieldSource.ChoiceID},
		OutputColumnID: "specimen_ids", Policy: authoringv2.ConstructionRowValueAll,
	}}
	rowValueCandidate.Steps[0].Outputs = append(append([]authoringv2.StageColumn(nil), rowValueCandidate.Steps[0].Outputs...),
		authoringv2.StageColumn{ID: "specimen_ids", Name: "specimen_ids", Label: "Specimen IDs", Type: "array"},
	)
	rowValueCandidateJSON, err := json.Marshal(rowValueCandidate)
	if err != nil {
		t.Fatal(err)
	}
	rowValueProposalResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":%q,"candidateConstruction":%s,"limit":100}`,
		snapshot.Token, updated.DraftVersion, updated.DraftDigest, stepID, rowValueCandidateJSON,
	))
	if rowValueProposalResponse.StatusCode != http.StatusOK {
		t.Fatalf("saved related Group Specimen.id RowValue proposal status=%d body=%s", rowValueProposalResponse.StatusCode, rowValueProposalResponse.Body)
	}
	var rowValueProposal loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(rowValueProposalResponse.Body), &rowValueProposal); err != nil {
		t.Fatalf("parse saved Group RowValue proposal response: %v", err)
	}
	if rowValueProposal.ProposalId == nil || string(rowValueProposal.PreviewStatus) != "READY" || rowValueProposal.Preview == nil || rowValueProposal.Preview.RowCount != 1 {
		t.Fatalf("saved related Group RowValue did not return READY preview: %#v", rowValueProposal)
	}
	if len(rowValueProposal.Preview.Rows) != 1 || !reflect.DeepEqual(rowValueProposal.Preview.Rows[0]["specimen_ids"], []interface{}{"specimen-1"}) {
		t.Fatalf("saved related Group RowValue preview rows = %#v; want specimen_ids [specimen-1]", rowValueProposal.Preview.Rows)
	}
	resolvedRowValueJSON, err := json.Marshal(rowValueProposal.CandidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	var resolvedRowValue authoringv2.Construction
	if err := json.Unmarshal(resolvedRowValueJSON, &resolvedRowValue); err != nil {
		t.Fatalf("parse resolved related Group RowValue: %v", err)
	}
	if len(resolvedRowValue.Steps) != 1 || len(resolvedRowValue.Steps[0].RowValues) != 1 ||
		resolvedRowValue.Steps[0].RowValues[0].InputColumnID != "" || resolvedRowValue.Steps[0].RowValues[0].FieldSource == nil {
		t.Fatalf("resolved related Group RowValue = %#v", resolvedRowValue.Steps)
	}
	rowValueBinding := resolvedRowValue.Steps[0].RowValues[0].FieldSource
	if rowValueBinding.Kind != "BINDING" || rowValueBinding.ValueKind != "SCALAR" || rowValueBinding.SourceStageID != "source_projection" ||
		rowValueBinding.SourceShapeID != specimenIDChoice.SourceShapeID || rowValueBinding.Input == nil ||
		rowValueBinding.Input.Kind != "RELATED_OCCURRENCE" || rowValueBinding.Input.RelatedOccurrence == nil ||
		rowValueBinding.Input.RelatedOccurrence.TargetResourceType != "Specimen" ||
		rowValueBinding.Input.RelatedOccurrence.TargetOccurrenceID != specimenIDChoice.OwnerOccurrenceID ||
		rowValueBinding.Input.RelatedOccurrence.CandidateID != "c_specimen_id" ||
		rowValueBinding.Input.RelatedOccurrence.CanonicalPath != "id" ||
		rowValueBinding.Input.RelatedOccurrence.Route[0].EdgeID != "e_patient_specimen" ||
		rowValueBinding.Input.RelatedOccurrence.Route[0].MatchMode != "OPTIONAL" {
		t.Fatalf("resolved Specimen.id RowValue did not retain the saved Group route: %#v", rowValueBinding)
	}
}

func TestAuthoredSemanticGroupOutputCanBeRemovedByDownstreamUnpivot(t *testing.T) {
	semanticPath := "construction_semantic_group:binding_specimen_type:family_specimen_type"
	measureName := "specimen_type"
	category := &authoringv2.ConstructionGroupSemanticCategory{
		Kind: "BINDING", BindingID: "binding_specimen_type", FamilyID: "family_specimen_type",
		ResourceType: "Specimen", CodingPath: "type.coding[]",
	}
	document := authoringv2.Document{
		Output:  authoringv2.Output{ID: "specimens"},
		Columns: []authoringv2.Column{{Column: "subject_reference", ColumnID: "source_subject_reference"}},
		Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
			{
				ID: "semantic_group",
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
					ConstructionID: "semantic_group", MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
					Keys:       []authoringv2.ConstructionGroupKey{{OutputColumnID: "specimen_type_group", SemanticCategory: category}},
					Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "specimen_type_group", Name: "specimen_type_group", Label: "Specimen type", Type: "string"},
					{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
				},
			},
			{
				ID: "unpivot_semantic_key", Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationUnpivot, Unpivot: &authoringv2.ConstructionUnpivot{
					ConstructionID:    "unpivot_semantic_key",
					Inputs:            []authoringv2.ConstructionUnpivotInput{{ColumnID: "specimen_type_group", Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &measureName}}},
					KeyOutputColumnID: "measure_key", ValueOutputColumnID: "measure_value", NullRowPolicy: authoringv2.ConstructionUnpivotPreserve,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
					{ID: "measure_key", Name: "measure", Label: "Measure", Type: "string"},
					{ID: "measure_value", Name: "measure_value", Label: "Measure value", Type: "string"},
				},
			},
		}},
	}
	compiled := lower.CompiledRecipeOutput{
		OutputSchema: []lower.CompiledOutputColumn{
			{ID: "row_count", Name: "row_count", Kind: "integer", Cardinality: "required_one"},
			{ID: "measure_key", Name: "measure", Kind: "string", Cardinality: "required_one"},
			{ID: "measure_value", Name: "measure_value", Kind: "string", Cardinality: "required_one"},
		},
		Stages: []lower.CompiledStageDescriptor{
			{ID: "semantic_group", Operation: "GROUP", Columns: []lower.CompiledOutputColumn{{ID: "specimen_type_group", Name: "specimen_type_group", SemanticPath: semanticPath}}},
			{ID: "unpivot_semantic_key", Operation: "UNPIVOT"},
		},
	}
	if err := authoredConstructionOutputsWithCombineInputs(document, map[string]authoredOutputColumn{}, compiled, nil); err != nil {
		t.Fatalf("reconcile semantic Group key removed by downstream UNPIVOT: %v", err)
	}
}

func TestRelatedSourceConstructionProposalHTTPPreviewsAllMatchesAsList(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_observation", ResourceType: "Observation"})
	route := []capability.ConstructionRouteStep{{
		EdgeID: "e_patient_observation", FromNodeID: "n_patient", ToNodeID: "n_observation",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
		StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	snapshot.Edges = []capability.Edge{{
		ID: "e_patient_observation", FromNodeID: "n_patient", ToNodeID: "n_observation",
		SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND",
	}}
	candidate := capability.Candidate{
		ID: "c_observation_status", NodeID: "n_observation", ResourceType: "Observation",
		FieldPath: "status", Label: "Observation status", LogicalType: "string", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	snapshot.Candidates = append(snapshot.Candidates, candidate)
	choice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, candidate)
	if err != nil {
		t.Fatal(err)
	}

	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	rootFields := []struct {
		path, name, label, logicalType, cardinality string
		projection                                  capability.ProjectionMode
	}{
		{path: "active", name: "patient_active", label: "Active", logicalType: "boolean", cardinality: "optional_one", projection: capability.ProjectionScalar},
		{path: "gender", name: "patient_gender", label: "Gender", logicalType: "string", cardinality: "optional_one", projection: capability.ProjectionScalar},
		{path: "birthDate", name: "patient_birth_date", label: "Birth date", logicalType: "date", cardinality: "optional_one", projection: capability.ProjectionScalar},
		{path: "deceasedBoolean", name: "patient_deceased", label: "Deceased", logicalType: "boolean", cardinality: "optional_one", projection: capability.ProjectionScalar},
		{path: "multipleBirthBoolean", name: "patient_multiple_birth", label: "Multiple birth", logicalType: "boolean", cardinality: "optional_one", projection: capability.ProjectionScalar},
		{path: "name[].family", name: "patient_family", label: "Family name", logicalType: "string", cardinality: "many", projection: capability.ProjectionFirst},
		{path: "telecom[].value", name: "patient_telecom", label: "Telecom", logicalType: "string", cardinality: "many", projection: capability.ProjectionFirst},
	}
	for index, field := range rootFields {
		columnID := fmt.Sprintf("source-column-%d", index)
		workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, authoringv2.Column{
			Column: field.name, Label: field.label, LogicalType: field.logicalType, OccurrenceID: "base",
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: field.path, ProjectionMode: map[capability.ProjectionMode]string{capability.ProjectionScalar: "VALUE", capability.ProjectionFirst: "FIRST"}[field.projection]}},
		})
		snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
			ID: columnID, NodeID: "n_patient", ResourceType: "Patient", FieldPath: field.path, Label: field.label,
			LogicalType: field.logicalType, Cardinality: field.cardinality,
			ProjectionModes: []capability.ProjectionMode{field.projection},
		})
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	stageColumns := make([]authoringv2.StageColumn, 0, len(document.Columns)+1)
	for _, column := range document.Columns {
		stageColumns = append(stageColumns, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	related := authoringv2.ConstructionRelatedSource{
		AnchorColumnID: "_key", ChoiceID: choice.ChoiceID, SourceOccurrenceID: candidate.NodeID,
		Source: authoringv2.ConstructionRelatedFieldSource{
			Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID, NodeID: candidate.NodeID,
			ResourceType: candidate.ResourceType, Path: candidate.FieldPath, Cardinality: candidate.Cardinality, LogicalType: candidate.LogicalType,
		},
		Route: route, ContributorRule: authoringv2.ConstructionRelatedContributorRule{
			Policy: authoringv2.ConstructionRelatedAllMatches,
			Predicate: &authoringv2.ContributorPredicate{
				CandidateID: candidate.ID, Operator: authoringv2.ContributorEquals,
				Value: &authoringv2.ContributorValue{Kind: authoringv2.ContributorString, String: stringPointer("final")},
			},
		},
		Form: capability.ConstructionChoiceAll, OutputColumnID: "observation-statuses",
	}
	stageColumns = append(stageColumns, authoringv2.StageColumn{ID: related.OutputColumnID, Name: "observation_statuses", Label: "Observation statuses", Type: candidate.LogicalType})
	step := authoringv2.ConstructionStep{
		ID: "step_related_observation_status", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedSource, RelatedSource: &related},
		Outputs:   stageColumns,
	}
	candidateConstruction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{step}}
	candidateJSON, err := json.Marshal(candidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	var decodedConstruction authoringv2.Construction
	if err := json.Unmarshal(candidateJSON, &decodedConstruction); err != nil || len(decodedConstruction.Steps) != 1 || decodedConstruction.Steps[0].Operation.RelatedSource == nil {
		t.Fatalf("related-source candidate JSON did not round trip: err=%v json=%s decoded=%#v", err, candidateJSON, decodedConstruction)
	}
	decodedPredicate := decodedConstruction.Steps[0].Operation.RelatedSource.ContributorRule.Predicate
	if decodedPredicate == nil || decodedPredicate.CandidateID != candidate.ID || decodedPredicate.Operator != authoringv2.ContributorEquals ||
		decodedPredicate.Value == nil || decodedPredicate.Value.String == nil || *decodedPredicate.Value.String != "final" {
		t.Fatalf("related-source proposal predicate changed during API JSON round trip: %#v", decodedPredicate)
	}
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
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
		Registry:     compilerTestRegistry{},
		ScopeDigest:  recipeScopeDigest,
		QueryRows:    func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		RootPageRows: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	config := lifecycle.Config{
		SelectionMembersCollection: "loom_explorer_selection_members",
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
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil || receipt.ConstructionProposal == nil {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("expected a construction proposal receipt")
			}
			if len(receipt.EmittedColumns) != 9 {
				t.Fatalf("related-source preview emitted %d columns, want the 9-column candidate", len(receipt.EmittedColumns))
			}
			rawReceipt, err := json.Marshal(receipt)
			if err != nil {
				t.Fatalf("marshal related-source receipt as the Arango store does: %v", err)
			}
			var arangoDocument map[string]any
			if err := json.Unmarshal(rawReceipt, &arangoDocument); err != nil {
				t.Fatalf("decode receipt into the Arango document shape: %v", err)
			}
			arangoDocument["_key"] = receipt.ID
			storedRaw, err := json.Marshal(arangoDocument)
			if err != nil {
				t.Fatalf("remarshal the Arango document: %v", err)
			}
			var storedReceipt explorer.CompilationReceipt
			if err := json.Unmarshal(storedRaw, &storedReceipt); err != nil {
				t.Fatalf("decode related-source receipt as the Arango store does: %v", err)
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, &storedReceipt, bindings)
			if err != nil {
				t.Fatalf("validate the JSON round-tripped related-source receipt before preview: %v", err)
			}
			if _, err := recipeEngine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 100}, func(map[string]any) error { return nil }); err != nil {
				t.Fatalf("preview the JSON round-tripped related-source receipt through root paging: %v", err)
			}
			var relatedEmission *explorer.EmittedColumn
			for index := range receipt.EmittedColumns {
				if receipt.EmittedColumns[index].PublicColumn == "observation_statuses" {
					relatedEmission = &receipt.EmittedColumns[index]
					break
				}
			}
			if relatedEmission == nil {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("candidate receipt omitted the related-source list metadata")
			}
			wantCardinality, wantShape := "many", "array"
			wantFilterable, wantChartable := false, false
			relatedValue := any([]any{"final", "amended"})
			if related.RowValuePolicy == authoringv2.ConstructionRowValueOne {
				wantCardinality, wantShape = "optional_one", "scalar"
				wantFilterable, wantChartable = true, true
				relatedValue = "final"
			}
			if relatedEmission.ConstructionID != step.ID || relatedEmission.SourceResourceType != "Observation" || relatedEmission.SourcePath != "status" || relatedEmission.CandidateID != candidate.ID || relatedEmission.OccurrenceID != candidate.NodeID ||
				relatedEmission.NodeID != candidate.NodeID || len(relatedEmission.AuthoredColumns) != 0 || len(relatedEmission.InputColumns) != 0 ||
				relatedEmission.Cardinality != wantCardinality || relatedEmission.Shape != wantShape || relatedEmission.Lossless || relatedEmission.MLReady || relatedEmission.StructuralSuitability != "requires-review" || relatedEmission.Filterable != wantFilterable || relatedEmission.Chartable != wantChartable ||
				!containsOutputContractString(relatedEmission.LossReasons, "RELATED_SOURCE_AUTHORIZED_MATCHES_ONLY") || !containsOutputContractString(relatedEmission.LossReasons, tableShapeMLReadinessUnassessed) {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("related-source output metadata does not match row value policy %q: %#v", related.RowValuePolicy, *relatedEmission)
			}
			contracts, err := explorer.DecodePublicOutputContracts(receipt.PublicOutputContract)
			if err != nil || len(contracts.Outputs) != 1 {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("decode related-source output contract: outputs=%d err=%v", len(contracts.Outputs), err)
			}
			var relatedContract *explorer.PublicOutputColumn
			for index := range contracts.Outputs[0].Columns {
				if contracts.Outputs[0].Columns[index].Column == "observation_statuses" {
					relatedContract = &contracts.Outputs[0].Columns[index]
					break
				}
			}
			if relatedContract == nil || relatedContract.ConstructionID != step.ID || relatedContract.SourceResourceType != "Observation" || relatedContract.SourcePath != "status" ||
				relatedContract.Cardinality != wantCardinality || relatedContract.Shape != wantShape || relatedContract.Lossless || relatedContract.MLReady || len(relatedContract.AuthoredColumns) != 0 || len(relatedContract.InputColumns) != 0 {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("public contract lost related-source identity or shape for row value policy %q: %#v", related.RowValuePolicy, relatedContract)
			}
			row := make(map[string]any, len(receipt.EmittedColumns))
			previewColumns := make([]string, 0, len(receipt.EmittedColumns))
			for _, emitted := range receipt.EmittedColumns {
				row[emitted.PublicColumn] = nil
				previewColumns = append(previewColumns, emitted.PublicColumn)
			}
			row["c_patient"] = "patient-1"
			row["observation_statuses"] = relatedValue
			if err := visit(row); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: previewColumns, RowCount: 1, Complete: true}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"
	proposalHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":%q,"candidateConstruction":%s}`,
		snapshot.Token, digest, step.ID, candidateJSON,
	))
	if proposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("related-source construction proposal status=%d body=%s", proposalHTTP.StatusCode, proposalHTTP.Body)
	}
	var proposal loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(proposalHTTP.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalId == nil || proposal.PreviewStatus != "READY" || proposal.Preview == nil || proposal.Preview.RowCount != 1 {
		t.Fatalf("related-source proposal did not preview the exact candidate: %#v", proposal)
	}
	if got := proposal.Preview.Rows[0]["observation_statuses"]; !reflect.DeepEqual(got, []any{"final", "amended"}) {
		t.Fatalf("related-source default ALL value = %#v, want the full matching list", got)
	}

	// Exercise the same exact proposal through the generated OpenAPI request DTO.
	// A missing wire field silently resets ONE to the default ALL array.
	related.RowValuePolicy = authoringv2.ConstructionRowValueOne
	candidateJSON, err = json.Marshal(candidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	oneProposalHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":%q,"candidateConstruction":%s}`,
		snapshot.Token, digest, step.ID, candidateJSON,
	))
	if oneProposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("related-source ONE proposal status=%d body=%s", oneProposalHTTP.StatusCode, oneProposalHTTP.Body)
	}
	var oneProposal loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(oneProposalHTTP.Body), &oneProposal); err != nil {
		t.Fatal(err)
	}
	if oneProposal.ProposalId == nil || oneProposal.PreviewStatus != "READY" || oneProposal.Preview == nil || oneProposal.Preview.RowCount != 1 {
		t.Fatalf("related-source ONE proposal did not preview the exact candidate: %#v", oneProposal)
	}
	if authored := oneProposal.CandidateConstruction.Steps[0].Operation.RelatedSource; authored == nil || authored.RowValuePolicy == nil || *authored.RowValuePolicy != loomapi.ConstructionRelatedSourceRowValuePolicyONE {
		t.Fatalf("generated OpenAPI proposal DTO dropped related-source ONE policy: %#v", authored)
	}
	if got := oneProposal.Preview.Rows[0]["observation_statuses"]; got != "final" {
		t.Fatalf("related-source ONE value = %#v, want scalar %q", got, "final")
	}
	var relatedPreviewColumn *loomapi.ContractColumn
	for index := range oneProposal.Preview.Columns {
		if oneProposal.Preview.Columns[index].Column == "observation_statuses" {
			relatedPreviewColumn = &oneProposal.Preview.Columns[index]
			break
		}
	}
	if relatedPreviewColumn == nil || relatedPreviewColumn.Shape == nil || *relatedPreviewColumn.Shape != "scalar" {
		t.Fatalf("related-source ONE preview column did not retain scalar shape: %#v", relatedPreviewColumn)
	}
}

func TestBuilderHTTPSerializesEmptyConstructionStepsAsArray(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.Columns = []authoringv2.Column{}
	document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion}
	workspace.Documents[0] = document
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(draft, []byte(`"steps":[]`)) {
		t.Fatalf("test draft did not contain canonical empty steps: %s", draft)
	}
	// Simulate a legacy persisted draft that decoded its empty slice as null.
	draft = bytes.Replace(draft, []byte(`"steps":[]`), []byte(`"steps":null`), 1)
	legacyDigest, err := workspace.LegacyNilConstructionStepsDigest()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if legacyDigest == "" || legacyDigest == digest {
		t.Fatalf("legacy and current construction digests should differ: legacy=%q current=%q", legacyDigest, digest)
	}
	store := newTestExplorerStore()
	if _, err := store.create(explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Patients",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: legacyDigest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{Capability: lifecycle.CapabilityResolver{
		Current: func(context.Context, string, string, string) (capability.Snapshot, error) {
			return snapshot, nil
		},
		ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
			return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		},
		Catalog: authoringV2Catalog,
	}, ConstructionSourceStage: func(context.Context, lifecycle.ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error) {
		return explorer.ReceiptConstructionStage{
			ID: "source_projection", RowIdentityColumn: "__loom_row_id",
			Columns: []explorer.ReceiptConstructionStageColumn{}, Capabilities: []explorer.ReceiptConstructionOperationChoice{},
		}, nil
	}}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"
	response := requestJSON(t, app, http.MethodGet, basePath+"/builder", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("builder status=%d body=%s", response.StatusCode, response.Body)
	}
	var state loomapi.BuilderState
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		t.Fatalf("decode BuilderState: %v; body=%s", err, response.Body)
	}
	if state.Workspace == nil || len(state.Workspace.Documents) != 1 || state.Workspace.Documents[0].Construction == nil {
		t.Fatalf("builder omitted blank table construction: %#v", state.Workspace)
	}
	steps := state.Workspace.Documents[0].Construction.Steps
	if steps == nil || len(steps) != 0 {
		t.Fatalf("builder returned construction steps %#v, want non-nil empty slice; body=%s", steps, response.Body)
	}
	if state.DraftVersion != 2 || state.DraftDigest == legacyDigest || state.DraftDigest != digest {
		t.Fatalf("builder did not return the migrated durable draft version/digest: version=%d digest=%q legacy=%q current=%q", state.DraftVersion, state.DraftDigest, legacyDigest, digest)
	}
	stored, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if stored.DraftVersion != state.DraftVersion || stored.DraftDigest != state.DraftDigest || !bytes.Contains(stored.DraftConfig, []byte(`"steps":[]`)) || bytes.Contains(stored.DraftConfig, []byte(`"steps":null`)) {
		t.Fatalf("stored draft was not durably normalized: version=%d digest=%q draft=%s", stored.DraftVersion, stored.DraftDigest, stored.DraftConfig)
	}
	capabilitiesHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection"}`,
		snapshot.Token, state.DraftVersion, state.DraftDigest,
	))
	if capabilitiesHTTP.StatusCode != http.StatusOK {
		t.Fatalf("construction capabilities after Builder load status=%d body=%s", capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	var capabilities loomapi.ConstructionCapabilitiesResponse
	if err := json.Unmarshal([]byte(capabilitiesHTTP.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.SelectedStage.Id != "source_projection" || int64(capabilities.DraftVersion) != state.DraftVersion || capabilities.DraftDigest != state.DraftDigest {
		t.Fatalf("capabilities did not accept the exact migrated Builder state: %#v", capabilities)
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
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil, nil)
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
		if string(operation.Kind) == "RELATED_SOURCE" {
			if !operation.Supported {
				t.Fatalf("hidden root identity should support related-source addition: %#v", operation)
			}
			continue
		}
		if string(operation.Kind) == "RELATED_EXPAND" {
			if !operation.Supported {
				t.Fatalf("hidden root key should support related expansion: %#v", operation)
			}
			continue
		}
		if string(operation.Kind) == "RELATED_ELIGIBILITY" {
			if !operation.Supported {
				t.Fatalf("hidden root key should support related eligibility: %#v", operation)
			}
			continue
		}
		if string(operation.Kind) == "CODED_PIVOT" {
			if !operation.Supported {
				t.Fatalf("coded pivot should be available before adding source columns: %#v", operation)
			}
			continue
		}
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

func TestRelatedExpandContributorTypedWireSourcesPreserveRepeatedBoundaries(t *testing.T) {
	boundaries := []capability.RepeatedBoundary{
		{Path: "category[]", MaxItems: 4},
		{Path: "category[].coding[]", MaxItems: 8},
	}
	source := authoringv2.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: "candidate-code", NodeID: "node-observation",
		ResourceType: "Observation", Path: "category[].coding[].code", Cardinality: "many", LogicalType: "string",
		RepeatedBoundaries: boundaries,
	}

	response, err := directAuthoringJSON[loomapi.RelatedExpandContributorChoiceSearchResponse](
		lifecycle.RelatedExpandContributorChoiceSearchResponse{
			SnapshotToken: "snapshot", DraftVersion: 2, DraftDigest: "draft-digest", OutputID: "output",
			StageID: "stage", RouteChoiceID: "route-choice", Complete: true,
			Choices: []lifecycle.RelatedExpandContributorChoice{{
				ChoiceID: "field-choice", Source: source, Label: "Observation.category[].coding[].code",
				Operators: []string{"EXISTS", "EQUALS"}, SuggestedValues: []string{"d"},
				SuggestionsComplete: true, SuggestionsSource: "catalog",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 {
		t.Fatalf("typed contributor response choices = %#v", response.Choices)
	}
	wireSource := response.Choices[0].Source
	if wireSource.Cardinality != "many" ||
		wireSource.RepeatedBoundaries == nil || !reflect.DeepEqual(*wireSource.RepeatedBoundaries, []loomapi.RepeatedBoundary{
		{Path: "category[]", MaxItems: 4}, {Path: "category[].coding[]", MaxItems: 8},
	}) {
		t.Fatalf("typed contributor response dropped repeated metadata: %#v", wireSource)
	}

	relatedExpand := authoringv2.ConstructionRelatedExpand{
		AnchorColumnID: "anchor", ChoiceID: "route-choice", TargetNodeID: "node-observation",
		TargetResourceType: "Observation", ContributorRule: authoringv2.ConstructionRelatedContributorRule{
			Policy: authoringv2.ConstructionRelatedAllMatches,
		},
		ContributorSource: &source, ContributorChoiceID: "field-choice",
		EmptyPolicy: authoringv2.ConstructionExpandEmptyExclude, RelatedRecordColumnID: "observation-id",
	}
	authored := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "related-step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind: authoringv2.ConstructionOperationRelatedExpand, RelatedExpand: &relatedExpand,
		},
		Outputs: []authoringv2.StageColumn{},
	}}}
	constructionJSON, err := json.Marshal(authored)
	if err != nil {
		t.Fatal(err)
	}
	proposalJSON := append([]byte(`{"candidateConstruction":`), constructionJSON...)
	proposalJSON = append(proposalJSON, '}')
	var proposalRequest loomapi.ConstructionProposalRequest
	if err := json.Unmarshal(proposalJSON, &proposalRequest); err != nil {
		t.Fatal(err)
	}
	if len(proposalRequest.CandidateConstruction.Steps) != 1 {
		t.Fatalf("typed proposal request construction = %#v", proposalRequest.CandidateConstruction.Steps)
	}
	requestDTO := proposalRequest.CandidateConstruction.Steps[0].Operation.RelatedExpand
	if requestDTO == nil || requestDTO.ContributorSource == nil ||
		requestDTO.ContributorSource.Cardinality != "many" ||
		requestDTO.ContributorSource.RepeatedBoundaries == nil ||
		len(*requestDTO.ContributorSource.RepeatedBoundaries) != len(boundaries) {
		t.Fatalf("typed RelatedExpand proposal request dropped repeated source metadata: %#v", requestDTO)
	}
	request, err := directAuthoringJSON[authoringv2.Construction](proposalRequest.CandidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	restoredSource := request.Steps[0].Operation.RelatedExpand.ContributorSource
	if restoredSource == nil || !reflect.DeepEqual(restoredSource.RepeatedBoundaries, boundaries) {
		t.Fatalf("typed RelatedExpand proposal request round trip dropped repeated boundaries: %#v", restoredSource)
	}
}
