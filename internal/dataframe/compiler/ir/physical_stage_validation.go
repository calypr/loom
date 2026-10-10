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
		if !samePhysicalStageColumns(stage.InputColumns, priorColumns) {
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
		switch stage.Kind {
		case PhysicalStageDeriveOp:
			if stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedField != nil {
				return fmt.Errorf("%s DERIVE has mismatched operation payloads", path)
			}
			if len(stage.DerivedLets) == 0 {
				return fmt.Errorf("%s DERIVE requires at least one typed expression LET", path)
			}
			if err := validateStageRowOperations(stage, bindVars, false); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageFilterOp:
			if stage.Filter == nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s FILTER requires only a filter payload", path)
			}
			if err := validateStageRowOperations(stage, bindVars, true); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStagePivotOp:
			if stage.GroupedPivot == nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s PIVOT requires only a grouped-pivot payload", path)
			}
			if stage.OutputRowVariable != stage.GroupedPivot.OutputRowVariable {
				return fmt.Errorf("%s output row variable does not match grouped pivot", path)
			}
			defined := map[string]bool{stage.InputRowVariable: true}
			if stage.GroupedPivot.CodedCorrelation != nil {
				if index != 0 || stage.InputStageID != sequence.SourceStageID {
					return fmt.Errorf("%s coded Pivot must be the first stage over the direct source projection", path)
				}
				rootVariable, resourceType, rootErr := validateCodedPivotSourceRoot(sourceOperations, bindVars)
				if rootErr != nil {
					return fmt.Errorf("%s coded Pivot source: %w", path, rootErr)
				}
				if rootVariable != stage.GroupedPivot.CodedSourceVariable || resourceType != stage.GroupedPivot.CodedCorrelation.ResourceType {
					return fmt.Errorf("%s coded Pivot source does not match the exact root scan", path)
				}
				defined[rootVariable] = true
			}
			if err := validatePhysicalGroupedPivot(*stage.GroupedPivot, defined, bindVars); err != nil {
				return fmt.Errorf("%s grouped pivot: %w", path, err)
			}
			if err := validateRootContributorReduction(
				stage.GroupedPivot.RootContributorInputColumn,
				stage.GroupedPivot.RootContributorInputMany,
				stage.GroupedPivot.RootContributorOutputColumn,
				stage.GroupedPivot.RootContributorVariable,
				stage.InputColumns, stage.OutputColumns, defined,
			); err != nil {
				return fmt.Errorf("%s grouped pivot root contributors: %w", path, err)
			}
			if err := validatePhysicalStageRowValues(
				stage.GroupedPivot.RowValues, stage.InputColumns, stage.OutputColumns,
				physicalProjectionNameSet(stage.GroupedPivot.InputProjections), groupedPivotBaseOutputs(*stage.GroupedPivot), nil,
			); err != nil {
				return fmt.Errorf("%s grouped pivot row values: %w", path, err)
			}
			if err := validateShapeStageOutput(stage, groupedPivotOutputNames(*stage.GroupedPivot)); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageUnpivotOp:
			if stage.Unpivot == nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s UNPIVOT requires only an unpivot payload", path)
			}
			if stage.OutputRowVariable != stage.Unpivot.OutputRowVariable {
				return fmt.Errorf("%s output row variable does not match unpivot", path)
			}
			defined := map[string]bool{stage.InputRowVariable: true}
			if err := validatePhysicalUnpivot(*stage.Unpivot, defined, bindVars); err != nil {
				return fmt.Errorf("%s unpivot: %w", path, err)
			}
			if err := validateShapeStageOutput(stage, unpivotOutputNames(*stage.Unpivot)); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageGroupOp:
			if stage.Group == nil || stage.Filter != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s GROUP requires only a group payload", path)
			}
			if err := validatePhysicalStageGroup(stage, *stage.Group, bindVars); err != nil {
				return fmt.Errorf("%s group: %w", path, err)
			}
			expected := make([]string, 0, len(stage.Group.Keys)+len(stage.Group.Aggregates)+len(stage.Group.RowValues)+2)
			for _, key := range stage.Group.Keys {
				expected = append(expected, key.OutputColumn)
			}
			for _, aggregate := range stage.Group.Aggregates {
				expected = append(expected, aggregate.Output)
			}
			for _, rowValue := range stage.Group.RowValues {
				expected = append(expected, rowValue.Output)
			}
			if stage.Group.RootContributorOutputColumn != "" {
				expected = append(expected, stage.Group.RootContributorOutputColumn)
			}
			expected = append(expected, "__loom_row_id")
			if err := validateShapeStageOutput(stage, expected); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageCodedGroupOp:
			if stage.CodedGroup == nil || stage.Group != nil || stage.Filter != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedSource != nil || stage.RelatedExpand != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s CODED_GROUP requires only a coded-group payload", path)
			}
			if err := validatePhysicalStageCodedGroup(stage, *stage.CodedGroup, bindVars, sourceOperations); err != nil {
				return fmt.Errorf("%s coded group: %w", path, err)
			}
			expected := []string{stage.CodedGroup.SystemOutputColumn, stage.CodedGroup.VersionOutputColumn, stage.CodedGroup.CodeOutputColumn, stage.CodedGroup.CountOutputColumn}
			for _, rowValue := range stage.CodedGroup.RowValues {
				expected = append(expected, rowValue.Output)
			}
			expected = append(expected, "__loom_row_id")
			if err := validateShapeStageOutput(stage, expected); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageCohortGroupOp:
			if stage.CohortGroup == nil || stage.Group != nil || stage.CodedGroup != nil || stage.Filter != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 || len(stage.OutputProjections) != 0 {
				return fmt.Errorf("%s COHORT_GROUP requires only a cohort-group payload", path)
			}
			if err := validatePhysicalStageCohortGroup(stage, *stage.CohortGroup, bindVars); err != nil {
				return fmt.Errorf("%s cohort group: %w", path, err)
			}
			expected := []string{"group_revision_id", "group_id", "group_label", "group_ordinal", "members"}
			for _, value := range stage.CohortGroup.Rows.MemberValues {
				expected = append(expected, value.Output)
			}
			expected = append(expected, stage.CohortGroup.RootContributorOutputColumn, "__loom_row_id")
			if err := validateShapeStageOutput(stage, expected); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageExpandOp:
			if stage.Expand == nil || stage.Filter != nil || stage.Group != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s EXPAND requires only an expand payload", path)
			}
			if err := validatePhysicalStageExpand(stage, *stage.Expand, bindVars); err != nil {
				return fmt.Errorf("%s expand: %w", path, err)
			}
			expected := make([]string, 0, len(stage.InputColumns)+1)
			for _, column := range stage.InputColumns {
				if column.Internal || column.Name == stage.Expand.InputColumn {
					continue
				}
				expected = append(expected, column.Name)
			}
			expected = append(expected, stage.Expand.OutputColumn)
			if stage.Expand.OrdinalColumn != "" {
				expected = append(expected, stage.Expand.OrdinalColumn)
			}
			expected = append(expected, "__loom_row_id")
			if err := validateShapeStageOutput(stage, expected); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageRelatedSourceOp:
			if stage.RelatedSource == nil || stage.RelatedExpand != nil || stage.RelatedField != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s RELATED_SOURCE requires only a related-source payload", path)
			}
			if err := validatePhysicalStageRelatedSource(stage, *stage.RelatedSource, bindVars); err != nil {
				return fmt.Errorf("%s related source: %w", path, err)
			}
		case PhysicalStageRelatedExpandOp:
			if stage.RelatedExpand == nil || stage.RelatedSource != nil || stage.RelatedField != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s RELATED_EXPAND requires only a related-expansion payload", path)
			}
			if err := validatePhysicalStageRelatedExpand(stage, *stage.RelatedExpand, bindVars); err != nil {
				return fmt.Errorf("%s related expansion: %w", path, err)
			}
		case PhysicalStageRelatedEligibilityOp:
			if stage.Filter == nil || stage.RelatedSource != nil || stage.RelatedExpand != nil || stage.RelatedField != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil {
				return fmt.Errorf("%s RELATED_ELIGIBILITY requires only a filter and optional typed count LET", path)
			}
			if err := validatePhysicalStageRelatedEligibility(stage, bindVars); err != nil {
				return fmt.Errorf("%s related eligibility: %w", path, err)
			}
			if err := validateStageRowOperations(stage, bindVars, true); err != nil {
				return err
			}
		case PhysicalStageRelatedFieldOp:
			if stage.RelatedField == nil || stage.RelatedExpand != nil || stage.RelatedSource != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s RELATED_FIELD requires only a related-field payload", path)
			}
			if err := validatePhysicalStageRelatedField(stage, *stage.RelatedField, bindVars); err != nil {
				return fmt.Errorf("%s related field: %w", path, err)
			}
		default:
			return fmt.Errorf("%s has unsupported operation kind %q", path, stage.Kind)
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
			// These source operations preserve root row grain.
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

