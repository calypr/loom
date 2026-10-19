package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
)

// RenderPreviewGroupProjectionFactorization renders the exact semantic Group
// alternative after the runtime has selected its root scan. An empty indexName
// leaves the planner choice unchanged; disableIndex requests a full scan.
func RenderPreviewGroupProjectionFactorization(plan ir.PhysicalPlan, indexName string, disableIndex bool) (string, map[string]any, error) {
	rendered, err := aql.RenderPhysicalPlanWithSemanticGroupProjectionFactorization(plan, indexName, disableIndex)
	if err != nil {
		return "", nil, err
	}
	return rendered.Query, rendered.BindVars, nil
}

// RenderPreviewGroupProjectionAssessment returns bounded metadata about the
// exact complete Coding-array projections seen under the plan's source scope.
// Its result contains no source values.
func RenderPreviewGroupProjectionAssessment(plan ir.PhysicalPlan, indexName string, disableIndex bool) (string, map[string]any, error) {
	rendered, err := aql.RenderPhysicalPlanWithSemanticGroupProjectionAssessment(plan, indexName, disableIndex)
	if err != nil {
		return "", nil, err
	}
	return rendered.Query, rendered.BindVars, nil
}

func previewGroupProjectionSpec(plan ir.PhysicalPlan) *PreviewGroupProjectionSpec {
	if err := aql.ValidateSemanticGroupProjectionPreviewPlan(plan); err != nil {
		return nil
	}
	root := plan.Operations[0].RootScan
	collection, ok := plan.BindVars[root.CollectionBindKey].(string)
	if !ok || collection == "" {
		return nil
	}
	scopeCount, err := aql.RenderPhysicalRootScopeCount(plan)
	if err != nil || scopeCount.Query == "" {
		return nil
	}
	binds := ir.ClonePhysicalPlan(ir.PhysicalPlan{BindVars: scopeCount.BindVars}).BindVars
	return &PreviewGroupProjectionSpec{
		Collection: collection, ScopeCountQuery: scopeCount.Query, ScopeCountBindVars: binds,
		ReadOnlyIndex: previewGroupProjectionIndexSpec(plan),
	}
}

func previewGroupProjectionIndexSpec(plan ir.PhysicalPlan) *PreviewGroupProjectionIndexSpec {
	if plan.StageSequence == nil || len(plan.StageSequence.Stages) != 1 || plan.StageSequence.Stages[0].Group == nil {
		return nil
	}
	semantic := plan.StageSequence.Stages[0].Group.SemanticKey
	if semantic == nil || len(semantic.PathSegments) == 0 {
		return nil
	}
	for _, segment := range semantic.PathSegments[:len(semantic.PathSegments)-1] {
		if segment.Repeated {
			return nil
		}
	}
	path := "payload"
	for _, segment := range semantic.PathSegments {
		path += "." + segment.Name
	}
	return &PreviewGroupProjectionIndexSpec{
		RequiredFields: append([]string(nil), previewCoveringIndexPrefixFields...),
		ProjectionPath: path,
	}
}
