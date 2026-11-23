package ir

import (
	"strings"
)

import (
	"fmt"
)

type physicalOperationValidation struct {
	plan                   PhysicalPlan
	defined                map[string]bool
	rootScans              int
	returns                int
	graphReturns           int
	groupRows              int
	tableReshapes          int
	reshapeOutputVariable  string
	reshapeWindowSortSeen  bool
	reshapeWindowLimitSeen bool
	reshapeWindowClosed    bool
}

func (validation *physicalOperationValidation) validate(i int, operation PhysicalOperation) error {
	if validation.returns+validation.graphReturns > 0 {
		return fmt.Errorf("operation %d appears after RETURN", i)
	}
	if validation.tableReshapes > 0 {
		if validation.reshapeWindowSortSeen && !validation.reshapeWindowLimitSeen && operation.Kind != PhysicalLimitOp {
			validation.reshapeWindowClosed = true
		}
		switch operation.Kind {
		case PhysicalExpressionLetOp, PhysicalReturnOp, PhysicalCellTraceReturnOp:
		case PhysicalSortOp:
			if validation.reshapeWindowSortSeen || validation.reshapeWindowLimitSeen || validation.reshapeWindowClosed {
				return fmt.Errorf("operation %d: reshaped execution window can contain only one SORT", i)
			}
			if operation.Sort == nil || len(operation.Sort.Keys) != 1 {
				return fmt.Errorf("operation %d: reshaped execution SORT must use the stable row identity", i)
			}
			key := operation.Sort.Keys[0]
			if key.Variable != validation.reshapeOutputVariable || key.BindKey != "" || len(key.Path) != 1 || key.Path[0] != "__loom_row_id" {
				return fmt.Errorf("operation %d: reshaped execution SORT must use the stable row identity", i)
			}
			validation.reshapeWindowSortSeen = true
		case PhysicalLimitOp:
			if !validation.reshapeWindowSortSeen || validation.reshapeWindowLimitSeen || validation.reshapeWindowClosed {
				return fmt.Errorf("operation %d: reshaped execution LIMIT requires an immediately preceding stable row SORT", i)
			}
			validation.reshapeWindowLimitSeen = true
		default:
			return fmt.Errorf("operation %d (%s) appears after terminal table reshape", i, operation.Kind)
		}
	}
	if err := operation.validatePayload(); err != nil {
		return fmt.Errorf("operation %d (%s): %w", i, operation.Kind, err)
	}
	if operation.Kind == PhysicalKeySetLookupOp {
		return fmt.Errorf("operation %d: KEY_SET_LOOKUP is only legal inside a typed subplan", i)
	}
	switch operation.Kind {
	case PhysicalRootScanOp:
		return validation.validateRootScan(i, operation)
	case PhysicalTraversalOp:
		return validation.validateTraversal(i, operation)
	case PhysicalFilterOp:
		return validation.validateFilter(i, operation)
	case PhysicalDerivedLetOp:
		return validation.validateDerivedLet(i, operation)
	case PhysicalExpressionLetOp:
		return validation.validateExpressionLet(i, operation)
	case PhysicalSetOp:
		return validation.validateSet(i, operation)
	case PhysicalUnnestOp:
		return validation.validateUnnest(i, operation)
	case PhysicalSortOp:
		return validation.validateSort(i, operation)
	case PhysicalLimitOp:
		return validation.validateLimit(i, operation)
	case PhysicalPathSeedOp:
		return validation.validatePathSeed(i, operation)
	case PhysicalPathExtendOp:
		return validation.validatePathExtend(i, operation)
	case PhysicalGraphReturnOp:
		return validation.validateGraphReturn(i, operation)
	case PhysicalPopulationMappingReturnOp:
		return validation.validatePopulationMappingReturn(i, operation)
	case PhysicalCellTraceReturnOp:
		return validation.validateCellTraceReturn(i, operation)
	case PhysicalGroupRowsOp:
		return validation.validateGroupRows(i, operation)
	case PhysicalGroupedPivotOp:
		return validation.validateGroupedPivot(i, operation)
	case PhysicalUnpivotOp:
		return validation.validateUnpivot(i, operation)
	case PhysicalReturnOp:
		return validation.validateReturn(i, operation)
	}
	return nil
}