func validatePhysicalStageRowLineage(sequence PhysicalStageSequence, terminal PhysicalRowLineageReturn, sourceOperations []PhysicalOperation, bindVars map[string]any) error {
	if len(sequence.Stages) == 0 {
		return fmt.Errorf("row lineage requires a construction stage")
	}
	stage := sequence.Stages[0]
	lastStage := sequence.Stages[len(sequence.Stages)-1]
	if stage.InputStageID != sequence.SourceStageID || lastStage.ID != sequence.FinalStageID || lastStage.RowIdentityColumn != sequence.FinalRowIdentity {
		return fmt.Errorf("row lineage requires a supported construction sequence over the direct source projection")
	}
	if terminal.Trace != nil {
		var err error
		if terminal.Trace.RootKeyBindKey == "" {
			err = validatePhysicalConstructionPivotLineageTrace(sequence, terminal, *terminal.Trace, sourceOperations, bindVars)
		} else {
			err = validatePhysicalRelatedRowLineageTrace(sequence, terminal, *terminal.Trace, sourceOperations, bindVars)
		}
		if err != nil {
			return err
		}
	} else {
		switch stage.Kind {
		case PhysicalStageGroupOp:
			if stage.Group == nil {
				return fmt.Errorf("row lineage Group payload is invalid")
			}
			if err := validatePhysicalGroupFilterLineage(sequence.Stages); err != nil {
				return err
			}
		case PhysicalStageCodedGroupOp:
			if len(sequence.Stages) != 1 {
				return fmt.Errorf("row lineage CODED_GROUP requires one terminal construction stage")
			}
			coded := stage.CodedGroup
			if coded == nil || sequence.SourceRowIdentity != "_key" || coded.SourceIdentityColumn != "_key" ||
				terminal.ResourceType != coded.ResourceType {
				return fmt.Errorf("row lineage CODED_GROUP requires direct root identity and no related-row bindings")
			}
		default:
			return fmt.Errorf("row lineage requires a terminal Group, CODED_GROUP, or RELATED_EXPAND Trace")
		}
	}
	if terminal.ResourceType == "" || terminal.ResourceIDColumn == "" || terminal.OccurrenceKeyColumn == "" {
		return fmt.Errorf("resource type, source resource ID, and occurrence key columns are required")
	}
	for _, key := range []string{terminal.RowIDBindKey, terminal.OffsetBindKey, terminal.LimitBindKey, terminal.FetchLimitBindKey} {
		if err := requireBind(bindVars, key); err != nil {
			return err
		}
	}
	if _, ok := bindVars[terminal.RowIDBindKey].(string); !ok {
		return fmt.Errorf("row ID bind %q must be a string", terminal.RowIDBindKey)
	}
	offset, offsetOK := bindVars[terminal.OffsetBindKey].(int)
	limit, limitOK := bindVars[terminal.LimitBindKey].(int)
	fetchLimit, fetchOK := bindVars[terminal.FetchLimitBindKey].(int)
	if !offsetOK || offset < 0 || !limitOK || limit < 1 || limit > 100 || !fetchOK || fetchLimit != limit+1 {
		return fmt.Errorf("row lineage requires a nonnegative offset and a page of 1 through 100 plus one row")
	}
	return nil
}

