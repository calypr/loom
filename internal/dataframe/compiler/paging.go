package compiler

import (
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

const (
	RootPageAfterKeyBind                  = "loom_root_page_after_key"
	RootPageSizeBind                      = genericPhysicalExecutionLimitBind
	RootPageKeysBind                      = "loom_root_page_keys"
	rootPageDatasetGenerationField        = "dataset_generation"
	rootPageDatasetGenerationBind         = "dataset_generation"
	previewProjectionPartitionSize        = 24
	minimumPreviewPartitionColumns        = 160
	minimumPreviewPartitionQueryBytes     = 60 << 10
	previewProjectionWeightBudget         = 30000
	previewProjectionMaxPartitions        = 16
	previewProjectionMaxQueryBytes        = 96 << 10
	previewProjectionMaxPlanBytes         = 4 << 20
	previewProjectionTotalCostMultiplier  = 16
	previewProjectionMaxQueryRatioPercent = 75
)

type CompiledOutputPage struct {
	RootKeysQuery    string
	RootKeysBindVars map[string]any
	RowsQuery        string
	RowsBindVars     map[string]any
	RowsPartitions   []CompiledOutputPagePartition
	RowsDiagnostics  ir.CompilerPlanDiagnostics
}

type CompiledOutputPagePartition struct {
	ProjectionNames []string
	RowsQuery       string
	RowsBindVars    map[string]any
}

func CompileRecipeOutputPageWithPolicy(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, pageSize int, policy ir.PhysicalOptimizationPolicy) (CompiledOutputPage, error) {
	if workspaceOutputCaptureRequired(output) {
		return CompiledOutputPage{}, fmt.Errorf("output %q references same-workspace outputs; server-owned workspace capture is not available", output.Name)
	}
	if pageSize < 1 {
		return CompiledOutputPage{}, fmt.Errorf("root page size must be positive")
	}
	physical, err := optimizedOutputPlan(output, policy)
	if err != nil {
		return CompiledOutputPage{}, err
	}
	if bindings.PreviewLimit > 0 {
		physical = withConstructionPreviewRootIDFilter(output, physical)
	}
	keysPlan, err := rootKeysPagePlan(physical, pageSize)
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("build root-key page: %w", err)
	}
	rowsPlan, keyWindow, err := selectedRootsPlan(physical)
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("build selected-root page: %w", err)
	}
	if bindings.IncludeSourceIdentity {
		rowsPlan, _, err = withPreviewSourceResourceID(output, rowsPlan)
		if err != nil {
			return CompiledOutputPage{}, fmt.Errorf("add preview source identity to selected-root page: %w", err)
		}
	}
	rowsPlan, err = WithGenericPhysicalExecutionWindow(rowsPlan, 0)
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("apply selected-root execution window: %w", err)
	}
	if bindings.IncludeAuthResourcePath {
		if err := appendAuthResourcePathProjection(&rowsPlan); err != nil {
			return CompiledOutputPage{}, err
		}
	}
	keys, err := aql.RenderPhysicalPlan(keysPlan)
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("render root-key page: %w", err)
	}
	previewLimit := 0
	if bindings.PreviewLimit > 0 {
		previewLimit = bindings.PreviewLimit
	}
	var rows aql.RenderedPhysicalPlan
	if keyWindow {
		rows, err = aql.RenderPhysicalPlanWithRootPageKeys(rowsPlan, RootPageKeysBind, previewLimit)
	} else if bindings.PreviewLimit > 0 {
		rows, err = aql.RenderPhysicalPlanWithRelatedExpandPreviewLimit(rowsPlan, bindings.PreviewLimit)
	} else {
		rows, err = aql.RenderPhysicalPlan(rowsPlan)
	}
	if err != nil {
		return CompiledOutputPage{}, fmt.Errorf("render selected-root page: %w", err)
	}
	var partitions []CompiledOutputPagePartition
	if keyWindow && bindings.PreviewLimit > 0 && previewProjectionPartitionEligible(rowsPlan) &&
		(len(rows.Query) >= minimumPreviewPartitionQueryBytes || len(rowsPlan.Operations[len(rowsPlan.Operations)-1].Return.Projections) >= minimumPreviewPartitionColumns) {
		partitions, err = renderSelectedRootProjectionPartitions(rowsPlan, rows, RootPageKeysBind, previewLimit)
		if err != nil {
			return CompiledOutputPage{}, fmt.Errorf("render selected-root projection partitions: %w", err)
		}
	}
	return CompiledOutputPage{
		RootKeysQuery: keys.Query, RootKeysBindVars: keys.BindVars,
		RowsQuery: rows.Query, RowsBindVars: rows.BindVars, RowsPartitions: partitions,
		RowsDiagnostics: PhysicalPlanDiagnostics(rowsPlan),
	}, nil
}

