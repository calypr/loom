package authoringv2

import (
	"encoding/json"

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

func findStageColumnByID(columns []StageColumn, id string) (StageColumn, bool) {
	return workspace.FindStageColumnByID(columns, id)
}

func normalizeConstructionOutputOrder(document *Document) {
	workspace.NormalizeConstructionOutputOrder(document)
}

func cloneDocumentForConstructionChange(document Document) Document {
	return workspace.CloneDocumentForConstructionChange(document)
}

func orderedStageIDs(columns []StageColumn) []string { return workspace.OrderedStageIDs(columns) }

func rebuildStageColumns(step ConstructionStep, input []StageColumn, authoredSourceIDs map[string]struct{}) ([]StageColumn, error) {
	return workspace.RebuildStageColumns(step, input, authoredSourceIDs)
}

func validateConstructionLiteral(literal ConstructionLiteral) error {
	return workspace.ValidateConstructionLiteral(literal)
}

func migrateMissingRowsBeforeCurrent(workspaceValue Workspace) Workspace {
	return workspace.MigrateMissingRowsBeforeCurrent(workspaceValue)
}

func constructionSourceColumnsWithChildrenAndProjections(columns []Column, projections []ConstructionSourceProjection, referencedIDs []string, declared []StageColumn) ([]StageColumn, error) {
	return workspace.ConstructionSourceColumnsWithChildrenAndProjections(columns, projections, referencedIDs, declared)
}

func testCatalog() CatalogSnapshot {
	nodes := []CatalogNode{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "encounter", ResourceType: "Encounter", RowRootEligible: true},
	}
	edges := []CatalogEdge{
		{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"},
		{ID: "encounter-self", FromNodeID: "encounter", ToNodeID: "encounter", Label: "revisits"},
	}
	return CatalogSnapshot{
		APIVersion: APIVersion, Kind: CatalogKind, Project: "p", ExplorerID: "e",
		SourceGeneration: "g", AuthorizationScopeDigest: "scope", SnapshotToken: "sha256:snapshot",
		Complete: true, Nodes: nodes, Edges: edges,
		Candidates: []CatalogCandidate{
			{ID: "patient-id", NodeID: "patient", FieldPath: "id", Cardinality: "optional_one", Label: "ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: testFieldConstructionChoice("sha256:snapshot", "patient-id", "patient", "Patient", "id", "optional_one", capability.ProjectionScalar)},
			{ID: "encounter-id", NodeID: "encounter", FieldPath: "id", Cardinality: "optional_one", Label: "ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: testFieldConstructionChoice("sha256:snapshot", "encounter-id", "encounter", "Encounter", "id", "optional_one", capability.ProjectionScalar)},
		},
		RoutePolicy: RoutePolicy{Unbounded: true, AllowRepeatedEdges: true, AllowSelfLoops: true},
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

func migrateStoredDocumentFixture(document Document) (Document, error) {
	if document.ArchiveRetiredShape == nil {
		return StartDocumentConstruction(document)
	}
	raw, err := json.Marshal(constructionWorkspace(document))
	if err != nil {
		return Document{}, err
	}
	result, err := MigrateStoredTableShapeWorkspace(raw, CatalogSnapshot{})
	if err != nil {
		return Document{}, err
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

func validCorrelatedLookup() *LookupSource {
	return &LookupSource{
		Binding: &fhirbinding.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"},
		Key:     &fhirbinding.CorrelatedKey{System: "urn:study:A", Code: "shared"},
	}
}

func dependencyWorkspace(documents ...Document) Workspace {
	w := Workspace{APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Dependencies"}, Documents: documents}
	for index, document := range documents {
		w.Tabs = append(w.Tabs, Tab{ID: "tab_" + document.Output.ID, Title: document.Output.Title, OutputID: document.Output.ID, Order: index, Visible: true})
	}
	return w
}

func workspaceOutputCombineDocument(id string, workspaceOutputID string) Document {
	document := terminalCombineDocument()
	document.Output.ID = id
	document.Output.Title = id
	document.Construction.Steps[0].Inputs[0] = ConstructionInputRef{Kind: ConstructionInputWorkspaceOutput, OutputID: workspaceOutputID}
	return document
}

func terminalCombineDocument() Document {
	document := workspaceDocument("training")
	document.Columns = nil
	document.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{{
		ID: "join_labs",
		Inputs: []ConstructionInputRef{
			{Kind: ConstructionInputTableRevision, TableID: "project:1:patients", RevisionID: "patients-r4", OutputID: "patients"},
			{Kind: ConstructionInputTableRevision, TableID: "project:1:labs", RevisionID: "labs-r9", OutputID: "labs"},
		},
		Operation: ConstructionOperation{Kind: ConstructionOperationCombine, Combine: &ConstructionCombine{
			Kind: ConstructionCombineKeyJoin,
			Keys: []ConstructionCombineKey{{LeftColumnID: "patient_key", RightColumnID: "subject_key"}},
			Projections: []ConstructionCombineProjection{
				{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "patient_key"},
				{OutputColumnID: "lab_value", InputIndex: 1, InputColumnID: "result_value"},
			},
			JoinType: ConstructionCombineLeftJoin, RightMatchPolicy: ConstructionCombinePreserveAllMatches,
		}},
		Outputs: []StageColumn{
			{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "integer"},
			{ID: "lab_value", Name: "lab_value", Label: "Lab value", Type: "decimal", Nullable: true},
		},
	}}}
	return document
}
