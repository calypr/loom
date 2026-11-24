package ir

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func validatePhysicalStageRelatedField(stage PhysicalConstructionStage, related PhysicalStageRelatedField, bindVars map[string]any) error {
	if related.ActiveRecordColumn == "" || related.CandidateID == "" || related.TargetNodeID == "" ||
		related.TargetResourceType == "" || related.OutputColumnID == "" || related.LogicalType == "" || len(related.Path) == 0 {
		return fmt.Errorf("active record, exact field identity, and output are required")
	}
	if project, ok := bindVars["project"].(string); !ok || project == "" {
		return fmt.Errorf("related field requires the exact project runtime binding")
	}
	if generation, ok := bindVars["dataset_generation"].(string); !ok || generation == "" {
		return fmt.Errorf("related field requires the exact dataset generation runtime binding")
	}
	if _, ok := bindVars["auth_resource_paths"].([]string); !ok {
		return fmt.Errorf("related field requires the effective authorization resource paths")
	}
	if _, ok := bindVars["auth_resource_paths_unrestricted"].(bool); !ok {
		return fmt.Errorf("related field requires the effective unrestricted authorization flag")
	}
	inputColumns := physicalStageColumnMap(stage.InputColumns)
	anchor, found := inputColumns[related.ActiveRecordColumn]
	if !found || !anchor.Internal || anchor.Identity || anchor.Kind != "string" ||
		(anchor.Cardinality != "required_one" && anchor.Cardinality != "optional_one") {
		return fmt.Errorf("active record does not identify a retained terminal document identity")
	}
	outputColumns := stageColumnsByID(stage.OutputColumns)
	output, found := outputColumns[related.OutputColumnID]
	wantNullable := true
	wantCardinality := "optional_one"
	if !found || output.Internal || output.Kind != related.LogicalType || output.Cardinality != wantCardinality {
		return fmt.Errorf("related field output schema differs from its exact source")
	}
	if output.Nullable != wantNullable {
		return fmt.Errorf("related field output nullability differs from its source and active record")
	}
	var fieldProjection *PhysicalProjection
	for index := range stage.OutputProjections {
		if stage.OutputProjections[index].Name == output.Name {
			fieldProjection = &stage.OutputProjections[index]
			break
		}
	}
	if fieldProjection == nil || fieldProjection.Expression == nil ||
		fieldProjection.Expression.Kind != PhysicalRelatedFieldExpression || fieldProjection.Expression.RelatedField == nil {
		return fmt.Errorf("related field projection does not use the typed exact-record expression")
	}
	value := fieldProjection.Expression.RelatedField
	if value.DocumentID.Variable != stage.InputRowVariable || value.ResourceType != related.TargetResourceType || len(value.DocumentID.Path) != 1 ||
		value.DocumentID.Path[0] != related.ActiveRecordColumn || !reflect.DeepEqual(value.Path, related.Path) {
		return fmt.Errorf("related field expression differs from its active record, resource, or selected path")
	}
	for index, segment := range related.Path {
		if !spec.ValidFieldPathSegment(segment) {
			return fmt.Errorf("related field path segment %d is invalid", index)
		}
	}
	return validateStageRowOperations(stage, bindVars, false)
}

