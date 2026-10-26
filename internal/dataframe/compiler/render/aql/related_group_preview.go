package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type relatedGroupPreviewShape struct {
	firstHop           ir.PhysicalTraversal
	groupStageIndex    int
	groupFilterIndex   int
	groupIdentityName  string
	groupIdentityKey   string
	groupKeyName       string
	rootCollection     string
	rootResourceType   string
	targetResourceType string
	groupKeyType       string
}

type relatedGroupPreviewFrontier struct {
	query                      string
	bindVars                   map[string]any
	selectedGroupsVariable     string
	boundedVariable            string
	rootKeysVariable           string
	groupIdentitiesVariable    string
	groupIdentityColumnBindKey string
}

type previewGroupIdentityFilter struct {
	StageIndex              int
	ColumnBindKey           string
	SelectedGroupIdentities string
	BoundedVariable         string
}

// relatedGroupPreviewShapeFor accepts only a direct scoped-root expansion
// chain whose first related FHIR ID is the sole COUNT_ROWS Group key. The
// preview can then select complete Group identities through the exact first
// hop and reuse the canonical construction stages for every selected root.
func relatedGroupPreviewShapeFor(plan ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, options physicalRenderOptions) (relatedGroupPreviewShape, bool) {
	if plan.Engine == ir.PhysicalEngineClickHouse || sequence == nil || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow || sequence.CellTraceReturn != nil ||
		sequence.RowLineageReturn != nil || sequence.OutputAuthResourcePathBindKey != "" || len(sequence.Stages) < 2 ||
		options.rootIndexHint != "" || options.disableRootIndex || options.omitTerminalRowSort || options.omitTerminalReturn ||
		options.terminalReturnVariable != "" || options.terminalProjectionColumn != "" ||
		options.projectionPresenceMarkerColumn != "" || len(options.preserveProjectionPresenceNames) != 0 ||
		options.twoScanPivotPreview || options.dynamicPivotPreview || options.pivotGroupTupleFilter != nil {
		return relatedGroupPreviewShape{}, false
	}
	if limit, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int); !ok || limit <= 0 {
		return relatedGroupPreviewShape{}, false
	}

	rootCollection, rootResourceType, ok := relatedGroupPreviewRootSource(plan)
	if !ok {
		return relatedGroupPreviewShape{}, false
	}
	groupIndex := -1
	for index, stage := range sequence.Stages {
		if stage.Kind != ir.PhysicalStageGroupOp {
			continue
		}
		if groupIndex >= 0 {
			return relatedGroupPreviewShape{}, false
		}
		groupIndex = index
	}
	if groupIndex < 1 {
		return relatedGroupPreviewShape{}, false
	}
	group := sequence.Stages[groupIndex].Group
	if group == nil {
		return relatedGroupPreviewShape{}, false
	}

	first := sequence.Stages[0]
	if first.ID == "" || first.InputStageID != sequence.SourceStageID || first.Kind != ir.PhysicalStageRelatedExpandOp ||
		first.RelatedExpand == nil || first.Filter != nil || len(first.DerivedLets) != 0 {
		return relatedGroupPreviewShape{}, false
	}
	related := first.RelatedExpand
	if related.EmptyPolicy != ir.PhysicalUnnestPreserveParent || related.AnchorKind != "root" ||
		related.AnchorColumnID != "_key" || related.AnchorResourceType != rootResourceType ||
		related.RelatedRecordColumnID == "" || len(related.Route) != 1 {
		return relatedGroupPreviewShape{}, false
	}
	firstHop, ok := relatedGroupPreviewExactFirstHop(plan, first)
	if !ok {
		return relatedGroupPreviewShape{}, false
	}
	groupKeyColumn, ok := relatedGroupPreviewStageColumnByID(first.OutputColumns, related.RelatedRecordColumnID)
	if !ok || groupKeyColumn.Internal || groupKeyColumn.Kind != "string" ||
		(groupKeyColumn.Cardinality != "required_one" && groupKeyColumn.Cardinality != "optional_one") ||
		!relatedGroupPreviewProjectsResourceID(first, groupKeyColumn) {
		return relatedGroupPreviewShape{}, false
	}
	rootKeyColumn := ir.PhysicalStageColumn{}
	needsRootKey := false
	if contributorColumn := group.RootContributorInputColumn; contributorColumn != "" {
		if contributorColumn != "_key" || group.RootContributorInputMany {
			return relatedGroupPreviewShape{}, false
		}
		var found bool
		rootKeyColumn, found = relatedGroupPreviewRootContributorColumn(first.OutputColumns, rootResourceType)
		if !found || rootKeyColumn.Name != "_key" || rootKeyColumn.ID != "_key" ||
			!relatedGroupPreviewPassesInputColumn(first, rootKeyColumn) {
			return relatedGroupPreviewShape{}, false
		}
		needsRootKey = true
	}

	previousStageID := first.ID
	for index := 1; index < groupIndex; index++ {
		stage := sequence.Stages[index]
		if stage.ID == "" || stage.InputStageID != previousStageID || stage.Kind != ir.PhysicalStageRelatedExpandOp ||
			stage.RelatedExpand == nil || stage.RelatedExpand.EmptyPolicy != ir.PhysicalUnnestPreserveParent ||
			stage.Filter != nil || len(stage.DerivedLets) != 0 ||
			!relatedGroupPreviewPassesInputColumn(stage, groupKeyColumn) ||
			(needsRootKey && !relatedGroupPreviewPassesInputColumn(stage, rootKeyColumn)) {
			return relatedGroupPreviewShape{}, false
		}
		previousStageID = stage.ID
	}

	groupStage := sequence.Stages[groupIndex]
	if groupStage.ID == "" || groupStage.InputStageID != previousStageID || groupStage.Kind != ir.PhysicalStageGroupOp ||
		group == nil || len(group.Keys) != 1 || group.Keys[0].InputColumn != groupKeyColumn.Name ||
		group.Keys[0].Kind != "STRING" || (group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyGroup &&
		group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyExclude) ||
		!constructionGroupCountsOnlyRows(group) || len(group.RowValues) != 0 ||
		(group.RootContributorInputColumn != "" && (group.RootContributorInputColumn != "_key" || group.RootContributorInputMany)) ||
		!relatedGroupPreviewHasDirectInputColumn(groupStage, groupKeyColumn) ||
		(needsRootKey && !relatedGroupPreviewHasDirectInputColumn(groupStage, rootKeyColumn)) {
		return relatedGroupPreviewShape{}, false
	}
	groupIdentity, ok := relatedGroupPreviewGroupIdentityColumn(groupStage)
	if !ok {
		return relatedGroupPreviewShape{}, false
	}

	previousStageID = groupStage.ID
	groupFilterIndex := groupIndex + 1
	for index := groupIndex + 1; index < len(sequence.Stages); index++ {
		stage := sequence.Stages[index]
		if stage.ID == "" || stage.InputStageID != previousStageID || stage.Kind != ir.PhysicalStageRelatedSourceOp ||
			stage.RelatedSource == nil || stage.Filter != nil || len(stage.DerivedLets) != 0 ||
			stage.RowIdentityColumn != groupIdentity.Name ||
			!relatedGroupPreviewPreservesIdentity(stage, groupIdentity) {
			return relatedGroupPreviewShape{}, false
		}
		previousStageID = stage.ID
	}
	if sequence.Stages[len(sequence.Stages)-1].ID != sequence.FinalStageID {
		return relatedGroupPreviewShape{}, false
	}
	if groupFilterIndex >= len(sequence.Stages) {
		groupFilterIndex = -1
	}
	if groupFilterIndex < 0 && sequence.FinalRowIdentity != groupIdentity.Name {
		return relatedGroupPreviewShape{}, false
	}
	if groupFilterIndex >= 0 && sequence.Stages[groupFilterIndex].InputRowVariable == "" {
		return relatedGroupPreviewShape{}, false
	}

	keyID := ""
	for _, column := range groupStage.OutputColumns {
		if column.Name == group.Keys[0].OutputColumn {
			keyID = column.ID
			break
		}
	}
	if keyID == "" {
		return relatedGroupPreviewShape{}, false
	}
	keyType, err := aqlTableScalarType(group.Keys[0].Kind)
	if err != nil {
		return relatedGroupPreviewShape{}, false
	}
	if group.ConstructionIDBindKey == "" || plan.BindVars[group.ConstructionIDBindKey] == nil ||
		firstHop.TargetTypeBindKey == "" || plan.BindVars[firstHop.TargetTypeBindKey] != related.TargetResourceType {
		return relatedGroupPreviewShape{}, false
	}
	return relatedGroupPreviewShape{
		firstHop: firstHop, groupStageIndex: groupIndex, groupFilterIndex: groupFilterIndex,
		groupIdentityName: groupIdentity.Name, groupIdentityKey: keyID, groupKeyName: groupKeyColumn.Name,
		rootCollection: rootCollection, rootResourceType: rootResourceType,
		targetResourceType: related.TargetResourceType, groupKeyType: keyType,
	}, true
}

