package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// renderComposedRelatedRowLineage replays the exact compiler-decoded identity
// through the canonical construction stages. It carries one scalar witness per
// authored RELATED_EXPAND stage, never a route or contributor frontier.
func (r *physicalPlanRenderer) renderComposedRelatedRowLineage(
	sourceQuery string,
	stages []ir.PhysicalConstructionStage,
	terminal ir.PhysicalRowLineageReturn,
) (RenderedPhysicalPlan, error) {
	trace := terminal.Trace
	if trace == nil || trace.RootKeyBindKey == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage requires a validated root key trace")
	}
	if _, ok := r.bindVars[trace.RootKeyBindKey]; !ok {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage root key bind %q is missing", trace.RootKeyBindKey)
	}
	if terminal.RowIDBindKey == "" || terminal.ResourceType == "" || terminal.ResourceIDColumn == "" || terminal.OccurrenceKeyColumn == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage is missing terminal or root contributor metadata")
	}
	if _, ok := r.bindVars[terminal.RowIDBindKey]; !ok {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage final identity bind %q is missing", terminal.RowIDBindKey)
	}
	if len(stages) == 0 || stages[len(stages)-1].RowIdentityColumn == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage final stage has no identity column")
	}

	matches := make(map[string]ir.PhysicalRowLineageStageMatch, len(trace.Stages))
	for _, match := range trace.Stages {
		if match.StageID == "" || match.Kind != ir.PhysicalStageRelatedExpandOp || match.StageRowIDBindKey == "" || match.RelatedTerminalIDBindKey == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage has an incomplete related-stage match")
		}
		if _, duplicate := matches[match.StageID]; duplicate {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage repeats stage %q", match.StageID)
		}
		if _, ok := r.bindVars[match.StageRowIDBindKey]; !ok {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage stage %q identity bind %q is missing", match.StageID, match.StageRowIDBindKey)
		}
		if _, ok := r.bindVars[match.RelatedTerminalIDBindKey]; !ok {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage stage %q terminal bind %q is missing", match.StageID, match.RelatedTerminalIDBindKey)
		}
		if match.RelatedRowKind != "RELATED" && match.RelatedRowKind != "EMPTY" {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage stage %q has unsupported row kind %q", match.StageID, match.RelatedRowKind)
		}
		matches[match.StageID] = match
	}

	rootSource := r.newInternalVariable("row_lineage_root_source")
	rootKeyColumnBind := r.newInternalBindKey("row_lineage_root_key_column")
	r.bindVars[rootKeyColumnBind] = terminal.OccurrenceKeyColumn
	rootIDColumnBind := r.newInternalBindKey("row_lineage_root_resource_id_column")
	r.bindVars[rootIDColumnBind] = terminal.ResourceIDColumn
	rootTypeBind := r.newInternalBindKey("row_lineage_root_resource_type")
	r.bindVars[rootTypeBind] = terminal.ResourceType
	states := r.newInternalVariable("row_lineage_composed_source_rows")
	lines := []string{
		"LET " + states + " = (",
		"  FOR " + rootSource + " IN (\n" + indentQuery(sourceQuery, "    ") + "\n  )",
		// The compiler also places this same key at the indexed root scan before
		// projection. This equality keeps the private replay fail-closed.
		"  FILTER " + rootSource + "[@" + rootKeyColumnBind + "] == @" + trace.RootKeyBindKey,
		"  RETURN {row: " + rootSource + ", root: {resourceType: @" + rootTypeBind + ", resourceId: " + rootSource + "[@" + rootIDColumnBind + "], occurrenceKey: " + rootSource + "[@" + rootKeyColumnBind + "]}}",
		")",
	}

	witnessFields := make([]string, 0, len(matches))
	traceIndex := 0
	for _, stage := range stages {
		if stage.ID == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage construction stage has no ID")
		}
		if stage.Kind == ir.PhysicalStageRelatedExpandOp {
			match, ok := matches[stage.ID]
			if !ok || match.Kind != stage.Kind || traceIndex >= len(trace.Stages) || trace.Stages[traceIndex].StageID != stage.ID {
				return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage has no ordered validated identity for related stage %q", stage.ID)
			}
			if stage.RelatedExpand == nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage related stage %q has no typed payload", stage.ID)
			}
			traceIndex++
			field := r.newInternalVariable("row_lineage_stage_witness")
			previousFields := append([]string(nil), witnessFields...)
			witnessFields = append(witnessFields, field)
			stageRows, stageLines, err := r.renderComposedRelatedLineageStage(states, stage, match, field, previousFields)
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render composed related stage %q: %w", stage.ID, err)
			}
			lines = append(lines, stageLines...)
			states = stageRows
			continue
		}
		if !composedLineageIdentityPreservingStage(stage.Kind) {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage does not replay stage %q of kind %q", stage.ID, stage.Kind)
		}
		stageRows, stageLines, err := r.renderComposedLineageProjectionStage(states, stage, witnessFields)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render composed identity-preserving stage %q: %w", stage.ID, err)
		}
		lines = append(lines, stageLines...)
		states = stageRows
	}
	if traceIndex != len(trace.Stages) {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage trace contains a stage outside the construction sequence")
	}

	finalState := r.newInternalVariable("row_lineage_composed_final_state")
	finalCandidate := r.newInternalVariable("row_lineage_composed_final_candidate")
	finalIdentityColumnBind := r.newInternalBindKey("row_lineage_final_identity_column")
	r.bindVars[finalIdentityColumnBind] = stages[len(stages)-1].RowIdentityColumn
	lines = append(lines,
		"LET "+finalState+" = FIRST(",
		"  FOR "+finalCandidate+" IN "+states,
		"  FILTER "+finalCandidate+".row[@"+finalIdentityColumnBind+"] == @"+terminal.RowIDBindKey,
		"  RETURN "+finalCandidate,
		")",
	)

	page := r.newInternalVariable("row_lineage_composed_page")
	ordinal := r.newInternalVariable("row_lineage_composed_ordinal")
	contributor := r.newInternalVariable("row_lineage_composed_contributor")
	selected := make([]string, 0, len(witnessFields)+1)
	selected = append(selected, finalState+".root")
	for _, field := range witnessFields {
		selected = append(selected, finalState+"."+field)
	}
	contributorExpression := "null"
	for index := len(selected) - 1; index >= 0; index-- {
		contributorExpression = fmt.Sprintf("%s == %d ? %s : (%s)", ordinal, index, selected[index], contributorExpression)
	}
	lines = append(lines,
		"LET "+page+" = (",
		"  FOR "+ordinal+" IN 0.."+fmt.Sprint(len(selected)-1),
		"  LET "+contributor+" = "+contributorExpression,
		"  FILTER "+finalState+" != null && "+contributor+" != null",
		"  LIMIT @"+terminal.OffsetBindKey+", @"+terminal.FetchLimitBindKey,
		"  RETURN "+contributor,
		")",
		"RETURN {found: "+finalState+" != null, contributors: SLICE("+page+", 0, @"+terminal.LimitBindKey+"), hasMore: LENGTH("+page+") > @"+terminal.LimitBindKey+"}",
	)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

