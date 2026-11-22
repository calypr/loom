package ir

import (
	"fmt"
	"reflect"
	"strings"
)

func validatePhysicalStageSequence(sequence PhysicalStageSequence, sourceOperations []PhysicalOperation, bindVars map[string]any) error {
	if sequence.SourceStageID == "" || sequence.SourceRowIdentity == "" || sequence.FinalStageID == "" || sequence.FinalRowIdentity == "" {
		return fmt.Errorf("source/final stage IDs and row identity columns are required")
	}
	if len(sequence.Stages) == 0 {
		return fmt.Errorf("at least one construction stage is required")
	}
	if _, ok := bindVars[sequence.PreviewLimitBindKey]; sequence.PreviewLimitBindKey != "" && !ok {
		return fmt.Errorf("preview limit bind %q is missing", sequence.PreviewLimitBindKey)
	}
	if sequence.OutputAuthResourcePathBindKey != "" {
		if !physicalBindKeyPattern.MatchString(sequence.OutputAuthResourcePathBindKey) {
			return fmt.Errorf("private prefix authorization path bind %q is unsafe", sequence.OutputAuthResourcePathBindKey)
		}
		value, ok := bindVars[sequence.OutputAuthResourcePathBindKey].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("private prefix authorization path bind %q is missing or empty", sequence.OutputAuthResourcePathBindKey)
		}
	}
	if sequence.OutputAuthResourcePathColumn != "" {
		if sequence.OutputAuthResourcePathBindKey != "" {
			return fmt.Errorf("authorization path cannot have both row and bind sources")
		}
		found := false
		for _, column := range sequence.FinalColumns {
			if column.Name == sequence.OutputAuthResourcePathColumn && column.Internal && column.Kind == "string" {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("authorization path column is not an internal final string column")
		}
	}
	projectionNames := map[string]bool{}
	for _, operation := range sourceOperations {
		if operation.Kind == PhysicalGroupedPivotOp || operation.Kind == PhysicalUnpivotOp {
			return fmt.Errorf("source projection cannot contain a terminal table reshape")
		}
		if operation.Kind == PhysicalReturnOp && operation.Return != nil {
			for _, projection := range operation.Return.Projections {
				projectionNames[projection.Name] = true
			}
		}
	}
	if err := validatePhysicalStageColumns(sequence.SourceColumns, sequence.SourceRowIdentity); err != nil {
		return fmt.Errorf("source projection schema: %w", err)
	}
	for _, column := range sequence.SourceColumns {
		if !projectionNames[column.Name] {
			return fmt.Errorf("source projection column %q is missing from the typed source RETURN", column.Name)
		}
	}
	priorStageID := sequence.SourceStageID
	priorColumns := sequence.SourceColumns
	priorRowIdentity := sequence.SourceRowIdentity
	seenIDs := map[string]bool{sequence.SourceStageID: true}
	validation := constructionStageValidation{sequence: sequence, sourceOperations: sourceOperations, bindVars: bindVars}
	for index, stage := range sequence.Stages {
		path := fmt.Sprintf("stage[%d]", index)
		if strings.TrimSpace(stage.ID) == "" || stage.ID != strings.TrimSpace(stage.ID) || seenIDs[stage.ID] {
			return fmt.Errorf("%s ID is empty, untrimmed, or duplicated", path)
		}
		seenIDs[stage.ID] = true
		if stage.InputStageID != priorStageID {
			return fmt.Errorf("%s input stage %q does not reference prior stage %q", path, stage.InputStageID, priorStageID)
		}
		if err := validatePhysicalStageColumns(stage.InputColumns, priorRowIdentity); err != nil {
			return fmt.Errorf("%s input schema: %w", path, err)
		}
		if !samePhysicalStageColumns(stage.InputColumns, priorColumns) && !physicalGroupInputColumnsExtendSource(stage, priorColumns) {
			return fmt.Errorf("%s input schema differs from preceding stage output", path)
		}
		if err := validatePhysicalStageColumns(stage.OutputColumns, stage.RowIdentityColumn); err != nil {
			return fmt.Errorf("%s output schema: %w", path, err)
		}
		if err := validateStageVariables(stage); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if stage.Kind != PhysicalStageCodedGroupOp && stage.CodedGroup != nil {
			return fmt.Errorf("%s has a coded-group payload for operation %q", path, stage.Kind)
		}
		if stage.Kind != PhysicalStageCohortGroupOp && stage.CohortGroup != nil {
			return fmt.Errorf("%s has a cohort-group payload for operation %q", path, stage.Kind)
		}
		if err := validation.validateOperation(stage, index, path); err != nil {
			return err
		}
		priorStageID, priorColumns, priorRowIdentity = stage.ID, stage.OutputColumns, stage.RowIdentityColumn
	}
	last := sequence.Stages[len(sequence.Stages)-1]
	if sequence.FinalStageID != last.ID || sequence.FinalRowIdentity != last.RowIdentityColumn {
		return fmt.Errorf("final stage or row identity does not match the final operation")
	}
	if sequence.PreviewTerminalPivotWindow {
		if sequence.PreviewLimitBindKey == "" || sequence.PreviewSourceWindowByRootID ||
			last.Kind != PhysicalStagePivotOp || last.GroupedPivot == nil ||
			last.GroupedPivot.OneInputRowPerGroup || len(last.GroupedPivot.GroupKeys) == 0 ||
			last.OutputRowVariable != last.GroupedPivot.OutputRowVariable {
			return fmt.Errorf("terminal Pivot preview window lacks a terminal nonunique Pivot proof")
		}
	}
	if !samePhysicalStageColumns(sequence.FinalColumns, last.OutputColumns) {
		return fmt.Errorf("final output schema differs from the final stage")
	}
	if sequence.CellTraceReturn != nil {
		if sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil {
			return fmt.Errorf("cell trace, row lineage, and population mapping cannot share a terminal")
		}
		if err := validatePhysicalStageCellTrace(sequence, *sequence.CellTraceReturn, bindVars); err != nil {
			return fmt.Errorf("cell trace: %w", err)
		}
	}
	if sequence.RowLineageReturn != nil {
		if sequence.PopulationMappingReturn != nil {
			return fmt.Errorf("row lineage and population mapping cannot share a terminal")
		}
		if err := validatePhysicalStageRowLineage(sequence, *sequence.RowLineageReturn, sourceOperations, bindVars); err != nil {
			return fmt.Errorf("row lineage: %w", err)
		}
	}
	if sequence.PopulationMappingReturn != nil {
		if err := validatePhysicalStagePopulationMappingReturn(sequence, *sequence.PopulationMappingReturn, sourceOperations); err != nil {
			return fmt.Errorf("population mapping: %w", err)
		}
	}
	return nil
}

