package authoringv2

import (
	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/workspace"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func cloneWorkspace(value ws.Workspace) (ws.Workspace, error) {
	return workspace.CloneWorkspace(value)
}

func commandGeneratedID(prefix string, values ...any) string {
	return workspace.CommandGeneratedID(prefix, values...)
}

func catalogNode(catalog ws.CatalogSnapshot, id string) (ws.CatalogNode, bool) {
	return workspace.CatalogNodeByID(catalog, id)
}

func catalogEdge(catalog ws.CatalogSnapshot, id string) (ws.CatalogEdge, bool) {
	return workspace.CatalogEdgeByID(catalog, id)
}

func routePath(route ws.RouteNode, occurrenceID string) ([]ws.RouteNode, bool) {
	return workspace.RoutePath(route, occurrenceID)
}

func catalogNodeForConstructionRoot(catalog ws.CatalogSnapshot, resourceType string) (ws.CatalogNode, bool) {
	return workspace.CatalogNodeForConstructionRoot(catalog, resourceType)
}

func findRoute(route *ws.RouteNode, occurrenceID string) *ws.RouteNode {
	return workspace.FindRoute(route, occurrenceID)
}

func qualifySemanticPath(ownerPath, valuePath string) string {
	return workspace.QualifySemanticPath(ownerPath, valuePath)
}

func inferredSourceLogicalType(document ws.Document, catalog ws.CatalogSnapshot, occurrenceID string, source ws.ColumnSource, fallback string) string {
	return workspace.InferredSourceLogicalType(document, catalog, occurrenceID, source, fallback)
}

func strictDecode(raw []byte, target any) error { return workspace.StrictDecode(raw, target) }

func canonicalTransformPath(value string) string { return workspace.CanonicalTransformPath(value) }

func cloneRowDefinition(rows ws.RowDefinition) (ws.RowDefinition, error) {
	return workspace.CloneRowDefinition(rows)
}

func cloneConstruction(construction *ws.Construction) (*ws.Construction, error) {
	return workspace.CloneConstruction(construction)
}

func emptyID(value string) bool { return workspace.EmptyID(value) }

func validFrameForm(form capability.ConstructionChoiceForm) bool {
	return workspace.ValidFrameForm(form)
}

func supportsExplicitGroupRowValueColumn(column ws.Column) bool {
	return workspace.SupportsExplicitGroupRowValueColumn(column)
}

func removeConstructionColumnValues(document *ws.Document, columnID string) {
	workspace.RemoveConstructionColumnValues(document, columnID)
}

func removeExplicitGroupRowValue(document *ws.Document, columnID string) {
	workspace.RemoveExplicitGroupRowValue(document, columnID)
}

func populateConstructionColumn(document *ws.Document, column ws.Column, policy ws.ConstructionRowValuePolicy) error {
	return workspace.PopulateConstructionColumn(document, column, policy)
}

func populateExplicitGroupRowValue(document *ws.Document, column ws.Column, policy ws.ConstructionRowValuePolicy) error {
	return workspace.PopulateExplicitGroupRowValue(document, column, policy)
}

func validateColumnValueTransformationForCatalog(document ws.Document, catalog ws.CatalogSnapshot, column ws.Column, source ws.ColumnSource, transformation columntransform.ValueTransformation) error {
	return workspace.ValidateColumnValueTransformationForCatalog(document, catalog, column, source, transformation)
}

func stagedSourceColumnID(outputID, commandID string, commandIndex int, commandType string, identity ...any) string {
	return workspace.StagedSourceColumnID(outputID, commandID, commandIndex, commandType, identity...)
}

func catalogNodeIDForOccurrence(route ws.RouteNode, occurrenceID string, catalog ws.CatalogSnapshot) (string, bool) {
	return workspace.CatalogNodeIDForOccurrence(route, occurrenceID, catalog)
}

func routePathUsesCatalogEdge(route ws.RouteNode, occurrenceID, edgeID string, catalog ws.CatalogSnapshot) (bool, error) {
	return workspace.RoutePathUsesCatalogEdge(route, occurrenceID, edgeID, catalog)
}

func routeSubtreeUsesCatalogEdge(route ws.RouteNode, nodeID, edgeID string, catalog ws.CatalogSnapshot) (bool, error) {
	return workspace.RouteSubtreeUsesCatalogEdge(route, nodeID, edgeID, catalog)
}

func resolveCatalogRouteEdge(catalog ws.CatalogSnapshot, parentNodeID string, parent, child ws.RouteNode) (ws.CatalogEdge, error) {
	return workspace.ResolveCatalogRouteEdge(catalog, parentNodeID, parent, child)
}

func validateEditableSource(document ws.Document, catalog ws.CatalogSnapshot, occurrenceID string, source ws.ColumnSource) error {
	return workspace.ValidateEditableSource(document, catalog, occurrenceID, source)
}

func validateEditableSourcePolicy(document ws.Document, catalog ws.CatalogSnapshot, occurrenceID string, source ws.ColumnSource, allowImportedRecipe bool) error {
	return workspace.ValidateEditableSourcePolicy(document, catalog, occurrenceID, source, allowImportedRecipe)
}

func ValidateContributorForCatalog(document ws.Document, catalog ws.CatalogSnapshot, occurrenceID string, source ws.ColumnSource, predicate ws.ContributorPredicate) error {
	return workspace.ValidateContributorForCatalog(document, catalog, occurrenceID, source, predicate)
}
