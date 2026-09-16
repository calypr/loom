package recipe

import (
	"fmt"
	"regexp"
	"strings"

	fhir "github.com/calypr/loom/generated/fhir"
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

func (v FilterValue) Validate() error {
	if !v.Kind.Valid() {
		return fmt.Errorf("unknown value kind %q", v.Kind)
	}
	populated := 0
	for _, present := range []bool{v.String != nil, v.Code != nil, v.Boolean != nil, v.Integer != nil, v.Decimal != nil, v.Date != nil, v.DateTime != nil} {
		if present {
			populated++
		}
	}
	if populated != 1 {
		return fmt.Errorf("exactly one typed value member is required, got %d", populated)
	}
	switch v.Kind {
	case FilterString:
		if v.String == nil {
			return fmt.Errorf("STRING requires string")
		}
	case FilterCode:
		if v.Code == nil || strings.TrimSpace(v.Code.Code) == "" {
			return fmt.Errorf("CODE requires a non-empty code")
		}
	case FilterBoolean:
		if v.Boolean == nil {
			return fmt.Errorf("BOOLEAN requires boolean")
		}
	case FilterInteger:
		if v.Integer == nil {
			return fmt.Errorf("INTEGER requires integer")
		}
	case FilterDecimal:
		if v.Decimal == nil {
			return fmt.Errorf("DECIMAL requires decimal")
		}
	case FilterDate:
		if v.Date == nil {
			return fmt.Errorf("DATE requires date")
		}
		if err := fhir.ValidateFhirDate(*v.Date); err != nil {
			return fmt.Errorf("DATE must use a valid FHIR date: %w", err)
		}
	case FilterDateTime:
		if v.DateTime == nil {
			return fmt.Errorf("DATE_TIME requires dateTime")
		}
		if err := fhir.ValidateFhirDateTime(*v.DateTime); err != nil {
			return fmt.Errorf("DATE_TIME must use a valid FHIR date-time: %w", err)
		}
	}
	return nil
}

func (f Filter) Validate() error { return f.validateAt("$") }

func (f Filter) validateAt(path string) error {
	if strings.TrimSpace(f.Select) == "" {
		return validationError("required", path+".select", "select is required")
	}
	if !f.Operator.Valid() {
		return validationError("invalid_filter_operator", path+".operator", fmt.Sprintf("unsupported operator %q", f.Operator))
	}
	if f.Quantifier != "" && !f.Quantifier.Valid() {
		return validationError("invalid_filter_quantifier", path+".quantifier", fmt.Sprintf("unsupported quantifier %q", f.Quantifier))
	}
	if f.Operator == FilterExists || f.Operator == FilterMissing {
		if len(f.Values) != 0 {
			return validationError("invalid_filter_values", path+".values", "EXISTS and MISSING do not accept values")
		}
	} else if f.Operator == FilterIn {
		if len(f.Values) == 0 {
			return validationError("invalid_filter_values", path+".values", "IN requires at least one value")
		}
	} else if len(f.Values) != 1 {
		return validationError("invalid_filter_values", path+".values", "operator requires exactly one value")
	}
	var kind FilterValueKind
	for i, value := range f.Values {
		if err := value.Validate(); err != nil {
			return validationError("invalid_filter_value", fmt.Sprintf("%s.values[%d]", path, i), err.Error())
		}
		if i == 0 {
			kind = value.Kind
		} else if value.Kind != kind {
			return validationError("invalid_filter_value", fmt.Sprintf("%s.values[%d].kind", path, i), "all values must use the same kind")
		}
		if !filterOperatorSupportsKind(f.Operator, value.Kind) {
			return validationError("invalid_filter_value", fmt.Sprintf("%s.values[%d].kind", path, i), fmt.Sprintf("operator %s is incompatible with %s", f.Operator, value.Kind))
		}
	}
	return nil
}

func filterOperatorSupportsKind(op FilterOperator, kind FilterValueKind) bool {
	switch op {
	case FilterExists, FilterMissing, FilterEquals, FilterNotEquals, FilterIn:
		return kind.Valid()
	case FilterContains:
		return kind == FilterString
	case FilterGreaterThan, FilterGreaterEq, FilterLessThan, FilterLessEq:
		return kind == FilterInteger || kind == FilterDecimal || kind == FilterDate || kind == FilterDateTime
	default:
		return false
	}
}

func validateRichFilterAt(filter Filter, path string) error {
	if err := filter.validateAt(path); err != nil {
		return err
	}
	if filter.Quantifier != "" {
		return validationError("unsupported_filter_quantifier", path+".quantifier", "rich shaping predicates do not support quantifiers")
	}
	switch filter.Operator {
	case FilterExists:
		return nil
	case FilterEquals:
		if len(filter.Values) != 1 || (filter.Values[0].Kind != FilterString && filter.Values[0].Kind != FilterCode) {
			return validationError("unsupported_filter_value", path+".values", "rich shaping equality supports STRING and CODE values")
		}
		return nil
	default:
		return validationError("unsupported_filter_operator", path+".operator", "rich shaping predicates support only EXISTS and EQUALS")
	}
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

func (p Pivot) validateAt(path string, budget *int) error {
	if err := validateRecipeName(p.Name, path+".name"); err != nil {
		return err
	}
	if p.Discovery != nil && len(p.Columns) != 0 {
		return validationError("ambiguous_columns", path, "pivot may use static columns or discovery, not both")
	}
	if p.Discovery == nil && (len(p.Columns) == 0 || len(p.Columns) > maxPivotColumns) {
		return validationError("invalid_columns", path+".columns", fmt.Sprintf("must contain 1..%d columns", maxPivotColumns))
	}
	if p.Discovery != nil {
		if strings.TrimSpace(p.Discovery.Family) == "" && strings.TrimSpace(p.Discovery.Path) == "" {
			return validationError("required", path+".discovery", "family or path is required")
		}
		if p.Discovery.MaxColumns <= 0 || p.Discovery.MaxColumns > maxPivotColumns {
			return validationError("invalid_max_columns", path+".discovery.maxColumns", fmt.Sprintf("must be 1..%d", maxPivotColumns))
		}
	}
	seen := map[string]bool{}
	for i, column := range p.Columns {
		if strings.TrimSpace(column) == "" {
			return validationError("required", fmt.Sprintf("%s.columns[%d]", path, i), "pivot column is required")
		}
		if seen[column] {
			return validationError("duplicate_name", fmt.Sprintf("%s.columns[%d]", path, i), "duplicate pivot column")
		}
		seen[column] = true
	}
	if !ValidPivotProjectionMode(p.ProjectionMode) {
		return validationError("invalid_projection_mode", path+".projectionMode", fmt.Sprintf("unsupported projection mode %q", p.ProjectionMode))
	}
	aliases := map[string]string{}
	for key, alias := range p.ColumnAliases {
		if !seen[key] {
			return validationError("unknown_column_alias", path+".columnAliases", fmt.Sprintf("alias key %q is not a pivot column", key))
		}
		if err := validateRecipeName(alias, path+".columnAliases."+key); err != nil {
			return err
		}
		if previous, exists := aliases[alias]; exists && previous != key {
			return validationError("duplicate_column_alias", path+".columnAliases", fmt.Sprintf("columns %q and %q share output alias %q", previous, key, alias))
		}
		aliases[alias] = key
	}
	if p.Correlation != nil && p.ExtensionCorrelation != nil {
		return validationError("ambiguous_correlation", path, "pivot cannot carry both terminology and extension correlation")
	}
	if p.Correlation != nil {
		if p.Discovery != nil {
			return validationError("ambiguous_correlation", path, "correlated pivot cannot use discovery")
		}
		if strings.TrimSpace(p.CorrelationSystem) == "" || strings.TrimSpace(p.CorrelationCode) == "" {
			return validationError("invalid_correlation", path+".correlation", "selected system and code are required")
		}
		if p.ItemResourceType == "" && p.ColumnExpr.zero() && p.ValueExpr.zero() && p.ItemSource.zero() && len(p.ValueFallbacks) == 0 {
			// Correlated pivots are a closed binding variant. Their selectors are
			// validated from Correlation and are deliberately not duplicated as
			// writable legacy expressions.
			return nil
		}
		return validationError("ambiguous_correlation", path, "correlated pivot must not carry legacy selector expressions")
	}
	if p.ExtensionCorrelation != nil {
		if p.Discovery != nil {
			return validationError("ambiguous_correlation", path, "extension-correlated pivot cannot use discovery")
		}
		if p.Correlation != nil || strings.TrimSpace(p.CorrelationSystem) != "" || strings.TrimSpace(p.CorrelationCode) != "" {
			return validationError("ambiguous_correlation", path, "extension-correlated pivot must not carry terminology correlation fields")
		}
		if p.ItemResourceType != "" || !p.ColumnExpr.zero() || !p.ValueExpr.zero() || !p.ItemSource.zero() || len(p.ValueFallbacks) != 0 {
			return validationError("ambiguous_correlation", path, "extension-correlated pivot must not carry legacy selector expressions")
		}
		return nil
	}
	// A catalog-backed pivot may omit selectors: the scoped resolver fills them
	// from the catalog's validated pivot metadata before semantic compilation.
	if p.Discovery != nil && p.ColumnExpr.Select == "" && p.ValueExpr.Select == "" && p.ColumnExpr.Call == "" && p.ValueExpr.Call == "" {
		return nil
	}
	if err := validateSelectorExpression(p.ColumnExpr, path+".columnExpr"); err != nil {
		return err
	}
	if err := validateSelectorExpression(p.ValueExpr, path+".valueExpr"); err != nil {
		return err
	}
	if err := validateExpressionBudget(p.ColumnExpr, path+".columnExpr", budget); err != nil {
		return err
	}
	if err := validateExpressionBudget(p.ValueExpr, path+".valueExpr", budget); err != nil {
		return err
	}
	if !p.ItemSource.zero() {
		if err := validateExpressionBudget(p.ItemSource, path+".itemSource", budget); err != nil {
			return err
		}
	}
	for index, fallback := range p.ValueFallbacks {
		if err := validateExpressionBudget(fallback, fmt.Sprintf("%s.valueFallbacks[%d]", path, index), budget); err != nil {
			return err
		}
	}
	return nil
}

func (p CatalogProjection) validateAt(path string) error {
	if err := validateRecipeName(p.Name, path+".name"); err != nil {
		return err
	}
	if len(p.IncludePaths) == 0 {
		return validationError("required", path+".includePaths", "at least one path pattern is required")
	}
	if p.MaxColumns <= 0 || p.MaxColumns > maxCatalogProjectionColumns {
		return validationError("invalid_max_columns", path+".maxColumns", fmt.Sprintf("must be 1..%d", maxCatalogProjectionColumns))
	}
	if p.Naming != "" && p.Naming != ColumnNamingPath && p.Naming != ColumnNamingPathSuffix {
		return validationError("invalid_naming", path+".naming", "must be PATH or PATH_SUFFIX")
	}
	if !p.ValueMode.Valid() {
		return validationError("invalid_value_mode", path+".valueMode", fmt.Sprintf("unsupported value mode %q", p.ValueMode))
	}
	seen := map[string]struct{}{}
	for i, kind := range p.Kinds {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			return validationError("required", fmt.Sprintf("%s.kinds[%d]", path, i), "kind is required")
		}
		if _, ok := seen[kind]; ok {
			return validationError("duplicate_name", fmt.Sprintf("%s.kinds[%d]", path, i), "duplicate kind")
		}
		seen[kind] = struct{}{}
	}
	return nil
}

// Validate checks a standalone catalog projection before it is attached to an
// output or traversal. Bundle validation uses the path-aware variant so
// callers still receive the precise location of malformed declarations.
func (p CatalogProjection) Validate() error {
	return p.validateAt("catalogProjection")
}

func (a Aggregate) validateAt(path string, budget *int) error {
	if err := validateRecipeName(a.Name, path+".name"); err != nil {
		return err
	}
	if a.OutputName != "" {
		if err := validateRecipeName(a.OutputName, path+".outputName"); err != nil {
			return err
		}
	}
	if !a.Operation.Valid() {
		return validationError("invalid_aggregate_operation", path+".operation", fmt.Sprintf("unsupported operation %q", a.Operation))
	}
	if !a.ValueMode.Valid() {
		return validationError("invalid_value_mode", path+".valueMode", fmt.Sprintf("unsupported value mode %q", a.ValueMode))
	}
	if a.ValueMode != "" && a.ValueMode != ValueModeAuto {
		return validationError("unsupported_value_mode", path+".valueMode", "aggregate valueMode must be AUTO")
	}
	requiresExpr := a.Operation == AggregateCountDistinct || a.Operation == AggregateDistinctValues || a.Operation == AggregateMin || a.Operation == AggregateMax || a.Operation == AggregateContainsAll
	if requiresExpr && a.Expr == nil {
		return validationError("required", path+".expr", "operation requires expr")
	}
	if !requiresExpr && a.Expr != nil {
		return validationError("invalid_expr", path+".expr", "COUNT and EXISTS do not accept expr")
	}
	if a.Expr != nil {
		if err := validateSelectorExpression(*a.Expr, path+".expr"); err != nil {
			return err
		}
		if err := validateExpressionBudget(*a.Expr, path+".expr", budget); err != nil {
			return err
		}
	}
	if a.Where != nil {
		if err := validateRichFilterAt(*a.Where, path+".where"); err != nil {
			return err
		}
	}
	if a.Operation == AggregateContainsAll {
		if len(a.RequiredValues) == 0 {
			return validationError("required", path+".requiredValues", "CONTAINS_ALL requires at least one required value")
		}
		seen := map[string]bool{}
		for index, value := range a.RequiredValues {
			if strings.TrimSpace(value) == "" {
				return validationError("required", fmt.Sprintf("%s.requiredValues[%d]", path, index), "required value must not be empty")
			}
			if seen[value] {
				return validationError("duplicate_name", fmt.Sprintf("%s.requiredValues[%d]", path, index), "duplicate required value")
			}
			seen[value] = true
		}
	} else if len(a.RequiredValues) != 0 {
		return validationError("invalid_required_values", path+".requiredValues", "requiredValues is only valid for CONTAINS_ALL")
	}
	return nil
}

func (s RepresentativeSlice) validateAt(path string, budget *int) error {
	if err := validateRecipeName(s.Name, path+".name"); err != nil {
		return err
	}
	if s.Limit <= 0 || s.Limit > maxRepresentativeSliceLimit {
		return validationError("invalid_limit", path+".limit", fmt.Sprintf("must be between 1 and %d", maxRepresentativeSliceLimit))
	}
	if len(s.Fields) == 0 {
		return validationError("required", path+".fields", "at least one field is required")
	}
	if s.Where != nil {
		if err := validateRichFilterAt(*s.Where, path+".where"); err != nil {
			return err
		}
	}
	for index, field := range s.Fields {
		fieldPath := fmt.Sprintf("%s.fields[%d]", path, index)
		if err := validateSelectorExpression(field.Expr, fieldPath+".expr"); err != nil {
			return err
		}
	}
	return validateFieldsBudget(s.Fields, path+".fields", budget)
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

func validateNodeShape(fields []Field, filters []Filter, pivots []Pivot, aggregates []Aggregate, slices []RepresentativeSlice, path string, budget *int) error {
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
