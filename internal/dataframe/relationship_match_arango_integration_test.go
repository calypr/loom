package dataframe

import (
	"context"
	"os"
	"testing"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

// TestRequiredTraversalMatchExplainsAndRunsAgainstArango verifies that the
// root-correlated bounded subquery is accepted by the real Arango parser and
// executes against the locally loaded META fixture. It remains opt-in because
// it reads a developer's local database.
func TestRequiredTraversalMatchExplainsAndRunsAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run compiler/Arango integration")
	}
	url, database, project := compilerArangoTarget()
	compiled, err := CompileRequest(Builder{
		Project:          project,
		RootResourceType: "Specimen",
		Traversals: []TraversalStep{{
			Label:          "subject_Specimen",
			ToResourceType: "DocumentReference",
			Alias:          "file",
			MatchMode:      TraversalMatchRequired,
			Filters: []TypedFilter{{
				FieldRef:   "DocumentReference.file_name",
				Selector:   "content[].attachment.title",
				FieldKind:  FilterString,
				Repeated:   true,
				Quantifier: QuantifierAny,
				Operator:   FilterExists,
			}},
		}},
	}, 5)
	if err != nil {
		t.Fatalf("CompileRequest() error = %v", err)
	}
	if _, err := ExplainCompiledQuery(context.Background(), arangostore.ConnectionOptions{URL: url, Database: database}, compiled); err != nil {
		t.Fatalf("ExplainCompiledQuery() error = %v\nAQL:\n%s", err, compiled.Query)
	}
	rows, err := executeCompiledRows(context.Background(), arangostore.ConnectionOptions{URL: url, Database: database}, compiled)
	if err != nil {
		t.Fatalf("execute required relationship match: %v\nAQL:\n%s", err, compiled.Query)
	}
	if len(rows) == 0 {
		t.Fatalf("required relationship match returned no rows for project %q", project)
	}
}