func validatePhysicalStagePopulationMappingReturn(sequence PhysicalStageSequence, terminal PhysicalStagePopulationMappingReturn, sourceOperations []PhysicalOperation) error {
	if terminal.RowIdentityColumn == "" || terminal.RowIdentityColumn != sequence.FinalRowIdentity {
		return fmt.Errorf("row identity column does not match the final construction identity")
	}
	if terminal.SourceRootKeyColumn != "_key" || terminal.SourceMemberIDsColumn != PhysicalPopulationMappingMembersColumn || terminal.FinalRootContributorColumn == "" {
		return fmt.Errorf("source member and root-contributor columns are not the compiler-owned mapping fields")
	}
	var identity, sourceRootKey, finalContributors PhysicalStageColumn
	var identityFound, sourceRootKeyFound, finalContributorsFound bool
	for _, column := range sequence.FinalColumns {
		if column.Name == terminal.RowIdentityColumn {
			identity, identityFound = column, true
		}
		if column.Name == terminal.FinalRootContributorColumn {
			finalContributors, finalContributorsFound = column, true
		}
	}
	for _, column := range sequence.SourceColumns {
		if column.Name == terminal.SourceRootKeyColumn {
			sourceRootKey, sourceRootKeyFound = column, true
		}
	}
	if !identityFound || !identity.Internal || !identity.Identity {
		return fmt.Errorf("row identity column %q is not the typed final identity", terminal.RowIdentityColumn)
	}
	if !sourceRootKeyFound || !sourceRootKey.Internal || sourceRootKey.RootContributorResourceType == "" || sourceRootKey.Kind != "string" || sourceRootKey.Cardinality != "required_one" {
		return fmt.Errorf("source root key column %q is not compiler-owned exact root provenance", terminal.SourceRootKeyColumn)
	}
	if !finalContributorsFound || !finalContributors.Internal || finalContributors.RootContributorResourceType != sourceRootKey.RootContributorResourceType || finalContributors.Kind != "string" {
		return fmt.Errorf("final contributor column %q is not compiler-owned provenance for the source root", terminal.FinalRootContributorColumn)
	}
	if terminal.FinalRootContributorsMany != (finalContributors.Cardinality == "many") {
		return fmt.Errorf("final contributor cardinality does not match its typed column")
	}
	if !terminal.FinalRootContributorsMany && finalContributors.Cardinality != "required_one" && finalContributors.Cardinality != "optional_one" {
		return fmt.Errorf("scalar final contributor column has an unsupported cardinality")
	}
	var root *PhysicalRootScan
	for index := range sourceOperations {
		operation := &sourceOperations[index]
		if operation.Kind == PhysicalRootScanOp && operation.RootScan != nil {
			root = operation.RootScan
			break
		}
	}
	if root == nil || root.Population == nil || root.Population.CollectMembersVariable != PopulationMappingMembersVariable {
		return fmt.Errorf("source does not collect exact population member IDs")
	}
	var rootKeyProjection, membersProjection bool
	for _, operation := range sourceOperations {
		if operation.Kind != PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == terminal.SourceRootKeyColumn && projection.Expression == nil &&
				projection.Value.Variable == root.Variable && len(projection.Value.Path) == 1 && projection.Value.Path[0] == "_key" {
				rootKeyProjection = true
			}
			if projection.Name == terminal.SourceMemberIDsColumn && projection.Hidden && projection.Expression == nil &&
				projection.Value.Variable == PopulationMappingMembersVariable && len(projection.Value.Path) == 0 {
				membersProjection = true
			}
		}
	}
	if !rootKeyProjection || !membersProjection {
		return fmt.Errorf("original source RETURN does not preserve exact root keys and selected member IDs")
	}
	return nil
}