func relatedGroupPreviewRootSource(plan ir.PhysicalPlan) (string, string, bool) {
	if len(plan.Operations) != 6 || plan.Operations[0].Kind != ir.PhysicalRootScanOp ||
		plan.Operations[0].RootScan == nil || plan.Operations[0].RootScan.Population != nil ||
		plan.Operations[0].RootScan.Variable == "" || plan.Operations[0].RootScan.CollectionBindKey != "root_collection" ||
		plan.Source.ResourceType == "" || plan.Operations[0].Source.ResourceType != plan.Source.ResourceType ||
		plan.BindVars["root_collection"] != plan.Source.ResourceType ||
		!matchesSingleDocumentScope(plan.Operations[1:5], plan.Operations[0].RootScan.Variable) ||
		plan.Operations[5].Kind != ir.PhysicalReturnOp || plan.Operations[5].Return == nil ||
		!relatedGroupPreviewProjectsRootKey(*plan.Operations[5].Return, plan.Operations[0].RootScan.Variable) {
		return "", "", false
	}
	scopeAllowed, scopeOK := plan.BindVars["scope_allowed"].(bool)
	_, pathsOK := plan.BindVars["auth_resource_paths"].([]string)
	_, unrestrictedOK := plan.BindVars["auth_resource_paths_unrestricted"].(bool)
	if !scopeOK || !scopeAllowed || !pathsOK || !unrestrictedOK {
		return "", "", false
	}
	return plan.Source.ResourceType, plan.Source.ResourceType, true
}

