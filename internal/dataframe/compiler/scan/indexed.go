package scan

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func categoryScanIndexedRootQueries(plan ir.PhysicalPlan, category lower.CompiledOutputColumn, index *model.PreviewCoveringIndexSpec, maxValues int) (string, map[string]any, *CategoryOverflowWitness, error) {
	root, sourceReturn, ok := categoryScanRootProjectionSource(plan)
	if !ok || root.Population != nil {
		return "", nil, nil, fmt.Errorf("category index requires one direct root scan")
	}
	var projection *ir.PhysicalProjection
	for position := range sourceReturn.Projections {
		candidate := &sourceReturn.Projections[position]
		if candidate.Name != category.Name {
			continue
		}
		if projection != nil || candidate.Hidden {
			return "", nil, nil, fmt.Errorf("category projection is ambiguous or hidden")
		}
		projection = candidate
	}
	if projection == nil {
		return "", nil, nil, fmt.Errorf("category projection is missing")
	}
	value, direct := ir.DirectScalarRootProjectionValue(*projection, root.Variable)
	if !direct || !categoryScanProjectionPresenceMatches(*projection, root.Variable, value) || !model.ValidPreviewIndexPath(value.Path) {
		return "", nil, nil, fmt.Errorf("category projection has no exact direct scalar presence proof")
	}

	resourceType, err := categoryScanRootResourceType(plan, root, value.Path, index)
	if err != nil {
		return "", nil, nil, err
	}
	indexed := &ir.PhysicalIndexedCategoryScan{RootVariable: root.Variable, ValuePath: append([]string(nil), value.Path...), ResourceType: resourceType}
	if categoryScanUsesStoredQuantityParent(index, value.Path) {
		indexed.StoredParentPath = strings.Split(index.StoredValues[1], ".")
	}
	rendered, err := aql.RenderCategoryScan(plan, ir.PhysicalCategoryScanReturn{CategoryColumn: category.Name, MaxValues: maxValues, RootIndexHint: index.Name, IndexedRoot: indexed})
	if err != nil {
		return "", nil, nil, err
	}
	var witness *CategoryOverflowWitness
	if rendered.OverflowWitness != nil {
		witness = &CategoryOverflowWitness{Query: rendered.OverflowWitness.Query, BindVars: rendered.OverflowWitness.BindVars}
	}
	return rendered.Query, rendered.BindVars, witness, nil
}

func categoryScanUsesStoredQuantityParent(index *model.PreviewCoveringIndexSpec, valuePath []string) bool {
	if index == nil || index.Collection != "Observation" ||
		!reflect.DeepEqual(index.Fields, model.PreviewQuantityCategoryFields()) ||
		!reflect.DeepEqual(index.StoredValues, model.PreviewQuantityCategoryStoredValues()) || len(index.StoredValues) < 2 {
		return false
	}
	parent := strings.Split(index.StoredValues[1], ".")
	return len(valuePath) == len(parent)+1 && spec.PathHasPrefix(valuePath, parent) && valuePath[len(valuePath)-1] == "code"
}

func categoryScanHasExactRootPresencePath(projection ir.PhysicalProjection, rootVariable string, valuePath []string) bool {
	if projection.Presence == nil || !categoryScanProjectionPresenceMatches(projection, rootVariable, ir.PhysicalValue{Variable: rootVariable, Path: valuePath}) {
		return false
	}
	return len(valuePath) == 3 && valuePath[0] == "payload" && valuePath[1] == "valueQuantity" && valuePath[2] == "code"
}

func categoryScanRootResourceType(plan ir.PhysicalPlan, root *ir.PhysicalRootScan, path []string, index *model.PreviewCoveringIndexSpec) (string, error) {
	collection, ok := plan.BindVars[root.CollectionBindKey].(string)
	if !ok || collection == "" || index.Collection != collection {
		return "", fmt.Errorf("category index collection does not match the direct root scan")
	}
	categoryPath := strings.Join(path, ".")
	rootType := strings.TrimSpace(plan.Source.ResourceType)
	if categoryScanUsesStoredQuantityParent(index, path) {
		if rootType != "Observation" || collection != rootType {
			return "", fmt.Errorf("stored quantity category index does not match the exact source collection type")
		}
		return rootType, nil
	}
	if sameCategoryIndexFields(index.Fields, []string{"project", "dataset_generation", categoryPath, "auth_resource_path"}) {
		return "", nil
	}
	if !sameCategoryIndexFields(index.Fields, []string{"project", "dataset_generation", categoryPath}) {
		return "", fmt.Errorf("category index does not match the exact source collection type and path")
	}
	if rootType == "" {
		return "", nil
	}
	if collection != rootType {
		return "", fmt.Errorf("category index does not match the exact source collection type and path")
	}
	return rootType, nil
}

func sameCategoryIndexFields(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