func previewProjectionPartitionEligible(plan ir.PhysicalPlan) bool {
	if plan.StageSequence != nil || !rootPageKeyLookupSafe(plan) || len(plan.Operations) == 0 {
		return false
	}
	root := plan.Operations[0].RootScan
	if root == nil {
		return false
	}
	var terminal *ir.PhysicalReturn
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		switch operation.Kind {
		case ir.PhysicalRootScanOp, ir.PhysicalTraversalOp, ir.PhysicalFilterOp,
			ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp, ir.PhysicalSetOp,
			ir.PhysicalSortOp, ir.PhysicalLimitOp:
		case ir.PhysicalReturnOp:
			if index != len(plan.Operations)-1 || operation.Return == nil {
				return false
			}
			terminal = operation.Return
		default:

			return false
		}
	}
	if terminal == nil {
		return false
	}
	publicProjectionCount := 0
	for _, projection := range terminal.Projections {
		if !projection.Hidden && projection.Name != "_key" {
			publicProjectionCount++
		}
	}
	if publicProjectionCount < 2 {
		return false
	}
	for _, projection := range terminal.Projections {
		if projection.Name == "_key" && projection.Expression == nil && projection.Value.Variable == root.Variable &&
			len(projection.Value.Path) == 1 && projection.Value.Path[0] == "_key" {
			return true
		}
	}
	return false
}

func renderSelectedRootProjectionPartitions(plan ir.PhysicalPlan, fullRendered aql.RenderedPhysicalPlan, bindKey string, relatedExpandPreviewLimit int) ([]CompiledOutputPagePartition, error) {
	if !previewProjectionPartitionEligible(plan) {
		return nil, fmt.Errorf("plan is not eligible for source-row projection partitions")
	}
	terminalIndex := len(plan.Operations) - 1
	projections := plan.Operations[terminalIndex].Return.Projections
	identity := make([]ir.PhysicalProjection, 0, 1)
	metadata := make([]ir.PhysicalProjection, 0)
	columns := make([]ir.PhysicalProjection, 0, len(projections)-1)
	for _, projection := range projections {
		if projection.Name == "_key" {
			identity = append(identity, projection)
		} else if projection.Hidden {
			metadata = append(metadata, projection)
		} else {
			columns = append(columns, projection)
		}
	}
	if len(identity) != 1 {
		return nil, fmt.Errorf("eligible source-row plan must return exactly one _key identity")
	}
	if len(plan.Operations) > 4096 || len(columns) > previewProjectionMaxPartitions*previewProjectionPartitionSize {
		return nil, nil
	}
	fullCost, valid := previewPartitionCostFor(plan, fullRendered.Query, fullRendered.BindVars)
	if !valid || fullCost.PlanBytes > previewProjectionMaxPlanBytes || fullCost.Nodes > 200000 {
		return nil, nil
	}
	groups, ok := groupPreviewProjections(columns)
	if !ok || len(groups) < 2 || len(groups)+boolInt(len(metadata) != 0) > previewProjectionMaxPartitions {
		return nil, nil
	}
	selections := make([][]ir.PhysicalProjection, 0, len(groups)+boolInt(len(metadata) != 0))
	if len(metadata) != 0 {
		selections = append(selections, metadata)
	}
	selections = append(selections, groups...)
	partitions := make([]CompiledOutputPagePartition, 0, len(selections))
	var totalCost previewPartitionCost
	for _, group := range selections {
		selected := make([]ir.PhysicalProjection, 0, 1+len(group))
		selected = append(selected, identity...)
		selected = append(selected, group...)
		partitionPlan := ir.ClonePhysicalPlan(plan)
		partitionPlan.Operations[terminalIndex].Return.Projections = selected
		partitionPlan, pruned := prunePreviewPartitionPlan(partitionPlan)
		if !pruned {
			return nil, nil
		}
		rendered, err := aql.RenderPhysicalPlanWithRootPageKeys(partitionPlan, bindKey, relatedExpandPreviewLimit)
		if err != nil {
			return nil, err
		}
		cost, valid := previewPartitionCostFor(partitionPlan, rendered.Query, rendered.BindVars)
		if !valid || cost.QueryBytes > previewProjectionMaxQueryBytes || cost.PlanBytes > previewProjectionMaxPlanBytes {
			return nil, nil
		}
		totalCost = totalCost.add(cost)
		if totalCost.Operations > 4096 || totalCost.PlanBytes > 8<<20 || totalCost.QueryBytes > 4<<20 || totalCost.Nodes > 200000 {
			return nil, nil
		}
		if len(partitions) == 0 {
			totalCost.PeakQueryBytes = cost.QueryBytes
		} else if cost.QueryBytes > totalCost.PeakQueryBytes {
			totalCost.PeakQueryBytes = cost.QueryBytes
		}
		names := make([]string, len(selected))
		for index, projection := range selected {
			names[index] = projection.Name
		}
		partitions = append(partitions, CompiledOutputPagePartition{
			ProjectionNames: names, RowsQuery: rendered.Query, RowsBindVars: rendered.BindVars,
		})
	}
	if !partitionCostWithinTotalBudget(totalCost, fullCost, len(partitions)) {
		return nil, nil
	}
	return partitions, nil
}

