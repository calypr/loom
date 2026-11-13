package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *planRenderer) documentColumn(alias string) string {
	return alias + "." + r.storage.Resource.MustColumn("payload").SQLText()
}

func (r *planRenderer) documentPathExpression(alias string, path []string) (string, error) {
	return r.documentPathFromExpression(r.documentColumn(alias), path)
}

func (r *planRenderer) documentPathFromExpression(base string, path []string) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("%w: a document path must have at least one segment", ErrUnsupportedPostgresPlan)
	}
	for _, segment := range path {
		if !payloadPathSegmentPattern.MatchString(segment) {
			return "", fmt.Errorf("%w: payload path segment %q is not renderer-safe", ErrUnsupportedPostgresPlan, segment)
		}
	}
	elements := make([]string, len(path))
	for index, segment := range path {
		elements[index] = arrayLiteralElement(segment)
	}
	pathLiteral := "{" + strings.Join(elements, ",") + "}"
	return base + " #> $" + strconv.Itoa(r.textParameter(pathLiteral, "path:"+pathLiteral)) + "::text[]", nil
}

type documentPathStep struct {
	field   string
	index   int
	indexed bool
}

func (r *planRenderer) documentPathStepsExpression(alias string, steps []documentPathStep) (string, error) {
	return r.documentPathStepsFromExpression(r.documentColumn(alias), steps)
}

func (r *planRenderer) documentPathStepsFromExpression(base string, steps []documentPathStep) (string, error) {
	if len(steps) == 0 {
		return "", fmt.Errorf("%w: a document path must have at least one segment", ErrUnsupportedPostgresPlan)
	}
	current := base
	for start := 0; start < len(steps); {
		end := start
		for end < len(steps) && !(steps[end].indexed && steps[end].index < 0) {
			end++
		}
		if end > start {
			elements := make([]string, 0, end-start)
			for _, step := range steps[start:end] {
				if !payloadPathSegmentPattern.MatchString(step.field) {
					return "", fmt.Errorf("%w: payload path segment %q is not renderer-safe", ErrUnsupportedPostgresPlan, step.field)
				}
				if step.indexed {
					if step.index < 0 {
						return "", fmt.Errorf("%w: negative document path index %d under segment %q reached the non-negative chunk", ErrUnsupportedPostgresPlan, step.index, step.field)
					}
					elements = append(elements, arrayLiteralElement(step.field))
					elements = append(elements, arrayLiteralElement(strconv.Itoa(step.index)))
					continue
				}
				elements = append(elements, arrayLiteralElement(step.field))
			}
			pathLiteral := "{" + strings.Join(elements, ",") + "}"
			current = current + " #> $" + strconv.Itoa(r.textParameter(pathLiteral, "path:"+pathLiteral)) + "::text[]"
		}
		if end >= len(steps) {
			break
		}
		step := steps[end]
		if !payloadPathSegmentPattern.MatchString(step.field) {
			return "", fmt.Errorf("%w: payload path segment %q is not renderer-safe", ErrUnsupportedPostgresPlan, step.field)
		}
		arrLiteral := "{" + arrayLiteralElement(step.field) + "}"
		arr := current + " #> $" + strconv.Itoa(r.textParameter(arrLiteral, "path:"+arrLiteral)) + "::text[]"
		indexParameter := strconv.Itoa(r.addParameter(
			command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(step.index)},
			"tailidx:"+strconv.Itoa(step.index)))
		propertyLiteral := "{" + arrayLiteralElement(strconv.Itoa(step.index)) + "}"
		property := arr + " #> $" + strconv.Itoa(r.textParameter(propertyLiteral, "path:"+propertyLiteral)) + "::text[]"
		current = "(CASE WHEN jsonb_typeof(" + arr + ") = 'array' THEN (CASE WHEN $" + indexParameter +
			" < 0 THEN " + arr + " -> (jsonb_array_length(" + arr + ") + $" + indexParameter + ") END) ELSE " +
			property + " END)"
		start = end + 1
	}
	return current, nil
}

func arrayLiteralElement(segment string) string {
	if strings.EqualFold(segment, "null") {
		return `"` + segment + `"`
	}
	return segment
}

func (r *planRenderer) scalarExtractValueExpression(alias string, path []string, behavior ir.PhysicalNullBehavior) (string, error) {
	extraction, err := r.documentPathExpression(alias, path)
	if err != nil {
		return "", err
	}
	switch behavior {
	case ir.PhysicalPreserveNull:
		return "(" + extraction + ")", nil
	case ir.PhysicalOmitNulls:
		return "NULLIF((" + extraction + "), 'null'::jsonb)", nil
	case ir.PhysicalEmptyOnNull:
		return "COALESCE(NULLIF((" + extraction + "), 'null'::jsonb), '\"\"'::jsonb)", nil
	default:
		return "", fmt.Errorf("%w: null behavior %q has no registered PostgreSQL extraction form",
			ErrUnsupportedPostgresPlan, string(behavior))
	}
}

func (r *planRenderer) propertyPresenceExpression(alias string, path []string) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("%w: a presence proof needs at least one property", ErrUnsupportedPostgresPlan)
	}
	for _, segment := range path {
		if !payloadPathSegmentPattern.MatchString(segment) {
			return "", fmt.Errorf("%w: presence path segment %q is not renderer-safe", ErrUnsupportedPostgresPlan, segment)
		}
	}
	parent := r.documentColumn(alias)
	if len(path) > 1 {
		prefix, err := r.documentPathExpression(alias, path[:len(path)-1])
		if err != nil {
			return "", err
		}
		parent = prefix
	}
	keyParameter := r.textParameter(path[len(path)-1], "text:"+path[len(path)-1])
	return "COALESCE(jsonb_typeof(" + parent + ") = 'object' AND jsonb_exists(" + parent + ", $" + strconv.Itoa(keyParameter) + "), false)", nil
}
