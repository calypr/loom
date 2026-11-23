package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

func TestSourceGroupPruningKeepsCorrelatedPivotALLValuesAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Condition"}, {Name: "Observation"}, {Name: "Specimen"},
		{Name: "Patient"}, {Name: "ResearchSubject"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_tumor_group_liveness_"+uuid.NewString(), "generation-tumor-group-liveness"
	key := func(id string) string { return project + "_" + id }
	cleanupKeys := make(map[string][]string)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Condition", "Observation", "Specimen", "Patient", "ResearchSubject", "fhir_edge"} {
			keys := cleanupKeys[collection]
			if len(keys) == 0 {
				continue
			}
			query := fmt.Sprintf("FOR key IN @keys REMOVE key IN %s", collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"keys": keys}); err != nil {
				t.Errorf("remove Tumor Group liveness fixtures from %s: %v", collection, err)
			}
		}
	})
	resource := func(resourceID, resourceType, resourceProject, resourceGeneration, authPath string, payload map[string]any) json.RawMessage {
		t.Helper()
		cleanupKeys[resourceType] = append(cleanupKeys[resourceType], key(resourceID))
		payload["id"], payload["resourceType"] = resourceID, resourceType
		document, err := json.Marshal(map[string]any{
			"_key": key(resourceID), "id": resourceID, "project": resourceProject, "project_id": resourceProject,
			"dataset_generation": resourceGeneration, "resourceType": resourceType, "auth_resource_path": authPath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		return document
	}
	insert := func(collection string, documents []json.RawMessage) {
		t.Helper()
		if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
			t.Fatalf("insert Tumor Group liveness fixtures into %s: %v", collection, err)
		}
	}
	edge := func(edgeID, fromType, fromID, toType, toID, label, authPath string) json.RawMessage {
		t.Helper()
		cleanupKeys["fhir_edge"] = append(cleanupKeys["fhir_edge"], key(edgeID))
		document, err := json.Marshal(map[string]any{
			"_key": key(edgeID), "_from": fromType + "/" + key(fromID), "_to": toType + "/" + key(toID),
			"project": project, "project_id": project, "dataset_generation": generation, "auth_resource_path": authPath,
			"label": label, "from_type": fromType, "to_type": toType,
		})
		if err != nil {
			t.Fatal(err)
		}
		return document
	}

	conditions := make([]json.RawMessage, 0, 29)
	observations := make([]json.RawMessage, 0, 28)
	for index := 1; index <= 26; index++ {
		id := fmt.Sprintf("condition_%02d", index)
		conditions = append(conditions, resource(id, "Condition", project, generation, "/allowed", map[string]any{"onsetString": "1"}))
		observationID := fmt.Sprintf("observation_%02d", index)
		observations = append(observations, resource(observationID, "Observation", project, generation, "/allowed", map[string]any{}))
	}
	conditions = append(conditions,
		resource("condition_missing_edge", "Condition", project, generation, "/allowed", map[string]any{"onsetString": "2"}),
		resource("condition_denied", "Condition", project+"_foreign", generation, "/allowed", map[string]any{"onsetString": "1"}),
		resource("condition_old_generation", "Condition", project, "old-generation", "/allowed", map[string]any{"onsetString": "1"}),
	)
	observations = append(observations,
		resource("observation_denied_edge", "Observation", project, generation, "/allowed", map[string]any{}),
		resource("observation_denied_root", "Observation", project, generation, "/allowed", map[string]any{}),
	)
	insert("Condition", conditions)
	insert("Observation", observations)

	specimenCoding := func(display string, extra ...map[string]any) map[string]any {
		codings := []any{map[string]any{"system": "https://cda.readthedocs.io/", "code": "Tumor", "display": display}}
		for _, coding := range extra {
			codings = append(codings, coding)
		}
		return map[string]any{"type": map[string]any{"coding": codings}}
	}
	specimens := []json.RawMessage{
		resource("specimen_solid", "Specimen", project, generation, "/allowed", specimenCoding("solid tumor",
			map[string]any{"system": "https://cda.readthedocs.io/", "code": "Tumor", "display": "solid tumor"})),
		resource("specimen_blank_display", "Specimen", project, generation, "/allowed", specimenCoding("  ",
			map[string]any{"system": "https://cda.readthedocs.io/", "code": "Tumor", "version": "v1", "display": "wrong version"},
			map[string]any{"system": "urn:other", "code": "Tumor", "display": "wrong system"})),
		resource("specimen_late", "Specimen", project, generation, "/allowed", specimenCoding("late tumor")),
		resource("specimen_wrong_system", "Specimen", project, generation, "/allowed", map[string]any{"type": map[string]any{"coding": []any{
			map[string]any{"system": "urn:other", "code": "Tumor", "display": "foreign-system value"},
		}}}),
		resource("specimen_denied_edge", "Specimen", project, generation, "/allowed", specimenCoding("denied edge")),
		resource("specimen_denied_root", "Specimen", project, generation, "/forbidden", specimenCoding("denied root")),
		resource("specimen_foreign", "Specimen", project+"_foreign", generation, "/allowed", specimenCoding("foreign project")),
		resource("specimen_old_generation", "Specimen", project, "old-generation", "/allowed", specimenCoding("old generation")),
	}
	insert("Specimen", specimens)

	patients := []json.RawMessage{resource("patient_1", "Patient", project, generation, "/allowed", map[string]any{})}
	researchSubjects := []json.RawMessage{resource("research_subject_1", "ResearchSubject", project, generation, "/allowed", map[string]any{})}
	insert("Patient", patients)
	insert("ResearchSubject", researchSubjects)
	edges := make([]json.RawMessage, 0, 35)
	addEdge := func(edgeID, fromType, fromID, toType, toID, label, authPath string) {
		edges = append(edges, edge(edgeID, fromType, fromID, toType, toID, label, authPath))
	}
	for index := 1; index <= 26; index++ {
		conditionID, observationID := fmt.Sprintf("condition_%02d", index), fmt.Sprintf("observation_%02d", index)
		addEdge(fmt.Sprintf("condition_observation_%02d", index), "Condition", conditionID, "Observation", observationID, "stage_assessment_Observation", "/allowed")
	}
	addEdge("condition_observation_denied", "Condition", "condition_01", "Observation", "observation_denied_edge", "stage_assessment_Observation", "/forbidden")
	addEdge("condition_observation_denied_root", "Condition", "condition_denied", "Observation", "observation_denied_root", "stage_assessment_Observation", "/allowed")
	addEdge("condition_observation_old", "Condition", "condition_old_generation", "Observation", "observation_01", "stage_assessment_Observation", "/allowed")
	addEdge("observation_specimen_solid", "Observation", "observation_01", "Specimen", "specimen_solid", "specimen_Specimen", "/allowed")
	addEdge("observation_specimen_solid_duplicate", "Observation", "observation_01", "Specimen", "specimen_solid", "specimen_Specimen", "/allowed")
	addEdge("observation_specimen_blank", "Observation", "observation_01", "Specimen", "specimen_blank_display", "specimen_Specimen", "/allowed")
	addEdge("observation_specimen_wrong_system", "Observation", "observation_02", "Specimen", "specimen_wrong_system", "specimen_Specimen", "/allowed")
	addEdge("observation_specimen_late", "Observation", "observation_26", "Specimen", "specimen_late", "specimen_Specimen", "/allowed")
	addEdge("observation_specimen_denied", "Observation", "observation_denied_edge", "Specimen", "specimen_denied_edge", "specimen_Specimen", "/allowed")
	addEdge("observation_specimen_denied_root", "Observation", "observation_denied_root", "Specimen", "specimen_denied_root", "specimen_Specimen", "/allowed")
	addEdge("observation_specimen_foreign", "Observation", "observation_02", "Specimen", "specimen_foreign", "specimen_Specimen", "/allowed")
	addEdge("observation_specimen_old", "Observation", "observation_02", "Specimen", "specimen_old_generation", "specimen_Specimen", "/allowed")
	addEdge("condition_patient", "Condition", "condition_01", "Patient", "patient_1", "subject_Patient", "/allowed")
	addEdge("research_subject_patient", "ResearchSubject", "research_subject_1", "Patient", "patient_1", "subject_Patient", "/allowed")
	insert("fhir_edge", edges)

	binding := &fhirbinding.CorrelatedBinding{
		KeyPath: "type.coding[]", ValueScope: fhirbinding.CorrelatedValueScopeCoding,
		SystemPath: "system", CodePath: "code", VersionPath: "version",
		ValuePath: "display", ValueFallback: []string{"code"}, FallbackOnBlankString: true, LogicalType: "string",
	}
	output := tumorGroupLivenessOutput(binding)
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
	}
	compiled := lowerConstructionOutput(t, output, bindings)
	preview, err := CompileRecipeOutputWithPolicy(compiled, bindings, 1, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if sequence := preview.PhysicalPlan.StageSequence; sequence == nil || sequence.PreviewLimitBindKey == "" ||
		len(sequence.Stages) != 1 || sequence.Stages[0].Kind != ir.PhysicalStageGroupOp ||
		sequence.Stages[0].Group == nil || len(sequence.Stages[0].Group.RowValues) == 0 {
		t.Fatalf("limit-1 output is not the exact typed terminal GROUP row-value plan: %#v", preview.PhysicalPlan.StageSequence)
	}
	assertTumorGroupLivenessPreviewFrontier(t, preview.Query)

	if strings.Contains(preview.Query, "child_set_3") || strings.Contains(preview.Query, "child_set_4") || strings.Contains(preview.Query, "subject_Patient") {
		t.Fatalf("unselected optional Patient→ResearchSubject path survived source projection liveness:\n%s", preview.Query)
	}
	for _, retained := range []string{
		"child_set_1_label", "child_set_2_label", "child_set_1_scope_allowed", "child_set_2_scope_allowed",
		"auth_resource_paths", "dataset_generation", "FILTER __correlation_version == @",
	} {
		if !strings.Contains(preview.Query, retained) && !strings.Contains(fmt.Sprint(preview.BindVars), retained) {
			t.Errorf("live authorized Correlated Pivot query lost %q", retained)
		}
	}
	versionFilter := "FILTER __correlation_version == @"
	versionFilterStart := strings.Index(preview.Query, versionFilter)
	if versionFilterStart < 0 {
		t.Fatal("compiled correlated Pivot query omitted exact Coding.version matching")
	}
	versionBindKey := strings.FieldsFunc(preview.Query[versionFilterStart+len(versionFilter):], func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == ' ' || r == ')' || r == ';'
	})
	if len(versionBindKey) == 0 {
		t.Fatalf("compiled version filter has no bind key: %s", preview.Query[versionFilterStart:])
	}
	if got, ok := preview.BindVars[versionBindKey[0]]; !ok || got != "" {
		t.Fatalf("versionless Tumor key bind %q = %#v (present=%t), want exact empty version", versionBindKey[0], got, ok)
	}
	if got := preview.BindVars["auth_resource_paths"]; !reflect.DeepEqual(got, []string{"/allowed"}) {
		t.Fatalf("authorization paths bind = %#v, want restricted /allowed scope", got)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, preview)
	t.Logf("compiled limit-1 AQL rows: %#v", rows)
	if len(rows) != 1 || rows[0]["onset"] != "1" || !constructionNumericEqual(rows[0]["source_records"], 26) {
		t.Fatalf("limit-1 Group rows = %#v, want onset 1 with all 26 source records", rows)
	}
	if got, want := sortedAnyStrings(rows[0]["tumor_values"]), []string{"Tumor", "late tumor", "solid tumor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("limit-1 ALL values = %#v, want %#v (late value must survive post-GROUP limit)", got, want)
	}

	genericPlan := ir.ClonePhysicalPlan(preview.PhysicalPlan)
	genericPlan.StageSequence.PreviewLimitBindKey = ""
	genericRendered, err := aql.Render(genericPlan, aql.Options{})
	if err != nil {
		t.Fatalf("render generic full-source typed GROUP plan: %v", err)
	}
	allRows := executeReshapeOracleQuery(t, ctx, client, model.CompiledQuery{
		PhysicalPlan: genericPlan,
		Query:        genericRendered.Query,
		BindVars:     genericRendered.BindVars,
	})
	t.Logf("compiled all-groups AQL rows: %#v", allRows)
	if len(allRows) != 2 {
		t.Fatalf("all Group rows = %#v, want grouped and missing-edge rows", allRows)
	}
	if !reflect.DeepEqual(rows[0], allRows[0]) {
		t.Fatalf("optimized preview row = %#v, want first generic full-source GROUP row %#v", rows[0], allRows[0])
	}
	byOnset := map[string]map[string]any{}
	for _, row := range allRows {
		if onset, ok := row["onset"].(string); ok {
			byOnset[onset] = row
		}
	}
	if !reflect.DeepEqual(sortedAnyStrings(byOnset["1"]["tumor_values"]), []string{"Tumor", "late tumor", "solid tumor"}) || !constructionNumericEqual(byOnset["1"]["source_records"], 26) {
		t.Fatalf("nonempty ALL group = %#v, want all deduplicated and correlated values from 26 contributors", byOnset["1"])
	}
	if values, ok := byOnset["2"]["tumor_values"].([]any); !ok || len(values) != 0 || !constructionNumericEqual(byOnset["2"]["source_records"], 1) {
		t.Fatalf("missing optional edge group = %#v, want one row with empty ALL values", byOnset["2"])
	}
}

