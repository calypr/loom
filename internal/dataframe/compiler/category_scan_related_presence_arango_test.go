package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestRelatedConstructionPivotTracksMissingNullAndStringAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Specimen"}, {Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_related_presence_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	foreignProject := project + "_foreign"
	generation := "generation-related-presence"
	const visible, denied = "/visible", "/denied"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Specimen", "Patient", "Observation", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project IN @projects REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"projects": []string{project, foreignProject}}); err != nil {
				t.Errorf("remove related presence fixture from %s: %v", collection, err)
			}
		}
	})

	insertResource := func(resourceProject, resourceGeneration, resourceType, id, authPath string, payload map[string]any) string {
		t.Helper()
		key := strings.ReplaceAll(resourceProject, "-", "") + "_" + id
		if payload == nil {
			payload = map[string]any{}
		}
		payload["id"], payload["resourceType"] = id, resourceType
		encoded, err := json.Marshal(map[string]any{
			"_key": key, "id": id, "project": resourceProject, "project_id": resourceProject,
			"dataset_generation": resourceGeneration, "resourceType": resourceType,
			"auth_resource_path": authPath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, resourceType, []json.RawMessage{encoded}, false, "document"); err != nil {
			t.Fatalf("insert related presence %s %s: %v", resourceType, id, err)
		}
		return key
	}
	insertEdge := func(edgeProject, edgeGeneration, id, fromType, fromKey, toType, toKey, authPath string) {
		t.Helper()
		key := strings.ReplaceAll(edgeProject, "-", "") + "_edge_" + id
		encoded, err := json.Marshal(map[string]any{
			"_key": key, "_from": fromType + "/" + fromKey, "_to": toType + "/" + toKey,
			"project": edgeProject, "project_id": edgeProject, "dataset_generation": edgeGeneration,
			"label": "subject_Patient", "from_type": fromType, "to_type": toType,
			"auth_resource_path": authPath,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, "fhir_edge", []json.RawMessage{encoded}, false, "document"); err != nil {
			t.Fatalf("insert related presence edge %s: %v", id, err)
		}
	}

	rootKeys := map[string]string{}
	addRootAndPatient := func(rootID, patientID string) (string, string) {
		t.Helper()
		rootKey := insertResource(project, generation, "Specimen", rootID, visible, map[string]any{"status": "available"})
		patientKey := insertResource(project, generation, "Patient", patientID, visible, map[string]any{})
		insertEdge(project, generation, rootID+"-patient", "Specimen", rootKey, "Patient", patientKey, visible)
		rootKeys[rootID] = rootKey
		return rootKey, patientKey
	}
	_, stringPatient := addRootAndPatient("root-string", "patient-string")
	_, nullPatient := addRootAndPatient("root-null", "patient-null")
	_, absentCodePatient := addRootAndPatient("root-absent-code", "patient-absent-code")
	_, absentParentPatient := addRootAndPatient("root-absent-parent", "patient-absent-parent")
	addRootAndPatient("root-no-observation", "patient-no-observation")

	addObservation := func(id, patientKey string, resourceProject, resourceGeneration, resourcePath, edgeProject, edgeGeneration, edgePath string, quantity map[string]any) {
		t.Helper()
		observationKey := insertResource(resourceProject, resourceGeneration, "Observation", id, resourcePath, map[string]any{"valueQuantity": quantity})
		insertEdge(edgeProject, edgeGeneration, id+"-patient", "Observation", observationKey, "Patient", patientKey, edgePath)
	}
	addObservation("observation-string", stringPatient, project, generation, visible, project, generation, visible,
		map[string]any{"code": "d", "value": 10})
	addObservation("observation-null", nullPatient, project, generation, visible, project, generation, visible,
		map[string]any{"code": nil, "value": 20})
	addObservation("observation-absent-code", absentCodePatient, project, generation, visible, project, generation, visible,
		map[string]any{"value": 30})
	// The entire nested valueQuantity parent is absent for this terminal.
	observationKey := insertResource(project, generation, "Observation", "observation-absent-parent", visible, map[string]any{"status": "final"})
	insertEdge(project, generation, "observation-absent-parent-patient", "Observation", observationKey, "Patient", absentParentPatient, visible)

	// These linked rows are plausible route candidates but fail one of the
	// independent terminal or edge scope predicates.
	addObservation("observation-foreign-project", stringPatient, foreignProject, generation, visible, project, generation, visible,
		map[string]any{"code": "forged-project", "value": 901})
	addObservation("observation-stale-generation", stringPatient, project, generation+"-old", visible, project, generation, visible,
		map[string]any{"code": "forged-generation", "value": 902})
	addObservation("observation-denied-terminal", stringPatient, project, generation, denied, project, generation, visible,
		map[string]any{"code": "forged-terminal-auth", "value": 903})
	addObservation("observation-denied-edge", stringPatient, project, generation, visible, project, generation, denied,
		map[string]any{"code": "forged-edge-auth", "value": 904})

	output := relatedPresencePivotOutput()
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{visible},
	}
	compiled, _, categoryScan := compileRelatedCategoryRuntimeScansWithBindings(t, output, bindings, MaxCategoryScanValues)
	if !categoryScan.Proof.PresenceTracked || categoryScan.OverflowWitness != nil {
		t.Fatalf("related presence scan proof = %#v, want a complete tracked query without a conclusive-MISSING overflow witness", categoryScan.Proof)
	}

	// This raw traversal is deliberately independent of the compiler plans. It
	// models the two PRESERVE_PARENT boundaries and the exact terminal field.
	oracle := CompiledQuery{
		Query: `FOR root IN Specimen
  FILTER root.project == @project AND root.dataset_generation == @generation
    AND root.auth_resource_path IN @auth_paths AND root._key IN @root_keys
  LET patients = (
    FOR first_edge IN fhir_edge
      FILTER first_edge._from == root._id AND first_edge.label == "subject_Patient"
        AND first_edge.project == @project AND first_edge.dataset_generation == @generation
        AND first_edge.auth_resource_path IN @auth_paths
      LET patient = DOCUMENT(first_edge._to)
      FILTER patient != null AND patient.project == @project AND patient.dataset_generation == @generation
        AND patient.resourceType == "Patient" AND patient.auth_resource_path IN @auth_paths
      RETURN patient
  )
  FOR patient_record IN APPEND(patients, LENGTH(patients) == 0 ? [null] : [])
    LET observations = (
      FOR second_edge IN fhir_edge
        FILTER patient_record != null AND second_edge._to == patient_record._id
          AND second_edge.label == "subject_Patient"
          AND second_edge.project == @project AND second_edge.dataset_generation == @generation
          AND second_edge.auth_resource_path IN @auth_paths
        LET observation = DOCUMENT(second_edge._from)
        FILTER observation != null AND observation.project == @project AND observation.dataset_generation == @generation
          AND observation.resourceType == "Observation" AND observation.auth_resource_path IN @auth_paths
        RETURN observation
    )
    FOR terminal IN APPEND(observations, LENGTH(observations) == 0 ? [null] : [])
      LET quantity = terminal == null ? null : terminal.payload.valueQuantity
      LET present = terminal == null ? true : (IS_OBJECT(quantity) AND HAS(quantity, "code"))
      LET category = present AND terminal != null ? quantity.code : null
      LET amount = terminal == null ? null : quantity.value
      RETURN { specimen_id: root.id, present, category, amount }
`,
		BindVars: map[string]any{
			"project": project, "generation": generation, "auth_paths": []string{visible},
			"root_keys": []string{rootKeys["root-string"], rootKeys["root-null"], rootKeys["root-absent-code"], rootKeys["root-absent-parent"], rootKeys["root-no-observation"]},
		},
	}
	rawRows := executeReshapeOracleQuery(t, ctx, client, oracle)
	if len(rawRows) != 5 {
		t.Fatalf("independent scoped related rows = %#v, want five visible roots including the preserved no-target row", rawRows)
	}
	wantCategoryKeys := map[string]bool{}
	wantPivot := map[string]map[string]any{}
	wantRawByRoot := map[string]struct {
		present  bool
		category any
		amount   any
	}{
		"root-string":         {present: true, category: "d", amount: float64(10)},
		"root-null":           {present: true, category: nil, amount: float64(20)},
		"root-absent-code":    {present: false, category: nil, amount: float64(30)},
		"root-absent-parent":  {present: false, category: nil, amount: nil},
		"root-no-observation": {present: true, category: nil, amount: nil},
	}
	for _, row := range rawRows {
		rootID, ok := row["specimen_id"].(string)
		if !ok || rootID == "" {
			t.Fatalf("independent raw row has no root identity: %#v", row)
		}
		present, ok := row["present"].(bool)
		if !ok {
			t.Fatalf("independent raw presence = %#v", row["present"])
		}
		category := row["category"]
		wantRaw, knownRoot := wantRawByRoot[rootID]
		if !knownRoot || present != wantRaw.present || !reflect.DeepEqual(category, wantRaw.category) || !reflect.DeepEqual(row["amount"], wantRaw.amount) {
			t.Fatalf("independent raw state for %q = (present=%t, category=%#v, amount=%#v), want (%t, %#v, %#v)", rootID, present, category, row["amount"], wantRaw.present, wantRaw.category, wantRaw.amount)
		}
		wantCategoryKeys[relatedCategoryValueKey(present, category)] = true
		values := map[string]any{"category_d": nil, "category_null": nil, "category_missing": nil}
		switch {
		case !present:
			values["category_missing"] = row["amount"]
		case category == nil:
			values["category_null"] = row["amount"]
		case category == "d":
			values["category_d"] = row["amount"]
		default:
			t.Fatalf("independent oracle discovered unexpected category %#v for %q", category, rootID)
		}
		if _, duplicate := wantPivot[rootID]; duplicate {
			t.Fatalf("independent fixture produced more than one source row for root %q: %#v", rootID, rawRows)
		}
		wantPivot[rootID] = values
	}
	if !reflect.DeepEqual(wantCategoryKeys, map[string]bool{
		relatedCategoryValueKey(true, "d"):  true,
		relatedCategoryValueKey(true, nil):  true,
		relatedCategoryValueKey(false, nil): true,
	}) {
		t.Fatalf("independent exact category states = %#v, want STRING d, explicit/preserved NULL, and MISSING", wantCategoryKeys)
	}
	if noTarget := wantPivot["root-no-observation"]; noTarget == nil || noTarget["category_null"] != nil || noTarget["category_missing"] != nil {
		t.Fatalf("preserved no-target row should remain in its group with NULL Pivot cells: %#v", noTarget)
	}

	discoveredRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: categoryScan.Query, BindVars: categoryScan.BindVars})
	gotCategoryKeys := make(map[string]bool, len(discoveredRows))
	for _, row := range discoveredRows {
		present, ok := row[categoryScan.PresentColumn].(bool)
		if !ok {
			t.Fatalf("compiled related category presence = %#v, want bool", row[categoryScan.PresentColumn])
		}
		value, exists := row[categoryScan.ValueColumn]
		if !exists {
			t.Fatalf("compiled related category row omitted value: %#v", row)
		}
		gotCategoryKeys[relatedCategoryValueKey(present, value)] = true
	}
	if !reflect.DeepEqual(gotCategoryKeys, wantCategoryKeys) {
		t.Fatalf("compiled category scan states = %#v, independent scoped states = %#v", gotCategoryKeys, wantCategoryKeys)
	}
	if len(discoveredRows) != 3 {
		t.Fatalf("compiled category scan returned %d rows, want distinct MISSING, NULL, and STRING d: %#v", len(discoveredRows), discoveredRows)
	}

	pivotQuery, err := CompileRecipeOutputWithPolicy(compiled, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile final typed-MISSING related Pivot: %v", err)
	}
	pivotRows := executeReshapeOracleQuery(t, ctx, client, pivotQuery)
	gotPivot := make(map[string]map[string]any, len(pivotRows))
	for _, row := range pivotRows {
		rootID, ok := row["specimen_id"].(string)
		if !ok {
			t.Fatalf("final Pivot row has invalid specimen_id: %#v", row)
		}
		gotPivot[rootID] = map[string]any{
			"category_d": row["category_d"], "category_null": row["category_null"], "category_missing": row["category_missing"],
		}
	}
	if len(gotPivot) != len(wantPivot) || !reflect.DeepEqual(gotPivot, wantPivot) {
		t.Fatalf("final typed-MISSING Pivot rows = %#v, independent raw expected rows = %#v", gotPivot, wantPivot)
	}
	if got := gotPivot["root-absent-code"]["category_missing"]; !constructionNumericEqual(got, 30) {
		t.Fatalf("typed MISSING category did not retain the exact absent-code value: %#v", got)
	}
	if got := gotPivot["root-string"]["category_d"]; !constructionNumericEqual(got, 10) {
		t.Fatalf("typed STRING category did not retain value 10: %#v", got)
	}
	for _, row := range discoveredRows {
		if value, ok := row[categoryScan.ValueColumn].(string); ok && strings.HasPrefix(value, "forged-") {
			t.Errorf("compiled category scan leaked forged contributor %q", value)
		}
	}
}
func TestRelatedConstructionPivotPresencePlanCompiles(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "related-presence-compile-project", SelectionProject: "related-presence-compile-project",
		DatasetGeneration: "related-presence-compile-generation",
		AuthScopeMode:     authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/visible"},
	}
	compiled, _, scanned := compileRelatedCategoryRuntimeScansWithBindings(t, relatedPresencePivotOutput(), bindings, MaxCategoryScanValues)
	if !scanned.Proof.PresenceTracked || scanned.OverflowWitness != nil {
		t.Fatalf("compiled related presence proof = %#v, want tracked category scan without conclusive-MISSING witness", scanned.Proof)
	}
	query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile final typed-MISSING Pivot: %v", err)
	}
	var presenceColumn string
	for _, stage := range compiled.Plan.StageSequence.Stages {
		if stage.GroupedPivot != nil && stage.GroupedPivot.CategoryPresenceFromInput {
			presenceColumn = stage.GroupedPivot.CategoryPresenceColumn
		}
	}
	if presenceColumn == "" || !strings.Contains(query.Query, "NOT (") || !containsBindValue(query.BindVars, presenceColumn) {
		t.Fatalf("compiled MISSING Pivot did not consume its typed presence companion %q:\n%s\n%#v", presenceColumn, query.Query, query.BindVars)
	}
}

func relatedPresencePivotOutput() recipe.Output {
	output := relatedCategoryScanOutput(relatedCategoryScanOptions{})
	construction := *output.Construction
	construction.Steps = append(construction.Steps, recipe.ConstructionStep{
		ID: "pivot_related_category", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "add_amount"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
			ConstructionID: "related_presence_pivot", GroupKeyIDs: []string{"specimen_id"},
			CategoryColumnID: "category_id", ValueColumnID: "amount_id",
			Categories: []recipe.ConstructionPivotCategory{
				{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: stringPointer("d")}, OutputColumnID: "category_d"},
				{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "category_null"},
				{Key: recipe.TableScalar{Kind: recipe.TableScalarMissing}, OutputColumnID: "category_missing"},
			},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		}},
		Outputs: []recipe.StageColumn{
			{ID: "specimen_id", Name: "specimen_id"},
			{ID: "category_d", Name: "category_d", Type: "decimal", Nullable: true},
			{ID: "category_null", Name: "category_null", Type: "decimal", Nullable: true},
			{ID: "category_missing", Name: "category_missing", Type: "decimal", Nullable: true},
		},
	})
	output.Construction = &construction
	return output
}