func validatePhysicalStageRelatedExpand(stage PhysicalConstructionStage, related PhysicalStageRelatedExpand, bindVars map[string]any) error {
	if related.AnchorColumnID == "" || related.AnchorKind == "" || related.AnchorNodeID == "" || related.AnchorResourceType == "" || related.RelatedRecordColumnID == "" || related.TargetNodeID == "" ||
		related.TargetResourceType == "" || related.ParentIdentityColumn == "" || related.ParentIdentityColumnID == "" ||
		related.TerminalIdentityColumn == "" || related.ConstructionIDBindKey == "" || len(related.Route) == 0 {
		return fmt.Errorf("row resource anchor, target, route, identities, and expansion bind are required")
	}
	if err := requireNonEmptyStringBind(bindVars, related.ConstructionIDBindKey); err != nil {
		return fmt.Errorf("construction ID: %w", err)
	}
	if related.EmptyPolicy != PhysicalUnnestError && related.EmptyPolicy != PhysicalUnnestExclude && related.EmptyPolicy != PhysicalUnnestPreserveParent {
		return fmt.Errorf("empty policy %q is unsupported", related.EmptyPolicy)
	}
	variables := []string{related.RelatedRecordsVariable, related.IndexVariable, related.ItemVariable, related.IdentityVariable}
	seenVariables := make(map[string]bool, len(variables)+2)
	seenVariables[stage.InputRowVariable], seenVariables[stage.OutputRowVariable] = true, true
	for _, variable := range variables {
		if !physicalVariablePattern.MatchString(variable) || seenVariables[variable] {
			return fmt.Errorf("related expansion variable %q is unsafe or duplicated", variable)
		}
		seenVariables[variable] = true
	}

	inputColumns := stageColumnsByID(stage.InputColumns)
	anchor, found := inputColumns[related.AnchorColumnID]
	if !found || !anchor.Internal || anchor.Name != related.AnchorColumnID || anchor.Kind != "string" {
		return fmt.Errorf("selected anchor does not identify compiler-proven resource identity")
	}
	switch related.AnchorKind {
	case "root":
		if related.AnchorColumnID != "_key" || anchor.Cardinality != "required_one" || related.AnchorNodeID != related.Route[0].FromNodeID ||
			related.RootContributorColumn != "" || related.RootResourceType != "" {
			return fmt.Errorf("root anchor must select the retained _key and exact route root")
		}
	case "rootContributors":
		if related.AnchorColumnID != "__loom_root_contributor_keys" || anchor.Cardinality != "many" ||
			anchor.RootContributorResourceType == "" || related.RootResourceType != anchor.RootContributorResourceType ||
			related.AnchorResourceType != related.RootResourceType || related.RootContributorColumn != related.AnchorColumnID ||
			related.AnchorNodeID != related.Route[0].FromNodeID {
			return fmt.Errorf("root-contributor anchor must select the exact compiler-owned root identity set")
		}
	case "activeRelatedRecord":
		if related.AnchorColumnID == "_key" || anchor.Cardinality != "required_one" && anchor.Cardinality != "optional_one" ||
			anchor.RelatedRecordAnchor == nil || related.RootContributorColumn != "" || related.RootResourceType != "" ||
			anchor.RelatedRecordAnchor.NodeID != related.AnchorNodeID || anchor.RelatedRecordAnchor.ResourceType != related.AnchorResourceType {
			return fmt.Errorf("active anchor must select the exact retained related-record identity")
		}
	default:
		return fmt.Errorf("anchor kind %q is unsupported", related.AnchorKind)
	}
	if related.Route[0].FromNodeID != related.AnchorNodeID || related.Route[0].FromResourceType != related.AnchorResourceType {
		return fmt.Errorf("route starts at a different node or resource type from its exact anchor")
	}
	if len(related.RelatedRecords.Operations) == 0 {
		return fmt.Errorf("related route has no exact anchor lookup")
	}
	firstOperation := related.RelatedRecords.Operations[0]
	if related.AnchorKind == "root" || related.AnchorKind == "rootContributors" {
		if firstOperation.Kind != PhysicalCollectionScanOp || firstOperation.CollectionScan == nil ||
			bindVars[firstOperation.CollectionScan.CollectionBindKey] != related.AnchorResourceType || len(related.RelatedRecords.Operations) < 2 {
			return fmt.Errorf("root anchor route must begin with the scoped root collection lookup")
		}
		rootFilter := related.RelatedRecords.Operations[1]
		if rootFilter.Kind != PhysicalFilterOp || rootFilter.Filter == nil || rootFilter.Filter.Expression != nil {
			return fmt.Errorf("root anchor route must filter its exact root key or contributor set")
		}
		predicate := rootFilter.Filter.Predicate
		wantOperator, wantPath := "EQUALS", related.AnchorColumnID
		if related.AnchorKind == "rootContributors" {
			wantOperator, wantPath = "IN", related.RootContributorColumn
		}
		if predicate.Operator != wantOperator || predicate.Left.Variable != firstOperation.CollectionScan.Variable ||
			!reflect.DeepEqual(predicate.Left.Path, []string{"_key"}) || predicate.Right == nil ||
			predicate.Right.Variable != stage.InputRowVariable || !reflect.DeepEqual(predicate.Right.Path, []string{wantPath}) {
			return fmt.Errorf("root anchor filter differs from its compiler-proven identity input")
		}
	}
	if related.AnchorKind == "activeRelatedRecord" && (firstOperation.Kind != PhysicalDocumentLookupOp || firstOperation.DocumentLookup == nil ||
		firstOperation.DocumentLookup.Variable == "" || firstOperation.DocumentLookup.ExactID.Variable != stage.InputRowVariable ||
		len(firstOperation.DocumentLookup.ExactID.Path) != 1 || firstOperation.DocumentLookup.ExactID.Path[0] != related.AnchorColumnID) {
		return fmt.Errorf("active anchor route must begin with an exact DOCUMENT lookup of the selected hidden _id")
	}
	parentIdentity, found := physicalStageColumnMap(stage.InputColumns)[related.ParentIdentityColumn]
	if !found || !parentIdentity.Internal || !parentIdentity.Identity || parentIdentity.Kind != "string" || parentIdentity.Cardinality != "required_one" {
		return fmt.Errorf("parent identity does not identify the exact input row identity")
	}
	for _, column := range stage.InputColumns {
		if column.Identity && column.Name != related.ParentIdentityColumn {
			return fmt.Errorf("parent identity differs from the stage input row identity")
		}
	}
	if err := validatePhysicalSubplan(related.RelatedRecords, map[string]bool{stage.InputRowVariable: true}, bindVars); err != nil {
		return fmt.Errorf("related route subplan: %w", err)
	}
	if len(related.RelatedRecords.Captures) != 1 || related.RelatedRecords.Captures[0] != stage.InputRowVariable ||
		!related.RelatedRecords.Unique || related.RelatedRecords.Sort == nil {
		return fmt.Errorf("related route must be uniquely sorted and capture only its input row")
	}
	if related.RelatedRecords.Return.Kind != PhysicalObjectExpression || related.RelatedRecords.Return.Object == nil || len(related.RelatedRecords.Return.Object.Fields) != 2 {
		return fmt.Errorf("related route must return typed terminal _id and public FHIR id fields")
	}
	terminalVariable := ""
	traversals := make([]PhysicalOperation, 0, len(related.Route))
	for _, operation := range related.RelatedRecords.Operations {
		if operation.Kind == PhysicalTraversalOp {
			traversals = append(traversals, operation)
		}
	}
	if len(traversals) != len(related.Route) {
		return fmt.Errorf("related route metadata contains %d hops but subplan contains %d traversals", len(related.Route), len(traversals))
	}
	for index, hop := range related.Route {
		if hop.EdgeID == "" || hop.FromNodeID == "" || hop.ToNodeID == "" || hop.FromResourceType == "" || hop.ToResourceType == "" ||
			hop.Relationship == "" || (hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") || index > 0 &&
			(related.Route[index-1].ToNodeID != hop.FromNodeID || related.Route[index-1].ToResourceType != hop.FromResourceType) {
			return fmt.Errorf("route metadata hop %d is invalid or discontinuous", index)
		}
		operation := traversals[index]
		traversal := operation.Traversal
		wantDirection := PhysicalInbound
		if hop.StorageDirection == "OUTBOUND" {
			wantDirection = PhysicalOutbound
		}
		if operation.Source.ResourceType != hop.ToResourceType || operation.Source.SemanticNode != hop.ToNodeID ||
			operation.Source.Relationship != hop.Relationship || traversal.Direction != wantDirection ||
			traversal.TargetTypeBindKey == "" || bindVars[traversal.TargetTypeBindKey] != hop.ToResourceType {
			return fmt.Errorf("route metadata hop %d differs from its compiled traversal", index)
		}
		terminalVariable = traversal.TargetVariable
	}
	last := related.Route[len(related.Route)-1]
	if last.ToNodeID != related.TargetNodeID || last.ToResourceType != related.TargetResourceType {
		return fmt.Errorf("route metadata terminal differs from its exact target")
	}
	if terminalVariable == "" || related.RelatedRecords.Sort.Variable != terminalVariable || len(related.RelatedRecords.Sort.Path) != 1 || related.RelatedRecords.Sort.Path[0] != "_id" {
		return fmt.Errorf("related route sort must use the exact terminal document _id")
	}
	fields := map[string]PhysicalExpression{}
	for _, field := range related.RelatedRecords.Return.Object.Fields {
		fields[field.Name] = field.Expression
	}
	if !physicalValueMatches(fields["terminal_id"], terminalVariable, "_id") || !physicalValueMatches(fields["resource_id"], terminalVariable, "id") {
		return fmt.Errorf("related route return differs from terminal document identity")
	}

	outputColumns := stageColumnsByID(stage.OutputColumns)
	publicID, found := outputColumns[related.RelatedRecordColumnID]
	wantNullable := related.EmptyPolicy == PhysicalUnnestPreserveParent
	wantCardinality := "required_one"
	if wantNullable {
		wantCardinality = "optional_one"
	}
	if !found || publicID.Internal || publicID.Kind != "string" || publicID.Cardinality != wantCardinality || publicID.Nullable != wantNullable {
		return fmt.Errorf("public related FHIR ID does not match the route or empty policy")
	}
	parentID, found := outputColumns[related.ParentIdentityColumnID]
	if !found || !parentID.Internal || parentID.Identity || parentID.Kind != "string" || parentID.Cardinality != "required_one" || parentID.Nullable {
		return fmt.Errorf("parent row identity is not retained as a hidden required string")
	}
	terminalID, found := outputColumns[related.TerminalIdentityColumn]
	if !found || !terminalID.Internal || terminalID.Identity || terminalID.Kind != "string" || terminalID.Cardinality != wantCardinality || terminalID.Nullable != wantNullable {
		return fmt.Errorf("terminal _id is not retained as a typed hidden identity")
	}
	inputRootKey, inputHasRootKey := physicalStageColumnMap(stage.InputColumns)["_key"]
	outputRootKey, outputHasRootKey := physicalStageColumnMap(stage.OutputColumns)["_key"]
	if inputHasRootKey && (!outputHasRootKey || !inputRootKey.Internal || !outputRootKey.Internal || outputRootKey.Identity ||
		outputRootKey.Kind != "string" || outputRootKey.Cardinality != "required_one") {
		return fmt.Errorf("available root _key anchor must remain separate from the terminal identity")
	}
	if !inputHasRootKey && outputHasRootKey {
		return fmt.Errorf("related expansion cannot introduce a root _key that was absent from its input")
	}
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return err
	}
	for _, projection := range stage.OutputProjections {
		switch projection.Name {
		case publicID.Name:
			if projection.Value.Variable != related.ItemVariable || len(projection.Value.Path) != 1 || projection.Value.Path[0] != "resource_id" {
				return fmt.Errorf("public related-record ID must project the terminal FHIR id")
			}
		case related.ParentIdentityColumnID:
			if projection.Value.Variable != stage.InputRowVariable || len(projection.Value.Path) != 1 || projection.Value.Path[0] != related.ParentIdentityColumn {
				return fmt.Errorf("parent identity must preserve the exact input row ID")
			}
		case related.TerminalIdentityColumn:
			if projection.Value.Variable != related.ItemVariable || len(projection.Value.Path) != 1 || projection.Value.Path[0] != "terminal_id" {
				return fmt.Errorf("terminal identity must preserve the exact terminal _id")
			}
		case stage.RowIdentityColumn:
			if projection.Value.Variable != related.IdentityVariable || len(projection.Value.Path) != 0 {
				return fmt.Errorf("row identity must use the parent, step, and terminal identity composite")
			}
		case "_key":
			if projection.Value.Variable != stage.InputRowVariable || len(projection.Value.Path) != 1 || projection.Value.Path[0] != "_key" {
				return fmt.Errorf("root identity must remain separate from the terminal identity")
			}
		}
		defined := map[string]bool{stage.InputRowVariable: true, related.ItemVariable: true, related.IdentityVariable: true}
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("output projection %q: %w", projection.Name, err)
		}
	}
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return err
	}
	return nil
}

