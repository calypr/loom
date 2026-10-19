package compiler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

const semanticGroupProjectionFactoredRawAlias = "_semantic_group_projection_distinct_raw"

func TestSemanticGroupProjectionFactorizationMatchesExactArangoOutputs(t *testing.T) {
	ctx, client := openSemanticGroupProjectionArango(t)
	const targetSystem, targetVersion, codingPath = "urn:loom:group-projection-factorization", "v1", "component[].code.coding[]"
	bindingID, familyID, ruleVersion := semanticGroupProjectionFixtureBinding(t, targetSystem, targetVersion, codingPath)

	t.Run("high_entropy_projection_set", func(t *testing.T) {
		project, generation := "loom_group_projection_high_entropy_"+uuid.NewString(), "generation_"+uuid.NewString()
		registerSemanticGroupProjectionCleanup(t, client, project, generation)

		const projectionCount = 520
		payloads := make([]map[string]any, 0, projectionCount+1)
		for index := 0; index < projectionCount; index++ {
			uniqueCode := fmt.Sprintf("high-entropy-%04d", index)
			payloads = append(payloads, semanticGroupProjectionRow(
				fmt.Sprintf("entropy_%04d", index), "/included",
				[]any{semanticGroupProjectionComponent(targetSystem, targetVersion,
					[]semanticGroupProjectionCoding{
						{code: "high-common", display: "High common"},
						{code: uniqueCode, display: "High shared label"},
					})},
			))
		}
		// This otherwise matching projection must be removed by the source scope
		// before either the assessment sample or the weighted Group is evaluated.
		payloads = append(payloads, semanticGroupProjectionRow(
			"blocked_entropy", "/blocked",
			[]any{semanticGroupProjectionComponent(targetSystem, targetVersion,
				[]semanticGroupProjectionCoding{{code: "blocked-entropy", display: "High shared label"}})},
		))
		insertSemanticGroupProjectionRows(t, ctx, client, project, generation, payloads)

		bindings := semanticGroupProjectionBindings(project, generation)
		output := semanticGroupProjectionOutput(t, recipe.ConstructionGroupMissingKeyGroup,
			bindingID, familyID, ruleVersion, targetSystem, targetVersion, codingPath)
		result := compileRunAndCaptureSemanticGroupProjectionPreview(t, ctx, client, bindings, output, 600)
		assertSemanticGroupProjectionRowsEqual(t, result.ordinaryRows, result.factoredRows)
		assertSemanticGroupProjectionRowsEqual(t, result.ordinaryRows, result.previewRows)
		assertSemanticGroupProjectionAssessment(t, result.assessmentRows, 512, 512, 512)
		assertSemanticGroupProjectionIndexDescriptor(t, result.ordinaryQuery, "Observation", "component[].code.coding[]")
		assertSemanticGroupProjectionScopeCount(t, ctx, client, result.ordinaryQuery, projectionCount)
		assertSemanticGroupHighEntropyExpected(t, result.previewRows)
	})

	t.Run("low_diversity_prefix_then_unique_projections", func(t *testing.T) {
		project, generation := "loom_group_projection_low_diversity_"+uuid.NewString(), "generation_"+uuid.NewString()
		registerSemanticGroupProjectionCleanup(t, client, project, generation)

		const repeatedPrefixCount = 1024
		const uniqueTailCount = 520
		payloads := make([]map[string]any, 0, repeatedPrefixCount+uniqueTailCount+3)
		sharedProjection := semanticGroupProjectionComponent(targetSystem, targetVersion,
			[]semanticGroupProjectionCoding{
				{code: "low-common", display: "Z Common"},
				{code: "low-common", display: "A Common"},
				{code: "low-common", display: "A Common"},
				// Different tuple identities intentionally share one display.
				{code: "low-label-one", display: "Shared label"},
				{code: "low-label-two", display: "Shared label"},
			},
		)
		for index := 0; index < repeatedPrefixCount; index++ {
			payloads = append(payloads, semanticGroupProjectionRow(
				fmt.Sprintf("a_prefix_%04d", index), "/included", []any{sharedProjection},
			))
		}
		for index := 0; index < uniqueTailCount; index++ {
			payloads = append(payloads, semanticGroupProjectionRow(
				fmt.Sprintf("z_unique_%04d", index), "/included",
				[]any{semanticGroupProjectionComponent(targetSystem, targetVersion,
					[]semanticGroupProjectionCoding{
						{code: "low-common", display: "Z Common"},
						{code: "low-common", display: "A Common"},
						{code: fmt.Sprintf("low-unique-%04d", index), display: "Shared label"},
					})},
			))
		}
		// Both source rows are in scope but have no matching Coding tuple. GROUP
		// preserves them as one missing-category group; EXCLUDE drops them and
		// ERROR rejects the query.
		payloads = append(payloads,
			semanticGroupProjectionRow("zz_empty_coding", "/included", []any{}),
			semanticGroupProjectionRow("zz_no_matching_coding", "/included",
				[]any{semanticGroupProjectionComponent("urn:loom:other-system", targetVersion,
					[]semanticGroupProjectionCoding{{code: "unmatched", display: "Unmatched"}})}),
		)
		// This matching projection sorts before the repeated prefix but is outside
		// the allowed source scope and must not affect the bounded assessment.
		payloads = append(payloads, semanticGroupProjectionRow(
			"000_blocked", "/blocked", []any{sharedProjection},
		))
		insertSemanticGroupProjectionRows(t, ctx, client, project, generation, payloads)

		bindings := semanticGroupProjectionBindings(project, generation)
		groupOutput := semanticGroupProjectionOutput(t, recipe.ConstructionGroupMissingKeyGroup,
			bindingID, familyID, ruleVersion, targetSystem, targetVersion, codingPath)
		result := compileRunAndCaptureSemanticGroupProjectionPreview(t, ctx, client, bindings, groupOutput, 600)
		assertSemanticGroupProjectionRowsEqual(t, result.ordinaryRows, result.factoredRows)
		assertSemanticGroupProjectionRowsEqual(t, result.ordinaryRows, result.previewRows)
		// The sample is bounded and unordered. The exact preview must still
		// include every unique projection after its 512-row assessment window.
		assertSemanticGroupProjectionAssessment(t, result.assessmentRows, 512, 1, 512)
		if uniqueTailCount <= 512 {
			t.Fatal("low-diversity fixture must place more unique projections after the bounded sample than the sample can contain")
		}
		assertSemanticGroupProjectionScopeCount(t, ctx, client, result.ordinaryQuery,
			repeatedPrefixCount+uniqueTailCount+2)
		assertSemanticGroupLowDiversityExpected(t, result.previewRows, repeatedPrefixCount, uniqueTailCount, true)
		excludedQuery := compileSemanticGroupProjectionPolicy(t, ctx, result.engine, bindings,
			semanticGroupProjectionOutput(t, recipe.ConstructionGroupMissingKeyExclude,
				bindingID, familyID, ruleVersion, targetSystem, targetVersion, codingPath), 600)
		excludedRows := querySemanticGroupProjectionRows(t, ctx, client, excludedQuery, 2000)
		assertSemanticGroupLowDiversityExpected(t, excludedRows, repeatedPrefixCount, uniqueTailCount, false)
		for _, row := range excludedRows {
			if row["category"] == nil {
				t.Fatalf("EXCLUDE retained a missing semantic category: %#v", row)
			}
		}

		errorQuery := compileSemanticGroupProjectionPolicy(t, ctx, result.engine, bindings,
			semanticGroupProjectionOutput(t, recipe.ConstructionGroupMissingKeyError,
				bindingID, familyID, ruleVersion, targetSystem, targetVersion, codingPath), 600)
		if err := client.QueryRowsWithMaxRuntime(ctx, errorQuery.Query, 2000, errorQuery.BindVars, 8*time.Second, func(map[string]any) error { return nil }); err == nil {
			t.Fatal("ERROR semantic Group accepted in-scope empty and nonmatching Coding arrays")
		}
	})
}

