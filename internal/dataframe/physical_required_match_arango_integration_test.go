package dataframe

import (
	"context"
	"os"
	"strings"
	"testing"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

// TestPhysicalRequiredTraversalMatchesExplainAgainstArango keeps the P4
// execution contract tied to the real parser and optimizer. It is opt-in like
// the other compiler integration tests because it targets a developer-loaded
// META database. It covers both the normal parent-to-child INBOUND route and
// the explicitly proven ResearchSubject -> ResearchStudy OUTBOUND route.
func TestPhysicalRequiredTraversalMatchesExplainAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run compiler/Arango integration")
	}
	url, database, project := compilerArangoTarget()
	for _, test := range []struct {
		name      string
		root      string
		label     string
		target    string
		direction string
	}{
		{name: "inbound", root: "Patient", label: "subject_Patient", target: "Specimen", direction: "INBOUND"},
		{name: "proven outbound", root: "ResearchSubject", label: "study", target: "ResearchStudy", direction: "OUTBOUND"},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := CompileRequest(Builder{
				Project: project, RootResourceType: test.root,
				Traversals: []TraversalStep{{Label: test.label, ToResourceType: test.target, Alias: "required", MatchMode: TraversalMatchRequired}},
			}, 5)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(compiled.Query, "FOR required_0_node_0, required_0_edge_0 IN 1..1 "+test.direction+" root @@required_0_0_edge_collection") {
				t.Fatalf("required traversal did not use physical renderer:\n%s", compiled.Query)
			}
			if _, err := ExplainCompiledQuery(context.Background(), arangostore.ConnectionOptions{URL: url, Database: database}, compiled); err != nil {
				t.Fatalf("ExplainCompiledQuery() error = %v\nAQL:\n%s", err, compiled.Query)
			}
		})
	}
}
