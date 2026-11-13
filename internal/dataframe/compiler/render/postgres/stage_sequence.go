package postgres

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	stageExpandArrayTypeTag = "CONSTRUCTION_EXPAND_ARRAY_TYPE_MISMATCH"
	stageExpandItemTypeTag  = "CONSTRUCTION_EXPAND_ITEM_TYPE_MISMATCH"
	stageExpandEmptyTag     = "CONSTRUCTION_EXPANSION_EMPTY"
)

const stageSequenceCTEPrefix = "__loom_"

func stageSequenceCompositionMentions(plan ir.PhysicalPlan) bool {
	return plan.StageSequence != nil && len(plan.StageSequence.Stages) >= 2
}

func stageSequenceRelatedOccurrenceMentions(plan ir.PhysicalPlan) bool {
	if plan.StageSequence == nil {
		return false
	}
	for _, stage := range plan.StageSequence.Stages {
		if stage.Group != nil && stage.Group.RelatedOccurrence != nil {
			return true
		}
	}
	return false
}

func renderStageSequencePhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateStageSequencePlanSurface(plan, false); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := plan.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}
	if plan.Engine != "" && plan.Engine != ir.PhysicalEngineAQL {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: engine %q has no PostgreSQL source; the source graph IR is backend-neutral and ClickHouse plans are not PostgreSQL-rendered",
			ErrUnsupportedPostgresPlan, string(plan.Engine))
	}
	if plan.ClickHouseCombine != nil || plan.ClickHousePrefix != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: ClickHouse combine/prefix payloads have no PostgreSQL rendering", ErrUnsupportedPostgresPlan)
	}
	if len(plan.DeferredExpressionLets) != 0 {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: completed plans must not carry deferred expression lets", ErrUnsupportedPostgresPlan)
	}
	renderer := &planRenderer{
		storage:           storage,
		rows:              map[string]rowScope{},
		parameters:        map[string]int{},
		canonicalAuthLets: map[string]string{},
	}
	if err := renderer.loadScope(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	sql, err := renderer.renderStageSequenceStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

func validateStageSequencePlanSurface(plan ir.PhysicalPlan, mappingTerminal bool) error {
	if plan.StageSequence == nil {
		return fmt.Errorf("%w: composition dispatch requires a stage sequence", ErrUnsupportedPostgresPlan)
	}
	sequence := plan.StageSequence
	if len(sequence.Stages) == 0 {
		return fmt.Errorf("%w: stage %d ID is empty, untrimmed, or duplicated — the composition door requires at least one construction stage (ir/physical_stage_validation.go:15 re-read at the door)",
			ErrUnsupportedPostgresPlan, 0)
	}
	if sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil ||
		(sequence.PopulationMappingReturn != nil && !mappingTerminal) {
		return fmt.Errorf("%w: row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route (compiler/row_lineage.go:655, cell_trace.go:54, population_mapping.go:38)",
			ErrUnsupportedPostgresPlan)
	}
	if mappingTerminal && sequence.PopulationMappingReturn == nil {
		return fmt.Errorf("%w: the population-mapping surface requires the stage sequence's PopulationMappingReturn payload",
			ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow {
		return fmt.Errorf("%w: preview source windowing and the terminal Pivot window are pg-sql-22/23 terminal windowing contracts and are refused, never silently rendered",
			ErrUnsupportedPostgresPlan)
	}
	if sequence.OutputAuthResourcePathBindKey != "" {
		return fmt.Errorf("%w: the composite Combine prefix authorization path is not registered for composed sequences (ir/physical_stage_sequence.go:25-28; the auth path column registers with pg-ir-03)",
			ErrUnsupportedPostgresPlan)
	}
	if sequence.PreviewLimitBindKey != "" {
		if value, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int); !ok || value <= 0 {
			return fmt.Errorf("%w: composition preview limit bind %q must be a positive int",
				ErrUnsupportedPostgresPlan, sequence.PreviewLimitBindKey)
		}
	}
	for index, stage := range sequence.Stages {
		switch {
		case stage.Kind == ir.PhysicalStageCodedGroupOp || stage.CodedGroup != nil:
			return fmt.Errorf("%w: the registered coded-group slice admits exactly one CODED_GROUP construction stage; multi-stage composition is a pg-sql-16 capability gap",
				ErrUnsupportedPostgresPlan)
		case stage.Kind == ir.PhysicalStageCohortGroupOp:
			return fmt.Errorf("%w: the COHORT_GROUP stage (index %d) is not part of the registered composition surface: the explicit-cohort boundary has no PostgreSQL rendering and its lineage surfaces refuse at their own doors (compiler/row_lineage.go:655)",
				ErrUnsupportedPostgresPlan, index)
		case stage.GroupedPivot != nil && stage.GroupedPivot.CodedCorrelation != nil:
			if index == 0 {
				return fmt.Errorf("%w: multi-stage construction sequences are the pg-sql-16 capability; the registered slice admits only the inline single-stage CODED_PIVOT",
					ErrUnsupportedPostgresPlan)
			}
			return fmt.Errorf("%w: a coded Pivot at stage index %d violates the ir rule that the coded Pivot must be the first stage over the direct source projection (ir/physical_stage_validation.go:106-108); coded-pivot composition beyond stage 1 stays the pg-sql-22/23 adjacency",
				ErrUnsupportedPostgresPlan, index)
		}
	}
	if err := validateStageSequenceOperations(plan, mappingTerminal); err != nil {
		return err
	}
	return validateStageSequenceChain(plan, mappingTerminal)
}

func validateStageSequenceOperations(plan ir.PhysicalPlan, mappingTerminal bool) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	first := plan.Operations[0]
	if first.Kind != ir.PhysicalRootScanOp || first.RootScan == nil {
		return fmt.Errorf("%w: the composition source requires exactly one leading ROOT_SCAN", ErrUnsupportedPostgresPlan)
	}
	scan := first.RootScan
	membershipRoot := scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil
	if membershipRoot && !(mappingTerminal && scan.Population != nil && scan.CohortSource == nil && scan.PageCandidateSeed == nil) {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are bound only by RenderPopulationRootScan (population.go); the composition door refuses them",
			ErrUnsupportedPostgresPlan)
	}
	if mappingTerminal && scan.Population != nil && scan.Population.CollectMembersVariable != ir.PopulationMappingMembersVariable {
		return fmt.Errorf("%w: the composition member-collecting root is registered only for CollectMembersVariable %q (compiler/population_mapping.go); got %q",
			ErrUnsupportedPostgresPlan, ir.PopulationMappingMembersVariable, scan.Population.CollectMembersVariable)
	}
	sawSort, sawLimit, sawReturn := false, false, false
	for index, operation := range plan.Operations {
		last := index == len(plan.Operations)-1
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return fmt.Errorf("%w: exactly one leading ROOT_SCAN is required", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp:
			if sawSort || sawLimit || sawReturn {
				return fmt.Errorf("%w: source operations must precede the execution window and the terminal RETURN",
					ErrUnsupportedPostgresPlan)
			}
			if operation.Kind == ir.PhysicalFilterOp && operation.Filter == nil {
				return fmt.Errorf("%w: operation %d: filter payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if operation.Kind == ir.PhysicalDerivedLetOp && operation.DerivedLet == nil {
				return fmt.Errorf("%w: operation %d: derived-let payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalUnnestOp:
			if sawSort || sawLimit || sawReturn {
				return fmt.Errorf("%w: source expansions must precede the execution window and the terminal RETURN",
					ErrUnsupportedPostgresPlan)
			}
			if operation.Unnest == nil {
				return fmt.Errorf("%w: operation %d: unnest payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalSetOp:
			if sawSort || sawLimit || sawReturn {
				return fmt.Errorf("%w: source child sets must precede the execution window and the terminal RETURN",
					ErrUnsupportedPostgresPlan)
			}
			if operation.Set == nil {
				return fmt.Errorf("%w: operation %d: set payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalExpressionLetOp:
			return fmt.Errorf("%w: source-region EXPRESSION_LET has no dispatch arm in the composition builder and is refused fail-closed (L-16.5, probe P-14.3 twin)",
				ErrUnsupportedPostgresPlan)
		case ir.PhysicalSortOp:
			if sawSort || sawLimit || sawReturn {
				return fmt.Errorf("%w: at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan)
			}
			if operation.Sort == nil {
				return fmt.Errorf("%w: operation %d: sort payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			sawSort = true
		case ir.PhysicalLimitOp:
			if sawLimit || sawReturn {
				return fmt.Errorf("%w: at most one LIMIT", ErrUnsupportedPostgresPlan)
			}
			if operation.Limit == nil {
				return fmt.Errorf("%w: operation %d: limit payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			sawLimit = true
		case ir.PhysicalReturnOp:
			if !last || operation.Return == nil || len(operation.Return.Projections) == 0 {
				return fmt.Errorf("%w: the composition source requires a terminal RETURN with its projection list",
					ErrUnsupportedPostgresPlan)
			}
			sawReturn = true
		default:
			return fmt.Errorf("%w: operation %q is not part of the registered composition source surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if !sawReturn {
		return fmt.Errorf("%w: the composition source requires a terminal RETURN", ErrUnsupportedPostgresPlan)
	}
	return nil
}

func validateStageSequenceChain(plan ir.PhysicalPlan, mappingTerminal bool) error {
	sequence := plan.StageSequence
	if sequence.SourceStageID == "" || sequence.SourceRowIdentity == "" || sequence.FinalStageID == "" || sequence.FinalRowIdentity == "" {
		return fmt.Errorf("%w: source/final stage IDs and row identity columns are required (ir/physical_stage_validation.go:12 re-read at the door)",
			ErrUnsupportedPostgresPlan)
	}
	projectionNames := map[string]bool{}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalGroupedPivotOp || operation.Kind == ir.PhysicalUnpivotOp {
			return fmt.Errorf("%w: source projection cannot contain a terminal table reshape (ir/physical_stage_validation.go:33 re-read at the door)",
				ErrUnsupportedPostgresPlan)
		}
		if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
			for _, projection := range operation.Return.Projections {
				projectionNames[projection.Name] = true
			}
		}
	}
	if err := validateStageColumns(sequence.SourceColumns, sequence.SourceRowIdentity); err != nil {
		return fmt.Errorf("%w: composition source projection schema: %v", ErrUnsupportedPostgresPlan, err)
	}
	for _, column := range sequence.SourceColumns {
		if !projectionNames[column.Name] {
			return fmt.Errorf("%w: source projection column %q is missing from the typed source RETURN (ir/physical_stage_validation.go:46 re-read at the door)",
				ErrUnsupportedPostgresPlan, column.Name)
		}
	}
	priorStageID := sequence.SourceStageID
	priorColumns := sequence.SourceColumns
	priorRowIdentity := sequence.SourceRowIdentity
	seenIDs := map[string]bool{sequence.SourceStageID: true}
	for index, stage := range sequence.Stages {
		path := "stage[" + strconv.Itoa(index) + "]"
		if strings.TrimSpace(stage.ID) == "" || stage.ID != strings.TrimSpace(stage.ID) || seenIDs[stage.ID] {
			return fmt.Errorf("%w: %s ID is empty, untrimmed, or duplicated (L-16.1)", ErrUnsupportedPostgresPlan, path)
		}
		seenIDs[stage.ID] = true
		if stage.InputStageID != priorStageID {
			return fmt.Errorf("%w: %s input stage %q does not reference prior stage %q — the CTE chain reads exactly the prior relation (L-16.1)",
				ErrUnsupportedPostgresPlan, path, stage.InputStageID, priorStageID)
		}
		if err := validateStageColumns(stage.InputColumns, priorRowIdentity); err != nil {
			return fmt.Errorf("%w: %s input schema: %v", ErrUnsupportedPostgresPlan, path, err)
		}
		if !sameStageColumns(stage.InputColumns, priorColumns) && !groupInputColumnsExtendSource(stage, priorColumns) {
			return fmt.Errorf("%w: %s input schema differs from preceding stage output (ir/physical_stage_validation.go:66 re-read at the door)",
				ErrUnsupportedPostgresPlan, path)
		}
		if err := validateStageColumns(stage.OutputColumns, stage.RowIdentityColumn); err != nil {
			return fmt.Errorf("%w: %s output schema: %v", ErrUnsupportedPostgresPlan, path, err)
		}
		if err := validateStageKindPayload(stage, mappingTerminal); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrUnsupportedPostgresPlan, path, err)
		}
		priorStageID = stage.ID
		priorColumns = stage.OutputColumns
		priorRowIdentity = stage.RowIdentityColumn
	}
	last := sequence.Stages[len(sequence.Stages)-1]
	if sequence.FinalStageID != last.ID {
		return fmt.Errorf("%w: final stage ID %q does not reference the last stage %q (L-16.1)",
			ErrUnsupportedPostgresPlan, sequence.FinalStageID, last.ID)
	}
	if sequence.FinalRowIdentity != last.RowIdentityColumn {
		return fmt.Errorf("%w: final row identity %q drifts from the last stage identity %q (L-16.1)",
			ErrUnsupportedPostgresPlan, sequence.FinalRowIdentity, last.RowIdentityColumn)
	}
	if !sameStageColumns(sequence.FinalColumns, last.OutputColumns) {
		return fmt.Errorf("%w: final columns differ from the last stage output schema (ir/physical_stage_validation.go:286 re-read at the door)",
			ErrUnsupportedPostgresPlan)
	}
	return nil
}

func validateStageColumns(columns []ir.PhysicalStageColumn, identityColumn string) error {
	if len(columns) == 0 {
		return fmt.Errorf("a stage schema must declare at least one column")
	}
	seen := map[string]bool{}
	for _, column := range columns {
		if strings.TrimSpace(column.Name) == "" {
			return fmt.Errorf("column ID %q has an empty name", column.ID)
		}
		if seen[column.Name] {
			return fmt.Errorf("column name %q is declared twice", column.Name)
		}
		seen[column.Name] = true
	}
	if identityColumn != "" {
		for _, column := range columns {
			if column.Name != identityColumn {
				continue
			}
			if !column.Internal || !column.Identity {
				return fmt.Errorf("row identity column %q must be internal and identity-marked (ir/physical_stage_validation.go:2634 re-read at the door)", identityColumn)
			}
			return nil
		}
		return fmt.Errorf("row identity column %q is not declared in the stage schema", identityColumn)
	}
	return nil
}

func sameStageColumns(left, right []ir.PhysicalStageColumn) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		a, b := left[i], right[i]
		if a.ID != b.ID || a.Name != b.Name || a.Label != b.Label || a.Kind != b.Kind ||
			a.Cardinality != b.Cardinality || a.Nullable != b.Nullable || a.Internal != b.Internal ||
			a.Identity != b.Identity || a.RootContributorResourceType != b.RootContributorResourceType ||
			!reflect.DeepEqual(a.RelatedRecordAnchor, b.RelatedRecordAnchor) {
			return false
		}
	}
	return true
}

func groupInputColumnsExtendSource(stage ir.PhysicalConstructionStage, prior []ir.PhysicalStageColumn) bool {
	if stage.Kind != ir.PhysicalStageGroupOp || stage.Group == nil || stage.Group.RelatedOccurrence == nil ||
		len(stage.InputColumns) <= len(prior) ||
		!sameStageColumns(stage.InputColumns[:len(prior)], prior) {
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

func validateStageKindPayload(stage ir.PhysicalConstructionStage, mappingTerminal bool) error {
	switch stage.Kind {
	case ir.PhysicalStageDeriveOp:
		if stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil ||
			stage.Unpivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil || stage.RelatedExpand != nil {
			return fmt.Errorf("DERIVE has mismatched operation payloads (ir/physical_stage_validation.go:82 re-read at the door)")
		}
		if len(stage.DerivedLets) == 0 {
			return fmt.Errorf("DERIVE requires at least one typed expression LET (ir/physical_stage_validation.go:86 re-read at the door)")
		}
		for index, operation := range stage.DerivedLets {
			if operation.Kind != ir.PhysicalExpressionLetOp || operation.ExpressionLet == nil {
				return fmt.Errorf("DERIVE derived operation %d has kind %q (only typed expression LETs are registered)",
					index, string(operation.Kind))
			}
		}
	case ir.PhysicalStageFilterOp:
		if stage.Filter == nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil ||
			stage.Unpivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil || stage.RelatedExpand != nil ||
			len(stage.DerivedLets) != 0 {
			return fmt.Errorf("FILTER requires only a filter payload (ir/physical_stage_validation.go:92 re-read at the door)")
		}
	case ir.PhysicalStageExpandOp:
		if stage.Expand == nil || stage.Filter != nil || stage.Group != nil || stage.GroupedPivot != nil ||
			stage.Unpivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil || stage.RelatedExpand != nil ||
			len(stage.DerivedLets) != 0 {
			return fmt.Errorf("EXPAND requires only an expand payload (ir/physical_stage_validation.go:82 shape re-read at the door)")
		}
	case ir.PhysicalStageUnpivotOp:
		if stage.Unpivot == nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil ||
			stage.GroupedPivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil ||
			stage.RelatedExpand != nil || len(stage.DerivedLets) != 0 {
			return fmt.Errorf("UNPIVOT requires only an unpivot payload (ir/physical_stage_validation.go:141 re-read at the door)")
		}
		if stage.OutputRowVariable != stage.Unpivot.OutputRowVariable {
			return fmt.Errorf("output row variable does not match unpivot (ir/physical_stage_validation.go:145 re-read at the door)")
		}
	case ir.PhysicalStageGroupOp:
		if stage.Group == nil || stage.Filter != nil || stage.Expand != nil || stage.GroupedPivot != nil ||
			stage.Unpivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil ||
			stage.RelatedExpand != nil || len(stage.DerivedLets) != 0 {
			return fmt.Errorf("GROUP requires only a group payload (ir/physical_stage_validation.go:155 re-read at the door)")
		}
		if stage.Group.SemanticKey != nil {
			return fmt.Errorf("the semantic family Group key is a registered named gap (L-13.3; enabling contract: the semantic membership walk and matcher) and is refused, never partially rendered")
		}
		if stage.Group.CodingKey != nil {
			return fmt.Errorf("CodingKey group keys are refused at the composition payload gate until their registered arm lands (group.go:444 twin, wave law)")
		}
		if stage.Group.RootContributorInputColumn != "" && !mappingTerminal {
			return fmt.Errorf("the root-contributor set is production-unreachable through the PostgreSQL compile target (L-13.6) and is refused, never partially rendered")
		}
	case ir.PhysicalStagePivotOp:
		if stage.GroupedPivot == nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil ||
			stage.Unpivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil ||
			stage.RelatedExpand != nil || len(stage.DerivedLets) != 0 {
			return fmt.Errorf("PIVOT requires only a grouped-pivot payload (ir/physical_stage_validation.go:99 re-read at the door)")
		}
		if stage.OutputRowVariable != stage.GroupedPivot.OutputRowVariable {
			return fmt.Errorf("output row variable does not match grouped pivot (ir/physical_stage_validation.go:102 re-read at the door)")
		}
	case ir.PhysicalStageRelatedSourceOp, ir.PhysicalStageRelatedExpandOp,
		ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedFieldOp:
		return fmt.Errorf("the %s stage is a related-route capability and is refused in composition, never partially rendered (the correlated-hop child-set contracts; sets.go:82-95 hop finding cited)",
			string(stage.Kind))
	default:
		return fmt.Errorf("stage kind %q has no registered composition rendering", string(stage.Kind))
	}
	return nil
}

const (
	stageSourceRelation = stageSequenceCTEPrefix + "src"
	stageRelationPrefix = stageSequenceCTEPrefix + "stage_"
	stageTerminalAlias  = "s"
)

type stageColumnFact struct {
	alias string
	jsonb bool
}

type stageColumnExpr struct {
	expr  string
	jsonb bool
}

type stageRowScope struct {
	qualification string
	columns       map[string]stageColumnFact
}

func (r *planRenderer) resolveStageRowValue(scope stageRowScope, value ir.PhysicalValue) (string, error) {
	if value.BindKey != "" {
		return "", fmt.Errorf("%w: a stage row read must name an intermediate column, not a bind value",
			ErrUnsupportedPostgresPlan)
	}
	if len(value.Path) == 0 {
		return "", fmt.Errorf("%w: a stage row read must name at least one intermediate column; the whole intermediate row is a composite, not a registered scalar",
			ErrUnsupportedPostgresPlan)
	}
	fact, ok := scope.columns[value.Path[0]]
	if !ok {
		return "", fmt.Errorf("%w: stage column %q is not among the registered intermediate columns of the prior stage (L-16.1)",
			ErrUnsupportedPostgresPlan, value.Path[0])
	}
	base := scope.qualification + `"` + fact.alias + `"`
	if len(value.Path) == 1 {
		if !fact.jsonb {
			return base, nil
		}
		return r.stageJSONBRead(base), nil
	}
	if !fact.jsonb {
		return "", fmt.Errorf("%w: stage column %q is a SQL scalar and carries no document path %v",
			ErrUnsupportedPostgresPlan, value.Path[0], value.Path[1:])
	}
	return r.documentPathFromExpression(base, value.Path[1:])
}

func (r *planRenderer) stageJSONBRead(base string) string {
	return base + " #> $" + strconv.Itoa(r.textParameter("{}", "path:{}")) + "::text[]"
}

func guardCastJSONB(tag string) string {
	return "CAST(" + guardCast(tag) + "::text AS jsonb)"
}

type stageSequenceChain struct {
	ctes        []string
	relation    string
	facts       map[string]stageColumnFact
	sourceFacts map[string]stageColumnFact
	last        *ir.PhysicalConstructionStage
}

func (r *planRenderer) renderStageSequenceCTEChain(plan ir.PhysicalPlan) (stageSequenceChain, error) {
	sequence := plan.StageSequence
	source, sourceFacts, err := r.renderStageSequenceSource(plan)
	if err != nil {
		return stageSequenceChain{}, err
	}
	ctes := []string{stageSourceRelation + " AS (" + source + ")"}
	relation := stageSourceRelation
	facts := sourceFacts
	var last *ir.PhysicalConstructionStage
	for index := range sequence.Stages {
		stage := sequence.Stages[index]
		body, stageCTEs, stageFacts, err := r.renderStageCTE(plan, stage, index, relation, facts)
		if err != nil {
			return stageSequenceChain{}, err
		}
		ctes = append(ctes, stageCTEs...)
		relation = stageRelationPrefix + strconv.Itoa(index)
		ctes = append(ctes, relation+" AS ("+body+")")
		facts = stageFacts
		last = &sequence.Stages[index]
	}
	if last == nil {
		return stageSequenceChain{}, fmt.Errorf("%w: the composition builder requires at least one stage", ErrUnsupportedPostgresPlan)
	}
	return stageSequenceChain{ctes: ctes, relation: relation, facts: facts, sourceFacts: sourceFacts, last: last}, nil
}

func (r *planRenderer) renderStageSequenceStatement(plan ir.PhysicalPlan) (string, error) {
	sequence := plan.StageSequence
	chain, err := r.renderStageSequenceCTEChain(plan)
	if err != nil {
		return "", err
	}
	ctes, relation, facts, last := chain.ctes, chain.relation, chain.facts, chain.last

	selects := make([]string, 0, len(sequence.FinalColumns))
	names := make([]string, 0, len(sequence.FinalColumns))
	for index, column := range sequence.FinalColumns {
		fact, ok := facts[column.Name]
		if !ok {
			return "", fmt.Errorf("%w: final column %q is not a column of the last stage (ir/physical_stage_validation.go:286 re-read at the builder)",
				ErrUnsupportedPostgresPlan, column.Name)
		}
		selects = append(selects, stageTerminalAlias+`.`+`"`+fact.alias+`"`+` AS "c`+strconv.Itoa(index)+`"`)
		names = append(names, column.Name)
	}
	final := "SELECT " + strings.Join(selects, ", ") + " FROM " + relation + " AS " + stageTerminalAlias
	// D-16a attribution (coordinator ruling 2026-11-11): CTE terminal re-sort over GROUP-last per coordinator ruling 2026-11-11; SQL CTE order unspecified; refines registered group total order.
	final += " ORDER BY " + strings.Join(stageTerminalOrder(*last, facts), ", ")
	if sequence.PreviewLimitBindKey != "" {
		limit, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int)
		if !ok || limit <= 0 {
			return "", fmt.Errorf("%w: composition preview limit bind %q must be a positive int (render/aql/stage_sequence.go:452-453 twin)",
				ErrUnsupportedPostgresPlan, sequence.PreviewLimitBindKey)
		}
		index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limit)}, "")
		final += " LIMIT $" + strconv.Itoa(index)
	}
	r.outputColumns = names
	return "WITH " + strings.Join(ctes, ", ") + " " + final, nil
}

func stageTerminalOrder(stage ir.PhysicalConstructionStage, facts map[string]stageColumnFact) []string {
	read := func(name string) string {
		return stageTerminalAlias + "." + `"` + facts[name].alias + `"`
	}
	if stage.Kind == ir.PhysicalStageGroupOp && stage.Group != nil {
		terms := make([]string, 0, len(stage.Group.Keys)+1)
		for _, key := range stage.Group.Keys {
			collate := ""
			if key.Kind == "STRING" {
				collate = ` COLLATE "C"`
			}
			terms = append(terms, read(key.OutputColumn)+collate+" ASC NULLS FIRST")
		}
		terms = append(terms, read(stage.RowIdentityColumn)+` COLLATE "C" ASC`)
		return terms
	}
	return []string{read(stage.RowIdentityColumn) + ` COLLATE "C" ASC`}
}

func (r *planRenderer) renderStageSequenceSource(plan ir.PhysicalPlan) (string, map[string]stageColumnFact, error) {
	if err := r.renderRootScan(plan, *plan.Operations[0].RootScan); err != nil {
		return "", nil, err
	}
	var returnOp *ir.PhysicalReturn
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			canonical, err := r.acceptCanonicalScopeFilter(operation.Filter)
			if err != nil {
				return "", nil, err
			}
			if canonical {
				continue
			}
			clause, err := r.renderFilter(plan, *operation.Filter)
			if err != nil {
				return "", nil, err
			}
			r.where = append(r.where, clause)
		case ir.PhysicalDerivedLetOp:
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", nil, err
			}
			if !canonical {
				return "", nil, fmt.Errorf("%w: a non-canonical DERIVED_LET has no dispatch arm in the composition source region (L-16.6)",
					ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalUnnestOp:
			if err := r.renderUnnest(plan, *operation.Unnest); err != nil {
				return "", nil, err
			}
		case ir.PhysicalSetOp:
			if err := r.renderSet(plan, *operation.Set); err != nil {
				return "", nil, err
			}
		case ir.PhysicalSortOp:
			if err := r.renderWindowSort(plan, *operation.Sort); err != nil {
				return "", nil, err
			}
		case ir.PhysicalLimitOp:
			if err := r.renderWindowLimit(plan, *operation.Limit); err != nil {
				return "", nil, err
			}
		case ir.PhysicalReturnOp:
			returnOp = operation.Return
		}
	}
	addressed := map[string]bool{}
	for _, column := range plan.StageSequence.SourceColumns {
		addressed[column.Name] = true
	}
	companionExprs := map[string]string{}
	declaredCardinality := map[string]string{}
	for _, column := range plan.StageSequence.SourceColumns {
		declaredCardinality[column.Name] = strings.ToUpper(strings.TrimSpace(column.Cardinality))
	}
	arrayExprs := map[string]string{}
	keyExprs := map[string]string{}
	setExprs := map[string]string{}
	memberWitnessExpr := ""
	memberWitnessSeen := false
	filtered := ir.PhysicalReturn{}
	for _, projection := range returnOp.Projections {
		if projection.Name == ir.PhysicalPopulationMappingMembersColumn && r.mapping != nil && r.mapping.witness {
			if memberWitnessSeen {
				return "", nil, fmt.Errorf("%w: the source RETURN projects the member witness column twice",
					ErrUnsupportedPostgresPlan)
			}
			if !projection.Hidden || projection.Expression != nil ||
				projection.Value.Variable != ir.PopulationMappingMembersVariable || len(projection.Value.Path) != 0 {
				return "", nil, fmt.Errorf("%w: source projection %q is not the collected selected member IDs",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			if r.mapping.membersColumnRef == "" {
				return "", nil, fmt.Errorf("%w: the member witness projection reads a member variable the member-collecting population root did not bind",
					ErrUnsupportedPostgresPlan)
			}
			memberWitnessExpr = r.mapping.membersColumnRef
			memberWitnessSeen = true
			continue
		}
		if !addressed[projection.Name] {
			continue
		}
		if projection.Hidden && projection.PresenceOutput && projection.Presence != nil &&
			len(projection.Presence.Paths) == 1 && len(projection.Presence.Paths[0]) > 0 {
			scope, ok := r.rows[projection.Presence.Source.Variable]
			if !ok || scope.alias != r.rootAlias {
				return "", nil, fmt.Errorf("%w: presence companion %q reads a variable the composition source region has not registered (L-16.6)",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			presence, err := r.propertyPresenceExpression(r.rootAlias, projection.Presence.Paths[0])
			if err != nil {
				return "", nil, err
			}
			companionExprs[projection.Name] = "(" + presence + ")"
			continue
		}
		if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalSliceExpression &&
			projection.Expression.Slice != nil {
			if _, registered := r.setBodies[projection.Expression.Slice.Source.Variable]; registered {
				expression, err := r.renderSliceValue(plan, r.rows, *projection.Expression, 0)
				if err != nil {
					return "", nil, fmt.Errorf("slice column %q: %w", projection.Name, err)
				}
				setExprs[projection.Name] = expression
				continue
			}
		}
		if projection.Hidden && hiddenRootKeyRead(projection) {
			scope, ok := r.rows[projection.Value.Variable]
			if !ok || scope.alias != r.rootAlias {
				return "", nil, fmt.Errorf("%w: key identity projection %q reads a variable the composition source region has not registered (L-16.6)",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			keyExprs[projection.Name] = r.rootAlias + "." + r.storage.Resource.MustColumn("logical_id").SQLText()
			continue
		}
		if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalExtractExpression &&
			projection.Expression.Cardinality != ir.PhysicalArrayCardinality {
			if extract := projection.Expression.Extract; extract != nil &&
				len(extract.Selector.Steps) == 1 && !extract.Selector.Steps[0].Iterate &&
				extract.Selector.Steps[0].Index == nil && extract.Selector.Filter == nil &&
				len(extract.Fallbacks) == 0 && !extract.Distinct &&
				extract.Prepared == nil && extract.UnitNormalization == nil &&
				len(extract.Source.Path) == 1 && extract.Source.Path[0] == "payload" {
				posture := projection.Expression.NullBehavior
				if column := canonicalKeyColumn(extract.Selector.Steps[0].Field); resourceFilterColumns[column] &&
					(posture == ir.PhysicalPreserveNull || posture == ir.PhysicalOmitNulls ||
						posture == ir.PhysicalEmptyOnNull) {
					scope, ok := r.rows[extract.Source.Variable]
					if !ok || scope.alias != r.rootAlias {
						return "", nil, fmt.Errorf("%w: key selector %q reads a variable the composition source region has not registered (L-16.6)",
							ErrUnsupportedPostgresPlan, projection.Name)
					}
					if extract.ResourceType != scope.resourceType {
						return "", nil, fmt.Errorf("%w: key selector %q validates against resource type %q, but the row is scanned as %q",
							ErrUnsupportedPostgresPlan, projection.Name, extract.ResourceType, scope.resourceType)
					}
					keyExprs[projection.Name] = r.rootAlias + "." +
						r.storage.Resource.MustColumn(column).SQLText()
					continue
				}
			}
		}
		if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalExtractExpression &&
			projection.Expression.Cardinality == ir.PhysicalArrayCardinality {
			if declaredCardinality[projection.Name] != "MANY" {
				return "", nil, fmt.Errorf("%w: source projection %q is an array EXTRACT but its declared column cardinality is %q (the many storage class is the only registered array reading)",
					ErrUnsupportedPostgresPlan, projection.Name, declaredCardinality[projection.Name])
			}
			expression, err := r.stageArraySelectorExpression(projection)
			if err != nil {
				return "", nil, err
			}
			arrayExprs[projection.Name] = expression
			continue
		}
		filtered.Projections = append(filtered.Projections, projection)
	}
	columns, err := r.materializeGroupSource(plan, &filtered)
	if err != nil {
		return "", nil, err
	}
	byName := map[string]groupColumn{}
	for i := range columns {
		byName[columns[i].name] = columns[i]
	}
	selects := make([]string, 0, len(plan.StageSequence.SourceColumns))
	facts := map[string]stageColumnFact{}
	for index, column := range plan.StageSequence.SourceColumns {
		alias := "c" + strconv.Itoa(index)
		if presence, ok := companionExprs[column.Name]; ok {
			if !strings.EqualFold(strings.TrimSpace(column.Kind), "boolean") || column.Identity {
				return "", nil, fmt.Errorf("%w: source column %q is rendered as a presence companion but is declared kind %q identity=%v",
					ErrUnsupportedPostgresPlan, column.Name, column.Kind, column.Identity)
			}
			selects = append(selects, presence+` AS "`+alias+`"`)
			facts[column.Name] = stageColumnFact{alias: alias, jsonb: false}
			continue
		}
		if composite, ok := setExprs[column.Name]; ok {
			if err := r.validateStageColumnClass(column, true); err != nil {
				return "", nil, fmt.Errorf("%w: source column %q: %v", ErrUnsupportedPostgresPlan, column.Name, err)
			}
			selects = append(selects, composite+` AS "`+alias+`"`)
			facts[column.Name] = stageColumnFact{alias: alias, jsonb: true}
			continue
		}
		if key, ok := keyExprs[column.Name]; ok {
			if err := r.validateStageColumnClass(column, false); err != nil {
				return "", nil, fmt.Errorf("%w: source column %q: %v", ErrUnsupportedPostgresPlan, column.Name, err)
			}
			selects = append(selects, key+` AS "`+alias+`"`)
			facts[column.Name] = stageColumnFact{alias: alias, jsonb: false}
			continue
		}
		if array, ok := arrayExprs[column.Name]; ok {
			if !strings.EqualFold(strings.TrimSpace(column.Cardinality), "many") {
				return "", nil, fmt.Errorf("%w: source column %q rendered an array reading under cardinality %q",
					ErrUnsupportedPostgresPlan, column.Name, column.Cardinality)
			}
			selects = append(selects, array+` AS "`+alias+`"`)
			facts[column.Name] = stageColumnFact{alias: alias, jsonb: true}
			continue
		}
		source, ok := byName[column.Name]
		if !ok {
			return "", nil, fmt.Errorf("%w: source projection column %q is not among the typed source RETURN projections (ir/physical_stage_validation.go:46 re-read at the builder)",
				ErrUnsupportedPostgresPlan, column.Name)
		}
		selects = append(selects, source.expr+` AS "`+alias+`"`)
		class := isPayloadExpression(source.expr)
		if err := r.validateStageColumnClass(column, class); err != nil {
			return "", nil, fmt.Errorf("%w: source column %q: %v", ErrUnsupportedPostgresPlan, column.Name, err)
		}
		facts[column.Name] = stageColumnFact{alias: alias, jsonb: class}
	}
	if r.mapping != nil && r.mapping.witness {
		if !memberWitnessSeen {
			return "", nil, fmt.Errorf("%w: the composition mapping source RETURN omits the compiler-owned member witness projection %q (compiler/population_mapping.go adds it)",
				ErrUnsupportedPostgresPlan, ir.PhysicalPopulationMappingMembersColumn)
		}
		memberAlias := "c" + strconv.Itoa(len(plan.StageSequence.SourceColumns))
		selects = append(selects, memberWitnessExpr+` AS "`+memberAlias+`"`)
		facts[ir.PhysicalPopulationMappingMembersColumn] = stageColumnFact{alias: memberAlias, jsonb: true}
	}
	resource := r.storage.Resource
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + resource.Identifier().SQLText() + " AS " + r.rootAlias
	for _, join := range r.joins {
		sql += " " + join
	}
	for _, lateral := range r.laterals {
		sql += " " + lateral
	}
	if len(r.where) > 0 {
		sql += " WHERE " + strings.Join(r.where, " AND ")
	}
	sql += " ORDER BY " + strings.Join(r.windowOrder(), ", ") + r.window.limitClause
	return sql, facts, nil
}

func (r *planRenderer) stageArraySelectorExpression(projection ir.PhysicalProjection) (string, error) {
	extract := projection.Expression.Extract
	if extract == nil {
		return "", fmt.Errorf("%w: array source projection %q: EXTRACT expression is missing its payload",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if projection.Expression.NullBehavior != ir.PhysicalEmptyOnNull {
		return "", fmt.Errorf("%w: array source projection %q: null behavior %q has no registered array reading (the empty-array posture is EMPTY_ON_NULL)",
			ErrUnsupportedPostgresPlan, projection.Name, string(projection.Expression.NullBehavior))
	}
	if extract.Selector.Filter != nil || len(extract.Fallbacks) != 0 || extract.Distinct ||
		extract.Prepared != nil || extract.UnitNormalization != nil {
		return "", fmt.Errorf("%w: array source projection %q uses a richer selector form (filter, fallbacks, DISTINCT, prepared, or unit normalization) that is not registered",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return "", fmt.Errorf("%w: array source projection %q must read the row document at [\"payload\"], got %v",
			ErrUnsupportedPostgresPlan, projection.Name, extract.Source.Path)
	}
	scope, ok := r.rows[extract.Source.Variable]
	if !ok || scope.alias != r.rootAlias {
		return "", fmt.Errorf("%w: array source projection %q reads a variable the composition source region has not registered (L-16.6)",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	iterate := -1
	for index, step := range extract.Selector.Steps {
		if step.Index != nil {
			return "", fmt.Errorf("%w: array source projection %q: indexed steps are the unit slice's registered reading, not the composition array gathering",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return "", fmt.Errorf("%w: array source projection %q: selector step %q is not renderer-safe",
				ErrUnsupportedPostgresPlan, projection.Name, step.Field)
		}
		if step.Iterate {
			if iterate >= 0 {
				return "", fmt.Errorf("%w: array source projection %q: repeated steps beyond one iterate step are the later array-rendering capability",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			iterate = index
		}
	}
	if iterate < 0 {
		return "", fmt.Errorf("%w: array source projection %q has no iterated selector step; a many column needs exactly one [] step",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	prefixSteps := make([]string, 0, iterate+1)
	for _, step := range extract.Selector.Steps[:iterate+1] {
		prefixSteps = append(prefixSteps, step.Field)
	}
	tailSteps := make([]string, 0, len(extract.Selector.Steps)-iterate-1)
	for _, step := range extract.Selector.Steps[iterate+1:] {
		tailSteps = append(tailSteps, step.Field)
	}
	arr := r.documentColumn(r.rootAlias)
	if len(prefixSteps) > 0 {
		var err error
		arr, err = r.documentPathFromExpression(arr, prefixSteps)
		if err != nil {
			return "", fmt.Errorf("array source projection %q: %w", projection.Name, err)
		}
	}
	var leafTail string
	if len(tailSteps) == 0 {
		leafTail = "{}"
	} else {
		leafTail = "{" + strings.Join(mapSlice(tailSteps, arrayLiteralElement), ",") + "}"
	}
	leafTailParameter := r.textParameter(leafTail, "path:"+leafTail)
	leaf := "e #> $" + strconv.Itoa(leafTailParameter) + "::text[]"
	return "(CASE WHEN jsonb_typeof(" + arr + ") = 'array' THEN COALESCE((SELECT jsonb_agg(" + leaf +
		") FROM jsonb_array_elements(" + arr + ") AS e WHERE " + leaf + " IS NOT NULL), '[]'::jsonb) ELSE '[]'::jsonb END)", nil
}

func mapSlice(input []string, mapFn func(string) string) []string {
	out := make([]string, 0, len(input))
	for _, value := range input {
		out = append(out, mapFn(value))
	}
	return out
}

func (r *planRenderer) validateStageColumnClass(column ir.PhysicalStageColumn, jsonb bool) error {
	cardinality := strings.ToUpper(strings.TrimSpace(column.Cardinality))
	if cardinality == "MANY" && !jsonb {
		return fmt.Errorf("cardinality %q needs the jsonb storage class but the emitter rendered a SQL scalar", column.Cardinality)
	}
	if column.RootContributorResourceType != "" {
		if r.mapping != nil && r.mapping.witness &&
			column.Internal && column.Name == populationMappingContributorColumn &&
			strings.EqualFold(strings.TrimSpace(column.Kind), "string") && cardinality == "MANY" {
			return nil
		}
		if !(column.Internal && column.Name == "_key" &&
			strings.EqualFold(strings.TrimSpace(column.Kind), "string") &&
			(strings.EqualFold(strings.TrimSpace(column.Cardinality), "required_one") ||
				strings.EqualFold(strings.TrimSpace(column.Cardinality), "optional_one"))) {
			return fmt.Errorf("root-contributor columns are bound only by RenderPopulationRootScan (population.go) and are outside the registered composition surface")
		}
	}
	return nil
}

func (r *planRenderer) renderStageCTE(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	index int,
	relation string,
	facts map[string]stageColumnFact,
) (body string, ctes []string, out map[string]stageColumnFact, err error) {
	r.stageRows = map[string]stageRowScope{}
	r.unpivotOutputs = nil
	scope := stageRowScope{qualification: stageTerminalAlias + ".", columns: facts}
	switch stage.Kind {
	case ir.PhysicalStageFilterOp, ir.PhysicalStageDeriveOp:
		body, out, err = r.renderPassThroughStage(plan, stage, relation, scope)
	case ir.PhysicalStageExpandOp:
		body, out, err = r.renderExpandStage(plan, stage, index, relation, scope)
	case ir.PhysicalStageUnpivotOp:
		body, out, err = r.renderStageMelt(plan, stage, relation, scope)
	case ir.PhysicalStageGroupOp:
		body, ctes, out, err = r.renderStageGroup(plan, stage, index, relation, facts)
	case ir.PhysicalStagePivotOp:
		body, ctes, out, err = r.renderStagePivot(plan, stage, index, relation, facts)
	default:
		return "", nil, nil, fmt.Errorf("%w: stage kind %q has no registered composition rendering",
			ErrUnsupportedPostgresPlan, string(stage.Kind))
	}
	if err != nil {
		return "", nil, nil, err
	}
	if _, ok := out[stage.RowIdentityColumn]; !ok {
		return "", nil, nil, fmt.Errorf("%w: stage %q does not emit its registered row identity column %q (stage identity contract)",
			ErrUnsupportedPostgresPlan, stage.ID, stage.RowIdentityColumn)
	}
	return body, ctes, out, nil
}

func (r *planRenderer) renderPassThroughStage(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage, relation string, scope stageRowScope) (string, map[string]stageColumnFact, error) {
	r.stageRows[stage.InputRowVariable] = scope
	r.stageRows[stage.OutputRowVariable] = scope
	direct := map[string]stageColumnExpr{}
	for _, operation := range stage.DerivedLets {
		derived := operation.ExpressionLet
		if derived.Expression.Kind != ir.PhysicalValueExpression || derived.Expression.Value == nil {
			return "", nil, fmt.Errorf("%w: a composition DERIVE LET of expression kind %q has no registered rendering; CALL, selector, and unit-normalization expressions belong to their own slice registrations (calls.go, selectors.go, unit_normalization.go) and are refused here (L-16.6)",
				ErrUnsupportedPostgresPlan, string(derived.Expression.Kind))
		}
		expression, err := r.resolveValue(*derived.Expression.Value)
		if err != nil {
			return "", nil, fmt.Errorf("derive LET %q: %w", derived.Variable, err)
		}
		jsonb := isPayloadExpression(expression)
		r.rows[derived.Variable] = rowScope{derivedValue: expression, derivedJSON: jsonb}
		direct[derived.Variable] = stageColumnExpr{expr: expression, jsonb: jsonb}
	}
	var where []string
	if stage.Filter != nil {
		if stage.Filter.Expression != nil && stage.Filter.Expression.Kind == ir.PhysicalExistsPredicate {
			return "", nil, fmt.Errorf("%w: an EXISTS filter inside a composition stage is a related-route contract (sets.go renderRelatedExists) and is refused over intermediate rows (L-16.6)",
				ErrUnsupportedPostgresPlan)
		}
		clause, err := r.renderFilter(plan, *stage.Filter)
		if err != nil {
			return "", nil, err
		}
		where = append(where, clause)
	}
	selects, facts, err := r.renderStageOutputSelects(plan, stage, direct, scope)
	if err != nil {
		return "", nil, err
	}
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + stageRelationSource(relation)
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	return sql, facts, nil
}

func stageRelationSource(relation string) string {
	return relation + " AS " + stageTerminalAlias
}

func (r *planRenderer) renderExpandStage(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage, index int, relation string, scope stageRowScope) (string, map[string]stageColumnFact, error) {
	expand := stage.Expand
	if _, ok := plan.BindVars[expand.ConstructionIDBindKey]; !ok {
		return "", nil, fmt.Errorf("%w: expand construction ID bind %q is missing", ErrUnsupportedPostgresPlan, expand.ConstructionIDBindKey)
	}
	input, ok := scope.columns[expand.InputColumn]
	if !ok {
		return "", nil, fmt.Errorf("%w: expand input column %q is not among the prior stage's registered columns (L-16.1)",
			ErrUnsupportedPostgresPlan, expand.InputColumn)
	}
	if !input.jsonb {
		return "", nil, fmt.Errorf("%w: expand input column %q is a SQL scalar and cannot hold an array", ErrUnsupportedPostgresPlan, expand.InputColumn)
	}
	identityColumn := ""
	for _, column := range stage.InputColumns {
		if column.Identity {
			identityColumn = column.Name
			break
		}
	}
	if identityColumn == "" {
		return "", nil, fmt.Errorf("%w: expand input has no row identity column (render/aql/construction_expand.go:26 twin)", ErrUnsupportedPostgresPlan)
	}
	identity, ok := scope.columns[identityColumn]
	if !ok {
		return "", nil, fmt.Errorf("%w: expand input identity column %q is missing from the prior relation (L-16.1)",
			ErrUnsupportedPostgresPlan, identityColumn)
	}
	typeofTag := r.textParameter("array", "text:typeof:array")
	itemTypeOf, err := stageScalarTypeOf(expand.InputKind)
	if err != nil {
		return "", nil, fmt.Errorf("expand item: %w", err)
	}
	itemTypeBind := r.textParameter(itemTypeOf, "text:typeof:"+itemTypeOf)

	raw := stageTerminalAlias + "." + `"` + input.alias + `"`
	items := "COALESCE(NULLIF(" + raw + ", 'null'::jsonb), '[]'::jsonb)"
	guarded := "CASE WHEN NOT (" + raw + " IS NULL OR " + raw + " = 'null'::jsonb OR jsonb_typeof(" + raw + ") = $" +
		strconv.Itoa(typeofTag) + "::text) THEN " + guardCastJSONB(stageExpandArrayTypeTag)
	if expand.EmptyPolicy == ir.PhysicalUnnestError {
		guarded += " WHEN jsonb_array_length(" + items + ") = 0 THEN " + guardCastJSONB(stageExpandEmptyTag)
	}
	guarded += " ELSE " + items + " END"

	expansion := "jsonb_array_elements(" + guarded + ") WITH ORDINALITY"
	join := "CROSS JOIN LATERAL " + expansion + " AS " + expandRelation(index) + "(item, ord)"
	if expand.EmptyPolicy == ir.PhysicalUnnestPreserveParent {
		join = "LEFT JOIN LATERAL " + expansion + " AS " + expandRelation(index) + "(item, ord) ON TRUE"
	}
	item := expandRelation(index) + ".item"
	ordinal := expandRelation(index) + ".ord - 1"

	itemGuard := "(CASE WHEN " + item + " IS NULL OR " + item + " = 'null'::jsonb OR jsonb_typeof(" + item +
		") = $" + strconv.Itoa(itemTypeBind) + "::text THEN 1 ELSE " + guardCast(stageExpandItemTypeTag) + " END) > 0"

	identityRead := stageTerminalAlias + "." + `"` + identity.alias + `"`
	constructionBind := r.textParameter(bindStringVar(plan.BindVars, expand.ConstructionIDBindKey), "")
	identityParts := []string{
		`'["input",' || COALESCE(to_jsonb(` + identityRead + `)::text, 'null') || ']'`,
		`'["construction",' || to_jsonb($` + strconv.Itoa(constructionBind) + `::text) || ']'`,
		`'["ordinal",' || COALESCE((` + expandRelation(index) + `.ord - 1)::text, 'null') || ']'`,
	}
	identityText := "'[' || " + strings.Join(identityParts, " || ',' || ") + " || ']'"

	direct := map[string]stageColumnExpr{
		expand.OutputColumn:     {expr: item, jsonb: true},
		expand.OrdinalColumn:    {expr: "(" + ordinal + ")", jsonb: false},
		stage.RowIdentityColumn: {expr: identityText, jsonb: false},
	}
	r.stageRows[stage.InputRowVariable] = scope
	r.stageRows[stage.OutputRowVariable] = scope
	selects, facts, err := r.renderStageOutputSelects(plan, stage, direct, scope)
	if err != nil {
		return "", nil, err
	}
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + stageRelationSource(relation) + " " + join +
		" WHERE " + itemGuard
	return sql, facts, nil
}

func expandRelation(index int) string { return stageSequenceCTEPrefix + "ex" + strconv.Itoa(index) }

func stageScalarTypeOf(kind string) (string, error) {
	switch kind {
	case "STRING", "CODE":
		return "string", nil
	case "INTEGER", "DECIMAL":
		return "number", nil
	case "BOOLEAN":
		return "boolean", nil
	default:
		return "", fmt.Errorf("%w: expand item kind %q is unsupported", ErrUnsupportedPostgresPlan, kind)
	}
}

func (r *planRenderer) renderStageMelt(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage, relation string, scope stageRowScope) (string, map[string]stageColumnFact, error) {
	unpivot := stage.Unpivot
	inputExprs := make(map[string]string, len(unpivot.InputProjections))
	for _, projection := range unpivot.InputProjections {
		expression, err := r.resolveStageColumnRead(plan, scope, projection)
		if err != nil {
			return "", nil, fmt.Errorf("unpivot input %q: %w", projection.Name, err)
		}
		inputExprs[projection.Name] = expression
	}
	r.unpivotSeq++
	alias := "x" + strconv.Itoa(r.unpivotSeq)
	region, err := r.renderMeltRegion(plan, *unpivot, inputExprs, alias)
	if err != nil {
		return "", nil, err
	}
	r.unpivotOutputs = map[string]map[string]string{unpivot.OutputRowVariable: region.columns}
	direct := map[string]stageColumnExpr{}
	for name, expression := range region.columns {
		direct[name] = stageColumnExpr{expr: expression, jsonb: name == unpivot.KeyOutput || name == unpivot.ValueOutput}
	}
	r.stageRows[stage.InputRowVariable] = scope
	selects, facts, err := r.renderStageOutputSelects(plan, stage, direct, scope)
	if err != nil {
		return "", nil, err
	}
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + stageRelationSource(relation) + " " + region.lateral
	if len(region.conjuncts) > 0 {
		sql += " WHERE " + strings.Join(region.conjuncts, " AND ")
	}
	return sql, facts, nil
}

func (r *planRenderer) resolveStageColumnRead(plan ir.PhysicalPlan, scope stageRowScope, projection ir.PhysicalProjection) (string, error) {
	if projection.Expression != nil {
		if projection.Expression.Kind == ir.PhysicalObjectLookupExpression && projection.Expression.ObjectLookup != nil &&
			projection.Expression.Cardinality == ir.PhysicalScalarCardinality &&
			projection.Expression.NullBehavior == ir.PhysicalPreserveNull {
			lookup := projection.Expression.ObjectLookup
			if lookup.ObjectVariable == "" || lookup.KeyBindKey == "" {
				return "", fmt.Errorf("%w: stage input projection %q: the carry-over object lookup needs its object variable and key bind",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			if _, registered := r.stageRows[lookup.ObjectVariable]; !registered {
				return "", fmt.Errorf("%w: stage input projection %q reads object variable %q, which no stage row registers (L-16.1)",
					ErrUnsupportedPostgresPlan, projection.Name, lookup.ObjectVariable)
			}
			column, ok := plan.BindVars[lookup.KeyBindKey].(string)
			if !ok || strings.TrimSpace(column) == "" {
				return "", fmt.Errorf("%w: stage input projection %q: carry-over key bind %q is not a non-blank string",
					ErrUnsupportedPostgresPlan, projection.Name, lookup.KeyBindKey)
			}
			return r.resolveStageRowValue(scope, ir.PhysicalValue{Variable: lookup.ObjectVariable, Path: []string{column}})
		}
		return "", fmt.Errorf("%w: stage input projection %q uses expression kind %q; staged inputs are named intermediate columns and selector/call forms register with their own slices (L-16.6)",
			ErrUnsupportedPostgresPlan, projection.Name, string(projection.Expression.Kind))
	}
	if projection.Value.Variable == "" || len(projection.Value.Path) == 0 {
		return "", fmt.Errorf("%w: stage input projection %q is not a named intermediate column read", ErrUnsupportedPostgresPlan, projection.Name)
	}
	return r.resolveStageRowValue(scope, projection.Value)
}

func (r *planRenderer) renderStageGroup(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	index int,
	relation string,
	facts map[string]stageColumnFact,
) (string, []string, map[string]stageColumnFact, error) {
	var relRegion *stageRelatedOccurrenceRegion
	source := "SELECT * FROM " + relation
	if stage.Group.RelatedOccurrence != nil {
		relRendered, relErr := r.renderStageGroupRelatedOccurrence(plan, stage, index, relation, facts)
		if relErr != nil {
			return "", nil, nil, relErr
		}
		relRegion = &relRendered
		source = relRegion.sourceText
	}
	columns := make([]groupColumn, 0, len(stage.InputColumns))
	for _, column := range stage.InputColumns {
		if relRegion != nil {
			if adapted, ok := relRegion.columns[column.Name]; ok {
				columns = append(columns, adapted)
				continue
			}
		}
		fact, ok := facts[column.Name]
		if !ok {
			return "", nil, nil, fmt.Errorf("%w: group input column %q is not among the prior stage's registered columns (L-16.1)",
				ErrUnsupportedPostgresPlan, column.Name)
		}
		columns = append(columns, groupColumn{
			name: column.Name, alias: fact.alias, ref: `"` + fact.alias + `"`, jsonb: fact.jsonb,
		})
	}
	jsonbOutputs := map[string]bool{}
	for _, rowValue := range stage.Group.RowValues {
		jsonbOutputs[rowValue.Output] = rowValue.Policy == "ALL"
	}
	if stage.Group.RootContributorOutputColumn != "" {
		jsonbOutputs[stage.Group.RootContributorOutputColumn] = true
	}
	region, err := r.renderGroupAggregateRegion(plan, stage, columns,
		source, "__loom_g"+strconv.Itoa(index)+"_",
		stage.OutputColumns, stage.RowIdentityColumn)
	if err != nil {
		return "", nil, nil, err
	}
	out := factsFromNames(region.names, func(name string) bool { return jsonbOutputs[name] })
	return region.final, region.ctes, out, nil
}

func (r *planRenderer) renderStagePivot(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	index int,
	relation string,
	facts map[string]stageColumnFact,
) (string, []string, map[string]stageColumnFact, error) {
	pivot := stage.GroupedPivot
	scope := stageRowScope{qualification: stageTerminalAlias + ".", columns: facts}
	projectionNames := make([]string, 0, len(stage.OutputColumns))
	for _, column := range stage.OutputColumns {
		projectionNames = append(projectionNames, column.Name)
	}
	region, err := r.renderGroupedPivotRegion(plan, pivot, "__loom_pv"+strconv.Itoa(index)+"_",
		projectionNames, stage.RowIdentityColumn, relation+" AS "+stageTerminalAlias,
		func(projection ir.PhysicalProjection) (string, ir.PhysicalNullBehavior, error) {
			expression, err := r.resolveStageColumnRead(plan, scope, projection)
			if err != nil {
				return "", "", err
			}
			return "to_jsonb(" + expression + ")", ir.PhysicalPreserveNull, nil
		}, nil)
	if err != nil {
		return "", nil, nil, err
	}
	out := factsFromNames(region.names, func(name string) bool { return name != stage.RowIdentityColumn })
	return region.final, region.ctes, out, nil
}

func factsFromNames(names []string, jsonb func(string) bool) map[string]stageColumnFact {
	facts := make(map[string]stageColumnFact, len(names))
	for index, name := range names {
		facts[name] = stageColumnFact{alias: "c" + strconv.Itoa(index), jsonb: jsonb(name)}
	}
	return facts
}

func (r *planRenderer) renderStageOutputSelects(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	direct map[string]stageColumnExpr,
	scope stageRowScope,
) ([]string, map[string]stageColumnFact, error) {
	byName := map[string]ir.PhysicalProjection{}
	for _, projection := range stage.OutputProjections {
		if _, seen := byName[projection.Name]; seen {
			return nil, nil, fmt.Errorf("%w: output projection %q is declared twice (stage schema parity)",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		byName[projection.Name] = projection
	}
	selects := make([]string, 0, len(stage.OutputColumns))
	facts := make(map[string]stageColumnFact, len(stage.OutputColumns))
	for index, column := range stage.OutputColumns {
		var expression string
		isJSONB := false
		if rendered, ok := direct[column.Name]; ok {
			expression, isJSONB = rendered.expr, rendered.jsonb
		} else {
			projection, ok := byName[column.Name]
			if !ok {
				return nil, nil, fmt.Errorf("%w: output column %q has no output projection and no registered reshape expression (stage output/input parity)",
					ErrUnsupportedPostgresPlan, column.Name)
			}
			if projection.Presence != nil && projection.Value.Variable == "" {
				proof, err := r.stagePresenceProof(scope, projection)
				if err != nil {
					return nil, nil, err
				}
				expression, isJSONB = proof, true
			} else {
				read, err := r.resolveStageColumnRead(plan, scope, projection)
				if err != nil {
					return nil, nil, fmt.Errorf("output column %q: %w", column.Name, err)
				}
				expression, isJSONB = read, isPayloadExpression(read)
			}
		}
		if err := r.validateStageColumnClass(column, isJSONB); err != nil {
			return nil, nil, fmt.Errorf("%w: stage output column %q: %v", ErrUnsupportedPostgresPlan, column.Name, err)
		}
		alias := "c" + strconv.Itoa(index)
		selects = append(selects, expression+` AS "`+alias+`"`)
		facts[column.Name] = stageColumnFact{alias: alias, jsonb: isJSONB}
	}
	return selects, facts, nil
}

func (r *planRenderer) stagePresenceProof(scope stageRowScope, projection ir.PhysicalProjection) (string, error) {
	if len(projection.Presence.Paths) != 1 || len(projection.Presence.Paths[0]) == 0 {
		return "", fmt.Errorf("%w: a stage presence companion needs exactly one column path (null_semantics.go contract)",
			ErrUnsupportedPostgresPlan)
	}
	name := projection.Presence.Paths[0][0]
	fact, ok := scope.columns[name]
	if !ok {
		return "", fmt.Errorf("%w: presence companion %q names no registered intermediate column (L-16.1)",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if len(projection.Presence.Paths[0]) > 1 {
		return "", fmt.Errorf("%w: a stage presence companion cannot address a document tail path over an intermediate column",
			ErrUnsupportedPostgresPlan)
	}
	base := scope.qualification + `"` + fact.alias + `"`
	if !fact.jsonb {
		return "(CASE WHEN " + base + " IS NULL THEN 'false'::jsonb ELSE 'true'::jsonb END)", nil
	}
	return "(CASE WHEN COALESCE(" + base + " IS NULL OR " + base + " = 'null'::jsonb, false) THEN 'false'::jsonb ELSE 'true'::jsonb END)", nil
}
