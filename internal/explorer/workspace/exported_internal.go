package workspace

import "github.com/calypr/loom/internal/explorer/capability"

type StoredTableShape = storedTableShape
type StoredTableReshape = storedTableReshape
type StoredPivotConstruction = storedPivotConstruction
type StoredPivotCategory = storedPivotCategory
type StoredUnpivotConstruction = storedUnpivotConstruction
type StoredUnpivotInput = storedUnpivotInput
type StoredDerivedConstruction = storedDerivedConstruction
type StoredArithmeticOperand = storedArithmeticOperand

func (d Document) ValidateAllowArchive() error { return d.validate(true) }

func (o ConstructionOperation) InputColumnIDs() []string { return o.inputColumnIDs() }

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