func validatePhysicalConstructionPivotLineageTrace(
	sequence PhysicalStageSequence,
	terminal PhysicalRowLineageReturn,
	trace PhysicalRowLineageTrace,
	sourceOperations []PhysicalOperation,
	bindVars map[string]any,
) error {
	if sequence.SourceRowIdentity != "_key" || len(trace.Stages) != 1 {
		return fmt.Errorf("construction Pivot lineage requires one terminal typed owner over direct root identity")
	}
	var stage PhysicalConstructionStage
	switch len(sequence.Stages) {
	case 1:
		stage = sequence.Stages[0]
		if stage.InputStageID != sequence.SourceStageID {
			return fmt.Errorf("construction Pivot lineage requires a terminal direct-source Pivot")
		}
	case 2:
		groupStage := sequence.Stages[0]
		stage = sequence.Stages[1]
		if err := validatePhysicalCountRowsGroupPivotLineageStages(sequence, groupStage, stage); err != nil {
			return err
		}
	default:
		return fmt.Errorf("construction Pivot lineage supports only a direct Pivot or COUNT_ROWS Group followed by Pivot")
	}
	match := trace.Stages[0]
	pivot := stage.GroupedPivot
	if stage.Kind != PhysicalStagePivotOp || pivot == nil || pivot.CodedCorrelation != nil ||
		stage.ID != sequence.FinalStageID ||
		match.StageID != stage.ID || match.Kind != PhysicalStagePivotOp ||
		match.StageRowIDBindKey != terminal.RowIDBindKey || match.RelatedTerminalIDBindKey != "" ||
		match.RelatedRowKind != "" || len(match.IdentityKeyBindKeys) != len(pivot.GroupKeys) {
		return fmt.Errorf("construction Pivot lineage owner does not match its typed stage")
	}
	_, sourceType, err := validateCodedPivotSourceRoot(sourceOperations, bindVars)
	if err != nil {
		return fmt.Errorf("construction Pivot lineage source: %w", err)
	}
	if sourceType != terminal.ResourceType {
		return fmt.Errorf("construction Pivot source resource %q differs from terminal resource %q", sourceType, terminal.ResourceType)
	}
	if _, ok := bindVars[match.StageRowIDBindKey].(string); !ok {
		return fmt.Errorf("construction Pivot row identity bind %q must be a string", match.StageRowIDBindKey)
	}
	seen := map[string]bool{match.StageRowIDBindKey: true}
	for index, key := range pivot.GroupKeys {
		bindKey := match.IdentityKeyBindKeys[index]
		if bindKey == "" || seen[bindKey] {
			return fmt.Errorf("construction Pivot key bind %q is empty or duplicated", bindKey)
		}
		seen[bindKey] = true
		if err := requireBind(bindVars, bindKey); err != nil {
			return err
		}
		value := bindVars[bindKey]
		valid := value == nil
		if value != nil {
			switch key.Kind {
			case "STRING":
				_, valid = value.(string)
			case "INTEGER":
				switch value.(type) {
				case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
					valid = true
				}
			case "DECIMAL":
				switch value.(type) {
				case float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
					valid = true
				}
			case "BOOLEAN":
				_, valid = value.(bool)
			default:
				valid = false
			}
		}
		if !valid {
			return fmt.Errorf("construction Pivot key bind %q has the wrong type for %q", bindKey, key.Kind)
		}
	}
	return nil
}

func validatePhysicalCountRowsGroupPivotLineageStages(
	sequence PhysicalStageSequence,
	groupStage PhysicalConstructionStage,
	pivotStage PhysicalConstructionStage,
) error {
	group, pivot := groupStage.Group, pivotStage.GroupedPivot
	if groupStage.Kind != PhysicalStageGroupOp || group == nil || groupStage.InputStageID != sequence.SourceStageID ||
		pivotStage.Kind != PhysicalStagePivotOp || pivot == nil || pivotStage.InputStageID != groupStage.ID ||
		pivotStage.ID != sequence.FinalStageID || len(group.Aggregates) != 1 ||
		group.Aggregates[0].Operation != "COUNT_ROWS" || group.Aggregates[0].InputColumn != "" ||
		group.Aggregates[0].InputKind != "" || group.Aggregates[0].OutputKind != "integer" ||
		len(group.RowValues) != 0 || len(group.Keys) != len(pivot.GroupKeys)+1 ||
		pivot.ValueColumn != group.Aggregates[0].Output || pivot.ValueType != "INTEGER" ||
		pivot.CategoryPresence != nil || pivot.CodedCorrelation != nil || len(pivot.GroupKeys) == 0 ||
		len(pivot.Categories) == 0 || len(pivot.RowValues) != 0 || pivot.UnlistedEvidenceColumn != "" || pivot.UnlistedCategoryPolicy != "ERROR" ||
		(pivot.DuplicatePolicy != "ERROR" && pivot.DuplicatePolicy != "SUM" && pivot.DuplicatePolicy != "MIN" && pivot.DuplicatePolicy != "MAX") ||
		(pivot.MissingCellPolicy != "ERROR" && pivot.MissingCellPolicy != "NULL") {
		return fmt.Errorf("composed construction Pivot lineage requires direct-source scalar COUNT_ROWS Group followed by an ordinary Pivot")
	}
	if group.MissingKeyPolicy != PhysicalStageGroupMissingKeyGroup &&
		group.MissingKeyPolicy != PhysicalStageGroupMissingKeyExclude &&
		group.MissingKeyPolicy != PhysicalStageGroupMissingKeyError {
		return fmt.Errorf("COUNT_ROWS Group lineage has unsupported missing-key policy %q", group.MissingKeyPolicy)
	}
	groupKeys := make(map[string]PhysicalStageGroupKey, len(group.Keys))
	for _, key := range group.Keys {
		if key.InputColumn == "" || key.OutputColumn == "" || key.Variable == "" || groupKeys[key.OutputColumn].OutputColumn != "" {
			return fmt.Errorf("COUNT_ROWS Group lineage has an empty or duplicate typed key")
		}
		groupKeys[key.OutputColumn] = key
	}
	category, ok := groupKeys[pivot.CategoryColumn]
	if !ok || category.Kind != pivot.CategoryType {
		return fmt.Errorf("COUNT_ROWS Group category %q does not match the Pivot category type", pivot.CategoryColumn)
	}
	delete(groupKeys, pivot.CategoryColumn)
	for _, key := range pivot.GroupKeys {
		groupKey, exists := groupKeys[key.Column]
		if !exists || groupKey.Kind != key.Kind {
			return fmt.Errorf("Pivot group key %q does not match a remaining COUNT_ROWS Group key", key.Column)
		}
		delete(groupKeys, key.Column)
	}
	if len(groupKeys) != 0 {
		return fmt.Errorf("COUNT_ROWS Group lineage requires every Group key to be the Pivot category or a Pivot row key")
	}
	return nil
}

