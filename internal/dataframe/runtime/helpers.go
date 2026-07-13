package runtime

import (
	"fmt"
	"strings"
)

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func normalizeDatasetGeneration(generation string) string {
	return strings.TrimSpace(generation)
}

func selectorStepText(step SelectorStep) string {
	switch {
	case step.Iterate:
		return step.Field + "[]"
	case step.Index != nil:
		return fmt.Sprintf("%s[%d]", step.Field, *step.Index)
	default:
		return step.Field
	}
}

func aggregateOperationRequiresSelector(operation string) bool {
	switch strings.ToUpper(strings.TrimSpace(operation)) {
	case "COUNT_DISTINCT", "EXISTS", "DISTINCT_VALUES", "MIN", "MAX":
		return true
	default:
		return false
	}
}

func sanitizeColumnName(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

const (
	datasetGenerationBindKey = "dataset_generation"
	datasetGenerationField   = "dataset_generation"
)

func cloneRowIdentity(in *RowIdentity) *RowIdentity {
	if in == nil {
		return nil
	}
	out := *in
	out.Fields = cloneStrings(in.Fields)
	return &out
}

func isDatasetGenerationScopePredicate(predicate PhysicalPredicate, variable string) bool {
	return predicate.Operator == "EQUALS" &&
		predicate.Left.Variable == variable &&
		len(predicate.Left.Path) == 1 && predicate.Left.Path[0] == "dataset_generation" &&
		predicate.Right != nil && predicate.Right.BindKey == "dataset_generation" &&
		predicate.Right.Variable == "" && len(predicate.Right.Path) == 0
}

type storageRoute = StorageRoute

func resolveStorageRoute(fromType, label, toType string) (storageRoute, error) {
	return ResolveStorageRoute(fromType, label, toType)
}
