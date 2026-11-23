package recipe

import (
	"fmt"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	"regexp"
	"strings"
)

const (
	maxRepresentativeSliceLimit = 1000
	maxPivotColumns             = 256
	maxCatalogProjectionColumns = 512
)

var recipeNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateRecipeName(value, path string) error {
	if strings.TrimSpace(value) == "" {
		return validationError("required", path, "name is required")
	}
	if !recipeNamePattern.MatchString(value) {
		return validationError("invalid_name", path, "must contain only letters, digits, and underscores and may not start with a digit")
	}
	return nil
}

func validateSelectorExpression(input Expression, path string) error {
	if strings.HasPrefix(strings.TrimSpace(input.Call), "fragment:") {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(input.Call)) {
	case "reference_id", "path_segment", "basename", "last_segment", "sanitize_name", "sanitize_graphql_name":
		return nil
	}
	if input.Select == "" || input.Call != "" || input.Literal != nil || input.Document != nil || len(input.Args) != 0 {
		return validationError("unsupported_expression", path, "rich shaping requires a selector expression")
	}
	return nil
}

func validateExpressionBudget(e Expression, path string, budget *int) error {
	if err := validateExpression(e, path); err != nil {
		return err
	}
	if budget == nil {
		return nil
	}
	*budget += expressionNodeCount(e)
	if *budget > maxExpressionNodes {
		return validationError("max_nodes", path, "expression node count exceeds limit")
	}
	return nil
}

func expressionNodeCount(e Expression) int {
	count := 1
	for _, arg := range e.Args {
		count += expressionNodeCount(arg)
	}
	return count
}

func validateFieldsBudget(fields []Field, path string, budget *int) error {
	seen := map[string]bool{}
	for i, f := range fields {
		p := fmt.Sprintf("%s[%d]", path, i)
		if err := validateRecipeName(f.Name, p+".name"); err != nil {
			return err
		}
		if seen[f.Name] {
			return validationError("duplicate_name", p+".name", "duplicate field name")
		}
		seen[f.Name] = true
		if err := validateExpressionBudget(f.Expr, p+".expr", budget); err != nil {
			return err
		}
		for j, fallback := range f.Fallbacks {
			fp := fmt.Sprintf("%s.fallbacks[%d]", p, j)
			if fallback.Select == "" || fallback.Call != "" || fallback.Literal != nil {
				return validationError("invalid_fallback", fp, "fallback must be a selector expression")
			}
			if err := validateExpressionBudget(fallback, fp, budget); err != nil {
				return err
			}
		}
		if !f.ValueMode.Valid() {
			return validationError("invalid_value_mode", p+".valueMode", fmt.Sprintf("unsupported value mode %q", f.ValueMode))
		}
	}
	return nil
}

func validateNodeShape(fields []Field, filters []Filter, pivots []Pivot, ownerRecords []OwnerRecordProjection, aggregates []Aggregate, slices []RepresentativeSlice, path string, budget *int) error {
	if err := validateFieldsBudget(fields, path+".fields", budget); err != nil {
		return err
	}
	names := make(map[string]bool, len(fields)+len(pivots)+len(aggregates)+len(slices))
	for _, f := range fields {
		names[f.Name] = true
	}
	check := func(name, p string) error {
		if names[name] {
			return validationError("duplicate_name", p+".name", "name conflicts with another node output")
		}
		names[name] = true
		return nil
	}
	for i, pivot := range pivots {
		p := fmt.Sprintf("%s.pivots[%d]", path, i)
		if err := check(pivot.Name, p); err != nil {
			return err
		}
		if err := pivot.validateAt(p, budget); err != nil {
			return err
		}
	}
	for i, projection := range ownerRecords {
		p := fmt.Sprintf("%s.ownerRecords[%d]", path, i)
		if err := validateRecipeName(projection.Name, p+".name"); err != nil {
			return err
		}
		if err := check(projection.Name, p); err != nil {
			return err
		}
		if projection.Binding.SelectionScope == fhirbinding.CorrelatedSelectionScopeSystemVersion {
			return validationError("invalid_owner_records", p, "owner records does not support SYSTEM_VERSION selection scope")
		}
		if strings.TrimSpace(projection.Binding.OwnerPath) == "" || strings.TrimSpace(projection.Key.System) == "" || strings.TrimSpace(projection.Key.Code) == "" {
			return validationError("invalid_owner_records", p, "binding.ownerPath and key.system/code are required")
		}
	}
	for i, aggregate := range aggregates {
		p := fmt.Sprintf("%s.aggregates[%d]", path, i)
		if err := check(aggregate.Name, p); err != nil {
			return err
		}
		if err := aggregate.validateAt(p, budget); err != nil {
			return err
		}
		finalName := aggregate.Name
		if aggregate.OutputName != "" {
			finalName = aggregate.OutputName
		}
		if finalName != aggregate.Name {
			if names[finalName] {
				return validationError("duplicate_name", p+".outputName", "name conflicts with another node output")
			}
			names[finalName] = true
		}
	}
	for i, slice := range slices {
		p := fmt.Sprintf("%s.slices[%d]", path, i)
		if err := check(slice.Name, p); err != nil {
			return err
		}
		if err := slice.validateAt(p, budget); err != nil {
			return err
		}
	}
	for i, filter := range filters {
		if err := filter.validateAt(fmt.Sprintf("%s.filters[%d]", path, i)); err != nil {
			return err
		}
	}
	return nil
}
