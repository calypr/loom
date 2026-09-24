package compiler

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// ProjectionShardPolicy is the conservative bound for one flat output
// projection plan. The bound is deliberately expressed in public projections
// rather than rendered AQL bytes or bind count: both are renderer details and
// the Arango execution-plan limit is ultimately a plan-shape constraint.
//
// This policy is intentionally named and immutable. Callers do not tune it
// from a request, which would make an execution-limit workaround part of the
// public API and could reintroduce oversized plans.
type ProjectionShardPolicy struct {
	Name                 string
	MaxPublicProjections int
}

const (
	conservativeFlatProjectionShardPolicyName = "conservative-flat-projection-v1"
	defaultMaxPublicProjectionsPerShard       = 8
)

// DefaultProjectionShardPolicy returns the production policy for bounded flat
// projection plans. It is deliberately separate from the physical optimizer
// policy because sharding is an execution-shape safeguard, not an optional
// semantic rewrite.
func DefaultProjectionShardPolicy() ProjectionShardPolicy {
	return ProjectionShardPolicy{
		Name:                 conservativeFlatProjectionShardPolicyName,
		MaxPublicProjections: defaultMaxPublicProjectionsPerShard,
	}
}

// projectionShardPhysicalPlans partitions a finalized, windowed physical
// plan. The caller must append auth_resource_path before this function so the
// authorization projection is carried by every shard.
func projectionShardPhysicalPlans(plan ir.PhysicalPlan, output lower.CompiledRecipeOutput, policy ProjectionShardPolicy) ([]ir.PhysicalPlan, [][]string, error) {
	if policy.MaxPublicProjections < 1 {
		return nil, nil, fmt.Errorf("projection shard policy %q has non-positive public projection bound", policy.Name)
	}
	returnIndex := -1
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalReturnOp && plan.Operations[index].Return != nil {
			returnIndex = index
			break
		}
	}
	if returnIndex < 0 {
		return nil, nil, fmt.Errorf("projection sharding requires a flat RETURN projection")
	}
	projections := plan.Operations[returnIndex].Return.Projections
	public := make([]ir.PhysicalProjection, 0, len(projections))
	for _, projection := range projections {
		if isPublicPhysicalProjection(projection) {
			public = append(public, projection)
		}
	}
	if len(public) <= policy.MaxPublicProjections {
		return []ir.PhysicalPlan{ir.ClonePhysicalPlan(plan)}, [][]string{physicalPublicProjectionNames(public)}, nil
	}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalGroupRowsOp, ir.PhysicalGroupedPivotOp, ir.PhysicalUnpivotOp:
			return nil, nil, fmt.Errorf("output %q is too wide for projection sharding: %s outputs are unsupported", output.Name, projectionShardUnsupportedShape(operation.Kind))
		}
	}
	if err := validateShardSchema(output, public); err != nil {
		return nil, nil, err
	}

	// Every shard carries compiler-only projections. They are required for
	// stable identity, publication authorization, and post-query dynamic
	// validation. The dynamic runtime-key object is filtered to only the
	// families represented by that shard below.
	internal := make([]ir.PhysicalProjection, 0, len(projections)-len(public))
	for _, projection := range projections {
		if !isPublicPhysicalProjection(projection) && projection.Name != "__loom_dynamic_runtime_keys" {
			internal = append(internal, projection)
		}
	}
	dynamicFamilies := dynamicRuntimeFamilyNames(output)

	plans := make([]ir.PhysicalPlan, 0, (len(public)+policy.MaxPublicProjections-1)/policy.MaxPublicProjections)
	names := make([][]string, 0, cap(plans))
	for start := 0; start < len(public); start += policy.MaxPublicProjections {
		end := start + policy.MaxPublicProjections
		if end > len(public) {
			end = len(public)
		}
		shardPublic := append([]ir.PhysicalProjection(nil), public[start:end]...)
		shard := ir.ClonePhysicalPlan(plan)
		shardProjections := make([]ir.PhysicalProjection, 0, len(internal)+len(shardPublic)+1)
		shardProjections = append(shardProjections, internal...)
		shardProjections = append(shardProjections, shardPublic...)
		if runtime, ok := findPhysicalProjection(projections, "__loom_dynamic_runtime_keys"); ok {
			filtered, err := filterDynamicRuntimeProjection(runtime, shardPublic, dynamicFamilies)
			if err != nil {
				return nil, nil, fmt.Errorf("output %q projection shard %d: %w", output.Name, len(plans)+1, err)
			}
			if filtered != nil {
				shardProjections = append(shardProjections, *filtered)
			}
		}
		shard.Operations[returnIndex].Return.Projections = shardProjections
		shard, err := pruneProjectionDependencies(shard, returnIndex)
		if err != nil {
			return nil, nil, fmt.Errorf("output %q projection shard %d: %w", output.Name, len(plans)+1, err)
		}
		if err := shard.Validate(); err != nil {
			return nil, nil, fmt.Errorf("output %q projection shard %d validation: %w", output.Name, len(plans)+1, err)
		}
		plans = append(plans, shard)
		names = append(names, physicalPublicProjectionNames(shardPublic))
	}
	return plans, names, nil
}