func composedLineageIdentityPreservingStage(kind ir.PhysicalStageOperationKind) bool {
	switch kind {
	case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageRelatedEligibilityOp,
		ir.PhysicalStageRelatedSourceOp, ir.PhysicalStageRelatedFieldOp:
		return true
	default:
		return false
	}
}

func (r *physicalPlanRenderer) renderComposedLineageProjectionStage(
	priorRows string,
	stage ir.PhysicalConstructionStage,
	witnessFields []string,
) (string, []string, error) {
	states := r.newInternalVariable("row_lineage_composed_projection_rows")
	state := r.newInternalVariable("row_lineage_composed_projection_state")
	lines := []string{"LET " + states + " = (", "  FOR " + state + " IN " + priorRows,
		"  LET " + stage.InputRowVariable + " = " + state + ".row"}
	for index, operation := range stage.DerivedLets {
		if operation.Kind != ir.PhysicalExpressionLetOp {
			return "", nil, fmt.Errorf("derived operation %d has kind %q", index, operation.Kind)
		}
		rendered, err := r.renderExpressionLet(operation, "  ")
		if err != nil {
			return "", nil, fmt.Errorf("render derived operation %d: %w", index, err)
		}
		lines = append(lines, rendered...)
	}
	if stage.Filter != nil {
		rendered, err := r.renderScopeOperation(ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: stage.Filter}, "  ")
		if err != nil {
			return "", nil, fmt.Errorf("render filter: %w", err)
		}
		lines = append(lines, rendered...)
	}
	output, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
	if err != nil {
		return "", nil, fmt.Errorf("render output row: %w", err)
	}
	lines = append(lines, "  LET "+stage.OutputRowVariable+" = "+output)
	fields := []string{"row: " + stage.OutputRowVariable, "root: " + state + ".root"}
	for _, field := range witnessFields {
		fields = append(fields, field+": "+state+"."+field)
	}
	lines = append(lines, "  RETURN {"+strings.Join(fields, ", ")+"}", ")")
	return states, lines, nil
}

