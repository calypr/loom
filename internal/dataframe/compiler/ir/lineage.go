package ir

import (
	"fmt"
	"reflect"
	"strings"
)

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
			if stage.Group.CodingKey != nil {
				coding := stage.Group.CodingKey
				if coding.RelatedSource != nil {
					if err := validatePhysicalRelatedCodingGroupRowLineage(sequence, terminal, stage, sourceOperations, bindVars); err != nil {
						return err
					}
				} else {
					if len(sequence.Stages) != 1 || sequence.SourceRowIdentity != "__loom_expansion_identity" ||
						coding.SourceStageID != sequence.SourceStageID || coding.SourceOccurrenceKeyColumn != terminal.OccurrenceKeyColumn {
						return fmt.Errorf("row lineage CODING_IDENTITY Group requires its exact expanded input occurrence key")
					}
					rootType := ""
					rootScans := 0
					for _, operation := range sourceOperations {
						if operation.Kind != PhysicalRootScanOp {
							continue
						}
						if operation.RootScan == nil {
							return fmt.Errorf("row lineage Coding Group root scan is invalid")
						}
						rootScans++
						rootType, _ = bindVars[operation.RootScan.CollectionBindKey].(string)
					}
					if rootScans != 1 || rootType == "" || rootType != terminal.ResourceType {
						return fmt.Errorf("row lineage Coding Group requires one root source of its declared resource type")
					}
				}
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

func validatePhysicalRelatedCodingGroupRowLineage(
	sequence PhysicalStageSequence,
	terminal PhysicalRowLineageReturn,
	stage PhysicalConstructionStage,
	sourceOperations []PhysicalOperation,
	bindVars map[string]any,
) error {
	if len(sequence.Stages) != 1 || sequence.SourceRowIdentity != "_key" ||
		stage.Kind != PhysicalStageGroupOp || stage.InputStageID != sequence.SourceStageID ||
		stage.Group == nil || stage.Group.CodingKey == nil || stage.Group.RelatedOccurrence == nil {
		return fmt.Errorf("row lineage related Coding Group requires one terminal direct-root Group")
	}
	group := stage.Group
	coding := group.CodingKey
	related := coding.RelatedSource
	adapter := group.RelatedOccurrence
	if related == nil || coding.SourceUnnest != nil || coding.SourceStageID != sequence.SourceStageID ||
		coding.SourceShapeID == "" || coding.SourceShapeID != related.SourceShapeID ||
		coding.SourceOccurrenceKeyColumn == "" || coding.SourceOccurrenceKeyColumn != adapter.OccurrenceKeyColumn ||
		terminal.OccurrenceKeyColumn != adapter.OccurrenceKeyColumn ||
		coding.MembershipColumn != adapter.CodingMembershipColumn || group.SemanticKey != nil ||
		group.RootContributorInputColumn != "" || group.RootContributorOutputColumn != "" || group.RootContributorVariable != "" {
		return fmt.Errorf("row lineage related Coding Group requires its exact route and occurrence identity without contributor arrays")
	}
	if len(group.Aggregates) == 0 {
		return fmt.Errorf("row lineage related Coding Group requires COUNT_ROWS")
	}
	for _, aggregate := range group.Aggregates {
		if aggregate.Operation != "COUNT_ROWS" || aggregate.InputColumn != "" {
			return fmt.Errorf("row lineage related Coding Group supports only COUNT_ROWS")
		}
	}
	if err := validatePhysicalGroupRelatedOccurrenceAdapter(*group, stage); err != nil {
		return fmt.Errorf("row lineage related Coding Group adapter: %w", err)
	}
	_, rootType, err := validateCodedPivotSourceRoot(sourceOperations, bindVars)
	if err != nil {
		return fmt.Errorf("row lineage related Coding Group source: %w", err)
	}
	if rootType != terminal.ResourceType || related.AnchorOccurrenceID != adapter.RelatedRecords.AnchorOccurrenceID ||
		related.AnchorResourceType != rootType {
		return fmt.Errorf("row lineage related Coding Group must anchor the declared direct root resource")
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
	operation := PhysicalOperation{Kind: PhysicalFilterOp, Filter: &filter}
	return physicalValueReferencesColumn(&operation, variable, column)
}

func physicalValueReferencesColumn(node Node, variable, column string) bool {
	visited := make(map[Node]bool)
	var references func(Node) bool
	references = func(node Node) bool {
		if visited[node] {
			return false
		}
		visited[node] = true
		if value, ok := node.(*PhysicalValue); ok {
			return value.Variable == variable && len(value.Path) != 0 && value.Path[0] == column
		}
		for _, child := range Children(node) {
			if references(child) {
				return true
			}
		}
		return false
	}
	return references(node)
}
