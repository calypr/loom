package aql

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"strings"
)

func renderGraphPhysicalPlan(plan ir.PhysicalPlan, collectionKeys map[string]struct{}) (RenderedPhysicalPlan, error) {
	if len(plan.Operations) < 3 || plan.Operations[0].Kind != ir.PhysicalRootScanOp {
		return RenderedPhysicalPlan{}, fmt.Errorf("graph renderer requires ROOT_SCAN followed by path operations")
	}
	last := plan.Operations[len(plan.Operations)-1]
	if last.Kind != ir.PhysicalGraphReturnOp || last.GraphReturn == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("graph renderer requires GRAPH_RETURN as final operation")
	}
	r := &physicalPlanRenderer{renderContext: renderContext{
		bindVars:       runtimePhysicalBindVars(plan.BindVars, collectionKeys),
		collectionKeys: collectionKeys,
		setVariables:   map[string]string{},
		reservedVars:   physicalPlanVariableNames(plan),
	}}
	root := plan.Operations[0].RootScan
	inner := []string{fmt.Sprintf("FOR %s IN @@%s", root.Variable, root.CollectionBindKey)}
	pathStart := 1
	for pathStart < len(plan.Operations)-1 && plan.Operations[pathStart].Kind != ir.PhysicalPathSeedOp && plan.Operations[pathStart].Kind != ir.PhysicalPathExtendOp {
		operation := plan.Operations[pathStart]
		if operation.Kind == ir.PhysicalFilterOp || operation.Kind == ir.PhysicalDerivedLetOp || operation.Kind == ir.PhysicalExpressionLetOp {
			rendered, err := r.renderScopeOperation(operation, "  ")
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render graph root scope %d: %w", pathStart, err)
			}
			inner = append(inner, rendered...)
			pathStart++
			continue
		}
		return RenderedPhysicalPlan{}, fmt.Errorf("unsupported graph root operation %d (%s)", pathStart, operation.Kind)
	}
	for index := pathStart; index < len(plan.Operations)-1; index++ {
		op := plan.Operations[index]
		var rendered []string
		var err error
		switch op.Kind {
		case ir.PhysicalPathSeedOp:
			rendered, err = r.renderPathSeed(*op.PathSeed)
		case ir.PhysicalPathExtendOp:
			rendered, err = r.renderPathExtend(*op.PathExtend, index)
		default:
			return RenderedPhysicalPlan{}, fmt.Errorf("unsupported graph path operation %d (%s)", index, op.Kind)
		}
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render graph path operation %d: %w", index, err)
		}
		inner = append(inner, rendered...)
	}
	rowFields := make([]string, len(last.GraphReturn.PathSets))
	for i, set := range last.GraphReturn.PathSets {
		rowFields[i] = fmt.Sprintf("s%d: %s", i, set)
	}
	inner = append(inner, "  RETURN { "+strings.Join(rowFields, ", ")+" }", ")")
	rows := r.navigation().newInternalVariable("graph_rows")
	lines := []string{fmt.Sprintf("LET %s = (", rows)}
	for _, line := range inner {
		lines = append(lines, "  "+line)
	}
	result, err := r.renderGraphReturn(*last.GraphReturn, rows)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render graph return: %w", err)
	}
	lines = append(lines, result...)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}
