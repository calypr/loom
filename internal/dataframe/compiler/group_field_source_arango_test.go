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

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer/capability"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestConstructionGroupExpandedCodingFieldSourceAgainstArango(t *testing.T) {
	project, generation := "loom_group_field_source_"+uuid.NewString(), "generation-group-field-source"
	selectionID := project + "_selection"
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		SelectionMembersCollection: "loom_explorer_selection_members",
	}

	base := expandedCodingGroupSourceOutput("subject_Patient", selectionID)
	sourceContext := expandedCodingGroupSourceContext(t, base, bindings)
	if sourceContext.SourceStageID != recipe.ConstructionSourceProjectionID || sourceContext.SourceExpansion == nil ||
		sourceContext.SourceExpansion.OwnerResourceType != "Observation" ||
		sourceContext.SourceExpansion.Selector != "valueCodeableConcept.coding[]" ||
		sourceContext.SourceExpansion.EmptyPolicy != string(recipe.ExpansionPreserveParent) {
		t.Fatalf("canonical expanded Group source context = %#v, want SOURCE_PROJECTION Observation/valueCodeableConcept.coding[]", sourceContext)
	}

	wrongRouteBase := expandedCodingGroupSourceOutput("focus_Patient", project+"_selection")
	wrongRouteContext := expandedCodingGroupSourceContext(t, wrongRouteBase, bindings)
	if wrongRouteContext.SourceShapeID == sourceContext.SourceShapeID {
		t.Fatalf("same-type wrong route reused the source shape ID %q", sourceContext.SourceShapeID)
	}
	wrongRouteOutput := expandedCodingGroupOutput(wrongRouteBase, sourceContext, recipe.ConstructionGroupMissingKeyGroup)
	if _, err := compileExpandedCodingGroupOutput(t, wrongRouteOutput, bindings); err == nil ||
		!strings.Contains(err.Error(), "CODING_IDENTITY fieldSource") {
		t.Fatalf("same-type wrong-route Coding field source error = %v, want exact source-shape rejection", err)
	}

	compiledByPolicy := make(map[recipe.ConstructionGroupMissingKeyPolicy]lower.CompiledRecipeOutput)
	queryByPolicy := make(map[recipe.ConstructionGroupMissingKeyPolicy]CompiledQuery)
	for _, policy := range []recipe.ConstructionGroupMissingKeyPolicy{
		recipe.ConstructionGroupMissingKeyGroup,
		recipe.ConstructionGroupMissingKeyExclude,
		recipe.ConstructionGroupMissingKeyError,
	} {
		output := expandedCodingGroupOutput(base, sourceContext, policy)
		compiled, err := compileExpandedCodingGroupOutput(t, output, bindings)
		if err != nil {
			t.Fatalf("lower expanded Coding Group with %s policy: %v", policy, err)
		}
		if len(compiled.Stages) == 0 || compiled.Stages[0].GroupFieldSourceContext == nil ||
			compiled.Stages[0].GroupFieldSourceContext.SourceShapeID != sourceContext.SourceShapeID {
			t.Fatalf("lowered source descriptor lost its exact pre-Group shape: %#v", compiled.Stages)
		}
		query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("compile expanded Coding Group with %s policy: %v", policy, err)
		}
		assertExpandedCodingGroupUsesProjectedMembership(t, compiled, query)
		compiledByPolicy[policy], queryByPolicy[policy] = compiled, query
	}

	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "fhir_edge", Edge: true}, {Name: "loom_explorer_selection_members"},
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Patient", "Observation", "fhir_edge", "loom_explorer_selection_members"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove expanded Coding Group fixture from %s: %v", collection, err)
			}
		}
	})

	patientKey := project + "_patient"
	insertExpandedCodingGroupResource(t, ctx, client, "Patient", patientKey, project, generation, map[string]any{
		"id": "patient", "resourceType": "Patient",
	})
	insertExpandedCodingGroupSelectionMember(t, ctx, client, project, generation, selectionID)
	observationAKey, observationBKey := project+"_observation_a", project+"_observation_b"
	insertExpandedCodingGroupResource(t, ctx, client, "Observation", observationAKey, project, generation, expandedCodingGroupObservation("observation-a", true))
	insertExpandedCodingGroupResource(t, ctx, client, "Observation", observationBKey, project, generation, expandedCodingGroupObservation("observation-b", false))
	edgeAKey, edgeBKey := project+"_edge_a", project+"_edge_b"
	insertExpandedCodingGroupEdge(t, ctx, client, edgeAKey, observationAKey, patientKey, project, generation)
	insertExpandedCodingGroupEdge(t, ctx, client, edgeBKey, observationBKey, patientKey, project, generation)

	groupRows := executeReshapeOracleQuery(t, ctx, client, queryByPolicy[recipe.ConstructionGroupMissingKeyGroup])
	if len(groupRows) != 12 {
		t.Fatalf("GROUP expanded Coding rows = %#v, want twelve exact Coding identity groups including the preserved empty owner", groupRows)
	}
	for _, row := range groupRows {
		if row["status"] != "final" {
			t.Errorf("expanded Coding Group scalar key = %#v, want the shared Observation status final", row)
		}
	}
	var sharedRow, fallbackRow, presentMissingCodeRow, emptyCodingRow map[string]any
	var sameDisplayRows, versionlessRows, noSystemRows, missingCodeRows []map[string]any
	for _, row := range groupRows {
		label, _ := row["coding"].(string)
		switch {
		case label == "Alpha":
			sharedRow = row
		case label == "Twin":
			sameDisplayRows = append(sameDisplayRows, row)
		case label == "Versionless A":
			versionlessRows = append(versionlessRows, row)
		case label == "No system":
			noSystemRows = append(noSystemRows, row)
		case label == "fallback-code":
			fallbackRow = row
		case label == "Empty code" || row["coding"] == nil:
			missingCodeRows = append(missingCodeRows, row)
		}
	}
	if sharedRow == nil || !constructionNumericEqual(sharedRow["occurrences"], 4) {
		t.Fatalf("shared tuple Group = %#v, want four physical Coding occurrences including identical items and MIN nonblank display Alpha", sharedRow)
	}
	assertExpandedCodingIdentityTuple(t, sharedRow, "urn:loom:group", "v1", "000-shared")
	assertExpandedCodingGroupOwnerValues(t, sharedRow["owners"], []string{"observation-a"})
	if len(sameDisplayRows) != 4 {
		t.Fatalf("same-display groups = %#v, want distinct code/system/version tuples", sameDisplayRows)
	}
	sameDisplayIdentities := make(map[string]bool, len(sameDisplayRows))
	for _, row := range sameDisplayRows {
		if !constructionNumericEqual(row["occurrences"], 1) {
			t.Errorf("same-display tuple count = %#v, want one occurrence: %#v", row["occurrences"], row)
		}
		rowID, _ := row["__loom_row_id"].(string)
		if rowID == "" || sameDisplayIdentities[rowID] {
			t.Errorf("same-display tuples reused or omitted grouped row identity %q", rowID)
		}
		sameDisplayIdentities[rowID] = true
	}
	wantSameDisplayTuples := map[string]bool{
		`["urn:loom:group","v1","same-display"]`:   true,
		`["urn:loom:other","v1","same-display"]`:   true,
		`["urn:loom:group","v2","same-display"]`:   true,
		`["urn:loom:group","v1","different-code"]`: true,
	}
	for _, row := range sameDisplayRows {
		tuple := expandedCodingIdentityTuple(t, row)
		encoded, _ := json.Marshal(tuple)
		key := string(encoded)
		if !wantSameDisplayTuples[key] {
			t.Errorf("same-display Coding identity tuple = %s, want one of the four exact code/system/version tuples", key)
		}
		delete(wantSameDisplayTuples, key)
	}
	if len(wantSameDisplayTuples) != 0 {
		t.Errorf("same-display Coding groups omitted exact tuples: %#v", wantSameDisplayTuples)
	}
	if len(missingCodeRows) != 2 {
		t.Fatalf("missing-code and empty-Coding GROUP rows = %#v, want the present null-code tuple and preserved no-item tuple", missingCodeRows)
	}
	for _, row := range missingCodeRows {
		switch {
		case constructionNumericEqual(row["occurrences"], 3):
			presentMissingCodeRow = row
			if row["coding"] != "Empty code" {
				t.Errorf("present missing-code display label = %#v, want MIN nonblank display Empty code", row["coding"])
			}
			assertExpandedCodingIdentityTuple(t, row, "urn:loom:group", nil, nil)
			assertExpandedCodingGroupOwnerValues(t, row["owners"], []string{"observation-a"})
		case constructionNumericEqual(row["occurrences"], 1):
			emptyCodingRow = row
			if row["coding"] != nil {
				t.Errorf("preserved empty Coding label = %#v, want null fallback", row["coding"])
			}
			assertExpandedCodingIdentityTuple(t, row, nil, nil, nil)
			assertExpandedCodingGroupOwnerValues(t, row["owners"], []string{"observation-b"})
		default:
			t.Errorf("missing Coding row = %#v, want three present null-code entries or one preserved empty owner", row)
		}
	}
	if presentMissingCodeRow == nil || emptyCodingRow == nil || presentMissingCodeRow["__loom_row_id"] == emptyCodingRow["__loom_row_id"] {
		t.Fatalf("present missing-code and preserved empty-Coding rows did not retain separate identities: %#v", missingCodeRows)
	}
	if len(versionlessRows) != 2 {
		t.Fatalf("versioned and versionless same-code rows = %#v, want exact versionless identity plus separate v1 identity", versionlessRows)
	}
	versionCounts := map[float64]int{}
	versionlessIDs := make(map[string]bool, len(versionlessRows))
	for _, row := range versionlessRows {
		count := numericValue(row["occurrences"])
		versionCounts[count]++
		if constructionNumericEqual(count, 3) {
			assertExpandedCodingIdentityTuple(t, row, "urn:loom:group", nil, "versionless")
		} else {
			assertExpandedCodingIdentityTuple(t, row, "urn:loom:group", "v1", "versionless")
		}
		rowID, _ := row["__loom_row_id"].(string)
		if rowID == "" || versionlessIDs[rowID] {
			t.Errorf("versioned/versionless identity rows reused or omitted row ID %q", rowID)
		}
		versionlessIDs[rowID] = true
	}
	if versionCounts[1] != 1 || versionCounts[3] != 1 {
		t.Errorf("same-code version counts = %#v, want one v1 entry and three absent/blank version entries", versionCounts)
	}
	if len(noSystemRows) != 2 {
		t.Fatalf("missing/present system same-code rows = %#v, want separate legal tuple identities", noSystemRows)
	}
	noSystemCounts := map[float64]int{}
	var absentSystemRow, presentSystemRow map[string]any
	for _, row := range noSystemRows {
		count := numericValue(row["occurrences"])
		noSystemCounts[count]++
		switch count {
		case 2:
			absentSystemRow = row
			assertExpandedCodingIdentityTuple(t, row, nil, nil, "no-system")
		case 1:
			presentSystemRow = row
			assertExpandedCodingIdentityTuple(t, row, "urn:loom:group", nil, "no-system")
		}
	}
	if noSystemCounts[1] != 1 || noSystemCounts[2] != 1 {
		t.Errorf("absent/null/present system tuple counts = %#v, want absent and null together at 2, present string separate at 1", noSystemCounts)
	}
	if absentSystemRow == nil || presentSystemRow == nil {
		t.Fatalf("absent/null and present-string system tuples were not both returned: %#v", noSystemRows)
	}
	if absentSystemRow["__loom_row_id"] == presentSystemRow["__loom_row_id"] {
		t.Errorf("missing and present system tuples shared a grouped row identity: %#v", noSystemRows)
	}
	if fallbackRow == nil || !constructionNumericEqual(fallbackRow["occurrences"], 1) {
		t.Fatalf("all-blank-display tuple = %#v, want code fallback after aggregation", fallbackRow)
	}
	assertExpandedCodingIdentityTuple(t, fallbackRow, "urn:loom:group", nil, "fallback-code")

	t.Run("missing-key-policies", func(t *testing.T) {
		excludedRows := executeReshapeOracleQuery(t, ctx, client, queryByPolicy[recipe.ConstructionGroupMissingKeyExclude])
		if len(excludedRows) != 10 {
			t.Fatalf("EXCLUDE expanded Coding Group rows = %#v, want ten keyed Coding identity groups", excludedRows)
		}
		for _, row := range excludedRows {
			if tuple := expandedCodingIdentityTuple(t, row); tuple[2] == nil {
				t.Fatalf("EXCLUDE retained a missing-code Coding tuple: %#v", row)
			}
		}
		errorQuery := queryByPolicy[recipe.ConstructionGroupMissingKeyError]
		if err := client.QueryRows(ctx, errorQuery.Query, 100, errorQuery.BindVars, func(map[string]any) error { return nil }); err == nil {
			t.Fatal("ERROR expanded Coding Group accepted missing and empty code occurrences")
		}
	})

	t.Run("coding-field-types", func(t *testing.T) {
		t.Cleanup(func() {
			replaceExpandedCodingGroupObservation(t, ctx, client, observationBKey, expandedCodingGroupObservation("observation-b", false))
		})
		for _, test := range []struct {
			name   string
			coding map[string]any
		}{
			{name: "numeric system", coding: map[string]any{"system": 17, "version": "v1", "code": "typed", "display": "Typed"}},
			{name: "numeric version", coding: map[string]any{"system": "urn:loom:typed", "version": 17, "code": "typed", "display": "Typed"}},
			{name: "numeric display", coding: map[string]any{"system": "urn:loom:typed", "version": "v1", "code": "typed", "display": 17}},
		} {
			t.Run(test.name, func(t *testing.T) {
				replaceExpandedCodingGroupObservation(t, ctx, client, observationBKey, map[string]any{
					"id": "observation-b", "resourceType": "Observation", "status": "final",
					"valueCodeableConcept": map[string]any{"coding": []any{test.coding}},
				})
				err := client.QueryRows(ctx, queryByPolicy[recipe.ConstructionGroupMissingKeyGroup].Query, 100,
					queryByPolicy[recipe.ConstructionGroupMissingKeyGroup].BindVars, func(map[string]any) error { return nil })
				if err == nil || !strings.Contains(err.Error(), "GROUP_KEY_TYPE_MISMATCH") {
					t.Fatalf("GROUP query with %s = %v, want GROUP_KEY_TYPE_MISMATCH", test.name, err)
				}
			})
		}
	})

	t.Run("on-demand-lineage", func(t *testing.T) {
		if capability := RowLineageCapabilityForOutput(compiledByPolicy[recipe.ConstructionGroupMissingKeyGroup]); !capability.Available {
			t.Fatalf("expanded Coding Group row lineage capability = %#v, want available for the exact source preimage", capability)
		}
		assertExpandedCodingGroupLineage(t, ctx, client, compiledByPolicy[recipe.ConstructionGroupMissingKeyGroup], sharedRow, project, patientKey,
			map[string]int{"observation-a": 4}, map[string]map[float64]bool{"observation-a": {0: true, 1: true, 2: true, 3: true}}, nil)
		assertExpandedCodingGroupLineage(t, ctx, client, compiledByPolicy[recipe.ConstructionGroupMissingKeyGroup], presentMissingCodeRow, project, patientKey,
			map[string]int{"observation-a": 3}, map[string]map[float64]bool{"observation-a": {15: true, 16: true, 17: true}}, nil)
		assertExpandedCodingGroupLineage(t, ctx, client, compiledByPolicy[recipe.ConstructionGroupMissingKeyGroup], emptyCodingRow, project, patientKey,
			map[string]int{"observation-b": 1}, nil, map[string]bool{"observation-b": true})
		assertExpandedCodingGroupLineage(t, ctx, client, compiledByPolicy[recipe.ConstructionGroupMissingKeyGroup], absentSystemRow, project, patientKey,
			map[string]int{"observation-a": 2}, map[string]map[float64]bool{"observation-a": {8: true, 9: true}}, nil)
		assertExpandedCodingGroupLineage(t, ctx, client, compiledByPolicy[recipe.ConstructionGroupMissingKeyGroup], presentSystemRow, project, patientKey,
			map[string]int{"observation-a": 1}, map[string]map[float64]bool{"observation-a": {10: true}}, nil)
	})
}