func TestSemanticGroupProjectionFactorizationSelectsReadOnlyIndexForScopedFixture(t *testing.T) {
	ctx, client := openSemanticGroupProjectionArango(t)
	const resourceType, targetSystem, targetVersion, codingPath = "Specimen", "https://cda.readthedocs.io/", "", "type.coding[]"
	project, generation := "loom_group_projection_index_"+uuid.NewString(), "generation_"+uuid.NewString()
	registerSemanticGroupProjectionCleanupForResource(t, client, resourceType, project, generation)
	bindingID, familyID, ruleVersion := semanticGroupProjectionFixtureBindingForResource(t, resourceType, targetSystem, targetVersion, codingPath)

	const repeatedRows = 700
	codings := []any{
		semanticGroupProjectionCodingValue(targetSystem, targetVersion, "alpha", "Z Alias"),
		semanticGroupProjectionCodingValue(targetSystem, targetVersion, "alpha", "A Alias"),
		semanticGroupProjectionCodingValue(targetSystem, targetVersion, "alpha", "A Alias"),
		semanticGroupProjectionCodingValue(targetSystem, targetVersion, "beta", "Shared label"),
		semanticGroupProjectionCodingValue(targetSystem, targetVersion, "gamma", "Shared label"),
	}
	rows := make([]map[string]any, 0, repeatedRows+1)
	for index := 0; index < repeatedRows; index++ {
		rows = append(rows, semanticGroupProjectionDirectCodingRow(
			fmt.Sprintf("scoped_%04d", index), "/included", codings,
		))
	}
	rows = append(rows, semanticGroupProjectionDirectCodingRow("blocked", "/blocked", codings))
	insertSemanticGroupProjectionRowsForResource(t, ctx, client, resourceType, project, generation, rows)

	bindings := semanticGroupProjectionBindings(project, generation)
	output := semanticGroupProjectionOutputForResource(t, resourceType, recipe.ConstructionGroupMissingKeyGroup,
		bindingID, familyID, ruleVersion, targetSystem, targetVersion, codingPath)
	result := compileRunAndCaptureSemanticGroupProjectionPreview(t, ctx, client, bindings, output, 10)
	assertSemanticGroupProjectionRowsEqual(t, result.ordinaryRows, result.factoredRows)
	assertSemanticGroupProjectionRowsEqual(t, result.ordinaryRows, result.previewRows)
	assertSemanticGroupProjectionAssessment(t, result.assessmentRows, 512, 1, 1)
	assertSemanticGroupProjectionScopeCount(t, ctx, client, result.ordinaryQuery, repeatedRows)
	assertSemanticGroupProjectionIndexDescriptor(t, result.ordinaryQuery, "Specimen", codingPath)
	assertSemanticGroupProjectionExpectedDisplayCounts(t, result.previewRows, map[string][]float64{
		"A Alias":      {float64(repeatedRows)},
		"Shared label": {float64(repeatedRows), float64(repeatedRows)},
	})
	assertProjectionSelectionCallbacks(t, result.ordinaryQuery, result.callbacks, "loom_pivot_preview_903397e30bbf044a", true)
}