func physicalValueMatches(expression PhysicalExpression, variable, path string) bool {
	return expression.Kind == PhysicalValueExpression && expression.Value != nil && expression.Value.Variable == variable &&
		len(expression.Value.Path) == 1 && expression.Value.Path[0] == path
}

func validatePhysicalStageRelatedEligibility(stage PhysicalConstructionStage, bindVars map[string]any) error {
	filter := stage.Filter
	if filter.Expression == nil {
		return fmt.Errorf("eligible-row filter must use a typed predicate expression")
	}
	var relatedSubplan *PhysicalSubplan
	switch filter.Expression.Kind {
	case PhysicalExistsPredicate:
		if filter.Expression.Exists == nil || len(stage.DerivedLets) != 0 {
			return fmt.Errorf("EXISTS requires one correlated subplan and no scalar LET")
		}
		relatedSubplan = filter.Expression.Exists
	case PhysicalNotPredicate:
		if len(filter.Expression.Children) != 1 || filter.Expression.Children[0].Kind != PhysicalExistsPredicate ||
			filter.Expression.Children[0].Exists == nil || len(stage.DerivedLets) != 0 {
			return fmt.Errorf("ABSENT requires NOT over one correlated EXISTS subplan and no scalar LET")
		}
		relatedSubplan = filter.Expression.Children[0].Exists
	case PhysicalComparisonPredicate:
		comparison := filter.Expression.Comparison
		if comparison == nil || strings.ToUpper(strings.TrimSpace(comparison.Operator)) != "GTE" || comparison.LeftExpression != nil ||
			comparison.Left.Variable == "" || comparison.Right == nil || comparison.Right.BindKey == "" || len(stage.DerivedLets) != 1 {
			return fmt.Errorf("COUNT_AT_LEAST requires a scalar GTE comparison and one count LET")
		}
		let := stage.DerivedLets[0].ExpressionLet
		if stage.DerivedLets[0].Kind != PhysicalExpressionLetOp || let == nil || let.Variable != comparison.Left.Variable {
			return fmt.Errorf("count comparison must use the variable produced by its typed LET")
		}
		count := let.Expression
		if count.Kind != PhysicalCallExpression || count.Call == nil || strings.ToLower(strings.TrimSpace(count.Call.Name)) != "length" || len(count.Call.Args) != 1 {
			return fmt.Errorf("count LET must apply LENGTH to one related-resource subplan")
		}
		array := count.Call.Args[0]
		if array.Kind != PhysicalSubplanExpression || array.Subplan == nil || !array.Subplan.Unique || array.Subplan.Sort == nil ||
			array.Subplan.Return.Kind != PhysicalValueExpression || array.Subplan.Return.Value == nil ||
			!reflect.DeepEqual(*array.Subplan.Sort, *array.Subplan.Return.Value) || len(array.Subplan.Sort.Path) != 1 || array.Subplan.Sort.Path[0] != "_id" {
			return fmt.Errorf("COUNT_AT_LEAST must count a sorted, distinct terminal document _id subplan")
		}
		relatedSubplan = array.Subplan
		threshold, ok := bindVars[comparison.Right.BindKey].(int)
		if !ok || threshold <= 0 {
			return fmt.Errorf("COUNT_AT_LEAST threshold must be a positive integer bind")
		}
	default:
		return fmt.Errorf("related eligibility supports only EXISTS, ABSENT, or COUNT_AT_LEAST")
	}
	return validateRelatedEligibilityAnchor(stage, relatedSubplan, bindVars)
}

