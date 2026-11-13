package postgres

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func (r *planRenderer) renderFilter(plan ir.PhysicalPlan, filter ir.PhysicalFilter) (string, error) {
	if err := r.rejectOptionalRowReferences(filter); err != nil {
		return "", err
	}
	if filter.Expression != nil {
		if filter.Expression.Kind == ir.PhysicalExistsPredicate {
			if isRelatedExistsSubplan(filter.Expression.Exists) {
				return r.renderRelatedExists(plan, *filter.Expression.Exists)
			}
			fragment, err := r.renderRequiredMatchExists(plan, *filter.Expression)
			if err != nil {
				return "", err
			}
			return fragment.sql, nil
		}
		if filter.Expression.Kind == ir.PhysicalNotPredicate && len(filter.Expression.Children) == 1 &&
			filter.Expression.Children[0].Kind == ir.PhysicalExistsPredicate &&
			isRelatedExistsSubplan(filter.Expression.Children[0].Exists) {
			clause, err := r.renderRelatedExists(plan, *filter.Expression.Children[0].Exists)
			if err != nil {
				return "", err
			}
			return "NOT (" + clause + ")", nil
		}
		return r.renderPredicateExpression(plan, *filter.Expression)
	}
	return r.renderComparison(plan, filter.Predicate)
}

func (r *planRenderer) renderPredicateExpression(plan ir.PhysicalPlan, predicate ir.PhysicalPredicateExpression) (string, error) {
	fragment, err := r.renderPredicateFragment(plan, predicate)
	if err != nil {
		return "", err
	}
	return fragment.sql, nil
}

type predicateFragment struct {
	sql          string
	mayYieldNull bool
}