type constructionStageValidation struct {
	sequence         PhysicalStageSequence
	sourceOperations []PhysicalOperation
	bindVars         map[string]any
}

func (validation constructionStageValidation) validateOperation(stage PhysicalConstructionStage, index int, path string) error {
	switch stage.Kind {
	case PhysicalStageDeriveOp:
		return validation.validateDerive(stage, path)
	case PhysicalStageFilterOp:
		return validation.validateFilter(stage, path)
	case PhysicalStagePivotOp:
		return validation.validatePivot(stage, path, index)
	case PhysicalStageUnpivotOp:
		return validation.validateUnpivot(stage, path)
	case PhysicalStageGroupOp:
		return validation.validateGroup(stage, path)
	case PhysicalStageCodedGroupOp:
		return validation.validateCodedGroup(stage, path)
	case PhysicalStageCohortGroupOp:
		return validation.validateCohortGroup(stage, path)
	case PhysicalStageExpandOp:
		return validation.validateExpand(stage, path)
	case PhysicalStageRelatedSourceOp:
		return validation.validateRelatedSource(stage, path)
	case PhysicalStageRelatedExpandOp:
		return validation.validateRelatedExpand(stage, path)
	case PhysicalStageRelatedEligibilityOp:
		return validation.validateRelatedEligibility(stage, path)
	case PhysicalStageRelatedFieldOp:
		return validation.validateRelatedField(stage, path)
	default:
		return fmt.Errorf("%s has unsupported operation kind %q", path, stage.Kind)
	}
}