func TestConstructionGroupAncestorRowValuesAgainstArango(t *testing.T) {
	project, generation := "loom_group_ancestor_row_value_"+uuid.NewString(), "generation-group-ancestor-row-value"
	selectionID := project + "_selection"
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		SelectionMembersCollection: "loom_explorer_selection_members",
	}
	candidateIDs := expandedAncestorFieldCandidateIDs(t)
	base := expandedAncestorGroupSourceOutput("subject_Patient", selectionID)
	sourceCompiled, err := compileExpandedCodingGroupOutput(t, base, bindings)
	if err != nil {
		t.Fatalf("lower ancestor-expanded Coding source: %v", err)
	}
	sourceDescriptor, err := lower.DescribeConstructionSourceStage(sourceCompiled.OutputSchema, sourceCompiled.RootResourceType, &sourceCompiled.Plan)
	if err != nil {
		t.Fatalf("describe canonical ancestor Coding source: %v", err)
	}
	sourceContext := sourceDescriptor.GroupFieldSourceContext
	if sourceDescriptor.ID != recipe.ConstructionSourceProjectionID || sourceDescriptor.Operation != "SOURCE_PROJECTION" || sourceContext == nil || sourceContext.SourceExpansion == nil ||
		sourceContext.SourceStageID != recipe.ConstructionSourceProjectionID ||
		sourceContext.SourceExpansion.OwnerOccurrenceID != "observation-owner" ||
		sourceContext.SourceExpansion.OwnerResourceType != "Observation" ||
		sourceContext.SourceExpansion.Selector != "component[].code.coding[]" || len(sourceContext.SourceExpansion.Route) != 1 ||
		sourceContext.SourceExpansion.Route[0].EdgeLabel != "subject_Patient" {
		t.Fatalf("canonical ancestor Coding Group source context = %#v, want routed Observation/component[].code.coding[]", sourceContext)
	}
	canonicalSourceContext := *sourceContext
	var componentOwner lower.CompiledGroupFieldOwner
	componentOwners := 0
	for _, owner := range sourceContext.FieldOwners {
		if owner.Role == "ANCESTOR" && owner.BoundarySelector == "component[]" {
			componentOwner = owner
			componentOwners++
		}
	}
	if componentOwners != 1 || componentOwner.OccurrenceID != "observation-owner" ||
		componentOwner.OwnerResourceType != "Observation" || componentOwner.ResourceType != "ObservationComponent" {
		t.Fatalf("canonical component ancestor owner = %#v, want the exact ObservationComponent owner at component[]", componentOwner)
	}

	wrongRouteBase := expandedAncestorGroupSourceOutput("focus_Patient", selectionID)
	wrongRouteSource, err := compileExpandedCodingGroupOutput(t, wrongRouteBase, bindings)
	if err != nil {
		t.Fatalf("lower same-type wrong-route source: %v", err)
	}
	wrongRouteDescriptor, err := lower.DescribeConstructionSourceStage(wrongRouteSource.OutputSchema, wrongRouteSource.RootResourceType, &wrongRouteSource.Plan)
	if err != nil || wrongRouteDescriptor.GroupFieldSourceContext == nil {
		t.Fatalf("describe same-type wrong-route source: context=%#v err=%v", wrongRouteDescriptor.GroupFieldSourceContext, err)
	}
	if wrongRouteDescriptor.GroupFieldSourceContext.SourceShapeID == sourceContext.SourceShapeID {
		t.Fatalf("same-type wrong route reused source shape ID %q", sourceContext.SourceShapeID)
	}
	wrongRouteGroup := expandedAncestorGroupOutput(wrongRouteBase, canonicalSourceContext, componentOwner, candidateIDs, false)
	if _, err := compileExpandedCodingGroupOutput(t, wrongRouteGroup, bindings); err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("same-type wrong-route field sources error = %v, want exact source-shape rejection", err)
	}

	baseGroup := expandedAncestorGroupOutput(base, canonicalSourceContext, componentOwner, candidateIDs, false)
	compiledBaseGroup, err := compileExpandedCodingGroupOutput(t, baseGroup, bindings)
	if err != nil {
		t.Fatalf("lower ancestor Coding Group before adding component values: %v", err)
	}
	assertAncestorQuantityNotPreprojected(t, sourceCompiled, sourceDescriptor, compiledBaseGroup)

	withComponentValues := expandedAncestorGroupOutput(base, canonicalSourceContext, componentOwner, candidateIDs, true)
	compiled, err := compileExpandedCodingGroupOutput(t, withComponentValues, bindings)
	if err != nil {
		t.Fatalf("lower ancestor Coding Group with post-Group component RowValue: %v", err)
	}
	query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile ancestor Coding Group with component RowValue: %v", err)
	}
	assertExpandedCodingGroupUsesProjectedMembership(t, compiled, query)
	sequence := compiled.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 1 || sequence.Stages[0].Group == nil || len(sequence.Stages[0].Group.RowValues) != 2 {
		t.Fatalf("ancestor Coding Group RowValues = %#v, want owner ID plus the added component field source", sequence)
	}
	groupStage := sequence.Stages[0]
	quantityRowValue := groupStage.Group.RowValues[1]
	if quantityRowValue.FieldSource == nil || quantityRowValue.FieldSource.SourceStageID != sourceContext.SourceStageID ||
		quantityRowValue.FieldSource.SourceShapeID != sourceContext.SourceShapeID ||
		quantityRowValue.FieldSource.Role != "ANCESTOR" ||
		quantityRowValue.FieldSource.OccurrenceID != componentOwner.OccurrenceID ||
		quantityRowValue.FieldSource.OwnerResourceType != "Observation" ||
		quantityRowValue.FieldSource.ResourceType != "ObservationComponent" ||
		quantityRowValue.FieldSource.CandidateID != candidateIDs["component[].valueQuantity.value"] ||
		quantityRowValue.FieldSource.CanonicalPath != "valueQuantity.value" ||
		quantityRowValue.FieldSource.BoundarySelector != "component[]" || quantityRowValue.FieldSource.SourceVariable == "" || quantityRowValue.InputColumn == "" {
		t.Fatalf("compiled component RowValue field source = %#v, want exact compiler-owned component ancestor", quantityRowValue.FieldSource)
	}
	rowValueInputFound := false
	for _, column := range groupStage.InputColumns {
		if column.Name == quantityRowValue.InputColumn && column.Internal {
			rowValueInputFound = true
		}
	}
	if !rowValueInputFound {
		t.Fatalf("added component RowValue source is not a hidden Group input projection: %#v", quantityRowValue)
	}

	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "fhir_edge", Edge: true}, {Name: "loom_explorer_selection_members"},
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Patient", "Observation", "fhir_edge", "loom_explorer_selection_members"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove ancestor Coding Group fixture from %s: %v", collection, err)
			}
		}
	})

	patientKey := project + "_patient"
	observationAPayload := expandedAncestorCodingObservation("observation-a", 10, 2, true)
	observationBPayload := expandedAncestorCodingObservation("observation-b", 30, 1, false)
	assertExpandedAncestorCodingShape(t, observationAPayload, []string{"component-one", "component-two"}, []int{2, 1})
	assertExpandedAncestorCodingShape(t, observationBPayload, []string{"component-one"}, []int{1})
	assertExpandedAncestorCodingQuantities(t, observationAPayload, []float64{10, 20})
	assertExpandedAncestorCodingQuantities(t, observationBPayload, []float64{30})
	for _, observation := range []map[string]any{observationAPayload, observationBPayload} {
		encoded, err := json.Marshal(observation)
		if err != nil {
			t.Fatalf("serialize synthetic Observation input: %v", err)
		}
		t.Logf("synthetic Observation input passed to insert helper: %s", encoded)
	}
	insertExpandedCodingGroupResource(t, ctx, client, "Patient", patientKey, project, generation, map[string]any{
		"id": "patient", "resourceType": "Patient",
	})
	insertExpandedCodingGroupSelectionMember(t, ctx, client, project, generation, selectionID)
	observationAKey, observationBKey := project+"_observation_a", project+"_observation_b"
	insertExpandedCodingGroupResource(t, ctx, client, "Observation", observationAKey, project, generation, observationAPayload)
	insertExpandedCodingGroupResource(t, ctx, client, "Observation", observationBKey, project, generation, observationBPayload)
	insertExpandedCodingGroupEdge(t, ctx, client, project+"_edge_a", observationAKey, patientKey, project, generation)
	insertExpandedCodingGroupEdge(t, ctx, client, project+"_edge_b", observationBKey, patientKey, project, generation)

	rows := executeReshapeOracleQuery(t, ctx, client, query)
	if len(rows) != 2 {
		t.Fatalf("ancestor Coding groups = %#v, want one exact shared Coding tuple group per component", rows)
	}
	groupsByComponent := make(map[string]map[string]any, len(rows))
	rowIDs := make(map[string]bool, len(rows))
	for _, row := range rows {
		component, _ := row["component_marker"].(string)
		if component != "component-one" && component != "component-two" {
			t.Fatalf("ancestor Coding component group = %#v, want component-one or component-two", row)
		}
		if groupsByComponent[component] != nil {
			t.Fatalf("ancestor Coding group duplicated component marker %q: %#v", component, rows)
		}
		groupsByComponent[component] = row
		rowID, _ := row["__loom_row_id"].(string)
		if rowID == "" || rowIDs[rowID] {
			t.Fatalf("ancestor Coding group row ID is absent or reused: %q (%#v)", rowID, rows)
		}
		rowIDs[rowID] = true
		assertExpandedAncestorGroupIdentity(t, row, component)
	}
	componentOne, componentTwo := groupsByComponent["component-one"], groupsByComponent["component-two"]
	if componentOne == nil || componentTwo == nil {
		t.Fatalf("ancestor Coding groups by component = %#v, want component-one and component-two", groupsByComponent)
	}
	if !constructionNumericEqual(componentOne["occurrences"], 3) || !constructionNumericEqual(componentTwo["occurrences"], 1) {
		t.Fatalf("component Coding counts = one:%#v two:%#v, want 3 and 1 physical occurrences", componentOne["occurrences"], componentTwo["occurrences"])
	}
	assertExpandedCodingGroupOwnerValues(t, componentOne["owners"], []string{"observation-a", "observation-b"})
	assertExpandedCodingGroupOwnerValues(t, componentTwo["owners"], []string{"observation-a"})
	assertExpandedAncestorGroupNumericValues(t, componentOne["component_values"], []float64{10, 30})
	assertExpandedAncestorGroupNumericValues(t, componentTwo["component_values"], []float64{20})
	if capability := RowLineageCapabilityForOutput(compiled); !capability.Available {
		t.Fatalf("ancestor Coding Group row lineage capability = %#v, want the exact routed Coding preimage", capability)
	}
	assertExpandedCodingGroupLineage(t, ctx, client, compiled, componentOne, project, patientKey,
		map[string]int{"observation-a": 2, "observation-b": 1},
		map[string]map[float64]bool{"observation-a": {0: true, 1: true}, "observation-b": {0: true}}, nil)
	assertExpandedCodingGroupLineage(t, ctx, client, compiled, componentTwo, project, patientKey,
		map[string]int{"observation-a": 1}, map[string]map[float64]bool{"observation-a": {2: true}}, nil)
}