func validatePhysicalRelatedRowLineageTrace(sequence PhysicalStageSequence, terminal PhysicalRowLineageReturn, trace PhysicalRowLineageTrace, sourceOperations []PhysicalOperation, bindVars map[string]any) error {
	if sequence.SourceRowIdentity != "_key" || trace.RootKeyBindKey == "" {
		return fmt.Errorf("composed RELATED_EXPAND lineage requires a direct root _key anchor")
	}
	if value, ok := bindVars[trace.RootKeyBindKey]; !ok {
		return fmt.Errorf("row lineage root key bind %q is required", trace.RootKeyBindKey)
	} else if value != nil {
		if key, ok := value.(string); !ok || key == "" {
			return fmt.Errorf("row lineage root key bind %q must be a string or null", trace.RootKeyBindKey)
		}
	}
	rootVariable, sourceType, err := validateCodedPivotSourceRoot(sourceOperations, bindVars)
	if err != nil {
		return fmt.Errorf("composed RELATED_EXPAND lineage source: %w", err)
	}
	if sourceType != terminal.ResourceType {
		return fmt.Errorf("row lineage source resource %q differs from terminal resource %q", sourceType, terminal.ResourceType)
	}
	rootKeyFilters, returnIndex := 0, -1
	for index, operation := range sourceOperations {
		if operation.Kind == PhysicalReturnOp {
			returnIndex = index
		}
		if operation.Kind == PhysicalFilterOp && physicalRowLineageRootKeyFilter(operation.Filter, rootVariable, trace.RootKeyBindKey) {
			rootKeyFilters++
			if returnIndex >= 0 {
				return fmt.Errorf("row lineage root key filter must precede the source projection")
			}
		}
	}
	if returnIndex < 0 || rootKeyFilters != 1 {
		return fmt.Errorf("row lineage requires exactly one indexed root _key filter bound to %q before source projection", trace.RootKeyBindKey)
	}

	owners := 0
	for _, stage := range sequence.Stages {
		if stage.Kind == PhysicalStageRelatedExpandOp {
			owners++
		}
	}
	if owners == 0 || len(trace.Stages) != owners {
		return fmt.Errorf("row lineage trace must name every authored RELATED_EXPAND stage")
	}
	seenBinds := map[string]bool{trace.RootKeyBindKey: true}
	traceIndex := 0
	priorIdentity := sequence.SourceRowIdentity
	for _, stage := range sequence.Stages {
		switch stage.Kind {
		case PhysicalStageDeriveOp, PhysicalStageFilterOp, PhysicalStageRelatedSourceOp, PhysicalStageRelatedEligibilityOp, PhysicalStageRelatedFieldOp:
			if stage.RowIdentityColumn != priorIdentity {
				return fmt.Errorf("row lineage identity-preserving stage %q changes row identity", stage.ID)
			}
			projectionFound := false
			for _, projection := range stage.OutputProjections {
				if projection.Name != stage.RowIdentityColumn {
					continue
				}
				projectionFound = projection.Value.Variable == stage.InputRowVariable && reflect.DeepEqual(projection.Value.Path, []string{priorIdentity})
				break
			}
			if !projectionFound {
				return fmt.Errorf("row lineage identity-preserving stage %q must pass through its exact row identity", stage.ID)
			}
		case PhysicalStageRelatedExpandOp:
			related := stage.RelatedExpand
			if related == nil {
				return fmt.Errorf("row lineage RELATED_EXPAND stage %q has no typed payload", stage.ID)
			}
			if related.AnchorKind != "root" && related.AnchorKind != "activeRelatedRecord" {
				return fmt.Errorf("row lineage RELATED_EXPAND stage %q has an unsupported composed anchor", stage.ID)
			}
			if related.AnchorKind == "root" && (related.AnchorColumnID != "_key" || related.ParentIdentityColumn != "_key") {
				return fmt.Errorf("row lineage root anchor at stage %q must select root _key", stage.ID)
			}
			match := trace.Stages[traceIndex]
			if match.StageID != stage.ID || match.Kind != PhysicalStageRelatedExpandOp {
				return fmt.Errorf("row lineage trace stage %d does not match authored stage %q", traceIndex, stage.ID)
			}
			if match.StageRowIDBindKey == "" || match.RelatedTerminalIDBindKey == "" || len(match.IdentityKeyBindKeys) != 0 ||
				(match.RelatedRowKind != "RELATED" && match.RelatedRowKind != "EMPTY") {
				return fmt.Errorf("row lineage trace stage %q requires exact row and related terminal bindings", stage.ID)
			}
			for _, key := range []string{match.StageRowIDBindKey, match.RelatedTerminalIDBindKey} {
				if seenBinds[key] {
					return fmt.Errorf("row lineage trace bind %q is duplicated", key)
				}
				seenBinds[key] = true
			}
			if rowID, ok := bindVars[match.StageRowIDBindKey].(string); !ok || rowID == "" {
				return fmt.Errorf("row lineage stage row ID bind %q must be a nonempty string", match.StageRowIDBindKey)
			}
			terminalID, ok := bindVars[match.RelatedTerminalIDBindKey].(string)
			if !ok || match.RelatedRowKind == "RELATED" && bindVars[trace.RootKeyBindKey] != nil && terminalID == "" {
				return fmt.Errorf("row lineage related terminal ID bind %q must be a string and nonempty for a valid root", match.RelatedTerminalIDBindKey)
			}
			if match.RelatedRowKind == "EMPTY" && related.EmptyPolicy != PhysicalUnnestPreserveParent {
				return fmt.Errorf("row lineage empty stage %q requires PRESERVE_PARENT", stage.ID)
			}
			if match.RelatedRowKind == "RELATED" && bindVars[trace.RootKeyBindKey] != nil {
				collection, key, found := strings.Cut(terminalID, "/")
				if !found || collection != related.TargetResourceType || key == "" {
					return fmt.Errorf("row lineage terminal identity %q is malformed", terminalID)
				}
			}
			traceIndex++
		default:
			return fmt.Errorf("row lineage does not support identity-changing stage %q", stage.Kind)
		}
		priorIdentity = stage.RowIdentityColumn
	}
	if traceIndex != len(trace.Stages) {
		return fmt.Errorf("row lineage trace has unmatched authored stages")
	}
	return nil
}

func physicalRowLineageRootKeyFilter(filter *PhysicalFilter, rootVariable, rootKeyBindKey string) bool {
	if filter == nil || filter.Expression != nil {
		return false
	}
	predicate := filter.Predicate
	return predicate.Operator == "EQUALS" && predicate.LeftExpression == nil && predicate.Correlation == nil &&
		predicate.Left.Variable == rootVariable && reflect.DeepEqual(predicate.Left.Path, []string{"_key"}) &&
		predicate.Right != nil && predicate.Right.BindKey == rootKeyBindKey && predicate.Right.Variable == "" && len(predicate.Right.Path) == 0
}