func assertTumorGroupLivenessPreviewFrontier(t *testing.T, query string) {
	t.Helper()

	frontier := strings.Index(query, "COLLECT __loom_physical_construction_group_preview_group_key")
	if frontier < 0 {
		t.Fatalf("terminalGroupRowValuePreviewWindowFor did not activate for the captured typed plan; query has no Group-key frontier:\n%s", query)
	}
	frontierLimitOffset := strings.Index(query[frontier:], "LIMIT @limit")
	selectedRootKeys := strings.Index(query, "LET __loom_physical_construction_group_preview_selected_root_keys = FLATTEN(")
	rootKeyWindow := strings.Index(query, "LET root = DOCUMENT(@@root_collection, __loom_physical_preview_root_key)")
	correlatedMapWork := strings.Index(query, "LET __correlation_owner_pairs = (")
	groupStage := strings.Index(query, "LET __loom_construction_stage_1 = (")
	groupCollect := strings.LastIndex(query, "COLLECT ")
	finalPreviewLimit := strings.LastIndex(query, "LIMIT @limit")
	if frontierLimitOffset < 0 {
		t.Fatalf("Group-key frontier has no preview LIMIT:\n%s", query)
	}
	frontierLimit := frontier + frontierLimitOffset
	if selectedRootKeys < frontierLimit || rootKeyWindow < selectedRootKeys || correlatedMapWork < rootKeyWindow {
		t.Fatalf("key frontier must select complete root-key sets before correlated map work; frontier=%d limit=%d selected-root-keys=%d root-window=%d correlation=%d:\n%s",
			frontier, frontierLimit, selectedRootKeys, rootKeyWindow, correlatedMapWork, query)
	}
	if groupStage < correlatedMapWork || groupCollect < groupStage || finalPreviewLimit < groupCollect || finalPreviewLimit == frontierLimit {
		t.Fatalf("correlated map work and complete GROUP must precede the separate final preview LIMIT; correlation=%d stage=%d collect=%d final-limit=%d frontier-limit=%d:\n%s",
			correlatedMapWork, groupStage, groupCollect, finalPreviewLimit, frontierLimit, query)
	}
}

func sortedAnyStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil
		}
		result = append(result, text)
	}
	sort.Strings(result)
	return result
}

func tumorGroupLivenessOutput(binding *fhirbinding.CorrelatedBinding) recipe.Output {
	groupColumns := []recipe.StageColumn{
		{ID: "onset_group", Name: "onset", Type: "string"},
		{ID: "source_records", Name: "source_records", Type: "integer"},
		{ID: "tumor_values", Name: "tumor_values", Type: "array"},
	}
	return recipe.Output{
		Name: "tumor_group_liveness", RootResourceType: "Condition", RowGrain: "resource",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "onset", ColumnID: "onset_source", Expr: recipe.Expression{Select: "root.onsetString"}}},
		Traversals: []recipe.Traversal{
			{
				Name: "stage_assessment_Observation", Alias: "Observation", OccurrenceID: "observation", ToResourceType: "Observation", MatchMode: recipe.MatchOptional,
				Traversals: []recipe.Traversal{{
					Name: "specimen_Specimen", Alias: "Specimen", OccurrenceID: "specimen", ToResourceType: "Specimen", MatchMode: recipe.MatchOptional,
					Pivots: []recipe.Pivot{{
						Name: "tumor_pivot", Columns: []string{"Tumor"}, ColumnAliases: map[string]string{"Tumor": "tumor"}, ProjectionMode: "ALL",
						Correlation: binding, CorrelationSystem: "https://cda.readthedocs.io/", CorrelationCode: "Tumor", CorrelationVersion: "",
					}},
				}},
			},
			{
				Name: "subject_Patient", Alias: "Patient", OccurrenceID: "patient", ToResourceType: "Patient", MatchMode: recipe.MatchOptional,
				Traversals: []recipe.Traversal{{
					Name: "subject_Patient", Alias: "ResearchSubject", OccurrenceID: "research_subject", ToResourceType: "ResearchSubject", MatchMode: recipe.MatchOptional,
					Fields: []recipe.Field{{Name: "research_subject", ColumnID: "research_subject_source", Expr: recipe.Expression{Select: "ResearchSubject.id"}}},
				}},
			},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "onset_source", Name: "onset", Type: "string"},
				{ID: "tumor_source", Name: "tumor", Type: "string"},
				{ID: "research_subject_source", Name: "research_subject", Type: "string"},
			},
			Steps: []recipe.ConstructionStep{{
				ID: "group_onset", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_onset", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
					Keys:       []recipe.ConstructionGroupKey{{InputColumnID: "onset_source", OutputColumnID: "onset_group"}},
					Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "source_records"}},
				}},
				RowValues: []recipe.ConstructionRowValue{{InputColumnID: "tumor_source", OutputColumnID: "tumor_values", Policy: recipe.ConstructionRowValueAll}},
				Outputs:   groupColumns,
			}},
		},
	}
}