func relatedGroupPreviewProjectsRootKey(returned ir.PhysicalReturn, rootVariable string) bool {
	for _, projection := range returned.Projections {
		if projection.Name == "_key" {
			return projection.Expression == nil &&
				samePhysicalValue(projection.Value, ir.PhysicalValue{Variable: rootVariable, Path: []string{"_key"}})
		}
	}
	return false
}

func relatedGroupPreviewExactFirstHop(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage) (ir.PhysicalTraversal, bool) {
	related := stage.RelatedExpand
	if related == nil || !related.RelatedRecords.Unique || related.RelatedRecords.Sort == nil ||
		len(related.RelatedRecords.Sort.Path) != 1 || related.RelatedRecords.Sort.Path[0] != "_id" ||
		related.RelatedRecords.Return.Kind != ir.PhysicalObjectExpression || related.RelatedRecords.Return.Object == nil ||
		len(related.RelatedRecords.Return.Object.Fields) != 2 {
		return ir.PhysicalTraversal{}, false
	}
	terminalVariable := related.RelatedRecords.Sort.Variable
	if terminalVariable == "" {
		return ir.PhysicalTraversal{}, false
	}
	fields := make(map[string]ir.PhysicalExpression, 2)
	for _, field := range related.RelatedRecords.Return.Object.Fields {
		if _, duplicate := fields[field.Name]; duplicate || field.Name != "terminal_id" && field.Name != "resource_id" {
			return ir.PhysicalTraversal{}, false
		}
		fields[field.Name] = field.Expression
	}
	if !relatedGroupPreviewReturnsValue(fields["terminal_id"], terminalVariable, "_id") ||
		!relatedGroupPreviewReturnsValue(fields["resource_id"], terminalVariable, "id") {
		return ir.PhysicalTraversal{}, false
	}

	// Reuse the exact COUNT route proof after converting only the already-verified
	// sorted-unique object projection into its terminal _id scalar. The candidate
	// scan assumes normal ingestion stores each resourceType in its named vertex
	// collection; the reverse root check still verifies the physical root bucket.
	route := related.RelatedRecords
	route.Unique = false
	route.Sort = nil
	route.Return = ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Value:        &ir.PhysicalValue{Variable: terminalVariable, Path: []string{"_id"}},
	}
	traversals, ok := relatedCountRoute(plan, stage, &route)
	if !ok || len(traversals) != 1 || traversals[0].TargetTypeBindKey == "" ||
		traversals[0].EdgeCollectionBindKey == "" || traversals[0].EdgeLabelBindKey == "" {
		return ir.PhysicalTraversal{}, false
	}
	return traversals[0], true
}