func expandedAncestorGroupSourceOutput(routeName, selectionID string) recipe.Output {
	return recipe.Output{
		Name: "ancestor_coding_group_row_values", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
		TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "observation-owner", From: recipe.Expression{Select: "observation.component[].code.coding[]"},
			As: "coding_item", Ordinality: "coding_ordinal", EmptyPolicy: recipe.ExpansionExclude,
		},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
		Traversals: []recipe.Traversal{{
			Name: routeName, OccurrenceID: "observation-owner", Alias: "observation", ToResourceType: "Observation", MatchMode: recipe.MatchRequired,
			Fields: []recipe.Field{{Name: "owner_id", ColumnID: "owner_id", Expr: recipe.Expression{Select: "observation.id"}}},
		}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: selectionID, MembershipDigest: selectionID + "-membership",
			MemberCount: 1, ResourceType: "Patient",
		},
	}
}

func expandedAncestorGroupOutput(
	base recipe.Output,
	sourceContext lower.CompiledGroupFieldSourceContext,
	componentOwner lower.CompiledGroupFieldOwner,
	candidateIDs map[string]string,
	includeComponentValues bool,
) recipe.Output {
	base.Construction = &recipe.Construction{
		Version:       1,
		SourceColumns: []recipe.StageColumn{{ID: "owner_id", Name: "owner_id", Type: "string"}},
		Steps: []recipe.ConstructionStep{{
			ID: "group_component_coding", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
				ConstructionID: "group_component_coding", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
				Keys: []recipe.ConstructionGroupKey{
					{
						OutputColumnID: "component_marker_id",
						FieldSource: &recipe.ConstructionGroupFieldSource{
							Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: sourceContext.SourceStageID,
							SourceShapeID: sourceContext.SourceShapeID, SourceRole: "ANCESTOR",
							OccurrenceID: componentOwner.OccurrenceID, OwnerResourceType: "Observation",
							ResourceType: "ObservationComponent", CandidateID: candidateIDs["component[].code.text"],
							CanonicalPath: "code.text", BoundarySelector: componentOwner.BoundarySelector,
						},
					},
					{
						OutputColumnID: "coding_id",
						FieldSource: &recipe.ConstructionGroupFieldSource{
							Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: sourceContext.SourceStageID,
							SourceShapeID: sourceContext.SourceShapeID, SourceRole: "CURRENT_ITEM",
							OccurrenceID:      sourceContext.SourceExpansion.OwnerOccurrenceID,
							OwnerResourceType: sourceContext.SourceExpansion.OwnerResourceType, ResourceType: "Coding",
							CanonicalPath: sourceContext.SourceExpansion.Selector,
						},
					},
				},
				Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "occurrences_id"}},
			}},
			RowValues: []recipe.ConstructionRowValue{{
				InputColumnID: "owner_id", OutputColumnID: "owners_id", Policy: recipe.ConstructionRowValueAll,
			}},
			Outputs: []recipe.StageColumn{
				{ID: "component_marker_id", Name: "component_marker", Type: "string"},
				{ID: "coding_id", Name: "coding", Type: "string", Nullable: true},
				{ID: "occurrences_id", Name: "occurrences", Type: "integer"},
				{ID: "owners_id", Name: "owners", Type: "array"},
			},
		}},
	}
	if includeComponentValues {
		rowValueSource := &recipe.ConstructionGroupFieldSource{
			Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: sourceContext.SourceStageID,
			SourceShapeID: sourceContext.SourceShapeID, SourceRole: "ANCESTOR",
			OccurrenceID: componentOwner.OccurrenceID, OwnerResourceType: "Observation",
			ResourceType: "ObservationComponent", CandidateID: candidateIDs["component[].valueQuantity.value"],
			CanonicalPath: "valueQuantity.value", BoundarySelector: componentOwner.BoundarySelector,
		}
		step := &base.Construction.Steps[0]
		step.RowValues = append(step.RowValues, recipe.ConstructionRowValue{
			FieldSource: rowValueSource, OutputColumnID: "component_values_id", Policy: recipe.ConstructionRowValueAll,
		})
		step.Outputs = append(step.Outputs, recipe.StageColumn{ID: "component_values_id", Name: "component_values", Type: "array"})
	}
	return base
}