func TestConstructionPivotSourceProjectionLivenessAgainstBindOnlyArango(t *testing.T) {
	endpoint := arangotest.Shared(t).URL

	const project, generation = "pivot-source-liveness", "pivot-source-generation"
	output := pivotSourceLivenessOutput()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                output.Name,
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{output},
	}
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := CompileResolvedRecipePlanWithPolicy(resolved, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 {
		t.Fatalf("compiled outputs = %d, want one", len(queries))
	}
	query := queries[0]
	if query.PhysicalPlan.StageSequence == nil || query.PhysicalPlan.StageSequence.Stages[0].GroupedPivot == nil {
		t.Fatal("compiled query does not contain a source Pivot stage")
	}
	planBeforeRender := ir.ClonePhysicalPlan(query.PhysicalPlan)
	if _, err := aql.Render(query.PhysicalPlan, aql.Options{}); err != nil {
		t.Fatalf("render compiled Pivot plan: %v", err)
	}
	if !reflect.DeepEqual(query.PhysicalPlan, planBeforeRender) {
		t.Fatal("rendering the Pivot plan mutated its caller-owned plan")
	}

	records := []map[string]any{
		pivotSourceLivenessRecord("a-female-1", true, "allowed", map[string]any{"gender": "female", "multipleBirthInteger": 2}),
		pivotSourceLivenessRecord("a-female-2", true, "allowed", map[string]any{"gender": "female", "multipleBirthInteger": 4}),
		pivotSourceLivenessRecord("a-male", true, "allowed", map[string]any{"gender": "male", "multipleBirthInteger": 3}),
		pivotSourceLivenessRecord("a-null", true, "allowed", map[string]any{"gender": nil, "multipleBirthInteger": 5}),
		pivotSourceLivenessRecord("a-missing", true, "allowed", map[string]any{"multipleBirthInteger": 7}),
		pivotSourceLivenessRecord("b-female", false, "allowed", map[string]any{"gender": "female", "multipleBirthInteger": 10}),
		pivotSourceLivenessRecord("b-null", false, "allowed", map[string]any{"gender": nil, "multipleBirthInteger": 6}),
		pivotSourceLivenessRecord("b-missing", false, "allowed", map[string]any{"multipleBirthInteger": 8}),
	}
	foreignProject := pivotSourceLivenessRecord("foreign-project", true, "allowed", map[string]any{"gender": "female", "multipleBirthInteger": 99})
	foreignProject["project"], foreignProject["project_id"] = "other-project", "other-project"
	records = append(records, foreignProject)
	foreignGeneration := pivotSourceLivenessRecord("foreign-generation", true, "allowed", map[string]any{"gender": "female", "multipleBirthInteger": 99})
	foreignGeneration["dataset_generation"] = "old-generation"
	records = append(records, foreignGeneration)
	foreignAuthorization := pivotSourceLivenessRecord("foreign-auth", true, "denied", map[string]any{"gender": "female", "multipleBirthInteger": 99})
	records = append(records, foreignAuthorization)

	queryText := strings.Replace(query.Query, "FOR root IN @@root_collection", "FOR root IN @records", 1)
	if queryText == query.Query {
		t.Fatal("compiled query did not contain its direct root collection scan")
	}
	delete(query.BindVars, "@root_collection")
	query.BindVars["records"] = records
	query.BindVars["auth_resource_paths_unrestricted"] = false
	query.BindVars["auth_resource_paths"] = []string{"allowed"}
	rows := runPivotSourceLivenessBindQuery(t, endpoint, queryText, query.BindVars)
	for index := range rows {
		projected := make(map[string]any, 5)
		for _, name := range []string{"group", "female_amount", "male_amount", "null_amount", "missing_amount"} {
			projected[name] = rows[index][name]
		}
		rows[index] = projected
	}
	sort.Slice(rows, func(i, j int) bool { return fmt.Sprint(rows[i]["group"]) < fmt.Sprint(rows[j]["group"]) })
	want := []map[string]any{
		{"group": false, "female_amount": float64(10), "male_amount": nil, "null_amount": float64(6), "missing_amount": float64(8)},
		{"group": true, "female_amount": float64(6), "male_amount": float64(3), "null_amount": float64(5), "missing_amount": float64(7)},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("Pivot rows = %#v, want %#v", rows, want)
	}
}