func (r *planRenderer) renderPredicateFragment(plan ir.PhysicalPlan, predicate ir.PhysicalPredicateExpression) (predicateFragment, error) {
	switch predicate.Kind {
	case ir.PhysicalComparisonPredicate:
		if predicate.Comparison == nil {
			return predicateFragment{}, fmt.Errorf("%w: COMPARISON predicate has no payload", ErrUnsupportedPostgresPlan)
		}
		return r.renderComparisonFragment(plan, *predicate.Comparison)
	case ir.PhysicalNotPredicate:
		if len(predicate.Children) != 1 {
			return predicateFragment{}, fmt.Errorf("%w: NOT requires exactly one child", ErrUnsupportedPostgresPlan)
		}
		child, err := r.renderPredicateFragment(plan, predicate.Children[0])
		if err != nil {
			return predicateFragment{}, err
		}
		if child.mayYieldNull {
			return predicateFragment{sql: "COALESCE(NOT (" + child.sql + "), true)"}, nil
		}
		return predicateFragment{sql: "NOT (" + child.sql + ")"}, nil
	case ir.PhysicalAllPredicate:
		if len(predicate.Children) == 0 {
			return predicateFragment{}, fmt.Errorf("%w: ALL requires at least one child", ErrUnsupportedPostgresPlan)
		}
		parts := make([]string, 0, len(predicate.Children))
		mayYieldNull := false
		for _, child := range predicate.Children {
			part, err := r.renderPredicateFragment(plan, child)
			if err != nil {
				return predicateFragment{}, err
			}
			mayYieldNull = mayYieldNull || part.mayYieldNull
			parts = append(parts, "("+part.sql+")")
		}
		return predicateFragment{sql: strings.Join(parts, " AND "), mayYieldNull: mayYieldNull}, nil
	case ir.PhysicalAnyPredicate:
		if len(predicate.Children) == 0 {
			return predicateFragment{}, fmt.Errorf("%w: ANY requires at least one child", ErrUnsupportedPostgresPlan)
		}
		parts := make([]string, 0, len(predicate.Children))
		mayYieldNull := false
		for _, child := range predicate.Children {
			part, err := r.renderPredicateFragment(plan, child)
			if err != nil {
				return predicateFragment{}, err
			}
			mayYieldNull = mayYieldNull || part.mayYieldNull
			parts = append(parts, "("+part.sql+")")
		}
		return predicateFragment{sql: strings.Join(parts, " OR "), mayYieldNull: mayYieldNull}, nil
	default:
		return predicateFragment{}, fmt.Errorf("%w: predicate kind %q is not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan, string(predicate.Kind))
	}
}

func (r *planRenderer) renderComparison(plan ir.PhysicalPlan, predicate ir.PhysicalPredicate) (string, error) {
	fragment, err := r.renderComparisonFragment(plan, predicate)
	if err != nil {
		return "", err
	}
	return fragment.sql, nil
}

func (r *planRenderer) renderComparisonFragment(plan ir.PhysicalPlan, predicate ir.PhysicalPredicate) (predicateFragment, error) {
	if predicate.Correlation != nil {
		return predicateFragment{}, fmt.Errorf("%w: correlated predicates are not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	}
	if predicate.Left.BindKey != "" {
		return predicateFragment{}, fmt.Errorf("%w: filter left side must be a row path, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if predicate.LeftExpression != nil {
		return r.renderSelectorComparisonFragment(plan, predicate)
	}
	if predicate.Quantifier != "" {
		return predicateFragment{}, fmt.Errorf("%w: array quantifier %q is not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan, string(predicate.Quantifier))
	}
	left, err := r.resolveValue(predicate.Left)
	if err != nil {
		return predicateFragment{}, err
	}
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	if err := validateDirectOperatorKind(operator, predicate.ValueKind); err != nil {
		return predicateFragment{}, err
	}
	switch operator {
	case "EXISTS", "MISSING":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: %s must not carry a right value", ErrUnsupportedPostgresPlan, operator)
		}
		if isPayloadExpression(left) {
			presence := "COALESCE(jsonb_typeof(" + left + "), 'null')"
			if operator == "EXISTS" {
				return predicateFragment{sql: presence + " <> 'null'"}, nil
			}
			return predicateFragment{sql: presence + " = 'null'"}, nil
		}
		if operator == "EXISTS" {
			return predicateFragment{sql: left + " IS NOT NULL"}, nil
		}
		return predicateFragment{sql: left + " IS NULL"}, nil
	case "EQUALS", "NOT_EQUALS", "GT", "GTE", "LT", "LTE", "IN":
	default:
		return predicateFragment{}, fmt.Errorf("%w: filter operator %q is not part of the v1 PostgreSQL surface",
			ErrUnsupportedPostgresPlan, predicate.Operator)
	}
	if predicate.Right == nil || predicate.Right.BindKey == "" {
		return predicateFragment{}, fmt.Errorf("%w: %s requires a bind-backed right value", ErrUnsupportedPostgresPlan, operator)
	}
	value, ok := plan.BindVars[predicate.Right.BindKey]
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: filter value bind %q is not defined", ErrUnsupportedPostgresPlan, predicate.Right.BindKey)
	}
	if !isPayloadExpression(left) {
		return r.renderColumnComparison(left, operator, predicate.ValueKind, value)
	}
	return r.renderPayloadComparison(left, operator, predicate.ValueKind, value)
}

func validateDirectOperatorKind(operator string, kind spec.FilterValueKind) error {
	switch operator {
	case "GT", "GTE", "LT", "LTE":
		if kind != "" && !spec.OperatorSupportsKind(spec.FilterOperator(operator), kind) {
			return fmt.Errorf("%w: operator %s is not compatible with value kind %q",
				ErrUnsupportedPostgresPlan, operator, string(kind))
		}
	}
	return nil
}

func isPayloadExpression(expression string) bool { return strings.Contains(expression, " #> $") }

func (r *planRenderer) renderColumnComparison(left, operator string, kind spec.FilterValueKind, value any) (predicateFragment, error) {
	isRecordedAt := strings.HasSuffix(left, "."+r.storage.Resource.MustColumn("recorded_at").SQLText())
	if operator == "IN" {
		paths, ok := value.([]string)
		if !ok {
			return predicateFragment{}, fmt.Errorf("%w: column IN requires a []string bind value", ErrUnsupportedPostgresPlan)
		}
		raw, err := json.Marshal(append([]string(nil), paths...))
		if err != nil {
			return predicateFragment{}, fmt.Errorf("%w: column IN values are not JSON-encodable: %v", ErrUnsupportedPostgresPlan, err)
		}
		if string(raw) == "null" {
			raw = []byte("[]")
		}
		return predicateFragment{sql: left + " = ANY (SELECT jsonb_array_elements_text($" + strconv.Itoa(r.jsonbParameter(raw)) + "::jsonb))"}, nil
	}
	if isRecordedAt {
		if kind != "" && kind != spec.FilterDate && kind != spec.FilterDateTime {
			return predicateFragment{}, fmt.Errorf("%w: value kind %q has no registered recorded-at comparison; that column admits only temporal comparisons",
				ErrUnsupportedPostgresPlan, string(kind))
		}
		timestamp, err := temporalComparisonValue(kind, value)
		if err != nil {
			return predicateFragment{}, err
		}
		index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterTimestamptz, Value: timestamp}, "")
		return predicateFragment{sql: left + " " + sqlComparison(operator) + " $" + strconv.Itoa(index)}, nil
	}
	if kind != "" && kind != spec.FilterString && kind != spec.FilterCode {
		return predicateFragment{}, fmt.Errorf("%w: value kind %q has no registered typed-column comparison; text columns accept STRING/CODE",
			ErrUnsupportedPostgresPlan, string(kind))
	}
	text, ok := value.(string)
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: typed column comparisons require a string bind value, got %T",
			ErrUnsupportedPostgresPlan, value)
	}
	return predicateFragment{sql: left + " " + sqlComparison(operator) + " $" + strconv.Itoa(r.textParameter(text, "text:"+text))}, nil
}

