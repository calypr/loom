package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	explorerdb "github.com/calypr/loom/internal/explorer/arango/db"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/compilerresearch/reference"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	store "github.com/calypr/loom/internal/store/arango"
)

func main() {
	endpoint := flag.String("arango", "http://127.0.0.1:8529", "read-only Arango endpoint")
	database := flag.String("database", "", "existing owned compiler_oracle_ database")
	project := flag.String("project", "", "project bind used by compiled AQL")
	generation := flag.String("generation", "", "dataset generation bind used by compiled AQL")
	recipePath := flag.String("recipe", "", "v1 recipe bundle JSON path")
	documentsPath := flag.String("documents", "", "JSON array of raw FHIR payloads for interpreter input")
	flag.Parse()
	if *database == "" || !strings.HasPrefix(*database, "compiler_oracle_") {
		fatalf("database must be a dedicated compiler_oracle_ namespace")
	}
	if *project == "" || *generation == "" || *recipePath == "" || *documentsPath == "" {
		fatalf("project, generation, recipe, and documents are required")
	}
	bundleBytes, err := os.ReadFile(*recipePath)
	if err != nil {
		fatalf("read recipe: %v", err)
	}
	bundle, err := recipe.Parse(bundleBytes)
	if err != nil {
		fatalf("parse recipe: %v", err)
	}
	documentsBytes, err := os.ReadFile(*documentsPath)
	if err != nil {
		fatalf("read documents: %v", err)
	}
	var rawDocuments []map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(documentsBytes)))
	decoder.UseNumber()
	if err := decoder.Decode(&rawDocuments); err != nil {
		fatalf("decode documents: %v", err)
	}
	if len(bundle.Outputs) != 1 {
		fatalf("prototype currently expects one output, got %d", len(bundle.Outputs))
	}
	output := bundle.Outputs[0]
	referenceRows, err := reference.Evaluate(output, referenceDocuments(rawDocuments))
	if err != nil {
		fatalf("reference interpreter: %v", err)
	}

	bindings := recipe.RuntimeBindings{Project: *project, DatasetGeneration: *generation, GroupRowsCollections: explorerdb.GroupRowsCollections()}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		fatalf("build current recipe plan: %v", err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, *project, *generation)
	if err != nil {
		fatalf("resolve current recipe plan: %v", err)
	}
	lowered, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		fatalf("lower current recipe plan: %v", err)
	}
	compiled, err := compiler.CompileRecipeOutputWithPolicy(lowered.Outputs[0], bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		fatalf("compile current AQL: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := store.OpenExisting(ctx, *endpoint, *database)
	if err != nil {
		fatalf("open existing Arango database: %v", err)
	}
	defer client.Close(context.Background())
	var arangoRows []map[string]any
	if err := client.QueryRows(ctx, compiled.Query, 100, compiled.BindVars, func(row map[string]any) error { arangoRows = append(arangoRows, row); return nil }); err != nil {
		fatalf("execute compiler AQL: %v", err)
	}
	if err := compare(referenceRows, arangoRows, compiled.PublicColumns); err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "AQL:", compiled.Query)
		os.Exit(1)
	}
	fmt.Printf("PASS recipe=%s output=%s documents=%d rows=%d\n", bundle.Name, output.Name, len(rawDocuments), len(arangoRows))
}

func referenceDocuments(documents []map[string]any) []reference.Document {
	result := make([]reference.Document, 0, len(documents))
	for _, doc := range documents {
		result = append(result, reference.Document(doc))
	}
	return result
}

func compare(referenceRows []reference.Row, actualRows []map[string]any, columns []string) error {
	want := make([]string, 0, len(referenceRows))
	for _, row := range referenceRows {
		view := map[string]any{}
		for _, column := range columns {
			value, exists := row[column]
			if !exists {
				return fmt.Errorf("reference output lacks public column %q", column)
			}
			if value.Present {
				view[column] = value.Data
			} else {
				view[column] = nil
			}
		}
		want = append(want, canonical(view))
	}
	got := make([]string, 0, len(actualRows))
	for _, row := range actualRows {
		view := map[string]any{}
		for _, column := range columns {
			view[column] = row[column]
		}
		got = append(got, canonical(view))
	}
	sort.Strings(want)
	sort.Strings(got)
	if len(want) != len(got) {
		return fmt.Errorf("result row count differs: interpreter=%d arango=%d", len(want), len(got))
	}
	for i := range want {
		if want[i] != got[i] {
			return fmt.Errorf("result row %d differs: interpreter=%s arango=%s", i, want[i], got[i])
		}
	}
	return nil
}

func canonical(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
func fatalf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(2) }