func (r *physicalPlanRenderer) renderComposedRelatedLineageStage(
	priorRows string,
	stage ir.PhysicalConstructionStage,
	match ir.PhysicalRowLineageStageMatch,
	witnessField string,
	previousWitnessFields []string,
) (string, []string, error) {
	related := stage.RelatedExpand
	if match.RelatedRowKind == "EMPTY" && related.EmptyPolicy != ir.PhysicalUnnestPreserveParent {
		return "", nil, fmt.Errorf("empty identity requires PRESERVE_PARENT")
	}
	input := stage.InputRowVariable
	state := r.newInternalVariable("row_lineage_composed_related_state")
	rows := r.newInternalVariable("row_lineage_composed_related_rows")
	item := related.ItemVariable
	if item == "" || related.IdentityVariable == "" || related.ParentIdentityColumn == "" {
		return "", nil, fmt.Errorf("related stage is missing identity variables")
	}

	// The exact terminal predicate follows the complete authorized route. The
	// bounded subplan returns at most one terminal witness, even for duplicate
	// edge paths to the same canonical target.
	subplan := related.RelatedRecords
	subplan.Sort, subplan.Unique, subplan.DistinctBy = nil, false, nil
	if match.RelatedRowKind == "RELATED" {
		terminalVariable := ""
		for _, operation := range subplan.Operations {
			if operation.Kind == ir.PhysicalTraversalOp && operation.Traversal != nil {
				terminalVariable = operation.Traversal.TargetVariable
			}
		}
		if terminalVariable == "" {
			return "", nil, fmt.Errorf("related route has no terminal traversal")
		}
		operations := append([]ir.PhysicalOperation(nil), subplan.Operations...)
		operations = append(operations, ir.PhysicalOperation{
			Kind: ir.PhysicalFilterOp,
			Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: terminalVariable, Path: []string{"_id"}},
				Right: &ir.PhysicalValue{BindKey: match.RelatedTerminalIDBindKey},
			}},
		})
		subplan.Operations = operations
	}
	route, err := r.renderSubplan(subplan, "  ", true)
	if err != nil {
		return "", nil, fmt.Errorf("render exact authorized route: %w", err)
	}

	lines := []string{"LET " + rows + " = (", "  FOR " + state + " IN " + priorRows,
		"  LET " + input + " = " + state + ".row",
		"  LET " + item + " = FIRST(" + route + ")"}
	if match.RelatedRowKind == "EMPTY" {
		lines = append(lines, "  FILTER "+item+" == null")
	} else {
		lines = append(lines, "  FILTER "+item+" != null")
	}
	parentIdentityColumnBind := r.newInternalBindKey("row_lineage_related_parent_identity_column")
	r.bindVars[parentIdentityColumnBind] = related.ParentIdentityColumn
	identity := constructionRelatedExpandIdentityExpression(input, parentIdentityColumnBind, related.ConstructionIDBindKey, item)
	output, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
	if err != nil {
		return "", nil, fmt.Errorf("render related output row: %w", err)
	}
	lines = append(lines,
		"  LET "+related.IdentityVariable+" = "+identity,
		"  LET "+stage.OutputRowVariable+" = "+output,
	)
	rowIdentityColumnBind := r.newInternalBindKey("row_lineage_related_stage_identity_column")
	r.bindVars[rowIdentityColumnBind] = stage.RowIdentityColumn
	lines = append(lines, "  FILTER "+stage.OutputRowVariable+"[@"+rowIdentityColumnBind+"] == @"+match.StageRowIDBindKey)
	witness := "null"
	if match.RelatedRowKind == "RELATED" {
		targetTypeBind := r.newInternalBindKey("row_lineage_related_resource_type")
		r.bindVars[targetTypeBind] = related.TargetResourceType
		witness = "{resourceType: @" + targetTypeBind + ", resourceId: " + item + ".resource_id, occurrenceKey: PARSE_IDENTIFIER(" + item + ".terminal_id).key}"
	}
	fields := []string{"row: " + stage.OutputRowVariable, "root: " + state + ".root"}
	for _, field := range previousWitnessFields {
		fields = append(fields, field+": "+state+"."+field)
	}
	fields = append(fields, witnessField+": "+witness)
	lines = append(lines, "  RETURN {"+strings.Join(fields, ", ")+"}", ")")
	return rows, lines, nil
}