func temporalComparisonValue(kind spec.FilterValueKind, value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		if kind == "" || kind == spec.FilterDate || kind == spec.FilterDateTime {
			return typed.UTC(), nil
		}
		return time.Time{}, fmt.Errorf("%w: value kind %q cannot bind a timestamp", ErrUnsupportedPostgresPlan, string(kind))
	case string:
		if strings.Contains(typed, ",") {
			return time.Time{}, fmt.Errorf("%w: comparison literal %q uses a comma decimal, which the PostgreSQL renderer refuses (AQL DATE_TIMESTAMP folds comma fractions to NaN and never matches)",
				ErrUnsupportedPostgresPlan, typed)
		}
		if kind == spec.FilterDate {
			parsed, err := time.Parse("2006-01-02", typed)
			if err != nil {
				return time.Time{}, fmt.Errorf("%w: DATE comparison literal %q is not a full-precision YYYY-MM-DD date",
					ErrUnsupportedPostgresPlan, typed)
			}
			return parsed.UTC(), nil
		}
		if kind == spec.FilterDateTime || kind == "" {
			parsed, err := time.Parse(time.RFC3339Nano, typed)
			if err != nil {
				return time.Time{}, fmt.Errorf("%w: DATE_TIME comparison literal %q is not a full RFC3339 timestamp",
					ErrUnsupportedPostgresPlan, typed)
			}
			return parsed.UTC(), nil
		}
		return time.Time{}, fmt.Errorf("%w: value kind %q cannot bind a timestamp", ErrUnsupportedPostgresPlan, string(kind))
	default:
		return time.Time{}, fmt.Errorf("%w: recorded_at comparisons require a time.Time or full-precision temporal string bind value, got %T",
			ErrUnsupportedPostgresPlan, value)
	}
}