func TestSemanticGroupProjectionFactorizationSelectsExistingIndexOnFullCDAReadOnly(t *testing.T) {
	ctx, client := openSemanticGroupProjectionArango(t)
	const resourceType, project, generation, codingPath = "Specimen", "loom_dev_cda_fhir", "cda-fhir-v1", "type.coding[]"
	const system, version = "https://cda.readthedocs.io/", ""
	bindingID, familyID, ruleVersion := semanticGroupProjectionFixtureBindingForResource(t, resourceType, system, version, codingPath)
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	output := semanticGroupProjectionOutputForResource(t, resourceType, recipe.ConstructionGroupMissingKeyGroup,
		bindingID, familyID, ruleVersion, system, version, codingPath)
	engine, resolved, ordinaryQuery, callbacks := prepareSemanticGroupProjectionPreview(t, ctx, client, bindings, output, 25)
	var previewRows []map[string]any
	summary, err := engine.PreviewOutput(ctx, resolved,
		execution.PreviewRequest{Output: output.Name, Limit: 25, IncludeRowIdentity: true},
		func(row map[string]any) error {
			previewRows = append(previewRows, row)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("execute read-only full-CDA Specimen semantic Group preview: %v", err)
	}
	if summary.RowCount != len(previewRows) || len(previewRows) == 0 {
		t.Fatalf("full-CDA preview summary = %+v with %d rows, want nonempty emitted rows", summary, len(previewRows))
	}
	assertProjectionSelectionCallbacks(t, ordinaryQuery, callbacks, "loom_pivot_preview_903397e30bbf044a", true)
}

func TestSemanticGroupProjectionFactorizationProjectsSimpleCodingArrayWhole(t *testing.T) {
	const system, version, codingPath = "urn:loom:group-projection-simple-path", "v1", "code.coding[]"
	bindingID, familyID, ruleVersion := semanticGroupProjectionFixtureBinding(t, system, version, codingPath)
	bindings := recipe.RuntimeBindings{Project: "group-projection-simple-project", DatasetGeneration: "group-projection-simple-generation"}
	output := semanticGroupProjectionOutput(t, recipe.ConstructionGroupMissingKeyGroup,
		bindingID, familyID, ruleVersion, system, version, codingPath)
	bundle := semanticGroupProjectionBundle(output)
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build simple-path semantic Group plan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("resolve simple-path semantic Group plan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower simple-path semantic Group plan: %v", err)
	}
	query, err := compiler.CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 32, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile simple-path semantic Group preview: %v", err)
	}
	if query.PreviewGroupProjection == nil {
		t.Fatalf("simple repeated Coding path did not qualify for projection factoring:\n%s", query.Query)
	}
	rootVariable := ""
	for _, operation := range query.PhysicalPlan.Operations {
		if operation.RootScan != nil {
			rootVariable = operation.RootScan.Variable
			break
		}
	}
	if rootVariable == "" {
		t.Fatalf("simple-path semantic Group plan has no root scan: %+v", query.PhysicalPlan.Operations)
	}
	arrayPath := fmt.Sprintf("%s.payload[%q][%q]", rootVariable, "code", "coding")
	wantArray := fmt.Sprintf("(IS_ARRAY(%s) ? %s : [])", arrayPath, arrayPath)
	assertSimpleSemanticGroupProjectionArray(t, "assessment", func() string {
		text, _, err := compiler.RenderPreviewGroupProjectionAssessment(query.PhysicalPlan, "", true)
		if err != nil {
			t.Fatalf("render simple-path assessment: %v", err)
		}
		return text
	}(), wantArray)
	assertSimpleSemanticGroupProjectionArray(t, "factored query", func() string {
		text, _, err := compiler.RenderPreviewGroupProjectionFactorization(query.PhysicalPlan, "", true)
		if err != nil {
			t.Fatalf("render simple-path factored query: %v", err)
		}
		return text
	}(), wantArray)
}

func assertSimpleSemanticGroupProjectionArray(t *testing.T, queryName, query, wantArray string) {
	t.Helper()
	if !strings.Contains(query, wantArray) || strings.Contains(query, "semantic_group_projection_path_") {
		t.Fatalf("%s did not keep the simple Coding array as one IS_ARRAY-normalized projection (want %q and no per-Coding path FOR):\n%s",
			queryName, wantArray, query)
	}
}

type semanticGroupProjectionRun struct {
	engine                 *execution.Engine
	ordinaryQuery          compiler.CompiledQuery
	ordinaryRows           []map[string]any
	factoredRows           []map[string]any
	assessmentRows         []map[string]any
	previewRows            []map[string]any
	capturedPreviewQueries []string
	callbacks              *semanticGroupProjectionCallbackCounts
}

type semanticGroupProjectionCallbackCounts struct {
	revisions             int
	indexLists            int
	explains              int
	collectionCount       int
	previewQueries        []string
	previewQueryDurations []time.Duration
	explainResults        []semanticGroupProjectionExplainResult
	listedIndexNames      []string
}

type semanticGroupProjectionExplainResult struct {
	query  string
	result arangostore.ExplainResult
}