func groupPreviewProjections(columns []ir.PhysicalProjection) ([][]ir.PhysicalProjection, bool) {
	groups := make([][]ir.PhysicalProjection, 0, len(columns)/previewProjectionPartitionSize+1)
	current := make([]ir.PhysicalProjection, 0, previewProjectionPartitionSize)
	var weight int
	for _, projection := range columns {
		encoded, err := json.Marshal(projection)
		if err != nil {
			return nil, false
		}
		projectionWeight := len(encoded)
		if projectionWeight > previewProjectionWeightBudget {
			if len(current) != 0 {
				groups = append(groups, current)
				current = nil
				weight = 0
			}
			groups = append(groups, []ir.PhysicalProjection{projection})
			continue
		}
		if len(current) >= previewProjectionPartitionSize || weight+projectionWeight > previewProjectionWeightBudget {
			groups = append(groups, current)
			current = make([]ir.PhysicalProjection, 0, previewProjectionPartitionSize)
			weight = 0
		}
		current = append(current, projection)
		weight += projectionWeight
	}
	if len(current) != 0 {
		groups = append(groups, current)
	}
	return groups, len(groups) <= previewProjectionMaxPartitions
}

type previewPartitionCost struct {
	QueryBytes     int
	PlanBytes      int
	Operations     int
	Projections    int
	Work           uint64
	Nodes          uint64
	PeakQueryBytes int
}

func previewPartitionCostFor(plan ir.PhysicalPlan, query string, bindVars map[string]any) (previewPartitionCost, bool) {
	planBytes, err := json.Marshal(plan)
	if err != nil {
		return previewPartitionCost{}, false
	}
	bindBytes, err := json.Marshal(bindVars)
	if err != nil {
		return previewPartitionCost{}, false
	}
	cost := previewPartitionCost{QueryBytes: len(query) + len(bindBytes), PlanBytes: len(planBytes)}
	cost.Operations, cost.Projections = countPreviewPlanWork(plan)
	if cost.Operations < 0 || cost.Projections < 0 {
		return previewPartitionCost{}, false
	}
	cost.Work = uint64(cost.Operations + cost.Projections)
	cost.Nodes = countSerializedNodes(planBytes)
	return cost, true
}

func countSerializedNodes(data []byte) uint64 {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return ^uint64(0)
	}
	var count func(any) uint64
	count = func(value any) uint64 {
		switch item := value.(type) {
		case map[string]any:
			total := uint64(1)
			for _, child := range item {
				total += count(child)
			}
			return total
		case []any:
			total := uint64(1)
			for _, child := range item {
				total += count(child)
			}
			return total
		default:
			return 1
		}
	}
	return count(value)
}

func countPreviewPlanWork(plan ir.PhysicalPlan) (operations, projections int) {
	var countOperations func([]ir.PhysicalOperation)
	countOperations = func(ops []ir.PhysicalOperation) {
		operations += len(ops)
		for _, operation := range ops {
			if operation.Return != nil {
				projections += len(operation.Return.Projections)
			}
			if operation.Set != nil {
				countOperations(operation.Set.Subplan.Operations)
			}
			if operation.RootScan != nil && operation.RootScan.PageCandidateSeed != nil {
				countOperations(operation.RootScan.PageCandidateSeed.Subplan.Operations)
			}
		}
	}
	countOperations(plan.Operations)
	return operations, projections
}

func (cost previewPartitionCost) add(other previewPartitionCost) previewPartitionCost {
	return previewPartitionCost{
		QueryBytes:     cost.QueryBytes + other.QueryBytes,
		PlanBytes:      cost.PlanBytes + other.PlanBytes,
		Operations:     cost.Operations + other.Operations,
		Projections:    cost.Projections + other.Projections,
		Work:           cost.Work + other.Work,
		Nodes:          cost.Nodes + other.Nodes,
		PeakQueryBytes: max(max(cost.PeakQueryBytes, other.PeakQueryBytes), other.QueryBytes),
	}
}

func partitionCostWithinTotalBudget(partitions, full previewPartitionCost, partitionCount int) bool {
	if full.QueryBytes <= 0 || full.PlanBytes <= 0 || full.Work == 0 || partitionCount < 2 {
		return false
	}
	return partitions.QueryBytes <= full.QueryBytes*previewProjectionTotalCostMultiplier &&
		partitions.PlanBytes <= full.PlanBytes*previewProjectionTotalCostMultiplier &&
		partitions.Work <= full.Work*previewProjectionTotalCostMultiplier &&
		partitions.Operations <= 4096 &&
		partitions.PlanBytes <= 8<<20 &&
		partitions.QueryBytes <= 4<<20 &&
		partitions.Nodes <= 200000 &&
		partitions.Operations <= full.Operations*previewProjectionTotalCostMultiplier &&
		partitions.Nodes <= full.Nodes*previewProjectionTotalCostMultiplier &&
		partitions.PeakQueryBytes*100 <= full.QueryBytes*previewProjectionMaxQueryRatioPercent
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func optimizedOutputPlan(output lower.CompiledRecipeOutput, policy ir.PhysicalOptimizationPolicy) (ir.PhysicalPlan, error) {
	if output.OptimizedPlan != nil {
		return ir.ClonePhysicalPlan(*output.OptimizedPlan), nil
	}
	physical, err := optimize.OptimizePhysicalPlanWithPolicy(output.Plan, policy)
	if err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("optimize canonical recipe plan: %w", err)
	}
	return physical, nil
}

