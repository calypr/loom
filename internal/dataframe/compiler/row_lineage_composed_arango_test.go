package compiler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

// This fixture independently enumerates membership roots and each authored
// relationship stage in raw AQL, then asks CompileRowLineageOutput to resolve
// those canonical row IDs. It emits the same Specimen at authored stages one
// and three: repeated paths collapse within each stage, while both authored
// occurrences remain distinct contributors.
func TestComposedRelatedExpandLineageMatchesIndependentMembershipOracleAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Specimen"}, {Name: "Observation"},
		{Name: "fhir_edge", Edge: true}, {Name: "loom_explorer_selection_members"},
	}}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_composed_lineage_"+strings.ReplaceAll(uuid.NewString(), "-", ""), "generation-composed-lineage"
	selectionID := "selection_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Patient", "Specimen", "Observation", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove composed row-lineage fixtures from %s: %v", collection, err)
			}
		}
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR member IN loom_explorer_selection_members FILTER member.selectionId == @selection_id REMOVE member IN loom_explorer_selection_members",
			map[string]any{"selection_id": selectionID}); err != nil {
			t.Errorf("remove composed row-lineage membership fixtures: %v", err)
		}
	}()

	key := func(id string) string { return project + "_" + id }
	insertResource := func(collection, id, resourceType, authPath, docProject, docGeneration string, active *bool) {
		t.Helper()
		payload := map[string]any{"id": id, "resourceType": resourceType}
		if active != nil {
			payload["active"] = *active
		}
		doc := map[string]any{
			"_key": key(id), "id": id, "project": docProject, "project_id": docProject,
			"dataset_generation": docGeneration, "resourceType": resourceType,
			"auth_resource_path": authPath, "payload": payload,
		}
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, collection, []json.RawMessage{encoded}, false, "document"); err != nil {
			t.Fatalf("insert %s fixture %s: %v", collection, id, err)
		}
	}
	insertEdge := func(id, fromType, fromID, toType, toID, label, edgeProject, edgeGeneration, authPath string) {
		t.Helper()
		doc, err := json.Marshal(map[string]any{
			"_key": key(id), "_from": fromType + "/" + key(fromID), "_to": toType + "/" + key(toID),
			"project": edgeProject, "project_id": edgeProject, "dataset_generation": edgeGeneration,
			"auth_resource_path": authPath, "label": label, "from_type": fromType, "to_type": toType,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, "fhir_edge", []json.RawMessage{doc}, false, "document"); err != nil {
			t.Fatalf("insert composed route edge %s: %v", id, err)
		}
	}
	active := true
	for _, id := range []string{"p1", "p2", "p3", "p_denied", "p_bridge"} {
		auth := "/allowed"
		if id == "p_denied" {
			auth = "/denied"
		}
		insertResource("Patient", id, "Patient", auth, project, generation, &active)
	}
	for _, id := range []string{"s1", "s_empty", "s2", "s3", "s_stale"} {
		insertResource("Specimen", id, "Specimen", "/allowed", project, generation, nil)
	}
	for _, id := range []string{"o1", "o2", "o3"} {
		insertResource("Observation", id, "Observation", "/allowed", project, generation, nil)
	}

	// Membership contains two allowed roots plus one denied root. p3 has a
	// complete valid route but is not a member; p_bridge is an allowed route
	// bridge but is neither selected nor authored as a terminal source record.
	members := make([]json.RawMessage, 0, 3)
	for _, id := range []string{"p1", "p2", "p_denied"} {
		doc, err := json.Marshal(map[string]any{
			"_key": selectionID + "_" + id, "selectionId": selectionID,
			"project": project, "generation": generation, "resourceType": "Patient", "id": id,
		})
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, doc)
	}
	if err := client.InsertBatchRaw(ctx, "loom_explorer_selection_members", members, false, "document"); err != nil {
		t.Fatalf("insert selected-root membership: %v", err)
	}

	// Authored stage one uses the generated Patient|subject_Patient|Specimen
	// tuple. Duplicate paths to s1 collapse within this stage. The old-generation
	// edge is the only route to s_stale and cannot admit that target.
	insertEdge("p1_s1_a", "Specimen", "s1", "Patient", "p1", "subject_Patient", project, generation, "/allowed")
	insertEdge("p1_s1_b", "Specimen", "s1", "Patient", "p1", "subject_Patient", project, generation, "/allowed")
	insertEdge("p1_s_empty", "Specimen", "s_empty", "Patient", "p1", "subject_Patient", project, generation, "/allowed")
	insertEdge("p2_s2", "Specimen", "s2", "Patient", "p2", "subject_Patient", project, generation, "/allowed")
	insertEdge("p3_s3", "Specimen", "s3", "Patient", "p3", "subject_Patient", project, generation, "/allowed")
	insertEdge("p1_s_stale", "Specimen", "s_stale", "Patient", "p1", "subject_Patient", project, "old-generation", "/allowed")

	// Authored stage two is the generated two-hop route Specimen -> Patient ->
	// Observation. p_bridge is a bridge-only resource; two paths to o1 should
	// still yield one authored terminal. The selected p2 row reaches an
	// unauthorized bridge with a valid, visible Observation and Specimen beyond
	// it; the bridge auth check is what must preserve the Specimen parent as empty.
	insertEdge("s1_bridge_a", "Specimen", "s1", "Patient", "p_bridge", "subject_Patient", project, generation, "/allowed")
	insertEdge("s1_bridge_b", "Specimen", "s1", "Patient", "p_bridge", "subject_Patient", project, generation, "/allowed")
	insertEdge("o1_bridge_a", "Observation", "o1", "Patient", "p_bridge", "subject_Patient", project, generation, "/allowed")
	insertEdge("o1_bridge_b", "Observation", "o1", "Patient", "p_bridge", "subject_Patient", project, generation, "/allowed")
	insertEdge("s2_denied_bridge", "Specimen", "s2", "Patient", "p_denied", "subject_Patient", project, generation, "/allowed")
	insertEdge("o2_denied_bridge", "Observation", "o2", "Patient", "p_denied", "subject_Patient", project, generation, "/allowed")
	insertEdge("o3_decoy_bridge", "Observation", "o3", "Patient", "p3", "subject_Patient", project, generation, "/allowed")
	insertEdge("s3_decoy_bridge", "Specimen", "s3", "Patient", "p3", "subject_Patient", project, generation, "/allowed")

	// Authored stage three returns to s1 using the supported Observation to
	// Specimen reference. Duplicate edge witnesses again collapse within the
	// stage, while s1 remains present a second time after stage one.
	insertEdge("o1_s1_a", "Observation", "o1", "Specimen", "s1", "specimen_Specimen", project, generation, "/allowed")
	insertEdge("o1_s1_b", "Observation", "o1", "Specimen", "s1", "specimen_Specimen", project, generation, "/allowed")
	insertEdge("o2_s2", "Observation", "o2", "Specimen", "s2", "specimen_Specimen", project, generation, "/allowed")
	insertEdge("o3_s3", "Observation", "o3", "Specimen", "s3", "specimen_Specimen", project, generation, "/allowed")

	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		SelectionMembersCollection: "loom_explorer_selection_members",
		AuthScopeMode:              authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
	}
	output := lowerConstructionOutput(t, composedRelatedExpandLineageOutput(selectionID), bindings)
	if capability := RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("composed related expansion row lineage capability = %#v", capability)
	}
	previewQuery, err := CompileRecipeOutputWithPolicy(output, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	previewRows := executeReshapeOracleQuery(t, ctx, client, previewQuery)
	oracleRows := executeComposedRelatedExpandOracle(t, ctx, client, project, generation, selectionID)
	if len(previewRows) != len(oracleRows) {
		t.Fatalf("preview produced %d rows, independent membership/route oracle produced %d: preview=%#v oracle=%#v", len(previewRows), len(oracleRows), previewRows, oracleRows)
	}
	stepIDs := composedRelatedExpandConstructionIDs(t, output)
	oracleByRowID := make(map[string]map[string]any, len(oracleRows))
	for _, oracle := range oracleRows {
		rowID := composedOracleRowID(t, oracle, stepIDs)
		if _, duplicate := oracleByRowID[rowID]; duplicate {
			t.Fatalf("independent oracle yielded duplicate authored row identity %s: %#v", rowID, oracle)
		}
		oracleByRowID[rowID] = oracle
	}
	previewByID := make(map[string]map[string]any, len(previewRows))
	for _, row := range previewRows {
		rowID, _ := row["__loom_row_id"].(string)
		if rowID == "" {
			t.Fatalf("composed preview row omitted canonical identity: %#v", row)
		}
		if _, exists := oracleByRowID[rowID]; !exists {
			t.Fatalf("preview row has no independent selected-route witness: %s (%#v)", rowID, row)
		}
		previewByID[rowID] = row
	}
	if len(previewByID) != len(oracleByRowID) {
		t.Fatalf("preview identity set size %d differs from independent oracle %d", len(previewByID), len(oracleByRowID))
	}
	for rowID, oracle := range oracleByRowID {
		if _, ok := previewByID[rowID]; !ok {
			t.Errorf("independent selected-route row missing from preview: %s (%#v)", rowID, oracle)
		}
	}
	if len(oracleRows) != 3 {
		t.Fatalf("independent oracle rows = %#v, want p1/s1, p1/s_empty, p2/s2, excluding denied and nonmember roots", oracleRows)
	}

	// The full row includes Patient/p1, Specimen/s1, Observation/o1, and the
	// same Specimen/s1 emitted again. limit=2 checks stage order, limit+1
	// hasMore, continuation, and past-end behavior.
	var fullRowID string
	for rowID, oracle := range oracleByRowID {
		if oracle["root_id"] == "p1" && oracle["specimen_id"] == "s1" && oracle["observation_id"] == "o1" && oracle["returned_specimen_id"] == "s1" {
			fullRowID = rowID
		}
	}
	if fullRowID == "" {
		t.Fatal("independent oracle omitted fully composed p1/s1/o1/s1 row")
	}
	page := func(offset int) map[string]any {
		t.Helper()
		compiled, compileErr := CompileRowLineageOutput(output, fullRowID, offset, 2, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile composed row lineage at offset %d: %v", offset, compileErr)
		}
		return executeRowLineageOracleQuery(t, ctx, client, compiled)
	}
	firstPage, secondPage, pastEnd := page(0), page(2), page(4)
	if firstPage["found"] != true || firstPage["hasMore"] != true || secondPage["found"] != true || secondPage["hasMore"] != false || pastEnd["found"] != true || pastEnd["hasMore"] != false {
		t.Fatalf("composed lineage page flags: first=%#v second=%#v past-end=%#v", firstPage, secondPage, pastEnd)
	}
	firstContributors := lineageContributorRows(t, firstPage)
	secondContributors := lineageContributorRows(t, secondPage)
	pastContributors := lineageContributorRows(t, pastEnd)
	if len(firstContributors) != 2 || len(secondContributors) != 2 || len(pastContributors) != 0 {
		t.Fatalf("composed lineage page lengths first=%d second=%d past-end=%d", len(firstContributors), len(secondContributors), len(pastContributors))
	}
	wantPages := [][3]string{
		{"Patient", "p1", key("p1")}, {"Specimen", "s1", key("s1")},
		{"Observation", "o1", key("o1")}, {"Specimen", "s1", key("s1")},
	}
	gotPages := append(firstContributors, secondContributors...)
	for index, contributor := range gotPages {
		if contributor["resourceType"] != wantPages[index][0] || contributor["resourceId"] != wantPages[index][1] || contributor["occurrenceKey"] != wantPages[index][2] {
			t.Errorf("ordered composed contributor %d = %#v, want (%s,%s,%s)", index, contributor, wantPages[index][0], wantPages[index][1], wantPages[index][2])
		}
	}

	// A later preserved empty emits no null/bridge contributor. This exercises
	// both no matching route and a route whose only bridge is denied by scope.
	for rowID, oracle := range oracleByRowID {
		if (oracle["root_id"] != "p1" || oracle["specimen_id"] != "s_empty") &&
			(oracle["root_id"] != "p2" || oracle["specimen_id"] != "s2") {
			continue
		}
		if oracle["observation_id"] != nil || oracle["returned_specimen_id"] != nil {
			t.Fatalf("empty oracle row unexpectedly has related terminals: %#v", oracle)
		}
		compiled, compileErr := CompileRowLineageOutput(output, rowID, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile preserved-empty composed lineage: %v", compileErr)
		}
		result := executeRowLineageOracleQuery(t, ctx, client, compiled)
		contributors := lineageContributorRows(t, result)
		if result["found"] != true || result["hasMore"] != false || len(contributors) != 2 ||
			contributors[0]["resourceType"] != "Patient" || contributors[0]["resourceId"] != oracle["root_id"] ||
			contributors[1]["resourceType"] != "Specimen" || contributors[1]["resourceId"] != oracle["specimen_id"] {
			t.Errorf("preserved-empty row lineage = %#v, want only selected root and authored stage-one Specimen", result)
		}
	}

	// Validly shaped identities for a nonmember root, a denied bridge terminal,
	// a forged stage-two terminal, and malformed JSON all fail closed.
	stage1ID, stage2ID, stage3ID := stepIDs["expand_specimens"], stepIDs["expand_observations"], stepIDs["return_specimen"]
	s2Parent := relatedExpandRowID(t, key("p2"), stage1ID, "Specimen/"+key("s2"))
	forgedRows := []string{
		relatedExpandRowID(t, relatedExpandRowID(t, key("p3"), stage1ID, "Specimen/"+key("s3")), stage2ID, "Observation/"+key("o3")),
		relatedExpandRowID(t, relatedExpandRowID(t, s2Parent, stage2ID, "Observation/"+key("o2")), stage3ID, "Specimen/"+key("s2")),
		relatedExpandRowID(t, relatedExpandRowID(t, relatedExpandRowID(t, key("p1"), stage1ID, "Specimen/"+key("s1")), stage2ID, "Observation/"+key("forged")), stage3ID, "Specimen/"+key("s1")),
		"not-json",
	}
	for _, forged := range forgedRows {
		compiled, compileErr := CompileRowLineageOutput(output, forged, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile fail-closed composed identity %q: %v", forged, compileErr)
		}
		result := executeRowLineageOracleQuery(t, ctx, client, compiled)
		if result["found"] != false || len(lineageContributorRows(t, result)) != 0 {
			t.Errorf("forged/nonmember/unauthorized composed identity disclosed contributors: row=%q result=%#v", forged, result)
		}
	}
}