func compileRunAndCaptureSemanticGroupProjectionPreview(
	t *testing.T,
	ctx context.Context,
	client *arangostore.Client,
	bindings recipe.RuntimeBindings,
	output recipe.Output,
	limit int,
) semanticGroupProjectionRun {
	t.Helper()
	engine, resolved, ordinaryQuery, callbacks := prepareSemanticGroupProjectionPreview(t, ctx, client, bindings, output, limit)
	ordinaryRows := querySemanticGroupProjectionRows(t, ctx, client, ordinaryQuery, 2000)

	factoredText, factoredBindVars, err := compiler.RenderPreviewGroupProjectionFactorization(ordinaryQuery.PhysicalPlan, "", false)
	if err != nil {
		t.Fatalf("render exact factored semantic Group projection query: %v", err)
	}
	factoredRows := querySemanticGroupProjectionRows(t, ctx, client,
		compiler.CompiledQuery{Query: factoredText, BindVars: factoredBindVars}, 2000)

	assessmentText, assessmentBindVars, err := compiler.RenderPreviewGroupProjectionAssessment(ordinaryQuery.PhysicalPlan, "", false)
	if err != nil {
		t.Fatalf("render bounded semantic Group projection assessment: %v", err)
	}
	assessmentRows := querySemanticGroupProjectionRows(t, ctx, client,
		compiler.CompiledQuery{Query: assessmentText, BindVars: assessmentBindVars}, 10)

	var previewRows []map[string]any
	summary, err := engine.PreviewOutput(ctx, resolved,
		execution.PreviewRequest{Output: output.Name, Limit: limit, IncludeRowIdentity: true},
		func(row map[string]any) error {
			previewRows = append(previewRows, row)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("execute production semantic Group preview: %v", err)
	}
	if summary.RowCount != len(previewRows) || summary.Truncated || !summary.Complete {
		t.Fatalf("production preview summary = %+v with %d rows, want complete exact output", summary, len(previewRows))
	}
	return semanticGroupProjectionRun{
		engine: engine, ordinaryQuery: ordinaryQuery, ordinaryRows: ordinaryRows,
		factoredRows: factoredRows, assessmentRows: assessmentRows, previewRows: previewRows,
		capturedPreviewQueries: append([]string(nil), callbacks.previewQueries...), callbacks: callbacks,
	}
}

func prepareSemanticGroupProjectionPreview(
	t *testing.T,
	ctx context.Context,
	client *arangostore.Client,
	bindings recipe.RuntimeBindings,
	output recipe.Output,
	limit int,
) (*execution.Engine, execution.Resolved, compiler.CompiledQuery, *semanticGroupProjectionCallbackCounts) {
	t.Helper()
	bundle := semanticGroupProjectionBundle(output)
	callbacks := &semanticGroupProjectionCallbackCounts{}
	engine, err := execution.New(execution.Config{
		Registry: semanticGroupProjectionRegistry{bundle: bundle},
		QueryRows: func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
			return client.QueryRows(ctx, query, batchSize, bindVars, visit)
		},
		PreviewQueryRows: func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
			callbacks.previewQueries = append(callbacks.previewQueries, query)
			started := time.Now()
			err := client.QueryRowsWithMaxRuntime(ctx, query, batchSize, bindVars, 8*time.Second, visit)
			callbacks.previewQueryDurations = append(callbacks.previewQueryDurations, time.Since(started))
			return err
		},
		PreviewCollectionRevision: func(ctx context.Context, collection string) (string, error) {
			callbacks.revisions++
			return client.CollectionRevision(ctx, collection)
		},
		PreviewExplainQuery: func(ctx context.Context, query string, bindVars map[string]any) (arangostore.ExplainResult, error) {
			callbacks.explains++
			result, err := client.Explain(ctx, arangostore.ExplainRequest{Query: query, BindVars: bindVars})
			callbacks.explainResults = append(callbacks.explainResults, semanticGroupProjectionExplainResult{query: query, result: result})
			return result, err
		},
		PreviewCoveringIndexes: func(ctx context.Context, collection string) ([]arangostore.PreviewCoveringIndex, error) {
			callbacks.indexLists++
			indexes, err := client.ListPreviewCoveringIndexes(ctx, collection)
			for _, index := range indexes {
				callbacks.listedIndexNames = append(callbacks.listedIndexNames, index.Name)
			}
			return indexes, err
		},
		PreviewCollectionCount: func(ctx context.Context, collection string) (int64, error) {
			callbacks.collectionCount++
			return client.CollectionCount(ctx, collection)
		},
	})
	if err != nil {
		t.Fatalf("create production recipe preview engine: %v", err)
	}
	resolved, err := engine.ResolveBundle(ctx, bundle, bindings)
	if err != nil {
		t.Fatalf("resolve semantic Group projection fixture: %v", err)
	}
	ordinaryQuery, err := compiler.CompileRecipeOutputWithPolicy(
		resolved.Compiled.Outputs[0], bindings, limit, ir.DefaultPhysicalOptimizationPolicy(),
	)
	if err != nil {
		t.Fatalf("compile ordinary semantic Group projection query: %v", err)
	}
	if ordinaryQuery.PreviewGroupProjection == nil {
		t.Fatalf("direct semantic Group has no exact source-scope projection evidence:\n%s", ordinaryQuery.Query)
	}
	return engine, resolved, ordinaryQuery, callbacks
}

func compileSemanticGroupProjectionPolicy(
	t *testing.T,
	ctx context.Context,
	engine *execution.Engine,
	bindings recipe.RuntimeBindings,
	output recipe.Output,
	limit int,
) compiler.CompiledQuery {
	t.Helper()
	resolved, err := engine.ResolveBundle(ctx, semanticGroupProjectionBundle(output), bindings)
	if err != nil {
		t.Fatalf("resolve semantic Group %s policy: %v", output.Construction.Steps[0].Operation.Group.MissingKeyPolicy, err)
	}
	query, err := compiler.CompileRecipeOutputWithPolicy(resolved.Compiled.Outputs[0], bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile semantic Group %s policy: %v", output.Construction.Steps[0].Operation.Group.MissingKeyPolicy, err)
	}
	return query
}

