package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestConstructionGroupRelatedOccurrenceCodingFieldSourceAgainstArango(t *testing.T) {
	project, generation := "loom_group_connected_field_source_"+uuid.NewString(), "generation-group-connected-field-source"
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
	source := connectedGroupFieldSourceFixture(t, bindings)
	queries := make(map[string]CompiledQuery, 3)
	compiledByPolicy := make(map[string]lower.CompiledRecipeOutput, 3)
	for _, codingEmptyPolicy := range []string{"GROUP", "EXCLUDE", "ERROR"} {
		output := connectedGroupOutputFromJSON(t, source, codingEmptyPolicy)
		compiled := lowerConstructionOutput(t, output, bindings)
		query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("compile connected Coding Group with coding-empty %s policy: %v", codingEmptyPolicy, err)
		}
		queries[codingEmptyPolicy], compiledByPolicy[codingEmptyPolicy] = query, compiled
	}

	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Specimen"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	keys := map[string][]string{"Patient": {}, "Specimen": {}, "fhir_edge": {}}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for collection, ownedKeys := range keys {
			if len(ownedKeys) == 0 {
				continue
			}
			query := fmt.Sprintf("FOR document IN %s FILTER document._key IN @keys REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"keys": ownedKeys}); err != nil {
				t.Errorf("remove connected Group fixture from %s: %v", collection, err)
			}
		}
	})
	insert := func(collection string, document map[string]any) {
		t.Helper()
		key, _ := document["_key"].(string)
		keys[collection] = append(keys[collection], key)
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encode connected Group %s fixture: %v", collection, err)
		}
		if err := client.InsertBatchRaw(ctx, collection, []json.RawMessage{raw}, false, "document"); err != nil {
			t.Fatalf("insert connected Group %s fixture: %v", collection, err)
		}
	}
	patientKey := func(id string) string { return project + "_patient_" + id }
	specimenKey := func(id string) string { return project + "_specimen_" + id }
	for _, patientID := range []string{"A", "B", "C"} {
		payload := map[string]any{"id": patientID, "resourceType": "Patient"}
		insert("Patient", map[string]any{
			"_key": patientKey(patientID), "id": patientID, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Patient", "payload": payload,
		})
	}
	specimens := []map[string]any{
		connectedSpecimenDocument(project, generation, "A1", "A", "A-one", []any{
			connectedCoding("urn:sysA", "v1", "code-same", "Zeta"),
			connectedCoding("urn:sysA", "v1", "code-same", "Alpha"),
			connectedCodingWithoutSystem("", "code-same", "Alpha"),
			connectedCoding("urn:sysA", "v2", "code-same", "Twin"),
		}),
		connectedSpecimenDocument(project, generation, "A2", "A", "A-two", []any{
			connectedCoding("urn:sysA", "v1", "code-same", "Beta"),
			connectedCoding("urn:sysB", "v1", "code-same", "Twin"),
			connectedCoding("urn:sysA", "v1", "code-different", "Twin"),
		}),
		connectedSpecimenDocument(project, generation, "B1", "B", "B-one", []any{
			connectedCoding("urn:sysA", "v1", "code-same", "B-one"),
		}),
		connectedSpecimenDocument(project, generation, "B2", "B", "B-two", []any{
			connectedCoding("urn:sysB", "v2", "code-same", "B-two"),
		}),
		connectedSpecimenDocument(project, generation, "B-empty", "B", "empty-coding", []any{}),
	}
	specimensByID := make(map[string]map[string]any, len(specimens))
	for _, document := range specimens {
		insert("Specimen", document)
		specimenID, _ := document["id"].(string)
		specimensByID[specimenID] = document
	}
	edgeKeysBySpecimen := make(map[string][]string)
	edgeDocumentsBySpecimen := make(map[string][]map[string]any)
	addRoute := func(edgeSuffix, specimenID, patientID string) {
		edgeKey := project + "_edge_" + edgeSuffix
		edgeDocument := map[string]any{
			"_key": edgeKey, "_from": "Specimen/" + specimenKey(specimenID), "_to": "Patient/" + patientKey(patientID),
			"project": project, "project_id": project, "dataset_generation": generation,
			"label": "subject_Patient", "from_type": "Specimen", "to_type": "Patient",
		}
		insert("fhir_edge", edgeDocument)
		edgeKeysBySpecimen[specimenID] = append(edgeKeysBySpecimen[specimenID], edgeKey)
		edgeDocumentsBySpecimen[specimenID] = append(edgeDocumentsBySpecimen[specimenID], edgeDocument)
	}
	addRoute("a1-00", "A1", "A")
	addRoute("a1-01-duplicate", "A1", "A")
	addRoute("a2-00", "A2", "A")
	addRoute("b1-00", "B1", "B")
	addRoute("b1-01-duplicate", "B1", "B")
	addRoute("b2-00", "B2", "B")
	addRoute("b-empty-00", "B-empty", "B")

	groupRows := executeConnectedGroupQuery(t, ctx, client, queries["GROUP"])
	if len(groupRows) != 10 {
		t.Fatalf("connected GROUP rows = %#v, want ten exact Patient/Specimen/Coding groups", groupRows)
	}
	rowIDs := make(map[string]bool, len(groupRows))
	for _, row := range groupRows {
		rowID, _ := row["__loom_row_id"].(string)
		if rowID == "" || rowIDs[rowID] {
			t.Fatalf("connected Group row identity is absent or reused: %q; row=%#v", rowID, row)
		}
		rowIDs[rowID] = true
	}
	aOneShared := connectedGroupRow(t, groupRows, "A", "A-one", []any{"urn:sysA", "v1", "code-same"})
	assertConnectedGroupRow(t, aOneShared, "A", "A-one", []any{"urn:sysA", "v1", "code-same"}, 2, "Alpha", []string{"A1"})
	aTwoShared := connectedGroupRow(t, groupRows, "A", "A-two", []any{"urn:sysA", "v1", "code-same"})
	assertConnectedGroupRow(t, aTwoShared, "A", "A-two", []any{"urn:sysA", "v1", "code-same"}, 1, "Beta", []string{"A2"})
	aVersionless := connectedGroupRow(t, groupRows, "A", "A-one", []any{nil, nil, "code-same"})
	assertConnectedGroupRow(t, aVersionless, "A", "A-one", []any{nil, nil, "code-same"}, 1, "Alpha", []string{"A1"})
	aTwinSystem := connectedGroupRow(t, groupRows, "A", "A-one", []any{"urn:sysA", "v2", "code-same"})
	assertConnectedGroupRow(t, aTwinSystem, "A", "A-one", []any{"urn:sysA", "v2", "code-same"}, 1, "Twin", []string{"A1"})
	aTwinVersion := connectedGroupRow(t, groupRows, "A", "A-two", []any{"urn:sysB", "v1", "code-same"})
	assertConnectedGroupRow(t, aTwinVersion, "A", "A-two", []any{"urn:sysB", "v1", "code-same"}, 1, "Twin", []string{"A2"})
	aTwinCode := connectedGroupRow(t, groupRows, "A", "A-two", []any{"urn:sysA", "v1", "code-different"})
	assertConnectedGroupRow(t, aTwinCode, "A", "A-two", []any{"urn:sysA", "v1", "code-different"}, 1, "Twin", []string{"A2"})
	twinRowIDs := []any{aTwinSystem["__loom_row_id"], aTwinVersion["__loom_row_id"], aTwinCode["__loom_row_id"]}
	if twinRowIDs[0] == twinRowIDs[1] || twinRowIDs[0] == twinRowIDs[2] || twinRowIDs[1] == twinRowIDs[2] {
		t.Fatalf("same-display Coding identities reused a Group row ID: %#v", twinRowIDs)
	}
	bShared := connectedGroupRow(t, groupRows, "B", "B-one", []any{"urn:sysA", "v1", "code-same"})
	assertConnectedGroupRow(t, bShared, "B", "B-one", []any{"urn:sysA", "v1", "code-same"}, 1, "B-one", []string{"B1"})
	bOther := connectedGroupRow(t, groupRows, "B", "B-two", []any{"urn:sysB", "v2", "code-same"})
	assertConnectedGroupRow(t, bOther, "B", "B-two", []any{"urn:sysB", "v2", "code-same"}, 1, "B-two", []string{"B2"})
	bEmpty := connectedGroupRow(t, groupRows, "B", "empty-coding", []any{nil, nil, nil})
	assertConnectedGroupRow(t, bEmpty, "B", "empty-coding", []any{nil, nil, nil}, 1, nil, []string{"B-empty"})
	cMissingRoute := connectedGroupRow(t, groupRows, "C", nil, []any{nil, nil, nil})
	assertConnectedGroupRow(t, cMissingRoute, "C", nil, []any{nil, nil, nil}, 1, nil, nil)
	if bEmpty["__loom_row_id"] == cMissingRoute["__loom_row_id"] {
		t.Fatalf("present empty-Coding target and absent Specimen route reused identity: empty=%#v absent=%#v", bEmpty, cMissingRoute)
	}

	excludedRows := executeConnectedGroupQuery(t, ctx, client, queries["EXCLUDE"])
	if len(excludedRows) != 9 {
		t.Fatalf("connected EXCLUDE rows = %#v, want eight coded groups and the absent-route Patient C group", excludedRows)
	}
	for _, row := range excludedRows {
		if tuple := connectedCodingIdentityTuple(t, row); tuple[2] == nil && row["patient"] != "C" {
			t.Fatalf("connected EXCLUDE retained a missing-code group: %#v", row)
		}
		for _, specimenID := range connectedRowValueStrings(t, row) {
			if specimenID == "B-empty" {
				t.Fatalf("connected EXCLUDE retained the matched empty-Coding target: %#v", row)
			}
		}
	}
	assertConnectedGroupRow(t,
		connectedGroupRow(t, excludedRows, "A", "A-one", []any{"urn:sysA", "v1", "code-same"}),
		"A", "A-one", []any{"urn:sysA", "v1", "code-same"}, 2, "Alpha", []string{"A1"})
	assertConnectedGroupRow(t,
		connectedGroupRow(t, excludedRows, "A", "A-two", []any{"urn:sysA", "v1", "code-same"}),
		"A", "A-two", []any{"urn:sysA", "v1", "code-same"}, 1, "Beta", []string{"A2"})
	assertConnectedGroupRow(t,
		connectedGroupRow(t, excludedRows, "A", "A-one", []any{nil, nil, "code-same"}),
		"A", "A-one", []any{nil, nil, "code-same"}, 1, "Alpha", []string{"A1"})
	assertConnectedGroupRow(t,
		connectedGroupRow(t, excludedRows, "A", "A-one", []any{"urn:sysA", "v2", "code-same"}),
		"A", "A-one", []any{"urn:sysA", "v2", "code-same"}, 1, "Twin", []string{"A1"})
	assertConnectedGroupRow(t,
		connectedGroupRow(t, excludedRows, "A", "A-two", []any{"urn:sysB", "v1", "code-same"}),
		"A", "A-two", []any{"urn:sysB", "v1", "code-same"}, 1, "Twin", []string{"A2"})
	assertConnectedGroupRow(t,
		connectedGroupRow(t, excludedRows, "A", "A-two", []any{"urn:sysA", "v1", "code-different"}),
		"A", "A-two", []any{"urn:sysA", "v1", "code-different"}, 1, "Twin", []string{"A2"})
	assertConnectedGroupRow(t,
		connectedGroupRow(t, excludedRows, "B", "B-one", []any{"urn:sysA", "v1", "code-same"}),
		"B", "B-one", []any{"urn:sysA", "v1", "code-same"}, 1, "B-one", []string{"B1"})
	assertConnectedGroupRow(t,
		connectedGroupRow(t, excludedRows, "B", "B-two", []any{"urn:sysB", "v2", "code-same"}),
		"B", "B-two", []any{"urn:sysB", "v2", "code-same"}, 1, "B-two", []string{"B2"})
	cExcluded := connectedGroupRow(t, excludedRows, "C", nil, []any{nil, nil, nil})
	assertConnectedGroupRow(t, cExcluded, "C", nil, []any{nil, nil, nil}, 1, nil, nil)
	if cExcluded["__loom_row_id"] != cMissingRoute["__loom_row_id"] {
		t.Fatalf("connected EXCLUDE changed the absent-route Patient C identity: GROUP=%#v EXCLUDE=%#v", cMissingRoute, cExcluded)
	}
	errorQuery := queries["ERROR"]
	if err := client.QueryRows(ctx, errorQuery.Query, 100, errorQuery.BindVars, func(map[string]any) error { return nil }); err == nil {
		t.Fatal("connected ERROR Coding-empty policy accepted the matched B-empty target")
	}
	removeDocument := func(collection, key string) {
		t.Helper()
		query := fmt.Sprintf("FOR document IN %s FILTER document._key == @key REMOVE document IN %s", collection, collection)
		if err := client.ExecuteAQL(ctx, query, map[string]any{"key": key}); err != nil {
			t.Fatalf("temporarily remove connected fixture %s/%s: %v", collection, key, err)
		}
	}
	removeDocument("Specimen", specimenKey("B-empty"))
	for _, edgeKey := range edgeKeysBySpecimen["B-empty"] {
		removeDocument("fhir_edge", edgeKey)
	}
	errorWithoutEmptyRows := executeConnectedGroupQuery(t, ctx, client, errorQuery)
	if len(errorWithoutEmptyRows) != 9 {
		t.Fatalf("connected ERROR rows without matched empty-Coding owner = %#v, want eight coded groups plus absent-route Patient C", errorWithoutEmptyRows)
	}
	cAfterRemovingEmpty := connectedGroupRow(t, errorWithoutEmptyRows, "C", nil, []any{nil, nil, nil})
	assertConnectedGroupRow(t, cAfterRemovingEmpty, "C", nil, []any{nil, nil, nil}, 1, nil, nil)
	if cAfterRemovingEmpty["__loom_row_id"] != cMissingRoute["__loom_row_id"] {
		t.Fatalf("connected ERROR changed the absent-route Patient C identity after removing B-empty: GROUP=%#v ERROR=%#v", cMissingRoute, cAfterRemovingEmpty)
	}
	for _, row := range errorWithoutEmptyRows {
		for _, specimenID := range connectedRowValueStrings(t, row) {
			if specimenID == "B-empty" {
				t.Fatalf("connected ERROR query returned removed B-empty target: %#v", row)
			}
		}
	}
	insert("Specimen", specimensByID["B-empty"])
	for _, edgeDocument := range edgeDocumentsBySpecimen["B-empty"] {
		insert("fhir_edge", edgeDocument)
	}

	if capability := RowLineageCapabilityForOutput(compiledByPolicy["GROUP"]); !capability.Available {
		t.Fatalf("connected Group row-lineage capability = %#v, want exact related owner and Coding ordinal lineage", capability)
	}
	assertConnectedGroupLineage(t, ctx, client, compiledByPolicy["GROUP"], aOneShared, project, patientKey("A"),
		map[string]map[int]bool{"A1": {0: true, 1: true}}, "", false)
	assertConnectedGroupLineage(t, ctx, client, compiledByPolicy["GROUP"], aTwoShared, project, patientKey("A"),
		map[string]map[int]bool{"A2": {0: true}}, "", false)
	assertConnectedGroupLineage(t, ctx, client, compiledByPolicy["GROUP"], aVersionless, project, patientKey("A"),
		map[string]map[int]bool{"A1": {2: true}}, "", false)
	assertConnectedGroupLineage(t, ctx, client, compiledByPolicy["GROUP"], aTwinSystem, project, patientKey("A"),
		map[string]map[int]bool{"A1": {3: true}}, "", false)
	assertConnectedGroupLineage(t, ctx, client, compiledByPolicy["GROUP"], aTwinVersion, project, patientKey("A"),
		map[string]map[int]bool{"A2": {1: true}}, "", false)
	assertConnectedGroupLineage(t, ctx, client, compiledByPolicy["GROUP"], aTwinCode, project, patientKey("A"),
		map[string]map[int]bool{"A2": {2: true}}, "", false)
	assertConnectedGroupLineage(t, ctx, client, compiledByPolicy["GROUP"], bShared, project, patientKey("B"),
		map[string]map[int]bool{"B1": {0: true}}, "", false)
	assertConnectedGroupLineage(t, ctx, client, compiledByPolicy["GROUP"], bOther, project, patientKey("B"),
		map[string]map[int]bool{"B2": {0: true}}, "", false)
	assertConnectedGroupLineage(t, ctx, client, compiledByPolicy["GROUP"], bEmpty, project, patientKey("B"),
		map[string]map[int]bool{}, "B-empty", false)
	assertConnectedGroupLineage(t, ctx, client, compiledByPolicy["GROUP"], cMissingRoute, project, patientKey("C"),
		map[string]map[int]bool{}, "", true)
}