func validateRelatedEligibilityAnchor(stage PhysicalConstructionStage, subplan *PhysicalSubplan, bindVars map[string]any) error {
	if subplan == nil || len(subplan.Captures) != 1 || subplan.Captures[0] != stage.InputRowVariable || len(subplan.Operations) < 2 {
		return fmt.Errorf("related eligibility must correlate its exact row anchor")
	}
	columns := physicalStageColumnMap(stage.InputColumns)
	first := subplan.Operations[0]
	if first.Kind == PhysicalCollectionScanOp && first.CollectionScan != nil {
		scan := first.CollectionScan
		rootType, ok := bindVars["root_collection"].(string)
		if !ok || rootType == "" || scan.CollectionBindKey != "root_collection" ||
			first.Source.ResourceType != rootType || scan.Variable == "" {
			return fmt.Errorf("root identity lookup must bind its compiler-proven resource type")
		}
		filter := subplan.Operations[1]
		if filter.Kind != PhysicalFilterOp || filter.Filter == nil || filter.Filter.Expression != nil {
			return fmt.Errorf("root identity lookup must filter its exact root key or contributor set")
		}
		predicate := filter.Filter.Predicate
		if predicate.Left.Variable != scan.Variable || !reflect.DeepEqual(predicate.Left.Path, []string{"_key"}) || predicate.Right == nil ||
			predicate.Right.Variable != stage.InputRowVariable {
			return fmt.Errorf("root identity filter must compare the scanned _key to its input row anchor")
		}
		switch predicate.Operator {
		case "EQUALS":
			anchor, found := columns["_key"]
			if len(predicate.Right.Path) != 1 || predicate.Right.Path[0] != "_key" || !found || !anchor.Internal ||
				anchor.Name != "_key" || anchor.Kind != "string" || anchor.Cardinality != "required_one" ||
				anchor.RootContributorResourceType != rootType || anchor.RelatedRecordAnchor != nil {
				return fmt.Errorf("root eligibility anchor is not the exact compiler-proven root document key")
			}
		case "IN":
			anchor, found := columns["__loom_root_contributor_keys"]
			if len(predicate.Right.Path) != 1 || predicate.Right.Path[0] != "__loom_root_contributor_keys" || !found || !anchor.Internal ||
				anchor.Name != "__loom_root_contributor_keys" || anchor.Kind != "string" || anchor.Cardinality != "many" ||
				anchor.RootContributorResourceType != rootType {
				return fmt.Errorf("root-contributor eligibility anchor is not the exact compiler-owned root identity set")
			}
		default:
			return fmt.Errorf("root eligibility anchor must use exact key equality or contributor-set membership")
		}
		return nil
	}
	if first.Kind != PhysicalDocumentLookupOp || first.DocumentLookup == nil {
		return fmt.Errorf("related eligibility must begin with an exact root scan or related-record lookup")
	}
	lookup := first.DocumentLookup
	if lookup.ExactID.Variable != stage.InputRowVariable || len(lookup.ExactID.Path) != 1 || lookup.ExactID.Path[0] == "" {
		return fmt.Errorf("active eligibility anchor must look up one exact input-row identity")
	}
	anchor, found := columns[lookup.ExactID.Path[0]]
	resourceType, resourceOK := bindVars[lookup.CollectionBindKey].(string)
	if !found || !anchor.Internal || anchor.Identity || anchor.Name != lookup.ExactID.Path[0] || anchor.Kind != "string" ||
		(anchor.Cardinality != "required_one" && anchor.Cardinality != "optional_one") || anchor.RelatedRecordAnchor == nil ||
		anchor.RelatedRecordAnchor.NodeID == "" || anchor.RelatedRecordAnchor.ResourceType == "" ||
		!resourceOK || resourceType != anchor.RelatedRecordAnchor.ResourceType {
		return fmt.Errorf("active eligibility anchor is not the exact retained related-record identity")
	}
	return nil
}

