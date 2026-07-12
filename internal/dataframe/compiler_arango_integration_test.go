package dataframe

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

// TestGenericSpecimenPlanExplainsAndRunsAgainstArango is deliberately opt-in:
// it reads the locally loaded development FHIR database and never mutates it.
// Run with LOOM_COMPILER_ARANGO_INTEGRATION=1 after provisioning the documented
// local Arango/META fixture database.
func TestGenericSpecimenPlanExplainsAndRunsAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run compiler/Arango integration")
	}
	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project := os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}

	compiled, err := CompileRequest(Builder{
		Project:          project,
		RootResourceType: "Specimen",
		Filters: []TypedFilter{{
			FieldRef:   "Specimen.type_display",
			Selector:   "type.coding[].display",
			FieldKind:  FilterString,
			Repeated:   true,
			Quantifier: QuantifierAny,
			Operator:   FilterExists,
		}},
		Fields: []FieldSelect{
			{Name: "specimen_type", Select: "type.coding[].display"},
		},
		Traversals: []TraversalStep{{
			Label:          "subject_Specimen",
			ToResourceType: "DocumentReference",
			Alias:          "file",
			Filters: []TypedFilter{{
				FieldRef:   "DocumentReference.file_name",
				Selector:   "content[].attachment.title",
				FieldKind:  FilterString,
				Repeated:   true,
				Quantifier: QuantifierAny,
				Operator:   FilterExists,
			}},
			Aggregates: []AggregateSelect{{Name: "file_count", Operation: "COUNT"}},
		}},
	}, 5)
	if err != nil {
		t.Fatalf("CompileRequest() error = %v", err)
	}
	if compiled.PlanProfile != "generic_fhir_graph" {
		t.Fatalf("plan profile = %q, want generic_fhir_graph", compiled.PlanProfile)
	}
	t.Logf("compiled generic AQL:\n%s", compiled.Query)

	opts := arangostore.ConnectionOptions{URL: url, Database: database}
	explain, err := ExplainCompiledQuery(context.Background(), opts, compiled)
	if err != nil {
		t.Fatalf("ExplainCompiledQuery() error = %v", err)
	}
	if explain.Plan == nil {
		t.Fatalf("explain returned no selected plan: %#v", explain)
	}
	if uses := arangostore.ExtractPlanIndexes(explain); len(uses) == 0 {
		t.Fatalf("generic compiler plan used no reported indexes: %#v", explain.Plan)
	} else {
		t.Logf("EXPLAIN indexes: %#v", uses)
	}
	assessment := arangostore.AssessExplainResult(explain)
	t.Logf("EXPLAIN assessment: plans=%#v fullCollectionScans=%#v optimizerRules=%#v warnings=%#v", assessment.Plans, assessment.FullCollectionScans, assessment.AppliedOptimizerRules, assessment.Warnings)
	if !hasExplainIndex(assessment, "fhir_edge", []string{"_to"}) {
		t.Fatalf("generic inbound traversal did not use fhir_edge's _to edge index: assessment=%#v", assessment)
	}

	rows := 0
	err = ExecuteQueryRows(context.Background(), ExecuteQueryOptions{
		ConnectionOptions: opts,
		BatchSize:         5,
	}, compiled.Query, compiled.BindVars, func(map[string]any) error {
		rows++
		return nil
	})
	if err != nil {
		t.Fatalf("ExecuteQueryRows() error = %v", err)
	}
	if rows == 0 {
		t.Fatalf("generic Specimen plan returned no rows for project %q", project)
	}
}

// TestGenericRootPreviewUsesScopedSortIndexAgainstArango protects the index
// family that makes a generic, root-grain preview viable. The loader now
// provisions this index for every resource collection; Patient is used here
// because the checked-in META fixture already contains it.
func TestGenericRootPreviewUsesScopedSortIndexAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run compiler/Arango integration")
	}
	url, database, project := compilerArangoTarget()
	compiled, err := CompileRequest(Builder{
		Project:          project,
		RootResourceType: "Patient",
	}, 5)
	if err != nil {
		t.Fatalf("CompileRequest() error = %v", err)
	}
	if !strings.Contains(compiled.Query, "FOR root IN @@root_collection") {
		t.Fatalf("root preview did not use the physical execution renderer:\n%s", compiled.Query)
	}
	explain, err := ExplainCompiledQuery(context.Background(), arangostore.ConnectionOptions{URL: url, Database: database}, compiled)
	if err != nil {
		t.Fatalf("ExplainCompiledQuery() error = %v", err)
	}
	assessment := arangostore.AssessExplainResult(explain)
	if !hasScopedRootPreviewIndex(assessment, "Patient") {
		t.Fatalf("root preview did not use the project/_key index; assessment=%#v", assessment)
	}
}

// TestRenderedGenericPhysicalNavigationExplainsAgainstArango verifies the
// typed navigation renderer against the real Arango parser without replacing
// the legacy dataframe renderer yet. It protects collection-bind handling,
// scoped inbound traversal syntax, and root-row-preserving LET subqueries.
// The navigation set is intentionally not projected yet, so Arango may prune
// it; this test verifies parseability and scoped-root index selection rather
// than requiring an unused traversal to execute.
func TestRenderedGenericPhysicalNavigationExplainsAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run compiler/Arango integration")
	}
	url, database, project := compilerArangoTarget()
	physical, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1,
		Project: project,
		Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildGenericPhysicalPlan() error = %v", err)
	}
	rendered, err := RenderPhysicalPlan(physical)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	explain, err := ExplainCompiledQuery(context.Background(), arangostore.ConnectionOptions{URL: url, Database: database}, CompiledQuery{
		Query:    rendered.Query,
		BindVars: rendered.BindVars,
	})
	if err != nil {
		t.Fatalf("Explain rendered physical navigation: %v\nAQL:\n%s", err, rendered.Query)
	}
	assessment := arangostore.AssessExplainResult(explain)
	if len(assessment.FullCollectionScans) != 0 {
		t.Fatalf("rendered physical navigation unexpectedly full-scanned a collection: assessment=%#v", assessment)
	}
	if !hasScopedRootPreviewIndex(assessment, "Patient") {
		t.Fatalf("rendered physical navigation did not use a scoped root index; assessment=%#v", assessment)
	}
}