func validateCodedPivotSourceRoot(sourceOperations []PhysicalOperation, bindVars map[string]any) (string, string, error) {
	if len(sourceOperations) < 2 || sourceOperations[0].Kind != PhysicalRootScanOp || sourceOperations[0].RootScan == nil {
		return "", "", fmt.Errorf("a direct root scan is required")
	}
	root := sourceOperations[0].RootScan
	resourceType, ok := bindVars[root.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(resourceType) == "" || root.Variable == "" {
		return "", "", fmt.Errorf("root scan collection and variable are required")
	}
	returns := 0
	for index, operation := range sourceOperations {
		switch operation.Kind {
		case PhysicalRootScanOp:
			if index != 0 || operation.RootScan == nil {
				return "", "", fmt.Errorf("exactly one direct root scan is required")
			}
		case PhysicalFilterOp, PhysicalDerivedLetOp, PhysicalExpressionLetOp,
			PhysicalSetOp, PhysicalSortOp, PhysicalLimitOp:

		case PhysicalReturnOp:
			returns++
			if operation.Return == nil || index != len(sourceOperations)-1 {
				return "", "", fmt.Errorf("one terminal root projection is required")
			}
		default:
			return "", "", fmt.Errorf("source operation %q can multiply or replace root rows", operation.Kind)
		}
	}
	if returns != 1 {
		return "", "", fmt.Errorf("one terminal root projection is required")
	}
	return root.Variable, resourceType, nil
}

func validatePhysicalStageExpand(stage PhysicalConstructionStage, expand PhysicalStageExpand, bindVars map[string]any) error {
	if err := requireNonEmptyStringBind(bindVars, expand.ConstructionIDBindKey); err != nil {
		return fmt.Errorf("construction ID: %w", err)
	}
	inputByName, outputByName := physicalStageColumnMap(stage.InputColumns), physicalStageColumnMap(stage.OutputColumns)
	input, ok := inputByName[expand.InputColumn]
	inputKind, inputScalar := physicalStageScalarKind(input.Kind)
	if !ok || input.Internal || input.Cardinality != "many" || !inputScalar || inputKind != expand.InputKind {
		return fmt.Errorf("input column %q must be a public array-valued column", expand.InputColumn)
	}
	item, ok := outputByName[expand.OutputColumn]
	itemKind, itemScalar := physicalStageScalarKind(item.Kind)
	if !ok || item.Internal || !itemScalar || itemKind != expand.InputKind || item.Cardinality != "required_one" && item.Cardinality != "optional_one" {
		return fmt.Errorf("item output column %q does not match the array item type", expand.OutputColumn)
	}
	if expand.OrdinalColumn != "" {
		ordinal, ok := outputByName[expand.OrdinalColumn]
		if !ok || ordinal.Internal || ordinal.Kind != "integer" || ordinal.Cardinality != "required_one" && ordinal.Cardinality != "optional_one" {
			return fmt.Errorf("ordinal output column %q must be an integer scalar", expand.OrdinalColumn)
		}
	}
	switch expand.EmptyPolicy {
	case PhysicalUnnestError, PhysicalUnnestExclude, PhysicalUnnestPreserveParent:
	default:
		return fmt.Errorf("unsupported empty policy %q", expand.EmptyPolicy)
	}
	for _, variable := range []string{expand.ItemsVariable, expand.IndexVariable, expand.ItemVariable, expand.IdentityVariable} {
		if !physicalVariablePattern.MatchString(variable) {
			return fmt.Errorf("variable %q is unsafe", variable)
		}
	}
	defined := map[string]bool{stage.InputRowVariable: true}
	for _, variable := range []string{expand.ItemsVariable, expand.IndexVariable, expand.ItemVariable, expand.IdentityVariable} {
		if err := definePhysicalVariable(defined, variable); err != nil {
			return err
		}
	}
	for index, projection := range stage.OutputProjections {
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("output projection %d (%q): %w", index, projection.Name, err)
		}
	}
	if err := definePhysicalVariable(defined, stage.OutputRowVariable); err != nil {
		return err
	}
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return fmt.Errorf("output projections: %w", err)
	}
	return nil
}

