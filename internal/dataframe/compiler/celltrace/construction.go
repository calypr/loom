package celltrace

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/population"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func compileGroupRowsCellTrace(output lower.CompiledRecipeOutput, physical *ir.PhysicalPlan, column string, offset, limit int) (CompiledCellTraceQuery, error) {
	var cardinality ir.PhysicalCardinality
	for _, candidate := range output.OutputSchema {
		if candidate.Name != column || candidate.Internal {
			continue
		}
		cardinality = ir.PhysicalScalarCardinality
		if candidate.Cardinality == "many" {
			cardinality = ir.PhysicalArrayCardinality
		}
		break
	}
	if cardinality == "" {
		return CompiledCellTraceQuery{}, fmt.Errorf("cell trace column %q is not a public group output", column)
	}
	const offsetBind, limitBind, fetchLimitBind = "cell_trace_offset", "cell_trace_limit", "cell_trace_fetch_limit"
	physical.BindVars[offsetBind] = offset
	physical.BindVars[limitBind] = limit
	physical.BindVars[fetchLimitBind] = limit + 1
	physical.Operations[0].GroupRows.CellTrace = &ir.PhysicalGroupRowsCellTrace{
		OutputColumn: column, Cardinality: cardinality,
		OffsetBindKey: offsetBind, LimitBindKey: limitBind, FetchLimitBindKey: fetchLimitBind,
	}
	if err := physical.Validate(); err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("validate group-row cell trace plan: %w", err)
	}
	rendered, err := aql.RenderPhysicalPlan(*physical)
	if err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("render group-row cell trace physical plan: %w", err)
	}
	return CompiledCellTraceQuery{
		Query: rendered.Query, BindVars: rendered.BindVars,
		ValueColumn: ir.PhysicalCellTraceValueField, ContributionsColumn: ir.PhysicalCellTraceContributionsField,
		StatusColumn: ir.PhysicalCellTraceStatusField, HasMoreColumn: ir.PhysicalCellTraceHasMoreField,
		OmissionColumn: ir.PhysicalCellTraceOmissionField, ExplicitIdentityColumn: ir.PhysicalCellTraceExplicitIdentityField,
		ExplicitIdentityObject: true, RowIdentity: output.RowIdentity.Clone(),
		ContributionOffset: offset, ContributionLimit: limit, Diagnostics: ir.PhysicalPlanDiagnostics(*physical),
	}, nil
}

func compileConstructionCellTrace(output lower.CompiledRecipeOutput, physical *ir.PhysicalPlan, column string, offset, limit int) (CompiledCellTraceQuery, error) {
	if physical == nil || physical.StageSequence == nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("construction cell trace requires a stage sequence")
	}
	lineage, err := ConstructionCellTraceLineage(output, *physical.StageSequence, column)
	if err != nil {
		return CompiledCellTraceQuery{}, err
	}
	const offsetBind, limitBind, fetchLimitBind = "cell_trace_offset", "cell_trace_limit", "cell_trace_fetch_limit"
	physical.BindVars[offsetBind] = offset
	physical.BindVars[limitBind] = limit
	physical.BindVars[fetchLimitBind] = limit + 1
	physical.StageSequence.CellTraceReturn = &ir.PhysicalCellTraceReturn{
		Construction: lineage, OffsetBindKey: offsetBind, LimitBindKey: limitBind,
		FetchLimitBindKey: fetchLimitBind,
	}
	if err := physical.Validate(); err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("validate construction cell trace plan: %w", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(*physical); err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("verify construction cell trace physical scope: %w", err)
	}
	rendered, err := aql.RenderPhysicalPlan(*physical)
	if err != nil {
		return CompiledCellTraceQuery{}, fmt.Errorf("render construction cell trace physical plan: %w", err)
	}
	query := CompiledCellTraceQuery{
		Query: rendered.Query, BindVars: rendered.BindVars,
		ValueColumn: ir.PhysicalCellTraceValueField, ContributionsColumn: ir.PhysicalCellTraceContributionsField,
		StatusColumn: ir.PhysicalCellTraceStatusField, HasMoreColumn: ir.PhysicalCellTraceHasMoreField,
		OmissionColumn: ir.PhysicalCellTraceOmissionField, RowIdentity: output.RowIdentity.Clone(),
		ContributionOffset: offset, ContributionLimit: limit, Diagnostics: ir.PhysicalPlanDiagnostics(*physical),
	}
	if lineage.RowIdentityColumn == "__loom_row_id" {
		query.ExplicitIdentityColumn = ir.PhysicalCellTraceExplicitIdentityField
		query.ExplicitIdentityObject, err = constructionCellTraceIdentityIsObject(physical.StageSequence, lineage.RowIdentityColumn)
		if err != nil {
			return CompiledCellTraceQuery{}, err
		}
	} else {
		query.IdentityPartsColumn = ir.PhysicalCellTraceIdentityPartsField
	}
	return query, nil
}

