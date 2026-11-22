package codedconcept

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type semanticChoiceValue struct {
	Arm                   string
	OwnerPath             string
	Selector              string
	FallbackSelectors     []string
	FallbackOnBlankString bool
	Type                  string
	Status                string
	Value                 any
}

func semanticExampleValues(value semanticChoiceValue) []any {
	object, ok := value.Value.(map[string]any)
	if !ok {
		return []any{value.Value}
	}
	if value.FallbackOnBlankString {
		primary := semanticValueAtPath(object, semanticValuePathWithinArm(value.Selector, value.Arm))
		if text, ok := primary.(string); ok && strings.TrimSpace(text) != "" {
			return []any{text}
		}
		var examples []any
		for _, selector := range value.FallbackSelectors {
			examples = append(examples, semanticValuesAtPath(object, semanticValuePathWithinArm(selector, value.Arm))...)
		}
		return examples
	}

	path := strings.TrimPrefix(value.Selector, value.Arm+".")
	if path != "" {
		return []any{semanticUseJSONNumber(semanticValueAtPath(object, path))}
	}
	return []any{value.Value}
}

func semanticValuePathWithinArm(path, arm string) string {
	path, arm = strings.TrimSpace(path), strings.TrimSpace(arm)
	if arm == "" {
		return path
	}
	prefix := arm + "."
	if index := strings.LastIndex(path, prefix); index >= 0 {
		return path[index+len(prefix):]
	}
	return strings.TrimPrefix(path, prefix)
}

func semanticValueAtPath(value any, path string) any {
	current := value
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSuffix(part, "[]")
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}

func semanticValuesAtPath(value any, path string) []any {
	parts := strings.Split(path, ".")
	var walk func(any, int) []any
	walk = func(current any, index int) []any {
		if index == len(parts) {
			return []any{semanticUseJSONNumber(current)}
		}
		part := parts[index]
		array := strings.HasSuffix(part, "[]")
		name := strings.TrimSuffix(part, "[]")
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		next := object[name]
		if !array {
			return walk(next, index+1)
		}
		items, ok := next.([]any)
		if !ok {
			return nil
		}
		var values []any
		for _, item := range items {
			values = append(values, walk(item, index+1)...)
		}
		return values
	}
	return walk(value, 0)
}

func semanticProjectionStatus(value map[string]any, path string) string {
	var current any = value
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return StatusUnsupportedValueProjection
		}
		current, ok = object[part]
		if !ok || current == nil {
			return StatusUnsupportedValueProjection
		}
	}
	if text, ok := current.(string); ok && strings.TrimSpace(text) == "" {
		return StatusUnsupportedValueProjection
	}
	return "SUPPORTED"
}

func semanticGeneratedValueType(resourceType, path string, value any, fallback string) string {
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, path)
	if ok && metadata.Primitive != fhirschema.PrimitiveUnknown {
		return string(metadata.Primitive)
	}

	if resourceType != "Extension" {
		metadata, ok = fhirschema.ResolveTerminalScalarMetadata("Extension", path)
		if ok && metadata.Primitive != fhirschema.PrimitiveUnknown {
			return string(metadata.Primitive)
		}
	}
	return fallback
}

func semanticObservedUnits(value any) []string {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	units := make([]string, 0, 3)
	for _, key := range []string{"unit", "code", "system"} {
		if text := strings.TrimSpace(stringValue(object[key])); text != "" {
			units = append(units, text)
		}
	}
	return units
}

func ProfileForPayload(payload map[string]any) string {
	meta, ok := payload["meta"].(map[string]any)
	if !ok {
		return ""
	}
	profiles := []string{}
	if values, ok := meta["profile"].([]any); ok {
		for _, value := range values {
			if text, valid := SafeExample(value); valid {
				profiles = append(profiles, text)
			}
		}
	}
	if text, valid := SafeExample(meta["profile"]); valid {
		profiles = append(profiles, text)
	}
	sort.Strings(profiles)
	if len(profiles) == 0 {
		return ""
	}
	return profiles[0]
}

func semanticCodings(value map[string]any) []map[string]any {
	items, _ := value["coding"].([]any)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if coding, ok := item.(map[string]any); ok {
			result = append(result, coding)
		}
	}
	return result
}

func semanticValueType(value any) string {
	switch value.(type) {
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return "number"
	default:
		return "string"
	}
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

const maxSemanticExampleBytes = 256

func SafeExample(value any) (string, bool) {
	var text string
	switch typed := value.(type) {
	case string:
		text = strings.TrimSpace(typed)
	case bool:
		text = strconv.FormatBool(typed)
	case int:
		text = strconv.Itoa(typed)
	case int8, int16, int32, int64:
		text = strconv.FormatInt(reflectInt64(typed), 10)
	case uint, uint8, uint16, uint32, uint64:
		text = strconv.FormatUint(reflectUint64(typed), 10)
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return "", false
		}
		text = strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return "", false
		}
		text = strconv.FormatFloat(typed, 'g', -1, 64)
	case json.Number:
		text = typed.String()
	default:
		return "", false
	}
	if text == "" || len(text) > maxSemanticExampleBytes || !utf8.ValidString(text) {
		return "", false
	}
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return text, true
}

func reflectInt64(value any) int64 {
	switch v := value.(type) {
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	}
	return 0
}
func reflectUint64(value any) uint64 {
	switch v := value.(type) {
	case uint8:
		return uint64(v)
	case uint16:
		return uint64(v)
	case uint32:
		return uint64(v)
	case uint64:
		return v
	case uint:
		return uint64(v)
	}
	return 0
}