type connectedGroupSourceFixture struct {
	GroupFieldSourceContext *lower.CompiledGroupFieldSourceContext
	PatientNodeID           string
	SpecimenNodeID          string
	PatientSpecimenEdge     string
	CandidateIDs            map[string]string
}

func connectedGroupFieldSourceFixture(t *testing.T, bindings recipe.RuntimeBindings) connectedGroupSourceFixture {
	t.Helper()
	identity := capability.SnapshotIdentity{
		Project: "connected-group-field-source", Generation: "connected-group-generation",
		AuthorizationScopeDigest: "scope", SchemaDigest: "schema", ResourceInventoryDigest: "resources",
		RelationshipDigest: "relationships", FieldDigest: "fields", ProtocolVersion: "test",
		CompilerVersion: "test", TraversalPolicyVersion: "test-route", ProjectionPolicyVersion: "test-projection",
	}
	evidence := capability.Evidence{
		Resources: []capability.ResourceObservation{
			{ResourceType: "Patient", Populated: true, DocumentCount: 3},
			{ResourceType: "Specimen", Populated: true, DocumentCount: 5},
		},
		Relationships: []capability.RelationshipObservation{{
			SourceResourceType: "Patient", TargetResourceType: "Specimen", Label: "subject_Patient",
			StorageDirection: "INBOUND", BuilderDirection: "INBOUND", ObservedEdgeCount: 7,
		}},
		Fields: []capability.FieldObservation{
			{ResourceType: "Specimen", Path: "id", Label: "Specimen ID", LogicalType: "string", Cardinality: "required_one", Observed: true, ObservedDocumentCount: 5, Populated: true},
			{ResourceType: "Specimen", Path: "type.text", Label: "Specimen type text", LogicalType: "string", Cardinality: "optional_one", Observed: true, ObservedDocumentCount: 5, Populated: true},
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
		t.Fatalf("build connected Group candidate source: usable=%t err=%v", snapshot.Usable(), err)
	}
	fixture := connectedGroupSourceFixture{CandidateIDs: make(map[string]string, 2)}
	for _, node := range snapshot.Nodes {
		switch node.ResourceType {
		case "Patient":
			fixture.PatientNodeID = node.ID
		case "Specimen":
			fixture.SpecimenNodeID = node.ID
		}
	}
	for _, edge := range snapshot.Edges {
		if edge.SourceResourceType == "Patient" && edge.TargetResourceType == "Specimen" && edge.Label == "subject_Patient" {
			fixture.PatientSpecimenEdge = edge.ID
		}
	}
	for _, candidate := range snapshot.Candidates {
		if candidate.ResourceType == "Specimen" && (candidate.FieldPath == "id" || candidate.FieldPath == "type.text") {
			fixture.CandidateIDs[candidate.FieldPath] = candidate.ID
		}
	}
	if fixture.PatientNodeID == "" || fixture.SpecimenNodeID == "" || fixture.PatientSpecimenEdge == "" ||
		fixture.CandidateIDs["id"] == "" || fixture.CandidateIDs["type.text"] == "" {
		t.Fatalf("connected Group candidate route/source fixture is incomplete: %#v", fixture)
	}
	compiledSource := lowerConstructionOutput(t, connectedGroupRootSourceOutput(), bindings)
	descriptor, err := lower.DescribeConstructionSourceStage(compiledSource.OutputSchema, compiledSource.RootResourceType, &compiledSource.Plan)
	if err != nil || descriptor.GroupFieldSourceContext == nil {
		t.Fatalf("describe connected Group root source shape: context=%#v err=%v", descriptor.GroupFieldSourceContext, err)
	}
	fixture.GroupFieldSourceContext = descriptor.GroupFieldSourceContext
	if fixture.GroupFieldSourceContext.SourceShapeID == "" {
		t.Fatalf("connected Group source context has no canonical shape ID: %#v", descriptor.GroupFieldSourceContext)
	}
	return fixture
}

func connectedGroupRootSourceOutput() recipe.Output {
	return recipe.Output{
		Name: "connected_group_root_source", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
	}
}

func connectedGroupOutputFromJSON(t *testing.T, source connectedGroupSourceFixture, codingEmptyPolicy string) recipe.Output {
	t.Helper()
	patientTypeSource := connectedGroupRelatedFieldSource(t, source, "SCALAR", source.CandidateIDs["type.text"], "type.text", "", "string")
	codingSource := connectedGroupRelatedFieldSource(t, source, "CODING_IDENTITY", "", "", codingEmptyPolicy, "Coding")
	rowValueSource := connectedGroupRelatedFieldSource(t, source, "SCALAR", source.CandidateIDs["id"], "id", "", "string")
	payload := map[string]any{
		"name": "connected_specimen_coding_group", "rootResourceType": "Patient", "rootOccurrenceId": "patient-root", "rowGrain": "patient",
		"fields": []any{map[string]any{"name": "patient_id", "columnId": "patient_id", "expr": map[string]any{"select": "root.id"}}},
		"construction": map[string]any{
			"version":       1,
			"sourceColumns": []any{map[string]any{"id": "patient_id", "name": "patient_id", "type": "string"}},
			"steps": []any{map[string]any{
				"id": "group_connected_specimen_coding", "inputs": []any{map[string]any{"kind": "SOURCE_PROJECTION"}},
				"operation": map[string]any{
					"kind": "GROUP",
					"group": map[string]any{
						"constructionId": "group_connected_specimen_coding", "missingKeyPolicy": "GROUP",
						"keys": []any{
							map[string]any{"inputColumnId": "patient_id", "outputColumnId": "patient_group_id"},
							map[string]any{"outputColumnId": "specimen_type_id", "fieldSource": patientTypeSource},
							map[string]any{"outputColumnId": "coding_id", "fieldSource": codingSource},
						},
						"aggregates": []any{map[string]any{"operation": "COUNT_ROWS", "outputColumnId": "occurrences_id"}},
					},
				},
				"rowValues": []any{map[string]any{
					"fieldSource": rowValueSource, "outputColumnId": "specimen_ids_id", "policy": "ALL",
				}},
				"outputs": []any{
					map[string]any{"id": "patient_group_id", "name": "patient", "type": "string"},
					map[string]any{"id": "specimen_type_id", "name": "specimen_type", "type": "string", "nullable": true},
					map[string]any{"id": "coding_id", "name": "coding", "type": "string", "nullable": true},
					map[string]any{"id": "occurrences_id", "name": "occurrences", "type": "integer"},
					map[string]any{"id": "specimen_ids_id", "name": "specimen_ids", "type": "array"},
				},
			}},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode connected Group recipe JSON: %v", err)
	}
	var output recipe.Output
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("decode connected Group recipe JSON at the actual recipe boundary: %v\n%s", err, raw)
	}
	return output
}

func connectedGroupRelatedFieldSource(
	t *testing.T,
	source connectedGroupSourceFixture,
	valueKind, candidateID, canonicalPath, codingEmptyPolicy, valueResourceType string,
) recipe.ConstructionGroupFieldSource {
	t.Helper()
	related := recipe.ConstructionGroupRelatedOccurrenceFieldSource{
		AnchorNodeID: source.PatientNodeID, AnchorResourceType: "Patient",
		Route: []recipe.ConstructionGroupRelatedRouteStep{{
			StepIndex: 0, FromNodeID: source.PatientNodeID, FromResourceType: "Patient",
			EdgeID: source.PatientSpecimenEdge, Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			ToNodeID: source.SpecimenNodeID, ToResourceType: "Specimen",
		}},
		TargetNodeID: source.SpecimenNodeID, TargetResourceType: "Specimen", ResourceType: valueResourceType,
		CandidateID: candidateID, CanonicalPath: canonicalPath,
		EmptyPolicy: recipe.ExpansionPreserveParent, Multiplicity: recipe.ConstructionGroupRelatedMultiplicityDistinctTargetPerInput,
	}
	if valueKind == "CODING_IDENTITY" {
		related.CodingSelector = "type.coding[]"
		related.CodingEmptyPolicy = recipe.ConstructionGroupCodingEmptyPolicy(codingEmptyPolicy)
		related.OrdinalSemantics = recipe.ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition
	}
	binding, err := lower.BindGroupRelatedOccurrenceSource(source.GroupFieldSourceContext, recipe.ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: valueKind,
		Input: &recipe.ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: &related},
	})
	if err != nil {
		t.Fatalf("bind compiler-issued related %s source: %v", valueKind, err)
	}
	return binding
}