func (validation *physicalOperationValidation) validateFilter(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validatePhysicalFilter(*operation.Filter, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validateDerivedLet(i int, operation PhysicalOperation) error {
	p := validation.plan
	derived := operation.DerivedLet
	if err := validatePhysicalDerivedLet(*derived, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	if err := definePhysicalVariable(validation.defined, derived.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validateExpressionLet(i int, operation PhysicalOperation) error {
	p := validation.plan
	if operation.ExpressionLet == nil || strings.TrimSpace(operation.ExpressionLet.Variable) == "" {
		return fmt.Errorf("operation %d: expression LET payload and variable are required", i)
	}
	if err := validatePhysicalExpression(operation.ExpressionLet.Expression, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d expression LET: %w", i, err)
	}
	if err := definePhysicalVariable(validation.defined, operation.ExpressionLet.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validateSet(i int, operation PhysicalOperation) error {
	p := validation.plan
	set := operation.Set
	if err := validatePhysicalSet(*set, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d set %q: %w", i, set.Variable, err)
	}
	if err := definePhysicalVariable(validation.defined, set.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	if set.Reduction != nil {
		if err := definePhysicalVariable(validation.defined, set.Reduction.Variable); err != nil {
			return fmt.Errorf("operation %d set reduction: %w", i, err)
		}
	}
	if set.Prepared != nil {
		if err := definePhysicalVariable(validation.defined, set.Prepared.Variable); err != nil {
			return fmt.Errorf("operation %d prepared set: %w", i, err)
		}
	}
	return nil
}

func (validation *physicalOperationValidation) validateUnnest(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validatePhysicalUnnest(*operation.Unnest, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d unnest: %w", i, err)
	}
	if err := definePhysicalUnnestVariables(*operation.Unnest, validation.defined); err != nil {
		return fmt.Errorf("operation %d unnest bindings: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validateSort(i int, operation PhysicalOperation) error {
	p := validation.plan
	if len(operation.Sort.Keys) == 0 {
		return fmt.Errorf("operation %d sort requires at least one key", i)
	}
	for keyIndex, key := range operation.Sort.Keys {
		if err := validatePhysicalValue(key, validation.defined, p.BindVars); err != nil {
			return fmt.Errorf("operation %d sort key %d: %w", i, keyIndex, err)
		}
	}
	return nil
}

func (validation *physicalOperationValidation) validateLimit(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := requireBind(p.BindVars, operation.Limit.BindKey); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	limit, ok := p.BindVars[operation.Limit.BindKey].(int)
	if !ok || limit <= 0 {
		return fmt.Errorf("operation %d: limit bind %q must be a positive int", i, operation.Limit.BindKey)
	}
	return nil
}

func (p PhysicalPlan) validateOperations() error {
	validation := physicalOperationValidation{plan: p, defined: make(map[string]bool)}
	reshapeIndex := -1
	for index, operation := range p.Operations {
		if operation.Kind == PhysicalGroupedPivotOp || operation.Kind == PhysicalUnpivotOp {
			reshapeIndex = index
			break
		}
	}
	if reshapeIndex >= 0 {
		for index := 0; index < reshapeIndex; index++ {
			if p.Operations[index].Kind == PhysicalLimitOp {
				return fmt.Errorf("operation %d: execution LIMIT cannot precede a terminal table reshape", index)
			}
		}
	}
	for index, operation := range p.Operations {
		if err := validation.validate(index, operation); err != nil {
			return err
		}
	}
	if validation.groupRows == 1 {
		if validation.rootScans != 0 || len(p.Operations) != 1 {
			return fmt.Errorf("grouped physical plan cannot contain a root scan or other operations")
		}
	} else if validation.rootScans != 1 {
		return fmt.Errorf("physical plan requires exactly one root scan")
	}
	if validation.groupRows > 0 && validation.tableReshapes > 0 {
		return fmt.Errorf("group rows cannot be combined with a table reshape")
	}
	if validation.returns+validation.graphReturns != 1 {
		return fmt.Errorf("physical plan requires exactly one RETURN, GRAPH_RETURN, POPULATION_MAPPING_RETURN, or CELL_TRACE_RETURN")
	}
	return nil
}

func (validation *physicalOperationValidation) validateGroupRows(i int, operation PhysicalOperation) error {
	p := validation.plan
	if i != len(p.Operations)-1 {
		return fmt.Errorf("operation %d: GROUP_ROWS must be the terminal operation", i)
	}
	if err := validatePhysicalGroupRows(*operation.GroupRows, p.BindVars); err != nil {
		return fmt.Errorf("operation %d group rows: %w", i, err)
	}
	validation.groupRows++
	validation.returns++
	return nil
}

func (validation *physicalOperationValidation) validateGroupedPivot(i int, operation PhysicalOperation) error {
	p := validation.plan
	validation.tableReshapes++
	if validation.tableReshapes > 1 {
		return fmt.Errorf("operation %d: physical plan has multiple table reshapes", i)
	}
	if validation.groupRows > 0 {
		return fmt.Errorf("operation %d: grouped row sources cannot be combined with table reshapes", i)
	}
	if err := validatePhysicalGroupedPivot(*operation.GroupedPivot, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d grouped pivot: %w", i, err)
	}
	pivotVariables := []string{operation.GroupedPivot.InputRowVariable}
	if !operation.GroupedPivot.OneInputRowPerGroup {
		pivotVariables = append(pivotVariables, operation.GroupedPivot.GroupRowsVariable)
	}
	pivotVariables = append(pivotVariables, groupedPivotVariables(operation.GroupedPivot.GroupKeys)...)
	for _, variable := range pivotVariables {
		if err := definePhysicalVariable(validation.defined, variable); err != nil {
			return fmt.Errorf("operation %d grouped pivot: %w", i, err)
		}
	}
	if err := definePhysicalVariable(validation.defined, operation.GroupedPivot.OutputRowVariable); err != nil {
		return fmt.Errorf("operation %d grouped pivot output row: %w", i, err)
	}
	validation.reshapeOutputVariable = operation.GroupedPivot.OutputRowVariable
	return nil
}

func (validation *physicalOperationValidation) validateUnpivot(i int, operation PhysicalOperation) error {
	p := validation.plan
	validation.tableReshapes++
	if validation.tableReshapes > 1 {
		return fmt.Errorf("operation %d: physical plan has multiple table reshapes", i)
	}
	if validation.groupRows > 0 {
		return fmt.Errorf("operation %d: grouped row sources cannot be combined with table reshapes", i)
	}
	if err := validatePhysicalUnpivot(*operation.Unpivot, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d unpivot: %w", i, err)
	}
	for _, variable := range []string{operation.Unpivot.InputRowVariable, operation.Unpivot.SlotVariable, operation.Unpivot.OutputRowVariable} {
		if err := definePhysicalVariable(validation.defined, variable); err != nil {
			return fmt.Errorf("operation %d unpivot: %w", i, err)
		}
	}
	validation.reshapeOutputVariable = operation.Unpivot.OutputRowVariable
	return nil
}

func (validation *physicalOperationValidation) validateGraphReturn(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validatePhysicalGraphReturn(*operation.GraphReturn, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d graph return: %w", i, err)
	}
	validation.graphReturns++
	return nil
}

func (validation *physicalOperationValidation) validatePopulationMappingReturn(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validatePhysicalPopulationMappingReturn(*operation.PopulationMappingReturn, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d population mapping return: %w", i, err)
	}
	validation.returns++
	return nil
}

func (validation *physicalOperationValidation) validateCellTraceReturn(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validatePhysicalCellTraceReturn(*operation.CellTraceReturn, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d cell trace return: %w", i, err)
	}
	validation.returns++
	return nil
}

func (validation *physicalOperationValidation) validateReturn(i int, operation PhysicalOperation) error {
	p := validation.plan
	validation.returns++
	seenNames := map[string]bool{}
	for _, projection := range operation.Return.Projections {
		if strings.TrimSpace(projection.Name) == "" || seenNames[projection.Name] {
			return fmt.Errorf("operation %d: return projection name %q is empty or duplicated", i, projection.Name)
		}
		seenNames[projection.Name] = true
		if err := validatePhysicalProjection(projection, validation.defined, p.BindVars); err != nil {
			return fmt.Errorf("operation %d projection %q: %w", i, projection.Name, err)
		}
	}
	return nil
}

func (validation *physicalOperationValidation) validateRootScan(i int, operation PhysicalOperation) error {
	p := validation.plan
	if hint := operation.RootScan.IndexHint; hint != "" && (!physicalPathPartPattern.MatchString(hint) || hint[0] == '_') {
		return fmt.Errorf("operation %d: unsafe root index hint %q", i, hint)
	}
	if operation.RootScan.IndexHint != "" && (operation.RootScan.Population != nil || operation.RootScan.CohortSource != nil || operation.RootScan.PageCandidateSeed != nil) {
		return fmt.Errorf("operation %d: root index hint requires a direct scan", i)
	}
	validation.rootScans++
	if validation.rootScans > 1 {
		return fmt.Errorf("operation %d: physical plan has multiple root scans", i)
	}
	if err := requireBind(p.BindVars, operation.RootScan.CollectionBindKey); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	if err := requireCollectionBind(p.BindVars, operation.RootScan.CollectionBindKey); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	if operation.RootScan.Population != nil {
		if err := validatePhysicalPopulationRootSource(*operation.RootScan.Population, p.BindVars); err != nil {
			return fmt.Errorf("operation %d population root source: %w", i, err)
		}
	}
	if operation.RootScan.CohortSource != nil {
		if operation.RootScan.Population != nil {
			return fmt.Errorf("operation %d cannot combine population and cohort root sources", i)
		}
		if err := validatePhysicalCohortRootSource(*operation.RootScan.CohortSource, p.BindVars); err != nil {
			return fmt.Errorf("operation %d cohort root source: %w", i, err)
		}
	}
	if seed := operation.RootScan.PageCandidateSeed; seed != nil {
		if operation.RootScan.Population != nil || operation.RootScan.CohortSource != nil {
			return fmt.Errorf("operation %d root page candidate seed cannot be combined with population or cohort root sources", i)
		}
		if err := validatePhysicalUncorrelatedSubplan(seed.Subplan, p.BindVars); err != nil {
			return fmt.Errorf("operation %d root page candidate seed: %w", i, err)
		}
	}
	if err := definePhysicalVariable(validation.defined, operation.RootScan.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	if population := operation.RootScan.Population; population != nil && population.CollectMembersVariable != "" {
		if err := definePhysicalVariable(validation.defined, population.CollectMembersVariable); err != nil {
			return fmt.Errorf("operation %d population member collection: %w", i, err)
		}
	}
	return nil
}

func (validation *physicalOperationValidation) validateTraversal(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validateAndDefinePhysicalTraversal(*operation.Traversal, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validatePathSeed(i int, operation PhysicalOperation) error {
	p := validation.plan
	seed := operation.PathSeed
	if err := validatePhysicalPathNode(seed.Node, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d path seed: %w", i, err)
	}
	if err := definePhysicalVariable(validation.defined, seed.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validatePathExtend(i int, operation PhysicalOperation) error {
	p := validation.plan
	extend := operation.PathExtend
	if err := validatePhysicalPathExtend(*extend, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d path extend: %w", i, err)
	}
	if err := definePhysicalVariable(validation.defined, extend.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}

func (validation constructionStageValidation) validateGroup(stage PhysicalConstructionStage, path string) error {
	sequence := validation.sequence
	sourceOperations := validation.sourceOperations
	bindVars := validation.bindVars
	if stage.Group == nil || stage.Filter != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s GROUP requires only a group payload", path)
	}
	if err := validatePhysicalStageGroup(stage, *stage.Group, bindVars, sequence.SourceRowIdentity, sourceOperations); err != nil {
		return fmt.Errorf("%s group: %w", path, err)
	}
	expected := make([]string, 0, len(stage.Group.Keys)+len(stage.Group.Aggregates)+len(stage.Group.RowValues)+2)
	for _, key := range stage.Group.Keys {
		if key.OutputColumn != "" {
			expected = append(expected, key.OutputColumn)
		}
	}
	if stage.Group.SemanticKey != nil {
		expected = append(expected, stage.Group.SemanticKey.OutputColumn)
	}
	if stage.Group.CodingKey != nil {
		expected = append(expected, stage.Group.CodingKey.OutputColumn)
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
	return nil
}

func (validation constructionStageValidation) validateCodedGroup(stage PhysicalConstructionStage, path string) error {
	sourceOperations := validation.sourceOperations
	bindVars := validation.bindVars
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
	return nil
}

func (validation constructionStageValidation) validateCohortGroup(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
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
	return nil
}

func (validation constructionStageValidation) validateRelatedSource(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.RelatedSource == nil || stage.RelatedExpand != nil || stage.RelatedField != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s RELATED_SOURCE requires only a related-source payload", path)
	}
	if err := validatePhysicalStageRelatedSource(stage, *stage.RelatedSource, bindVars); err != nil {
		return fmt.Errorf("%s related source: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateRelatedExpand(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.RelatedExpand == nil || stage.RelatedSource != nil || stage.RelatedField != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s RELATED_EXPAND requires only a related-expansion payload", path)
	}
	if err := validatePhysicalStageRelatedExpand(stage, *stage.RelatedExpand, bindVars); err != nil {
		return fmt.Errorf("%s related expansion: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateRelatedEligibility(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.Filter == nil || stage.RelatedSource != nil || stage.RelatedExpand != nil || stage.RelatedField != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil {
		return fmt.Errorf("%s RELATED_ELIGIBILITY requires only a filter and optional typed count LET", path)
	}
	if err := validatePhysicalStageRelatedEligibility(stage, bindVars); err != nil {
		return fmt.Errorf("%s related eligibility: %w", path, err)
	}
	if err := validateStageRowOperations(stage, bindVars, true); err != nil {
		return err
	}
	return nil
}

func (validation constructionStageValidation) validateRelatedField(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.RelatedField == nil || stage.RelatedExpand != nil || stage.RelatedSource != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s RELATED_FIELD requires only a related-field payload", path)
	}
	if err := validatePhysicalStageRelatedField(stage, *stage.RelatedField, bindVars); err != nil {
		return fmt.Errorf("%s related field: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validatePivot(stage PhysicalConstructionStage, path string, index int) error {
	sequence := validation.sequence
	sourceOperations := validation.sourceOperations
	bindVars := validation.bindVars
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
		physicalProjectionNameSet(stage.GroupedPivot.InputProjections), groupedPivotBaseOutputs(*stage.GroupedPivot), nil, "",
	); err != nil {
		return fmt.Errorf("%s grouped pivot row values: %w", path, err)
	}
	if err := validateShapeStageOutput(stage, groupedPivotOutputNames(*stage.GroupedPivot)); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateUnpivot(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
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
	return nil
}

func (validation constructionStageValidation) validateDerive(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedField != nil {
		return fmt.Errorf("%s DERIVE has mismatched operation payloads", path)
	}
	if len(stage.DerivedLets) == 0 {
		return fmt.Errorf("%s DERIVE requires at least one typed expression LET", path)
	}
	if err := validateStageRowOperations(stage, bindVars, false); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateFilter(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.Filter == nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s FILTER requires only a filter payload", path)
	}
	if err := validateStageRowOperations(stage, bindVars, true); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateExpand(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
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
	return nil
}

func (p PhysicalPlan) Validate() error {
	if p.Version <= 0 {
		return fmt.Errorf("physical plan version must be positive")
	}
	for key := range p.BindVars {
		if !physicalBindKeyPattern.MatchString(key) {
			return fmt.Errorf("unsafe bind key %q", key)
		}
	}
	standalone, err := p.validateEngine()
	if err != nil || standalone {
		return err
	}
	if err := p.validateOperations(); err != nil {
		return err
	}
	if p.StageSequence != nil {
		if err := validatePhysicalStageSequence(*p.StageSequence, p.Operations, p.BindVars); err != nil {
			return fmt.Errorf("construction stage sequence: %w", err)
		}
	}
	if err := validateCohortRootSourceProof(p); err != nil {
		return err
	}
	return nil
}

func (p PhysicalPlan) validateEngine() (bool, error) {
	switch p.Engine {
	case "", PhysicalEngineAQL:
		if p.ClickHouseCombine != nil || p.ClickHousePrefix != nil {
			return false, fmt.Errorf("AQL physical plan cannot carry a ClickHouse combine payload")
		}
	case PhysicalEngineClickHouse:
		if p.ClickHouseCombine == nil {
			return false, fmt.Errorf("ClickHouse physical plan requires a typed combine payload")
		}
		if p.ClickHousePrefix == nil {
			if len(p.Operations) != 0 || p.StageSequence != nil || len(p.DeferredExpressionLets) != 0 {
				return false, fmt.Errorf("standalone ClickHouse combine cannot carry AQL operations or construction stages")
			}
			if err := p.ClickHouseCombine.Validate(); err != nil {
				return false, fmt.Errorf("ClickHouse combine: %w", err)
			}
			return true, nil
		}
		if len(p.DeferredExpressionLets) != 0 || p.StageSequence == nil {
			return false, fmt.Errorf("composite ClickHouse combine requires a typed AQL stage sequence and no deferred expressions")
		}
		if p.StageSequence.PreviewLimitBindKey != "" {
			return false, fmt.Errorf("bounded AQL preview cannot feed a private ClickHouse artifact")
		}
		for _, operation := range p.Operations {
			if operation.Kind == PhysicalLimitOp {
				return false, fmt.Errorf("bounded AQL prefix cannot feed a private ClickHouse artifact")
			}
			if operation.Kind == PhysicalGroupRowsOp {
				return false, fmt.Errorf("GROUP prefixes cannot feed a private ClickHouse Combine until authorization scope is preserved by grouping")
			}
		}
		for _, stage := range p.StageSequence.Stages {
			if stage.Kind == PhysicalStageGroupOp || stage.Kind == PhysicalStageCohortGroupOp {
				return false, fmt.Errorf("%s prefixes cannot feed a private ClickHouse Combine until authorization scope is preserved by grouping", stage.Kind)
			}
		}
		if err := p.ClickHouseCombine.ValidateWithPrivateStage(); err != nil {
			return false, fmt.Errorf("composite ClickHouse combine: %w", err)
		}
		if err := p.ClickHousePrefix.Validate(p.StageSequence, p.BindVars); err != nil {
			return false, fmt.Errorf("composite ClickHouse prefix: %w", err)
		}
		if p.StageSequence.OutputAuthResourcePathBindKey != p.ClickHousePrefix.AuthResourcePathBindKey {
			return false, fmt.Errorf("composite ClickHouse prefix path bind does not match its typed stage sequence")
		}
		privateStageID := ""
		for _, input := range p.ClickHouseCombine.Inputs {
			if input.PrivateStageID == "" {
				continue
			}
			privateStageID = input.PrivateStageID
		}
		if privateStageID != p.ClickHousePrefix.StageID {
			return false, fmt.Errorf("composite ClickHouse input does not match the exact AQL prefix stage")
		}
	default:
		return false, fmt.Errorf("unsupported physical engine %q", p.Engine)
	}
	return false, nil
}

func (p PhysicalPlan) ValidateForWorkspaceCompilation() error {
	if p.Engine != PhysicalEngineClickHouse || p.ClickHouseCombine == nil || p.ClickHousePrefix != nil {
		return p.Validate()
	}
	if p.Version <= 0 || len(p.Operations) != 0 || p.StageSequence != nil || len(p.DeferredExpressionLets) != 0 {
		return fmt.Errorf("workspace-output ClickHouse combine must be a standalone typed plan")
	}
	if len(p.BindVars) != 0 {
		return fmt.Errorf("workspace-output ClickHouse combine cannot carry execution bind variables")
	}
	return p.ClickHouseCombine.ValidateForWorkspaceCompilation()
}

func (operation PhysicalOperation) validatePayload() error {
	payloads := 0
	if operation.RootScan != nil {
		payloads++
	}
	if operation.Traversal != nil {
		payloads++
	}
	if operation.Filter != nil {
		payloads++
	}
	if operation.DerivedLet != nil {
		payloads++
	}
	if operation.ExpressionLet != nil {
		payloads++
	}
	if operation.Set != nil {
		payloads++
	}
	if operation.Unnest != nil {
		payloads++
	}
	if operation.Sort != nil {
		payloads++
	}
	if operation.Limit != nil {
		payloads++
	}
	if operation.Return != nil {
		payloads++
	}
	if operation.PathSeed != nil {
		payloads++
	}
	if operation.PathExtend != nil {
		payloads++
	}
	if operation.GraphReturn != nil {
		payloads++
	}
	if operation.CollectionScan != nil {
		payloads++
	}
	if operation.KeySetLookup != nil {
		payloads++
	}
	if operation.DocumentLookup != nil {
		payloads++
	}
	if operation.PopulationMappingReturn != nil {
		payloads++
	}
	if operation.CellTraceReturn != nil {
		payloads++
	}
	if operation.GroupRows != nil {
		payloads++
	}
	if operation.GroupedPivot != nil {
		payloads++
	}
	if operation.Unpivot != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("operation must contain exactly one payload")
	}
	valid := (operation.Kind == PhysicalRootScanOp && operation.RootScan != nil) ||
		(operation.Kind == PhysicalTraversalOp && operation.Traversal != nil) ||
		(operation.Kind == PhysicalFilterOp && operation.Filter != nil) ||
		(operation.Kind == PhysicalDerivedLetOp && operation.DerivedLet != nil) ||
		(operation.Kind == PhysicalExpressionLetOp && operation.ExpressionLet != nil) ||
		(operation.Kind == PhysicalSetOp && operation.Set != nil) ||
		(operation.Kind == PhysicalUnnestOp && operation.Unnest != nil) ||
		(operation.Kind == PhysicalSortOp && operation.Sort != nil) ||
		(operation.Kind == PhysicalLimitOp && operation.Limit != nil) ||
		(operation.Kind == PhysicalReturnOp && operation.Return != nil) ||
		(operation.Kind == PhysicalPathSeedOp && operation.PathSeed != nil) ||
		(operation.Kind == PhysicalPathExtendOp && operation.PathExtend != nil) ||
		(operation.Kind == PhysicalGraphReturnOp && operation.GraphReturn != nil) ||
		(operation.Kind == PhysicalCollectionScanOp && operation.CollectionScan != nil) ||
		(operation.Kind == PhysicalKeySetLookupOp && operation.KeySetLookup != nil) ||
		(operation.Kind == PhysicalDocumentLookupOp && operation.DocumentLookup != nil) ||
		(operation.Kind == PhysicalPopulationMappingReturnOp && operation.PopulationMappingReturn != nil) ||
		(operation.Kind == PhysicalCellTraceReturnOp && operation.CellTraceReturn != nil) ||
		(operation.Kind == PhysicalGroupRowsOp && operation.GroupRows != nil) ||
		(operation.Kind == PhysicalGroupedPivotOp && operation.GroupedPivot != nil) ||
		(operation.Kind == PhysicalUnpivotOp && operation.Unpivot != nil)
	if !valid {
		return fmt.Errorf("payload does not match operation kind")
	}
	return nil
}

func (cardinality PhysicalCardinality) valid() bool {
	return cardinality == PhysicalScalarCardinality || cardinality == PhysicalArrayCardinality || cardinality == PhysicalObjectCardinality
}

func (behavior PhysicalNullBehavior) valid() bool {
	return behavior == PhysicalPreserveNull || behavior == PhysicalOmitNulls || behavior == PhysicalEmptyOnNull
}