func validatePhysicalStageRelatedSource(stage PhysicalConstructionStage, related PhysicalStageRelatedSource, bindVars map[string]any) error {
	if related.AnchorColumnID == "" || related.OutputColumnID == "" || related.CandidateID == "" || related.SourceOccurrenceID == "" ||
		related.ResourceType == "" || related.Path == "" || related.LogicalType == "" {
		return fmt.Errorf("source, anchor, and output identities are required")
	}
	if (related.Form != "ALL" && related.Form != "COUNT" && related.Form != "PRESENCE") || related.ContributorPolicy != "ALL_MATCHES" {
		return fmt.Errorf("only ALL, COUNT, or PRESENCE with ALL_MATCHES contributor policy is supported")
	}
	rowValuePolicy := related.RowValuePolicy
	if rowValuePolicy == "" {
		rowValuePolicy = "ALL"
	}
	if (rowValuePolicy != "ALL" && rowValuePolicy != "ONE") || (rowValuePolicy == "ONE" && related.Form != "ALL") {
		return fmt.Errorf("related row value policy must be ALL, or ONE with form ALL")
	}
	var anchor, output *PhysicalStageColumn
	for index := range stage.InputColumns {
		if stage.InputColumns[index].ID == related.AnchorColumnID {
			anchor = &stage.InputColumns[index]
		}
	}
	for index := range stage.OutputColumns {
		if stage.OutputColumns[index].ID == related.OutputColumnID {
			output = &stage.OutputColumns[index]
		}
	}
	if anchor == nil || !anchor.Internal || !anchor.Identity || anchor.Name != stage.RowIdentityColumn || output == nil || output.Internal ||
		related.RootResourceType == "" {
		return fmt.Errorf("anchor or related output does not match the typed stage schema")
	}
	rootContributorColumn := ""
	if related.RootContributorColumn == "" {
		if anchor.Name != "_key" || anchor.RootContributorResourceType != related.RootResourceType || anchor.Kind != "string" || anchor.Cardinality != "required_one" {
			return fmt.Errorf("direct related source anchor is not the exact root document identity")
		}
	} else {
		contributor, found := physicalStageColumnMap(stage.InputColumns)[related.RootContributorColumn]
		validScalar := contributor.Name == "_key" && (contributor.Cardinality == "required_one" || contributor.Cardinality == "optional_one")
		validSet := contributor.Name == "__loom_root_contributor_keys" && contributor.Cardinality == "many"
		if !found || !contributor.Internal || contributor.RootContributorResourceType != related.RootResourceType ||
			contributor.Kind != "string" || !(validScalar || validSet) {
			return fmt.Errorf("related source root contributor is not a compiler-proven root identity or set")
		}
		rootContributorColumn = contributor.Name
	}
	outputKind, outputCardinality, outputNullable := related.LogicalType, "many", true
	switch related.Form {
	case "COUNT":
		outputKind, outputCardinality, outputNullable = "integer", "required_one", false
	case "PRESENCE":
		outputKind, outputCardinality, outputNullable = "boolean", "required_one", false
	}
	if related.Form == "ALL" && rowValuePolicy == "ONE" {
		outputCardinality = "optional_one"
	}
	if output.Kind != outputKind || output.Cardinality != outputCardinality || output.Nullable != outputNullable {
		return fmt.Errorf("related output type does not match its selected form")
	}
	var relatedProjection *PhysicalProjection
	var identityProjection *PhysicalProjection
	for index := range stage.OutputProjections {
		projection := &stage.OutputProjections[index]
		if projection.Name == output.Name {
			relatedProjection = projection
		}
		if projection.Name == stage.RowIdentityColumn {
			identityProjection = projection
		}
	}
	if relatedProjection == nil || relatedProjection.Expression == nil || relatedSourceProjectionSubplan(*relatedProjection.Expression, related) == nil {
		return fmt.Errorf("related output does not match its typed route and form")
	}
	subplan := relatedSourceProjectionSubplan(*relatedProjection.Expression, related)
	var rootVariable string
	if rootContributorColumn == "__loom_root_contributor_keys" {
		if len(subplan.Operations) == 0 || subplan.Operations[0].Kind != PhysicalKeySetLookupOp || subplan.Operations[0].KeySetLookup == nil {
			return fmt.Errorf("related source contributor-set route must begin with an exact key-set lookup")
		}
		lookup := subplan.Operations[0].KeySetLookup
		if lookup.CollectionBindKey != "root_collection" || lookup.Keys.Variable != stage.InputRowVariable ||
			!reflect.DeepEqual(lookup.Keys.Path, []string{rootContributorColumn}) ||
			subplan.Operations[0].Source.ResourceType != related.RootResourceType {
			return fmt.Errorf("related source key-set lookup does not use the exact compiler-owned root contributor set")
		}
		rootVariable = lookup.Variable
		rootScope := physicalScopeResource{
			description:         "related-source root contributor lookup",
			projectVariables:    []string{rootVariable},
			datasetGenVariables: []string{rootVariable},
			authPaths:           []PhysicalValue{{Variable: rootVariable, Path: []string{physicalScopeAuthPathField}}},
		}
		windowEnd := physicalScopeWindowEnd(subplan.Operations, 1)
		if err := validatePhysicalScopeWindow(subplan.Operations, 0, windowEnd, rootScope); err != nil {
			return fmt.Errorf("related source key-set scope: %w", err)
		}
	} else {
		if len(subplan.Operations) < 2 || subplan.Operations[0].CollectionScan == nil || subplan.Operations[1].Filter == nil {
			return fmt.Errorf("related source route does not begin with its root contributor filter")
		}
		rootFilter := subplan.Operations[1].Filter.Predicate
		rootVariable = subplan.Operations[0].CollectionScan.Variable
		wantRight := related.AnchorColumnID
		if rootContributorColumn != "" {
			wantRight = rootContributorColumn
		}
		if rootFilter.Operator != "EQUALS" || rootFilter.Left.Variable != rootVariable ||
			!reflect.DeepEqual(rootFilter.Left.Path, []string{"_key"}) || rootFilter.Right == nil ||
			rootFilter.Right.Variable != stage.InputRowVariable || !reflect.DeepEqual(rootFilter.Right.Path, []string{wantRight}) {
			return fmt.Errorf("related source root scan does not use the exact row anchor or scalar contributor")
		}
	}
	if identityProjection == nil || identityProjection.Value.Variable != stage.InputRowVariable ||
		len(identityProjection.Value.Path) != 1 || identityProjection.Value.Path[0] != stage.RowIdentityColumn {
		return fmt.Errorf("related source must preserve the exact preceding row identity")
	}
	return validateStageRowOperations(stage, bindVars, false)
}