func relatedGroupPreviewReturnsValue(expression ir.PhysicalExpression, variable, field string) bool {
	return expression.Kind == ir.PhysicalValueExpression && expression.Value != nil &&
		samePhysicalValue(*expression.Value, ir.PhysicalValue{Variable: variable, Path: []string{field}})
}

func relatedGroupPreviewProjectsResourceID(stage ir.PhysicalConstructionStage, column ir.PhysicalStageColumn) bool {
	for _, projection := range stage.OutputProjections {
		if projection.Name == column.Name {
			return !projection.Hidden && projection.Expression == nil &&
				samePhysicalValue(projection.Value, ir.PhysicalValue{Variable: stage.RelatedExpand.ItemVariable, Path: []string{"resource_id"}})
		}
	}
	return false
}

func relatedGroupPreviewStageColumnByID(columns []ir.PhysicalStageColumn, id string) (ir.PhysicalStageColumn, bool) {
	for _, column := range columns {
		if column.ID == id {
			return column, true
		}
	}
	return ir.PhysicalStageColumn{}, false
}

func relatedGroupPreviewRootContributorColumn(columns []ir.PhysicalStageColumn, resourceType string) (ir.PhysicalStageColumn, bool) {
	for _, column := range columns {
		if column.RootContributorResourceType == resourceType && column.Name == "_key" && column.ID == "_key" &&
			column.Kind == "string" && column.Cardinality == "required_one" && column.Internal {
			return column, true
		}
	}
	return ir.PhysicalStageColumn{}, false
}

func relatedGroupPreviewPassesInputColumn(stage ir.PhysicalConstructionStage, input ir.PhysicalStageColumn) bool {
	inputFound, outputFound := false, false
	for _, column := range stage.InputColumns {
		if column.ID == input.ID && column.Name == input.Name && column.RootContributorResourceType == input.RootContributorResourceType {
			inputFound = true
			break
		}
	}
	for _, column := range stage.OutputColumns {
		if column.ID == input.ID && column.Name == input.Name && column.RootContributorResourceType == input.RootContributorResourceType {
			outputFound = true
			break
		}
	}
	if !inputFound || !outputFound {
		return false
	}
	for _, projection := range stage.OutputProjections {
		if projection.Name != input.Name {
			continue
		}
		return projection.Expression == nil && projection.Value.Variable == stage.InputRowVariable &&
			projection.Value.BindKey == "" && len(projection.Value.Path) == 1 && projection.Value.Path[0] == input.Name
	}
	return false
}

func relatedGroupPreviewHasDirectInputColumn(stage ir.PhysicalConstructionStage, input ir.PhysicalStageColumn) bool {
	for _, column := range stage.InputColumns {
		if column.ID == input.ID && column.Name == input.Name {
			for _, projection := range stage.InputProjections {
				if projection.Name == input.Name {
					return projection.Expression == nil && projection.Value.Variable == stage.InputRowVariable &&
						projection.Value.BindKey == "" && len(projection.Value.Path) == 1 && projection.Value.Path[0] == input.Name
				}
			}
		}
	}
	return false
}

func relatedGroupPreviewGroupIdentityColumn(stage ir.PhysicalConstructionStage) (ir.PhysicalStageColumn, bool) {
	if stage.Group == nil || stage.Group.IdentityVariable == "" || stage.RowIdentityColumn == "" {
		return ir.PhysicalStageColumn{}, false
	}
	for _, column := range stage.OutputColumns {
		if column.Name != stage.RowIdentityColumn || !column.Internal || !column.Identity ||
			column.Kind != "string" || column.Cardinality != "required_one" {
			continue
		}
		for _, projection := range stage.OutputProjections {
			if projection.Name == column.Name && projection.Expression == nil &&
				projection.Value.Variable == stage.Group.IdentityVariable && projection.Value.BindKey == "" && len(projection.Value.Path) == 0 {
				return column, true
			}
		}
	}
	return ir.PhysicalStageColumn{}, false
}