func physicalStageColumnMap(columns []PhysicalStageColumn) map[string]PhysicalStageColumn {
	byName := make(map[string]PhysicalStageColumn, len(columns))
	for _, column := range columns {
		byName[column.Name] = column
	}
	return byName
}

func physicalStageScalarKind(kind string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "string", "date", "datetime", "date_time", "code", "uuid":
		return "STRING", true
	case "integer":
		return "INTEGER", true
	case "decimal":
		return "DECIMAL", true
	case "boolean":
		return "BOOLEAN", true
	default:
		return "", false
	}
}

func requireNonEmptyStringBind(bindVars map[string]any, key string) error {
	if key == "" {
		return fmt.Errorf("bind key is required")
	}
	value, ok := bindVars[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fmt.Errorf("bind %q must be a non-empty string", key)
	}
	return nil
}

func validatePhysicalStageCellTrace(sequence PhysicalStageSequence, terminal PhysicalCellTraceReturn, bindVars map[string]any) error {
	construction := terminal.Construction
	if construction == nil || terminal.Contribution != nil || terminal.Reshape != nil {
		return fmt.Errorf("construction lineage is required and must be the only contributor contract")
	}
	if construction.RelatedSource != nil && len(construction.Inputs) != 0 {
		return fmt.Errorf("related-source lineage cannot also contain construction input lineage")
	}
	if construction.Operation == string(PhysicalStageRelatedSourceOp) && construction.RelatedSource == nil && construction.OmissionCode == "" {
		return fmt.Errorf("complete RELATED_SOURCE lineage requires its typed route subplan")
	}
	for _, key := range []string{terminal.OffsetBindKey, terminal.LimitBindKey, terminal.FetchLimitBindKey} {
		if err := requireBind(bindVars, key); err != nil {
			return err
		}
	}
	if construction.FinalStageID != sequence.FinalStageID || construction.RowIdentityColumn != sequence.FinalRowIdentity {
		return fmt.Errorf("final stage or row identity does not match the construction sequence")
	}
	if !validConstructionCellTraceIdentityFields(sequence, construction.RowIdentityFields) {
		return fmt.Errorf("construction trace identity fields do not match the published row identity")
	}
	if len(construction.RowIdentityFields) == 2 {
		if err := requireNonEmptyStringBind(bindVars, "project"); err != nil {
			return fmt.Errorf("canonical project/key construction identity: %w", err)
		}
	}
	finalColumns := stageColumnsByID(sequence.FinalColumns)
	output, exists := finalColumns[construction.OutputColumnID]
	if !exists || output.Name != construction.OutputColumn || output.Internal {
		return fmt.Errorf("output column ID %q does not identify a public final column", construction.OutputColumnID)
	}
	if construction.OmissionCode == "" && len(construction.Inputs) == 0 && construction.RelatedSource == nil {
		return fmt.Errorf("complete construction lineage requires source columns or a related-source route")
	}
	producerInput := map[string]PhysicalStageColumn{}
	expectedInputStageID := sequence.SourceStageID
	producerFound := construction.ProducerStageID == sequence.SourceStageID && construction.Operation == "SOURCE_PROJECTION"
	if producerFound {
		producerInput = stageColumnsByID(sequence.SourceColumns)
	} else {
		for _, stage := range sequence.Stages {
			if stage.ID != construction.ProducerStageID {
				continue
			}
			producerFound = true
			if string(stage.Kind) != construction.Operation {
				return fmt.Errorf("producer operation %q does not match stage %q kind %q", construction.Operation, stage.ID, stage.Kind)
			}
			expectedInputStageID = stage.InputStageID
			producerInput = stageColumnsByID(stage.InputColumns)
			if _, ok := stageColumnsByID(stage.OutputColumns)[construction.OutputColumnID]; !ok {
				return fmt.Errorf("output column ID %q is absent from producer stage %q", construction.OutputColumnID, stage.ID)
			}
			if construction.RelatedSource != nil {
				if stage.Kind != PhysicalStageRelatedSourceOp || stage.RelatedSource == nil || construction.Operation != string(PhysicalStageRelatedSourceOp) {
					return fmt.Errorf("related-source lineage must be produced by a RELATED_SOURCE stage")
				}
				projection, found := findPhysicalStageProjection(stage.OutputProjections, construction.OutputColumn)
				if !found || projection.Expression == nil || relatedSourceProjectionSubplan(*projection.Expression, *stage.RelatedSource) == nil {
					return fmt.Errorf("related-source output has no typed route subplan")
				}
				subplan := relatedSourceProjectionSubplan(*projection.Expression, *stage.RelatedSource)
				related := construction.RelatedSource
				anchor, found := stageColumnsByID(stage.InputColumns)[stage.RelatedSource.AnchorColumnID]
				if !found || related.InputRowVariable != stage.InputRowVariable || related.AnchorColumn != anchor.Name ||
					related.ResourceType != stage.RelatedSource.ResourceType || !reflect.DeepEqual(related.Subplan, *subplan) {
					return fmt.Errorf("related-source trace must retain the producer's exact scoped route subplan")
				}
			}
			break
		}
	}
	if !producerFound {
		return fmt.Errorf("producer stage %q is absent from the construction sequence", construction.ProducerStageID)
	}
	seenInputs := map[string]bool{}
	for _, input := range construction.Inputs {
		stageColumn, ok := producerInput[input.ColumnID]
		if !ok || stageColumn.Name != input.Column || stageColumn.Internal || input.StageID != expectedInputStageID {
			return fmt.Errorf("input column ID %q is not a public producer input", input.ColumnID)
		}
		if seenInputs[input.ColumnID] {
			return fmt.Errorf("input column ID %q is duplicated", input.ColumnID)
		}
		seenInputs[input.ColumnID] = true
		finalColumn, ok := finalColumns[input.ColumnID]
		if !ok || finalColumn.Internal || finalColumn.Name != input.FinalValueColumn {
			return fmt.Errorf("input column ID %q does not survive in the final output", input.ColumnID)
		}
	}
	return nil
}

