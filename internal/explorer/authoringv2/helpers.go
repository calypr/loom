package authoringv2

import (
	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/explorer/capability"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func cloneWorkspace(value ws.Workspace) (ws.Workspace, error) {
	return ws.CloneWorkspace(value)
}

func commandGeneratedID(prefix string, values ...any) string {
	return ws.CommandGeneratedID(prefix, values...)
}

func catalogNode(catalog ws.CatalogSnapshot, id string) (ws.CatalogNode, bool) {
	return ws.CatalogNodeByID(catalog, id)
}

func catalogEdge(catalog ws.CatalogSnapshot, id string) (ws.CatalogEdge, bool) {
	return ws.CatalogEdgeByID(catalog, id)
}

func routePath(route ws.RouteNode, occurrenceID string) ([]ws.RouteNode, bool) {
	return ws.RoutePath(route, occurrenceID)
}

func catalogNodeForConstructionRoot(catalog ws.CatalogSnapshot, resourceType string) (ws.CatalogNode, bool) {
	return ws.CatalogNodeForConstructionRoot(catalog, resourceType)
}

func findRoute(route *ws.RouteNode, occurrenceID string) *ws.RouteNode {
	return ws.FindRoute(route, occurrenceID)
}

func inferredSourceLogicalType(document ws.Document, catalog ws.CatalogSnapshot, occurrenceID string, source ws.ColumnSource, fallback string) string {
	return ws.InferredSourceLogicalType(document, catalog, occurrenceID, source, fallback)
}

func strictDecode(raw []byte, target any) error { return ws.StrictDecode(raw, target) }

func cloneRowDefinition(rows ws.RowDefinition) (ws.RowDefinition, error) {
	return ws.CloneRowDefinition(rows)
}

func cloneConstruction(construction *ws.Construction) (*ws.Construction, error) {
	return ws.CloneConstruction(construction)
}

func emptyID(value string) bool { return ws.EmptyID(value) }

func validFrameForm(form capability.ConstructionChoiceForm) bool {
	return ws.ValidFrameForm(form)
}

func supportsExplicitGroupRowValueColumn(column ws.Column) bool {
	return ws.SupportsExplicitGroupRowValueColumn(column)
}

func removeConstructionColumnValues(document *ws.Document, columnID string) {
	ws.RemoveConstructionColumnValues(document, columnID)
}

func removeExplicitGroupRowValue(document *ws.Document, columnID string) {
	ws.RemoveExplicitGroupRowValue(document, columnID)
}

func populateConstructionColumn(document *ws.Document, column ws.Column, policy ws.ConstructionRowValuePolicy) error {
	return ws.PopulateConstructionColumn(document, column, policy)
}

func populateExplicitGroupRowValue(document *ws.Document, column ws.Column, policy ws.ConstructionRowValuePolicy) error {
	return ws.PopulateExplicitGroupRowValue(document, column, policy)
}

func validateColumnValueTransformationForCatalog(document ws.Document, catalog ws.CatalogSnapshot, column ws.Column, source ws.ColumnSource, transformation columntransform.ValueTransformation) error {
	return ws.ValidateColumnValueTransformationForCatalog(document, catalog, column, source, transformation)
}

func stagedSourceColumnID(outputID, commandID string, commandIndex int, commandType string, identity ...any) string {
	return ws.StagedSourceColumnID(outputID, commandID, commandIndex, commandType, identity...)
}

func catalogNodeIDForOccurrence(route ws.RouteNode, occurrenceID string, catalog ws.CatalogSnapshot) (string, bool) {
	return ws.CatalogNodeIDForOccurrence(route, occurrenceID, catalog)
}

func routePathUsesCatalogEdge(route ws.RouteNode, occurrenceID, edgeID string, catalog ws.CatalogSnapshot) (bool, error) {
	return ws.RoutePathUsesCatalogEdge(route, occurrenceID, edgeID, catalog)
}

func routeSubtreeUsesCatalogEdge(route ws.RouteNode, nodeID, edgeID string, catalog ws.CatalogSnapshot) (bool, error) {
	return ws.RouteSubtreeUsesCatalogEdge(route, nodeID, edgeID, catalog)
}

func resolveCatalogRouteEdge(catalog ws.CatalogSnapshot, parentNodeID string, parent, child ws.RouteNode) (ws.CatalogEdge, error) {
	return ws.ResolveCatalogRouteEdge(catalog, parentNodeID, parent, child)
}

func validateEditableSource(document ws.Document, catalog ws.CatalogSnapshot, occurrenceID string, source ws.ColumnSource) error {
	return ws.ValidateEditableSource(document, catalog, occurrenceID, source)
}

func validateEditableSourcePolicy(document ws.Document, catalog ws.CatalogSnapshot, occurrenceID string, source ws.ColumnSource, allowImportedRecipe bool) error {
	return ws.ValidateEditableSourcePolicy(document, catalog, occurrenceID, source, allowImportedRecipe)
}

func ValidateContributorForCatalog(document ws.Document, catalog ws.CatalogSnapshot, occurrenceID string, source ws.ColumnSource, predicate ws.ContributorPredicate) error {
	return ws.ValidateContributorForCatalog(document, catalog, occurrenceID, source, predicate)
}
