package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// RenderedPhysicalPlan is an executable AQL representation of a validated
// PhysicalPlan. BindVars is independent of the input plan and uses Arango's
// required "@name" key form for collection bind variables referenced as
// "@@name" in Query.
//
// This renderer covers generic physical navigation and rich expression
// operators emitted by BuildGenericPhysicalPlan. Projection names, including
// nested object field names, remain bind-backed and never become AQL source.
type RenderedPhysicalPlan struct {
	Query    string
	BindVars map[string]any
}

// RenderPhysicalPlan renders a validated physical plan to deterministic AQL.
// It keeps data and metadata values out of the generated AQL source.
func RenderPhysicalPlan(plan ir.PhysicalPlan) (RenderedPhysicalPlan, error) {
	if err := plan.Validate(); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}

	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	if err := validateRenderablePhysicalPlan(plan, collectionKeys); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalGraphReturnOp {
			return renderGraphPhysicalPlan(plan, collectionKeys)
		}
	}
	// This renderer deliberately supports only BuildGenericPhysicalPlan's
	// navigation contract. Validate its required project/auth windows again at
	// the executable boundary so a manually assembled plan cannot render an
	// unscoped resource scan.
	if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("verify renderable generic physical plan scope: %w", err)
	}
	layout, err := buildNavigationRenderLayout(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}

	renderer := physicalPlanRenderer{
		bindVars:       runtimePhysicalBindVars(plan.BindVars, collectionKeys),
		collectionKeys: collectionKeys,
		setVariables:   map[string]string{},
		reservedVars:   physicalPlanVariableNames(plan),
	}
	lines := make([]string, 0, len(plan.Operations)+1)
	lines = append(lines, fmt.Sprintf("FOR %s IN @@%s", layout.root.Variable, layout.root.CollectionBindKey))
	for index, operation := range layout.rootScope {
		line, err := renderer.renderScopeOperation(operation, "  ")
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render root scope operation %d (%s): %w", index, operation.Kind, err)
		}
		lines = append(lines, line...)
	}
	for index, operation := range layout.rootPredicates {
		line, err := renderer.renderScopeOperation(operation, "  ")
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render root predicate %d (%s): %w", index, operation.Kind, err)
		}
		lines = append(lines, line...)
	}
	for index, unnest := range layout.unnests {
		line, err := renderer.renderUnnest(unnest, "  ", index, 0)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render unnest %d: %w", index+1, err)
		}
		lines = append(lines, line...)
	}
	for index, operation := range layout.rootWindow {
		line, err := renderer.renderRootWindowOperation(operation, "  ")
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render root execution window operation %d (%s): %w", index, operation.Kind, err)
		}
		lines = append(lines, line...)
	}
	traversalIndex, setIndex, expressionLetIndex := 0, 0, 0
	for _, item := range layout.postWindow {
		var line []string
		switch item.operation.Kind {
		case ir.PhysicalTraversalOp:
			traversalIndex++
			block := physicalNavigationTraversal{traversal: *item.operation.Traversal, scope: item.traversalScope}
			line, err = renderer.renderTraversalSet(block, layout.root.Variable, traversalIndex)
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render traversal %d: %w", traversalIndex, err)
			}
		case ir.PhysicalSetOp:
			setIndex++
			line, err = renderer.renderSet(*item.operation.Set, setIndex)
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render child set %d: %w", setIndex, err)
			}
		case ir.PhysicalExpressionLetOp:
			line, err = renderer.renderExpressionLet(item.operation, "  ")
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render expression LET %d: %w", expressionLetIndex, err)
			}
			expressionLetIndex++
		default:
			return RenderedPhysicalPlan{}, fmt.Errorf("render post-window operation %q: unsupported operation", item.operation.Kind)
		}
		lines = append(lines, line...)
	}
	returnExpression, err := renderer.renderReturn(layout.returnOp)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render RETURN: %w", err)
	}
	lines = append(lines, "RETURN "+returnExpression)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{
		Query:    query,
		BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query),
	}, nil
}