func pivotSourceLivenessOutput() recipe.Output {
	output := constructionPreviewPivotOutput("root.status", false)
	output.Name = "pivot_source_projection_liveness"
	output.Fields[0] = recipe.Field{Name: "group", ColumnID: "group_id", Expr: recipe.Expression{Select: "root.active"}}
	output.Construction.SourceColumns[0] = recipe.StageColumn{ID: "group_id", Name: "group"}

	female, male := "female", "male"
	pivotStep := &output.Construction.Steps[0]
	pivot := pivotStep.Operation.Pivot
	pivot.GroupKeyIDs = []string{"group_id"}
	pivot.DuplicatePolicy = recipe.PivotDuplicateSum
	pivot.UnlistedCategoryPolicy = recipe.PivotUnlistedCategoryError
	pivot.Categories = []recipe.ConstructionPivotCategory{
		{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &female}, OutputColumnID: "female_amount_id"},
		{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &male}, OutputColumnID: "male_amount_id"},
		{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "null_amount_id"},
		{Key: recipe.TableScalar{Kind: recipe.TableScalarMissing}, OutputColumnID: "missing_amount_id"},
	}
	pivotStep.Outputs = []recipe.StageColumn{
		{ID: "group_id", Name: "group"},
		{ID: "female_amount_id", Name: "female_amount"},
		{ID: "male_amount_id", Name: "male_amount"},
		{ID: "null_amount_id", Name: "null_amount"},
		{ID: "missing_amount_id", Name: "missing_amount"},
	}
	for index := range 50 {
		name := fmt.Sprintf("unused_source_%d", index)
		columnID := name + "_id"
		output.Fields = append(output.Fields, recipe.Field{Name: name, ColumnID: columnID, Expr: recipe.Expression{Select: "root.active"}})
		output.Construction.SourceColumns = append(output.Construction.SourceColumns, recipe.StageColumn{ID: columnID, Name: name})
	}
	return output
}

