package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/fhir/schema"
	store "github.com/calypr/loom/internal/store/arango"
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

	binding := &schema.CorrelatedBinding{
		KeyPath: "type.coding[]", ValueScope: schema.CorrelatedValueScopeCoding,
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

	// Render the same finalized typed GROUP plan without its preview window.
	// This is the generic full-source reference for the optimized root-key
	// frontier query above, independent of the fixture's hand-written oracle.
	genericPlan := clonePhysicalPlan(preview.PhysicalPlan)
	genericPlan.StageSequence.PreviewLimitBindKey = ""
	genericRendered, err := aql.RenderPhysicalPlan(genericPlan)
	if err != nil {
		t.Fatalf("render generic full-source typed GROUP plan: %v", err)
	}
	allRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{
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

func tumorGroupLivenessOutput(binding *schema.CorrelatedBinding) recipe.Output {
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
