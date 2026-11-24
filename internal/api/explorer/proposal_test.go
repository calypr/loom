package explorerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/semantic"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/gofiber/fiber/v3"
)

func TestConstructionProposalPreviewBudgetExtendsOnlyPivot(t *testing.T) {
	pivotContext, pivotTimeout := constructionProposalPreviewBudget(context.Background(), ws.Construction{
		Steps: []ws.ConstructionStep{{Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot}}},
	})
	if pivotTimeout != 30*time.Second || executionarango.PreviewQueryMaxRuntime(pivotContext) != executionarango.PivotCategoryScanTimeout {
		t.Fatalf("Pivot proposal preview budget = %s / query max runtime %s, want 30s", pivotTimeout, executionarango.PreviewQueryMaxRuntime(pivotContext))
	}

	parentDeadline := time.Now().Add(2 * time.Second)
	parentContext, cancel := context.WithDeadline(context.Background(), parentDeadline)
	defer cancel()
	boundedContext, _ := constructionProposalPreviewBudget(parentContext, ws.Construction{
		Steps: []ws.ConstructionStep{{Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot}}},
	})
	if got, ok := boundedContext.Deadline(); !ok || !got.Equal(parentDeadline) {
		t.Fatalf("Pivot proposal context deadline = %s (present %t), want caller deadline %s", got, ok, parentDeadline)
	}

	ordinaryContext, ordinaryTimeout := constructionProposalPreviewBudget(context.Background(), ws.Construction{
		Steps: []ws.ConstructionStep{{Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter}}},
	})
	if ordinaryTimeout != executionarango.ExplorerPreviewTimeout || executionarango.PreviewQueryMaxRuntime(ordinaryContext) != executionarango.ExplorerPreviewTimeout {
		t.Fatalf("ordinary proposal preview budget = %s / query max runtime %s, want 10s", ordinaryTimeout, executionarango.PreviewQueryMaxRuntime(ordinaryContext))
	}
}

func TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	sibling := workspace.Documents[0]
	sibling.Output = ws.Output{ID: "sibling_patients", Title: "Sibling patients", RowLabel: "Sibling patients"}
	sibling.Columns = append([]ws.Column(nil), sibling.Columns...)
	sibling.Columns[0].ColumnID = "sibling_patient_id"
	sibling.Columns[0].Column = "sibling_patient_id"
	sibling.Columns[0].Label = "Sibling patient ID"
	workspace.Documents = append(workspace.Documents, sibling)
	workspace.Tabs = append(workspace.Tabs, ws.Tab{ID: "sibling-patients-tab", Title: "Sibling patients", OutputID: "sibling_patients", Order: 1, Visible: true})
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]ws.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	document.Construction.Steps = []ws.ConstructionStep{{
		ID: "only_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{
			Kind:   ws.ConstructionOperationFilter,
			Filter: &ws.ConstructionFilter{ColumnID: columns[0].ID, Operator: ws.ConstructionFilterExists},
		},
		Outputs: columns,
	}}
	workspace.Documents[0] = document
	workspace, err = ws.MigrateLegacyContributors(workspace, ws.CatalogFromCapability(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, ws.CatalogFromCapability(snapshot, "custom")).NormalizePresentationOrders()
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
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Patients",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorerfixtures.NewService(store)
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
			return receiptapi.Compile(ctx, request, nil, recipeEngine, store, nil, nil)
		},
		ScanCategories: func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
			categoryScanCalls++
			return dataframeexecution.CategoryScanResult{}, nil
		},
		PreviewReceipt: func(previewCtx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			proposalPreviewCalls++
			proposalPreviewQueryRuntime = executionarango.PreviewQueryMaxRuntime(previewCtx)
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
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config, store)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"

	capabilitiesHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection","includeGroupChoices":true}`,
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

	missingWorkspaceOutputID := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID:     "only_step",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputWorkspaceOutput}},
		Operation: ws.ConstructionOperation{
			Kind:   ws.ConstructionOperationFilter,
			Filter: &ws.ConstructionFilter{ColumnID: columns[0].ID, Operator: ws.ConstructionFilterExists},
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

	unsupportedTableInput := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "only_step",
		Inputs: []ws.ConstructionInputRef{{
			Kind: ws.ConstructionInputTableRevision, TableID: "table-patients", RevisionID: "patients-r1", OutputID: "patients",
		}},
		Operation: ws.ConstructionOperation{
			Kind:   ws.ConstructionOperationFilter,
			Filter: &ws.ConstructionFilter{ColumnID: columns[0].ID, Operator: ws.ConstructionFilterExists},
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
	if proposalPreviewQueryRuntime != executionarango.ExplorerPreviewTimeout {
		t.Fatalf("ordinary proposal inline preview max runtime = %s, want %s", proposalPreviewQueryRuntime, executionarango.ExplorerPreviewTimeout)
	}
	if proposal.Preview.ReceiptId != *proposal.ProposalId || proposal.Preview.OutputId != "patients" {
		t.Fatalf("preview is not bound to the proposal receipt/output: %#v", proposal.Preview)
	}
	if proposal.Preview.PartialValidation == nil || !*proposal.Preview.PartialValidation {
		t.Fatalf("proposal preview lost the compiler partial-validation marker: %#v", proposal.Preview)
	}

	applyHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-last-step-removal","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, digest, *proposal.ProposalId,
	))
	if applyHTTP.StatusCode != http.StatusOK {
		t.Fatalf("apply remove-only construction status=%d body=%s", applyHTTP.StatusCode, applyHTTP.Body)
	}
	updated, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := ws.DecodeWorkspace(updated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DraftVersion != 2 || accepted.Documents[0].Construction == nil || len(accepted.Documents[0].Construction.Steps) != 0 {
		t.Fatalf("remove-only proposal was not atomically applied: draft=%#v workspace=%#v", updated, accepted)
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

	familyReference := semantic.FamilyReference{
		BindingID: "binding_specimen_type", SourceKind: semantic.SourceRetained,
		ResourceType: "Specimen", SourcePath: "Specimen.type.coding[]", System: "urn:example:specimen-type",
	}
	familyReference.FamilyID = semantic.FamilyID(familyReference)
	family := semantic.Family{
		FamilyReference: familyReference,
		Source:          semantic.Source{Type: "Specimen", Path: "Specimen.type.coding[]"},
		KeySelector:     "type.coding[]",
		Value: semantic.Value{
			Selector: "type.coding[].display", Type: "string",
			FallbackSelectors: []string{"type.coding[].code"}, FallbackOnBlankString: true,
		},
		LogicalType: "string", SchemaVersion: semantic.ObservationSchemaVersion,
		RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion), Completeness: semantic.CompletenessComplete,
		Status: "SUPPORTED", Population: 12,
	}
	buildID := semantic.BuildID("project-a", "generation-a")

	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].RootResourceType = "Specimen"
	workspace.Documents[0].Route = ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Specimen"}
	workspace.Documents[0].Output = ws.Output{ID: "specimens", Title: "Specimens", RowLabel: "Specimens"}
	workspace.Documents[0].Columns = []ws.Column{
		{Column: "specimen_id", Label: "Specimen ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
			Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
		{Column: "subject_reference", Label: "Subject reference", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
			Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "subject.reference", ProjectionMode: "VALUE"}}},
	}
	workspace.Tabs[0].OutputID = "specimens"
	workspace.Tabs[0].Title = "Specimens"
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0] = document
	catalogSnapshot := ws.CatalogFromCapability(snapshot, "specimen-explorer")
	workspace, err = ws.MigrateLegacyContributors(workspace, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, catalogSnapshot).NormalizePresentationOrders()
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
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "specimen-explorer", Title: "Specimens",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorerfixtures.NewService(store)
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
	var resolvedFamilyReferences []semantic.FamilyReference
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
		SemanticInventory: func(context.Context, semantic.PageOptions) (semantic.Page, error) {
			return semantic.Page{
				State:    semantic.Complete,
				Build:    semantic.Build{BuildID: buildID, State: semantic.Complete},
				Families: []semantic.Family{family},
			}, nil
		},
		ResolveSemanticInventorySelections: func(_ context.Context, options semantic.ResolveOptions) (semantic.ResolveResult, error) {
			resolvedFamilyReferences = append([]semantic.FamilyReference(nil), options.FamilyReferences...)
			return semantic.ResolveResult{
				State:    semantic.Complete,
				Build:    semantic.Build{BuildID: buildID, State: semantic.Complete},
				Families: []semantic.Family{family},
			}, nil
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, recipeEngine, store, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config, store)
	basePath := "/api/v1/projects/project-a/explorers/specimen-explorer/authoring/v2"
	capabilitiesResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"specimens","stageId":"source_projection","includeGroupChoices":true}`,
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
	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
		{
			ID: stepID, Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
				ConstructionID: stepID, MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
				Keys: []ws.ConstructionGroupKey{
					{InputColumnID: subjectColumnID, OutputColumnID: "subject_reference_group"},
					{OutputColumnID: "specimen_type_group", SemanticCategory: &ws.ConstructionGroupSemanticCategory{Kind: "CHOICE", ChoiceID: capabilities.SemanticGroupChoices[0].ChoiceID}},
				},
				Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
			}},
			Outputs: []ws.StageColumn{
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
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.RootResourceType = "Observation"
	document.Route = ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Observation"}
	document.Rows = ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{
		OccurrenceID: ws.RootOccurrenceID, ScopePath: "component[].code.coding[]",
		EmptyCollectionPolicy: ws.EmptyCollectionPreserveParent,
	}}
	document.Columns = []ws.Column{}
	document.Output = ws.Output{ID: "observations", Title: "Observations", RowLabel: "Observations"}
	document, err = ws.StartDocumentConstruction(document)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0] = document
	workspace.Tabs[0].OutputID = "observations"
	workspace.Tabs[0].Title = "Observations"
	catalogSnapshot := ws.CatalogFromCapability(snapshot, "observation-explorer")
	workspace, err = ws.MigrateLegacyContributors(workspace, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, catalogSnapshot).NormalizePresentationOrders()
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
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "observation-explorer", Title: "Observations",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorerfixtures.NewService(store)
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
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		RowChoicePlanner: rowChoices,
		ConstructionSourceStage: func(ctx context.Context, request ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
			return receiptapi.CompileConstructionSourceStage(ctx, request, recipeEngine)
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, recipeEngine, store, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config, store)
	basePath := "/api/v1/projects/project-a/explorers/observation-explorer/authoring/v2"
	capabilitiesResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"observations","stageId":"source_projection","includeGroupChoices":true}`,
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
		codingChoice.SourceRole != "CURRENT_ITEM" || codingChoice.OccurrenceID != ws.RootOccurrenceID ||
		codingChoice.ResourceType != "Coding" || codingChoice.OwnerType != "Observation" || codingChoice.Path != "component[].code.coding[]" {
		t.Fatalf("current Coding group choice = %#v, want exact Coding item under Observation owner", capabilities.GroupFieldChoices)
	}
	if scalarChoice == nil || scalarChoice.FieldSource.ChoiceID == "" || scalarChoice.SourceStage != "source_projection" ||
		scalarChoice.SourceShape == "" || scalarChoice.SourceRole != "ROOT" || scalarChoice.OccurrenceID != ws.RootOccurrenceID ||
		scalarChoice.ResourceType != "Observation" || scalarChoice.OwnerType != "Observation" || scalarChoice.CandidateID != "c_observation_status" {
		t.Fatalf("scalar Group field choice = %#v, want exact observed root scalar", capabilities.GroupFieldChoices)
	}

	stepID := "group_current_coding"
	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: stepID, Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: stepID, MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
			Keys: []ws.ConstructionGroupKey{{
				OutputColumnID: "status_group",
				FieldSource:    &ws.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: scalarChoice.FieldSource.ChoiceID},
			}, {
				OutputColumnID: "coding_identity",
				FieldSource:    &ws.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: codingChoice.FieldSource.ChoiceID},
			}},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
		}},
		Outputs: []ws.StageColumn{
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
	var resolved ws.Construction
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
		binding.OwnerResourceType != "Observation" || binding.OccurrenceID != ws.RootOccurrenceID ||
		binding.CanonicalPath != "component[].code.coding[]" || binding.CandidateID != "" {
		t.Fatalf("resolved Coding field binding = %#v, want exact current Coding identity", binding)
	}

	applyInitialGroup := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-current-coding-group","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"observations","proposalId":%q}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, digest, *proposal.ProposalID,
	))
	if applyInitialGroup.StatusCode != http.StatusOK {
		t.Fatalf("apply mixed-source Group status=%d body=%s", applyInitialGroup.StatusCode, applyInitialGroup.Body)
	}
	updated, err := service.Get(context.Background(), "project-a", "observation-explorer")
	if err != nil {
		t.Fatal(err)
	}
	acceptedWorkspace, err := ws.DecodeWorkspace(updated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DraftVersion != 2 || len(acceptedWorkspace.Documents) != 1 || acceptedWorkspace.Documents[0].Construction == nil ||
		len(acceptedWorkspace.Documents[0].Construction.Steps) != 1 || len(acceptedWorkspace.Documents[0].Columns) != 0 {
		t.Fatalf("initial Group proposal was not durably applied: version=%d workspace=%#v", updated.DraftVersion, acceptedWorkspace)
	}
	acceptedConstruction := *acceptedWorkspace.Documents[0].Construction
	rowValueCapabilitiesResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"observations","stageId":"source_projection","includeGroupChoices":true,"groupStepId":%q}`,
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
		componentValueChoice.OwnerID != ws.RootOccurrenceID || componentValueChoice.OwnerType != "Observation" ||
		componentValueChoice.Path != "valueQuantity.value" || componentValueChoice.Boundary != "component[]" {
		t.Fatalf("post-Group ancestor row-value choice = %#v, want exact component[] source from the original Group input", rowValueCapabilities.GroupRowValueChoices)
	}
	if len(acceptedConstruction.Steps) != 1 {
		t.Fatalf("accepted Group construction has unexpected steps: %#v", acceptedConstruction)
	}
	rowValueCandidate := acceptedConstruction
	rowValueCandidate.Steps = append([]ws.ConstructionStep(nil), acceptedConstruction.Steps...)
	rowValueCandidate.Steps[0].RowValues = []ws.ConstructionRowValue{{
		FieldSource:    &ws.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: componentValueChoice.FieldSource.ChoiceID},
		OutputColumnID: "component_values", Policy: ws.ConstructionRowValueAll,
	}}
	rowValueCandidate.Steps[0].Outputs = append(append([]ws.StageColumn(nil), rowValueCandidate.Steps[0].Outputs...),
		ws.StageColumn{ID: "component_values", Name: "component_values", Label: "Component values", Type: "array"},
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
	var rowValueResolved ws.Construction
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
		rowBinding.OccurrenceID != ws.RootOccurrenceID || rowBinding.ResourceType != "ObservationComponent" ||
		rowBinding.OwnerResourceType != "Observation" || rowBinding.CandidateID != "c_observation_component_value" ||
		rowBinding.CanonicalPath != "valueQuantity.value" || rowBinding.BoundarySelector != "component[]" {
		t.Fatalf("resolved post-Group ancestor binding = %#v, want exact component[] BINDING", rowBinding)
	}
	applyRowValueGroup := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-current-coding-group-add-columns","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"observations","proposalId":%q}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, updated.DraftVersion, updated.DraftDigest, *rowValueProposal.ProposalID,
	))
	if applyRowValueGroup.StatusCode != http.StatusOK {
		t.Fatalf("apply post-Group Add-columns proposal status=%d body=%s", applyRowValueGroup.StatusCode, applyRowValueGroup.Body)
	}
	finalExplorer, err := service.Get(context.Background(), "project-a", "observation-explorer")
	if err != nil {
		t.Fatal(err)
	}
	finalWorkspace, err := ws.DecodeWorkspace(finalExplorer.DraftConfig)
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

func TestConstructionProposalCurrentCodingGroupSpecimenFocusObservationHTTP(t *testing.T) {
	const (
		outputID     = "out_5b1_coding_group_fixture"
		explorerID   = "specimen-coding-group-fixture"
		observation  = "focus-observation"
		codingPath   = "valueCodeableConcept.coding[]"
		draftVersion = 86
	)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Nodes = []capability.Node{
		{ID: "n_specimen", ResourceType: "Specimen", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 12},
		{ID: "n_observation", ResourceType: "Observation", Populated: true, DocumentCount: 12},
	}
	snapshot.Edges = []capability.Edge{{
		ID: "e_specimen_focus_observation", FromNodeID: "n_specimen", ToNodeID: "n_observation",
		SourceResourceType: "Specimen", TargetResourceType: "Observation", Label: "focus_Specimen", StorageDirection: "INBOUND",
	}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "c_observation_value_coding", NodeID: "n_observation", ResourceType: "Observation",
		FieldPath: codingPath + ".code", Label: "Observation value coding", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries:  []capability.RepeatedBoundary{{Path: codingPath, MaxItems: 8}},
		ProjectionModes:     []capability.ProjectionMode{capability.ProjectionArray},
		SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true,
	}}

	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.RootResourceType = "Specimen"
	document.Route = ws.RouteNode{
		OccurrenceID: ws.RootOccurrenceID, ResourceType: "Specimen",
		Children: []ws.RouteNode{{OccurrenceID: observation, ResourceType: "Observation", Relationship: "focus_Specimen"}},
	}
	document.Rows = ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{
		OccurrenceID: observation, ScopePath: codingPath, EmptyCollectionPolicy: ws.EmptyCollectionPreserveParent,
	}}
	document.Columns = []ws.Column{}
	document.Output = ws.Output{ID: outputID, Title: "Specimen coding groups", RowLabel: "Specimens"}
	document, err = ws.StartDocumentConstruction(document)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0] = document
	workspace.Tabs[0].OutputID = outputID
	workspace.Tabs[0].Title = "Specimen coding groups"
	catalogSnapshot := ws.CatalogFromCapability(snapshot, explorerID)
	workspace, err = ws.MigrateLegacyContributors(workspace, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, catalogSnapshot).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate fresh Specimen-to-Observation Coding workspace: %v", err)
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
		Project: "project-a", ExplorerID: explorerID, Title: "Specimen coding groups",
		DraftConfig: draft, DraftVersion: draftVersion, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{}, ScopeDigest: reciperuntime.ScopeDigest, RootPageRows: 100,
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	rowChoices, err := lifecycle.NewSchemaRowChoiceResolver(index)
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
		RowChoicePlanner: rowChoices,
		ConstructionSourceStage: func(ctx context.Context, request ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
			return receiptapi.CompileConstructionSourceStage(ctx, request, recipeEngine)
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, recipeEngine, store, nil, nil)
		},
		PreviewReceipt: func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil || receipt.ConstructionProposal == nil || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != outputID {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("expected the exact current Coding proposal receipt and output binding")
			}
			var sourceStage receiptmodel.ReceiptConstructionStage
			foundSourceStage := false
			for _, stage := range receipt.ConstructionStages[outputID] {
				if stage.ID == "source_projection" {
					sourceStage = stage
					foundSourceStage = true
					break
				}
			}
			if !foundSourceStage || sourceStage.RowIdentityColumn != "__loom_expansion_identity" || sourceStage.GroupFieldSourceContext == nil ||
				sourceStage.GroupFieldSourceContext.SourceStageID != "source_projection" ||
				sourceStage.GroupFieldSourceContext.SourceIdentityColumn != "__loom_expansion_identity" ||
				sourceStage.GroupFieldSourceContext.SourceExpansion == nil ||
				sourceStage.GroupFieldSourceContext.SourceExpansion.OwnerResourceType != "Observation" ||
				sourceStage.GroupFieldSourceContext.SourceExpansion.Selector != codingPath {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("compiled source projection lost the exact expanded Observation Coding context: found=%t stage=%#v", foundSourceStage, sourceStage)
			}
			resolved, err := receiptapi.CompileValidatedResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("validate proposal receipt before native preview: %w", err)
			}
			return recipeEngine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: outputID, Limit: 100}, visit)
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config, store)
	basePath := "/api/v1/projects/project-a/explorers/" + explorerID + "/authoring/v2"
	capabilitiesResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":%q,"stageId":"source_projection","includeGroupChoices":true}`,
		snapshot.Token, draftVersion, digest, outputID,
	))
	if capabilitiesResponse.StatusCode != http.StatusOK {
		t.Fatalf("Specimen focus Observation Coding capabilities status=%d body=%s", capabilitiesResponse.StatusCode, capabilitiesResponse.Body)
	}
	var capabilities struct {
		SelectedStage struct {
			ID          string `json:"id"`
			RowIdentity string `json:"rowIdentityColumn"`
			GroupSource *struct {
				SourceStage     string `json:"sourceStageId"`
				SourceShape     string `json:"sourceShapeId"`
				SourceIdentity  string `json:"sourceIdentityColumn"`
				SourceExpansion *struct {
					OwnerResourceType string `json:"ownerResourceType"`
					Selector          string `json:"selector"`
				} `json:"sourceExpansion"`
			} `json:"groupFieldSourceContext"`
		} `json:"selectedStage"`
		GroupFieldChoices []struct {
			Kind          string `json:"kind"`
			ValueKind     string `json:"valueKind"`
			SourceStage   string `json:"sourceStageId"`
			SourceShape   string `json:"sourceShapeId"`
			SourceRole    string `json:"sourceRole"`
			OccurrenceID  string `json:"occurrenceId"`
			ResourceType  string `json:"resourceType"`
			OwnerType     string `json:"ownerResourceType"`
			CanonicalPath string `json:"canonicalPath"`
			FieldSource   struct {
				ChoiceID string `json:"choiceId"`
			} `json:"fieldSource"`
		} `json:"groupFieldChoices"`
	}
	if err := json.Unmarshal([]byte(capabilitiesResponse.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.SelectedStage.ID != "source_projection" || capabilities.SelectedStage.RowIdentity != "__loom_expansion_identity" ||
		capabilities.SelectedStage.GroupSource == nil || capabilities.SelectedStage.GroupSource.SourceStage != "source_projection" ||
		capabilities.SelectedStage.GroupSource.SourceShape == "" || capabilities.SelectedStage.GroupSource.SourceIdentity != "__loom_expansion_identity" ||
		capabilities.SelectedStage.GroupSource.SourceExpansion == nil ||
		capabilities.SelectedStage.GroupSource.SourceExpansion.OwnerResourceType != "Observation" ||
		capabilities.SelectedStage.GroupSource.SourceExpansion.Selector != codingPath {
		t.Fatalf("source projection lost the exact expanded Observation Coding identity before proposal: %#v", capabilities.SelectedStage)
	}
	var codingChoice *struct {
		Kind          string `json:"kind"`
		ValueKind     string `json:"valueKind"`
		SourceStage   string `json:"sourceStageId"`
		SourceShape   string `json:"sourceShapeId"`
		SourceRole    string `json:"sourceRole"`
		OccurrenceID  string `json:"occurrenceId"`
		ResourceType  string `json:"resourceType"`
		OwnerType     string `json:"ownerResourceType"`
		CanonicalPath string `json:"canonicalPath"`
		FieldSource   struct {
			ChoiceID string `json:"choiceId"`
		} `json:"fieldSource"`
	}
	for i := range capabilities.GroupFieldChoices {
		choice := &capabilities.GroupFieldChoices[i]
		if choice.ValueKind == "CODING_IDENTITY" && choice.SourceRole == "CURRENT_ITEM" && choice.CanonicalPath == codingPath {
			codingChoice = choice
			break
		}
	}
	if codingChoice == nil || codingChoice.Kind != "FIELD_SOURCE" || codingChoice.FieldSource.ChoiceID == "" ||
		codingChoice.SourceStage != "source_projection" || codingChoice.SourceShape == "" ||
		codingChoice.OccurrenceID != observation || codingChoice.ResourceType != "Coding" || codingChoice.OwnerType != "Observation" {
		t.Fatalf("current Coding field choice lost Specimen -> focus_Specimen INBOUND -> Observation identity: %#v", capabilities.GroupFieldChoices)
	}

	stepID := "group_specimen_observation_coding"
	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: stepID, Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: stepID, MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
			Keys:       []ws.ConstructionGroupKey{{OutputColumnID: "coding_identity", FieldSource: &ws.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: codingChoice.FieldSource.ChoiceID}}},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
		}},
		Outputs: []ws.StageColumn{
			{ID: "coding_identity", Name: "coding_identity", Label: "Observation coding", Type: "string"},
			{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
		},
	}}}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	proposalBody := fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":%q,"changedStepId":%q,"candidateConstruction":%s,"limit":100}`,
		snapshot.Token, draftVersion, digest, outputID, stepID, candidateJSON,
	)
	proposalResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", proposalBody)
	if proposalResponse.StatusCode != http.StatusOK {
		t.Fatalf("exact current Coding Group proposal status=%d body=%s", proposalResponse.StatusCode, proposalResponse.Body)
	}
	var proposal loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(proposalResponse.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalId == nil || proposal.PreviewStatus != loomapi.ConstructionProposalResponsePreviewStatusREADY || proposal.Preview == nil {
		t.Fatalf("exact current Coding Group proposal did not complete native preview: %#v", proposal)
	}
	resolvedJSON, err := json.Marshal(proposal.CandidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	var resolved ws.Construction
	if err := json.Unmarshal(resolvedJSON, &resolved); err != nil {
		t.Fatal(err)
	}
	if len(resolved.Steps) != 1 || resolved.Steps[0].Operation.Group == nil || len(resolved.Steps[0].Operation.Group.Keys) != 1 {
		t.Fatalf("resolved Specimen Observation Coding Group = %#v", resolved)
	}
	binding := resolved.Steps[0].Operation.Group.Keys[0].FieldSource
	if binding == nil || binding.Kind != "BINDING" || binding.ValueKind != "CODING_IDENTITY" ||
		binding.SourceStageID != "source_projection" || binding.SourceShapeID != codingChoice.SourceShape ||
		binding.SourceRole != "CURRENT_ITEM" || binding.ResourceType != "Coding" || binding.OwnerResourceType != "Observation" ||
		binding.OccurrenceID != observation || binding.CanonicalPath != codingPath || binding.CandidateID != "" {
		t.Fatalf("resolved current Coding binding = %#v", binding)
	}
}

func stringPointer(value string) *string { return &value }