func relatedGroupPreviewPreservesIdentity(stage ir.PhysicalConstructionStage, identity ir.PhysicalStageColumn) bool {
	inputFound, outputFound, projected := false, false, false
	for _, column := range stage.InputColumns {
		if column.ID == identity.ID && column.Name == identity.Name && column.Identity && column.Internal {
			inputFound = true
			break
		}
	}
	for _, column := range stage.OutputColumns {
		if column.ID == identity.ID && column.Name == identity.Name && column.Identity && column.Internal {
			outputFound = true
			break
		}
	}
	for _, projection := range stage.OutputProjections {
		if projection.Name == identity.Name {
			projected = projection.Expression == nil && projection.Value.Variable == stage.InputRowVariable &&
				projection.Value.BindKey == "" && len(projection.Value.Path) == 1 && projection.Value.Path[0] == identity.Name
			break
		}
	}
	return inputFound && outputFound && projected
}

func renderRelatedGroupPreviewFrontier(
	plan ir.PhysicalPlan,
	sequence *ir.PhysicalStageSequence,
	shape relatedGroupPreviewShape,
	collectionKeys map[string]struct{},
) (relatedGroupPreviewFrontier, error) {
	bindVars := make(map[string]any, len(plan.BindVars))
	for key, value := range plan.BindVars {
		bindVars[key] = clonePhysicalBindValue(value)
	}
	keys := make(map[string]struct{}, len(collectionKeys)+1)
	for key := range collectionKeys {
		keys[key] = struct{}{}
	}
	key := runtimePhysicalBindVars(bindVars, keys)
	renderer := physicalPlanRenderer{
		bindVars: key, collectionKeys: keys,
		reservedVars: stageSequenceVariableNames(plan), internalPrefix: "related_group_preview_",
	}
	targetCollectionBind := renderer.newInternalBindKey("target_collection")
	renderer.collectionKeys[targetCollectionBind] = struct{}{}
	renderer.bindVars["@"+targetCollectionBind] = shape.targetResourceType
	rootCollectionBind := renderer.newInternalBindKey("root_collection_name")
	renderer.bindVars[rootCollectionBind] = shape.rootCollection
	keyColumnBind := renderer.newInternalBindKey("group_identity_key")
	renderer.bindVars[keyColumnBind] = shape.groupIdentityKey
	identityColumnBind := renderer.newInternalBindKey("group_identity_column")
	renderer.bindVars[identityColumnBind] = shape.groupIdentityName
	keyTypeBind := renderer.newInternalBindKey("group_key_type")
	renderer.bindVars[keyTypeBind] = shape.groupKeyType

	selectedGroups := renderer.newInternalVariable("selected_groups")
	candidateGroups := renderer.newInternalVariable("candidate_groups")
	patient := renderer.newInternalVariable("candidate_patient")
	patientFHIRID := renderer.newInternalVariable("candidate_patient_fhir_id")
	groupIdentity := renderer.newInternalVariable("candidate_group_identity")
	candidateIdentity := renderer.newInternalVariable("candidate_identity")
	patientDocs := renderer.newInternalVariable("candidate_patient_docs")
	candidate := renderer.newInternalVariable("candidate_group")
	patientDoc := renderer.newInternalVariable("candidate_patient_doc")
	edge := renderer.newInternalVariable("candidate_edge")
	root := renderer.newInternalVariable("candidate_root")
	edgeScope := renderer.newInternalVariable("candidate_edge_scope_allowed")
	patientScope := renderer.newInternalVariable("candidate_patient_scope_allowed")
	candidateRootScope := renderer.newInternalVariable("candidate_root_scope_allowed")
	selectedDocID := renderer.newInternalVariable("candidate_patient_doc_id")
	selectedPatientID := renderer.newInternalVariable("selected_patient_id")
	selectedGroup := renderer.newInternalVariable("selected_group")
	rootEdge := renderer.newInternalVariable("selected_root_edge")
	selectedRoot := renderer.newInternalVariable("selected_root")
	selectedEdgeScope := renderer.newInternalVariable("selected_edge_scope_allowed")
	selectedRootScope := renderer.newInternalVariable("selected_root_scope_allowed")
	bounded := renderer.newInternalVariable("bounded")
	rootKeys := renderer.newInternalVariable("root_keys")
	groupIdentities := renderer.newInternalVariable("group_identities")

	traversal := shape.firstHop
	rootEndpoint, targetEndpoint := traversal.EndpointField, traversal.EndpointJoinField
	if rootEndpoint == "" || targetEndpoint == "" || traversal.EdgeTargetTypeField == "" {
		return relatedGroupPreviewFrontier{}, fmt.Errorf("proven related first hop has incomplete endpoint fields")
	}
	group := sequence.Stages[shape.groupStageIndex].Group
	lines := []string{
		fmt.Sprintf("LET %s = (", selectedGroups),
		fmt.Sprintf("  LET %s = (", candidateGroups),
		fmt.Sprintf("    FOR %s IN @@%s", patient, targetCollectionBind),
		fmt.Sprintf("      FILTER %s.resourceType == @%s", patient, traversal.TargetTypeBindKey),
		fmt.Sprintf("      FILTER %s.project == @project", patient),
		fmt.Sprintf("      FILTER %s.dataset_generation == @dataset_generation", patient),
		fmt.Sprintf("      LET %s = @auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths", patientScope, patient),
		fmt.Sprintf("      FILTER %s == @scope_allowed", patientScope),
		fmt.Sprintf("      LET %s = %s.id", patientFHIRID, patient),
		fmt.Sprintf("      LET %s = TO_STRING([[\"construction\", @%s], [\"operation\", \"GROUP\"], [@%s, %s]])", groupIdentity, group.ConstructionIDBindKey, keyColumnBind, patientFHIRID),
		fmt.Sprintf("      SORT %s ASC", groupIdentity),
		fmt.Sprintf("      COLLECT %s = %s INTO %s = {id: %s._id, fhirId: %s} OPTIONS {method: \"sorted\"}", candidateIdentity, groupIdentity, patientDocs, patient, patientFHIRID),
		fmt.Sprintf("      SORT %s ASC", candidateIdentity),
		fmt.Sprintf("      LIMIT (@%s * 4)", sequence.PreviewLimitBindKey),
		fmt.Sprintf("      RETURN {identity: %s, patientDocs: %s}", candidateIdentity, patientDocs),
		"  )",
		fmt.Sprintf("  FOR %s IN %s", candidate, candidateGroups),
		fmt.Sprintf("    FILTER LENGTH(("),
		fmt.Sprintf("      FOR %s IN %s.patientDocs", patientDoc, candidate),
		fmt.Sprintf("        FILTER LENGTH(("),
		fmt.Sprintf("          FOR %s IN @@%s", edge, traversal.EdgeCollectionBindKey),
		fmt.Sprintf("            FILTER %s.%s == %s.id", edge, targetEndpoint, patientDoc),
		fmt.Sprintf("            FILTER %s.label == @%s", edge, traversal.EdgeLabelBindKey),
		fmt.Sprintf("            FILTER %s.%s == @%s", edge, traversal.EdgeTargetTypeField, traversal.TargetTypeBindKey),
		fmt.Sprintf("            FILTER %s.project == @project", edge),
		fmt.Sprintf("            FILTER %s.dataset_generation == @dataset_generation", edge),
		fmt.Sprintf("            LET %s = @auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths", edgeScope, edge),
		fmt.Sprintf("            FILTER %s == @scope_allowed", edgeScope),
		fmt.Sprintf("            FILTER IS_SAME_COLLECTION(@%s, %s.%s)", rootCollectionBind, edge, rootEndpoint),
		fmt.Sprintf("            LET %s = DOCUMENT(%s.%s)", root, edge, rootEndpoint),
		fmt.Sprintf("            FILTER %s != null", root),
		fmt.Sprintf("            FILTER %s.project == @project", root),
		fmt.Sprintf("            FILTER %s.dataset_generation == @dataset_generation", root),
		fmt.Sprintf("            LET %s = @auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths", candidateRootScope, root),
		fmt.Sprintf("            FILTER %s == @scope_allowed", candidateRootScope),
		"            LIMIT 1",
		"            RETURN 1",
		"        )) > 0",
		"      LIMIT 1",
		fmt.Sprintf("      RETURN %s.id", patientDoc),
		"    )) > 0",
		fmt.Sprintf("    FILTER ASSERT(%s.patientDocs[0].fhirId == null OR TYPENAME(%s.patientDocs[0].fhirId) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", candidate, candidate, keyTypeBind),
		fmt.Sprintf("    FILTER %s.patientDocs[0].fhirId != null", candidate),
		fmt.Sprintf("    LIMIT @%s", sequence.PreviewLimitBindKey),
		fmt.Sprintf("    RETURN {identity: %s.identity, docIds: (FOR %s IN %s.patientDocs RETURN %s.id)}", candidate, selectedDocID, candidate, selectedDocID),
		")",
		fmt.Sprintf("LET %s = @%s > 0 && LENGTH(%s) == @%s", bounded, sequence.PreviewLimitBindKey, selectedGroups, sequence.PreviewLimitBindKey),
		fmt.Sprintf("LET %s = %s ? SORTED_UNIQUE((", rootKeys, bounded),
		fmt.Sprintf("  FOR %s IN %s", selectedGroup, selectedGroups),
		fmt.Sprintf("    FOR %s IN %s.docIds", selectedPatientID, selectedGroup),
		fmt.Sprintf("      FOR %s IN @@%s", rootEdge, traversal.EdgeCollectionBindKey),
		fmt.Sprintf("        FILTER %s.%s == %s", rootEdge, targetEndpoint, selectedPatientID),
		fmt.Sprintf("        FILTER %s.label == @%s", rootEdge, traversal.EdgeLabelBindKey),
		fmt.Sprintf("        FILTER %s.%s == @%s", rootEdge, traversal.EdgeTargetTypeField, traversal.TargetTypeBindKey),
		fmt.Sprintf("        FILTER %s.project == @project", rootEdge),
		fmt.Sprintf("        FILTER %s.dataset_generation == @dataset_generation", rootEdge),
		fmt.Sprintf("        LET %s = @auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths", selectedEdgeScope, rootEdge),
		fmt.Sprintf("        FILTER %s == @scope_allowed", selectedEdgeScope),
		fmt.Sprintf("        FILTER IS_SAME_COLLECTION(@%s, %s.%s)", rootCollectionBind, rootEdge, rootEndpoint),
		fmt.Sprintf("        LET %s = DOCUMENT(%s.%s)", selectedRoot, rootEdge, rootEndpoint),
		fmt.Sprintf("        FILTER %s != null", selectedRoot),
		fmt.Sprintf("        FILTER %s.project == @project", selectedRoot),
		fmt.Sprintf("        FILTER %s.dataset_generation == @dataset_generation", selectedRoot),
		fmt.Sprintf("        LET %s = @auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths", selectedRootScope, selectedRoot),
		fmt.Sprintf("        FILTER %s == @scope_allowed", selectedRootScope),
		fmt.Sprintf("        RETURN %s._key", selectedRoot),
		")) : []",
		fmt.Sprintf("LET %s = %s[*].identity", groupIdentities, selectedGroups),
	}
	query := strings.Join(lines, "\n") + "\n"
	frontierBindVars := pruneUnusedRuntimeBindVars(renderer.bindVars, query)
	frontierBindVars[identityColumnBind] = shape.groupIdentityName
	return relatedGroupPreviewFrontier{
		query: query, bindVars: frontierBindVars,
		selectedGroupsVariable: selectedGroups, boundedVariable: bounded, rootKeysVariable: rootKeys,
		groupIdentitiesVariable: groupIdentities, groupIdentityColumnBindKey: identityColumnBind,
	}, nil
}

func renderRelatedGroupPreviewConditionalSource(boundedVariable, windowQuery, canonicalQuery string) string {
	indent := func(query string) string {
		lines := strings.Split(strings.TrimSuffix(query, "\n"), "\n")
		for index := range lines {
			lines[index] = "  " + lines[index]
		}
		return strings.Join(lines, "\n")
	}
	return fmt.Sprintf("%s ? (\n%s\n) : (\n%s\n)", boundedVariable, indent(windowQuery), indent(canonicalQuery))
}

func renderRelatedGroupPreviewSourceWindow(plan ir.PhysicalPlan, options physicalRenderOptions, rootKeysVariable string) (RenderedPhysicalPlan, error) {
	if rootKeysVariable == "" || len(plan.Operations) == 0 || plan.Operations[0].RootScan == nil ||
		plan.Operations[0].RootScan.Population != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("related Group source window requires a direct root scan")
	}
	options.previewRootKeyWindowVariable = rootKeysVariable
	return renderPhysicalPlanWithOptions(plan, options)
}