func rootKeysPagePlan(plan ir.PhysicalPlan, pageSize int) (ir.PhysicalPlan, error) {
	if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
		return ir.PhysicalPlan{}, err
	}
	if len(plan.Operations) < 6 || plan.Operations[0].RootScan == nil {
		return ir.PhysicalPlan{}, fmt.Errorf("generic physical plan requires a root scan and scope")
	}
	for _, key := range []string{RootPageAfterKeyBind, RootPageSizeBind} {
		if _, exists := plan.BindVars[key]; exists {
			return ir.PhysicalPlan{}, fmt.Errorf("root page bind %q is already defined", key)
		}
	}
	out := ir.ClonePhysicalPlan(plan)
	root := out.Operations[0].RootScan.Variable
	rootPageFilters, candidateSeed := rootPageConstructionFilters(out)
	if candidateSeed != nil {
		out.Operations[0].RootScan.PageCandidateSeed = candidateSeed
	}
	insertAt := rootPageInsertionIndex(out.Operations)
	out.BindVars[RootPageAfterKeyBind] = ""
	out.BindVars[RootPageSizeBind] = pageSize
	source := ir.PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType, SemanticField: "_key"}
	left := ir.PhysicalValue{Variable: root, Path: []string{"_key"}}
	right := ir.PhysicalValue{BindKey: RootPageAfterKeyBind}
	pageOperations := make([]ir.PhysicalOperation, 0, len(rootPageFilters)+4)
	pageOperations = append(pageOperations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Source: source, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "GT", Left: left, Right: &right}}},
	)
	pageOperations = append(pageOperations, rootPageFilters...)
	pageOperations = append(pageOperations,
		ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Source: source, Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{left}}},
		ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Source: source, Limit: &ir.PhysicalLimit{BindKey: RootPageSizeBind}},
		ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Source: source, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "_key", Value: left}}}},
	)
	out.Operations = append(append([]ir.PhysicalOperation(nil), out.Operations[:insertAt]...), pageOperations...)

	out.StageSequence = nil
	if err := ir.ValidateGenericPhysicalPlanScope(out); err != nil {
		return ir.PhysicalPlan{}, err
	}
	return out, nil
}

func rootPageConstructionFilters(plan ir.PhysicalPlan) ([]ir.PhysicalOperation, *ir.PhysicalRootPageCandidateSeed) {
	sequence := plan.StageSequence
	if sequence == nil || len(sequence.Stages) == 0 || len(plan.Operations) == 0 {
		return nil, nil
	}
	var sourceProjections map[string]ir.PhysicalProjection
	for index := len(plan.Operations) - 1; index >= 0; index-- {
		operation := plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		sourceProjections = make(map[string]ir.PhysicalProjection, len(operation.Return.Projections))
		for _, projection := range operation.Return.Projections {
			sourceProjections[projection.Name] = projection
		}
		break
	}
	if len(sourceProjections) == 0 {
		return nil, nil
	}
	root := plan.Operations[0].RootScan.Variable
	operations := make([]ir.PhysicalOperation, 0, len(sequence.Stages)*2)
	includedSetProducers := make(map[string]bool)
	var candidateSeed *ir.PhysicalRootPageCandidateSeed
	priorStageID := sequence.SourceStageID
	for _, stage := range sequence.Stages {
		if stage.Kind != ir.PhysicalStageFilterOp || stage.Filter == nil || len(stage.DerivedLets) != 0 || stage.InputStageID != priorStageID {
			break
		}
		filter, projection, producer, ok := rootPageConstructionFilter(stage, sourceProjections, plan.Operations, root)
		if !ok {
			break
		}
		rootScan := plan.Operations[0].RootScan
		if candidateSeed == nil && producer != nil && rootScan != nil && rootScan.Population == nil && rootScan.CohortSource == nil {
			candidateSeed = rootPageRelatedCandidateSeed(&plan, stage, projection, producer, root)
		}
		if producer != nil && !includedSetProducers[producer.Set.Variable] {
			operations = append(operations, ir.ClonePhysicalOperation(*producer))
			includedSetProducers[producer.Set.Variable] = true
		}
		if projection.Expression != nil {
			operations = append(operations, ir.PhysicalOperation{
				Kind: ir.PhysicalExpressionLetOp,
				ExpressionLet: &ir.PhysicalExpressionLet{
					Variable: stage.InputRowVariable, Expression: ir.ClonePhysicalExpression(*projection.Expression),
				},
			})

			filter.Predicate.Left = ir.PhysicalValue{Variable: stage.InputRowVariable}
		} else {
			filter.Predicate.Left = projection.Value
		}
		operations = append(operations, ir.PhysicalOperation{
			Kind:   ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{SemanticNode: plan.Source.SemanticNode, ResourceType: plan.Source.ResourceType, SemanticField: projection.Name},
			Filter: &filter,
		})
		sourceProjections = rootPagePassThroughProjections(stage, sourceProjections)
		priorStageID = stage.ID
	}
	return operations, candidateSeed
}