// pruneUnusedRuntimeBindVars is required after physical rewrites. Traversal
// sharing can remove a typed edge predicate while retaining its original
// logical bind in the cloned plan. Arango rejects undeclared bind variables,
// so only values referenced by the final rendered AQL may cross the execution
// boundary.
func pruneUnusedRuntimeBindVars(bindVars map[string]any, query string) map[string]any {
	pruned := make(map[string]any, len(bindVars))
	for key, value := range bindVars {
		if strings.Contains(query, "@"+key) {
			pruned[key] = value
		}
	}
	return pruned
}

func (r *physicalPlanRenderer) renderRootWindowOperation(operation ir.PhysicalOperation, indent string) ([]string, error) {
	switch operation.Kind {
	case ir.PhysicalSortOp:
		value, err := r.renderValue(operation.Sort.Value)
		if err != nil {
			return nil, err
		}
		return []string{indent + "SORT " + value}, nil
	case ir.PhysicalLimitOp:
		if _, collectionBinding := r.collectionKeys[operation.Limit.BindKey]; collectionBinding {
			return nil, fmt.Errorf("limit bind key %q cannot be a collection bind", operation.Limit.BindKey)
		}
		return []string{indent + "LIMIT @" + operation.Limit.BindKey}, nil
	default:
		return nil, fmt.Errorf("root execution window cannot contain physical operation %q", operation.Kind)
	}
}

type physicalPlanRenderer struct {
	bindVars       map[string]any
	collectionKeys map[string]struct{}
	setVariables   map[string]string
	reservedVars   map[string]struct{}
	preparedItem   string
}

func (r *physicalPlanRenderer) renderExpressionLet(operation ir.PhysicalOperation, indent string) ([]string, error) {
	if operation.ExpressionLet == nil {
		return nil, fmt.Errorf("expression LET is missing payload")
	}
	expression, err := r.renderExpression(operation.ExpressionLet.Expression)
	if err != nil {
		return nil, err
	}
	return []string{fmt.Sprintf("%sLET %s = %s", indent, operation.ExpressionLet.Variable, expression)}, nil
}

func (r *physicalPlanRenderer) renderScopeOperation(operation ir.PhysicalOperation, indent string) ([]string, error) {
	switch operation.Kind {
	case ir.PhysicalFilterOp:
		var expression string
		var err error
		if operation.Filter.Expression != nil {
			expression, err = r.renderPredicateExpression(*operation.Filter.Expression, indent)
		} else {
			expression, err = r.renderPredicate(operation.Filter.Predicate)
		}
		if err != nil {
			return nil, err
		}
		return []string{indent + "FILTER " + expression}, nil
	case ir.PhysicalDerivedLetOp:
		expression, err := r.renderDerivedLet(*operation.DerivedLet)
		if err != nil {
			return nil, err
		}
		return []string{fmt.Sprintf("%sLET %s = %s", indent, operation.DerivedLet.Variable, expression)}, nil
	case ir.PhysicalExpressionLetOp:
		return r.renderExpressionLet(operation, indent)
	default:
		return nil, fmt.Errorf("navigation scope cannot contain physical operation %q", operation.Kind)
	}
}

// physicalNavigationRenderLayout is the intentionally narrow executable shape
// produced by BuildGenericPhysicalPlan. Post-window operations retain physical
// plan order because sets, traversals, and expression LETs may depend on values
// introduced by any preceding operation.
type physicalNavigationRenderLayout struct {
	root           ir.PhysicalRootScan
	rootScope      []ir.PhysicalOperation
	rootPredicates []ir.PhysicalOperation
	rootWindow     []ir.PhysicalOperation
	unnests        []ir.PhysicalUnnest
	postWindow     []physicalNavigationRenderItem
	returnOp       ir.PhysicalReturn
}

type physicalNavigationRenderItem struct {
	operation      ir.PhysicalOperation
	traversalScope []ir.PhysicalOperation
}

type physicalNavigationTraversal struct {
	traversal ir.PhysicalTraversal
	scope     []ir.PhysicalOperation
}