func validConstructionCellTraceIdentityFields(sequence PhysicalStageSequence, fields []string) bool {
	if len(fields) == 1 {
		return fields[0] != "" && fields[0] == sequence.FinalRowIdentity
	}
	return sequence.FinalRowIdentity == "_key" && len(fields) == 2 && fields[0] == "project" && fields[1] == "_key"
}

func findPhysicalStageProjection(projections []PhysicalProjection, name string) (PhysicalProjection, bool) {
	for _, projection := range projections {
		if projection.Name == name {
			return projection, true
		}
	}
	return PhysicalProjection{}, false
}

func stageColumnsByID(columns []PhysicalStageColumn) map[string]PhysicalStageColumn {
	result := make(map[string]PhysicalStageColumn, len(columns))
	for _, column := range columns {
		result[column.ID] = column
	}
	return result
}

func validatePhysicalStageColumns(columns []PhysicalStageColumn, identityName string) error {
	if len(columns) == 0 || strings.TrimSpace(identityName) == "" {
		return fmt.Errorf("columns and row identity are required")
	}
	ids, names := map[string]bool{}, map[string]bool{}
	identityFound := false
	for _, column := range columns {
		if strings.TrimSpace(column.ID) == "" || column.ID != strings.TrimSpace(column.ID) || ids[column.ID] {
			return fmt.Errorf("column ID %q is empty, untrimmed, or duplicated", column.ID)
		}
		if !physicalPathPartPattern.MatchString(column.Name) || names[column.Name] {
			return fmt.Errorf("column name %q is unsafe or duplicated", column.Name)
		}
		if strings.TrimSpace(column.Kind) == "" || strings.TrimSpace(column.Cardinality) == "" {
			return fmt.Errorf("column %q requires logical kind and cardinality", column.Name)
		}
		if column.RootContributorResourceType != "" {
			validScalar := column.Name == "_key" && column.Kind == "string" &&
				(column.Cardinality == "required_one" || column.Cardinality == "optional_one")
			validSet := column.Name == "__loom_root_contributor_keys" && column.Kind == "string" && column.Cardinality == "many"
			if !column.Internal || !schemaDefinitionExists(column.RootContributorResourceType) || !(validScalar || validSet) {
				return fmt.Errorf("root contributor column %q must be a hidden root identity or key set for a generated resource type", column.Name)
			}
		}
		if column.RelatedRecordAnchor != nil && (!column.Internal || column.Identity || column.Kind != "string" ||
			(column.Cardinality != "required_one" && column.Cardinality != "optional_one") ||
			strings.TrimSpace(column.RelatedRecordAnchor.NodeID) == "" || strings.TrimSpace(column.RelatedRecordAnchor.ResourceType) == "") {
			return fmt.Errorf("related-record anchor column %q must be a hidden scalar terminal identity with exact node and type metadata", column.Name)
		}
		ids[column.ID], names[column.Name] = true, true
		if column.Name == identityName {
			if !column.Internal || !column.Identity {
				return fmt.Errorf("row identity column %q must be internal and identity-marked", identityName)
			}
			identityFound = true
		}
	}
	if !identityFound {
		return fmt.Errorf("row identity column %q is absent from schema", identityName)
	}
	return nil
}