func semanticGroupProjectionBundle(output recipe.Output) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output},
	}
}

type semanticGroupProjectionRegistry struct{ bundle recipe.Bundle }

func (r semanticGroupProjectionRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	digest, err := r.bundle.Digest()
	return exec.Entry{Bundle: r.bundle, Digest: digest}, err
}

func (r semanticGroupProjectionRegistry) LoadRecipeVersion(context.Context, string, string) (exec.Entry, error) {
	return r.LoadRecipe(context.Background(), r.bundle.Name)
}

func semanticGroupProjectionOutput(
	t *testing.T,
	policy recipe.ConstructionGroupMissingKeyPolicy,
	bindingID, familyID, ruleVersion, system, version, codingPath string,
) recipe.Output {
	t.Helper()
	return semanticGroupProjectionOutputForResource(t, "Observation", policy,
		bindingID, familyID, ruleVersion, system, version, codingPath)
}

func semanticGroupProjectionOutputForResource(
	t *testing.T,
	resourceType string,
	policy recipe.ConstructionGroupMissingKeyPolicy,
	bindingID, familyID, ruleVersion, system, version, codingPath string,
) recipe.Output {
	t.Helper()
	return recipe.Output{
		Name: "semantic_group_projection_factorization", RootResourceType: resourceType, RowGrain: "resource",
		Fields: []recipe.Field{{Name: "source_id", ColumnID: "source_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "source_id", Name: "source_id", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_semantic_category", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_semantic_category", MissingKeyPolicy: policy,
					Keys: []recipe.ConstructionGroupKey{
						{OutputColumnID: "category_id", SemanticCategory: &recipe.ConstructionGroupSemanticCategory{
							Kind: "BINDING", BindingID: bindingID, RuleVersion: ruleVersion,
							FamilyID: familyID, System: system, Version: version,
							SourceKind:   catalog.SemanticInventorySourceRetained,
							ResourceType: resourceType, CodingPath: codingPath,
						}},
					},
					Aggregates: []recipe.ConstructionGroupAggregate{{
						Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id",
					}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "category_id", Name: "category", Type: "string", Nullable: true},
					{ID: "rows_id", Name: "rows", Type: "integer"},
				},
			}},
		},
	}
}

func semanticGroupProjectionFixtureBinding(t *testing.T, system, version, codingPath string) (bindingID, familyID, ruleVersion string) {
	t.Helper()
	return semanticGroupProjectionFixtureBindingForResource(t, "Observation", system, version, codingPath)
}

func semanticGroupProjectionFixtureBindingForResource(t *testing.T, resourceType, system, version, codingPath string) (bindingID, familyID, ruleVersion string) {
	t.Helper()
	sourceID := "retained:" + resourceType + "/semantic-group-projection-binding-fixture"
	coding := semanticGroupProjectionCodingValue(system, version, "binding-witness", "Binding witness")
	payload := map[string]any{"resourceType": resourceType}
	switch codingPath {
	case "code.coding[]":
		payload["code"] = map[string]any{"coding": []any{coding}}
	case "type.coding[]":
		payload["type"] = map[string]any{"coding": []any{coding}}
	default:
		payload["component"] = []any{map[string]any{"code": map[string]any{"coding": []any{coding}}}}
	}
	var contribution catalog.SemanticInventoryContribution
	if err := catalog.NewSemanticInventoryEmitter("semantic-group-projection-fixture", "generation").ObservePayload(
		payload, resourceType, "", sourceID,
		func(value catalog.SemanticInventoryContribution) {
			if value.Observation.Source.Path == codingPath && value.Observation.Key.System == system && value.Observation.Key.Version == version {
				contribution = value
			}
		},
	); err != nil {
		t.Fatalf("derive semantic Coding binding for fixture: %v", err)
	}
	if contribution.BindingID == "" || contribution.Observation.Source.Path != codingPath {
		t.Fatalf("semantic Coding binding witness = %#v, want generated path %q", contribution, codingPath)
	}
	family := catalog.SemanticInventoryFamilyReference{
		BindingID: contribution.BindingID, SourceKind: catalog.SemanticInventorySourceRetained,
		ResourceType: resourceType, SourcePath: codingPath, System: system, Version: version,
	}
	familyID = catalog.SemanticInventoryFamilyID(family)
	return contribution.BindingID, familyID, strconv.Itoa(catalog.SemanticObservationRuleVersion)
}

func semanticGroupProjectionBindings(project, generation string) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/included"},
	}
}

func semanticGroupProjectionRow(id, authResourcePath string, components []any) map[string]any {
	return map[string]any{
		"id": id, "status": "active", "auth_resource_path": authResourcePath, "component": components,
	}
}

func semanticGroupProjectionDirectCodingRow(id, authResourcePath string, codings []any) map[string]any {
	return map[string]any{
		"id": id, "status": "active", "auth_resource_path": authResourcePath,
		"type": map[string]any{"coding": codings},
	}
}

type semanticGroupProjectionCoding struct{ code, display string }

func semanticGroupProjectionComponent(system, version string, codings []semanticGroupProjectionCoding) map[string]any {
	values := make([]any, 0, len(codings))
	for _, coding := range codings {
		values = append(values, map[string]any{
			"system": system, "version": version, "code": coding.code, "display": coding.display,
		})
	}
	return map[string]any{"code": map[string]any{"coding": values}}
}