func composedRelatedExpandLineageOutput(selectionID string) recipe.Output {
	trueValue := true
	sourceColumns := []recipe.StageColumn{{ID: "patient-id", Name: "patient_id"}, {ID: "active-id", Name: "active", Type: "boolean"}}
	activeColumns := append([]recipe.StageColumn(nil), sourceColumns...)
	specimenColumns := append(append([]recipe.StageColumn(nil), sourceColumns...), recipe.StageColumn{ID: "specimen-id", Name: "specimen_id", Type: "string"})
	observationColumns := append(append([]recipe.StageColumn(nil), specimenColumns...), recipe.StageColumn{ID: "observation-id", Name: "observation_id", Type: "string", Nullable: true})
	finalColumns := append(append([]recipe.StageColumn(nil), observationColumns...), recipe.StageColumn{ID: "returned-specimen-id", Name: "returned_specimen_id", Type: "string", Nullable: true})
	optional := "OPTIONAL"
	return recipe.Output{
		Name: "composed_related_lineage", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "patient_id", ColumnID: "patient-id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "active", ColumnID: "active-id", Expr: recipe.Expression{Select: "root.active"}},
		},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: selectionID, MembershipDigest: "sha256:composed-lineage-fixture-membership",
			MemberCount: 3, ResourceType: "Patient",
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: sourceColumns, Steps: []recipe.ConstructionStep{
			{
				ID: "filter_active", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "active-id", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterBoolean, Boolean: &trueValue}},
				}}, Outputs: activeColumns,
			},
			{
				ID: "expand_specimens", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter_active"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: "_key", ChoiceID: "patient-specimen", TargetNodeID: "specimen-node", TargetResourceType: "Specimen",
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "patient-specimen-edge", FromNodeID: "patient-node", ToNodeID: "specimen-node",
						FromResourceType: "Patient", ToResourceType: "Specimen", Relationship: "subject_Patient",
						StorageDirection: "INBOUND", MatchMode: optional,
					}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionExclude, RelatedRecordColumnID: "specimen-id",
				}}, Outputs: specimenColumns,
			},
			{
				ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_specimens"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: composedRelatedTerminalIdentityColumn("expand_specimens"), ChoiceID: "specimen-patient-observation",
					TargetNodeID: "observation-node", TargetResourceType: "Observation",
					Route: []recipe.ConstructionRelatedRouteStep{
						{EdgeID: "specimen-patient", FromNodeID: "specimen-node", ToNodeID: "bridge-patient-node", FromResourceType: "Specimen", ToResourceType: "Patient", Relationship: "subject_Patient", StorageDirection: "OUTBOUND", MatchMode: optional},
						{EdgeID: "patient-observation", FromNodeID: "bridge-patient-node", ToNodeID: "observation-node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: optional},
					}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "observation-id",
				}}, Outputs: observationColumns,
			},
			{
				ID: "return_specimen", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: composedRelatedTerminalIdentityColumn("expand_observations"), ChoiceID: "observation-specimen",
					TargetNodeID: "returned-specimen-node", TargetResourceType: "Specimen",
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "observation-specimen-edge", FromNodeID: "observation-node", ToNodeID: "returned-specimen-node",
						FromResourceType: "Observation", ToResourceType: "Specimen", Relationship: "specimen_Specimen",
						StorageDirection: "OUTBOUND", MatchMode: optional,
					}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "returned-specimen-id",
				}}, Outputs: finalColumns,
			},
		}},
	}
}

