package explorerapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	receiptoutput "github.com/calypr/loom/internal/explorer/receipt/output"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/gofiber/fiber/v3"
)

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
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = ws.MigrateLegacyContributors(workspace, ws.CatalogFromCapability(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, ws.CatalogFromCapability(snapshot, "custom")).NormalizePresentationOrders()
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
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
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
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, engine, service, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	outputs := make([]ws.StageColumn, 0, len(document.Columns)+1)
	for _, column := range document.Columns {
		outputs = append(outputs, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	outputs = append(outputs, ws.StageColumn{ID: "observation_id", Name: "observation_id", Label: "FHIR resource ID", Type: "string"})
	authoredRoute := make([]capability.ConstructionRouteStep, 0, len(choice.Route))
	for _, hop := range choice.Route {
		authoredRoute = append(authoredRoute, capability.ConstructionRouteStep{
			EdgeID: hop.EdgeId, FromNodeID: hop.FromNodeId, ToNodeID: hop.ToNodeId,
			FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
			Relationship: hop.Relationship, StorageDirection: string(hop.StorageDirection), MatchMode: string(hop.MatchMode),
		})
	}
	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "expand_observations", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedExpand, RelatedExpand: &ws.ConstructionRelatedExpand{
			AnchorColumnID: "_key", ChoiceID: choice.ChoiceId, TargetNodeID: choice.TargetNodeId, TargetResourceType: choice.TargetResourceType,
			Route: authoredRoute, ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches},
			EmptyPolicy: ws.ConstructionExpandEmptyExclude, RelatedRecordColumnID: "observation_id",
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
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0] = document
	catalogSnapshot := ws.CatalogFromCapability(snapshot, "patient-specimen-related-group")
	workspace, err = ws.MigrateLegacyContributors(workspace, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, catalogSnapshot).NormalizePresentationOrders()
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
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
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
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		ConstructionSourceStage: func(ctx context.Context, request ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
			return receiptapi.CompileConstructionSourceStage(ctx, request, recipeEngine)
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection","includeGroupChoices":true}`,
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
	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: stepID, Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: stepID, MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
			Keys: []ws.ConstructionGroupKey{
				{OutputColumnID: "patient_id_group", FieldSource: &ws.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: patientIDChoice.FieldSource.ChoiceID}},
				{OutputColumnID: "specimen_type", FieldSource: &ws.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: codingChoice.FieldSource.ChoiceID}},
			},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
		}},
		Outputs: []ws.StageColumn{
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
	var resolved ws.Construction
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
		ws.CurrentSemanticsVersion, snapshot.Token, digest, *proposal.ProposalId,
	))
	if applyGroup.StatusCode != http.StatusOK {
		t.Fatalf("apply related Group status=%d body=%s", applyGroup.StatusCode, applyGroup.Body)
	}
	updated, err := service.Get(context.Background(), "project-a", "patient-specimen-related-group")
	if err != nil {
		t.Fatal(err)
	}
	acceptedWorkspace, err := ws.DecodeWorkspace(updated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DraftVersion != 2 || len(acceptedWorkspace.Documents) != 1 || acceptedWorkspace.Documents[0].Construction == nil {
		t.Fatalf("related Group was not durably applied: version=%d workspace=%#v", updated.DraftVersion, acceptedWorkspace)
	}
	rowValueCapabilitiesResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection","includeGroupChoices":true,"groupStepId":%q}`,
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
	rowValueCandidate.Steps = append([]ws.ConstructionStep(nil), rowValueCandidate.Steps...)
	rowValueCandidate.Steps[0].RowValues = []ws.ConstructionRowValue{{
		FieldSource:    &ws.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: specimenIDChoice.FieldSource.ChoiceID},
		OutputColumnID: "specimen_ids", Policy: ws.ConstructionRowValueAll,
	}}
	rowValueCandidate.Steps[0].Outputs = append(append([]ws.StageColumn(nil), rowValueCandidate.Steps[0].Outputs...),
		ws.StageColumn{ID: "specimen_ids", Name: "specimen_ids", Label: "Specimen IDs", Type: "array"},
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
	var resolvedRowValue ws.Construction
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

	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
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
		workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, ws.Column{
			Column: field.name, Label: field.label, LogicalType: field.logicalType, OccurrenceID: "base",
			Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: field.path, ProjectionMode: map[capability.ProjectionMode]string{capability.ProjectionScalar: "VALUE", capability.ProjectionFirst: "FIRST"}[field.projection]}},
		})
		snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
			ID: columnID, NodeID: "n_patient", ResourceType: "Patient", FieldPath: field.path, Label: field.label,
			LogicalType: field.logicalType, Cardinality: field.cardinality,
			ProjectionModes: []capability.ProjectionMode{field.projection},
		})
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	stageColumns := make([]ws.StageColumn, 0, len(document.Columns)+1)
	for _, column := range document.Columns {
		stageColumns = append(stageColumns, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	related := ws.ConstructionRelatedSource{
		AnchorColumnID: "_key", ChoiceID: choice.ChoiceID, SourceOccurrenceID: candidate.NodeID,
		Source: ws.ConstructionRelatedFieldSource{
			Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID, NodeID: candidate.NodeID,
			ResourceType: candidate.ResourceType, Path: candidate.FieldPath, Cardinality: candidate.Cardinality, LogicalType: candidate.LogicalType,
		},
		Route: route, ContributorRule: ws.ConstructionRelatedContributorRule{
			Policy: ws.ConstructionRelatedAllMatches,
			Predicate: &ws.ContributorPredicate{
				CandidateID: candidate.ID, Operator: ws.ContributorEquals,
				Value: &ws.ContributorValue{Kind: ws.ContributorString, String: stringPointer("final")},
			},
		},
		Form: capability.ConstructionChoiceAll, OutputColumnID: "observation-statuses",
	}
	stageColumns = append(stageColumns, ws.StageColumn{ID: related.OutputColumnID, Name: "observation_statuses", Label: "Observation statuses", Type: candidate.LogicalType})
	step := ws.ConstructionStep{
		ID: "step_related_observation_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedSource, RelatedSource: &related},
		Outputs:   stageColumns,
	}
	candidateConstruction := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{step}}
	candidateJSON, err := json.Marshal(candidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	var decodedConstruction ws.Construction
	if err := json.Unmarshal(candidateJSON, &decodedConstruction); err != nil || len(decodedConstruction.Steps) != 1 || decodedConstruction.Steps[0].Operation.RelatedSource == nil {
		t.Fatalf("related-source candidate JSON did not round trip: err=%v json=%s decoded=%#v", err, candidateJSON, decodedConstruction)
	}
	decodedPredicate := decodedConstruction.Steps[0].Operation.RelatedSource.ContributorRule.Predicate
	if decodedPredicate == nil || decodedPredicate.CandidateID != candidate.ID || decodedPredicate.Operator != ws.ContributorEquals ||
		decodedPredicate.Value == nil || decodedPredicate.Value.String == nil || *decodedPredicate.Value.String != "final" {
		t.Fatalf("related-source proposal predicate changed during API JSON round trip: %#v", decodedPredicate)
	}
	workspace.Documents[0] = document
	workspace, err = ws.MigrateLegacyContributors(workspace, ws.CatalogFromCapability(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, ws.CatalogFromCapability(snapshot, "custom")).NormalizePresentationOrders()
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
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
		ScopeDigest:  reciperuntime.ScopeDigest,
		QueryRows:    func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		RootPageRows: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	config := lifecycle.Config{
		SelectionMembersCollection: "loom_explorer_selection_members",
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
			var storedReceipt receiptmodel.CompilationReceipt
			if err := json.Unmarshal(storedRaw, &storedReceipt); err != nil {
				t.Fatalf("decode related-source receipt as the Arango store does: %v", err)
			}
			resolved, err := receiptapi.CompileValidatedResolution(ctx, recipeEngine, &storedReceipt, bindings)
			if err != nil {
				t.Fatalf("validate the JSON round-tripped related-source receipt before preview: %v", err)
			}
			if _, err := recipeEngine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 100}, func(map[string]any) error { return nil }); err != nil {
				t.Fatalf("preview the JSON round-tripped related-source receipt through root paging: %v", err)
			}
			var relatedEmission *viewer.EmittedColumn
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
			if related.RowValuePolicy == ws.ConstructionRowValueOne {
				wantCardinality, wantShape = "optional_one", "scalar"
				wantFilterable, wantChartable = true, true
				relatedValue = "final"
			}
			if relatedEmission.ConstructionID != step.ID || relatedEmission.SourceResourceType != "Observation" || relatedEmission.SourcePath != "status" || relatedEmission.CandidateID != candidate.ID || relatedEmission.OccurrenceID != candidate.NodeID ||
				relatedEmission.NodeID != candidate.NodeID || len(relatedEmission.AuthoredColumns) != 0 || len(relatedEmission.InputColumns) != 0 ||
				relatedEmission.Cardinality != wantCardinality || relatedEmission.Shape != wantShape || relatedEmission.Lossless || relatedEmission.MLReady || relatedEmission.StructuralSuitability != "requires-review" || relatedEmission.Filterable != wantFilterable || relatedEmission.Chartable != wantChartable ||
				!slices.Contains(relatedEmission.LossReasons, "RELATED_SOURCE_AUTHORIZED_MATCHES_ONLY") || !slices.Contains(relatedEmission.LossReasons, receiptoutput.TableShapeMLReadinessUnassessed) {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("related-source output metadata does not match row value policy %q: %#v", related.RowValuePolicy, *relatedEmission)
			}
			contracts, err := receiptmodel.DecodePublicOutputContracts(receipt.PublicOutputContract)
			if err != nil || len(contracts.Outputs) != 1 {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("decode related-source output contract: outputs=%d err=%v", len(contracts.Outputs), err)
			}
			var relatedContract *receiptmodel.PublicOutputColumn
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

	related.RowValuePolicy = ws.ConstructionRowValueOne
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

func TestRelatedExpandContributorTypedWireSourcesPreserveRepeatedBoundaries(t *testing.T) {
	boundaries := []capability.RepeatedBoundary{
		{Path: "category[]", MaxItems: 4},
		{Path: "category[].coding[]", MaxItems: 8},
	}
	source := ws.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: "candidate-code", NodeID: "node-observation",
		ResourceType: "Observation", Path: "category[].coding[].code", Cardinality: "many", LogicalType: "string",
		RepeatedBoundaries: boundaries,
	}

	response, err := directAuthoringJSON[loomapi.RelatedExpandContributorChoiceSearchResponse](
		construction.RelatedExpandContributorChoiceSearchResponse{
			SnapshotToken: "snapshot", DraftVersion: 2, DraftDigest: "draft-digest", OutputID: "output",
			StageID: "stage", RouteChoiceID: "route-choice", Complete: true,
			Choices: []construction.RelatedExpandContributorChoice{{
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

	relatedExpand := ws.ConstructionRelatedExpand{
		AnchorColumnID: "anchor", ChoiceID: "route-choice", TargetNodeID: "node-observation",
		TargetResourceType: "Observation", ContributorRule: ws.ConstructionRelatedContributorRule{
			Policy: ws.ConstructionRelatedAllMatches,
		},
		ContributorSource: &source, ContributorChoiceID: "field-choice",
		EmptyPolicy: ws.ConstructionExpandEmptyExclude, RelatedRecordColumnID: "observation-id",
	}
	authored := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "related-step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{
			Kind: ws.ConstructionOperationRelatedExpand, RelatedExpand: &relatedExpand,
		},
		Outputs: []ws.StageColumn{},
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
	request, err := directAuthoringJSON[ws.Construction](proposalRequest.CandidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	restoredSource := request.Steps[0].Operation.RelatedExpand.ContributorSource
	if restoredSource == nil || !reflect.DeepEqual(restoredSource.RepeatedBoundaries, boundaries) {
		t.Fatalf("typed RelatedExpand proposal request round trip dropped repeated boundaries: %#v", restoredSource)
	}
}