func semanticGroupProjectionCodingValue(system, version, code, display string) map[string]any {
	coding := map[string]any{"system": system, "code": code, "display": display}
	if version != "" {
		coding["version"] = version
	}
	return coding
}

func openSemanticGroupProjectionArango(t *testing.T) (context.Context, *arangostore.Client) {
	t.Helper()
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	client, err := arangostore.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	return ctx, client
}

func insertSemanticGroupProjectionRows(
	t *testing.T,
	ctx context.Context,
	client *arangostore.Client,
	project, generation string,
	payloads []map[string]any,
) {
	t.Helper()
	insertSemanticGroupProjectionRowsForResource(t, ctx, client, "Observation", project, generation, payloads)
}

func insertSemanticGroupProjectionRowsForResource(
	t *testing.T,
	ctx context.Context,
	client *arangostore.Client,
	resourceType, project, generation string,
	payloads []map[string]any,
) {
	t.Helper()
	documents := make([]json.RawMessage, 0, len(payloads))
	for _, payload := range payloads {
		id, _ := payload["id"].(string)
		if id == "" {
			t.Fatalf("semantic Group fixture row has no ID: %#v", payload)
		}
		document := map[string]any{
			"_key": project + "_" + id, "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": resourceType, "payload": payload,
		}
		if authResourcePath, ok := payload["auth_resource_path"]; ok {
			document["auth_resource_path"] = authResourcePath
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encode semantic Group projection row: %v", err)
		}
		documents = append(documents, encoded)
	}
	if err := client.InsertBatchRaw(ctx, resourceType, documents, false, "document"); err != nil {
		t.Fatalf("insert semantic Group projection rows: %v", err)
	}
}

func registerSemanticGroupProjectionCleanup(t *testing.T, client *arangostore.Client, project, generation string) {
	t.Helper()
	registerSemanticGroupProjectionCleanupForResource(t, client, "Observation", project, generation)
}

func registerSemanticGroupProjectionCleanupForResource(t *testing.T, client *arangostore.Client, resourceType, project, generation string) {
	t.Helper()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN "+resourceType+" FILTER document.project == @project AND document.dataset_generation == @generation REMOVE document IN "+resourceType,
			map[string]any{"project": project, "generation": generation},
		); err != nil {
			t.Errorf("remove semantic Group factorization resources: %v", err)
		}
	})
}

func querySemanticGroupProjectionRows(
	t *testing.T,
	ctx context.Context,
	client *arangostore.Client,
	query compiler.CompiledQuery,
	maxRows int,
) []map[string]any {
	t.Helper()
	rows := make([]map[string]any, 0)
	if err := client.QueryRowsWithMaxRuntime(ctx, query.Query, maxRows, query.BindVars, 8*time.Second, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute semantic Group projection query: %v\n%s", err, query.Query)
	}
	return rows
}

func assertSemanticGroupProjectionRowsEqual(t *testing.T, expected, actual []map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(semanticGroupProjectionRowsByID(t, actual), semanticGroupProjectionRowsByID(t, expected)) {
		t.Fatalf("semantic Group rows differ:\nexpected=%#v\nactual=%#v",
			semanticGroupProjectionRowsByID(t, expected), semanticGroupProjectionRowsByID(t, actual))
	}
}

func semanticGroupProjectionRowsByID(t *testing.T, rows []map[string]any) map[string]map[string]any {
	t.Helper()
	byID := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		id, _ := row["__loom_row_id"].(string)
		if id == "" {
			t.Fatalf("semantic Group result has no stable row identity: %#v", row)
		}
		if _, duplicate := byID[id]; duplicate {
			t.Fatalf("semantic Group result repeats row identity %q", id)
		}
		byID[id] = row
	}
	return byID
}

func assertSemanticGroupProjectionAssessment(t *testing.T, rows []map[string]any, wantSampleRows, minDistinct, maxDistinct int) {
	t.Helper()
	const maxSerializedCharacters = 256 * 1024 * 1024
	if len(rows) != 1 {
		t.Fatalf("projection assessment returned %d rows, want one bounded sample summary: %#v", len(rows), rows)
	}
	assessment := rows[0]
	distinct := semanticGroupProjectionNumber(assessment["distinctProjections"])
	if !semanticGroupProjectionNumberEqual(assessment["sampleRows"], float64(wantSampleRows)) ||
		distinct < float64(minDistinct) || distinct > float64(maxDistinct) ||
		semanticGroupProjectionNumber(assessment["serializedCharacters"]) <= 0 ||
		semanticGroupProjectionNumber(assessment["serializedCharacters"]) > maxSerializedCharacters {
		t.Fatalf("projection assessment = %#v, want sampleRows=%d distinctProjections in [%d,%d] and 0<serializedCharacters<=%d",
			assessment, wantSampleRows, minDistinct, maxDistinct, maxSerializedCharacters)
	}
}

func assertSemanticGroupProjectionIndexDescriptor(t *testing.T, query compiler.CompiledQuery, resourceType, codingPath string) {
	t.Helper()
	if query.PreviewGroupProjection == nil {
		t.Fatal("semantic Group query has no projection-factorization metadata")
	}
	descriptor := query.PreviewGroupProjection.ReadOnlyIndex
	if strings.Contains(strings.TrimSuffix(codingPath, "[]"), "[]") {
		if descriptor != nil {
			t.Fatalf("nested repeated semantic path unexpectedly has read-only index descriptor: %+v", descriptor)
		}
		return
	}
	if descriptor == nil {
		t.Fatalf("direct terminal Coding array for %s.%s has no read-only index descriptor", resourceType, codingPath)
	}
	wantFields := []string{"project", "dataset_generation", "auth_resource_path"}
	wantPath := "payload." + strings.TrimSuffix(codingPath, "[]")
	if !reflect.DeepEqual(descriptor.RequiredFields, wantFields) || descriptor.ProjectionPath != wantPath {
		t.Fatalf("read-only projection index descriptor = %+v, want fields %#v and path %q",
			descriptor, wantFields, wantPath)
	}
}