func rootPageRelatedCandidateSeed(plan *ir.PhysicalPlan, stage ir.PhysicalConstructionStage, projection ir.PhysicalProjection, producer *ir.PhysicalOperation, root string) *ir.PhysicalRootPageCandidateSeed {
	if plan == nil || producer == nil || producer.Set == nil || producer.Set.Projection == nil || producer.Set.Reduction == nil || stage.Filter == nil ||
		stage.Filter.Expression != nil || stage.Filter.Predicate.Operator != "EQUALS" ||
		stage.Filter.Predicate.Correlation != nil || stage.Filter.Predicate.LeftExpression != nil || stage.Filter.Predicate.Right == nil || stage.Filter.Predicate.Right.BindKey == "" {
		return nil
	}
	set := producer.Set
	reduced := projection.Value
	if projection.Expression != nil || reduced.Variable != set.Reduction.Variable || reduced.BindKey != "" || len(reduced.Path) != 1 {
		return nil
	}
	var sourceField string
	for _, field := range set.Reduction.Fields {
		if field.Name != reduced.Path[0] || field.Mode != ir.PhysicalSetReductionFirst {
			continue
		}
		if sourceField != "" {
			return nil
		}
		sourceField = field.SourceField
	}
	if sourceField == "" {
		return nil
	}
	var selected ir.PhysicalSetProjectionField
	for _, field := range set.Projection.Fields {
		if field.Name == sourceField {
			selected = field
			break
		}
	}
	if selected.Name == "" || selected.ExecutionMode != ir.PhysicalSelectorDirectScalar || len(selected.Selector.Steps) == 0 || selected.Selector.Filter != nil {
		return nil
	}
	selectorPath := []string{"payload"}
	for _, step := range selected.Selector.Steps {
		if step.Iterate || step.Index != nil {
			return nil
		}
		selectorPath = append(selectorPath, step.Field)
	}
	traversalIndex := -1
	for index, operation := range set.Subplan.Operations {
		if operation.Kind == ir.PhysicalTraversalOp {
			if traversalIndex != -1 || operation.Traversal == nil || operation.Traversal.SourceVariable != root ||
				operation.Traversal.Direction != ir.PhysicalInbound && operation.Traversal.Direction != ir.PhysicalOutbound {
				return nil
			}
			traversalIndex = index
		}
	}
	if traversalIndex != 0 {
		return nil
	}
	traversal := *set.Subplan.Operations[traversalIndex].Traversal
	childType, ok := plan.BindVars[traversal.TargetTypeBindKey].(string)
	if !ok || childType == "" || selected.ResourceType != childType || plan.Source.ResourceType == "" {
		return nil
	}
	collectionBind := nextRootPageBindKey(plan.BindVars, "loom_root_page_candidate_collection")
	rootTypeBind := nextRootPageBindKey(plan.BindVars, "loom_root_page_candidate_root_type")
	plan.BindVars[collectionBind] = childType
	plan.BindVars[rootTypeBind] = plan.Source.ResourceType

	childVariable := traversal.TargetVariable
	rootCandidateVariable := traversal.SourceVariable
	seedOperations := []ir.PhysicalOperation{{
		Kind:           ir.PhysicalCollectionScanOp,
		Source:         ir.PhysicalSource{SemanticNode: set.Subplan.Operations[traversalIndex].Source.SemanticNode, ResourceType: childType},
		CollectionScan: &ir.PhysicalCollectionScan{Variable: childVariable, CollectionBindKey: collectionBind},
	}}
	childProjectScoped, childGenerationScoped, childAuthScoped := false, false, false
	var childAuth *ir.PhysicalDerivedLet
	var childAuthFilter *ir.PhysicalFilter
	for _, operation := range set.Subplan.Operations[traversalIndex+1:] {
		if isRootPageDirectScopeFilter(operation, childVariable, "project", "project") {
			seedOperations = append(seedOperations, ir.ClonePhysicalOperation(operation))
			childProjectScoped = true
		}
		if isRootPageDirectScopeFilter(operation, childVariable, rootPageDatasetGenerationField, rootPageDatasetGenerationBind) {
			seedOperations = append(seedOperations, ir.ClonePhysicalOperation(operation))
			childGenerationScoped = true
		}
		if operation.Kind != ir.PhysicalDerivedLetOp || operation.DerivedLet == nil || operation.DerivedLet.Operator != "AUTH_RESOURCE_PATH_ALLOWED" {
			continue
		}
		for _, input := range operation.DerivedLet.Inputs {
			if input.Variable != childVariable || len(input.Path) != 1 || input.Path[0] != "auth_resource_path" {
				continue
			}
			clone := *operation.DerivedLet
			clone.Variable = nextRootPageSeedVariable(plan, "__loom_root_page_candidate_child_auth")
			clone.Inputs = []ir.PhysicalValue{
				{Variable: childVariable, Path: []string{"auth_resource_path"}},
				{BindKey: "auth_resource_paths"}, {BindKey: "auth_resource_paths_unrestricted"},
			}
			childAuth = &clone
			childAuthFilter = &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: clone.Variable}, Right: &ir.PhysicalValue{BindKey: "scope_allowed"},
			}}
			childAuthScoped = true
			break
		}
	}
	if !childProjectScoped || !childGenerationScoped || !childAuthScoped {
		return nil
	}
	seedOperations = append(seedOperations,
		ir.PhysicalOperation{Kind: ir.PhysicalDerivedLetOp, Source: ir.PhysicalSource{ResourceType: childType, SemanticField: "auth_resource_path"}, DerivedLet: childAuth},
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Source: ir.PhysicalSource{ResourceType: childType, SemanticField: "auth_resource_path"}, Filter: childAuthFilter},
	)
	seedPredicate := ir.PhysicalPredicate{
		Operator:  stage.Filter.Predicate.Operator,
		Left:      ir.PhysicalValue{Variable: childVariable, Path: selectorPath},
		ValueKind: stage.Filter.Predicate.ValueKind,
	}
	right := *stage.Filter.Predicate.Right
	right.Path = append([]string(nil), right.Path...)
	seedPredicate.Right = &right
	seedOperations = append(seedOperations, ir.PhysicalOperation{
		Kind:   ir.PhysicalFilterOp,
		Source: ir.PhysicalSource{SemanticNode: stage.ID, ResourceType: childType, SemanticField: projection.Name},
		Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &seedPredicate}},
	})
	traversal.SourceVariable = childVariable
	traversal.TargetVariable = rootCandidateVariable
	traversal.SourceTypeBindKey = set.Subplan.Operations[traversalIndex].Traversal.TargetTypeBindKey
	traversal.TargetTypeBindKey = rootTypeBind
	traversal.Direction = reversePhysicalTraversalDirection(traversal.Direction)
	if traversal.Direction == ir.PhysicalOutbound {
		traversal.EdgeTargetTypeField = "to_type"
	} else {
		traversal.EdgeTargetTypeField = "from_type"
	}
	if traversal.Strategy == ir.PhysicalTraversalEndpointLookup {
		indexTypeField := "from_type"
		traversal.EndpointField, traversal.EndpointJoinField = "_to", "_from"
		if traversal.Direction == ir.PhysicalOutbound {
			traversal.EndpointField, traversal.EndpointJoinField = "_from", "_to"
			indexTypeField = "to_type"
		}
		traversal.EndpointIndexFields = []string{traversal.EndpointField, "project", rootPageDatasetGenerationField, "label", indexTypeField}
	}
	seedOperations = append(seedOperations, ir.PhysicalOperation{
		Kind: ir.PhysicalTraversalOp, Source: set.Subplan.Operations[traversalIndex].Source, Traversal: &traversal,
	})
	for _, operation := range set.Subplan.Operations[traversalIndex+1:] {
		seedOperations = append(seedOperations, remapRootPageReverseScope(operation, childVariable, rootCandidateVariable))
	}
	return &ir.PhysicalRootPageCandidateSeed{Subplan: ir.PhysicalSubplan{
		Operations: seedOperations,
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Value: &ir.PhysicalValue{Variable: rootCandidateVariable, Path: []string{"_key"}},
		},
		Sort: &ir.PhysicalValue{Variable: rootCandidateVariable, Path: []string{"_key"}}, Unique: true,
	}}
}

