package compilerfixture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestPublicCompilerAcceptsEveryGeneratedFHIRRoot(t *testing.T) {
	resourceTypes := fhirschema.ResourceTypes()
	if len(resourceTypes) == 0 {
		t.Fatal("generated FHIR schema advertises no resource roots")
	}
	for _, resourceType := range resourceTypes {
		t.Run(resourceType, func(t *testing.T) {
			compiled, err := compileRecipe(rootRecipe(resourceType), "compiler-oracle", 1, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatalf("compile recipe root %s: %v", resourceType, err)
			}
			assertOnlyValidatedRootCollection(t, compiled, resourceType)
			if compiled.PlanProfile != "generic_fhir_graph_recipe" {
				t.Fatalf("plan profile = %q, want generic_fhir_graph_recipe", compiled.PlanProfile)
			}
		})
	}
}

func TestPublicCompilerAcceptsEveryGeneratedBuilderTraversal(t *testing.T) {
	traversals := loadGeneratedBuilderTraversals(t)
	if len(traversals) == 0 {
		t.Fatal("no compiler-visible generated catalog traversals found")
	}
	for _, traversal := range traversals {
		name := traversal.FromType + "__" + traversal.EdgeLabel + "__" + traversal.ToType
		t.Run(name, func(t *testing.T) {
			compiled, err := compileRecipe(recipeWithTraversal(traversal.FromType, traversal.EdgeLabel, traversal.ToType), "compiler-oracle", 1, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatalf("compile recipe %s -> %s via %s: %v", traversal.FromType, traversal.ToType, traversal.EdgeLabel, err)
			}
			assertOnlyValidatedRootCollection(t, compiled, traversal.FromType)
			if compiled.PlanProfile != "generic_fhir_graph_recipe" {
				t.Fatalf("plan profile = %q, want generic_fhir_graph_recipe", compiled.PlanProfile)
			}
			if !bindVarsContain(compiled.BindVars, traversal.ToType) {
				t.Fatalf("target resource type %q is not represented as a bind value: %#v", traversal.ToType, compiled.BindVars)
			}
		})
	}
}

func TestPublicCompilerRejectsUnadvertisedRootBeforeRenderingAQL(t *testing.T) {
	malicious := "Patient REMOVE root IN Patient"
	if fhirschema.HasResource(malicious) {
		t.Fatal("test root unexpectedly advertised by schema")
	}
	compiled, err := compileRecipe(rootRecipe(malicious), "compiler-oracle", 1, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || !strings.Contains(err.Error(), "not represented by the active generated FHIR schema") {
		t.Fatalf("error = %v, want generated-schema rejection", err)
	}
	if compiled.Query != "" {
		t.Fatalf("unsafe root produced AQL: %s", compiled.Query)
	}
}

type graphSchemaDocument struct {
	Definitions map[string]struct {
		Links []struct {
			Relation string `json:"rel"`
			Target   struct {
				Reference string `json:"$ref"`
			} `json:"targetSchema"`
		} `json:"links"`
	} `json:"$defs"`
}

// loadGeneratedBuilderTraversals derives the same parent -> related-child
// orientation exposed by Loom's populated-reference catalog. A graph-schema
// link describes the stored forward FHIR reference (child -> parent), while
// the dataframe builder intentionally follows its generated reverse route
// (parent -> child) through INBOUND fhir_edge traversal. This coverage stays
// on that broad route family; the one separately proven forward contract
// (ResearchSubject --study--> ResearchStudy) has focused storage-route and
// extractor regressions. Other forward links remain unproven and must not be
// blessed by this generic coverage loop.
func loadGeneratedBuilderTraversals(t *testing.T) []fhirschema.CompilerTraversal {
	t.Helper()
	path := filepath.Join("..", "..", "schemas", "graph-fhir.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read active graph schema %s: %v", path, err)
	}
	var document graphSchemaDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode active graph schema: %v", err)
	}

	seen := map[string]fhirschema.CompilerTraversal{}
	for childType, definition := range document.Definitions {
		if !fhirschema.HasResource(childType) {
			continue
		}
		for _, link := range definition.Links {
			parentType := referenceName(link.Target.Reference)
			if !fhirschema.HasResource(parentType) {
				continue
			}
			traversal, found, err := fhirschema.ResolveCompilerTraversal(parentType, link.Relation, childType)
			if err != nil {
				t.Fatalf("normalize generated builder traversal %s -> %s (%s): %v", parentType, childType, link.Relation, err)
			}
			if !found {
				t.Fatalf("generated builder traversal missing from compiler API: %s -> %s (%s)", parentType, childType, link.Relation)
			}
			key := fmt.Sprintf("%s\x00%s\x00%s", traversal.FromType, traversal.EdgeLabel, traversal.ToType)
			seen[key] = traversal
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]fhirschema.CompilerTraversal, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out
}

func referenceName(reference string) string {
	if index := strings.LastIndex(reference, "/"); index >= 0 {
		return reference[index+1:]
	}
	return reference
}

var collectionIteration = regexp.MustCompile(`(?m)^FOR\s+[A-Za-z_][A-Za-z0-9_]*\s+IN\s+([A-Za-z_][A-Za-z0-9_]*)\s*$`)

func assertOnlyValidatedRootCollection(t *testing.T, compiled compiler.CompiledQuery, rootResourceType string) {
	t.Helper()
	matches := collectionIteration.FindAllStringSubmatch(compiled.Query, -1)
	if strings.Contains(compiled.Query, "FOR root IN @@root_collection") {
		if len(matches) != 0 || compiled.BindVars["@root_collection"] != rootResourceType {
			t.Fatalf("physical root collection = %#v / direct iterations %#v, want only validated root %q\nquery:\n%s", compiled.BindVars["@root_collection"], matches, rootResourceType, compiled.Query)
		}
		return
	}
	if len(matches) != 1 || matches[0][1] != rootResourceType {
		t.Fatalf("direct collection iterations = %#v, want only validated root %q\nquery:\n%s", matches, rootResourceType, compiled.Query)
	}
}

func bindVarsContain(bindVars map[string]any, expected string) bool {
	for _, value := range bindVars {
		if value == expected {
			return true
		}
	}
	return false
}