func pivotSourceLivenessRecord(id string, group bool, authPath string, payload map[string]any) map[string]any {
	resource := map[string]any{"id": id, "resourceType": "Patient", "active": group}
	for _, field := range []string{"multipleBirthInteger", "gender"} {
		if value, exists := payload[field]; exists {
			resource[field] = value
		}
	}
	return map[string]any{
		"_key": id, "id": id, "project": "pivot-source-liveness", "project_id": "pivot-source-liveness",
		"dataset_generation": "pivot-source-generation", "resourceType": "Patient", "auth_resource_path": authPath,
		"payload": resource,
	}
}

func runPivotSourceLivenessBindQuery(t *testing.T, endpoint, query string, bindVars map[string]any) []map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"query": query, "bindVars": bindVars,
		"options": map[string]any{"maxRuntime": 8}, "memoryLimit": 268435456,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 12 * time.Second}
	response, err := client.Post(strings.TrimRight(endpoint, "/")+"/_api/cursor", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Error   bool             `json:"error"`
		Message string           `json:"errorMessage"`
		HasMore bool             `json:"hasMore"`
		Rows    []map[string]any `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode Arango response (HTTP %d): %v", response.StatusCode, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Error || result.HasMore {
		t.Fatalf("bind-only Pivot query failed or truncated (HTTP %d): %+v", response.StatusCode, result)
	}
	return result.Rows
}

func livenessTestPlan(t *testing.T) ir.PhysicalPlan {
	t.Helper()
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Fields: []recipeplan.SemanticField{{
		Name: "id", FieldRef: "Patient.id", Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionScalar,
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func selectorExpression(variable, field string) ir.PhysicalExpression {
	return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: variable, Path: []string{"payload", field}}}
}

func insertExpressionLet(plan *ir.PhysicalPlan, name string, expr ir.PhysicalExpression) {
	index := len(plan.Operations) - 1
	op := ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Variable: name, Expression: expr}}
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{})
	copy(plan.Operations[index+1:], plan.Operations[index:])
	plan.Operations[index] = op
}

func operationNames(operations []ir.PhysicalOperation) map[string]bool {
	got := map[string]bool{}
	for _, op := range operations {
		if op.ExpressionLet != nil {
			got[op.ExpressionLet.Variable] = true
		}
	}
	return got
}

func TestPrunePreviewPartitionPlanDropsUnusedPureLetChain(t *testing.T) {
	plan := livenessTestPlan(t)
	root := plan.Operations[0].RootScan.Variable
	for i := 0; i < 1000; i++ {
		insertExpressionLet(&plan, fmt.Sprintf("unused_%04d", i), selectorExpression(root, "id"))
	}
	before := len(plan.Operations)
	pruned, ok := prunePreviewPartitionPlan(plan)
	if !ok {
		t.Fatal("valid source row plan was not analyzable")
	}
	if len(pruned.Operations) != before-1000 {
		t.Fatalf("operation count=%d, want %d", len(pruned.Operations), before-1000)
	}
	if len(plan.Operations) != before {
		t.Fatal("input plan was mutated")
	}
	if err := pruned.Validate(); err != nil {
		t.Fatalf("pruned plan invalid: %v", err)
	}
}

func TestPrunePreviewPartitionPlanKeepsTransitiveLiveLets(t *testing.T) {
	plan := livenessTestPlan(t)
	root := plan.Operations[0].RootScan.Variable
	insertExpressionLet(&plan, "first", selectorExpression(root, "id"))
	insertExpressionLet(&plan, "second", selectorExpression("first", "value"))
	terminal := plan.Operations[len(plan.Operations)-1].Return
	terminal.Projections = append(terminal.Projections, ir.PhysicalProjection{Name: "live_value", Value: ir.PhysicalValue{Variable: "second"}})
	pruned, ok := prunePreviewPartitionPlan(plan)
	if !ok {
		t.Fatal("valid source row plan was not analyzable")
	}
	if names := operationNames(pruned.Operations); !names["first"] || !names["second"] {
		t.Fatalf("live producers removed: %v", names)
	}
}

func TestPrunePreviewPartitionPlanKeepsMayFailProducerAndMandatoryFilters(t *testing.T) {
	plan := livenessTestPlan(t)
	mayFail := ir.PhysicalExpression{Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Call: &ir.PhysicalCall{Name: "assert", Args: []ir.PhysicalExpression{
		{Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: "assert_condition"}},
		{Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: "assert_message"}},
	}}}
	plan.BindVars["assert_condition"] = true
	plan.BindVars["assert_message"] = "bad"
	insertExpressionLet(&plan, "may_fail", mayFail)
	beforeFilters := 0
	for _, op := range plan.Operations {
		if op.Kind == ir.PhysicalFilterOp {
			beforeFilters++
		}
	}
	pruned, ok := prunePreviewPartitionPlan(plan)
	if !ok {
		t.Fatal("valid source row plan was not analyzable")
	}
	if !operationNames(pruned.Operations)["may_fail"] {
		t.Fatal("unused may-fail producer was removed")
	}
	afterFilters := 0
	for _, op := range pruned.Operations {
		if op.Kind == ir.PhysicalFilterOp {
			afterFilters++
		}
	}
	if afterFilters != beforeFilters {
		t.Fatalf("filters changed: %d -> %d", beforeFilters, afterFilters)
	}
}

func TestPrunePreviewPartitionPlanKeepsComplexProducerButPrunesSafeSiblings(t *testing.T) {
	plan := livenessTestPlan(t)
	root := plan.Operations[0].RootScan.Variable
	complex := ir.PhysicalExpression{
		Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Call: &ir.PhysicalCall{Name: "concat", Args: []ir.PhysicalExpression{
			selectorExpression(root, "id"),
			{Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: "suffix"}},
		}},
	}
	plan.BindVars["suffix"] = "-x"
	insertExpressionLet(&plan, "complex", complex)
	insertExpressionLet(&plan, "simple_unused", selectorExpression(root, "id"))
	pruned, ok := prunePreviewPartitionPlan(plan)
	if !ok {
		t.Fatal("valid plan with a supported complex expression was not analyzable")
	}
	names := operationNames(pruned.Operations)
	if !names["complex"] {
		t.Fatal("complex producer was removed")
	}
	if names["simple_unused"] {
		t.Fatal("independent safe producer was retained")
	}
}

func TestPrunePreviewPartitionPlanDeclinesUnsupportedShape(t *testing.T) {
	plan := livenessTestPlan(t)
	plan.StageSequence = &ir.PhysicalStageSequence{}
	if _, ok := prunePreviewPartitionPlan(plan); ok {
		t.Fatal("stage sequence unexpectedly accepted")
	}
}

func TestPreviewLivenessPreservesCaptureBoundaryAndMetadata(t *testing.T) {
	expression := ir.PhysicalExpression{Kind: ir.PhysicalCallExpression, Call: &ir.PhysicalCall{Args: []ir.PhysicalExpression{
		{Kind: ir.PhysicalSubplanExpression, Subplan: &ir.PhysicalSubplan{
			Captures: []string{"outer"},
			Return:   ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Value: &ir.PhysicalValue{Variable: "local"}},
		}},
		{Kind: ir.PhysicalExtractExpression, Extract: &ir.PhysicalExtract{
			Source:    ir.PhysicalValue{Variable: "source"},
			Fallbacks: []ir.PhysicalSelectorFallback{{Source: ir.PhysicalValue{Variable: "fallback"}}},
			Prepared:  &ir.PhysicalPreparedReference{SetVariable: "prepared"},
		}},
		{Kind: ir.PhysicalObjectLookupExpression, ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: "object"}},
	}}}
	uses := map[string]bool{}
	if !collectPreviewExpressionUses(expression, uses) {
		t.Fatal("supported nested expression was not analyzable")
	}
	want := map[string]bool{"outer": true, "source": true, "fallback": true, "prepared": true, "object": true}
	if !reflect.DeepEqual(uses, want) {
		t.Fatalf("outer dependencies=%v, want %v", uses, want)
	}
}

func TestPreviewLivenessDeclinesUnknownNestedExpression(t *testing.T) {
	expression := ir.PhysicalExpression{Kind: ir.PhysicalCallExpression, Call: &ir.PhysicalCall{Args: []ir.PhysicalExpression{{Kind: "FUTURE_EXPRESSION"}}}}
	if collectPreviewExpressionUses(expression, map[string]bool{}) {
		t.Fatal("unknown nested expression was treated as safe for pruning")
	}
}