func isRootPageDirectScopeFilter(operation ir.PhysicalOperation, variable, field, bindKey string) bool {
	if operation.Kind != ir.PhysicalFilterOp || operation.Filter == nil || operation.Filter.Expression != nil || operation.Filter.Predicate.LeftExpression != nil {
		return false
	}
	predicate := operation.Filter.Predicate
	return predicate.Operator == "EQUALS" && predicate.Left.Variable == variable && predicate.Left.BindKey == "" && len(predicate.Left.Path) == 1 && predicate.Left.Path[0] == field &&
		predicate.Right != nil && predicate.Right.BindKey == bindKey && predicate.Right.Variable == "" && len(predicate.Right.Path) == 0
}

func remapRootPageReverseScope(operation ir.PhysicalOperation, childVariable, rootVariable string) ir.PhysicalOperation {
	clone := ir.ClonePhysicalOperation(operation)
	if isRootPageDirectScopeFilter(clone, childVariable, "project", "project") ||
		isRootPageDirectScopeFilter(clone, childVariable, rootPageDatasetGenerationField, rootPageDatasetGenerationBind) {
		clone.Filter.Predicate.Left.Variable = rootVariable
		return clone
	}
	if clone.Kind == ir.PhysicalDerivedLetOp && clone.DerivedLet != nil && clone.DerivedLet.Operator == "AUTH_RESOURCE_PATH_ALLOWED" {
		for index := range clone.DerivedLet.Inputs {
			input := &clone.DerivedLet.Inputs[index]
			if input.Variable == childVariable && len(input.Path) == 1 && input.Path[0] == "auth_resource_path" {
				input.Variable = rootVariable
			}
		}
	}
	return clone
}