func (r *planRenderer) renderPayloadComparison(left, operator string, kind spec.FilterValueKind, value any) (predicateFragment, error) {
	if operator == "IN" {
		return predicateFragment{}, fmt.Errorf("%w: payload-path IN is not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if err := validatePayloadBindType(kind, value); err != nil {
		return predicateFragment{}, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return predicateFragment{}, fmt.Errorf("%w: filter value %T is not JSON-encodable: %v", ErrUnsupportedPostgresPlan, value, err)
	}
	if string(raw) == "null" {
		presence := "COALESCE(jsonb_typeof(" + left + "), 'null')"
		switch operator {
		case "EQUALS", "LTE":
			return predicateFragment{sql: presence + " = 'null'"}, nil
		case "NOT_EQUALS", "GT":
			return predicateFragment{sql: presence + " <> 'null'"}, nil
		case "LT", "GTE":
			return predicateFragment{}, fmt.Errorf("%w: payload ordering %s against a JSON null literal is not part of the v1 PostgreSQL surface",
				ErrUnsupportedPostgresPlan, operator)
		default:
			return predicateFragment{}, fmt.Errorf("%w: payload operator %q against a JSON null literal is not part of the v1 PostgreSQL surface",
				ErrUnsupportedPostgresPlan, operator)
		}
	}
	if kind == spec.FilterDate || kind == spec.FilterDateTime {
		switch operator {
		case "GT", "GTE", "LT", "LTE":
			return r.renderTemporalPayloadComparison(left, operator, kind, value)
		}
	}
	if isNumericOrdering(operator, kind) {
		index := r.jsonbParameter(raw)
		return predicateFragment{sql: "(jsonb_typeof((" + left + ")) = 'number' AND (" + left + ") " +
			sqlComparison(operator) + " ($" + strconv.Itoa(index) + "::jsonb))", mayYieldNull: true}, nil
	}
	index := r.jsonbParameter(raw)
	if operator == "NOT_EQUALS" {
		return predicateFragment{sql: "(" + left + ") IS DISTINCT FROM ($" + strconv.Itoa(index) + "::jsonb)"}, nil
	}
	return predicateFragment{sql: "(" + left + ") " + sqlComparison(operator) + " ($" + strconv.Itoa(index) + "::jsonb)", mayYieldNull: true}, nil
}

func validatePayloadBindType(kind spec.FilterValueKind, value any) error {
	if kind == "" {
		switch value.(type) {
		case string, bool, int, int64, float64, json.RawMessage:
			return nil
		default:
			return fmt.Errorf("%w: payload comparisons accept string, bool, number, or raw JSON values, got %T",
				ErrUnsupportedPostgresPlan, value)
		}
	}
	switch kind {
	case spec.FilterString, spec.FilterCode, spec.FilterDate, spec.FilterDateTime:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%w: value kind %q requires a string bind value, got %T", ErrUnsupportedPostgresPlan, string(kind), value)
		}
		return nil
	case spec.FilterBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%w: value kind BOOLEAN requires a bool bind value, got %T", ErrUnsupportedPostgresPlan, value)
		}
		return nil
	case spec.FilterInteger:
		if _, ok := value.(int64); !ok {
			return fmt.Errorf("%w: value kind INTEGER requires an int64 bind value, got %T", ErrUnsupportedPostgresPlan, value)
		}
		return nil
	case spec.FilterDecimal:
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%w: value kind DECIMAL requires a float64 bind value, got %T", ErrUnsupportedPostgresPlan, value)
		}
		return nil
	default:
		return fmt.Errorf("%w: value kind %q is not registered for PostgreSQL", ErrUnsupportedPostgresPlan, string(kind))
	}
}