func expandedAncestorFieldCandidateIDs(t *testing.T) map[string]string {
	t.Helper()
	identity := capability.SnapshotIdentity{
		Project: "group-ancestor-field-source-fixture", Generation: "generation-group-ancestor-field-source",
		AuthorizationScopeDigest: "scope", SchemaDigest: "schema", ResourceInventoryDigest: "resources",
		RelationshipDigest: "relationships", FieldDigest: "fields", ProtocolVersion: "test",
		CompilerVersion: "test", TraversalPolicyVersion: "test-route", ProjectionPolicyVersion: "test-projection",
	}
	evidence := capability.Evidence{
		Resources: []capability.ResourceObservation{
			{ResourceType: "Patient", Populated: true, DocumentCount: 1},
			{ResourceType: "Observation", Populated: true, DocumentCount: 2},
		},
		Fields: []capability.FieldObservation{
			{
				ResourceType: "Observation", Path: "component[].code.text", Label: "Component code text",
				LogicalType: "string", Cardinality: "optional_one", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "component[]", MaxItems: 4}},
				Observed: true, ObservedDocumentCount: 2, Populated: true,
			},
			{
				ResourceType: "Observation", Path: "component[].valueQuantity.value", Label: "Component quantity value",
				LogicalType: "decimal", Cardinality: "optional_one", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "component[]", MaxItems: 4}},
				Observed: true, ObservedDocumentCount: 2, Populated: true,
			},
		},
		ResourcesAvailable: true, RelationshipsAvailable: true, FieldsAvailable: true,
	}
	compiler := capability.CompilerCallbacks{
		Node: func(_ context.Context, _ capability.Node) (capability.NodeProof, error) {
			return capability.NodeProof{
				Allowed: true, RowRootEligible: true, RowGrain: "RESOURCE",
				SupportedOperations: []capability.Operation{capability.OperationSelect},
			}, nil
		},
		Edge: func(context.Context, capability.Edge) (capability.EdgeProof, error) {
			return capability.EdgeProof{Allowed: true}, nil
		},
		Candidate: func(_ context.Context, candidate capability.Candidate) (capability.CandidateProof, error) {
			return capability.CandidateProof{
				Allowed: true, LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
				ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
				SupportedOperations: []capability.Operation{capability.OperationSelect},
			}, nil
		},
	}
	snapshot, err := capability.NewBuilder(identity, evidence, compiler).Build(context.Background())
	if err != nil || !snapshot.Usable() {
		t.Fatalf("build exact component field candidates: usable=%t err=%v", snapshot.Usable(), err)
	}
	candidateIDs := make(map[string]string, 2)
	for _, candidate := range snapshot.Candidates {
		if candidate.ResourceType == "Observation" && (candidate.FieldPath == "component[].code.text" || candidate.FieldPath == "component[].valueQuantity.value") {
			candidateIDs[candidate.FieldPath] = candidate.ID
		}
	}
	for _, path := range []string{"component[].code.text", "component[].valueQuantity.value"} {
		if candidateIDs[path] == "" {
			t.Fatalf("capability Builder returned no server candidate ID for Observation.%s: %#v", path, snapshot.Candidates)
		}
	}
	return candidateIDs
}