func nextRootPageSeedVariable(plan *ir.PhysicalPlan, base string) string {
	used := make(map[string]bool, len(plan.Operations)*2)
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalRootScanOp && operation.RootScan != nil {
			used[operation.RootScan.Variable] = true
		}
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil {
			used[operation.Set.Variable] = true
			if operation.Set.Reduction != nil {
				used[operation.Set.Reduction.Variable] = true
			}
		}
	}
	if !used[base] {
		return base
	}
	for index := 1; ; index++ {
		candidate := fmt.Sprintf("%s_%d", base, index)
		if !used[candidate] {
			return candidate
		}
	}
}

func nextRootPageBindKey(bindVars map[string]any, base string) string {
	if _, exists := bindVars[base]; !exists {
		return base
	}
	for index := 1; ; index++ {
		key := fmt.Sprintf("%s_%d", base, index)
		if _, exists := bindVars[key]; !exists {
			return key
		}
	}
}

func reversePhysicalTraversalDirection(direction ir.PhysicalTraversalDirection) ir.PhysicalTraversalDirection {
	if direction == ir.PhysicalInbound {
		return ir.PhysicalOutbound
	}
	return ir.PhysicalInbound
}

func rootPageConstructionFilter(stage ir.PhysicalConstructionStage, projections map[string]ir.PhysicalProjection, physicalOperations []ir.PhysicalOperation, root string) (ir.PhysicalFilter, ir.PhysicalProjection, *ir.PhysicalOperation, bool) {
	if stage.Filter.Expression != nil {
		return ir.PhysicalFilter{}, ir.PhysicalProjection{}, nil, false
	}
	left := stage.Filter.Predicate.Left
	if left.Variable != stage.InputRowVariable || left.BindKey != "" || len(left.Path) != 1 {
		return ir.PhysicalFilter{}, ir.PhysicalProjection{}, nil, false
	}
	projection, ok := projections[left.Path[0]]
	if !ok {
		return ir.PhysicalFilter{}, ir.PhysicalProjection{}, nil, false
	}
	var producer *ir.PhysicalOperation
	if !rootPageProjectionAvailable(projection, root) {
		producer = rootPageProjectionSetProducer(projection, physicalOperations, root)
		if producer == nil {
			return ir.PhysicalFilter{}, ir.PhysicalProjection{}, nil, false
		}
	}
	filter := ir.ClonePhysicalOperation(ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: stage.Filter}).Filter
	if filter == nil {
		return ir.PhysicalFilter{}, ir.PhysicalProjection{}, nil, false
	}
	return *filter, projection, producer, true
}

func rootPageProjectionSetProducer(projection ir.PhysicalProjection, operations []ir.PhysicalOperation, root string) *ir.PhysicalOperation {
	if projection.Expression != nil || projection.Value.Variable == "" || projection.Value.BindKey != "" || len(projection.Value.Path) != 1 {
		return nil
	}
	var match *ir.PhysicalOperation
	for index := range operations {
		operation := &operations[index]
		if operation.Kind != ir.PhysicalSetOp || operation.Set == nil || operation.Set.Reduction == nil {
			continue
		}
		set := operation.Set
		if set.SourceSetVariable != "" || len(set.Subplan.Captures) != 1 || set.Subplan.Captures[0] != root ||
			set.Reduction.Variable != projection.Value.Variable || set.Reduction.SourceSetVariable != set.Variable {
			continue
		}
		matchedField := false
		for _, field := range set.Reduction.Fields {
			if field.Name == projection.Value.Path[0] {
				if matchedField {
					return nil
				}
				if field.Mode != ir.PhysicalSetReductionFirst {
					return nil
				}
				matchedField = true
			}
		}
		if !matchedField || !rootCorrelatedPhysicalSet(set, root) {
			continue
		}
		if match != nil {
			return nil
		}
		match = operation
	}
	return match
}

func rootCorrelatedPhysicalSet(set *ir.PhysicalSet, root string) bool {
	if set == nil || root == "" || set.SourceSetVariable != "" || len(set.Subplan.Captures) != 1 || set.Subplan.Captures[0] != root || len(set.Subplan.Operations) == 0 {
		return false
	}
	first := set.Subplan.Operations[0]
	if first.Kind != ir.PhysicalTraversalOp || first.Traversal == nil || first.Traversal.SourceVariable != root {
		return false
	}
	traversals := 0
	for _, operation := range set.Subplan.Operations {
		if operation.Kind == ir.PhysicalTraversalOp {
			traversals++
			if operation.Traversal == nil || operation.Traversal.SourceVariable != root {
				return false
			}
		}
	}
	return traversals == 1
}