const (
	temporalLeapTensOnesPattern    = `(0[48]|[2468][048]|[13579][26])`
	temporalStoredYearPattern      = `[1-9][0-9]{3}`
	temporalStoredDateCorePattern  = `([1-9][0-9]{3}-(0[13578]|1[02])-(0[1-9]|[12][0-9]|3[01])|` + temporalStoredYearPattern + `-(0[469]|11)-(0[1-9]|[12][0-9]|30)|` + temporalStoredYearPattern + `-02-(0[1-9]|1[0-9]|2[0-8])|` + temporalLeapTensOnesPattern + `00-02-29|[0-9]{2}` + temporalLeapTensOnesPattern + `-02-29)`
	temporalStoredTimePattern      = `(0[0-9]|1[0-9]|2[0-3]):[0-5][0-9](:[0-5][0-9]([.][0-9]+)?)?`
	temporalStoredOffsetPattern    = `(Z|[+-]((0[0-9]|1[0-4]):[0-5][0-9]|15:[0-5][0-9]))`
	temporalStoredDatePattern      = `^` + temporalStoredDateCorePattern + `$`
	temporalStoredInstantPattern   = `^` + temporalStoredDateCorePattern + `T` + temporalStoredTimePattern + temporalStoredOffsetPattern + `$`
	temporalStoredNoOffsetPattern  = `^` + temporalStoredDateCorePattern + `T` + temporalStoredTimePattern + `$`
	temporalStoredComparisonSuffix = `T00:00:00+00:00`
)

func temporalStoredExpression(extraction string) string {
	text := "((" + extraction + ") #>> '{}')"
	return "CASE" +
		" WHEN jsonb_typeof(" + extraction + ") = 'string' AND " + text + " ~ '" + temporalStoredDatePattern + "'" +
		" THEN (" + text + " || '" + temporalStoredComparisonSuffix + "')::timestamptz" +
		" WHEN jsonb_typeof(" + extraction + ") = 'string' AND " + text + " ~ '" + temporalStoredInstantPattern + "'" +
		" THEN " + text + "::timestamptz" +
		" WHEN jsonb_typeof(" + extraction + ") = 'string' AND " + text + " ~ '" + temporalStoredNoOffsetPattern + "'" +
		" THEN (" + text + " || '+00:00')::timestamptz" +
		" END"
}

func (r *planRenderer) renderTemporalPayloadComparison(left, operator string, kind spec.FilterValueKind, value any) (predicateFragment, error) {
	timestamp, err := temporalComparisonValue(kind, value)
	if err != nil {
		return predicateFragment{}, err
	}
	index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterTimestamptz, Value: timestamp}, "")
	return predicateFragment{
		sql:          "(" + temporalStoredExpression(left) + ") " + sqlComparison(operator) + " $" + strconv.Itoa(index),
		mayYieldNull: true,
	}, nil
}

func sqlComparison(operator string) string {
	switch operator {
	case "EQUALS":
		return "="
	case "NOT_EQUALS":
		return "IS DISTINCT FROM"
	case "GT":
		return ">"
	case "GTE":
		return ">="
	case "LT":
		return "<"
	case "LTE":
		return "<="
	default:
		return ""
	}
}

