package ir

import (
	"fmt"
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
		switch stage.Kind {
		case PhysicalStageDeriveOp:
			if stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil {
				return fmt.Errorf("%s DERIVE has mismatched operation payloads", path)
			}
			if len(stage.DerivedLets) == 0 {
				return fmt.Errorf("%s DERIVE requires at least one typed expression LET", path)
			}
			if err := validateStageRowOperations(stage, bindVars, false); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageFilterOp:
			if stage.Filter == nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
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
			if err := validatePhysicalGroupedPivot(*stage.GroupedPivot, defined, bindVars); err != nil {
				return fmt.Errorf("%s grouped pivot: %w", path, err)
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
			expected := make([]string, 0, len(stage.Group.Keys)+len(stage.Group.Aggregates)+1)
			for _, key := range stage.Group.Keys {
				expected = append(expected, key.OutputColumn)
			}
			for _, aggregate := range stage.Group.Aggregates {
				expected = append(expected, aggregate.Output)
			}
			expected = append(expected, "__loom_row_id")
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
		default:
			return fmt.Errorf("%s has unsupported operation kind %q", path, stage.Kind)
		}
		priorStageID, priorColumns, priorRowIdentity = stage.ID, stage.OutputColumns, stage.RowIdentityColumn
	}
	last := sequence.Stages[len(sequence.Stages)-1]
	if sequence.FinalStageID != last.ID || sequence.FinalRowIdentity != last.RowIdentityColumn {
		return fmt.Errorf("final stage or row identity does not match the final operation")
	}
	if !samePhysicalStageColumns(sequence.FinalColumns, last.OutputColumns) {
		return fmt.Errorf("final output schema differs from the final stage")
	}
	return nil
}

func validatePhysicalStageGroup(stage PhysicalConstructionStage, group PhysicalStageGroup, bindVars map[string]any) error {
	if len(group.Keys) == 0 && len(group.Aggregates) == 0 {
		return fmt.Errorf("requires at least one key or aggregate")
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
	if !ok || item.Internal || !itemScalar || itemKind != expand.InputKind || item.Cardinality != "one" && item.Cardinality != "optional_one" {
		return fmt.Errorf("item output column %q does not match the array item type", expand.OutputColumn)
	}
	if expand.OrdinalColumn != "" {
		ordinal, ok := outputByName[expand.OrdinalColumn]
		if !ok || ordinal.Internal || ordinal.Kind != "integer" || ordinal.Cardinality != "one" && ordinal.Cardinality != "optional_one" {
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
		if a.ID != b.ID || a.Name != b.Name || a.Label != b.Label || a.Kind != b.Kind || a.Cardinality != b.Cardinality || a.Nullable != b.Nullable || a.Internal != b.Internal || a.Identity != b.Identity {
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
	columns := make([]string, 0, len(pivot.GroupKeys)+len(pivot.Categories)+2)
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
	if pivot.UnlistedEvidenceColumn != "" {
		columns = append(columns, pivot.UnlistedEvidenceColumn)
	}
	columns = append(columns, "__loom_row_id")
	return columns
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
	actual := make(map[string]bool, len(stage.OutputColumns))
	for _, column := range stage.OutputColumns {
		actual[column.Name] = true
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("shape output schema has %d columns, operation emits %d", len(actual), len(expected))
	}
	for _, name := range expected {
		if !actual[name] {
			return fmt.Errorf("shape output schema is missing emitted column %q", name)
		}
	}
	return nil
}