func projectionShardUnsupportedShape(kind ir.PhysicalOperationKind) string {
	switch kind {
	case ir.PhysicalGroupRowsOp:
		return "group"
	case ir.PhysicalGroupedPivotOp:
		return "table reshape"
	case ir.PhysicalUnpivotOp:
		return "unpivot"
	default:
		return string(kind)
	}
}

func isPublicPhysicalProjection(projection ir.PhysicalProjection) bool {
	if projection.Hidden || projection.Name == "_key" || projection.Name == "auth_resource_path" {
		return false
	}
	return !strings.HasPrefix(projection.Name, "__loom_")
}

func physicalPublicProjectionNames(projections []ir.PhysicalProjection) []string {
	result := make([]string, 0, len(projections))
	for _, projection := range projections {
		if isPublicPhysicalProjection(projection) {
			result = append(result, projection.Name)
		}
	}
	return result
}

func validateShardSchema(output lower.CompiledRecipeOutput, public []ir.PhysicalProjection) error {
	want := publicOutputColumns(output.OutputSchema)
	got := physicalPublicProjectionNames(public)
	if len(want) == 0 {
		return nil
	}
	if len(want) != len(got) {
		return fmt.Errorf("output %q projection sharding requires a flat public schema: finalized projections=%d schema columns=%d", output.Name, len(got), len(want))
	}
	for index := range want {
		if want[index] != got[index] {
			return fmt.Errorf("output %q projection sharding public schema mismatch at column %d: projection %q, schema %q", output.Name, index, got[index], want[index])
		}
	}
	seen := make(map[string]struct{}, len(got))
	for _, name := range got {
		if _, exists := seen[name]; exists {
			return fmt.Errorf("output %q projection sharding found duplicate public projection %q", output.Name, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func findPhysicalProjection(projections []ir.PhysicalProjection, name string) (ir.PhysicalProjection, bool) {
	for _, projection := range projections {
		if projection.Name == name {
			return projection, true
		}
	}
	return ir.PhysicalProjection{}, false
}

func dynamicRuntimeFamilyNames(output lower.CompiledRecipeOutput) map[string]map[string]struct{} {
	families := make(map[string]map[string]struct{})
	for _, column := range output.DynamicColumns {
		if families[column.DynamicName] == nil {
			families[column.DynamicName] = make(map[string]struct{})
		}
		families[column.DynamicName][column.Name] = struct{}{}
	}
	return families
}

func filterDynamicRuntimeProjection(projection ir.PhysicalProjection, public []ir.PhysicalProjection, families map[string]map[string]struct{}) (*ir.PhysicalProjection, error) {
	if projection.Expression == nil || projection.Expression.Object == nil {
		return &projection, nil
	}
	selectedColumns := make(map[string]struct{}, len(public))
	for _, column := range public {
		selectedColumns[column.Name] = struct{}{}
	}
	fields := make([]ir.PhysicalExpressionProjection, 0, len(projection.Expression.Object.Fields))
	for _, field := range projection.Expression.Object.Fields {
		familyColumns, ok := families[field.Name]
		if !ok {
			return nil, fmt.Errorf("dynamic runtime family %q is missing compiler metadata", field.Name)
		}
		keep := false
		for column := range familyColumns {
			if _, ok := selectedColumns[column]; ok {
				keep = true
				break
			}
		}
		if keep {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return nil, nil
	}
	filtered := projection
	if projection.Expression != nil {
		expression := ir.ClonePhysicalExpression(*projection.Expression)
		filtered.Expression = &expression
	}
	filtered.Expression.Object.Fields = fields
	return &filtered, nil
}

// pruneProjectionDependencies removes projection-only definitions that are no
// longer reachable from selected projections. Set operations need the same
// dependency closure as LETs: a selected projection can reference a prepared
// or reduced set slot, a child set can capture an ancestor set, and a filter or
// row expansion can make a set semantically mandatory. Unknown operations are
// retained conservatively so pruning cannot alter row membership or cardinality.
func pruneProjectionDependencies(plan ir.PhysicalPlan, returnIndex int) (ir.PhysicalPlan, error) {
	closure := newProjectionDependencyClosure(plan)
	returnOperation := plan.Operations[returnIndex]
	if returnOperation.Return == nil {
		return plan, fmt.Errorf("projection dependency pruning requires a return operation")
	}
	for _, projection := range returnOperation.Return.Projections {
		if projection.Expression != nil {
			closure.addExpression(*projection.Expression)
		} else {
			closure.addVariable(projection.Value.Variable)
		}
		if projection.Presence != nil {
			closure.addValue(projection.Presence.Source)
		}
	}
	for index, operation := range plan.Operations {
		if index == returnIndex || operation.Kind == ir.PhysicalExpressionLetOp || operation.Kind == ir.PhysicalDerivedLetOp {
			continue
		}
		closure.addOperation(operation, false)
	}

	filtered := make([]ir.PhysicalOperation, 0, len(plan.Operations))
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalExpressionLetOp:
			if operation.ExpressionLet != nil && !closure.retainedExpressionLets[operation.ExpressionLet.Variable] {
				continue
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet != nil && !closure.retainedDerivedLets[operation.DerivedLet.Variable] {
				continue
			}
		case ir.PhysicalSetOp:
			if operation.Set != nil && !closure.retainedSets[operation.Set.Variable] && physicalSetProjectionOnly(*operation.Set) {
				continue
			}
		}
		filtered = append(filtered, operation)
	}
	plan.Operations = filtered
	return plan, nil
}

type projectionDependencyClosure struct {
	used                   map[string]struct{}
	setDefs                map[string]*ir.PhysicalSet
	auxiliarySetDefs       map[string]string
	expressionLets         map[string]*ir.PhysicalExpressionLet
	derivedLets            map[string]*ir.PhysicalDerivedLet
	retainedSets           map[string]bool
	retainedExpressionLets map[string]bool
	retainedDerivedLets    map[string]bool
	expandedSets           map[string]bool
	expandedExpressionLets map[string]bool
	expandedDerivedLets    map[string]bool
}

func newProjectionDependencyClosure(plan ir.PhysicalPlan) *projectionDependencyClosure {
	closure := &projectionDependencyClosure{
		used:                   make(map[string]struct{}),
		setDefs:                make(map[string]*ir.PhysicalSet),
		auxiliarySetDefs:       make(map[string]string),
		expressionLets:         make(map[string]*ir.PhysicalExpressionLet),
		derivedLets:            make(map[string]*ir.PhysicalDerivedLet),
		retainedSets:           make(map[string]bool),
		retainedExpressionLets: make(map[string]bool),
		retainedDerivedLets:    make(map[string]bool),
		expandedSets:           make(map[string]bool),
		expandedExpressionLets: make(map[string]bool),
		expandedDerivedLets:    make(map[string]bool),
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		switch operation.Kind {
		case ir.PhysicalSetOp:
			if operation.Set != nil {
				closure.setDefs[operation.Set.Variable] = operation.Set
				if operation.Set.Reduction != nil && operation.Set.Reduction.Variable != "" {
					closure.auxiliarySetDefs[operation.Set.Reduction.Variable] = operation.Set.Variable
				}
				if operation.Set.Prepared != nil && operation.Set.Prepared.Variable != "" {
					closure.auxiliarySetDefs[operation.Set.Prepared.Variable] = operation.Set.Variable
				}
			}
		case ir.PhysicalExpressionLetOp:
			if operation.ExpressionLet != nil {
				closure.expressionLets[operation.ExpressionLet.Variable] = operation.ExpressionLet
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet != nil {
				closure.derivedLets[operation.DerivedLet.Variable] = operation.DerivedLet
			}
		}
	}
	return closure
}

func (c *projectionDependencyClosure) addValue(value ir.PhysicalValue) {
	c.addVariable(value.Variable)
}

func (c *projectionDependencyClosure) addVariable(variable string) {
	if variable == "" {
		return
	}
	c.used[variable] = struct{}{}
	if owner, ok := c.auxiliarySetDefs[variable]; ok {
		c.retainSet(owner)
	}
	if _, ok := c.setDefs[variable]; ok {
		c.retainSet(variable)
	}
	if _, ok := c.expressionLets[variable]; ok {
		c.retainExpressionLet(variable)
	}
	if _, ok := c.derivedLets[variable]; ok {
		c.retainDerivedLet(variable)
	}
}

func (c *projectionDependencyClosure) retainSet(variable string) {
	set, ok := c.setDefs[variable]
	if !ok {
		return
	}
	c.used[variable] = struct{}{}
	c.retainedSets[variable] = true
	if c.expandedSets[variable] {
		return
	}
	c.expandedSets[variable] = true
	c.addVariable(set.SourceSetVariable)
	for _, capture := range set.Subplan.Captures {
		c.addVariable(capture)
	}
	for _, operation := range set.Subplan.Operations {
		c.addOperation(operation, true)
	}
	c.addExpression(set.Subplan.Return)
	if set.Subplan.Sort != nil {
		c.addValue(*set.Subplan.Sort)
	}
	if set.Reduction != nil {
		c.addVariable(set.Reduction.SourceSetVariable)
		c.addVariable(set.Reduction.Variable)
	}
	if set.Prepared != nil {
		c.addVariable(set.Prepared.SourceSetVariable)
		c.addVariable(set.Prepared.Variable)
	}
}

func (c *projectionDependencyClosure) retainExpressionLet(variable string) {
	let, ok := c.expressionLets[variable]
	if !ok {
		return
	}
	c.retainedExpressionLets[variable] = true
	if c.expandedExpressionLets[variable] {
		return
	}
	c.expandedExpressionLets[variable] = true
	c.addExpression(let.Expression)
}

func (c *projectionDependencyClosure) retainDerivedLet(variable string) {
	let, ok := c.derivedLets[variable]
	if !ok {
		return
	}
	c.retainedDerivedLets[variable] = true
	if c.expandedDerivedLets[variable] {
		return
	}
	c.expandedDerivedLets[variable] = true
	for _, input := range let.Inputs {
		c.addValue(input)
	}
}

func (c *projectionDependencyClosure) addPredicate(predicate ir.PhysicalPredicate) {
	c.addValue(predicate.Left)
	if predicate.LeftExpression != nil {
		c.addExpression(*predicate.LeftExpression)
	}
	if predicate.Right != nil {
		c.addValue(*predicate.Right)
	}
	if predicate.Correlation != nil {
		c.addValue(predicate.Correlation.Source)
	}
}

func (c *projectionDependencyClosure) addPredicateExpression(predicate ir.PhysicalPredicateExpression) {
	if predicate.Comparison != nil {
		c.addPredicate(*predicate.Comparison)
	}
	for _, child := range predicate.Children {
		c.addPredicateExpression(child)
	}
	if predicate.Exists != nil {
		for _, capture := range predicate.Exists.Captures {
			c.addVariable(capture)
		}
		for _, operation := range predicate.Exists.Operations {
			c.addOperation(operation, true)
		}
		c.addExpression(predicate.Exists.Return)
		if predicate.Exists.Sort != nil {
			c.addValue(*predicate.Exists.Sort)
		}
	}
}

func (c *projectionDependencyClosure) addExpression(expression ir.PhysicalExpression) {
	if expression.Value != nil {
		c.addValue(*expression.Value)
	}
	if expression.Extract != nil {
		c.addValue(expression.Extract.Source)
		if expression.Extract.Prepared != nil {
			c.addVariable(expression.Extract.Prepared.SetVariable)
		}
	}
	if expression.Aggregate != nil {
		c.addValue(expression.Aggregate.Source)
		if expression.Aggregate.Value != nil {
			c.addExpression(*expression.Aggregate.Value)
		}
		if expression.Aggregate.Predicate != nil {
			c.addPredicateExpression(*expression.Aggregate.Predicate)
		}
		if expression.Aggregate.ContributorWindow != nil {
			c.addExpression(expression.Aggregate.ContributorWindow.Timestamp)
			c.addExpression(expression.Aggregate.ContributorWindow.Anchor)
		}
		if expression.Aggregate.Ordering != nil {
			c.addExpression(expression.Aggregate.Ordering.Timestamp)
		}
	}
	if expression.Pivot != nil {
		c.addValue(expression.Pivot.Source)
		if expression.Pivot.Correlation != nil {
			c.addValue(expression.Pivot.Correlation.Source)
		}
		if expression.Pivot.PreparedKey != nil {
			c.addVariable(expression.Pivot.PreparedKey.SetVariable)
		}
		if expression.Pivot.PreparedValue != nil {
			c.addVariable(expression.Pivot.PreparedValue.SetVariable)
		}
	}
	if expression.OwnerRecords != nil {
		c.addValue(expression.OwnerRecords.Correlation.Source)
	}
	if expression.Slice != nil {
		c.addValue(expression.Slice.Source)
		if expression.Slice.Predicate != nil {
			c.addPredicateExpression(*expression.Slice.Predicate)
		}
		if expression.Slice.Sort != nil {
			c.addExpression(*expression.Slice.Sort)
		}
		for _, projection := range expression.Slice.Projections {
			c.addExpression(projection.Expression)
		}
	}
	if expression.ObjectLookup != nil {
		c.addVariable(expression.ObjectLookup.ObjectVariable)
	}
	if expression.KeyedMap != nil {
		c.addExpression(expression.KeyedMap.Source)
		c.addExpression(expression.KeyedMap.ItemKey)
		c.addExpression(expression.KeyedMap.ItemValue)
		for _, fallback := range expression.KeyedMap.ValueFallbacks {
			c.addExpression(fallback)
		}
	}
	if expression.ObjectKeys != nil {
		c.addVariable(expression.ObjectKeys.ObjectVariable)
	}
	if expression.KeySet != nil {
		c.addExpression(expression.KeySet.Source)
		c.addExpression(expression.KeySet.ItemKey)
	}
	if expression.Object != nil {
		for _, field := range expression.Object.Fields {
			c.addExpression(field.Expression)
		}
	}
	if expression.Subplan != nil {
		for _, capture := range expression.Subplan.Captures {
			c.addVariable(capture)
		}
		for _, operation := range expression.Subplan.Operations {
			c.addOperation(operation, true)
		}
		c.addExpression(expression.Subplan.Return)
		if expression.Subplan.Sort != nil {
			c.addValue(*expression.Subplan.Sort)
		}
	}
	if expression.Call != nil {
		for _, argument := range expression.Call.Args {
			c.addExpression(argument)
		}
	}
}

func (c *projectionDependencyClosure) addOperation(operation ir.PhysicalOperation, retainOptionalSets bool) {
	switch operation.Kind {
	case ir.PhysicalRootScanOp:
		if operation.RootScan != nil && operation.RootScan.Population != nil {
			population := operation.RootScan.Population
			for _, filter := range population.MemberFilters {
				c.addOperation(ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &filter}, true)
			}
			for _, resourceOperation := range population.ResourceOperations {
				c.addOperation(resourceOperation, true)
			}
			c.addValue(population.RootKey)
			c.addValue(population.MemberID)
		}
	case ir.PhysicalTraversalOp:
		if operation.Traversal != nil {
			c.addVariable(operation.Traversal.SourceVariable)
		}
	case ir.PhysicalFilterOp:
		if operation.Filter != nil {
			c.addPredicate(operation.Filter.Predicate)
			if operation.Filter.Expression != nil {
				c.addPredicateExpression(*operation.Filter.Expression)
			}
		}
	case ir.PhysicalDerivedLetOp:
		if operation.DerivedLet != nil {
			c.retainDerivedLet(operation.DerivedLet.Variable)
			for _, input := range operation.DerivedLet.Inputs {
				c.addValue(input)
			}
		}
	case ir.PhysicalExpressionLetOp:
		if operation.ExpressionLet != nil {
			c.retainExpressionLet(operation.ExpressionLet.Variable)
			c.addExpression(operation.ExpressionLet.Expression)
		}
	case ir.PhysicalSetOp:
		if operation.Set != nil && (retainOptionalSets || !physicalSetProjectionOnly(*operation.Set)) {
			c.retainSet(operation.Set.Variable)
		}
	case ir.PhysicalUnnestOp:
		if operation.Unnest != nil {
			c.addVariable(operation.Unnest.Owner.RootVariable)
			c.addVariable(operation.Unnest.Owner.OwnerVariable)
			c.addExpression(operation.Unnest.Expression)
			for _, step := range operation.Unnest.Owner.Route {
				c.addVariable(step.Traversal.SourceVariable)
				for _, scopeOperation := range step.Scope {
					c.addOperation(scopeOperation, true)
				}
			}
		}
	case ir.PhysicalSortOp:
		if operation.Sort != nil {
			for _, key := range operation.Sort.Keys {
				c.addValue(key)
			}
		}
	case ir.PhysicalPathSeedOp:
		if operation.PathSeed != nil {
			c.addValue(operation.PathSeed.Node.Value)
		}
	case ir.PhysicalPathExtendOp:
		if operation.PathExtend != nil {
			c.addVariable(operation.PathExtend.SourceVariable)
			c.addVariable(operation.PathExtend.Traversal.SourceVariable)
			c.addValue(operation.PathExtend.Node.Value)
			for _, scopeOperation := range operation.PathExtend.Scope {
				c.addOperation(scopeOperation, true)
			}
		}
	case ir.PhysicalGraphReturnOp:
		if operation.GraphReturn != nil {
			for _, pathSet := range operation.GraphReturn.PathSets {
				c.addVariable(pathSet)
			}
		}
	case ir.PhysicalPopulationMappingReturnOp:
		if operation.PopulationMappingReturn != nil {
			c.addExpression(operation.PopulationMappingReturn.Members)
			for _, part := range operation.PopulationMappingReturn.IdentityParts {
				c.addExpression(part.Expression)
			}
			if operation.PopulationMappingReturn.ExplicitIdentity != nil {
				c.addExpression(*operation.PopulationMappingReturn.ExplicitIdentity)
			}
		}
	case ir.PhysicalCellTraceReturnOp:
		if operation.CellTraceReturn != nil {
			c.addExpression(operation.CellTraceReturn.Value)
			for _, part := range operation.CellTraceReturn.IdentityParts {
				c.addExpression(part.Expression)
			}
			if operation.CellTraceReturn.ExplicitIdentity != nil {
				c.addExpression(*operation.CellTraceReturn.ExplicitIdentity)
			}
		}
	case ir.PhysicalTableShapeExclusionReturnOp:
		if operation.TableShapeExclusionReturn != nil {
			c.addVariable(operation.TableShapeExclusionReturn.Pivot.InputRowVariable)
			for _, projection := range operation.TableShapeExclusionReturn.Pivot.InputProjections {
				if projection.Expression != nil {
					c.addExpression(*projection.Expression)
				} else {
					c.addValue(projection.Value)
				}
			}
		}
	case ir.PhysicalGroupedPivotOp:
		if operation.GroupedPivot != nil {
			c.addVariable(operation.GroupedPivot.InputRowVariable)
			c.addVariable(operation.GroupedPivot.GroupRowsVariable)
			for _, key := range operation.GroupedPivot.GroupKeys {
				c.addVariable(key.Variable)
			}
			for _, projection := range operation.GroupedPivot.InputProjections {
				if projection.Expression != nil {
					c.addExpression(*projection.Expression)
				} else {
					c.addValue(projection.Value)
				}
			}
		}
	case ir.PhysicalUnpivotOp:
		if operation.Unpivot != nil {
			c.addVariable(operation.Unpivot.InputRowVariable)
			for _, projection := range operation.Unpivot.InputProjections {
				if projection.Expression != nil {
					c.addExpression(*projection.Expression)
				} else {
					c.addValue(projection.Value)
				}
			}
			for _, part := range operation.Unpivot.IdentityParts {
				c.addValue(part.Value)
			}
		}
	}
}

// physicalSetProjectionOnly is intentionally strict. A set whose subplan has
// an operation other than the known projection-safe scope operations is kept;
// this leaves membership/cardinality semantics untouched when the IR grows.
func physicalSetProjectionOnly(set ir.PhysicalSet) bool {
	for _, operation := range set.Subplan.Operations {
		switch operation.Kind {
		case ir.PhysicalTraversalOp, ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp:
		case ir.PhysicalSetOp:
			if operation.Set == nil || !physicalSetProjectionOnly(*operation.Set) {
				return false
			}
		case ir.PhysicalRootScanOp, ir.PhysicalCollectionScanOp:
			// Correlated set scope may use a compiler-owned scan; it does not
			// change the outer row grain when the set itself is unused.
		default:
			return false
		}
	}
	return true
}

// compileProjectionShardPhysicalPlans is shared by ordinary query and page
// compilation so both Preview and publication use the exact same partition.
func compileProjectionShardPhysicalPlans(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, limit int, policy ir.PhysicalOptimizationPolicy, shardPolicy ProjectionShardPolicy) ([]ir.PhysicalPlan, [][]string, error) {
	groupRows := len(output.Plan.Operations) == 1 && output.Plan.Operations[0].Kind == ir.PhysicalGroupRowsOp
	if groupRows {
		physical := ir.ClonePhysicalPlan(output.Plan)
		physical, err := withGenericPhysicalExecutionWindow(physical, limit)
		if err != nil {
			return nil, nil, err
		}
		return []ir.PhysicalPlan{physical}, nil, nil
	}
	var physical ir.PhysicalPlan
	var err error
	if output.OptimizedPlan != nil {
		physical = ir.ClonePhysicalPlan(*output.OptimizedPlan)
	} else {
		physical, err = optimize.OptimizePhysicalPlanWithPolicy(output.Plan, policy)
		if err != nil {
			return nil, nil, fmt.Errorf("optimize canonical recipe plan: %w", err)
		}
	}
	physical, err = withGenericPhysicalExecutionWindow(physical, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("apply canonical recipe execution window: %w", err)
	}
	if bindings.IncludeAuthResourcePath {
		if err := appendAuthResourcePathProjection(&physical); err != nil {
			return nil, nil, err
		}
	}
	return projectionShardPhysicalPlans(physical, output, shardPolicy)
}

func renderProjectionShardQueries(plans []ir.PhysicalPlan, output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, limits int, names [][]string) ([]CompiledQuery, error) {
	queries := make([]CompiledQuery, 0, len(plans))
	for index, physical := range plans {
		rendered, err := aql.RenderPhysicalPlan(physical)
		if err != nil {
			return nil, fmt.Errorf("render projection shard %d: %w", index+1, err)
		}
		columns, pivotFields := physicalProjectionMetadata(physical)
		if len(output.Columns) != 0 {
			columns = append([]string(nil), output.Columns...)
		}
		outputSchema := lower.CloneCompiledOutputSchema(output.OutputSchema)
		publicColumns := publicOutputColumns(outputSchema)
		if len(names) > index {
			publicColumns = append([]string(nil), names[index]...)
		}
		queries = append(queries, CompiledQuery{
			Project: bindings.Project, DatasetGeneration: normalizeDatasetGeneration(bindings.DatasetGeneration), RootResourceType: output.RootResourceType,
			TranslationVersion: output.TranslationVersion, AuthResourcePaths: cloneStrings(bindings.AuthResourcePaths), PlanMode: "physical", PlanProfile: "generic_fhir_graph_recipe",
			TraversalCount: physicalTraversalCount(physical), RowIdentity: output.RowIdentity.Clone(), OptimizationRules: recipeOptimizationRules(physical), Query: rendered.Query,
			BindVars: rendered.BindVars, Columns: columns, OutputSchema: outputSchema, PublicColumns: publicColumns, PivotFields: pivotFields, Limit: limits,
			PlanDiagnostics: physicalPlanDiagnostics(physical),
		})
	}
	return queries, nil
}

// CompileRecipeOutputShardsWithPolicy compiles one output into one or more
// independently valid flat projection queries. Narrow outputs return exactly
// one query with the legacy compiler shape.
func CompileRecipeOutputShardsWithPolicy(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, limit int, policy ir.PhysicalOptimizationPolicy) ([]CompiledQuery, error) {
	shardPolicy := DefaultProjectionShardPolicy()
	plans, names, err := compileProjectionShardPhysicalPlans(output, bindings, limit, policy, shardPolicy)
	if err != nil {
		return nil, err
	}
	return renderProjectionShardQueries(plans, output, bindings, limit, names)
}
