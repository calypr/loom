package schema

import (
	"sort"
	"strings"
	"sync"
)

type ResourceSpec struct {
	ResourceType string
	Fields       []FieldSpec
}

type FieldSpec struct {
	Path           string
	SourcePath     string
	ValuePath      string
	Kind           string
	PredicatePaths []string
}

type ResolvedPath struct {
	Path        string
	Property    generatedProperty
	PropertyRef string
}

const maxSelectorFieldDepth = 6

var resourceCache sync.Map

func FieldsForResource(resourceType string) []FieldSpec {
	if cached, ok := resourceCache.Load(resourceType); ok {
		return cloneFields(cached.([]FieldSpec))
	}
	fields := flattenDefinition(resourceType, 0, map[string]bool{})
	if len(fields) == 0 {
		return []FieldSpec{}
	}
	resourceCache.Store(resourceType, cloneFields(fields))
	return cloneFields(fields)
}

func LookupField(resourceType, canonicalPath string) (FieldSpec, bool) {
	for _, field := range FieldsForResource(resourceType) {
		if field.Path == canonicalPath {
			return field, true
		}
	}
	return FieldSpec{}, false
}

func ResolvePath(resourceType, canonicalPath string) (ResolvedPath, bool) {
	parts := strings.Split(strings.TrimSpace(canonicalPath), ".")
	if len(parts) == 0 || parts[0] == "" {
		return ResolvedPath{}, false
	}
	def, ok := generatedDefinitions[resourceType]
	if !ok {
		return ResolvedPath{}, false
	}
	var currentProps []generatedProperty = def.Properties
	var current generatedProperty
	for _, part := range parts {
		name := strings.TrimSuffix(part, "[]")
		prop, ok := findGeneratedProperty(currentProps, name)
		if !ok {
			return ResolvedPath{}, false
		}
		current = prop
		switch prop.Kind {
		case "object":
			currentProps = childProperties(prop)
		case "array":
			currentProps = arrayChildProperties(prop)
		default:
			currentProps = nil
		}
	}
	return ResolvedPath{Path: canonicalPath, Property: current, PropertyRef: propertyRefName(current)}, true
}

func ResolvesToCodeableConcept(resourceType, canonicalPath string) bool {
	resolved, ok := ResolvePath(resourceType, canonicalPath)
	return ok && resolved.PropertyRef == "CodeableConcept"
}

func flattenDefinition(defName string, depth int, stack map[string]bool) []FieldSpec {
	if depth >= maxSelectorFieldDepth || stack[defName] {
		return nil
	}
	def, ok := generatedDefinitions[defName]
	if !ok {
		return nil
	}
	stack[defName] = true
	defer delete(stack, defName)
	return flattenProperties(def.Properties, depth, stack)
}

