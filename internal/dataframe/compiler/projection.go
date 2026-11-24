package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
)

func RenderPreviewGroupProjectionFactorization(plan ir.PhysicalPlan, indexName string, disableIndex bool) (string, map[string]any, error) {
	rendered, err := aql.RenderPhysicalPlanWithSemanticGroupProjectionFactorization(plan, indexName, disableIndex)
	if err != nil {
		return "", nil, err
	}
	return rendered.Query, rendered.BindVars, nil
}

func RenderPreviewGroupProjectionAssessment(plan ir.PhysicalPlan, indexName string, disableIndex bool) (string, map[string]any, error) {
	rendered, err := aql.RenderPhysicalPlanWithSemanticGroupProjectionAssessment(plan, indexName, disableIndex)
	if err != nil {
		return "", nil, err
	}
	return rendered.Query, rendered.BindVars, nil
}

func previewGroupProjectionSpec(plan ir.PhysicalPlan) *model.PreviewGroupProjectionSpec {
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
	return &model.PreviewGroupProjectionSpec{
		Collection: collection, ScopeCountQuery: scopeCount.Query, ScopeCountBindVars: binds,
		ReadOnlyIndex: previewGroupProjectionIndexSpec(plan),
	}
}

func previewGroupProjectionIndexSpec(plan ir.PhysicalPlan) *model.PreviewGroupProjectionIndexSpec {
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
	return &model.PreviewGroupProjectionIndexSpec{
		RequiredFields:       append([]string(nil), previewCoveringIndexPrefixFields...),
		RequiredStoredValues: []string{"auth_resource_path"},
		ProjectionPath:       path,
	}
}