func assertAncestorQuantityNotPreprojected(
	t *testing.T,
	source lower.CompiledRecipeOutput,
	sourceDescriptor lower.CompiledStageDescriptor,
	group lower.CompiledRecipeOutput,
) {
	t.Helper()
	assertNoAncestorQuantityColumn := func(columns []lower.CompiledOutputColumn, label string) {
		for _, column := range columns {
			if strings.Contains(column.SemanticPath, "valueQuantity.value") || column.ID == "component_values_id" || column.Name == "component_values" {
				t.Fatalf("%s preprojected ancestor quantity column: %#v", label, column)
			}
		}
	}
	assertNoAncestorQuantityColumn(sourceDescriptor.Columns, "source descriptor")
	assertNoAncestorQuantityColumn(source.OutputSchema, "source output")
	assertNoAncestorQuantityColumn(group.OutputSchema, "Group without the added RowValue")
	for label, compiled := range map[string]lower.CompiledRecipeOutput{"source": source, "Group before Add": group} {
		plan, err := json.Marshal(compiled.Plan)
		if err != nil {
			t.Fatalf("marshal %s physical plan: %v", label, err)
		}
		if strings.Contains(string(plan), "valueQuantity.value") || strings.Contains(string(plan), "component_values_id") {
			t.Fatalf("%s physical source/projections contain the ancestor quantity before Add: %s", label, plan)
		}
	}
	if group.Plan.StageSequence == nil || len(group.Plan.StageSequence.SourceColumns) == 0 {
		t.Fatalf("Group-before-Add plan has no typed source columns: %#v", group.Plan.StageSequence)
	}
	for _, column := range group.Plan.StageSequence.SourceColumns {
		if column.ID == "component_values_id" || column.Name == "component_values" || strings.Contains(column.Name, "valueQuantity") {
			t.Fatalf("Group-before-Add source columns contain the unrequested ancestor quantity: %#v", column)
		}
	}
	for _, stage := range group.Plan.StageSequence.Stages {
		if stage.ID != "group_component_coding" {
			continue
		}
		for _, column := range stage.InputColumns {
			if column.ID == "component_values_id" || column.Name == "component_values" || strings.Contains(column.Name, "valueQuantity") {
				t.Fatalf("Group-before-Add input columns contain the unrequested ancestor quantity: %#v", column)
			}
		}
		projections, err := json.Marshal(stage.InputProjections)
		if err != nil {
			t.Fatalf("marshal Group-before-Add input projections: %v", err)
		}
		if strings.Contains(string(projections), "valueQuantity.value") || strings.Contains(string(projections), "component_values_id") {
			t.Fatalf("Group-before-Add input projections contain the unrequested ancestor quantity: %s", projections)
		}
	}
}