func rootPageProjectionAvailable(projection ir.PhysicalProjection, root string) bool {
	if projection.Expression != nil {
		expression := projection.Expression
		if expression.Kind == ir.PhysicalExtractExpression && expression.Extract != nil {
			extract := expression.Extract
			if extract.Prepared != nil || extract.Source.Variable != root || extract.Source.BindKey != "" {
				return false
			}
			for _, fallback := range extract.Fallbacks {
				if fallback.Source.Variable != root || fallback.Source.BindKey != "" {
					return false
				}
			}
			return true
		}
		if expression.Kind == ir.PhysicalValueExpression && expression.Value != nil {
			return expression.Value.Variable == root && expression.Value.BindKey == ""
		}
		return false
	}
	return projection.Value.Variable == root && projection.Value.BindKey == ""
}

func rootPagePassThroughProjections(stage ir.PhysicalConstructionStage, projections map[string]ir.PhysicalProjection) map[string]ir.PhysicalProjection {
	outputs := make(map[string]ir.PhysicalProjection, len(stage.OutputProjections))
	for _, output := range stage.OutputProjections {
		value := output.Value
		if output.Expression != nil || value.Variable != stage.InputRowVariable || value.BindKey != "" || len(value.Path) != 1 {
			continue
		}
		projection, ok := projections[value.Path[0]]
		if !ok {
			continue
		}
		projection.Name = output.Name
		outputs[output.Name] = projection
	}
	return outputs
}

func selectedRootsPlan(plan ir.PhysicalPlan) (ir.PhysicalPlan, bool, error) {
	if _, exists := plan.BindVars[RootPageKeysBind]; exists {
		return ir.PhysicalPlan{}, false, fmt.Errorf("root page bind %q is already defined", RootPageKeysBind)
	}
	out := ir.ClonePhysicalPlan(plan)
	root := out.Operations[0].RootScan.Variable
	insertAt := rootPageInsertionIndex(out.Operations)
	out.BindVars[RootPageKeysBind] = []string{}
	if rootPageKeyLookupSafe(out) {
		return out, true, nil
	}
	left := ir.PhysicalValue{Variable: root, Path: []string{"_key"}}
	right := ir.PhysicalValue{BindKey: RootPageKeysBind}
	filter := ir.PhysicalOperation{
		Kind:   ir.PhysicalFilterOp,
		Source: ir.PhysicalSource{SemanticNode: out.Source.SemanticNode, ResourceType: out.Source.ResourceType, SemanticField: "_key"},
		Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "IN", Left: left, Right: &right}},
	}
	operations := make([]ir.PhysicalOperation, 0, len(out.Operations)+1)
	operations = append(operations, out.Operations[:insertAt]...)
	operations = append(operations, filter)
	operations = append(operations, out.Operations[insertAt:]...)
	out.Operations = operations
	if err := ir.ValidateGenericPhysicalPlanScope(out); err != nil {
		return ir.PhysicalPlan{}, false, err
	}
	return out, false, nil
}

func rootPageKeyLookupSafe(plan ir.PhysicalPlan) bool {
	if plan.PreviewSourceWindowByRootID {
		return false
	}
	if len(plan.Operations) == 0 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return false
	}
	root := plan.Operations[0].RootScan
	if root.Population != nil || root.CohortSource != nil || root.PageCandidateSeed != nil {
		return false
	}
	if plan.StageSequence != nil {
		sequence := plan.StageSequence
		if sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow ||
			sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil {
			return false
		}
		for _, stage := range sequence.Stages {
			switch stage.Kind {
			case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageUnpivotOp,
				ir.PhysicalStageExpandOp, ir.PhysicalStageRelatedSourceOp, ir.PhysicalStageRelatedExpandOp,
				ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedFieldOp:

			case ir.PhysicalStagePivotOp, ir.PhysicalStageGroupOp, ir.PhysicalStageCodedGroupOp,
				ir.PhysicalStageCohortGroupOp:

				return false
			default:

				return false
			}
		}
	}
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return false
			}
		case ir.PhysicalTraversalOp, ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp,
			ir.PhysicalSetOp, ir.PhysicalUnnestOp, ir.PhysicalUnpivotOp, ir.PhysicalSortOp, ir.PhysicalLimitOp,
			ir.PhysicalReturnOp:

		default:

			return false
		}
	}
	return true
}

func rootPageInsertionIndex(operations []ir.PhysicalOperation) int {
	index := 5
	for index < len(operations) {
		operation := operations[index]
		if operation.Kind == ir.PhysicalExpressionLetOp {

			if index+1 >= len(operations) || operations[index+1].Kind != ir.PhysicalFilterOp {
				break
			}
		} else if operation.Kind != ir.PhysicalFilterOp {
			break
		}
		index++
	}
	return index
}
