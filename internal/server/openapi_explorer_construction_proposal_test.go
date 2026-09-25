package server

import (
	"bytes"
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
		Route: route, ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches},
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
		Registry:    compilerTestRegistry{},
		ScopeDigest: recipeScopeDigest,
		QueryRows:   func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
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
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil)
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
			var storedReceipt explorer.CompilationReceipt
			if err := json.Unmarshal(rawReceipt, &storedReceipt); err != nil {
				t.Fatalf("decode related-source receipt as the Arango store does: %v", err)
			}
			if _, err := compileValidatedReceiptResolution(ctx, recipeEngine, &storedReceipt, bindings); err != nil {
				t.Fatalf("validate the JSON round-tripped related-source receipt before preview: %v", err)
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
			if relatedEmission.ConstructionID != step.ID || relatedEmission.SourceResourceType != "Observation" || relatedEmission.SourcePath != "status" || relatedEmission.CandidateID != candidate.ID || relatedEmission.OccurrenceID != candidate.NodeID ||
				relatedEmission.NodeID != candidate.NodeID || len(relatedEmission.AuthoredColumns) != 0 || len(relatedEmission.InputColumns) != 0 ||
				relatedEmission.Cardinality != "many" || relatedEmission.Shape != "array" || relatedEmission.Lossless || relatedEmission.MLReady || relatedEmission.StructuralSuitability != "requires-review" || relatedEmission.Filterable || relatedEmission.Chartable ||
				!containsOutputContractString(relatedEmission.LossReasons, "RELATED_SOURCE_AUTHORIZED_MATCHES_ONLY") || !containsOutputContractString(relatedEmission.LossReasons, tableShapeMLReadinessUnassessed) {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("related-source list metadata is not conservative: %#v", *relatedEmission)
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
				relatedContract.Cardinality != "many" || relatedContract.Shape != "array" || relatedContract.Lossless || relatedContract.MLReady || len(relatedContract.AuthoredColumns) != 0 || len(relatedContract.InputColumns) != 0 {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("public contract lost related-source identity or list shape: %#v", relatedContract)
			}
			row := make(map[string]any, len(receipt.EmittedColumns))
			previewColumns := make([]string, 0, len(receipt.EmittedColumns))
			for _, emitted := range receipt.EmittedColumns {
				row[emitted.PublicColumn] = nil
				previewColumns = append(previewColumns, emitted.PublicColumn)
			}
			row["c_patient"] = "patient-1"
			row["observation_statuses"] = []any{"final", "amended"}
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
		if string(operation.Kind) == "RELATED_SOURCE" {
			if !operation.Supported {
				t.Fatalf("hidden root identity should support related-source addition: %#v", operation)
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