func assertSemanticGroupProjectionScopeCount(
	t *testing.T,
	ctx context.Context,
	client *arangostore.Client,
	query compiler.CompiledQuery,
	want int,
) {
	t.Helper()
	if query.PreviewGroupProjection == nil {
		t.Fatal("semantic Group query lost its source-scope count evidence")
	}
	countQuery := compiler.CompiledQuery{
		Query:    query.PreviewGroupProjection.ScopeCountQuery,
		BindVars: query.PreviewGroupProjection.ScopeCountBindVars,
	}
	rows := querySemanticGroupProjectionRows(t, ctx, client, countQuery, 10)
	if len(rows) != 1 || !semanticGroupProjectionNumberEqual(rows[0]["count"], float64(want)) {
		t.Fatalf("exact source-scope count = %#v, want %d scoped rows", rows, want)
	}
}

func assertProjectionSelectionCallbacks(
	t *testing.T,
	ordinaryQuery compiler.CompiledQuery,
	callbacks *semanticGroupProjectionCallbackCounts,
	selectedIndex string,
	wantSelectedIndex bool,
) {
	t.Helper()
	if ordinaryQuery.PreviewGroupProjection == nil {
		t.Fatal("ordinary semantic Group query has no exact scoped projection evidence")
	}
	if callbacks == nil || callbacks.indexLists == 0 || callbacks.explains == 0 {
		t.Fatalf("projection selector did not list and EXPLAIN existing indexes: %+v", callbacks)
	}
	scopeCountSeen, sampleSeen, factorizedFinalSeen, selectedIndexListed := false, false, false, false
	for _, name := range callbacks.listedIndexNames {
		if name == selectedIndex {
			selectedIndexListed = true
		}
	}
	for _, query := range callbacks.previewQueries {
		if query == ordinaryQuery.PreviewGroupProjection.ScopeCountQuery {
			scopeCountSeen = true
		}
		if strings.Contains(query, "distinctProjections") && strings.Contains(query, "sampleRows") {
			sampleSeen = true
		}
		if strings.Contains(query, semanticGroupProjectionFactoredRawAlias) {
			factorizedFinalSeen = true
		}
	}
	if !scopeCountSeen || !sampleSeen || !factorizedFinalSeen {
		t.Fatalf("projection selection evidence missing (scope-count=%t sample=%t factorized-final=%t); preview queries:\n%s",
			scopeCountSeen, sampleSeen, factorizedFinalSeen, strings.Join(callbacks.previewQueries, "\n---\n"))
	}
	if wantSelectedIndex && (!selectedIndexListed || !previewQueryUsesIndex(callbacks.previewQueries, selectedIndex) ||
		!hasCoveringFactoredIndexExplain(callbacks.explainResults, ordinaryQuery.PreviewGroupProjection.Collection, selectedIndex)) {
		explainQueries := make([]string, 0, len(callbacks.explainResults))
		for _, explained := range callbacks.explainResults {
			explainQueries = append(explainQueries, explained.query)
		}
		t.Fatalf("projection preview did not select a covering %q IndexNode (listed=%t); explain queries:\n%s\npreview queries:\n%s",
			selectedIndex, selectedIndexListed, strings.Join(explainQueries, "\n---\n"),
			strings.Join(callbacks.previewQueries, "\n---\n"))
	}
	if wantSelectedIndex {
		for _, query := range callbacks.previewQueries {
			if strings.Contains(query, semanticGroupProjectionFactoredRawAlias) && strings.Contains(query, selectedIndex) {
				t.Logf("selected semantic Group projection index: %s", selectedIndex)
				t.Logf("exact selected factored production AQL:\n%s", query)
				break
			}
		}
		for index, query := range callbacks.previewQueries {
			role := "final preview"
			switch {
			case query == ordinaryQuery.PreviewGroupProjection.ScopeCountQuery:
				role = "exact scope count"
			case strings.Contains(query, "distinctProjections") && strings.Contains(query, "sampleRows"):
				role = "projection assessment"
			case strings.Contains(query, semanticGroupProjectionFactoredRawAlias):
				role = "factored final preview"
			}
			if index < len(callbacks.previewQueryDurations) {
				t.Logf("semantic Group %s callback elapsed: %s", role, callbacks.previewQueryDurations[index])
			}
		}
	}
}

func previewQueryUsesIndex(queries []string, indexName string) bool {
	for _, query := range queries {
		if strings.Contains(query, semanticGroupProjectionFactoredRawAlias) && strings.Contains(query, indexName) {
			return true
		}
	}
	return false
}

func hasCoveringFactoredIndexExplain(records []semanticGroupProjectionExplainResult, collection, indexName string) bool {
	for _, record := range records {
		if !strings.Contains(record.query, semanticGroupProjectionFactoredRawAlias) || !strings.Contains(record.query, indexName) {
			continue
		}
		plans := append([]arangostore.ExplainPlan(nil), record.result.Plans...)
		if record.result.Plan != nil {
			plans = append(plans, *record.result.Plan)
		}
		for _, plan := range plans {
			for _, node := range plan.Nodes {
				if node.Type != "IndexNode" || node.Collection != collection ||
					node.IndexCoversProjections == nil || !*node.IndexCoversProjections ||
					node.IndexCoversOutProjections == nil || !*node.IndexCoversOutProjections {
					continue
				}
				for _, index := range node.Indexes {
					if index.Name == indexName {
						return true
					}
				}
			}
		}
	}
	return false
}