func connectedSpecimenDocument(project, generation, id, patientID, typeText string, codings []any) map[string]any {
	key := project + "_specimen_" + id
	payload := map[string]any{
		"id": id, "resourceType": "Specimen", "status": "available",
		"subject": map[string]any{"reference": "Patient/" + patientID},
		"type":    map[string]any{"text": typeText, "coding": codings},
	}
	return map[string]any{
		"_key": key, "id": id, "project": project, "project_id": project,
		"dataset_generation": generation, "resourceType": "Specimen", "payload": payload,
	}
}

func connectedCoding(system, version, code, display string) map[string]any {
	return map[string]any{"system": system, "version": version, "code": code, "display": display}
}

func connectedCodingWithoutSystem(version, code, display string) map[string]any {
	return map[string]any{"version": version, "code": code, "display": display}
}

func executeConnectedGroupQuery(t *testing.T, ctx context.Context, client *store.Client, query CompiledQuery) []map[string]any {
	t.Helper()
	rows := make([]map[string]any, 0)
	if err := client.QueryRows(ctx, query.Query, 500, query.BindVars, func(row map[string]any) error {
		raw, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("snapshot connected Group result row: %w", err)
		}
		var copy map[string]any
		if err := json.Unmarshal(raw, &copy); err != nil {
			return fmt.Errorf("decode connected Group result row snapshot: %w", err)
		}
		rows = append(rows, copy)
		return nil
	}); err != nil {
		t.Fatalf("execute connected Group query: %v\n%s", err, query.Query)
	}
	return rows
}

