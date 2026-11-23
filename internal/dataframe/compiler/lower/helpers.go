package lower

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const (
	datasetGenerationBindKey = "dataset_generation"
	datasetGenerationField   = "dataset_generation"
)

func datasetGenerationBindValue(generation string) any {
	return ir.DatasetGenerationBindValue(generation)
}

func physicalSelectorFallbacks(source ir.PhysicalValue, resourceType string, selectors []spec.Selector) []ir.PhysicalSelectorFallback {
	fallbacks := make([]ir.PhysicalSelectorFallback, len(selectors))
	for index, selector := range selectors {
		fallbacks[index] = ir.PhysicalSelectorFallback{Source: source, ResourceType: resourceType, Selector: selector}
	}
	return fallbacks
}