func constructionCellTraceIdentityIsObject(sequence *ir.PhysicalStageSequence, columnName string) (bool, error) {
	if sequence == nil || strings.TrimSpace(columnName) == "" {
		return false, fmt.Errorf("construction cell trace has no typed final identity")
	}
	for _, column := range sequence.FinalColumns {
		if column.Name != columnName {
			continue
		}
		if column.Kind != "object" {
			return false, nil
		}
		if !column.Internal || !column.Identity || column.Cardinality != "required_one" {
			return false, fmt.Errorf("construction cell trace object identity is not a required compiler-owned identity")
		}
		return true, nil
	}
	return false, fmt.Errorf("construction cell trace final identity column %q is missing", columnName)
}

func ConstructionCellTraceLineage(output lower.CompiledRecipeOutput, sequence ir.PhysicalStageSequence, column string) (*ir.PhysicalCellTraceConstruction, error) {
	var selected ir.PhysicalStageColumn
	found := false
	for _, candidate := range sequence.FinalColumns {
		if candidate.Name == column && !candidate.Internal {
			selected, found = candidate, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("cell trace column %q is not a public construction output", column)
	}
	identityFields, err := constructionCellTraceIdentityFields(output, sequence)
	if err != nil {
		return nil, fmt.Errorf("construction cell trace row identity does not match the final stage")
	}
	lineage := &ir.PhysicalCellTraceConstruction{
		FinalStageID: sequence.FinalStageID, RowIdentityColumn: sequence.FinalRowIdentity,
		RowIdentityFields: identityFields,
		OutputColumnID:    selected.ID, OutputColumn: selected.Name,
	}
	finalByID := make(map[string]ir.PhysicalStageColumn, len(sequence.FinalColumns))
	for _, finalColumn := range sequence.FinalColumns {
		finalByID[finalColumn.ID] = finalColumn
	}

	producerIndex := -1
	for index := len(sequence.Stages) - 1; index >= 0; index-- {
		stage := sequence.Stages[index]
		outputColumn, hasOutput := stageColumnByID(stage.OutputColumns, selected.ID)
		if !hasOutput {
			continue
		}
		if _, wasInput := stageColumnByID(stage.InputColumns, selected.ID); !wasInput {
			producerIndex = index
			lineage.ProducerStageID = stage.ID
			lineage.Operation = string(stage.Kind)
			if descriptor, ok := constructionStageDescriptor(output.Stages, stage.ID); ok {
				if descriptorColumn, ok := compiledStageColumnByID(descriptor.Columns, selected.ID); ok && strings.HasPrefix(descriptorColumn.SemanticPath, "derived:") {
					lineage.ConstructionID = strings.TrimPrefix(descriptorColumn.SemanticPath, "derived:")
				}
			}
			if stage.Kind == ir.PhysicalStageRelatedSourceOp {
				related, supported := constructionRelatedSourceTrace(stage, outputColumn.Name)
				if !supported {
					lineage.OmissionCode = "CONSTRUCTION_TRACE_RELATED_SOURCE_UNAVAILABLE"
					break
				}
				lineage.RelatedSource = related
				break
			}
			if stage.Kind != ir.PhysicalStageDeriveOp {
				lineage.OmissionCode = "CONSTRUCTION_TRACE_OPERATION_UNSUPPORTED"
				break
			}
			if lineage.ConstructionID == "" {
				lineage.OmissionCode = "CONSTRUCTION_TRACE_CONSTRUCTION_ID_UNAVAILABLE"
				break
			}
			inputs, supported := constructionDerivedTraceInputs(stage, outputColumn.Name)
			if !supported {
				lineage.OmissionCode = "CONSTRUCTION_TRACE_EXPRESSION_UNSUPPORTED"
				break
			}
			if len(inputs) == 0 {
				lineage.OmissionCode = "CONSTRUCTION_TRACE_NO_SOURCE_COLUMNS"
				break
			}
			for _, input := range inputs {
				finalInput, survives := finalByID[input.ID]
				if !survives || finalInput.Internal {
					lineage.OmissionCode = "CONSTRUCTION_TRACE_INPUT_NOT_PRESERVED"
					lineage.Inputs = nil
					break
				}
				lineage.Inputs = append(lineage.Inputs, ir.PhysicalCellTraceConstructionInput{
					StageID: stage.InputStageID, ColumnID: input.ID, Column: input.Name,
					FinalValueColumn: finalInput.Name,
				})
			}
			break
		}
		if stage.Kind != ir.PhysicalStageDeriveOp && stage.Kind != ir.PhysicalStageFilterOp && stage.Kind != ir.PhysicalStageRelatedSourceOp {
			lineage.OmissionCode = "CONSTRUCTION_TRACE_OPERATION_UNSUPPORTED"
			producerIndex = index
			lineage.ProducerStageID = stage.ID
			lineage.Operation = string(stage.Kind)
			break
		}
	}

	if producerIndex < 0 {
		sourceColumn, exists := stageColumnByID(sequence.SourceColumns, selected.ID)
		if !exists || sourceColumn.Internal {
			return nil, fmt.Errorf("cell trace output column ID %q has no source or construction producer", selected.ID)
		}
		lineage.ProducerStageID = sequence.SourceStageID
		lineage.Operation = "SOURCE_PROJECTION"
		lineage.Inputs = []ir.PhysicalCellTraceConstructionInput{{
			StageID: sequence.SourceStageID, ColumnID: sourceColumn.ID, Column: sourceColumn.Name,
			FinalValueColumn: selected.Name,
		}}
		for _, stage := range sequence.Stages {
			if stage.Kind != ir.PhysicalStageDeriveOp && stage.Kind != ir.PhysicalStageFilterOp && stage.Kind != ir.PhysicalStageRelatedSourceOp {
				lineage.OmissionCode = "CONSTRUCTION_TRACE_OPERATION_UNSUPPORTED"
				lineage.Inputs = nil
				break
			}
		}
	}
	if lineage.OmissionCode == "" && producerIndex >= 0 {
		for _, stage := range sequence.Stages[producerIndex+1:] {
			if stage.Kind != ir.PhysicalStageDeriveOp && stage.Kind != ir.PhysicalStageFilterOp && stage.Kind != ir.PhysicalStageRelatedSourceOp {
				lineage.OmissionCode = "CONSTRUCTION_TRACE_OPERATION_UNSUPPORTED"
				lineage.Inputs = nil
				break
			}
			for _, input := range lineage.Inputs {
				if _, exists := stageColumnByID(stage.InputColumns, input.ColumnID); !exists {
					lineage.OmissionCode = "CONSTRUCTION_TRACE_INPUT_NOT_PRESERVED"
					lineage.Inputs = nil
					break
				}
			}
			if lineage.OmissionCode != "" {
				break
			}
		}
	}
	return lineage, nil
}

func constructionCellTraceIdentityFields(output lower.CompiledRecipeOutput, sequence ir.PhysicalStageSequence) ([]string, error) {
	identity := output.RowIdentity
	if identity == nil || identity.Validate() != nil || identity.Grain != output.RowGrain {
		return nil, fmt.Errorf("compiled row identity is malformed")
	}
	if len(identity.Fields) == 1 && identity.Fields[0] == sequence.FinalRowIdentity {
		return append([]string(nil), identity.Fields...), nil
	}
	defaultIdentity, ok := spec.DefaultRowIdentity(output.RowGrain)
	if !ok || len(identity.Fields) != len(defaultIdentity.Fields) ||
		identity.Fields[0] != defaultIdentity.Fields[0] || identity.Fields[1] != defaultIdentity.Fields[1] ||
		defaultIdentity.Fields[0] != "project" || defaultIdentity.Fields[1] != "_key" {
		return nil, fmt.Errorf("compiled row identity is not the construction final identity or canonical project/key identity")
	}
	switch sequence.FinalRowIdentity {
	case "_key":
		project, ok := output.Plan.BindVars["project"].(string)
		if !ok || strings.TrimSpace(project) == "" {
			return nil, fmt.Errorf("canonical project/key identity requires the pinned project bind")
		}
		return append([]string(nil), identity.Fields...), nil
	case "__loom_row_id":
		for _, column := range sequence.FinalColumns {
			if column.Name == "__loom_row_id" && column.Internal && column.Identity && column.Kind == "string" && column.Cardinality == "required_one" {
				return []string{"__loom_row_id"}, nil
			}
		}
		return nil, fmt.Errorf("compiler-generated construction identity is not a required scalar string")
	default:
		return nil, fmt.Errorf("canonical project/key identity is incompatible with final stage identity %q", sequence.FinalRowIdentity)
	}
}

func constructionRelatedSourceTrace(stage ir.PhysicalConstructionStage, outputColumn string) (*ir.PhysicalCellTraceRelatedSource, bool) {
	if stage.Kind != ir.PhysicalStageRelatedSourceOp || stage.RelatedSource == nil {
		return nil, false
	}
	projection, found := StageProjectionByName(stage.OutputProjections, outputColumn)
	if !found || projection.Expression == nil {
		return nil, false
	}
	subplanSource := projection.Expression
	if stage.RelatedSource.Form == "PRESENCE" && subplanSource.Call != nil && len(subplanSource.Call.Args) == 2 {
		subplanSource = &subplanSource.Call.Args[0]
	}
	if stage.RelatedSource.Form != "ALL" && subplanSource.Call != nil && len(subplanSource.Call.Args) == 1 {
		subplanSource = &subplanSource.Call.Args[0]
	}
	if subplanSource.Kind != ir.PhysicalSubplanExpression || subplanSource.Subplan == nil ||
		subplanSource.Cardinality != ir.PhysicalArrayCardinality || subplanSource.NullBehavior != ir.PhysicalEmptyOnNull {
		return nil, false
	}
	anchor, found := stageColumnByID(stage.InputColumns, stage.RelatedSource.AnchorColumnID)
	if !found || !anchor.Internal || !anchor.Identity || anchor.Name != "_key" {
		return nil, false
	}
	subplan := ir.ClonePhysicalSubplan(*subplanSource.Subplan)
	if len(subplan.Captures) != 1 || subplan.Captures[0] != stage.InputRowVariable {
		return nil, false
	}
	return &ir.PhysicalCellTraceRelatedSource{
		InputRowVariable: stage.InputRowVariable,
		AnchorColumn:     anchor.Name,
		ResourceType:     stage.RelatedSource.ResourceType,
		Subplan:          subplan,
	}, true
}

func constructionDerivedTraceInputs(stage ir.PhysicalConstructionStage, outputColumn string) ([]ir.PhysicalStageColumn, bool) {
	projection, found := StageProjectionByName(stage.OutputProjections, outputColumn)
	if !found {
		return nil, false
	}
	expression, err := population.PhysicalProjectionExpression(projection)
	if err != nil {
		return nil, false
	}
	lets := make(map[string]ir.PhysicalExpression, len(stage.DerivedLets))
	for _, operation := range stage.DerivedLets {
		if operation.Kind != ir.PhysicalExpressionLetOp || operation.ExpressionLet == nil {
			return nil, false
		}
		lets[operation.ExpressionLet.Variable] = operation.ExpressionLet.Expression
	}
	names := make([]string, 0, 2)
	visiting := map[string]bool{}
	if !collectConstructionTraceInputNames(expression, stage.InputRowVariable, lets, visiting, &names) {
		return nil, false
	}
	columns := make(map[string]ir.PhysicalStageColumn, len(stage.InputColumns))
	for _, input := range stage.InputColumns {
		columns[input.Name] = input
	}
	seen := map[string]bool{}
	result := make([]ir.PhysicalStageColumn, 0, len(names))
	for _, name := range names {
		input, ok := columns[name]
		if !ok || input.Internal {
			return nil, false
		}
		if seen[input.ID] {
			continue
		}
		seen[input.ID] = true
		result = append(result, input)
	}
	return result, true
}

func collectConstructionTraceInputNames(expression ir.PhysicalExpression, inputRow string, lets map[string]ir.PhysicalExpression, visiting map[string]bool, names *[]string) bool {
	switch expression.Kind {
	case ir.PhysicalLiteralExpression:
		return expression.Literal != nil
	case ir.PhysicalValueExpression:
		if expression.Value == nil {
			return false
		}
		value := expression.Value
		if value.Variable == inputRow && len(value.Path) == 1 {
			*names = append(*names, value.Path[0])
			return true
		}
		if value.Variable == "" || len(value.Path) != 0 || visiting[value.Variable] {
			return false
		}
		let, ok := lets[value.Variable]
		if !ok {
			return false
		}
		visiting[value.Variable] = true
		defer delete(visiting, value.Variable)
		return collectConstructionTraceInputNames(let, inputRow, lets, visiting, names)
	case ir.PhysicalCallExpression:
		if expression.Call == nil {
			return false
		}
		for _, argument := range expression.Call.Args {
			if !collectConstructionTraceInputNames(argument, inputRow, lets, visiting, names) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