func flattenProperties(props []generatedProperty, depth int, stack map[string]bool) []FieldSpec {
	containerPredicates := predicatePaths(props)
	out := map[string]FieldSpec{}
	for _, prop := range props {
		if ignoreProperty(prop.Name) {
			continue
		}
		for _, field := range flattenProperty(prop, depth, containerPredicates, stack) {
			out[field.Path] = field
		}
	}
	fields := make([]FieldSpec, 0, len(out))
	for _, field := range out {
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
	return fields
}

func flattenProperty(prop generatedProperty, depth int, containerPredicates []string, stack map[string]bool) []FieldSpec {
	if depth >= maxSelectorFieldDepth {
		return nil
	}
	switch prop.Kind {
	case "scalar", "string", "number", "boolean", "integer":
		return []FieldSpec{newField(prop.Name, "scalar", containerPredicates)}
	case "object":
		return flattenObjectProperty(prop.Name, prop.Ref, prop.Properties, depth, stack)
	case "array":
		return flattenArrayProperty(prop, depth, containerPredicates, stack)
	default:
		return nil
	}
}

func flattenObjectProperty(name, ref string, inline []generatedProperty, depth int, stack map[string]bool) []FieldSpec {
	var childFields []FieldSpec
	switch {
	case ref != "":
		childFields = flattenDefinition(ref, depth+1, stack)
	case len(inline) > 0:
		childFields = flattenProperties(inline, depth+1, stack)
	}
	return prefixFields(name, childFields)
}

func flattenArrayProperty(prop generatedProperty, depth int, containerPredicates []string, stack map[string]bool) []FieldSpec {
	arrayPath := prop.Name + "[]"
	switch {
	case prop.ItemKind == "scalar" || (prop.ItemKind == "" && prop.ItemRef == "" && len(prop.ItemProperties) == 0):
		return []FieldSpec{newField(arrayPath, "array", containerPredicates)}
	case prop.ItemRef != "":
		return prefixFields(arrayPath, flattenDefinition(prop.ItemRef, depth+1, stack))
	case len(prop.ItemProperties) > 0:
		return prefixFields(arrayPath, flattenProperties(prop.ItemProperties, depth+1, stack))
	default:
		return []FieldSpec{newField(arrayPath, "array", containerPredicates)}
	}
}

func prefixFields(prefix string, fields []FieldSpec) []FieldSpec {
	out := make([]FieldSpec, 0, len(fields))
	for _, field := range fields {
		path := prefix + "." + field.Path
		sourcePath, valuePath := selectorParts(path)
		out = append(out, FieldSpec{Path: path, SourcePath: sourcePath, ValuePath: valuePath, Kind: field.Kind, PredicatePaths: cloneStrings(field.PredicatePaths)})
	}
	return out
}

func newField(path, kind string, predicatePaths []string) FieldSpec {
	sourcePath, valuePath := selectorParts(path)
	return FieldSpec{Path: path, SourcePath: sourcePath, ValuePath: valuePath, Kind: kind, PredicatePaths: cloneStrings(predicatePaths)}
}

func childProperties(prop generatedProperty) []generatedProperty {
	switch {
	case prop.Ref != "":
		if def, ok := generatedDefinitions[prop.Ref]; ok {
			return def.Properties
		}
	case len(prop.Properties) > 0:
		return prop.Properties
	}
	return nil
}

func arrayChildProperties(prop generatedProperty) []generatedProperty {
	switch {
	case prop.ItemRef != "":
		if def, ok := generatedDefinitions[prop.ItemRef]; ok {
			return def.Properties
		}
	case len(prop.ItemProperties) > 0:
		return prop.ItemProperties
	}
	return nil
}

func propertyRefName(prop generatedProperty) string {
	switch {
	case prop.Kind == "object":
		return prop.Ref
	case prop.Kind == "array":
		return prop.ItemRef
	default:
		return ""
	}
}

func findGeneratedProperty(props []generatedProperty, name string) (generatedProperty, bool) {
	for _, prop := range props {
		if prop.Name == name {
			return prop, true
		}
	}
	return generatedProperty{}, false
}

func predicatePaths(props []generatedProperty) []string {
	out := make([]string, 0, len(props))
	for _, prop := range props {
		if ignoreProperty(prop.Name) {
			continue
		}
		if prop.Kind == "array" {
			out = append(out, prop.Name+"[]")
			continue
		}
		out = append(out, prop.Name)
	}
	sort.Strings(out)
	return out
}

func ignoreProperty(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "_") {
		return true
	}
	switch name {
	case "resourceType", "fhir_comments", "links":
		return true
	default:
		return false
	}
}

func cloneFields(in []FieldSpec) []FieldSpec {
	out := make([]FieldSpec, len(in))
	for i := range in {
		out[i] = FieldSpec{Path: in[i].Path, SourcePath: in[i].SourcePath, ValuePath: in[i].ValuePath, Kind: in[i].Kind, PredicatePaths: cloneStrings(in[i].PredicatePaths)}
	}
	return out
}

func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}
