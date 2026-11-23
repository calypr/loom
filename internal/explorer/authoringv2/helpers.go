package authoringv2

import (
	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/workspace"
)

func cloneWorkspace(value Workspace) (Workspace, error) {
	return workspace.CloneWorkspace(value)
}

func commandGeneratedID(prefix string, values ...any) string {
	return workspace.CommandGeneratedID(prefix, values...)
}

func catalogNode(catalog CatalogSnapshot, id string) (CatalogNode, bool) {
	return workspace.CatalogNodeByID(catalog, id)
}

func catalogEdge(catalog CatalogSnapshot, id string) (CatalogEdge, bool) {
	return workspace.CatalogEdgeByID(catalog, id)
}

func routePath(route RouteNode, occurrenceID string) ([]RouteNode, bool) {
	return workspace.RoutePath(route, occurrenceID)
}

func catalogNodeForConstructionRoot(catalog CatalogSnapshot, resourceType string) (CatalogNode, bool) {
	return workspace.CatalogNodeForConstructionRoot(catalog, resourceType)
}

func findRoute(route *RouteNode, occurrenceID string) *RouteNode {
	return workspace.FindRoute(route, occurrenceID)
}

func qualifySemanticPath(ownerPath, valuePath string) string {
	return workspace.QualifySemanticPath(ownerPath, valuePath)
}

func inferredSourceLogicalType(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource, fallback string) string {
	return workspace.InferredSourceLogicalType(document, catalog, occurrenceID, source, fallback)
}

func strictDecode(raw []byte, target any) error { return workspace.StrictDecode(raw, target) }

func canonicalTransformPath(value string) string { return workspace.CanonicalTransformPath(value) }

func cloneRowDefinition(rows RowDefinition) (RowDefinition, error) {
	return workspace.CloneRowDefinition(rows)
}

func cloneConstruction(construction *Construction) (*Construction, error) {
	return workspace.CloneConstruction(construction)
}

func emptyID(value string) bool { return workspace.EmptyID(value) }

func validFrameForm(form capability.ConstructionChoiceForm) bool {
	return workspace.ValidFrameForm(form)
}

func supportsExplicitGroupRowValueColumn(column Column) bool {
	return workspace.SupportsExplicitGroupRowValueColumn(column)
}

func removeConstructionColumnValues(document *Document, columnID string) {
	workspace.RemoveConstructionColumnValues(document, columnID)
}

func removeExplicitGroupRowValue(document *Document, columnID string) {
	workspace.RemoveExplicitGroupRowValue(document, columnID)
}

func populateConstructionColumn(document *Document, column Column, policy ConstructionRowValuePolicy) error {
	return workspace.PopulateConstructionColumn(document, column, policy)
}

func populateExplicitGroupRowValue(document *Document, column Column, policy ConstructionRowValuePolicy) error {
	return workspace.PopulateExplicitGroupRowValue(document, column, policy)
}

func validateColumnValueTransformationForCatalog(document Document, catalog CatalogSnapshot, column Column, source ColumnSource, transformation columntransform.ValueTransformation) error {
	return workspace.ValidateColumnValueTransformationForCatalog(document, catalog, column, source, transformation)
}

func stagedSourceColumnID(outputID, commandID string, commandIndex int, commandType string, identity ...any) string {
	return workspace.StagedSourceColumnID(outputID, commandID, commandIndex, commandType, identity...)
}

func catalogNodeIDForOccurrence(route RouteNode, occurrenceID string, catalog CatalogSnapshot) (string, bool) {
	return workspace.CatalogNodeIDForOccurrence(route, occurrenceID, catalog)
}

func routePathUsesCatalogEdge(route RouteNode, occurrenceID, edgeID string, catalog CatalogSnapshot) (bool, error) {
	return workspace.RoutePathUsesCatalogEdge(route, occurrenceID, edgeID, catalog)
}

func routeSubtreeUsesCatalogEdge(route RouteNode, nodeID, edgeID string, catalog CatalogSnapshot) (bool, error) {
	return workspace.RouteSubtreeUsesCatalogEdge(route, nodeID, edgeID, catalog)
}

func resolveCatalogRouteEdge(catalog CatalogSnapshot, parentNodeID string, parent, child RouteNode) (CatalogEdge, error) {
	return workspace.ResolveCatalogRouteEdge(catalog, parentNodeID, parent, child)
}

func validateEditableSource(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource) error {
	return workspace.ValidateEditableSource(document, catalog, occurrenceID, source)
}

func validateEditableSourcePolicy(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource, allowImportedRecipe bool) error {
	return workspace.ValidateEditableSourcePolicy(document, catalog, occurrenceID, source, allowImportedRecipe)
}

func ValidateContributorForCatalog(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource, predicate ContributorPredicate) error {
	return workspace.ValidateContributorForCatalog(document, catalog, occurrenceID, source, predicate)
}
