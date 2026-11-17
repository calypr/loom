package postgres

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const maxCallExpressionDepth = 64

const maxCallFragmentBytes = 1 << 20

func checkCallFragment(fragment string) error {
	if len(fragment) > maxCallFragmentBytes {
		return fmt.Errorf("%w: call fragment of %d bytes exceeds the %d-byte registered bound (operand replication under nesting)",
			ErrUnsupportedPostgresPlan, len(fragment), maxCallFragmentBytes)
	}
	return nil
}

var registeredCallNames = map[string]bool{
	"add": true, "subtract": true, "multiply": true, "divide": true,
	"cast": true, "coalesce": true, "fallback": true,
	"if": true, "case": true, "not": true, "and": true, "or": true,
	"eq": true, "neq": true, "gt": true, "gte": true, "lt": true, "lte": true,
	"assert": true, "distinct_present_one": true,
}

type callLiteralKind uint8

const (
	callLiteralNot callLiteralKind = iota
	callLiteralNull
	callLiteralNumber
	callLiteralString
	callLiteralBoolean
	callLiteralArray
)

type callOperand struct {
	sql  string
	kind callLiteralKind
}

type callResultFacts struct {
	staticKind callLiteralKind
}

func (r *planRenderer) renderCallProjection(plan ir.PhysicalPlan, projection ir.PhysicalProjection) (string, error) {
	fragment, _, err := r.renderCallExpression(plan, *projection.Expression, 0)
	if err != nil {
		return "", fmt.Errorf("projection %q: %w", projection.Name, err)
	}
	if err := checkCallFragment(fragment); err != nil {
		return "", fmt.Errorf("projection %q: %w", projection.Name, err)
	}
	return "(" + fragment + ")", nil
}

