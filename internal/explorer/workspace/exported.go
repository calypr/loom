package workspace

import (
	"encoding/json"

	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/explorer/capability"
)

func StrictDecode(raw []byte, target any) error { return strictDecode(raw, target) }

func CanonicalTransformPath(value string) string { return canonicalTransformPath(value) }

func CloneRowDefinition(rows RowDefinition) (RowDefinition, error) { return cloneRowDefinition(rows) }

func CloneConstruction(construction *Construction) (*Construction, error) {
	return cloneConstruction(construction)
}

func EmptyID(value string) bool { return emptyID(value) }

func ValidFrameForm(form capability.ConstructionChoiceForm) bool { return validFrameForm(form) }

func SupportsExplicitGroupRowValueColumn(column Column) bool {
	return supportsExplicitGroupRowValueColumn(column)
}

func RemoveConstructionColumnValues(document *Document, columnID string) {
	removeConstructionColumnValues(document, columnID)
}

func RemoveExplicitGroupRowValue(document *Document, columnID string) {
	removeExplicitGroupRowValue(document, columnID)
}

func PopulateConstructionColumn(document *Document, column Column, policy ConstructionRowValuePolicy) error {
	return populateConstructionColumn(document, column, policy)
}

func PopulateExplicitGroupRowValue(document *Document, column Column, policy ConstructionRowValuePolicy) error {
	return populateExplicitGroupRowValue(document, column, policy)
}

func ValidateColumnValueTransformationForCatalog(document Document, catalog CatalogSnapshot, column Column, source ColumnSource, transformation columntransform.ValueTransformation) error {
	return validateColumnValueTransformationForCatalog(document, catalog, column, source, transformation)
}

func StagedSourceColumnID(outputID, commandID string, commandIndex int, commandType string, identity ...any) string {
	return stagedSourceColumnID(outputID, commandID, commandIndex, commandType, identity...)
}

func CatalogNodeIDForOccurrence(route RouteNode, occurrenceID string, catalog CatalogSnapshot) (string, bool) {
	return catalogNodeIDForOccurrence(route, occurrenceID, catalog)
}

func RoutePathUsesCatalogEdge(route RouteNode, occurrenceID, edgeID string, catalog CatalogSnapshot) (bool, error) {
	return routePathUsesCatalogEdge(route, occurrenceID, edgeID, catalog)
}

func RouteSubtreeUsesCatalogEdge(route RouteNode, nodeID, edgeID string, catalog CatalogSnapshot) (bool, error) {
	return routeSubtreeUsesCatalogEdge(route, nodeID, edgeID, catalog)
}

func ResolveCatalogRouteEdge(catalog CatalogSnapshot, parentNodeID string, parent, child RouteNode) (CatalogEdge, error) {
	return resolveCatalogRouteEdge(catalog, parentNodeID, parent, child)
}

func MarshalStrict(value any) (json.RawMessage, error) {
	return json.Marshal(value)
}

type StoredTableShape = storedTableShape

type StoredTableReshape = storedTableReshape

type StoredPivotConstruction = storedPivotConstruction

type StoredPivotCategory = storedPivotCategory

type StoredUnpivotConstruction = storedUnpivotConstruction

type StoredUnpivotInput = storedUnpivotInput

type StoredDerivedConstruction = storedDerivedConstruction

type StoredArithmeticOperand = storedArithmeticOperand

func FindStageColumnByID(columns []StageColumn, id string) (StageColumn, bool) {
	return findStageColumnByID(columns, id)
}

func NormalizeConstructionOutputOrder(document *Document) {
	normalizeConstructionOutputOrder(document)
}

func CloneDocumentForConstructionChange(document Document) Document {
	return cloneDocumentForConstructionChange(document)
}

func OrderedStageIDs(columns []StageColumn) []string { return orderedStageIDs(columns) }

func RebuildStageColumns(step ConstructionStep, input []StageColumn, authoredSourceIDs map[string]struct{}) ([]StageColumn, error) {
	return rebuildStageColumns(step, input, authoredSourceIDs)
}

func ValidateConstructionLiteral(literal ConstructionLiteral) error {
	return validateConstructionLiteral(literal)
}

func MigrateMissingRowsBeforeCurrent(workspace Workspace) Workspace {
	return migrateMissingRowsBeforeCurrent(workspace)
}

func FrameIdentityID(source capability.SemanticFrameFamily, route []capability.ConstructionRouteStep) string {
	return frameIdentityID(source, route)
}

func ConstructionSourceColumnsWithChildrenAndProjections(columns []Column, projections []ConstructionSourceProjection, referencedIDs []string, declared []StageColumn) ([]StageColumn, error) {
	return constructionSourceColumnsWithChildrenAndProjections(columns, projections, referencedIDs, declared)
}

func (d Document) ValidateAllowArchive() error { return d.validate(true) }

func (o ConstructionOperation) InputColumnIDs() []string { return o.inputColumnIDs() }