func validatePhysicalGroupFilterLineage(stages []PhysicalConstructionStage) error {
	if len(stages) == 1 {
		return nil
	}
	prior := stages[0]
	if prior.Group == nil {
		return fmt.Errorf("row lineage GROUP payload is required")
	}
	contributorColumn := prior.Group.RootContributorOutputColumn
	for index, stage := range stages[1:] {
		if stage.Kind != PhysicalStageFilterOp || stage.Filter == nil || stage.InputStageID != prior.ID {
			return fmt.Errorf("row lineage supports only identity-preserving FILTER stages after GROUP")
		}
		if stage.RowIdentityColumn != prior.RowIdentityColumn ||
			!samePhysicalStageColumns(stage.InputColumns, prior.OutputColumns) ||
			!samePhysicalStageColumns(stage.OutputColumns, stage.InputColumns) {
			return fmt.Errorf("row lineage FILTER stage %d must preserve its input schema and row identity", index+1)
		}
		if contributorColumn != "" && physicalFilterReferencesColumn(*stage.Filter, stage.InputRowVariable, contributorColumn) {
			return fmt.Errorf("row lineage FILTER cannot depend on hidden root contributor metadata")
		}
		prior = stage
	}
	return nil
}

func physicalFilterReferencesColumn(filter PhysicalFilter, variable, column string) bool {
	return physicalValueReferencesColumn(reflect.ValueOf(filter), variable, column)
}

