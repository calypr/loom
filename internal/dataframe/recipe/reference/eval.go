// Package reference evaluates the storage-neutral recipe language over a
// caller-provided row and relationship resolver. It is deliberately generic:
// the evaluator knows only logical contexts and expressions, never FHIR types,
// graph collections, or ClickHouse tables.
package reference

import (
	"regexp"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

// Related resolves one logical traversal. Implementations may use Loom's
// generated relationship catalog, a fixture graph, or another backend.
type Related func(parent map[string]any, traversal recipe.Traversal) ([]map[string]any, error)

// EvaluateOutput evaluates one output for a root resource. One root can yield
// multiple rows when the recipe declares an expansion.

// DiscoverColumns evaluates dynamic column sources without emitting rows.
// Callers use the result to freeze a schema before materialization.

type context map[string]any

var invalidName = regexp.MustCompile(`[^A-Za-z0-9_]`)