func composedRelatedTerminalIdentityColumn(stageID string) string {
	digest := sha256.Sum256([]byte(stageID))
	return "__loom_related_terminal_id_" + hex.EncodeToString(digest[:])
}

func composedRelatedExpandConstructionIDs(t *testing.T, output lower.CompiledRecipeOutput) map[string]string {
	t.Helper()
	ids := make(map[string]string, 3)
	for _, stage := range output.Plan.StageSequence.Stages {
		if stage.RelatedExpand == nil {
			continue
		}
		id, ok := output.Plan.BindVars[stage.RelatedExpand.ConstructionIDBindKey].(string)
		if !ok || id == "" {
			t.Fatalf("related expansion stage %s has no bound construction ID", stage.ID)
		}
		ids[stage.ID] = id
	}
	if len(ids) != 3 {
		t.Fatalf("lowered related expansion construction IDs = %#v, want all three authored stages", ids)
	}
	return ids
}

func executeComposedRelatedExpandOracle(t *testing.T, ctx context.Context, client *store.Client, project, generation, selectionID string) []map[string]any {
	t.Helper()
	// This is a source-side oracle, independent from construction compilation:
	// it spells out membership, every typed route hop, every edge label, and all
	// project/generation/auth guards before deduplicating per authored stage.
	query := `
FOR member IN loom_explorer_selection_members
  FILTER member.selectionId == @selection_id AND member.project == @project AND member.generation == @generation AND member.resourceType == "Patient"
  FOR root IN Patient
    FILTER root.id == member.id AND root.project == @project AND root.dataset_generation == @generation
      AND root.resourceType == "Patient" AND root.auth_resource_path IN @auth_resource_paths AND root.payload.active == true
    LET specimens = (
      FOR specimen IN Specimen
        FILTER specimen.project == @project AND specimen.dataset_generation == @generation AND specimen.resourceType == "Specimen"
          AND specimen.auth_resource_path IN @auth_resource_paths
        LET root_witnesses = (
          FOR edge IN fhir_edge
            FILTER edge._from == CONCAT("Specimen/", specimen._key) AND edge._to == CONCAT("Patient/", root._key)
              AND edge.label == "subject_Patient" AND edge.project == @project AND edge.dataset_generation == @generation
              AND edge.auth_resource_path IN @auth_resource_paths
            RETURN edge._key
        )
        FILTER LENGTH(root_witnesses) > 0
        RETURN DISTINCT {key: specimen._key, id: specimen.id}
    )
    FOR specimen IN specimens
      LET observations = (
        FOR bridge IN Patient
          FILTER bridge.project == @project AND bridge.dataset_generation == @generation AND bridge.resourceType == "Patient"
            AND bridge.auth_resource_path IN @auth_resource_paths
          LET bridge_witnesses = (
            FOR edge IN fhir_edge
              FILTER edge._from == CONCAT("Specimen/", specimen.key) AND edge._to == CONCAT("Patient/", bridge._key)
                AND edge.label == "subject_Patient" AND edge.project == @project AND edge.dataset_generation == @generation
                AND edge.auth_resource_path IN @auth_resource_paths
              RETURN edge._key
          )
          FILTER LENGTH(bridge_witnesses) > 0
          FOR observation IN Observation
            FILTER observation.project == @project AND observation.dataset_generation == @generation AND observation.resourceType == "Observation"
              AND observation.auth_resource_path IN @auth_resource_paths
            LET terminal_witnesses = (
              FOR edge IN fhir_edge
                FILTER edge._from == CONCAT("Observation/", observation._key) AND edge._to == CONCAT("Patient/", bridge._key)
                  AND edge.label == "subject_Patient" AND edge.project == @project AND edge.dataset_generation == @generation
                  AND edge.auth_resource_path IN @auth_resource_paths
                RETURN edge._key
            )
            FILTER LENGTH(terminal_witnesses) > 0
            RETURN DISTINCT {key: observation._key, id: observation.id}
      )
      LET observation_rows = LENGTH(observations) == 0 ? [null] : observations
      FOR observation IN observation_rows
        LET returned_specimens = observation == null ? [] : (
          FOR target IN Specimen
            FILTER target.project == @project AND target.dataset_generation == @generation AND target.resourceType == "Specimen"
              AND target.auth_resource_path IN @auth_resource_paths
            LET terminal_witnesses = (
              FOR edge IN fhir_edge
                FILTER edge._from == CONCAT("Observation/", observation.key) AND edge._to == CONCAT("Specimen/", target._key)
                  AND edge.label == "specimen_Specimen" AND edge.project == @project AND edge.dataset_generation == @generation
                  AND edge.auth_resource_path IN @auth_resource_paths
                RETURN edge._key
            )
            FILTER LENGTH(terminal_witnesses) > 0
            RETURN DISTINCT {key: target._key, id: target.id}
        )
        LET returned_rows = LENGTH(returned_specimens) == 0 ? [null] : returned_specimens
        FOR returned_specimen IN returned_rows
          RETURN {
            root_key: root._key, root_id: root.id,
            specimen_key: specimen.key, specimen_id: specimen.id,
            observation_key: observation == null ? null : observation.key,
            observation_id: observation == null ? null : observation.id,
            returned_specimen_key: returned_specimen == null ? null : returned_specimen.key,
            returned_specimen_id: returned_specimen == null ? null : returned_specimen.id
          }
`
	rows := make([]map[string]any, 0)
	binds := map[string]any{
		"selection_id": selectionID, "project": project, "generation": generation,
		"auth_resource_paths": []string{"/allowed"},
	}
	if err := client.QueryRows(ctx, query, 128, binds, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute independent selected-root relationship oracle: %v\n%s", err, query)
	}
	return rows
}

