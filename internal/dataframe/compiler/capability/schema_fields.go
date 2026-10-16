package capability

import (
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// SchemaFieldDescriptor is structural metadata from the active generated FHIR
// definitions. It deliberately carries neither population observations nor
// compiler-probed operations; route selection owns the exact compiler proof.
type SchemaFieldDescriptor struct {
	ResourceType  string   `json:"resourceType"`
	Path          string   `json:"path"`
	PrimitiveType string   `json:"primitiveType"`
	Cardinality   string   `json:"cardinality"`
	RepeatedPaths []string `json:"repeatedPaths,omitempty"`
}

// SchemaFieldDescriptors returns bounded-depth generated primitive paths for
// one concrete resource type. Arrays remain MANY at every nested scalar path,
// and repeated path identities are reported without inferred widths.
func SchemaFieldDescriptors(resourceType string) []SchemaFieldDescriptor {
	canonical, ok := fhirschema.ConcreteResourceType(resourceType)
	if !ok {
		return []SchemaFieldDescriptor{}
	}
	fields := fhirschema.FieldsForResource(canonical)
	out := make([]SchemaFieldDescriptor, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		path := strings.TrimSpace(field.Path)
		if path == "" || field.Kind != "scalar" && field.Kind != "array" {
			continue
		}
		metadata, ok := fhirschema.ResolveTerminalScalarMetadata(canonical, path)
		if !ok || metadata.Primitive == fhirschema.PrimitiveUnknown {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		cardinality := string(spec.CardinalityOptionalOne)
		if metadata.Repeated {
			cardinality = string(spec.CardinalityMany)
		}
		out = append(out, SchemaFieldDescriptor{
			ResourceType: canonical, Path: path, PrimitiveType: string(metadata.Primitive),
			Cardinality: cardinality, RepeatedPaths: repeatedPathPrefixes(path),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func repeatedPathPrefixes(path string) []string {
	parts := strings.Split(path, ".")
	prefix := make([]string, 0, len(parts))
	repeated := make([]string, 0, 2)
	for _, part := range parts {
		prefix = append(prefix, part)
		if strings.HasSuffix(part, "[]") {
			repeated = append(repeated, strings.Join(prefix, "."))
		}
	}
	return repeated
}