func (r *planRenderer) renderSelectorComparisonFragment(plan ir.PhysicalPlan, predicate ir.PhysicalPredicate) (predicateFragment, error) {
	expression := predicate.LeftExpression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Cardinality != ir.PhysicalArrayCardinality {
		return predicateFragment{}, fmt.Errorf("%w: selector filter left side must be an ARRAY-cardinality EXTRACT expression",
			ErrUnsupportedPostgresPlan)
	}
	if expression.NullBehavior != ir.PhysicalEmptyOnNull {
		return predicateFragment{}, fmt.Errorf("%w: selector filter extraction null behavior %q has no registered comparison form",
			ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	extract := expression.Extract
	if extract == nil || len(extract.Fallbacks) != 0 || extract.Prepared != nil ||
		extract.UnitNormalization != nil || extract.Distinct || extract.Selector.Filter != nil {
		return predicateFragment{}, fmt.Errorf("%w: selector filter extraction carries unregistered payload (fallbacks, prepared sets, unit normalization, DISTINCT, or selector filters are gaps)",
			ErrUnsupportedPostgresPlan)
	}
	switch extract.ExecutionMode {
	case ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar, "":
	default:
		return predicateFragment{}, fmt.Errorf("%w: selector filter execution mode %q has no registered comparison form",
			ErrUnsupportedPostgresPlan, string(extract.ExecutionMode))
	}
	source := extract.Source
	if source.BindKey != "" || source.Variable == "" || len(source.Path) != 1 || source.Path[0] != "payload" {
		return predicateFragment{}, fmt.Errorf("%w: selector filter source must be one readable row's document payload",
			ErrUnsupportedPostgresPlan)
	}
	scope, ok := r.rows[source.Variable]
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: selector filter source variable %q does not bind a readable row",
			ErrUnsupportedPostgresPlan, source.Variable)
	}
	if strings.TrimSpace(extract.ResourceType) == "" || extract.ResourceType != scope.resourceType {
		return predicateFragment{}, fmt.Errorf("%w: selector filter resource type %q contradicts the scanned row type %q",
			ErrUnsupportedPostgresPlan, extract.ResourceType, scope.resourceType)
	}
	if predicate.Left.Variable != "" || len(predicate.Left.Path) != 0 {
		return predicateFragment{}, fmt.Errorf("%w: selector filter predicate cannot also declare an ordinary left value",
			ErrUnsupportedPostgresPlan)
	}
	if predicate.Right != nil && predicate.Right.BindKey == "" {
		return predicateFragment{}, fmt.Errorf("%w: selector filter right value must be bind-backed", ErrUnsupportedPostgresPlan)
	}
	if predicate.Right != nil && (predicate.Right.Variable != "" || len(predicate.Right.Path) != 0) {
		return predicateFragment{}, fmt.Errorf("%w: selector filter right value must be a bind, not a row path", ErrUnsupportedPostgresPlan)
	}

	path := make([]string, 0, len(extract.Selector.Steps))
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return predicateFragment{}, fmt.Errorf("%w: selector filter accepts only direct property paths; repeated, indexed, and filtered steps are gaps",
				ErrUnsupportedPostgresPlan)
		}
		path = append(path, step.Field)
	}
	if len(path) == 0 {
		return predicateFragment{}, fmt.Errorf("%w: selector filter selector must have at least one property", ErrUnsupportedPostgresPlan)
	}
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	if err := validateDirectOperatorKind(operator, predicate.ValueKind); err != nil {
		return predicateFragment{}, err
	}

	extraction, err := r.documentPathExpression(scope.alias, path)
	if err != nil {
		return predicateFragment{}, err
	}
	presence := "COALESCE(jsonb_typeof(" + extraction + "), 'null') <> 'null'"

	switch operator {
	case "EXISTS":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: EXISTS must not carry a right value", ErrUnsupportedPostgresPlan)
		}
		return predicateFragment{sql: presence}, nil
	case "MISSING":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: MISSING must not carry a right value", ErrUnsupportedPostgresPlan)
		}
		return predicateFragment{sql: "NOT (" + presence + ")"}, nil
	}
	if predicate.Right == nil || predicate.Right.BindKey == "" {
		return predicateFragment{}, fmt.Errorf("%w: %s requires a bind-backed right value", ErrUnsupportedPostgresPlan, operator)
	}
	if !predicate.ValueKind.Valid() {
		return predicateFragment{}, fmt.Errorf("%w: selector filter value kind %q is not registered",
			ErrUnsupportedPostgresPlan, string(predicate.ValueKind))
	}
	value, ok := plan.BindVars[predicate.Right.BindKey]
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: filter value bind %q is not defined", ErrUnsupportedPostgresPlan, predicate.Right.BindKey)
	}
	match, err := r.selectorMatchExpression(extraction, operator, predicate.ValueKind, value)
	if err != nil {
		return predicateFragment{}, err
	}
	any_ := "(" + presence + " AND " + match + ")"
	switch predicate.Quantifier {
	case "", spec.QuantifierAny:
		return predicateFragment{sql: any_}, nil
	case spec.QuantifierNone:
		return predicateFragment{sql: "NOT (" + any_ + ")"}, nil
	case spec.QuantifierAll:
		return predicateFragment{sql: any_}, nil
	default:
		return predicateFragment{}, fmt.Errorf("%w: selector filter quantifier %q is not registered",
			ErrUnsupportedPostgresPlan, string(predicate.Quantifier))
	}
}

