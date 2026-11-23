package authoringv2

import (
	"encoding/json"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/workspace"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
)

type storedTableShape = workspace.StoredTableShape
type storedTableReshape = workspace.StoredTableReshape
type storedPivotConstruction = workspace.StoredPivotConstruction
type storedPivotCategory = workspace.StoredPivotCategory
type storedUnpivotConstruction = workspace.StoredUnpivotConstruction
type storedUnpivotInput = workspace.StoredUnpivotInput
type storedDerivedConstruction = workspace.StoredDerivedConstruction
type storedArithmeticOperand = workspace.StoredArithmeticOperand

const (
	tableShapeSemanticsVersion              = 9
	explicitRowsSemanticsVersion            = 8
	contributorSemanticsVersion             = 4
	aggregateTemporalPolicySemanticsVersion = 9
)

func frameIdentityID(source capability.SemanticFrameFamily, route []capability.ConstructionRouteStep) string {
	return workspace.FrameIdentityID(source, route)
}

func findStageColumnByID(columns []ws.StageColumn, id string) (ws.StageColumn, bool) {
	return workspace.FindStageColumnByID(columns, id)
}

func normalizeConstructionOutputOrder(document *ws.Document) {
	workspace.NormalizeConstructionOutputOrder(document)
}

func cloneDocumentForConstructionChange(document ws.Document) ws.Document {
	return workspace.CloneDocumentForConstructionChange(document)
}

func orderedStageIDs(columns []ws.StageColumn) []string { return workspace.OrderedStageIDs(columns) }

func rebuildStageColumns(step ws.ConstructionStep, input []ws.StageColumn, authoredSourceIDs map[string]struct{}) ([]ws.StageColumn, error) {
	return workspace.RebuildStageColumns(step, input, authoredSourceIDs)
}

func validateConstructionLiteral(literal ws.ConstructionLiteral) error {
	return workspace.ValidateConstructionLiteral(literal)
}

func migrateMissingRowsBeforeCurrent(workspaceValue ws.Workspace) ws.Workspace {
	return workspace.MigrateMissingRowsBeforeCurrent(workspaceValue)
}

func constructionSourceColumnsWithChildrenAndProjections(columns []ws.Column, projections []ws.ConstructionSourceProjection, referencedIDs []string, declared []ws.StageColumn) ([]ws.StageColumn, error) {
	return workspace.ConstructionSourceColumnsWithChildrenAndProjections(columns, projections, referencedIDs, declared)
}

func testCatalog() ws.CatalogSnapshot {
	nodes := []ws.CatalogNode{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "encounter", ResourceType: "Encounter", RowRootEligible: true},
	}
	edges := []ws.CatalogEdge{
		{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"},
		{ID: "encounter-self", FromNodeID: "encounter", ToNodeID: "encounter", Label: "revisits"},
	}
	return ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "p", ExplorerID: "e",
		SourceGeneration: "g", AuthorizationScopeDigest: "scope", SnapshotToken: "sha256:snapshot",
		Complete: true, Nodes: nodes, Edges: edges,
		Candidates: []ws.CatalogCandidate{
			{ID: "patient-id", NodeID: "patient", FieldPath: "id", Cardinality: "optional_one", Label: "ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: testFieldConstructionChoice("sha256:snapshot", "patient-id", "patient", "Patient", "id", "optional_one", capability.ProjectionScalar)},
			{ID: "encounter-id", NodeID: "encounter", FieldPath: "id", Cardinality: "optional_one", Label: "ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: testFieldConstructionChoice("sha256:snapshot", "encounter-id", "encounter", "Encounter", "id", "optional_one", capability.ProjectionScalar)},
		},
		RoutePolicy: ws.RoutePolicy{Unbounded: true, AllowRepeatedEdges: true, AllowSelfLoops: true},
	}
}

func testFieldConstructionChoice(snapshotToken, candidateID, nodeID, resourceType, path, cardinality string, projection capability.ProjectionMode) *capability.ConstructionChoice {
	choice, err := capability.NewFieldConstructionChoice(snapshotToken, capability.Candidate{
		ID: candidateID, NodeID: nodeID, ResourceType: resourceType, FieldPath: path,
		Cardinality: cardinality, LogicalType: "string", ProjectionModes: []capability.ProjectionMode{projection},
	})
	if err != nil {
		panic(err)
	}
	return &choice
}

func migrateStoredDocumentFixture(document ws.Document) (ws.Document, error) {
	if document.ArchiveRetiredShape == nil {
		return ws.StartDocumentConstruction(document)
	}
	raw, err := json.Marshal(constructionWorkspace(document))
	if err != nil {
		return ws.Document{}, err
	}
	result, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		return ws.Document{}, err
	}
	return result.Workspace.Documents[0], nil
}

func cloneStoredShapeFixture(shape *storedTableShape) (*storedTableShape, error) {
	raw, err := json.Marshal(shape)
	if err != nil {
		return nil, err
	}
	var out *storedTableShape
	err = json.Unmarshal(raw, &out)
	return out, err
}

func validCorrelatedLookup() *ws.LookupSource {
	return &ws.LookupSource{
		Binding: &fhirbinding.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"},
		Key:     &fhirbinding.CorrelatedKey{System: "urn:study:A", Code: "shared"},
	}
}

func dependencyWorkspace(documents ...ws.Document) ws.Workspace {
	w := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Dependencies"}, Documents: documents}
	for index, document := range documents {
		w.Tabs = append(w.Tabs, ws.Tab{ID: "tab_" + document.Output.ID, Title: document.Output.Title, OutputID: document.Output.ID, Order: index, Visible: true})
	}
	return w
}

func workspaceOutputCombineDocument(id string, workspaceOutputID string) ws.Document {
	document := terminalCombineDocument()
	document.Output.ID = id
	document.Output.Title = id
	document.Construction.Steps[0].Inputs[0] = ws.ConstructionInputRef{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: workspaceOutputID}
	return document
}

func terminalCombineDocument() ws.Document {
	document := workspaceDocument("training")
	document.Columns = nil
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "join_labs",
		Inputs: []ws.ConstructionInputRef{
			{Kind: ws.ConstructionInputTableRevision, TableID: "project:1:patients", RevisionID: "patients-r4", OutputID: "patients"},
			{Kind: ws.ConstructionInputTableRevision, TableID: "project:1:labs", RevisionID: "labs-r9", OutputID: "labs"},
		},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCombine, Combine: &ws.ConstructionCombine{
			Kind: ws.ConstructionCombineKeyJoin,
			Keys: []ws.ConstructionCombineKey{{LeftColumnID: "patient_key", RightColumnID: "subject_key"}},
			Projections: []ws.ConstructionCombineProjection{
				{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "patient_key"},
				{OutputColumnID: "lab_value", InputIndex: 1, InputColumnID: "result_value"},
			},
			JoinType: ws.ConstructionCombineLeftJoin, RightMatchPolicy: ws.ConstructionCombinePreserveAllMatches,
		}},
		Outputs: []ws.StageColumn{
			{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "integer"},
			{ID: "lab_value", Name: "lab_value", Label: "Lab value", Type: "decimal", Nullable: true},
		},
	}}}
	return document
}