func relatedSourceProjectionSubplan(expression PhysicalExpression, related PhysicalStageRelatedSource) *PhysicalSubplan {
	rowValuePolicy := related.RowValuePolicy
	if rowValuePolicy == "" {
		rowValuePolicy = "ALL"
	}
	if related.Form == "ALL" && rowValuePolicy == "ONE" {
		if expression.Kind != PhysicalCallExpression || expression.Call == nil || expression.Call.Name != "distinct_present_one" ||
			expression.Cardinality != PhysicalScalarCardinality || expression.NullBehavior != PhysicalPreserveNull ||
			len(expression.Call.Args) != 1 || expression.Call.TargetKind != physicalRelatedSourceTableScalarKind(related.LogicalType) {
			return nil
		}
		array := expression.Call.Args[0]
		if array.Kind != PhysicalSubplanExpression || array.Subplan == nil || array.Cardinality != PhysicalArrayCardinality ||
			array.NullBehavior != PhysicalEmptyOnNull || array.Subplan.Unique || array.Subplan.DistinctBy == nil ||
			len(array.Subplan.DistinctBy.Path) != 1 || array.Subplan.DistinctBy.Path[0] != "_id" {
			return nil
		}
		return array.Subplan
	}
	if related.Form == "ALL" {
		if expression.Kind == PhysicalSubplanExpression && expression.Subplan != nil &&
			expression.Cardinality == PhysicalArrayCardinality && expression.NullBehavior == PhysicalEmptyOnNull &&
			!expression.Subplan.Unique && expression.Subplan.DistinctBy != nil &&
			len(expression.Subplan.DistinctBy.Path) == 1 && expression.Subplan.DistinctBy.Path[0] == "_id" {
			return expression.Subplan
		}
		return nil
	}
	if expression.Kind != PhysicalCallExpression || expression.Call == nil || expression.Cardinality != PhysicalScalarCardinality {
		return nil
	}
	if related.Form == "PRESENCE" {
		if expression.Call.Name != "gt" || len(expression.Call.Args) != 2 ||
			expression.Call.Args[1].Kind != PhysicalLiteralExpression {
			return nil
		}
		expression = expression.Call.Args[0]
	}
	if expression.Call == nil || expression.Call.Name != "length" || len(expression.Call.Args) != 1 {
		return nil
	}
	source := expression.Call.Args[0]
	if source.Kind != PhysicalSubplanExpression || source.Subplan == nil ||
		source.Cardinality != PhysicalArrayCardinality || !source.Subplan.Unique || source.Subplan.Sort == nil ||
		source.Subplan.Return.Value == nil || len(source.Subplan.Return.Value.Path) != 1 ||
		source.Subplan.Return.Value.Path[0] != "_id" {
		return nil
	}
	return source.Subplan
}

func physicalRelatedSourceTableScalarKind(logicalType string) string {
	switch strings.ToLower(strings.TrimSpace(logicalType)) {
	case "string", "code", "uuid", "date", "date_time", "datetime":
		return "STRING"
	case "integer":
		return "INTEGER"
	case "decimal":
		return "DECIMAL"
	case "boolean":
		return "BOOLEAN"
	default:
		return ""
	}
}