func (r *planRenderer) selectorMatchExpression(extraction, operator string, kind spec.FilterValueKind, value any) (string, error) {
	if operator == "CONTAINS_TEXT" {
		return "", fmt.Errorf("%w: selector CONTAINS_TEXT is not part of the registered PostgreSQL surface",
			ErrUnsupportedPostgresPlan)
	}
	if operator == "IN" {
		if err := validateSelectorInList(kind, value); err != nil {
			return "", err
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("%w: selector IN values are not JSON-encodable: %v", ErrUnsupportedPostgresPlan, err)
		}
		if string(raw) == "null" {
			raw = []byte("[]")
		}
		index := r.jsonbParameter(raw)
		return "EXISTS (SELECT 1 FROM jsonb_array_elements($" + strconv.Itoa(index) + "::jsonb) AS loom_selector_item WHERE loom_selector_item = (" + extraction + "))", nil
	}
	if err := validatePayloadBindType(kind, value); err != nil {
		return "", err
	}
	if kind == spec.FilterDate || kind == spec.FilterDateTime {
		switch operator {
		case "GT", "GTE", "LT", "LTE":
			timestamp, err := temporalComparisonValue(kind, value)
			if err != nil {
				return "", err
			}
			index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterTimestamptz, Value: timestamp}, "")
			return "COALESCE((" + temporalStoredExpression(extraction) + ") " + sqlComparison(operator) +
				" $" + strconv.Itoa(index) + ", false)", nil
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("%w: filter value %T is not JSON-encodable: %v", ErrUnsupportedPostgresPlan, value, err)
	}
	if string(raw) == "null" {
		return "", fmt.Errorf("%w: selector comparison against a null literal is not part of the registered PostgreSQL surface",
			ErrUnsupportedPostgresPlan)
	}
	comparison := sqlComparison(operator)
	if operator == "NOT_EQUALS" {
		comparison = "<>"
	}
	if isNumericOrdering(operator, kind) {
		return "COALESCE(jsonb_typeof(" + extraction + ") = 'number' AND (" + extraction + ") " +
			comparison + " ($" + strconv.Itoa(r.jsonbParameter(raw)) + "::jsonb), false)", nil
	}
	return "(" + extraction + ") " + comparison + " ($" + strconv.Itoa(r.jsonbParameter(raw)) + "::jsonb)", nil
}

func isNumericOrdering(operator string, kind spec.FilterValueKind) bool {
	switch operator {
	case "GT", "GTE", "LT", "LTE":
		return kind == spec.FilterInteger || kind == spec.FilterDecimal
	}
	return false
}

func validateSelectorInList(kind spec.FilterValueKind, value any) error {
	var elements []any
	switch list := value.(type) {
	case []string:
		if kind != spec.FilterString && kind != spec.FilterCode {
			return fmt.Errorf("%w: selector IN with a []string bind is admitted only for STRING and CODE kinds, got kind %q",
				ErrUnsupportedPostgresPlan, string(kind))
		}
		return nil
	case []any:
		elements = list
	default:
		return fmt.Errorf("%w: selector IN requires a []any or []string bind value, got %T", ErrUnsupportedPostgresPlan, value)
	}
	for _, element := range elements {
		var ok bool
		switch kind {
		case spec.FilterString, spec.FilterCode, spec.FilterDate, spec.FilterDateTime:
			_, ok = element.(string)
		case spec.FilterBoolean:
			_, ok = element.(bool)
		case spec.FilterInteger:
			_, ok = element.(int64)
		case spec.FilterDecimal:
			_, ok = element.(float64)
		default:
			return fmt.Errorf("%w: selector IN value kind %q is not registered", ErrUnsupportedPostgresPlan, string(kind))
		}
		if !ok {
			return fmt.Errorf("%w: selector IN element %T contradicts the declared value kind %q",
				ErrUnsupportedPostgresPlan, element, string(kind))
		}
	}
	return nil
}