func assertSemanticGroupProjectionExpectedDisplayCounts(t *testing.T, rows []map[string]any, want map[string][]float64) {
	t.Helper()
	semanticGroupProjectionRowsByID(t, rows)
	actual := make(map[string]int, len(rows))
	for _, row := range rows {
		key := fmt.Sprintf("%v\x00%.0f", row["category"], semanticGroupProjectionNumber(row["rows"]))
		actual[key]++
	}
	expected := make(map[string]int, len(rows))
	for display, counts := range want {
		for _, count := range counts {
			key := fmt.Sprintf("%s\x00%.0f", display, count)
			expected[key]++
		}
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("semantic Group display/count outcomes = %#v, want %#v", actual, expected)
	}
}

func semanticGroupProjectionNumberEqual(value any, want float64) bool {
	return semanticGroupProjectionNumber(value) == want
}

func semanticGroupProjectionNumber(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case float32:
		return float64(number)
	case int:
		return float64(number)
	case int64:
		return float64(number)
	case uint64:
		return float64(number)
	default:
		return 0
	}
}

func assertSemanticGroupHighEntropyExpected(t *testing.T, rows []map[string]any) {
	t.Helper()
	if len(rows) != 521 {
		t.Fatalf("high-entropy semantic Group rows = %d, want one common tuple plus 520 unique tuples", len(rows))
	}
	common, sharedLabelRows := 0, 0
	sharedLabelIDs := make(map[string]struct{}, 520)
	for _, row := range rows {
		switch row["category"] {
		case "High common":
			common++
			if !semanticGroupProjectionNumberEqual(row["rows"], 520) {
				t.Errorf("high-entropy common tuple count = %#v, want 520", row["rows"])
			}
		case "High shared label":
			sharedLabelRows++
			if !semanticGroupProjectionNumberEqual(row["rows"], 1) {
				t.Errorf("high-entropy unique tuple count = %#v, want 1", row["rows"])
			}
			id, _ := row["__loom_row_id"].(string)
			if _, duplicate := sharedLabelIDs[id]; duplicate {
				t.Errorf("distinct high-entropy tuples share row identity %q", id)
			}
			sharedLabelIDs[id] = struct{}{}
		default:
			t.Errorf("unexpected high-entropy semantic category: %#v", row)
		}
	}
	if common != 1 || sharedLabelRows != 520 || len(sharedLabelIDs) != 520 {
		t.Fatalf("high-entropy tuple/display outcomes = common:%d shared-label:%d unique IDs:%d, want 1/520/520",
			common, sharedLabelRows, len(sharedLabelIDs))
	}
}

func assertSemanticGroupLowDiversityExpected(t *testing.T, rows []map[string]any, repeatedPrefixCount, uniqueTailCount int, wantMissing bool) {
	t.Helper()
	commonCount, missingCount := 0, 0
	sharedLabelCounts := make([]float64, 0, uniqueTailCount+2)
	sharedLabelIDs := make(map[string]struct{}, uniqueTailCount+2)
	for _, row := range rows {
		switch row["category"] {
		case "A Common":
			commonCount++
			if !semanticGroupProjectionNumberEqual(row["rows"], float64(repeatedPrefixCount+uniqueTailCount)) {
				t.Errorf("canonical common tuple count = %#v, want %d", row["rows"], repeatedPrefixCount+uniqueTailCount)
			}
		case "Shared label":
			sharedLabelCounts = append(sharedLabelCounts, semanticGroupProjectionNumber(row["rows"]))
			id, _ := row["__loom_row_id"].(string)
			if _, duplicate := sharedLabelIDs[id]; duplicate {
				t.Errorf("distinct tuples with the same display share row identity %q", id)
			}
			sharedLabelIDs[id] = struct{}{}
		case nil:
			missingCount++
			if !semanticGroupProjectionNumberEqual(row["rows"], 2) {
				t.Errorf("missing Coding tuple count = %#v, want 2", row["rows"])
			}
		default:
			t.Errorf("unexpected low-diversity semantic category: %#v", row)
		}
	}
	if commonCount != 1 || len(sharedLabelCounts) != uniqueTailCount+2 || len(sharedLabelIDs) != uniqueTailCount+2 {
		t.Fatalf("low-diversity semantic groups = common:%d shared labels:%d unique identities:%d, want 1/%d/%d",
			commonCount, len(sharedLabelCounts), len(sharedLabelIDs), uniqueTailCount+2, uniqueTailCount+2)
	}
	largeCounts, unitCounts := 0, 0
	for _, count := range sharedLabelCounts {
		switch {
		case count == float64(repeatedPrefixCount):
			largeCounts++
		case count == 1:
			unitCounts++
		default:
			t.Errorf("same-display Group count = %v, want %d or 1", count, repeatedPrefixCount)
		}
	}
	wantMissingCount := 0
	if wantMissing {
		wantMissingCount = 1
	}
	if largeCounts != 2 || unitCounts != uniqueTailCount || missingCount != wantMissingCount {
		t.Fatalf("low-diversity counts = repeated-display:%d singletons:%d missing:%d, want 2/%d/%d",
			largeCounts, unitCounts, missingCount, uniqueTailCount, wantMissingCount)
	}
}