func expandedAncestorCodingObservation(id string, componentOneQuantity float64, componentOneCodingCount int, includeSecondComponent bool) map[string]any {
	components := []any{expandedAncestorCodingComponent("component-one", componentOneQuantity, componentOneCodingCount)}
	if includeSecondComponent {
		components = append(components, expandedAncestorCodingComponent("component-two", 20, 1))
	}
	return map[string]any{"id": id, "resourceType": "Observation", "component": components}
}

func assertExpandedAncestorCodingShape(t *testing.T, observation map[string]any, wantMarkers []string, wantCounts []int) {
	t.Helper()
	components, ok := observation["component"].([]any)
	if !ok || len(components) != len(wantCounts) || len(wantMarkers) != len(wantCounts) {
		t.Fatalf("ancestor Coding fixture components = %#v, want %d components", observation["component"], len(wantCounts))
	}
	for index, rawComponent := range components {
		component, ok := rawComponent.(map[string]any)
		if !ok {
			t.Fatalf("ancestor Coding fixture component[%d] = %#v, want object", index, rawComponent)
		}
		code, ok := component["code"].(map[string]any)
		if !ok {
			t.Fatalf("ancestor Coding fixture component[%d].code = %#v, want CodeableConcept object", index, component["code"])
		}
		if code["text"] != wantMarkers[index] {
			t.Fatalf("ancestor Coding fixture component[%d] marker = %#v, want %q", index, code["text"], wantMarkers[index])
		}
		codings, ok := code["coding"].([]any)
		if !ok || len(codings) != wantCounts[index] {
			t.Fatalf("ancestor Coding fixture component[%d] Coding count = %#v, want %d", index, code["coding"], wantCounts[index])
		}
	}
}

func assertExpandedAncestorCodingQuantities(t *testing.T, observation map[string]any, want []float64) {
	t.Helper()
	components, ok := observation["component"].([]any)
	if !ok || len(components) != len(want) {
		t.Fatalf("ancestor Coding fixture components = %#v, want %d quantity-bearing components", observation["component"], len(want))
	}
	for index, rawComponent := range components {
		component, ok := rawComponent.(map[string]any)
		if !ok {
			t.Fatalf("ancestor Coding fixture component[%d] = %#v, want object", index, rawComponent)
		}
		valueQuantity, ok := component["valueQuantity"].(map[string]any)
		if !ok || !constructionNumericEqual(valueQuantity["value"], want[index]) {
			t.Fatalf("ancestor Coding fixture component[%d] quantity = %#v, want %v", index, component["valueQuantity"], want[index])
		}
	}
}

func expandedAncestorCodingComponent(marker string, quantity float64, codingCount int) map[string]any {
	codings := make([]any, 0, codingCount)
	for index := 0; index < codingCount; index++ {
		codings = append(codings, map[string]any{
			"system": "urn:loom:ancestor-group", "version": "v1", "code": "shared", "display": "Shared",
		})
	}
	return map[string]any{
		"code": map[string]any{
			"text":   marker,
			"coding": codings,
		},
		"valueQuantity": map[string]any{
			"value": quantity, "unit": "mg", "system": "http://unitsofmeasure.org", "code": "mg",
		},
	}
}

func assertExpandedAncestorGroupNumericValues(t *testing.T, raw any, want []float64) {
	t.Helper()
	values, ok := raw.([]any)
	if !ok {
		t.Fatalf("component ancestor RowValues = %#v, want numeric array", raw)
	}
	got := make([]float64, 0, len(values))
	for _, value := range values {
		got = append(got, numericValue(value))
	}
	sort.Float64s(got)
	want = append([]float64(nil), want...)
	sort.Float64s(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("component ancestor RowValues = %#v, want exact distinct values %#v", got, want)
	}
}

func assertExpandedAncestorGroupIdentity(t *testing.T, row map[string]any, component string) {
	t.Helper()
	if row["component_marker"] != component {
		t.Fatalf("component group marker = %#v, want %q", row["component_marker"], component)
	}
	assertExpandedCodingIdentityTuple(t, row, "urn:loom:ancestor-group", "v1", "shared")
	rowID, ok := row["__loom_row_id"].(string)
	if !ok || rowID == "" {
		t.Fatalf("component Coding group row ID = %#v, want exact grouped identity", row["__loom_row_id"])
	}
	var identity []any
	if err := json.Unmarshal([]byte(rowID), &identity); err != nil {
		t.Fatalf("decode component Coding group row ID %q: %v", rowID, err)
	}
	want := []any{
		[]any{"construction", "group_component_coding"},
		[]any{"operation", "GROUP"},
		[]any{"component_marker_id", component},
		[]any{"coding_id", "coding_identity", []any{"urn:loom:ancestor-group", "v1", "shared"}},
	}
	if !reflect.DeepEqual(identity, want) {
		t.Fatalf("component Coding group row identity = %#v, want exact marker and Coding tuple identity %#v", identity, want)
	}
}

func expandedCodingGroupSourceOutput(routeName, selectionID string) recipe.Output {
	return recipe.Output{
		Name: "expanded_coding_field_source", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
		TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "observation-owner", From: recipe.Expression{Select: "observation.valueCodeableConcept.coding[]"},
			As: "coding_item", Ordinality: "coding_ordinal", EmptyPolicy: recipe.ExpansionPreserveParent,
		},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
		Traversals: []recipe.Traversal{{
			Name: routeName, OccurrenceID: "observation-owner", Alias: "observation", ToResourceType: "Observation", MatchMode: recipe.MatchRequired,
			Fields: []recipe.Field{
				{Name: "owner_id", ColumnID: "owner_id", Expr: recipe.Expression{Select: "observation.id"}},
				{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "observation.status"}},
			},
		}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: selectionID, MembershipDigest: selectionID + "-membership",
			MemberCount: 1, ResourceType: "Patient",
		},
	}
}

func expandedCodingGroupSourceContext(t *testing.T, output recipe.Output, bindings recipe.RuntimeBindings) lower.CompiledGroupFieldSourceContext {
	t.Helper()
	output.Construction = nil
	compiled, err := compileExpandedCodingGroupOutput(t, output, bindings)
	if err != nil {
		t.Fatalf("lower expanded Group source: %v", err)
	}
	descriptor, err := lower.DescribeConstructionSourceStage(compiled.OutputSchema, compiled.RootResourceType, &compiled.Plan)
	if err != nil {
		t.Fatalf("describe canonical expanded Group source: %v", err)
	}
	if descriptor.GroupFieldSourceContext == nil {
		t.Fatalf("canonical source descriptor has no Group field-source context: %#v", descriptor)
	}
	return *descriptor.GroupFieldSourceContext
}