func samePhysicalStageColumns(left, right []PhysicalStageColumn) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		a, b := left[index], right[index]
		if a.ID != b.ID || a.Name != b.Name || a.Label != b.Label || a.Kind != b.Kind || a.Cardinality != b.Cardinality || a.Nullable != b.Nullable || a.Internal != b.Internal || a.Identity != b.Identity ||
			a.RootContributorResourceType != b.RootContributorResourceType || !reflect.DeepEqual(a.RelatedRecordAnchor, b.RelatedRecordAnchor) {
			return false
		}
	}
	return true
}

func physicalGroupInputColumnsExtendSource(stage PhysicalConstructionStage, prior []PhysicalStageColumn) bool {
	if stage.Kind != PhysicalStageGroupOp || stage.Group == nil || stage.Group.RelatedOccurrence == nil || len(stage.InputColumns) <= len(prior) ||
		!samePhysicalStageColumns(stage.InputColumns[:len(prior)], prior) {
		return false
	}
	adapter := stage.Group.RelatedOccurrence
	expected := make(map[string]bool, len(adapter.ScalarProjections)+2)
	for _, projection := range adapter.ScalarProjections {
		if projection.InputColumn == "" || expected[projection.InputColumn] {
			return false
		}
		expected[projection.InputColumn] = true
	}
	if adapter.CodingMembershipColumn != "" {
		if expected[adapter.CodingMembershipColumn] {
			return false
		}
		expected[adapter.CodingMembershipColumn] = true
	}
	if adapter.OccurrenceKeyColumn != "" {
		if expected[adapter.OccurrenceKeyColumn] {
			return false
		}
		expected[adapter.OccurrenceKeyColumn] = true
	}
	if len(stage.InputColumns)-len(prior) != len(expected) {
		return false
	}
	for _, column := range stage.InputColumns[len(prior):] {
		if !expected[column.Name] || !column.Internal || column.ID != column.Name {
			return false
		}
		delete(expected, column.Name)
	}
	return len(expected) == 0
}