func validateCallProjection(projection ir.PhysicalProjection) error {
	expression := projection.Expression
	if expression == nil {
		return fmt.Errorf("%w: projection %q: CALL expression is missing its payload",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	call := expression.Call
	if call == nil {
		return fmt.Errorf("%w: projection %q: CALL expression is missing its payload",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if expression.Cardinality != ir.PhysicalScalarCardinality || expression.NullBehavior != ir.PhysicalPreserveNull {
		return fmt.Errorf("%w: projection %q: CALL is registered for SCALAR-cardinality PRESERVE_NULL expressions; array and object call projections belong to a later capability",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if projection.Presence != nil {
		return fmt.Errorf("%w: projection %q carries presence metadata on a CALL expression; presence proofs pair with selector extractions only",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	return validateCallShape(projection.Name, call, 0)
}

func validateCallShape(projectionName string, call *ir.PhysicalCall, depth int) error {
	if depth > maxCallExpressionDepth {
		return fmt.Errorf("%w: projection %q: call nesting exceeds the %d-level registered bound",
			ErrUnsupportedPostgresPlan, projectionName, maxCallExpressionDepth)
	}
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if !registeredCallNames[name] {
		return fmt.Errorf("%w: call %q is not part of the v1 PostgreSQL registered call surface (unregistered: array reducers, string pipelines, path utilities, uuid markers, and subplan-valued construction calls belong to later capabilities)",
			ErrUnsupportedPostgresPlan, call.Name)
	}
	switch name {
	case "add", "subtract", "multiply", "divide", "eq", "neq", "gt", "gte", "lt", "lte":
		if len(call.Args) != 2 {
			return fmt.Errorf("%w: call %q requires 2 arguments, got %d", ErrUnsupportedPostgresPlan, name, len(call.Args))
		}
	case "not":
		if len(call.Args) != 1 {
			return fmt.Errorf("%w: call %q requires 1 argument, got %d", ErrUnsupportedPostgresPlan, name, len(call.Args))
		}
	case "and", "or":
		if len(call.Args) < 2 {
			return fmt.Errorf("%w: call %q requires at least 2 arguments, got %d", ErrUnsupportedPostgresPlan, name, len(call.Args))
		}
	case "if":
		if len(call.Args) != 3 {
			return fmt.Errorf("%w: call %q requires 3 arguments, got %d", ErrUnsupportedPostgresPlan, name, len(call.Args))
		}
	case "case":
		if len(call.Args) < 2 {
			return fmt.Errorf("%w: call %q requires at least one condition/result pair", ErrUnsupportedPostgresPlan, name)
		}
	case "coalesce", "fallback":
		if len(call.Args) == 0 {
			return fmt.Errorf("%w: call %q requires at least one argument", ErrUnsupportedPostgresPlan, name)
		}
	case "assert":
		if len(call.Args) != 2 {
			return fmt.Errorf("%w: call %q requires a condition and a message", ErrUnsupportedPostgresPlan, name)
		}
	case "cast":
		if len(call.Args) != 1 {
			return fmt.Errorf("%w: cast requires 1 argument, got %d", ErrUnsupportedPostgresPlan, len(call.Args))
		}
		if !validCallCastTarget(call.TargetKind) {
			return fmt.Errorf("%w: cast target kind %q is not registered for PostgreSQL (date/date_time coercions are gaps)",
				ErrUnsupportedPostgresPlan, call.TargetKind)
		}
	case "distinct_present_one":
		if len(call.Args) != 1 ||
			(call.TargetKind != "STRING" && call.TargetKind != "INTEGER" && call.TargetKind != "DECIMAL" && call.TargetKind != "BOOLEAN") {
			return fmt.Errorf("%w: distinct_present_one requires one array argument and a STRING/INTEGER/DECIMAL/BOOLEAN target kind (the aqlTableScalarType domain, render/aql/table_reshape.go:693-704)",
				ErrUnsupportedPostgresPlan)
		}
	default:
		return fmt.Errorf("%w: call %q is not part of the v1 PostgreSQL registered call surface",
			ErrUnsupportedPostgresPlan, call.Name)
	}
	if name != "cast" && name != "distinct_present_one" && strings.TrimSpace(call.TargetKind) != "" {
		return fmt.Errorf("%w: call %q must not carry target kind %q (only cast and distinct_present_one name a target kind)",
			ErrUnsupportedPostgresPlan, name, call.TargetKind)
	}
	for index := range call.Args {
		arg := &call.Args[index]
		if arg.Call != nil && arg.Kind == ir.PhysicalCallExpression {
			if err := validateCallShape(projectionName, arg.Call, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func validCallCastTarget(target string) bool {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "string", "code", "uuid", "integer", "decimal", "boolean":
		return true
	default:
		return false
	}
}

func forEachCallOperandRowVariable(expression ir.PhysicalExpression, visit func(variable string, extract *ir.PhysicalExtract) error) error {
	call := expression.Call
	if call == nil {
		return fmt.Errorf("%w: CALL expression is missing its payload", ErrUnsupportedPostgresPlan)
	}
	for index := range call.Args {
		arg := &call.Args[index]
		switch arg.Kind {
		case ir.PhysicalValueExpression:
			if arg.Value == nil {
				return fmt.Errorf("%w: VALUE call operand is missing its payload", ErrUnsupportedPostgresPlan)
			}
			if err := visit(arg.Value.Variable, nil); err != nil {
				return fmt.Errorf("argument %d: %w", index, err)
			}
		case ir.PhysicalExtractExpression:
			if arg.Extract == nil {
				return fmt.Errorf("%w: EXTRACT call operand is missing its payload", ErrUnsupportedPostgresPlan)
			}
			if err := visit(arg.Extract.Source.Variable, arg.Extract); err != nil {
				return fmt.Errorf("argument %d: %w", index, err)
			}
		case ir.PhysicalCallExpression:
			if err := forEachCallOperandRowVariable(*arg, visit); err != nil {
				return fmt.Errorf("argument %d: %w", index, err)
			}
		}
	}
	return nil
}

func (r *planRenderer) renderCallExpression(plan ir.PhysicalPlan, expression ir.PhysicalExpression, depth int) (string, callResultFacts, error) {
	if depth > maxCallExpressionDepth {
		return "", callResultFacts{}, fmt.Errorf("%w: call nesting exceeds the %d-level registered bound",
			ErrUnsupportedPostgresPlan, maxCallExpressionDepth)
	}
	if expression.Kind != ir.PhysicalCallExpression || expression.Call == nil {
		return "", callResultFacts{}, fmt.Errorf("%w: CALL expression payload is missing", ErrUnsupportedPostgresPlan)
	}
	call := expression.Call
	if expression.Cardinality != ir.PhysicalScalarCardinality || expression.NullBehavior != ir.PhysicalPreserveNull {
		return "", callResultFacts{}, fmt.Errorf("%w: CALL is registered for SCALAR-cardinality PRESERVE_NULL expressions",
			ErrUnsupportedPostgresPlan)
	}
	if err := validateCallShape("", call, depth); err != nil {
		return "", callResultFacts{}, err
	}
	name := strings.ToLower(strings.TrimSpace(call.Name))
	switch name {
	case "add", "subtract", "multiply", "divide":
		sql, err := r.renderCallArithmetic(plan, name, call, depth)
		return sql, callResultFacts{staticKind: callLiteralNumber}, err
	case "cast":
		sql, kind, err := r.renderCallCast(plan, call, depth)
		return sql, callResultFacts{staticKind: kind}, err
	case "coalesce", "fallback":
		sql, err := r.renderCallCoalesce(plan, call, depth)
		return sql, callResultFacts{}, err
	case "if":
		sql, err := r.renderCallIf(plan, call, depth)
		return sql, callResultFacts{}, err
	case "case":
		sql, err := r.renderCallCase(plan, call, depth)
		return sql, callResultFacts{}, err
	case "and", "or":
		sql, err := r.renderCallLogical(plan, name, call, depth)
		return sql, callResultFacts{}, err
	case "not":
		operand, err := r.renderCallOperand(plan, call.Args[0], depth)
		if err != nil {
			return "", callResultFacts{}, err
		}
		return "to_jsonb(NOT " + callTruthyTest(operand.sql) + ")", callResultFacts{staticKind: callLiteralBoolean}, nil
	case "eq", "neq":
		left, right, err := r.renderCallPair(plan, call, depth)
		if err != nil {
			return "", callResultFacts{}, err
		}
		equal := "COALESCE(" + left.sql + ", 'null'::jsonb) = COALESCE(" + right.sql + ", 'null'::jsonb)"
		if name == "neq" {
			equal = "NOT (" + equal + ")"
		}
		return "to_jsonb(" + equal + ")", callResultFacts{staticKind: callLiteralBoolean}, nil
	case "gt", "gte", "lt", "lte":
		left, right, err := r.renderCallPair(plan, call, depth)
		if err != nil {
			return "", callResultFacts{}, err
		}
		return "to_jsonb(" + callOrderTest(left.sql, sqlCallComparison(name), right.sql) + ")",
			callResultFacts{staticKind: callLiteralBoolean}, nil
	case "assert":
		sql, err := r.renderCallAssert(plan, call, depth)
		return sql, callResultFacts{staticKind: callLiteralBoolean}, err
	case "distinct_present_one":
		sql, err := r.renderCallDistinctPresentOne(plan, call, depth)
		return sql, callResultFacts{}, err
	default:
		return "", callResultFacts{}, fmt.Errorf("%w: call %q is not part of the v1 PostgreSQL registered call surface",
			ErrUnsupportedPostgresPlan, call.Name)
	}
}

func (r *planRenderer) renderCallOperand(plan ir.PhysicalPlan, expression ir.PhysicalExpression, depth int) (callOperand, error) {
	switch expression.Kind {
	case ir.PhysicalLiteralExpression:
		return r.renderCallLiteral(plan, expression)
	case ir.PhysicalExtractExpression:
		return r.renderCallExtractOperand(expression)
	case ir.PhysicalValueExpression:
		return r.renderCallValueOperand(expression)
	case ir.PhysicalCallExpression:
		sql, _, err := r.renderCallExpression(plan, expression, depth+1)
		if err != nil {
			return callOperand{}, err
		}
		sql = "(" + sql + ")"
		if err := checkCallFragment(sql); err != nil {
			return callOperand{}, err
		}
		return callOperand{sql: sql}, nil
	default:
		return callOperand{}, fmt.Errorf("%w: call operand kind %q has no registered PostgreSQL rendering (subplan, aggregate, pivot, object, and related-field operands are capability gaps)",
			ErrUnsupportedPostgresPlan, string(expression.Kind))
	}
}

func (r *planRenderer) renderCallLiteral(plan ir.PhysicalPlan, expression ir.PhysicalExpression) (callOperand, error) {
	if expression.Literal == nil || expression.Literal.BindKey == "" {
		return callOperand{}, fmt.Errorf("%w: LITERAL call operand is missing its bind key", ErrUnsupportedPostgresPlan)
	}
	value, ok := plan.BindVars[expression.Literal.BindKey]
	if !ok {
		return callOperand{}, fmt.Errorf("%w: call literal bind %q is not defined", ErrUnsupportedPostgresPlan, expression.Literal.BindKey)
	}
	switch typed := value.(type) {
	case nil:
		return callOperand{sql: "$" + strconv.Itoa(r.jsonbParameter(json.RawMessage("null"))) + "::jsonb", kind: callLiteralNull}, nil
	case bool:
		return r.bindCallNumberOrScalar(typed, callLiteralBoolean)
	case int64:
		return r.bindCallNumberOrScalar(typed, callLiteralNumber)
	case float64:
		return r.bindCallNumberOrScalar(typed, callLiteralNumber)
	case string:
		return r.bindCallNumberOrScalar(typed, callLiteralString)
	case json.RawMessage:
		var probe any
		if err := json.Unmarshal(typed, &probe); err != nil {
			return callOperand{}, fmt.Errorf("%w: call literal bind %q is not valid JSON: %v",
				ErrUnsupportedPostgresPlan, expression.Literal.BindKey, err)
		}
		if _, isArray := probe.([]any); !isArray {
			return callOperand{}, fmt.Errorf("%w: call literal bind %q has stored kind %s; only a JSON array binds raw (the distinct_present_one collection form) — the unknown call/type pair gate",
				ErrUnsupportedPostgresPlan, expression.Literal.BindKey, jsonProbeKind(probe))
		}
		raw := append(json.RawMessage(nil), typed...)
		return callOperand{sql: "$" + strconv.Itoa(r.jsonbParameter(raw)) + "::jsonb", kind: callLiteralArray}, nil
	default:
		return callOperand{}, fmt.Errorf("%w: call literal bind %q has Go type %T; the registered literal bind types are int64, float64, string, bool, nil, and a json.RawMessage array — the unknown call/type pair gate",
			ErrUnsupportedPostgresPlan, expression.Literal.BindKey, value)
	}
}

func (r *planRenderer) bindCallNumberOrScalar(value any, kind callLiteralKind) (callOperand, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return callOperand{}, fmt.Errorf("%w: call literal bind value %T is not JSON-encodable: %v",
			ErrUnsupportedPostgresPlan, value, err)
	}
	return callOperand{sql: "$" + strconv.Itoa(r.jsonbParameter(raw)) + "::jsonb", kind: kind}, nil
}

func jsonProbeKind(probe any) string {
	switch probe.(type) {
	case nil:
		return "JSON null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func (r *planRenderer) renderCallExtractOperand(expression ir.PhysicalExpression) (callOperand, error) {
	if expression.Cardinality != ir.PhysicalScalarCardinality {
		return callOperand{}, fmt.Errorf("%w: CALL scalar operands accept SCALAR-cardinality EXTRACT expressions only",
			ErrUnsupportedPostgresPlan)
	}
	path, err := validateSelectorExtraction(ir.PhysicalProjection{Name: "call operand", Expression: &expression})
	if err != nil {
		return callOperand{}, err
	}
	scope, ok := r.rows[expression.Extract.Source.Variable]
	if !ok || scope.alias == "" || scope.derivedValue != "" || scope.derivedJSON {
		return callOperand{}, fmt.Errorf("%w: call operand extracts from variable %q, which does not bind a readable stored row",
			ErrUnsupportedPostgresPlan, expression.Extract.Source.Variable)
	}
	if expression.Extract.ResourceType != scope.resourceType {
		return callOperand{}, fmt.Errorf("%w: call operand validates selector against resource type %q, but the row is scanned as %q",
			ErrUnsupportedPostgresPlan, expression.Extract.ResourceType, scope.resourceType)
	}
	sql, err := r.scalarExtractValueExpression(scope.alias, path, expression.NullBehavior)
	if err != nil {
		return callOperand{}, err
	}
	return callOperand{sql: sql}, nil
}

func (r *planRenderer) renderCallValueOperand(expression ir.PhysicalExpression) (callOperand, error) {
	value := expression.Value
	if value == nil || value.BindKey != "" {
		return callOperand{}, fmt.Errorf("%w: VALUE call operands must read a row path, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if _, ok := r.rows[value.Variable]; !ok {
		return callOperand{}, fmt.Errorf("%w: call operand reads variable %q, which does not bind a readable row",
			ErrUnsupportedPostgresPlan, value.Variable)
	}
	if scope := r.rows[value.Variable]; scope.derivedValue != "" && !scope.derivedJSON {
		return callOperand{}, fmt.Errorf("%w: call operand reads derived variable %q, which carries a computed non-JSON value (ordinality and has-item columns are not registered call operands)",
			ErrUnsupportedPostgresPlan, value.Variable)
	}
	if len(value.Path) == 1 && resourceFilterColumns[canonicalKeyColumn(value.Path[0])] {
		return callOperand{}, fmt.Errorf("%w: call operand %v addresses the typed storage column %q; registered call operands read JSON documents, not storage columns",
			ErrUnsupportedPostgresPlan, value.Path, value.Path[0])
	}
	sql, err := r.resolveValue(*value)
	if err != nil {
		return callOperand{}, err
	}
	return callOperand{sql: "(" + sql + ")"}, nil
}

func (r *planRenderer) renderCallPair(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (callOperand, callOperand, error) {
	left, err := r.renderCallOperand(plan, call.Args[0], depth)
	if err != nil {
		return callOperand{}, callOperand{}, fmt.Errorf("argument 0: %w", err)
	}
	right, err := r.renderCallOperand(plan, call.Args[1], depth)
	if err != nil {
		return callOperand{}, callOperand{}, fmt.Errorf("argument 1: %w", err)
	}
	return left, right, nil
}

func (r *planRenderer) renderCallArithmetic(plan ir.PhysicalPlan, name string, call *ir.PhysicalCall, depth int) (string, error) {
	operator, ok := map[string]string{"add": "+", "subtract": "-", "multiply": "*", "divide": "/"}[name]
	if !ok {
		return "", fmt.Errorf("%w: arithmetic call %q is not registered for PostgreSQL",
			ErrUnsupportedPostgresPlan, name)
	}
	left, err := r.renderCallNumericOperand(plan, call.Args[0], depth)
	if err != nil {
		return "", fmt.Errorf("argument 0: %w", err)
	}
	right, err := r.renderCallNumericOperand(plan, call.Args[1], depth)
	if err != nil {
		return "", fmt.Errorf("argument 1: %w", err)
	}
	clampedLeft, clampedRight := callBandOperandClamp(left), callBandOperandClamp(right)
	switch operator {
	case "+", "-":
		magnitude := "abs((" + clampedLeft + " " + operator + " " + clampedRight + "))"
		return "to_jsonb(CASE WHEN " + magnitude + " < '" + callDoubleAddPassUpper + "'::numeric" +
			" THEN " + callDoubleOperand(left) + " " + operator + " " + callDoubleOperand(right) +
			" ELSE NULL END)", nil
	case "*":
		magnitude := "abs((" + clampedLeft + " * " + clampedRight + "))"
		return "to_jsonb(CASE WHEN " + magnitude + " < '" + callDoubleMulDivPassUpper + "'::numeric" +
			" AND " + magnitude + " >= '" + callDoubleSubnormalFloor + "'::numeric" +
			" THEN " + callDoubleOperand(left) + " * " + callDoubleOperand(right) +
			" WHEN " + magnitude + " < '" + callDoubleSubnormalFloor + "'::numeric" +
			" THEN 0::double precision ELSE NULL END)", nil
	default:
		quotient := "((" + clampedLeft + " + " + callDividendScaleBias + ") / NULLIF(" + clampedRight + ", 0))"
		magnitude := "abs(" + quotient + ")"
		return "to_jsonb(CASE WHEN " + magnitude + " < '" + callDoubleMulDivPassUpper + "'::numeric" +
			" AND " + magnitude + " >= '" + callDoubleSubnormalFloor + "'::numeric" +
			" THEN " + callDoubleOperand(left) + " / " + callDoubleDivisor(right) +
			" WHEN " + magnitude + " < '" + callDoubleSubnormalFloor + "'::numeric" +
			" THEN 0::double precision ELSE NULL END)", nil
	}
}

const (
	callDoubleOverflowBound   = "179769313486231580793728971405303415079934132710037826936173778980444968292764750946649017977587207096330286416692887910946555547851940402630657488671505820681908902000708383676273854845817711531764475730270069855571366959622842914819860834936475292719074168444365510704342711559699508093042880177904174497792"
	callDoubleAddPassUpper    = "179769313486231560835325876058105298516207002341652166261661174625869553267292326574530099287946549246750631490335877017522087105926987962906277604735569213290190919152394180476217125334960946356387261286640198029037799514183602981511756283727771403830521483963923935633133642802139091669457927874464075218944"
	callDoubleMulDivPassUpper = "179769313486231500960116590016510948825025611236495184238123361562143308190875053458173343219024575698011666711264844337248681780152130643733137952927759391115036970607451570876046936802390650830255617955750582549437097177865883181587442630101659737164863430522599210419506436529457842398703070964143777382400"
	callDoubleUnderflowFloor  = "2.5E-324"
	callDoubleSubnormalFloor  = "4.9406564584124655E-324"
	callDoubleNormalFloor     = "2.225073858507201383090232717332404064219215980462331830553327416887204434813918195854283159012511020564067339731035811005152434161553460108856012385377718821130777993532002330479610147442583636071921565046942503734208375250806650616658158948720491179968591639648500635908770118304874799780887753749949451580451605050915399856582470818645113537935804992115981085766051992433352114352390148795699609591288891602992641511063466313393663477586513029371762047325631781485664350872122828637642044846811407613911477062801689853244110024161447421618567166150540154285084716752901903161322778896729707373123334086988983175067838846926092773977972858659654941091369095406136467568702398678315290680984617210924625396728515625E-308"
	callDividendScaleBias     = "0E-400"
	callBandClampCap          = "1e400"
)

func callBandOperandClamp(operand string) string {
	return "(CASE" +
		" WHEN " + operand + " > '" + callBandClampCap + "'::numeric THEN '" + callBandClampCap + "'::numeric" +
		" WHEN " + operand + " < '-" + callBandClampCap + "'::numeric THEN '-" + callBandClampCap + "'::numeric" +
		" ELSE " + operand + " END)"
}

func callDoubleOperand(numericOperand string) string {
	return "(CASE WHEN abs(" + numericOperand + ") < '" + callDoubleOverflowBound + "'::numeric" +
		" THEN CASE WHEN abs(" + numericOperand + ") >= '" + callDoubleUnderflowFloor + "'::numeric" +
		" THEN (" + numericOperand + ")::double precision ELSE 0::double precision END" +
		" ELSE NULL END)"
}

func callDoubleDivisor(numericOperand string) string {
	return "(CASE WHEN abs(" + numericOperand + ") < '" + callDoubleOverflowBound + "'::numeric" +
		" THEN CASE WHEN abs(" + numericOperand + ") >= '" + callDoubleNormalFloor + "'::numeric" +
		" THEN (" + numericOperand + ")::double precision ELSE NULL END" +
		" ELSE NULL END)"
}

func callNumericToJSONB(expression string) string {
	return "to_jsonb(CASE WHEN abs(" + expression + ") < '" + callDoubleOverflowBound + "'::numeric" +
		" THEN CASE WHEN abs(" + expression + ") >= '" + callDoubleUnderflowFloor + "'::numeric" +
		" THEN (" + expression + ")::double precision ELSE 0::double precision END END)"
}

func (r *planRenderer) renderCallNumericOperand(plan ir.PhysicalPlan, expression ir.PhysicalExpression, depth int) (string, error) {
	if expression.Kind == ir.PhysicalLiteralExpression {
		literal, err := r.renderCallLiteral(plan, expression)
		if err != nil {
			return "", err
		}
		if literal.kind != callLiteralNumber {
			return "", fmt.Errorf("%w: arithmetic operand literal has stored kind %s; arithmetic accepts number binds (dynamic operands carry the registered coercion table)",
				ErrUnsupportedPostgresPlan, callLiteralKindName(literal.kind))
		}
		return "((" + literal.sql + ") #>> '{}')::numeric", nil
	}
	if expression.Kind == ir.PhysicalCallExpression {
		sql, facts, err := r.renderCallExpression(plan, expression, depth)
		if err != nil {
			return "", err
		}
		if err := checkCallFragment(sql); err != nil {
			return "", err
		}
		if facts.staticKind == callLiteralNumber {
			return "((" + sql + ") #>> '{}')::numeric", nil
		}
		return callNumericCoercion("(" + sql + ")"), nil
	}
	operand, err := r.renderCallOperand(plan, expression, depth)
	if err != nil {
		return "", err
	}
	return callNumericCoercion(operand.sql), nil
}

func callNumericCoercion(operand string) string {
	return "(CASE" +
		" WHEN " + operand + " IS NULL OR jsonb_typeof(" + operand + ") = 'null' THEN NULL" +
		" WHEN jsonb_typeof(" + operand + ") = 'number' THEN (" + operand + " #>> '{}')::numeric" +
		callDecimalTierArms(operand) +
		" ELSE NULL END)"
}

const callDecimalWhitespace = " \t\n\v\f\r"

const (
	callDecimalUnderflowIntegerDigits     = 99
	callDecimalUnderflowMinExponentDigits = 5
)

var callDecimalZeroPattern = `^` + callWhitespaceRegexClass() + `*[+-]?` +
	`(0+(\.0*)?|\.[0]+)([eE][+-]?[0-9]+)?` + callWhitespaceRegexClass() + `*$`

var callDecimalUnderflowPattern = `^` + callWhitespaceRegexClass() + `*[+-]?` +
	"[0-9]{1," + strconv.Itoa(callDecimalUnderflowIntegerDigits) + `}` +
	`(\.[0-9]*)?[eE]-` +
	"[1-9][0-9]{" + strconv.Itoa(callDecimalUnderflowMinExponentDigits-1) + ",}" +
	callWhitespaceRegexClass() + `*$`

var callDecimalStringPattern = `^` + callWhitespaceRegexClass() + `*[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?` + callWhitespaceRegexClass() + `*$`

func callDecimalTierArms(operand string) string {
	text := "(" + operand + ` #>> '{}')`
	btrim := "btrim(" + text + ", " + callDecimalTrimCharacters + ")"
	castArm := "(" + btrim + " || COALESCE(current_setting(" + quoteSQLLiteral(guardBarrierGUC) + ", true), ''))::numeric"
	return " WHEN jsonb_typeof(" + operand + ") = 'string' AND " + text + " ~ " + quoteSQLLiteral(callDecimalStringPattern) +
		" THEN CASE WHEN pg_input_is_valid(" + btrim + ", 'numeric') THEN " + castArm +
		" WHEN (" + text + " ~ " + quoteSQLLiteral(callDecimalZeroPattern) +
		" OR " + text + " ~ " + quoteSQLLiteral(callDecimalUnderflowPattern) + ") THEN 0" +
		" ELSE NULL END"
}

var callDecimalTrimCharacters = quoteSQLLiteral(callDecimalWhitespace)

func callWhitespaceRegexClass() string {
	var class strings.Builder
	class.WriteByte('[')
	for _, r := range callDecimalWhitespace {
		switch r {
		case '\\', ']', '^', '-':
			class.WriteByte('\\')
		}
		class.WriteRune(r)
	}
	class.WriteByte(']')
	return class.String()
}

func (r *planRenderer) renderCallCast(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, callLiteralKind, error) {
	operand, err := r.renderCallOperand(plan, call.Args[0], depth)
	if err != nil {
		return "", callLiteralNot, err
	}
	switch strings.ToLower(strings.TrimSpace(call.TargetKind)) {
	case "string", "code", "uuid":
		return "CASE" +
			" WHEN " + operand.sql + " IS NULL OR jsonb_typeof(" + operand.sql + ") = 'null' THEN '\"\"'::jsonb" +
			" WHEN jsonb_typeof(" + operand.sql + ") IN ('string', 'boolean', 'number')" +
			" THEN to_jsonb((" + operand.sql + ") #>> '{}')" +
			" ELSE NULL END", callLiteralString, nil
	case "integer", "decimal":
		return callToNumberExpression(operand.sql), callLiteralNumber, nil
	case "boolean":
		return "CASE" +
			" WHEN " + operand.sql + " IS NULL OR jsonb_typeof(" + operand.sql + ") = 'null' THEN 'false'::jsonb" +
			" WHEN jsonb_typeof(" + operand.sql + ") = 'boolean' THEN " + operand.sql +
			" WHEN jsonb_typeof(" + operand.sql + ") = 'number' THEN CASE WHEN " + operand.sql +
			" = '0'::jsonb THEN 'false'::jsonb ELSE 'true'::jsonb END" +
			" WHEN jsonb_typeof(" + operand.sql + ") = 'string' THEN CASE WHEN " + operand.sql +
			" = '\"\"'::jsonb THEN 'false'::jsonb ELSE 'true'::jsonb END" +
			" ELSE 'true'::jsonb END", callLiteralBoolean, nil
	default:
		return "", callLiteralNot, fmt.Errorf("%w: cast target kind %q is not registered for PostgreSQL",
			ErrUnsupportedPostgresPlan, call.TargetKind)
	}
}

const callToNumberMaxRecursion = 3

func callToNumberExpression(operand string) string {
	return callNumericToJSONB(callToNumberDouble(operand, 0))
}

func callToNumberDouble(operand string, depth int) string {
	if depth >= callToNumberMaxRecursion {
		return "0"
	}
	element := callToNumberDouble("(("+operand+") -> 0)", depth+1)
	return "CASE" +
		" WHEN " + operand + " IS NULL OR jsonb_typeof(" + operand + ") = 'null' THEN 0" +
		" WHEN jsonb_typeof(" + operand + ") = 'boolean' THEN CASE WHEN " + operand +
		" = 'true'::jsonb THEN 1 ELSE 0 END" +
		" WHEN jsonb_typeof(" + operand + ") = 'number' THEN (" + operand + " #>> '{}')::numeric" +
		callDecimalTierArms(operand) +
		" WHEN jsonb_typeof(" + operand + ") = 'string' THEN 0" +
		" WHEN jsonb_typeof(" + operand + ") = 'array' THEN CASE" +
		" WHEN jsonb_array_length(" + operand + ") = 0 THEN 0" +
		" WHEN jsonb_array_length(" + operand + ") = 1 THEN " + element +
		" ELSE 0 END" +
		" ELSE 0 END"
}

func (r *planRenderer) renderCallCoalesce(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, error) {
	parts := make([]string, 0, len(call.Args))
	for index := range call.Args {
		operand, err := r.renderCallOperand(plan, call.Args[index], depth)
		if err != nil {
			return "", fmt.Errorf("argument %d: %w", index, err)
		}
		parts = append(parts, "NULLIF("+operand.sql+", 'null'::jsonb)")
	}
	return "COALESCE(" + strings.Join(parts, ", ") + ")", nil
}

func (r *planRenderer) renderCallIf(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, error) {
	condition, err := r.renderCallOperand(plan, call.Args[0], depth)
	if err != nil {
		return "", fmt.Errorf("condition: %w", err)
	}
	thenOperand, err := r.renderCallOperand(plan, call.Args[1], depth)
	if err != nil {
		return "", fmt.Errorf("then arm: %w", err)
	}
	elseOperand, err := r.renderCallOperand(plan, call.Args[2], depth)
	if err != nil {
		return "", fmt.Errorf("else arm: %w", err)
	}
	return "CASE WHEN " + callTruthyTest(condition.sql) + " THEN " + thenOperand.sql + " ELSE " + elseOperand.sql + " END", nil
}

func (r *planRenderer) renderCallCase(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, error) {
	operands := make([]callOperand, len(call.Args))
	for index := range call.Args {
		operand, err := r.renderCallOperand(plan, call.Args[index], depth)
		if err != nil {
			return "", fmt.Errorf("argument %d: %w", index, err)
		}
		operands[index] = operand
	}
	end := len(operands)
	result := "NULL"
	if end%2 == 1 {
		result = operands[end-1].sql
		end--
	}
	for index := end - 2; index >= 0; index -= 2 {
		result = "CASE WHEN " + callTruthyTest(operands[index].sql) + " THEN " + operands[index+1].sql + " ELSE " + result + " END"
	}
	return result, nil
}

func (r *planRenderer) renderCallLogical(plan ir.PhysicalPlan, name string, call *ir.PhysicalCall, depth int) (string, error) {
	operands := make([]callOperand, len(call.Args))
	for index := range call.Args {
		operand, err := r.renderCallOperand(plan, call.Args[index], depth)
		if err != nil {
			return "", fmt.Errorf("argument %d: %w", index, err)
		}
		operands[index] = operand
	}
	sql := operands[len(operands)-1].sql
	for index := len(operands) - 2; index >= 0; index-- {
		test := callTruthyTest(operands[index].sql)
		if name == "and" {
			sql = "CASE WHEN " + test + " THEN " + sql + " ELSE " + operands[index].sql + " END"
		} else {
			sql = "CASE WHEN " + test + " THEN " + operands[index].sql + " ELSE " + sql + " END"
		}
	}
	return sql, nil
}

func callTruthyTest(operand string) string {
	return "NOT (" + operand + " IS NULL" +
		" OR jsonb_typeof(" + operand + ") = 'null'" +
		" OR " + operand + " = 'false'::jsonb" +
		" OR (jsonb_typeof(" + operand + ") = 'number' AND " + operand + " = '0'::jsonb)" +
		" OR " + operand + " = '\"\"'::jsonb)"
}

func sqlCallComparison(name string) string {
	switch name {
	case "gt":
		return ">"
	case "gte":
		return ">="
	case "lt":
		return "<"
	case "lte":
		return "<="
	default:
		return ""
	}
}

func callOrderTest(left, operator, right string) string {
	leftRank := callValueRank(left)
	rightRank := callValueRank(right)
	return "CASE" +
		" WHEN " + leftRank + " >= 4 OR " + rightRank + " >= 4 THEN NULL" +
		" WHEN " + leftRank + " <> " + rightRank + " THEN " + leftRank + " " + operator + " " + rightRank +
		" ELSE (" + callNullNormalized(left) + ") " + operator + " (" + callNullNormalized(right) + ") END"
}

func callNullNormalized(operand string) string {
	return "COALESCE(" + operand + ", 'null'::jsonb)"
}

func callValueRank(operand string) string {
	return "(CASE" +
		" WHEN " + operand + " IS NULL OR jsonb_typeof(" + operand + ") = 'null' THEN 0" +
		" WHEN jsonb_typeof(" + operand + ") = 'boolean' THEN 1" +
		" WHEN jsonb_typeof(" + operand + ") = 'number' THEN 2" +
		" WHEN jsonb_typeof(" + operand + ") = 'string' THEN 3" +
		" ELSE 4 END)"
}

func (r *planRenderer) renderCallAssert(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, error) {
	condition, err := r.renderCallOperand(plan, call.Args[0], depth)
	if err != nil {
		return "", fmt.Errorf("condition: %w", err)
	}
	message := call.Args[1]
	if message.Kind != ir.PhysicalLiteralExpression || message.Literal == nil || message.Literal.BindKey == "" {
		return "", fmt.Errorf("%w: assert message must be one bind-backed string literal", ErrUnsupportedPostgresPlan)
	}
	value, ok := plan.BindVars[message.Literal.BindKey]
	if !ok {
		return "", fmt.Errorf("%w: assert message bind %q is not defined", ErrUnsupportedPostgresPlan, message.Literal.BindKey)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: assert message bind %q has type %T; the ASSERT message must be a string (the unknown call/type pair gate)",
			ErrUnsupportedPostgresPlan, message.Literal.BindKey, value)
	}
	index := r.textParameter(text, "")
	raise := "to_jsonb(CAST('ASSERT:' || $" + strconv.Itoa(index) +
		" || COALESCE(current_setting(" + quoteSQLLiteral(guardBarrierGUC) + ", true), '') AS integer))"
	return "CASE WHEN " + callTruthyTest(condition.sql) + " THEN 'true'::jsonb ELSE " + raise + " END", nil
}

const (
	distinctPresentGuardTypeMismatch   = "CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH"
	distinctPresentGuardMultipleValues = "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES"
)

func (r *planRenderer) renderCallDistinctPresentOne(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, error) {
	var expected string
	switch call.TargetKind {
	case "STRING":
		expected = "string"
	case "INTEGER", "DECIMAL":
		expected = "number"
	case "BOOLEAN":
		expected = "boolean"
	default:
		return "", fmt.Errorf("%w: distinct_present_one target kind %q is not registered", ErrUnsupportedPostgresPlan, call.TargetKind)
	}
	base, err := r.renderCallArrayOperand(plan, call.Args[0], depth)
	if err != nil {
		return "", err
	}
	guarded := "CASE WHEN " + base + " IS NULL OR jsonb_typeof(" + base + ") <> 'array' THEN NULL::jsonb ELSE " + base + " END"
	return "(SELECT CASE" +
		" WHEN bool_or(jsonb_typeof(loom_dp_item.item) <> " + quoteSQLLiteral(expected) + ") THEN to_jsonb(" + guardCast(distinctPresentGuardTypeMismatch) + ")" +
		" WHEN COUNT(*) = 0 THEN NULL" +
		" WHEN COUNT(DISTINCT loom_dp_item.item) > 1 THEN to_jsonb(" + guardCast(distinctPresentGuardMultipleValues) + ")" +
		" ELSE min(loom_dp_item.item::text)::jsonb" +
		" END FROM jsonb_array_elements(" + guarded + ") AS loom_dp_outer(value)" +
		" LEFT JOIN LATERAL jsonb_array_elements(" +
		"CASE WHEN jsonb_typeof(loom_dp_outer.value) = 'array' THEN loom_dp_outer.value ELSE NULL::jsonb END" +
		") AS loom_dp_flattened(value) ON TRUE" +
		" LEFT JOIN LATERAL (SELECT CASE" +
		" WHEN jsonb_typeof(loom_dp_outer.value) = 'array' THEN loom_dp_flattened.value" +
		" ELSE loom_dp_outer.value END AS item) AS loom_dp_item ON TRUE" +
		" WHERE loom_dp_item.item IS NOT NULL AND loom_dp_item.item <> 'null'::jsonb)", nil
}

func (r *planRenderer) renderCallArrayOperand(plan ir.PhysicalPlan, expression ir.PhysicalExpression, depth int) (string, error) {
	switch expression.Kind {
	case ir.PhysicalLiteralExpression:
		literal, err := r.renderCallLiteral(plan, expression)
		if err != nil {
			return "", err
		}
		if literal.kind != callLiteralArray {
			return "", fmt.Errorf("%w: distinct_present_one literal must bind a JSON array (the unknown call/type pair gate); got stored kind %s",
				ErrUnsupportedPostgresPlan, callLiteralKindName(literal.kind))
		}
		return literal.sql, nil
	case ir.PhysicalExtractExpression:
		if expression.Cardinality != ir.PhysicalArrayCardinality {
			return "", fmt.Errorf("%w: distinct_present_one EXTRACT argument must be ARRAY-cardinality", ErrUnsupportedPostgresPlan)
		}
		extract := expression.Extract
		if extract == nil || len(extract.Fallbacks) != 0 || extract.Prepared != nil ||
			extract.UnitNormalization != nil || extract.Distinct || extract.Selector.Filter != nil {
			return "", fmt.Errorf("%w: distinct_present_one accepts only the closed direct-property EXTRACT; fallbacks, prepared sets, unit normalization, DISTINCT, and selector filters are gaps",
				ErrUnsupportedPostgresPlan)
		}
		switch extract.ExecutionMode {
		case ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar, "":
		default:
			return "", fmt.Errorf("%w: distinct_present_one extraction mode %q has no registered form",
				ErrUnsupportedPostgresPlan, string(extract.ExecutionMode))
		}
		switch expression.NullBehavior {
		case ir.PhysicalPreserveNull, ir.PhysicalEmptyOnNull:
		default:
			return "", fmt.Errorf("%w: distinct_present_one extraction null behavior %q has no registered form",
				ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
		}
		source := extract.Source
		if source.BindKey != "" || source.Variable == "" || len(source.Path) != 1 || source.Path[0] != "payload" {
			return "", fmt.Errorf("%w: distinct_present_one source must be one readable row's document payload", ErrUnsupportedPostgresPlan)
		}
		scope, ok := r.rows[source.Variable]
		if !ok || scope.alias == "" || scope.derivedValue != "" || scope.derivedJSON {
			return "", fmt.Errorf("%w: distinct_present_one source variable %q does not bind a readable stored row",
				ErrUnsupportedPostgresPlan, source.Variable)
		}
		if strings.TrimSpace(extract.ResourceType) == "" || extract.ResourceType != scope.resourceType {
			return "", fmt.Errorf("%w: distinct_present_one resource type %q contradicts the scanned row type %q",
				ErrUnsupportedPostgresPlan, extract.ResourceType, scope.resourceType)
		}
		path := make([]string, 0, len(extract.Selector.Steps))
		for _, step := range extract.Selector.Steps {
			if step.Field == "" || step.Iterate || step.Index != nil {
				return "", fmt.Errorf("%w: distinct_present_one accepts only direct property paths; repeated, indexed, and filtered steps are gaps",
					ErrUnsupportedPostgresPlan)
			}
			if !payloadPathSegmentPattern.MatchString(step.Field) {
				return "", fmt.Errorf("%w: distinct_present_one path segment %q is not renderer-safe", ErrUnsupportedPostgresPlan, step.Field)
			}
			path = append(path, step.Field)
		}
		if len(path) == 0 {
			return "", fmt.Errorf("%w: distinct_present_one selector must have at least one property", ErrUnsupportedPostgresPlan)
		}
		return r.scalarExtractValueExpression(scope.alias, path, expression.NullBehavior)
	default:
		return "", fmt.Errorf("%w: distinct_present_one argument kind %q is not registered (subplan arguments stay unregistered in this wave; their registration is a coordinator decision)",
			ErrUnsupportedPostgresPlan, string(expression.Kind))
	}
}

func callLiteralKindName(kind callLiteralKind) string {
	switch kind {
	case callLiteralNull:
		return "JSON null"
	case callLiteralNumber:
		return "number"
	case callLiteralString:
		return "string"
	case callLiteralBoolean:
		return "boolean"
	case callLiteralArray:
		return "array"
	default:
		return "unknown"
	}
}