func composedOracleRowID(t *testing.T, row map[string]any, constructionIDs map[string]string) string {
	t.Helper()
	rootKey, _ := row["root_key"].(string)
	specimenKey, _ := row["specimen_key"].(string)
	if rootKey == "" || specimenKey == "" {
		t.Fatalf("independent oracle row has no root or stage-one key: %#v", row)
	}
	id := relatedExpandRowID(t, rootKey, constructionIDs["expand_specimens"], "Specimen/"+specimenKey)
	observationKey, hasObservation := row["observation_key"].(string)
	if !hasObservation || observationKey == "" {
		id = relatedExpandEmptyRowID(t, id, constructionIDs["expand_observations"])
	} else {
		id = relatedExpandRowID(t, id, constructionIDs["expand_observations"], "Observation/"+observationKey)
	}
	returnedKey, hasReturned := row["returned_specimen_key"].(string)
	if !hasReturned || returnedKey == "" {
		return relatedExpandEmptyRowID(t, id, constructionIDs["return_specimen"])
	}
	return relatedExpandRowID(t, id, constructionIDs["return_specimen"], "Specimen/"+returnedKey)
}

func lineageContributorRows(t *testing.T, result map[string]any) []map[string]any {
	t.Helper()
	values, ok := result["contributors"].([]any)
	if !ok {
		t.Fatalf("row-lineage response contributors are %T, want array: %#v", result["contributors"], result)
	}
	contributors := make([]map[string]any, 0, len(values))
	for _, value := range values {
		contributor, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("row-lineage contributor is %T, want object: %#v", value, value)
		}
		contributors = append(contributors, contributor)
	}
	return contributors
}