func connectedGroupRow(t *testing.T, rows []map[string]any, patient string, specimenType any, tuple []any) map[string]any {
	t.Helper()
	var matches []map[string]any
	for _, row := range rows {
		if row["patient"] != patient || !reflect.DeepEqual(row["specimen_type"], specimenType) {
			continue
		}
		if reflect.DeepEqual(connectedCodingIdentityTuple(t, row), tuple) {
			matches = append(matches, row)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("connected Group match patient=%q type=%#v tuple=%#v = %#v, want exactly one row", patient, specimenType, tuple, matches)
	}
	return matches[0]
}

func assertConnectedGroupRow(t *testing.T, row map[string]any, patient string, specimenType any, tuple []any, count float64, display any, specimenIDs []string) {
	t.Helper()
	if row["patient"] != patient || !reflect.DeepEqual(row["specimen_type"], specimenType) ||
		!constructionNumericEqual(row["occurrences"], count) || !reflect.DeepEqual(row["coding"], display) {
		t.Fatalf("connected Group row = %#v, want patient=%q type=%#v tuple=%#v count=%v display=%#v", row, patient, specimenType, tuple, count, display)
	}
	assertConnectedSpecimenRowValues(t, row, specimenIDs)
	assertConnectedGroupIdentity(t, row, patient, specimenType, tuple)
}

func assertConnectedSpecimenRowValues(t *testing.T, row map[string]any, want []string) {
	t.Helper()
	values, ok := row["specimen_ids"].([]any)
	if !ok {
		if len(want) == 0 && row["specimen_ids"] == nil {
			return
		}
		t.Fatalf("connected Specimen RowValues = %#v, want array %v; row=%#v", row["specimen_ids"], want, row)
	}
	got := make([]string, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		id, ok := value.(string)
		if !ok {
			t.Fatalf("connected Specimen RowValue item = %#v, want string or absent-route null; row=%#v", value, row)
		}
		got = append(got, id)
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connected Specimen RowValues = %#v, want %#v; row=%#v", got, want, row)
	}
}

func connectedRowValueStrings(t *testing.T, row map[string]any) []string {
	t.Helper()
	values, ok := row["specimen_ids"].([]any)
	if !ok {
		if row["specimen_ids"] == nil {
			return nil
		}
		t.Fatalf("connected Specimen RowValues = %#v, want array or nil; row=%#v", row["specimen_ids"], row)
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		id, ok := value.(string)
		if !ok {
			t.Fatalf("connected Specimen RowValue item = %#v, want string or nil; row=%#v", value, row)
		}
		result = append(result, id)
	}
	return result
}

func connectedCodingIdentityTuple(t *testing.T, row map[string]any) []any {
	t.Helper()
	rowID, ok := row["__loom_row_id"].(string)
	if !ok || rowID == "" {
		t.Fatalf("connected Group row identity = %#v, want encoded exact Coding tuple", row["__loom_row_id"])
	}
	var parts []any
	if err := json.Unmarshal([]byte(rowID), &parts); err != nil {
		t.Fatalf("decode connected Group row identity %q: %v", rowID, err)
	}
	var tuple []any
	for _, raw := range parts {
		part, ok := raw.([]any)
		if !ok || len(part) < 3 || part[0] != "coding_id" || part[1] != "coding_identity" {
			continue
		}
		if tuple != nil {
			t.Fatalf("connected Group row identity repeats the Coding tuple part: %#v", parts)
		}
		tuple, ok = part[2].([]any)
		if !ok || len(tuple) != 3 {
			t.Fatalf("connected Coding identity tuple = %#v, want [system version code]", part[2])
		}
	}
	if tuple == nil {
		t.Fatalf("connected Group row identity has no exact Coding tuple part: %#v", parts)
	}
	return tuple
}

func assertConnectedGroupIdentity(t *testing.T, row map[string]any, patient string, specimenType any, tuple []any) {
	t.Helper()
	if got := connectedCodingIdentityTuple(t, row); !reflect.DeepEqual(got, tuple) {
		t.Fatalf("connected Coding identity tuple = %#v, want %#v; row=%#v", got, tuple, row)
	}
	rowID := row["__loom_row_id"].(string)
	var parts []any
	if err := json.Unmarshal([]byte(rowID), &parts); err != nil {
		t.Fatalf("decode connected Group identity %q: %v", rowID, err)
	}
	if len(parts) < 5 || !reflect.DeepEqual(parts[0], []any{"construction", "group_connected_specimen_coding"}) ||
		!reflect.DeepEqual(parts[1], []any{"operation", "GROUP"}) ||
		!reflect.DeepEqual(parts[2], []any{"patient_group_id", patient}) ||
		!reflect.DeepEqual(parts[3], []any{"specimen_type_id", specimenType}) {
		t.Fatalf("connected Group row identity = %#v, want exact Patient and Specimen type key preimage; row=%#v", parts, row)
	}
}

func assertConnectedGroupLineage(
	t *testing.T,
	ctx context.Context,
	client *store.Client,
	compiled lower.CompiledRecipeOutput,
	row map[string]any,
	project, patientKey string,
	wantOrdinals map[string]map[int]bool,
	emptyCodingOwner string,
	missingRelatedRoute bool,
) {
	t.Helper()
	rowID, _ := row["__loom_row_id"].(string)
	query, err := CompileRowLineageOutput(compiled, rowID, 0, 20, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile connected Group lineage for row %#v: %v", row, err)
	}
	lineage := executeRowLineageOracleQuery(t, ctx, client, query)
	contributors, _ := lineage["contributors"].([]any)
	wantCount := 0
	for _, ordinals := range wantOrdinals {
		wantCount += len(ordinals)
	}
	if emptyCodingOwner != "" || missingRelatedRoute {
		wantCount++
	}
	if lineage["found"] != true || lineage["hasMore"] != false || len(contributors) != wantCount {
		t.Fatalf("connected Group lineage = %#v, want %d exact related Specimen/Coding preimages for row %#v", lineage, wantCount, row)
	}
	remaining := make(map[string]map[int]bool, len(wantOrdinals))
	for owner, ordinals := range wantOrdinals {
		remaining[owner] = make(map[int]bool, len(ordinals))
		for ordinal := range ordinals {
			remaining[owner][ordinal] = true
		}
	}
	seen := make(map[string]bool, len(contributors))
	for _, raw := range contributors {
		contributor, _ := raw.(map[string]any)
		if contributor["resourceType"] != "Patient" || contributor["resourceId"] != row["patient"] {
			t.Errorf("connected Group lineage root contributor = %#v, want Patient/%v", contributor, row["patient"])
			continue
		}
		occurrence := connectedOccurrenceKey(t, contributor["occurrenceKey"])
		if len(occurrence) != 4 || occurrence["root_id"] != patientKey {
			t.Errorf("connected Group occurrence identity = %#v, want only exact root_id, terminal_id, item_present, ordinal fields for Patient key %q", occurrence, patientKey)
		}
		terminalID := occurrence["terminal_id"]
		ownerID := ""
		if missingRelatedRoute && terminalID == nil {
			if occurrence["item_present"] != false || occurrence["ordinal"] != nil {
				t.Errorf("absent-route Group occurrence = %#v, want null terminal, item_present=false, and null ordinal", occurrence)
			}
			missingRelatedRoute = false
			keyBytes, _ := json.Marshal(occurrence)
			key := string(keyBytes)
			if seen[key] {
				t.Errorf("absent-route Group preimage identity repeated: %s", key)
			}
			seen[key] = true
			continue
		}
		for candidateID, ordinals := range remaining {
			if terminalID == "Specimen/"+project+"_specimen_"+candidateID && len(ordinals) != 0 {
				ownerID = candidateID
				break
			}
		}
		if ownerID == "" && emptyCodingOwner != "" && terminalID == "Specimen/"+project+"_specimen_"+emptyCodingOwner {
			if occurrence["item_present"] != false || occurrence["ordinal"] != nil {
				t.Errorf("empty Coding occurrence = %#v, want exact terminal Specimen ID without item or ordinal", occurrence)
			}
			ownerID = emptyCodingOwner
			emptyCodingOwner = ""
		} else if ownerID != "" {
			ordinalValue, ok := occurrence["ordinal"].(float64)
			ordinal := int(ordinalValue)
			if occurrence["item_present"] != true || !ok || !remaining[ownerID][ordinal] {
				t.Errorf("connected Coding occurrence for %s = %#v, want an exact source ordinal", ownerID, occurrence)
			} else {
				delete(remaining[ownerID], ordinal)
			}
		} else {
			t.Errorf("connected Group lineage terminal_id = %#v, want exact contributing Specimen owner", terminalID)
			continue
		}
		keyBytes, _ := json.Marshal(occurrence)
		key := string(keyBytes)
		if seen[key] {
			t.Errorf("connected Group lineage repeated owner/ordinal identity %s", key)
		}
		seen[key] = true
	}
	for owner, ordinals := range remaining {
		if len(ordinals) != 0 {
			t.Errorf("connected Group lineage omitted exact owner/ordinals for %s: %#v", owner, ordinals)
		}
	}
	if emptyCodingOwner != "" {
		t.Errorf("connected Group lineage omitted preserved empty-Coding owner %q", emptyCodingOwner)
	}
	if missingRelatedRoute {
		t.Errorf("connected Group lineage omitted the absent related route preimage")
	}
}

func connectedOccurrenceKey(t *testing.T, raw any) map[string]any {
	t.Helper()
	if key, ok := raw.(map[string]any); ok {
		return key
	}
	if encoded, ok := raw.(string); ok {
		var key map[string]any
		if err := json.Unmarshal([]byte(encoded), &key); err == nil {
			return key
		}
	}
	t.Fatalf("connected Group occurrence key = %#v, want exact compact root/terminal/item/ordinal identity", raw)
	return nil
}
