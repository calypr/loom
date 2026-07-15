package aql

import (
	"fmt"
	"strings"
)

func (r *physicalPlanRenderer) renderCall(expression PhysicalExpression) (string, error) {
	call := expression.Call
	if call == nil {
		return "", fmt.Errorf("CALL expression is missing payload")
	}
	name := strings.ToLower(strings.TrimSpace(call.Name))
	nestedUUID := name != "uuid3" && name != "uuid5" && containsExactUUIDCall(call.Args)
	args := make([]string, len(call.Args))
	for index, arg := range call.Args {
		value, err := r.renderExpression(arg)
		if err != nil {
			return "", fmt.Errorf("call %q argument %d: %w", name, index, err)
		}
		args[index] = value
	}
	if nestedUUID {
		return fmt.Sprintf("{\"__loom_postquery_call\": %q, \"__loom_postquery_target\": %q, \"__loom_postquery_args\": [%s]}", name, call.TargetKind, strings.Join(args, ", ")), nil
	}
	require := func(count int) error {
		if len(args) != count {
			return fmt.Errorf("%s requires %d argument(s), got %d", name, count, len(args))
		}
		return nil
	}
	joinArgs := func(separator string) string { return strings.Join(args, separator) }
	scalarNull := func() string {
		key := r.newInternalBindKey("call_null")
		r.bindVars[key] = nil
		return "@" + key
	}
	switch name {
	case "coalesce_string":
		if len(args) == 0 {
			return "", fmt.Errorf("coalesce_string requires at least one argument")
		}
		converted := make([]string, len(args))
		for index, arg := range args {
			converted[index] = "(" + arg + " == null ? null : TO_STRING(" + arg + "))"
		}
		return "FIRST(FOR __loom_call_value IN [" + strings.Join(converted, ", ") + "] FILTER __loom_call_value != null RETURN __loom_call_value)", nil
	case "coalesce", "fallback":
		if len(args) == 0 {
			return "", fmt.Errorf("%s requires at least one argument", name)
		}
		return "FIRST(FOR __loom_call_value IN [" + joinArgs(", ") + "] FILTER __loom_call_value != null RETURN __loom_call_value)", nil
	case "first":
		if err := require(1); err != nil {
			return "", err
		}
		return "FIRST(FLATTEN(" + args[0] + "))", nil
	case "all":
		if err := require(1); err != nil {
			return "", err
		}
		if expression.Cardinality != PhysicalArrayCardinality {
			return "[" + args[0] + "]", nil
		}
		return args[0], nil
	case "distinct":
		if err := require(1); err != nil {
			return "", err
		}
		return "SORTED_UNIQUE(FLATTEN(" + args[0] + "))", nil
	case "concat":
		if len(args) == 0 {
			return "", fmt.Errorf("concat requires at least one argument")
		}
		return "CONCAT(" + joinArgs(", ") + ")", nil
	case "join":
		if err := require(2); err != nil {
			return "", err
		}
		// AQL has no JOIN function.  Recipe expressions pass the value
		// collection first and the delimiter second; CONCAT_SEPARATOR uses the
		// inverse argument order and accepts an array as its value operand.
		return "CONCAT_SEPARATOR(" + args[1] + ", " + args[0] + ")", nil
	case "cast":
		if err := require(1); err != nil {
			return "", err
		}
		switch strings.ToLower(strings.TrimSpace(call.TargetKind)) {
		case "string", "code", "uuid":
			return "TO_STRING(" + args[0] + ")", nil
		case "integer", "decimal":
			return "TO_NUMBER(" + args[0] + ")", nil
		case "boolean":
			return "TO_BOOL(" + args[0] + ")", nil
		case "date", "date_time":
			return "DATE_ISO8601(" + args[0] + ")", nil
		default:
			return "", fmt.Errorf("cast target kind %q is unsupported", call.TargetKind)
		}
	case "reference_id", "path_segment", "basename", "last_segment":
		if err := require(1); err != nil {
			return "", err
		}
		trimPattern := r.newInternalBindKey("call_path_trim_pattern")
		segmentPattern := r.newInternalBindKey("call_path_segment_pattern")
		emptyReplacement := r.newInternalBindKey("call_path_empty_replacement")
		r.bindVars[trimPattern] = `/+$`
		r.bindVars[segmentPattern] = `[/#]`
		r.bindVars[emptyReplacement] = ""
		trimmed := "REGEX_REPLACE(TO_STRING(" + args[0] + "), @" + trimPattern + ", @" + emptyReplacement + ")"
		return "(" + args[0] + " == null ? null : LAST(REGEX_SPLIT(" + trimmed + ", @" + segmentPattern + ")))", nil
	case "sanitize_name", "sanitize_graphql_name":
		if err := require(1); err != nil {
			return "", err
		}
		pattern := r.newInternalBindKey("call_name_pattern")
		replacement := r.newInternalBindKey("call_name_replacement")
		empty := r.newInternalBindKey("call_name_empty")
		underscore := r.newInternalBindKey("call_name_underscore")
		startsDigit := r.newInternalBindKey("call_name_starts_digit")
		dunder := r.newInternalBindKey("call_name_dunder")
		r.bindVars[pattern] = `[^A-Za-z0-9_]`
		r.bindVars[replacement] = "_"
		r.bindVars[empty] = ""
		r.bindVars[underscore] = "_"
		r.bindVars[startsDigit] = `^[0-9]`
		r.bindVars[dunder] = `^__`
		clean := "REGEX_REPLACE(TO_STRING(" + args[0] + "), @" + pattern + ", @" + replacement + ")"
		return "(" + clean + " == @" + empty + " ? @" + underscore + " : (REGEX_TEST(" + clean + ", @" + startsDigit + ") ? CONCAT(@" + underscore + ", " + clean + ") : (REGEX_TEST(" + clean + ", @" + dunder + ") ? CONCAT(@" + underscore + ", SUBSTRING(" + clean + ", 2)) : " + clean + ")))", nil
	case "uuid3", "uuid5":
		if len(args) < 2 {
			return "", fmt.Errorf("%s requires a namespace and at least one name", name)
		}
		// AQL cannot reproduce namespace-byte hashing and RFC bit handling
		// portably. Return a typed marker for the compiler-owned post-query
		// stage instead of emitting a textual hash approximation.
		return fmt.Sprintf("{\"__loom_exact_uuid_operation\": %q, \"__loom_exact_uuid_args\": [%s]}", name, strings.Join(args, ", ")), nil
	case "if":
		if err := require(3); err != nil {
			return "", err
		}
		return "(" + args[0] + " ? " + args[1] + " : " + args[2] + ")", nil
	case "case":
		if len(args) < 2 {
			return "", fmt.Errorf("case requires at least one condition/result pair")
		}
		withElse := len(args)%2 == 1
		end := len(args)
		result := scalarNull()
		if withElse {
			result = args[end-1]
			end--
		}
		if end%2 != 0 {
			return "", fmt.Errorf("case requires condition/result pairs and optional else")
		}
		for index := end - 2; index >= 0; index -= 2 {
			result = "(" + args[index] + " ? " + args[index+1] + " : " + result + ")"
		}
		return result, nil
	case "not":
		if err := require(1); err != nil {
			return "", err
		}
		return "NOT (" + args[0] + ")", nil
	case "and", "or":
		if len(args) < 2 {
			return "", fmt.Errorf("%s requires at least two arguments", name)
		}
		operator := " AND "
		if name == "or" {
			operator = " OR "
		}
		return "(" + joinArgs(operator) + ")", nil
	case "eq", "neq", "gt", "gte", "lt", "lte":
		if err := require(2); err != nil {
			return "", err
		}
		operator := map[string]string{"eq": "==", "neq": "!=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[name]
		return "(" + args[0] + " " + operator + " " + args[1] + ")", nil
	case "contains":
		if err := require(2); err != nil {
			return "", err
		}
		return "CONTAINS(TO_STRING(" + args[0] + "), TO_STRING(" + args[1] + "))", nil
	default:
		return "", fmt.Errorf("unsupported physical call %q", call.Name)
	}
}

func containsExactUUIDCall(expressions []PhysicalExpression) bool {
	for _, expression := range expressions {
		if expression.Call == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(expression.Call.Name))
		if name == "uuid3" || name == "uuid5" || containsExactUUIDCall(expression.Call.Args) {
			return true
		}
	}
	return false
}

// renderObject renders a recursively typed object expression. Sorting a copy
// gives equivalent physical plans stable AQL and bind-key allocation without
// changing the semantic field order held by the plan.
//
// The compact dynamic-key literal is used when every field preserves nulls.
// If any field requests OMIT_NULLS, fields are represented as a temporary
// stream and merged so null-valued fields can be removed without evaluating
// their expression twice.