func physicalValueReferencesColumn(value reflect.Value, variable, column string) bool {
	if !value.IsValid() {
		return false
	}
	if value.Type() == reflect.TypeOf(PhysicalValue{}) {
		physicalValue := value.Interface().(PhysicalValue)
		return physicalValue.Variable == variable && len(physicalValue.Path) != 0 && physicalValue.Path[0] == column
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		return !value.IsNil() && physicalValueReferencesColumn(value.Elem(), variable, column)
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if physicalValueReferencesColumn(value.Field(index), variable, column) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for index := 0; index < value.Len(); index++ {
			if physicalValueReferencesColumn(value.Index(index), variable, column) {
				return true
			}
		}
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			if physicalValueReferencesColumn(iterator.Value(), variable, column) {
				return true
			}
		}
	}
	return false
}

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
		if !validPhysicalFieldPathSegment(segment) {
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
	if relatedProjection == nil || relatedProjection.Expression == nil || relatedSourceProjectionSubplan(*relatedProjection.Expression, related.Form) == nil {
		return fmt.Errorf("related output does not match its typed route and form")
	}
	subplan := relatedSourceProjectionSubplan(*relatedProjection.Expression, related.Form)
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

func relatedSourceProjectionSubplan(expression PhysicalExpression, form string) *PhysicalSubplan {
	if form == "ALL" {
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
	if form == "PRESENCE" {
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

func validatePhysicalStageCohortGroup(stage PhysicalConstructionStage, cohort PhysicalStageCohortGroup, bindVars map[string]any) error {
	if err := validatePhysicalGroupRows(cohort.Rows, bindVars); err != nil {
		return err
	}
	if !physicalVariablePattern.MatchString(cohort.RootContributorVariable) {
		return fmt.Errorf("root contributor variable is unsafe")
	}
	if cohort.RootContributorOutputColumn != "__loom_root_contributor_keys" {
		return fmt.Errorf("root contributor output must use the compiler-owned contributor-set column")
	}
	resourceType, ok := bindVars[cohort.Rows.ResourceTypeBindKey].(string)
	if !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("root resource type is missing")
	}
	input, found := physicalStageColumnMap(stage.InputColumns)[cohort.ContributorInputColumn]
	if !found || !input.Internal || input.RootContributorResourceType != resourceType || input.Kind != "string" {
		return fmt.Errorf("contributor input %q is not a compiler-proven root identity", cohort.ContributorInputColumn)
	}
	if cohort.ContributorInputMany {
		if input.Name != cohort.RootContributorOutputColumn || input.Cardinality != "many" {
			return fmt.Errorf("contributor input %q is not a root contributor set", cohort.ContributorInputColumn)
		}
	} else if input.Name != "_key" || input.Cardinality != "required_one" {
		return fmt.Errorf("contributor input %q is not the direct root storage identity", cohort.ContributorInputColumn)
	}
	output, found := physicalStageColumnMap(stage.OutputColumns)[cohort.RootContributorOutputColumn]
	if !found || !output.Internal || output.Kind != "string" || output.Cardinality != "many" || output.RootContributorResourceType != resourceType {
		return fmt.Errorf("root contributor output %q has an invalid schema", cohort.RootContributorOutputColumn)
	}
	return nil
}

func validatePhysicalStageGroup(stage PhysicalConstructionStage, group PhysicalStageGroup, bindVars map[string]any) error {
	if len(group.Keys) == 0 && len(group.Aggregates) == 0 && len(group.RowValues) == 0 {
		return fmt.Errorf("requires at least one key or aggregate")
	}
	switch group.MissingKeyPolicy {
	case PhysicalStageGroupMissingKeyGroup, PhysicalStageGroupMissingKeyExclude, PhysicalStageGroupMissingKeyError:
	default:
		return fmt.Errorf("missing-key policy %q is unsupported", group.MissingKeyPolicy)
	}
	if err := requireNonEmptyStringBind(bindVars, group.ConstructionIDBindKey); err != nil {
		return fmt.Errorf("construction ID: %w", err)
	}
	if !physicalVariablePattern.MatchString(group.GroupRowsVariable) || !physicalVariablePattern.MatchString(group.IdentityVariable) {
		return fmt.Errorf("group row and identity variables must be safe")
	}
	if err := validateStageProjectionNames(stage.InputProjections, stage.InputColumns); err != nil {
		return fmt.Errorf("input projections: %w", err)
	}
	defined := map[string]bool{stage.InputRowVariable: true}
	for index, projection := range stage.InputProjections {
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("input projection %d (%q): %w", index, projection.Name, err)
		}
	}
	if err := definePhysicalVariable(defined, group.GroupRowsVariable); err != nil {
		return err
	}
	inputByName, outputByName := physicalStageColumnMap(stage.InputColumns), physicalStageColumnMap(stage.OutputColumns)
	if err := validateRootContributorReduction(
		group.RootContributorInputColumn,
		group.RootContributorInputMany,
		group.RootContributorOutputColumn,
		group.RootContributorVariable,
		stage.InputColumns, stage.OutputColumns, defined,
	); err != nil {
		return err
	}
	for index, key := range group.Keys {
		input, ok := inputByName[key.InputColumn]
		if !ok || input.Internal || input.Cardinality == "many" {
			return fmt.Errorf("key %d column %q is not a public scalar input", index, key.InputColumn)
		}
		kind, ok := physicalStageScalarKind(input.Kind)
		if !ok || kind != key.Kind {
			return fmt.Errorf("key %d column %q has unsupported or mismatched scalar kind %q", index, key.InputColumn, key.Kind)
		}
		output, ok := outputByName[key.OutputColumn]
		if !ok || output.Internal || output.Kind != input.Kind || output.Cardinality != input.Cardinality {
			return fmt.Errorf("key %d output column %q does not preserve its input type", index, key.OutputColumn)
		}
		if err := definePhysicalVariable(defined, key.Variable); err != nil {
			return fmt.Errorf("key %d: %w", index, err)
		}
	}
	for index, aggregate := range group.Aggregates {
		output, ok := outputByName[aggregate.Output]
		if !ok || output.Internal || output.Cardinality == "many" || output.Kind != aggregate.OutputKind {
			return fmt.Errorf("aggregate %d output column %q does not match its type", index, aggregate.Output)
		}
		needInput := aggregate.Operation != "COUNT_ROWS"
		if !needInput {
			if aggregate.InputColumn != "" || aggregate.InputKind != "" {
				return fmt.Errorf("aggregate %d COUNT_ROWS must not have an input column", index)
			}
		} else {
			input, ok := inputByName[aggregate.InputColumn]
			if !ok || input.Internal || input.Cardinality == "many" {
				return fmt.Errorf("aggregate %d input column %q is not a public scalar input", index, aggregate.InputColumn)
			}
			kind, ok := physicalStageScalarKind(input.Kind)
			if !ok || kind != aggregate.InputKind {
				return fmt.Errorf("aggregate %d input column %q has unsupported or mismatched scalar kind %q", index, aggregate.InputColumn, aggregate.InputKind)
			}
		}
		switch aggregate.Operation {
		case "COUNT_ROWS", "COUNT_NON_NULL", "COUNT_DISTINCT":
			if output.Kind != "integer" {
				return fmt.Errorf("aggregate %d count output must be integer", index)
			}
		case "MIN", "MAX":
			if output.Kind != inputByName[aggregate.InputColumn].Kind {
				return fmt.Errorf("aggregate %d %s output kind must match its input", index, aggregate.Operation)
			}
		case "SUM":
			if aggregate.InputKind != "INTEGER" && aggregate.InputKind != "DECIMAL" || output.Kind != inputByName[aggregate.InputColumn].Kind {
				return fmt.Errorf("aggregate %d SUM requires a matching numeric output", index)
			}
		case "MEAN":
			if aggregate.InputKind != "INTEGER" && aggregate.InputKind != "DECIMAL" || output.Kind != "decimal" {
				return fmt.Errorf("aggregate %d MEAN requires numeric input and decimal output", index)
			}
		default:
			return fmt.Errorf("aggregate %d has unsupported operation %q", index, aggregate.Operation)
		}
		if err := definePhysicalVariable(defined, aggregate.Variable); err != nil {
			return fmt.Errorf("aggregate %d: %w", index, err)
		}
	}
	reservedOutputs := make(map[string]bool, len(group.Keys)+len(group.Aggregates))
	for _, key := range group.Keys {
		reservedOutputs[key.OutputColumn] = true
	}
	for _, aggregate := range group.Aggregates {
		reservedOutputs[aggregate.Output] = true
	}
	if err := validatePhysicalStageRowValues(
		group.RowValues, stage.InputColumns, stage.OutputColumns,
		physicalProjectionNameSet(stage.InputProjections), reservedOutputs, defined,
	); err != nil {
		return fmt.Errorf("row values: %w", err)
	}
	if err := definePhysicalVariable(defined, group.IdentityVariable); err != nil {
		return fmt.Errorf("identity: %w", err)
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

func validateRootContributorReduction(
	inputColumn string,
	inputMany bool,
	outputColumn string,
	variable string,
	inputColumns, outputColumns []PhysicalStageColumn,
	defined map[string]bool,
) error {
	if inputColumn == "" && outputColumn == "" && variable == "" {
		if inputMany {
			return fmt.Errorf("root contributor cardinality is set without a contributor input")
		}
		return nil
	}
	if inputColumn == "" || outputColumn == "" || !physicalVariablePattern.MatchString(variable) {
		return fmt.Errorf("root contributor input, output, and variable are required together")
	}
	input, ok := physicalStageColumnMap(inputColumns)[inputColumn]
	if !ok || !input.Internal || input.Kind != "string" || input.RootContributorResourceType == "" {
		return fmt.Errorf("input column %q is not a typed hidden root contributor", inputColumn)
	}
	validInput := input.Name == "_key" && (input.Cardinality == "required_one" || input.Cardinality == "optional_one") ||
		input.Name == "__loom_root_contributor_keys" && input.Cardinality == "many"
	if !validInput || inputMany != (input.Cardinality == "many") {
		return fmt.Errorf("input column %q has an unsupported contributor shape", inputColumn)
	}
	output, ok := physicalStageColumnMap(outputColumns)[outputColumn]
	if !ok || !output.Internal || output.Name != "__loom_root_contributor_keys" ||
		output.Kind != "string" || output.Cardinality != "many" ||
		output.RootContributorResourceType != input.RootContributorResourceType {
		return fmt.Errorf("output column %q is not the matching typed contributor set", outputColumn)
	}
	if err := definePhysicalVariable(defined, variable); err != nil {
		return fmt.Errorf("root contributor variable: %w", err)
	}
	return nil
}

func validatePhysicalStageRowValues(
	rowValues []PhysicalStageRowValue,
	inputColumns, outputColumns []PhysicalStageColumn,
	projectedInputs, reservedOutputs map[string]bool,
	defined map[string]bool,
) error {
	inputs, outputs := physicalStageColumnMap(inputColumns), physicalStageColumnMap(outputColumns)
	seenOutputs := make(map[string]bool, len(rowValues))
	for index, rowValue := range rowValues {
		input, ok := inputs[rowValue.InputColumn]
		inputKind, scalar := physicalStageScalarKind(input.Kind)
		if input.Kind == "object" {
			inputKind, scalar = "OBJECT", true
		}
		if !ok || input.Internal || !scalar || inputKind != rowValue.InputKind ||
			(rowValue.InputMany != (input.Cardinality == "many")) {
			return fmt.Errorf("row value %d input column %q is not a supported scalar or scalar array", index, rowValue.InputColumn)
		}
		if projectedInputs != nil && !projectedInputs[rowValue.InputColumn] {
			return fmt.Errorf("row value %d input column %q is missing from shape projections", index, rowValue.InputColumn)
		}
		output, ok := outputs[rowValue.Output]
		if !ok || output.Internal || output.Kind != input.Kind || !physicalPathPartPattern.MatchString(rowValue.Output) ||
			seenOutputs[rowValue.Output] || reservedOutputs[rowValue.Output] {
			return fmt.Errorf("row value %d output column %q is missing, mistyped, or collides with another shape output", index, rowValue.Output)
		}
		switch rowValue.Policy {
		case "ALL":
			if output.Cardinality != "many" || output.Nullable {
				return fmt.Errorf("row value %d ALL output must be a non-null array of its scalar type", index)
			}
		case "ONE":
			if output.Cardinality != "optional_one" || !output.Nullable {
				return fmt.Errorf("row value %d ONE output must be a nullable scalar", index)
			}
		default:
			return fmt.Errorf("row value %d has unsupported policy %q", index, rowValue.Policy)
		}
		if defined != nil {
			if err := definePhysicalVariable(defined, rowValue.Variable); err != nil {
				return fmt.Errorf("row value %d: %w", index, err)
			}
		}
		seenOutputs[rowValue.Output] = true
	}
	return nil
}

func physicalProjectionNameSet(projections []PhysicalProjection) map[string]bool {
	names := make(map[string]bool, len(projections))
	for _, projection := range projections {
		names[projection.Name] = true
	}
	return names
}

func validatePhysicalStageCodedGroup(stage PhysicalConstructionStage, coded PhysicalStageCodedGroup, bindVars map[string]any, sourceOperations []PhysicalOperation) error {
	for key, label := range map[string]string{
		coded.RootCollectionBindKey: "root collection",
		coded.ConstructionIDBindKey: "construction ID",
		coded.OccurrenceIDBindKey:   "source occurrence ID",
		coded.CodingPathBindKey:     "Coding path",
	} {
		if err := requireNonEmptyStringBind(bindVars, key); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}
	collection, _ := bindVars[coded.RootCollectionBindKey].(string)
	constructionID, _ := bindVars[coded.ConstructionIDBindKey].(string)
	occurrenceID, _ := bindVars[coded.OccurrenceIDBindKey].(string)
	codingPath, _ := bindVars[coded.CodingPathBindKey].(string)
	if collection != coded.ResourceType || strings.TrimSpace(coded.ResourceType) == "" {
		return fmt.Errorf("root collection does not match the exact root resource type")
	}
	if !coded.SourceRowsUnique {
		return fmt.Errorf("CODED_GROUP requires compiler proof that the direct source has at most one row per root _key")
	}
	if constructionID == "" || occurrenceID != "base" || codingPath != coded.CodingPath || coded.SourceIdentityColumn != "_key" {
		return fmt.Errorf("construction, root occurrence, Coding path, and root identity facts are inconsistent")
	}
	inputIdentity, exists := physicalStageColumnMap(stage.InputColumns)[coded.SourceIdentityColumn]
	if !exists || !inputIdentity.Internal || !inputIdentity.Identity || inputIdentity.Kind != "string" || inputIdentity.Cardinality != "required_one" {
		return fmt.Errorf("source identity column %q is not a required root key", coded.SourceIdentityColumn)
	}
	if len(coded.PathSegments) == 0 {
		return fmt.Errorf("Coding path segments are required")
	}
	var canonical strings.Builder
	for index, segment := range coded.PathSegments {
		if !physicalVariablePattern.MatchString(segment.Name) {
			return fmt.Errorf("Coding path segment %d has an unsafe field name", index)
		}
		if index > 0 {
			canonical.WriteByte('.')
		}
		canonical.WriteString(segment.Name)
		if segment.Repeated {
			canonical.WriteString("[]")
		}
	}
	last := coded.PathSegments[len(coded.PathSegments)-1]
	if !last.Repeated || canonical.String() != coded.CodingPath {
		return fmt.Errorf("Coding path must be the exact generated repeated Coding path")
	}
	switch coded.MissingKeyPolicy {
	case PhysicalStageGroupMissingKeyGroup, PhysicalStageGroupMissingKeyExclude, PhysicalStageGroupMissingKeyError:
	default:
		return fmt.Errorf("missing-key policy %q is unsupported", coded.MissingKeyPolicy)
	}
	variables := []string{coded.SystemVariable, coded.VersionVariable, coded.CodeVariable, coded.CountVariable, coded.IdentityVariable}
	seenVariables := map[string]bool{stage.InputRowVariable: true, stage.OutputRowVariable: true}
	for _, variable := range variables {
		if !physicalVariablePattern.MatchString(variable) || seenVariables[variable] {
			return fmt.Errorf("coded-group output variables must be safe and distinct")
		}
		seenVariables[variable] = true
	}
	outputs := physicalStageColumnMap(stage.OutputColumns)
	for _, columnName := range []string{coded.SystemOutputColumn, coded.VersionOutputColumn, coded.CodeOutputColumn} {
		column, ok := outputs[columnName]
		if !ok || column.Internal || column.Kind != "string" || column.Cardinality != "optional_one" || !column.Nullable {
			return fmt.Errorf("coded-group key output %q must be a nullable scalar string", columnName)
		}
	}
	count, ok := outputs[coded.CountOutputColumn]
	if !ok || count.Internal || count.Kind != "integer" || count.Cardinality != "required_one" || count.Nullable {
		return fmt.Errorf("coded-group source count must be a required integer")
	}
	identity, ok := outputs["__loom_row_id"]
	if !ok || !identity.Internal || !identity.Identity || identity.Kind != "string" || identity.Cardinality != "required_one" {
		return fmt.Errorf("coded-group output requires the hidden stable row identity")
	}
	projectionByName := make(map[string]PhysicalProjection, len(stage.OutputProjections))
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return fmt.Errorf("output projections: %w", err)
	}
	for _, projection := range stage.OutputProjections {
		projectionByName[projection.Name] = projection
	}
	for name, variable := range map[string]string{
		coded.SystemOutputColumn:  coded.SystemVariable,
		coded.VersionOutputColumn: coded.VersionVariable,
		coded.CodeOutputColumn:    coded.CodeVariable,
		coded.CountOutputColumn:   coded.CountVariable,
		"__loom_row_id":           coded.IdentityVariable,
	} {
		projection, ok := projectionByName[name]
		if !ok || projection.Value.Variable != variable || len(projection.Value.Path) != 0 || projection.Expression != nil {
			return fmt.Errorf("coded-group output projection %q does not use its exact value variable", name)
		}
	}
	for _, rowValue := range coded.RowValues {
		projection, ok := projectionByName[rowValue.Output]
		if !ok || projection.Value.Variable != rowValue.Variable || len(projection.Value.Path) != 0 || projection.Expression != nil {
			return fmt.Errorf("coded-group row value output projection %q does not use its exact value variable", rowValue.Output)
		}
	}
	reservedOutputs := map[string]bool{
		coded.SystemOutputColumn: true, coded.VersionOutputColumn: true,
		coded.CodeOutputColumn: true, coded.CountOutputColumn: true, "__loom_row_id": true,
	}
	projectionNames := physicalProjectionNameSet(coded.RowValueProjections)
	if err := validatePhysicalStageRowValues(coded.RowValues, stage.InputColumns, stage.OutputColumns, projectionNames, reservedOutputs, seenVariables); err != nil {
		return fmt.Errorf("row values: %w", err)
	}
	if len(coded.RowValueProjections) != len(projectionNames) {
		return fmt.Errorf("row value source projections contain duplicate columns")
	}
	var sourceReturn *PhysicalReturn
	for index := len(sourceOperations) - 1; index >= 0; index-- {
		if sourceOperations[index].Kind == PhysicalReturnOp && sourceOperations[index].Return != nil {
			sourceReturn = sourceOperations[index].Return
			break
		}
	}
	if len(coded.RowValues) != 0 && sourceReturn == nil {
		return fmt.Errorf("row values require a resolved source projection")
	}
	if sourceReturn != nil {
		sourceProjections := make(map[string]PhysicalProjection, len(sourceReturn.Projections))
		for _, projection := range sourceReturn.Projections {
			sourceProjections[projection.Name] = projection
		}
		expectedInputs := make(map[string]bool, len(coded.RowValues))
		for _, rowValue := range coded.RowValues {
			expectedInputs[rowValue.InputColumn] = true
		}
		if len(expectedInputs) != len(coded.RowValueProjections) {
			return fmt.Errorf("row value source projections do not match requested input columns")
		}
		for _, projection := range coded.RowValueProjections {
			want, ok := sourceProjections[projection.Name]
			if !ok || !expectedInputs[projection.Name] {
				return fmt.Errorf("row value source projection %q is not a requested public input", projection.Name)
			}
			want.Hidden = true
			if !reflect.DeepEqual(projection, want) {
				return fmt.Errorf("row value source projection %q differs from its resolved source", projection.Name)
			}
		}
	}
	return nil
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
	case "string", "date", "datetime", "code", "uuid":
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
				if !found || projection.Expression == nil || relatedSourceProjectionSubplan(*projection.Expression, stage.RelatedSource.Form) == nil {
					return fmt.Errorf("related-source output has no typed route subplan")
				}
				subplan := relatedSourceProjectionSubplan(*projection.Expression, stage.RelatedSource.Form)
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

func groupedPivotOutputNames(pivot PhysicalGroupedPivot) []string {
	columns := make([]string, 0, len(pivot.GroupKeys)+len(pivot.Categories)+len(pivot.CodedCategories)+len(pivot.RowValues)+2)
	for _, key := range pivot.GroupKeys {
		name := key.Output
		if name == "" {
			name = key.Column
		}
		columns = append(columns, name)
	}
	for _, category := range pivot.Categories {
		columns = append(columns, category.Output)
	}
	for _, category := range pivot.CodedCategories {
		columns = append(columns, category.Output)
	}
	for _, rowValue := range pivot.RowValues {
		columns = append(columns, rowValue.Output)
	}
	if pivot.RootContributorOutputColumn != "" {
		columns = append(columns, pivot.RootContributorOutputColumn)
	}
	if pivot.UnlistedEvidenceColumn != "" {
		columns = append(columns, pivot.UnlistedEvidenceColumn)
	}
	columns = append(columns, "__loom_row_id")
	return columns
}

func groupedPivotBaseOutputs(pivot PhysicalGroupedPivot) map[string]bool {
	outputs := make(map[string]bool, len(pivot.GroupKeys)+len(pivot.Categories)+len(pivot.CodedCategories)+2)
	for _, key := range pivot.GroupKeys {
		name := key.Output
		if name == "" {
			name = key.Column
		}
		outputs[name] = true
	}
	for _, category := range pivot.Categories {
		outputs[category.Output] = true
	}
	for _, category := range pivot.CodedCategories {
		outputs[category.Output] = true
	}
	if pivot.RootContributorOutputColumn != "" {
		outputs[pivot.RootContributorOutputColumn] = true
	}
	if pivot.UnlistedEvidenceColumn != "" {
		outputs[pivot.UnlistedEvidenceColumn] = true
	}
	outputs["__loom_row_id"] = true
	return outputs
}

func unpivotOutputNames(unpivot PhysicalUnpivot) []string {
	selected := map[string]bool{}
	for _, input := range unpivot.Inputs {
		selected[input.Column] = true
	}
	preservedNames := map[string]string{}
	for _, output := range unpivot.PreservedOutputs {
		preservedNames[output.InputColumn] = output.OutputColumn
	}
	columns := make([]string, 0, len(unpivot.InputProjections)+2)
	for _, projection := range unpivot.InputProjections {
		if !selected[projection.Name] && projection.Name != "__loom_row_id" {
			name := projection.Name
			if renamed, ok := preservedNames[projection.Name]; ok {
				name = renamed
			}
			columns = append(columns, name)
		}
	}
	columns = append(columns, unpivot.KeyOutput, unpivot.ValueOutput, "__loom_row_id")
	return columns
}

func validateShapeStageOutput(stage PhysicalConstructionStage, expected []string) error {
	expectedNames := make(map[string]bool, len(expected))
	for _, name := range expected {
		expectedNames[name] = true
	}
	actual := make(map[string]PhysicalStageColumn, len(stage.OutputColumns))
	for _, column := range stage.OutputColumns {
		actual[column.Name] = column
	}
	for _, name := range expected {
		if _, ok := actual[name]; !ok {
			return fmt.Errorf("shape output schema is missing emitted column %q", name)
		}
	}
	for name, column := range actual {
		if !expectedNames[name] && !column.Internal {
			return fmt.Errorf("shape output schema contains unexpected public column %q", name)
		}
	}
	return nil
}