// BenchmarkGenericCompilerAgainstArango measures two different costs against
// the locally loaded META fixture: AQL compilation in-process and execution
// of the already-compiled AQL over one reusable Arango client. The latter is
// intentionally an end-to-end database benchmark, not a Go allocation proxy.
// It is opt-in so normal unit test runs never require a local database.
func BenchmarkGenericCompilerAgainstArango(b *testing.B) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		b.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to benchmark against local Arango/META")
	}
	url, database, project := compilerArangoTarget()
	builder := genericMetaSpecimenBuilder(project)

	b.Run("compile_specimen_file", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := CompileRequest(builder, 25); err != nil {
				b.Fatal(err)
			}
		}
	})

	compiled, err := CompileRequest(builder, 25)
	if err != nil {
		b.Fatal(err)
	}
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close(context.Background())
	b.Run("execute_specimen_file", func(b *testing.B) {
		b.ReportAllocs()
		rows := 0
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rows = 0
			err := client.QueryRows(context.Background(), compiled.Query, 25, compiled.BindVars, func(map[string]any) error {
				rows++
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(rows), "rows/op")
	})
}

func genericMetaSpecimenBuilder(project string) Builder {
	return Builder{
		Project:          project,
		RootResourceType: "Specimen",
		Filters: []TypedFilter{{
			FieldRef: "Specimen.type_display", Selector: "type.coding[].display", FieldKind: FilterString,
			Repeated: true, Quantifier: QuantifierAny, Operator: FilterExists,
		}},
		Fields: []FieldSelect{{Name: "specimen_type", Select: "type.coding[].display"}},
		Traversals: []TraversalStep{{
			Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "file",
			Filters: []TypedFilter{{
				FieldRef: "DocumentReference.file_name", Selector: "content[].attachment.title", FieldKind: FilterString,
				Repeated: true, Quantifier: QuantifierAny, Operator: FilterExists,
			}},
			Aggregates: []AggregateSelect{{Name: "file_count", Operation: "COUNT"}},
		}},
	}
}

func compilerArangoTarget() (url, database, project string) {
	url = os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database = os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project = os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}
	return url, database, project
}

func executeCompiledRows(ctx context.Context, opts arangostore.ConnectionOptions, compiled CompiledQuery) ([]map[string]any, error) {
	rows := make([]map[string]any, 0)
	err := ExecuteQueryRows(ctx, ExecuteQueryOptions{ConnectionOptions: opts, BatchSize: 25}, compiled.Query, compiled.BindVars, func(row map[string]any) error {
		clone := make(map[string]any, len(row))
		for key, value := range row {
			clone[key] = value
		}
		rows = append(rows, clone)
		return nil
	})
	return rows, err
}

func sortRowsByKey(rows []map[string]any) {
	sort.Slice(rows, func(i, j int) bool {
		return fmt.Sprint(rows[i]["_key"]) < fmt.Sprint(rows[j]["_key"])
	})
}

func firstRowDifference(genericRows, specializedRows []map[string]any) string {
	if len(genericRows) != len(specializedRows) {
		return fmt.Sprintf("row count generic=%d specialized=%d", len(genericRows), len(specializedRows))
	}
	for index := range genericRows {
		generic := genericRows[index]
		specialized := specializedRows[index]
		if key := fmt.Sprint(generic["_key"]); key != fmt.Sprint(specialized["_key"]) {
			return fmt.Sprintf("row %d key generic=%q specialized=%q", index, key, fmt.Sprint(specialized["_key"]))
		}
		keys := make([]string, 0, len(generic)+len(specialized))
		seen := map[string]bool{}
		for key := range generic {
			seen[key] = true
			keys = append(keys, key)
		}
		for key := range specialized {
			if !seen[key] {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			if reflect.DeepEqual(generic[key], specialized[key]) {
				continue
			}
			return fmt.Sprintf("row %q field %q generic=%s specialized=%s", fmt.Sprint(generic["_key"]), key, summarizeParityValue(generic[key]), summarizeParityValue(specialized[key]))
		}
	}
	return ""
}

func summarizeParityValue(value any) string {
	rv := reflect.ValueOf(value)
	if rv.IsValid() && (rv.Kind() == reflect.Array || rv.Kind() == reflect.Slice) {
		limit := rv.Len()
		if limit > 3 {
			limit = 3
		}
		prefix := make([]any, 0, limit)
		for index := 0; index < limit; index++ {
			prefix = append(prefix, rv.Index(index).Interface())
		}
		return fmt.Sprintf("%T(len=%d, prefix=%#v)", value, rv.Len(), prefix)
	}
	return fmt.Sprintf("%#v", value)
}

func hasExplainIndex(assessment arangostore.ExplainAssessment, collection string, fields []string) bool {
	for _, index := range assessment.Indexes {
		if index.Collection == collection && reflect.DeepEqual(index.Fields, fields) {
			return true
		}
	}
	return false
}

func hasScopedRootPreviewIndex(assessment arangostore.ExplainAssessment, collection string) bool {
	for _, index := range assessment.Indexes {
		if index.Collection != collection || len(index.Fields) < 2 {
			continue
		}
		if index.Fields[0] == "project" && index.Fields[len(index.Fields)-1] == "_key" {
			return true
		}
	}
	return false
}
