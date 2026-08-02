package schema

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Selector struct {
	Steps  []SelectorStep
	Filter *ContainsFilter
}

type SelectorStep struct {
	Field   string
	Iterate bool
	Index   *int
}

type ContainsFilter struct {
	Field  string
	Needle string
}

type FieldSelectorSpec struct {
	SourcePath string
	Where      *FieldPredicateSpec
	ValuePath  string
}

type FieldPredicateSpec struct {
	Path  string
	Op    string
	Value string
}

const PredicateContains = "CONTAINS"

var containsPattern = regexp.MustCompile(`^([A-Za-z0-9_]+)\s+contains\s+"([^"]*)"$`)

func FieldSelectorSpecFromPath(path string) FieldSelectorSpec {
	sourcePath, valuePath := selectorParts(CanonicalizePath(path))
	return FieldSelectorSpec{SourcePath: sourcePath, ValuePath: valuePath}
}

func SelectorFromField(field FieldSpec) FieldSelectorSpec {
	return FieldSelectorSpec{SourcePath: field.SourcePath, ValuePath: field.ValuePath}
}

func SelectorExpression(spec FieldSelectorSpec) string {
	path := strings.TrimSpace(spec.ValuePath)
	if strings.TrimSpace(spec.SourcePath) != "" {
		path = strings.TrimSpace(spec.SourcePath) + "." + path
	}
	if spec.Where == nil || spec.Where.Op != PredicateContains || strings.TrimSpace(spec.Where.Path) == "" || spec.Where.Value == "" {
		return path
	}
	return fmt.Sprintf(`%s where %s contains %q`, path, spec.Where.Path, spec.Where.Value)
}

func CanonicalPath(spec FieldSelectorSpec) string {
	return CanonicalizePath(SelectorExpression(FieldSelectorSpec{SourcePath: spec.SourcePath, ValuePath: spec.ValuePath}))
}

func CanonicalizePath(path string) string {
	parts := strings.Split(strings.TrimSpace(path), ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || strings.Contains(part, " ") {
			continue
		}
		if strings.HasSuffix(part, "]") && strings.Contains(part, "[") && !strings.HasSuffix(part, "[]") {
			part = part[:strings.Index(part, "[")] + "[]"
		}
		out = append(out, part)
	}
	return strings.Join(out, ".")
}

func ParseSelector(input string) (Selector, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Selector{}, fmt.Errorf("selector is required")
	}
	var filter *ContainsFilter
	pathPart := input
	if before, after, found := strings.Cut(input, " where "); found {
		pathPart = strings.TrimSpace(before)
		match := containsPattern.FindStringSubmatch(strings.TrimSpace(after))
		if len(match) != 3 {
			return Selector{}, fmt.Errorf("unsupported where clause %q", after)
		}
		filter = &ContainsFilter{Field: match[1], Needle: match[2]}
	}
	parts := strings.Split(pathPart, ".")
	steps := make([]SelectorStep, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return Selector{}, fmt.Errorf("invalid path segment in %q", input)
		}
		step := SelectorStep{}
		switch {
		case strings.HasSuffix(part, "[]"):
			step.Field = strings.TrimSuffix(part, "[]")
			step.Iterate = true
		case strings.HasSuffix(part, "]") && strings.Contains(part, "["):
			idxStart := strings.Index(part, "[")
			step.Field = part[:idxStart]
			idx, err := strconv.Atoi(strings.TrimSuffix(part[idxStart+1:], "]"))
			if err != nil {
				return Selector{}, fmt.Errorf("invalid array index in %q", part)
			}
			step.Index = &idx
		default:
			step.Field = part
		}
		if step.Field == "" {
			return Selector{}, fmt.Errorf("invalid field in %q", part)
		}
		steps = append(steps, step)
	}
	return Selector{Steps: steps, Filter: filter}, nil
}

func (s Selector) CanonicalPath() string {
	parts := make([]string, 0, len(s.Steps))
	for _, step := range s.Steps {
		switch {
		case step.Iterate:
			parts = append(parts, step.Field+"[]")
		case step.Index != nil:
			parts = append(parts, step.Field+"[]")
		default:
			parts = append(parts, step.Field)
		}
	}
	return strings.Join(parts, ".")
}

func selectorParts(path string) (string, string) {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[:idx], path[idx+1:]
	}
	return "", path
}