func validateStageVariables(stage PhysicalConstructionStage) error {
	if !physicalVariablePattern.MatchString(stage.InputRowVariable) || !physicalVariablePattern.MatchString(stage.OutputRowVariable) || stage.InputRowVariable == stage.OutputRowVariable {
		return fmt.Errorf("input and output row variables must be safe and distinct")
	}
	return nil
}

func validateStageRowOperations(stage PhysicalConstructionStage, bindVars map[string]any, expectFilter bool) error {
	defined := map[string]bool{stage.InputRowVariable: true}
	for index, operation := range stage.DerivedLets {
		if operation.Kind != PhysicalExpressionLetOp || operation.ExpressionLet == nil {
			return fmt.Errorf("derived operation %d must be a typed expression LET", index)
		}
		if err := validatePhysicalExpression(operation.ExpressionLet.Expression, defined, bindVars); err != nil {
			return fmt.Errorf("derived operation %d: %w", index, err)
		}
		if err := definePhysicalVariable(defined, operation.ExpressionLet.Variable); err != nil {
			return err
		}
	}
	if expectFilter {
		if err := validatePhysicalFilter(*stage.Filter, defined, bindVars); err != nil {
			return err
		}
	}
	for index, projection := range stage.OutputProjections {
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("output projection %d (%q): %w", index, projection.Name, err)
		}
	}
	if err := definePhysicalVariable(defined, stage.OutputRowVariable); err != nil {
		return err
	}
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return err
	}
	return nil
}

func validateStageProjectionNames(projections []PhysicalProjection, columns []PhysicalStageColumn) error {
	if len(projections) != len(columns) {
		return fmt.Errorf("output projection count %d does not match schema count %d", len(projections), len(columns))
	}
	for index := range projections {
		if projections[index].Name != columns[index].Name || projections[index].Hidden != columns[index].Internal {
			return fmt.Errorf("output projection %d does not match schema column %q", index, columns[index].Name)
		}
	}
	return nil
}