func assertExpandedCodingGroupUsesProjectedMembership(t *testing.T, compiled lower.CompiledRecipeOutput, query CompiledQuery) {
	t.Helper()
	sequence := compiled.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 1 || sequence.Stages[0].Group == nil ||
		sequence.Stages[0].Group.CodingKey == nil {
		t.Fatalf("expanded Coding Group has no canonical CodingKey IR: %#v", sequence)
	}
	stage := sequence.Stages[0]
	coding := stage.Group.CodingKey
	if stage.InputRowVariable == "" || coding.MembershipColumn == "" {
		t.Fatalf("expanded Coding Group has incomplete input row or membership column: stage=%#v coding=%#v", stage, coding)
	}
	membershipBind := ""
	for key, value := range query.BindVars {
		if value == coding.MembershipColumn && strings.Contains(key, "construction_group_coding_membership_column") {
			if membershipBind != "" {
				t.Fatalf("Coding membership column %q has multiple query binds %q and %q", coding.MembershipColumn, membershipBind, key)
			}
			membershipBind = key
		}
	}
	if membershipBind == "" {
		t.Fatalf("compiled query has no bind for Coding membership column %q", coding.MembershipColumn)
	}
	read := fmt.Sprintf("%s[@%s]", stage.InputRowVariable, membershipBind)
	readAt := strings.Index(query.Query, read)
	if readAt < 0 {
		t.Fatalf("Group query does not read projected Coding membership at %s:\n%s", read, query.Query)
	}
	collectAtRelative := strings.Index(query.Query[readAt+len(read):], "COLLECT ")
	if collectAtRelative < 0 {
		t.Fatalf("Group query does not collect after reading Coding membership:\n%s", query.Query)
	}
	groupKeyRead := query.Query[readAt : readAt+len(read)+collectAtRelative]
	if strings.Contains(groupKeyRead, "DOCUMENT(") || strings.Contains(groupKeyRead, ".payload") {
		t.Fatalf("Group key evaluation re-scans or walks a root payload instead of using the projected Coding membership:\n%s", groupKeyRead)
	}
}

func expandedCodingGroupOutput(base recipe.Output, sourceContext lower.CompiledGroupFieldSourceContext, policy recipe.ConstructionGroupMissingKeyPolicy) recipe.Output {
	output := base
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "owner_id", Name: "owner_id", Type: "string"},
			{ID: "status_id", Name: "status", Type: "string"},
		},
		Steps: []recipe.ConstructionStep{{
			ID: "group_expanded_coding", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
				ConstructionID: "group_expanded_coding", MissingKeyPolicy: policy,
				Keys: []recipe.ConstructionGroupKey{
					{InputColumnID: "status_id", OutputColumnID: "group_status_id"},
					{
						OutputColumnID: "coding_id",
						FieldSource: &recipe.ConstructionGroupFieldSource{
							Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: sourceContext.SourceStageID,
							SourceShapeID: sourceContext.SourceShapeID, SourceRole: "CURRENT_ITEM",
							OccurrenceID:      sourceContext.SourceExpansion.OwnerOccurrenceID,
							OwnerResourceType: sourceContext.SourceExpansion.OwnerResourceType, ResourceType: "Coding",
							CanonicalPath: sourceContext.SourceExpansion.Selector,
						},
					},
				},
				Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "occurrences_id"}},
			}},
			RowValues: []recipe.ConstructionRowValue{{InputColumnID: "owner_id", OutputColumnID: "owners_id", Policy: recipe.ConstructionRowValueAll}},
			Outputs: []recipe.StageColumn{
				{ID: "group_status_id", Name: "status", Type: "string"},
				{ID: "coding_id", Name: "coding", Type: "string", Nullable: true},
				{ID: "occurrences_id", Name: "occurrences", Type: "integer"},
				{ID: "owners_id", Name: "owners", Type: "array"},
			},
		}},
	}
	return output
}

func compileExpandedCodingGroupOutput(t *testing.T, output recipe.Output, bindings recipe.RuntimeBindings) (lower.CompiledRecipeOutput, error) {
	t.Helper()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return lower.CompiledRecipeOutput{}, err
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		return lower.CompiledRecipeOutput{}, err
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return lower.CompiledRecipeOutput{}, err
	}
	return compiled.Outputs[0], nil
}

func expandedCodingGroupObservation(id string, includeSharedDuplicates bool) map[string]any {
	coding := []any{
		map[string]any{"system": "urn:loom:group", "version": "v1", "code": "000-shared", "display": "Zeta"},
		map[string]any{"system": "urn:loom:group", "version": "v1", "code": "000-shared", "display": "Zeta"},
		map[string]any{"system": "urn:loom:group", "version": "v1", "code": "000-shared", "display": "Alpha"},
		map[string]any{"system": "urn:loom:group", "version": "v1", "code": "000-shared", "display": " "},
		map[string]any{"system": "urn:loom:group", "version": "v1", "code": "same-display", "display": "Twin"},
		map[string]any{"system": "urn:loom:other", "version": "v1", "code": "same-display", "display": "Twin"},
		map[string]any{"system": "urn:loom:group", "version": "v2", "code": "same-display", "display": "Twin"},
		map[string]any{"system": "urn:loom:group", "version": "v1", "code": "different-code", "display": "Twin"},
		map[string]any{"code": "no-system", "display": "No system"},
		map[string]any{"system": nil, "code": "no-system", "display": "No system"},
		map[string]any{"system": "urn:loom:group", "code": "no-system", "display": "No system"},
		map[string]any{"system": "urn:loom:group", "code": "versionless", "display": "Versionless B"},
		map[string]any{"system": "urn:loom:group", "version": "   ", "code": "versionless", "display": "Versionless A"},
		map[string]any{"system": "urn:loom:group", "version": "", "code": "versionless", "display": "Versionless C"},
		map[string]any{"system": "urn:loom:group", "version": "v1", "code": "versionless", "display": "Versionless A"},
		map[string]any{"system": "urn:loom:group", "display": "Missing code"},
		map[string]any{"system": "urn:loom:group", "code": "", "display": "Empty code"},
		map[string]any{"system": "urn:loom:group", "code": "   ", "display": "Whitespace code"},
		map[string]any{"system": "urn:loom:group", "code": "fallback-code", "display": "  "},
	}
	if !includeSharedDuplicates {
		coding = []any{}
	}
	return map[string]any{
		"id": id, "resourceType": "Observation", "status": "final",
		"valueCodeableConcept": map[string]any{"coding": coding},
	}
}

