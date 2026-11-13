package catalog

import (
	"fmt"
	"sort"
	"testing"

	generatedschema "github.com/calypr/loom/generated/fhirschema"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestRelationshipRegistryHasNoDirectOnlyTraversals(t *testing.T) {
	var directOnly []string
	census := 0
	for key, spec := range generatedschema.Traversals {
		from, label, to := spec.FromType, spec.EdgeLabel, spec.ToType
		if triple(from, label, to) != key {
			t.Errorf("generated traversal record under key %q declares inconsistent fields %q", key, triple(from, label, to))
			continue
		}
		_, directFound, directErr := fhirschema.ResolveCompilerTraversal(from, label, to)
		if !directFound || directErr != nil {
			continue
		}
		census++
		if _, inverseFound, inverseErr := fhirschema.ResolveCompilerTraversal(to, label, from); !inverseFound || inverseErr != nil {
			directOnly = append(directOnly, triple(from, label, to))
		}
	}
	if directOnly != nil {
		sort.Strings(directOnly)
		t.Fatalf("generated relationship registry contains %d direct-only traversal(s) that would silently exercise the relationship-evidence direct-fallback branch: %v — handle deliberately (registry fix, or a live witness for the fallback branch reviewed with the arch-backend-07 owner); the stub variant in TestRelationshipEvidenceProjection is not a substitute for real registry coverage", len(directOnly), directOnly)
	}
	if census == 0 {
		t.Fatal("census selected zero resolvable generated traversals — the registry or the resolver is broken; a vacuous pass is not acceptance")
	}
	t.Logf("census: %d resolvable generated traversals, all inverse-reachable", census)
}

func triple(from, label, to string) string {
	return fmt.Sprintf("%s|%s|%s", from, label, to)
}