func insertExpandedCodingGroupSelectionMember(t *testing.T, ctx context.Context, client *store.Client, project, generation, selectionID string) {
	t.Helper()
	document, err := json.Marshal(map[string]any{
		"_key": selectionID + "_patient", "selectionId": selectionID,
		"project": project, "generation": generation, "resourceType": "Patient", "id": "patient",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "loom_explorer_selection_members", []json.RawMessage{document}, false, "document"); err != nil {
		t.Fatalf("insert typed Population selection member: %v", err)
	}
}

func replaceExpandedCodingGroupObservation(t *testing.T, ctx context.Context, client *store.Client, key string, payload map[string]any) {
	t.Helper()
	err := client.ExecuteAQL(ctx,
		"FOR document IN Observation FILTER document._key == @key UPDATE document WITH {payload: @payload} IN Observation",
		map[string]any{"key": key, "payload": payload},
	)
	if err != nil {
		t.Fatalf("replace expanded Coding Group Observation payload: %v", err)
	}
}

func insertExpandedCodingGroupResource(t *testing.T, ctx context.Context, client *store.Client, collection, key, project, generation string, payload map[string]any) {
	t.Helper()
	document, err := json.Marshal(map[string]any{
		"_key": key, "id": payload["id"], "project": project, "project_id": project,
		"dataset_generation": generation, "resourceType": collection, "payload": payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, collection, []json.RawMessage{document}, false, "document"); err != nil {
		t.Fatalf("insert expanded Coding Group %s fixture: %v", collection, err)
	}
}

func insertExpandedCodingGroupEdge(t *testing.T, ctx context.Context, client *store.Client, key, observationKey, patientKey, project, generation string) {
	t.Helper()
	document, err := json.Marshal(map[string]any{
		"_key": key, "_from": "Observation/" + observationKey, "_to": "Patient/" + patientKey,
		"project": project, "project_id": project, "dataset_generation": generation,
		"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_edge", []json.RawMessage{document}, false, "document"); err != nil {
		t.Fatalf("insert expanded Coding Group route edge: %v", err)
	}
}

func assertExpandedCodingGroupOwnerValues(t *testing.T, raw any, want []string) {
	t.Helper()
	values, ok := raw.([]any)
	if !ok {
		t.Fatalf("Group owner RowValues = %#v, want array", raw)
	}
	got := make([]string, 0, len(values))
	for _, value := range values {
		owner, ok := value.(string)
		if !ok {
			t.Fatalf("Group owner RowValue item = %#v, want string", value)
		}
		got = append(got, owner)
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Group owner RowValues = %#v, want %#v", got, want)
	}
}

func assertExpandedCodingGroupLineage(
	t *testing.T,
	ctx context.Context,
	client *store.Client,
	compiled lower.CompiledRecipeOutput,
	row map[string]any,
	project, patientKey string,
	wantOwners map[string]int,
	wantOrdinals map[string]map[float64]bool,
	wantEmptyItems map[string]bool,
) {
	t.Helper()
	rowID, _ := row["__loom_row_id"].(string)
	query, err := CompileRowLineageOutput(compiled, rowID, 0, 20, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile expanded Coding Group lineage for %q: %v", row["coding"], err)
	}
	lineage := executeRowLineageOracleQuery(t, ctx, client, query)
	contributors, _ := lineage["contributors"].([]any)
	wantCount := 0
	for _, count := range wantOwners {
		wantCount += count
	}
	if lineage["found"] != true || lineage["hasMore"] != false || len(contributors) != wantCount {
		t.Fatalf("expanded Coding Group lineage for %q = %#v, want %d exact Coding source occurrences", row["coding"], lineage, wantCount)
	}
	seenOccurrenceKeys := make(map[string]bool, len(contributors))
	seenOrdinals := make(map[string]map[float64]bool)
	for _, raw := range contributors {
		contributor, _ := raw.(map[string]any)
		if contributor["resourceType"] != "Patient" || contributor["resourceId"] != "patient" {
			t.Errorf("expanded Coding Group contributor root = %#v, want Patient/patient", contributor)
			continue
		}
		occurrence := expandedCodingOccurrenceKey(t, contributor["occurrenceKey"])
		ownerKey, _ := occurrence["owner_id"].(string)
		id := ""
		for expectedID := range wantOwners {
			if ownerKey == "Observation/"+project+"_"+strings.ReplaceAll(expectedID, "-", "_") {
				id = expectedID
				break
			}
		}
		if id == "" || wantOwners[id] == 0 {
			t.Errorf("expanded Coding Group occurrence owner = %#v, want an expected routed Observation source occurrence", occurrence)
			continue
		}
		wantOwners[id]--
		edgeKey := project + "_edge_" + strings.TrimPrefix(id, "observation-")
		if occurrence["root_id"] != "Patient/"+patientKey || occurrence["occurrence_id"] != "observation-owner" ||
			occurrence["edge_00_id"] != "fhir_edge/"+edgeKey || ownerKey != "Observation/"+project+"_"+strings.ReplaceAll(id, "-", "_") {
			t.Errorf("expanded Coding occurrence identity = %#v, want exact Patient route owner", occurrence)
		}
		if wantEmptyItems[id] {
			if occurrence["item_present"] != false || occurrence["ordinal"] != nil {
				t.Errorf("preserved empty Coding owner identity = %#v, want no item and null ordinal", occurrence)
			}
		} else {
			if occurrence["item_present"] != true {
				t.Errorf("present Coding occurrence identity = %#v, want item_present=true", occurrence)
			}
			ordinal, ok := occurrence["ordinal"].(float64)
			if !ok || !wantOrdinals[id][ordinal] {
				t.Errorf("expanded Coding occurrence ordinal = %#v, want an expected distinct ordinal for %s", occurrence["ordinal"], id)
			} else {
				if seenOrdinals[id] == nil {
					seenOrdinals[id] = make(map[float64]bool)
				}
				if seenOrdinals[id][ordinal] {
					t.Errorf("expanded Coding preimage ordinal %v for %s was repeated", ordinal, id)
				}
				seenOrdinals[id][ordinal] = true
			}
		}
		keyBytes, _ := json.Marshal(occurrence)
		key := string(keyBytes)
		if seenOccurrenceKeys[key] {
			t.Errorf("two Coding preimages reused occurrence identity %s", key)
		}
		seenOccurrenceKeys[key] = true
	}
	for id, count := range wantOwners {
		if count != 0 {
			t.Errorf("expanded Coding Group lineage has %d missing occurrences for %s", count, id)
		}
	}
	for id, ordinals := range wantOrdinals {
		if len(seenOrdinals[id]) != len(ordinals) {
			t.Errorf("expanded Coding Group lineage ordinals for %s = %#v, want %#v", id, seenOrdinals[id], ordinals)
		}
	}
}

func expandedCodingOccurrenceKey(t *testing.T, raw any) map[string]any {
	t.Helper()
	if value, ok := raw.(map[string]any); ok {
		return value
	}
	if encoded, ok := raw.(string); ok {
		var value map[string]any
		if err := json.Unmarshal([]byte(encoded), &value); err == nil {
			return value
		}
	}
	t.Fatalf("expanded Coding Group occurrence key = %#v, want exact occurrence identity object", raw)
	return nil
}

func assertExpandedCodingIdentityTuple(t *testing.T, row map[string]any, system, version, code any) {
	t.Helper()
	got := expandedCodingIdentityTuple(t, row)
	want := []any{system, version, code}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Coding identity tuple = %#v, want %#v", got, want)
	}
}

func expandedCodingIdentityTuple(t *testing.T, row map[string]any) []any {
	t.Helper()
	rowID, ok := row["__loom_row_id"].(string)
	if !ok || rowID == "" {
		t.Fatalf("Coding Group row identity = %#v, want encoded exact tuple identity", row["__loom_row_id"])
	}
	var identity []any
	if err := json.Unmarshal([]byte(rowID), &identity); err != nil || len(identity) == 0 {
		t.Fatalf("decode Coding Group row identity %q: %v", rowID, err)
	}
	final, ok := identity[len(identity)-1].([]any)
	if !ok || len(final) != 3 || final[0] != "coding_id" || final[1] != "coding_identity" {
		t.Fatalf("final Coding Group identity part = %#v, want [coding_id coding_identity tuple]", identity[len(identity)-1])
	}
	tuple, ok := final[2].([]any)
	if !ok || len(tuple) != 3 {
		t.Fatalf("encoded Coding tuple = %#v, want [system version code]", final[2])
	}
	return tuple
}
