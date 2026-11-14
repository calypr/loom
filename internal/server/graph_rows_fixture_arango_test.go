package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

// Expected values are read from the independently authored literal fixture,
// never from a preceding query response. This proves compiler/AQL execution,
// not native browser interaction, draft persistence or authentication.
func TestGraphRowsIndependentFixtureAgainstArango(t *testing.T) {
	graphRowsIndependentFixtureAgainstArango(t, "fixture-v1", false, false, "original")
}

func TestGraphRowsDefaultRelatedInventoryAgainstArango(t *testing.T) {
	for _, mode := range []string{"known", "drift", "type", "related-source", "related-source-drift", "related-source-type"} {
		t.Run(mode, func(t *testing.T) {
			graphRowsIndependentFixtureAgainstArango(t, "fixture-v1", false, false, "default-"+mode)
		})
	}
}

func TestGraphRowsLogicalContributorOrderAgainstArango(t *testing.T) {
	for _, values := range []string{"original", "swapped", "duplicate"} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("values-%s-reversed-insertion-%v", values, reverse), func(t *testing.T) {
				graphRowsIndependentFixtureAgainstArango(t, "fixture-v2", true, reverse, values)
			})
		}
	}
}

func graphRowsIndependentFixtureAgainstArango(t *testing.T, generation string, qualified, reverse bool, values string) {
	vertexKey := func(collection, id string) string {
		if !qualified {
			return id
		}
		return fmt.Sprintf("g_%x", sha256.Sum256([]byte("vertex\x00graph-fixture\x00"+generation+"\x00"+collection+"\x00"+id+"\x00")))
	}
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to the owned disposable Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database := "loom_test_graph_rows_" + uuid.NewString()
	client, err := storearango.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
		if err != nil {
			t.Error(err)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("remove owned graph fixture database status=%d", response.StatusCode)
		}
	})
	if err := client.Bootstrap(ctx, storearango.BootstrapSpec{Collections: []storearango.CollectionSpec{{Name: "ResearchSubject"}, {Name: "Patient"}, {Name: "Observation"}, {Name: "Condition"}, {Name: "fhir_edge", Edge: true}}}); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join("..", "..", "testdata", "graph-rows")
	insert := func(collection string, documents []json.RawMessage) {
		t.Helper()
		if reverse {
			for i, j := 0, len(documents)-1; i < j; i, j = i+1, j-1 {
				documents[i], documents[j] = documents[j], documents[i]
			}
		}
		if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
			t.Fatal(err)
		}
	}
	var edges []json.RawMessage
	for _, collection := range []string{"ResearchSubject", "Patient", "Observation", "Condition"} {
		file, err := os.Open(filepath.Join(fixture, collection+".ndjson"))
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		var documents []json.RawMessage
		for scanner.Scan() {
			var payload map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			id := payload["id"].(string)
			if collection == "ResearchSubject" && (values == "default-drift" || values == "default-related-source-drift") {
				identifiers, _ := payload["identifier"].([]any)
				payload["identifier"] = append(identifiers, map[string]any{"system": "urn:unexpected", "value": "unexpected"})
			}
			if collection == "ResearchSubject" && (values == "default-type" || values == "default-related-source-type") {
				payload["identifier"] = []any{map[string]any{"system": "urn:graph-rows", "value": 123}}
			}
			if qualified && collection == "ResearchSubject" {
				payload["meta"] = map[string]any{"lastUpdated": "2022-01-01T00:00:00Z"}
				payload["extension"] = []any{map[string]any{"valueInteger": 9}, map[string]any{"valueInteger": 1}, map[string]any{"valueInteger": 9}}
			}
			if qualified && collection == "Observation" {
				if values == "swapped" && id == "obs-many-A" {
					payload["valueInteger"] = 13
				}
				if values == "swapped" && id == "obs-many-B" {
					payload["valueInteger"] = 11
				}
				if values == "duplicate" && id == "obs-many-B" {
					payload["valueInteger"] = 11
				}
				payload["extension"] = []any{map[string]any{"url": "score", "valueInteger": payload["valueInteger"]}}
				payload["effectiveDateTime"] = "2020-01-01T00:00:00Z"
				if id == "obs-many-B" {
					payload["effectiveDateTime"] = "2021-01-01T00:00:00Z"
				}
			}
			resource := map[string]any{"_key": vertexKey(collection, id), "id": id, "project": "graph-fixture", "project_id": "graph-fixture", "dataset_generation": generation, "resourceType": collection, "payload": payload}
			if qualified {
				resource["logical_key"] = id
			}
			raw, err := json.Marshal(resource)
			if err != nil {
				t.Fatal(err)
			}
			documents = append(documents, raw)
			addEdge := func(reference, label string) {
				t.Helper()
				target := strings.Split(reference, "/")
				if len(target) != 2 {
					t.Fatal("invalid fixture reference")
				}
				encoded, err := json.Marshal(map[string]any{"_key": fmt.Sprintf("edge_%d", len(edges)), "_from": collection + "/" + vertexKey(collection, id), "_to": target[0] + "/" + vertexKey(target[0], target[1]), "label": label, "from_type": collection, "to_type": target[0], "project": "graph-fixture", "project_id": "graph-fixture", "dataset_generation": generation})
				if err != nil {
					t.Fatal(err)
				}
				edges = append(edges, encoded)
				if qualified && collection == "Observation" && id == "obs-many-A" && label == "subject_Patient" {
					var duplicate map[string]any
					if err := json.Unmarshal(encoded, &duplicate); err != nil {
						t.Fatal(err)
					}
					duplicate["_key"] = fmt.Sprint(duplicate["_key"]) + "_duplicate"
					raw, err := json.Marshal(duplicate)
					if err != nil {
						t.Fatal(err)
					}
					edges = append(edges, raw)
				}
			}
			if subject, ok := payload["subject"].(map[string]any); ok {
				addEdge(subject["reference"].(string), "subject_Patient")
			}
			if performers, ok := payload["performer"].([]any); ok {
				for _, item := range performers {
					addEdge(item.(map[string]any)["reference"].(string), "performer_Patient")
				}
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		insert(collection, documents)
	}
	insert("fhir_edge", edges)
	if !qualified && !strings.HasPrefix(values, "default-") {
		t.Run("group-dynamic-source-runtime-validation", func(t *testing.T) { graphRowsCheckGroupedDynamicInventories(t, ctx, client) })
	}
	var oracle struct {
		Preserving []struct {
			Identity          []string `json:"identity"`
			PatientID         string   `json:"patientId"`
			ObservationIDs    []string `json:"observationIds"`
			ObservationValues []int    `json:"observationValues"`
			ConditionIDs      []string `json:"conditionIds"`
			ObservationCount  int      `json:"observationCount"`
			HasObservation    bool     `json:"hasObservation"`
		} `json:"preserving"`
		Required [][]string `json:"requiredObservationIdentities"`
		Expanded []struct {
			Identity  []string `json:"identity"`
			PatientID string   `json:"patientId"`
			Value     int      `json:"observationValue"`
			Status    string   `json:"subjectStatus"`
		} `json:"expandedDropMissing"`
		Groups []struct {
			Identity          []any    `json:"identity"`
			Contributors      []string `json:"contributorSubjectIds"`
			Count             int      `json:"count"`
			ObservationValues []int    `json:"observationValues"`
		} `json:"groupKeepMissing"`
		SourceExpanded []struct {
			Identity []any  `json:"identity"`
			Value    string `json:"value"`
		} `json:"sourceExpandedDropEmpty"`
		ExplicitGroups []struct {
			Identity     []string `json:"identity"`
			Contributors []string `json:"contributorSubjectIds"`
			Count        int      `json:"count"`
		} `json:"explicitGroups"`
		DistinctPatients struct {
			Root       string   `json:"rootSubjectPatient"`
			Performers []string `json:"observationPerformerPatients"`
		} `json:"distinctPatientOccurrences"`
	}
	raw, err := os.ReadFile(filepath.Join(fixture, "oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	execute := func(t *testing.T, output recipe.Output) []map[string]any {
		t.Helper()
		query := compileGraphRowsFixtureWithBindings(t, output, recipe.RuntimeBindings{Project: "graph-fixture", DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeUnrestricted, IncludeRowIdentity: true, SelectionProject: "graph-fixture"})
		if qualified {
			for _, name := range append(append([]string{}, query.PublicColumns...), query.PivotFields...) {
				if strings.Contains(name, "logical_key") {
					t.Fatalf("internal ordering metadata in public schema: %q", name)
				}
			}
		}
		rows := []map[string]any{}
		if err := client.QueryRows(ctx, query.Query, 100, query.BindVars, func(row map[string]any) error { rows = append(rows, row); return nil }); err != nil {
			t.Fatal(err)
		}
		if qualified {
			for _, row := range rows {
				for name := range row {
					if strings.Contains(name, "logical_key") {
						t.Fatalf("internal ordering metadata in public row: %q", name)
					}
				}
				if inventory, ok := row["__loom_dynamic_runtime_keys"]; ok {
					raw, _ := json.Marshal(inventory)
					if strings.Contains(string(raw), "logical_key") {
						t.Fatalf("internal ordering metadata in dynamic inventory: %s", raw)
					}
				}
			}
		}
		return rows
	}
	if strings.HasPrefix(values, "default-") {
		graphRowsCheckDefaultRelatedInventory(t, ctx, client, generation, strings.TrimPrefix(values, "default-"))
		return
	}
	if qualified {
		if vertexKey("Observation", "obs-many-A") < vertexKey("Observation", "obs-many-B") {
			t.Fatal("fixture must reverse physical and logical contributor order")
		}
		output := graphRowsFixtureSource(false, true)
		output.Fields = append(output.Fields,
			recipe.Field{Name: "item_all", Expr: recipe.Expression{Select: "root.extension[].valueInteger"}, ValueMode: recipe.ValueModeAll},
			recipe.Field{Name: "item_first", Expr: recipe.Expression{Select: "root.extension[].valueInteger"}, ValueMode: recipe.ValueModeFirst},
			recipe.Field{Name: "item_distinct", Expr: recipe.Expression{Select: "root.extension[].valueInteger"}, ValueMode: recipe.ValueModeDistinct})
		obs := &output.Traversals[0].Traversals[0]
		obs.Fields = append(obs.Fields,
			recipe.Field{Name: "first_value", Expr: recipe.Expression{Select: "observations.valueInteger"}, ValueMode: recipe.ValueModeFirst},
			recipe.Field{Name: "distinct_values", Expr: recipe.Expression{Select: "observations.valueInteger"}, ValueMode: recipe.ValueModeDistinct})
		dynamicKey, dynamicValue := recipe.Expression{Select: "item.url"}, recipe.Expression{Select: "item.valueInteger"}
		obs.DynamicColumns = []recipe.DynamicColumn{{Name: "scores", Source: recipe.Expression{Select: "observations.extension[]"}, Key: &dynamicKey, Value: &dynamicValue, ValueMode: recipe.ValueModeAll, Columns: []string{"score"}}}
		valueExpr := recipe.Expression{Select: "observations.valueInteger"}
		obs.Aggregates = []recipe.Aggregate{{Name: "latest", OutputName: "latest", Operation: recipe.AggregateFirstOrdered, Expr: &valueExpr,
			ContributorWindow: &recipe.ContributorWindow{Timestamp: recipe.Expression{Select: "observations.effectiveDateTime"}, Anchor: recipe.Expression{Select: "root.meta.lastUpdated"}, LowerOffset: -94608000, UpperOffset: 0, LowerInclusive: true, UpperInclusive: true, Precision: recipe.TemporalPrecisionInstant},
			Ordering:          &recipe.TemporalOrdering{Timestamp: recipe.Expression{Select: "observations.effectiveDateTime"}, Direction: recipe.TemporalDescending, TiePolicy: recipe.TemporalTieResourceKey}}}
		rows := execute(t, output)
		if len(rows) != 4 {
			t.Fatalf("two by three branches changed rows: %#v", rows)
		}
		for _, row := range rows {
			graphRowsAssertJSON(t, row["item_all"], []int{9, 1, 9})
			graphRowsAssertJSON(t, row["item_first"], 9)
			graphRowsAssertJSON(t, row["item_distinct"], []int{1, 9})
			switch row["subject_id"] {
			case "rs-many", "rs-shared":
				if inventory, ok := row["__loom_dynamic_runtime_keys"]; !ok || inventory == nil {
					t.Fatal("dynamic inventory negative control did not exercise a runtime inventory")
				}
				graphRowsAssertJSON(t, row["observations__id"], []string{"obs-many-A", "obs-many-B"})
				expectedValues, first, latest, distinct := []int{11, 13}, 11, 13, []int{11, 13}
				if values == "swapped" {
					expectedValues, first, latest = []int{13, 11}, 13, 11
				}
				if values == "duplicate" {
					expectedValues, first, latest, distinct = []int{11, 11}, 11, 11, []int{11}
				}
				graphRowsAssertJSON(t, row["observations__value"], expectedValues)
				graphRowsAssertJSON(t, row["observations__first_value"], first)
				graphRowsAssertJSON(t, row["latest"], latest)
				graphRowsAssertJSON(t, row["observations__distinct_values"], distinct)
				graphRowsAssertJSON(t, row["conditions__id"], []string{"condition-A", "condition-B", "condition-C"})
			}
		}
		required := output
		required.Traversals = append([]recipe.Traversal(nil), output.Traversals...)
		required.Traversals[0].MatchMode = recipe.MatchRequired
		required.Traversals[0].Traversals = append([]recipe.Traversal(nil), output.Traversals[0].Traversals...)
		required.Traversals[0].Traversals[0].MatchMode = recipe.MatchRequired
		requiredRows := execute(t, required)
		if len(requiredRows) != 3 {
			t.Fatalf("required match must only filter unmatched root: %#v", requiredRows)
		}
		byID := map[string]map[string]any{}
		for _, row := range rows {
			byID[row["subject_id"].(string)] = row
		}
		for _, row := range requiredRows {
			if row["subject_id"] == "rs-zero" {
				t.Fatal("unmatched row survived")
			}
			graphRowsAssertJSON(t, row["observations__value"], byID[row["subject_id"].(string)]["observations__value"])
		}
		return
	}
	t.Run("optional-two-by-three-values-preserve-four-source-identities", func(t *testing.T) {
		rows := execute(t, graphRowsFixtureSource(false, true))
		if len(rows) != len(oracle.Preserving) {
			t.Fatalf("got %d rows, want %d; independent branches multiplied rows: %#v", len(rows), len(oracle.Preserving), rows)
		}
		byID := map[string]map[string]any{}
		for _, row := range rows {
			byID[row["subject_id"].(string)] = row
		}
		for _, want := range oracle.Preserving {
			row := byID[want.Identity[0]]
			if row == nil {
				t.Fatalf("missing subject %s", want.Identity[0])
			}
			if row["patient__id"] != want.PatientID {
				t.Fatalf("patient binding: %#v", row)
			}
			graphRowsAssertJSON(t, row["observations__id"], want.ObservationIDs)
			graphRowsAssertJSON(t, row["observations__value"], want.ObservationValues)
			graphRowsAssertJSON(t, row["conditions__id"], want.ConditionIDs)
		}
	})
	t.Run("count-and-presence-policies-preserve-source-identities", func(t *testing.T) {
		rows := execute(t, graphRowsFixtureSummaries())
		if len(rows) != len(oracle.Preserving) {
			t.Fatalf("summary policies changed row count: %#v", rows)
		}
		byID := map[string]map[string]any{}
		for _, row := range rows {
			byID[row["subject_id"].(string)] = row
		}
		for _, want := range oracle.Preserving {
			row := byID[want.Identity[0]]
			if row == nil {
				t.Fatal("missing summary identity")
			}
			graphRowsAssertJSON(t, row["observation_count"], want.ObservationCount)
			graphRowsAssertJSON(t, row["has_observation"], want.HasObservation)
		}
	})
	t.Run("one-distinct-value-never-chooses-an-arbitrary-related-value", func(t *testing.T) {
		onlyOne := graphRowsFixtureOneDistinct("rs-one")
		rows := execute(t, onlyOne)
		if len(rows) != 1 {
			t.Fatalf("one-value subject rows=%#v", rows)
		}
		graphRowsAssertJSON(t, rows[0]["observation_value"], 7)
		query := compileGraphRowsFixture(t, graphRowsFixtureOneDistinct("rs-many"))
		err := client.QueryRows(ctx, query.Query, 100, query.BindVars, func(map[string]any) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES") {
			t.Fatalf("one-distinct-value silently chose among 11 and13: %v", err)
		}
	})
	t.Run("independent-branches-cannot-become-an-implicit-row-product", func(t *testing.T) {
		makeStep := func(id, target, edge string) authoringv2.ConstructionStep {
			return authoringv2.ConstructionStep{ID: id, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedExpand, RelatedExpand: &authoringv2.ConstructionRelatedExpand{AnchorColumnID: "_key", Route: []capability.ConstructionRouteStep{{EdgeID: edge, FromNodeID: "subject", ToNodeID: target, StorageDirection: "OUTBOUND"}}}}}
		}
		plan := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{makeStep("two-observations", "observations", "subject-observations"), makeStep("three-conditions", "conditions", "subject-conditions")}}
		if err := plan.Validate(nil); err == nil || !strings.Contains(err.Error(), "INDEPENDENT_RELATED_EXPANSIONS_UNSUPPORTED") {
			t.Fatalf("unsupported product accepted: %v", err)
		}
		rows := execute(t, graphRowsFixtureSource(false, true))
		if len(rows) != len(oracle.Preserving) {
			t.Fatal("rejected branch expansion changed source identities")
		}
	})
	t.Run("distinct-patient-occurrences-keep-subject-and-performer-bindings", func(t *testing.T) {
		output := graphRowsFixtureSource(false, true)
		output.Traversals[0].Traversals[0].Traversals = []recipe.Traversal{{Name: "performer_Patient", Alias: "performer", OccurrenceID: "performer", ToResourceType: "Patient", MatchMode: recipe.MatchOptional, Fields: []recipe.Field{{Name: "id", ColumnID: "performer-id", Expr: recipe.Expression{Select: "performer.id"}, ValueMode: recipe.ValueModeDistinct}}}}
		rows := execute(t, output)
		found := false
		for _, row := range rows {
			if row["subject_id"] == "rs-many" {
				found = true
				if row["patient__id"] != oracle.DistinctPatients.Root {
					t.Fatalf("subject patient retargeted: %#v", row)
				}
				graphRowsAssertJSON(t, row["performer__id"], oracle.DistinctPatients.Performers)
			}
		}
		if !found {
			t.Fatal("missing rs-many")
		}
	})
	t.Run("source-repeated-item-scope-and-empty-policies", func(t *testing.T) {
		rows := execute(t, graphRowsFixtureSourceExpanded(recipe.ExpansionExclude))
		if len(rows) != len(oracle.SourceExpanded) {
			t.Fatalf("source expanded rows=%#v", rows)
		}
		byID := map[string]map[string]any{}
		for _, row := range rows {
			identity, ok := row["__loom_expansion_identity"].(map[string]any)
			if !ok {
				t.Fatalf("missing source item identity: %#v", row)
			}
			if identity["root_id"] != "ResearchSubject/"+fmt.Sprint(row["subject_id"]) || identity["owner_id"] != identity["root_id"] || identity["occurrence_id"] != "subject" || identity["item_present"] != true {
				t.Fatalf("incorrect source item identity: %#v", identity)
			}
			key := fmt.Sprint(row["subject_id"]) + "/" + fmt.Sprint(identity["ordinal"])
			byID[key] = row
		}
		for _, want := range oracle.SourceExpanded {
			key := fmt.Sprint(want.Identity[0]) + "/" + fmt.Sprint(want.Identity[1])
			row := byID[key]
			if row == nil || row["identifier_value"] != want.Value {
				t.Fatalf("missing/wrong source item %s: %#v", key, row)
			}
		}
		kept := execute(t, graphRowsFixtureSourceExpanded(recipe.ExpansionPreserveParent))
		if len(kept) != len(rows)+1 {
			t.Fatalf("source empty parent lost: %#v", kept)
		}
		found := false
		for _, row := range kept {
			if row["subject_id"] == "rs-zero" {
				found = true
				identity, ok := row["__loom_expansion_identity"].(map[string]any)
				if !ok || identity["item_present"] != false || identity["ordinal"] != nil {
					t.Fatalf("empty source item identity wrong: %#v", row)
				}
				if row["identifier_value"] != nil {
					t.Fatalf("empty source item gained a value: %#v", row)
				}
			}
		}
		if !found {
			t.Fatal("missing source empty-parent identity")
		}
	})
	t.Run("required-match-reports-exact-row-loss", func(t *testing.T) {
		rows := execute(t, graphRowsFixtureSource(true, true))
		ids := []string{}
		for _, row := range rows {
			ids = append(ids, row["subject_id"].(string))
		}
		sort.Strings(ids)
		want := []string{}
		for _, identity := range oracle.Required {
			want = append(want, identity[0])
		}
		sort.Strings(want)
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("required identities=%v want=%v", ids, want)
		}
	})
	t.Run("related-expansion-keeps-shared-record-pairs-and-values", func(t *testing.T) {
		rows := execute(t, graphRowsFixtureExpanded(recipe.ExpansionExclude))
		if len(rows) != len(oracle.Expanded) {
			t.Fatalf("expanded rows=%#v", rows)
		}
		byPair := map[string]map[string]any{}
		rowIDs := map[string]bool{}
		for _, row := range rows {
			key := fmt.Sprint(row["subject_id"]) + "/" + fmt.Sprint(row["observation_id"])
			byPair[key] = row
			id, ok := row["__loom_row_id"].(string)
			if !ok || id == "" || rowIDs[id] {
				t.Fatalf("missing or repeated expansion identity: %#v", row)
			}
			rowIDs[id] = true
		}
		for _, want := range oracle.Expanded {
			row := byPair[strings.Join(want.Identity, "/")]
			if row == nil {
				t.Fatalf("missing pair=%v", want.Identity)
			}
			if row["patient__id"] != want.PatientID || row["subject_status"] != want.Status {
				t.Fatalf("upstream values changed: %#v", row)
			}
			graphRowsAssertJSON(t, row["observation_value"], want.Value)
			identity := [][]string{{"input", want.Identity[0]}, {"construction", "expand-observations"}, {"related", "Observation/" + want.Identity[1]}}
			encoded, _ := json.Marshal(identity)
			if row["__loom_row_id"] != string(encoded) {
				t.Fatalf("identity=%v want=%s", row["__loom_row_id"], encoded)
			}
		}
		repeated := execute(t, graphRowsFixtureExpanded(recipe.ExpansionExclude))
		for _, row := range repeated {
			if !rowIDs[row["__loom_row_id"].(string)] {
				t.Fatal("unstable related row identity")
			}
		}
		kept := execute(t, graphRowsFixtureExpanded(recipe.ExpansionPreserveParent))
		if len(kept) != len(oracle.Expanded)+1 {
			t.Fatalf("preserve-parent rows=%#v", kept)
		}
		found := false
		for _, row := range kept {
			if row["subject_id"] == "rs-zero" {
				found = true
				if row["observation_id"] != nil || row["observation_value"] != nil {
					t.Fatalf("empty related values=%#v", row)
				}
			}
		}
		if !found {
			t.Fatal("missing preserve-parent row")
		}
	})
	t.Run("related-expansion-preserves-existing-all-lists-and-explicit-scalar", func(t *testing.T) {
		output := graphRowsFixtureExpandedWithLists(recipe.ExpansionExclude)
		// The existing list and the separately authored per-match scalar retain
		// different identities and policies throughout the same expansion.
		observation := output.Traversals[0].Traversals[0]
		if observation.Fields[1].ColumnID != "observation-values" || observation.Fields[1].ValueMode != recipe.ValueModeAll || output.Construction.Steps[1].Operation.RelatedField.OutputColumnID != "observation-value" {
			t.Fatal("expansion fixture rewrote existing column policy or identity")
		}
		for _, step := range output.Construction.Steps {
			found := false
			for _, column := range step.Outputs {
				if column.ID == "observation-values" {
					found = column.Name == "observations__value" && column.Type == "array"
				}
			}
			if !found {
				t.Fatalf("step %s dropped or scalarized the existing list", step.ID)
			}
		}
		preserved := map[string]struct {
			values     []int
			conditions []string
		}{}
		for _, want := range oracle.Preserving {
			preserved[want.Identity[0]] = struct {
				values     []int
				conditions []string
			}{want.ObservationValues, want.ConditionIDs}
		}
		assertPairs := func(rows []map[string]any, keepParent bool) {
			t.Helper()
			wanted := len(oracle.Expanded)
			if keepParent {
				wanted++
			}
			if len(rows) != wanted {
				t.Fatalf("unexpected multiplication: got %d rows, want %d", len(rows), wanted)
			}
			byPair := map[string]map[string]any{}
			for _, row := range rows {
				subject := fmt.Sprint(row["subject_id"])
				graphRowsAssertJSON(t, row["observations__value"], preserved[subject].values)
				graphRowsAssertJSON(t, row["conditions__id"], preserved[subject].conditions)
				key := subject + "/" + fmt.Sprint(row["observation_id"])
				if byPair[key] != nil {
					t.Fatalf("duplicate related pair %s", key)
				}
				byPair[key] = row
			}
			for _, want := range oracle.Expanded {
				row := byPair[strings.Join(want.Identity, "/")]
				if row == nil {
					t.Fatalf("missing pair %v", want.Identity)
				}
				graphRowsAssertJSON(t, row["observation_value"], want.Value)
				if row["subject_status"] != want.Status || row["patient__id"] != want.PatientID {
					t.Fatalf("upstream scalar changed: %#v", row)
				}
			}
			if keepParent {
				row := byPair["rs-zero/<nil>"]
				if row == nil || row["observation_value"] != nil {
					t.Fatalf("missing-parent policy changed: %#v", row)
				}
			}
		}
		assertPairs(execute(t, output), false)
		assertPairs(execute(t, graphRowsFixtureExpandedWithLists(recipe.ExpansionPreserveParent)), true)
	})
	t.Run("named-source-groups-retain-empty-group-and-exact-member-identities", func(t *testing.T) {
		collections := []storearango.CollectionSpec{}
		for _, name := range []string{"loom_explorer_explicit_group_revisions", "loom_explorer_explicit_group_definitions", "loom_explorer_explicit_group_memberships", "loom_explorer_selections", "loom_explorer_selection_members"} {
			collections = append(collections, storearango.CollectionSpec{Name: name})
		}
		if err := client.Bootstrap(ctx, storearango.BootstrapSpec{Collections: collections}); err != nil {
			t.Fatal(err)
		}
		put := func(collection string, documents ...map[string]any) {
			t.Helper()
			raw := []json.RawMessage{}
			for _, document := range documents {
				encoded, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				raw = append(raw, encoded)
			}
			insert(collection, raw)
		}
		put("loom_explorer_explicit_group_revisions", map[string]any{"_key": "graph-named-groups", "state": "COMPLETE", "project": "graph-fixture", "generation": "fixture-v1", "resourceType": "ResearchSubject", "sourceSelectionRevisionId": "graph-group-selection", "scopeDigest": "fixture-scope", "sourceMembershipDigest": "fixture-members"})
		put("loom_explorer_selections", map[string]any{"_key": "graph-group-selection", "complete": true, "project": "graph-fixture", "generation": "fixture-v1", "resourceType": "ResearchSubject", "scopeDigest": "fixture-scope", "membershipDigest": "fixture-members"})
		for _, want := range oracle.Preserving {
			id := want.Identity[0]
			put("loom_explorer_selection_members", map[string]any{"_key": "selected-" + id, "selectionId": "graph-group-selection", "project": "graph-fixture", "generation": "fixture-v1", "resourceType": "ResearchSubject", "id": id})
		}
		for index, want := range oracle.ExplicitGroups {
			group := want.Identity[0]
			put("loom_explorer_explicit_group_definitions", map[string]any{"_key": "definition-" + group, "revisionId": "graph-named-groups", "project": "graph-fixture", "groupId": group, "label": group, "ordinal": index})
			for _, id := range want.Contributors {
				put("loom_explorer_explicit_group_memberships", map[string]any{"_key": "member-" + group + "-" + id, "revisionId": "graph-named-groups", "groupId": group, "project": "graph-fixture", "generation": "fixture-v1", "resourceType": "ResearchSubject", "id": id, "ref": map[string]any{"project": "graph-fixture", "generation": "fixture-v1", "resourceType": "ResearchSubject", "id": id}})
			}
		}
		output := recipe.Output{Name: "named_groups", RootResourceType: "ResearchSubject", RowGrain: "groups", GroupRows: &recipe.GroupRows{RevisionID: "graph-named-groups", UnassignedMemberPolicy: "EXCLUDE"}}
		rows := execute(t, output)
		if len(rows) != len(oracle.ExplicitGroups) {
			t.Fatalf("named source group lost empty identity: %#v", rows)
		}
		byID := map[string]map[string]any{}
		for _, row := range rows {
			byID[row["group_id"].(string)] = row
		}
		for _, want := range oracle.ExplicitGroups {
			row := byID[want.Identity[0]]
			if row == nil {
				t.Fatal("missing named group")
			}
			members, ok := row["members"].([]any)
			if !ok || len(members) != want.Count {
				t.Fatalf("members=%#v want count=%d", row["members"], want.Count)
			}
			actual := []string{}
			for _, member := range members {
				actual = append(actual, member.(map[string]any)["source_identity"].(map[string]any)["id"].(string))
			}
			expected := append([]string{}, want.Contributors...)
			sort.Strings(actual)
			sort.Strings(expected)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("named member identity set=%v want=%v", actual, expected)
			}
			graphRowsAssertJSON(t, row["__loom_row_id"], map[string]any{"group_revision_id": "graph-named-groups", "group_id": want.Identity[0]})
		}
	})
	t.Run("group-complete-key-tuples-and-retained-contributors", func(t *testing.T) {
		rows := execute(t, graphRowsFixtureGrouped())
		if len(rows) != len(oracle.Groups) {
			t.Fatalf("group rows=%#v", rows)
		}
		byKey := map[string]map[string]any{}
		for _, row := range rows {
			key, _ := json.Marshal([]any{row["gender"], row["status"]})
			byKey[string(key)] = row
		}
		for _, want := range oracle.Groups {
			key, _ := json.Marshal(want.Identity)
			row := byKey[string(key)]
			if row == nil {
				t.Fatalf("missing complete key tuple=%s", key)
			}
			graphRowsAssertJSON(t, row["subjects"], want.Contributors)
			graphRowsAssertJSON(t, row["count"], want.Count)
			graphRowsAssertJSON(t, row["observation_values"], want.ObservationValues)
		}
		exclude := graphRowsFixtureGrouped()
		exclude.Construction.Steps[0].Operation.Group.MissingKeyPolicy = recipe.ConstructionGroupMissingKeyExclude
		filtered := execute(t, exclude)
		expectedCount := 0
		for _, want := range oracle.Groups {
			if want.Identity[0] != nil {
				expectedCount++
			}
		}
		if len(filtered) != expectedCount {
			t.Fatalf("missing-key exclusion rows=%#v", filtered)
		}
		for _, row := range filtered {
			if row["gender"] == nil {
				t.Fatal("missing-key group survived EXCLUDE")
			}
		}
	})
}

func graphRowsAssertJSON(t *testing.T, actual, expected any) {
	t.Helper()
	got, _ := json.Marshal(actual)
	want, _ := json.Marshal(expected)
	if string(got) != string(want) {
		t.Fatalf("actual=%s want independent fixture=%s", got, want)
	}
}

func TestGraphRowsIndependentFixturePlansCompile(t *testing.T) {
	for _, output := range []recipe.Output{graphRowsFixtureSource(false, true), graphRowsFixtureSource(true, true), graphRowsFixtureExpanded(recipe.ExpansionExclude), graphRowsFixtureExpanded(recipe.ExpansionPreserveParent), graphRowsFixtureGrouped(), graphRowsFixtureSummaries(), graphRowsFixtureSourceExpanded(recipe.ExpansionExclude), graphRowsFixtureSourceExpanded(recipe.ExpansionPreserveParent)} {
		compileGraphRowsFixture(t, output)
	}
}

func compileGraphRowsFixture(t *testing.T, output recipe.Output) compiler.CompiledQuery {
	t.Helper()
	bindings := recipe.RuntimeBindings{Project: "graph-fixture", DatasetGeneration: "fixture-v1", AuthScopeMode: authscope.ReadScopeUnrestricted, IncludeRowIdentity: true, SelectionProject: "graph-fixture"}
	return compileGraphRowsFixtureWithBindings(t, output, bindings)
}
func compileGraphRowsFixtureWithBindings(t *testing.T, output recipe.Output, bindings recipe.RuntimeBindings) compiler.CompiledQuery {
	t.Helper()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "graph-rows-fixture", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	query, err := compiler.CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return query
}

func graphRowsFixtureSource(required, branches bool) recipe.Output {
	match := recipe.MatchOptional
	if required {
		match = recipe.MatchRequired
	}
	patient := recipe.Traversal{Name: "subject_Patient", Alias: "patient", OccurrenceID: "patient", ToResourceType: "Patient", MatchMode: match, Fields: []recipe.Field{
		{Name: "id", ColumnID: "patient-id", Expr: recipe.Expression{Select: "patient.id"}, ValueMode: recipe.ValueModeFirst},
		{Name: "gender", ColumnID: "patient-gender", Expr: recipe.Expression{Select: "patient.gender"}, ValueMode: recipe.ValueModeFirst},
	}}
	if branches {
		patient.Traversals = []recipe.Traversal{
			{Name: "subject_Patient", Alias: "observations", OccurrenceID: "observations", ToResourceType: "Observation", MatchMode: match, Fields: []recipe.Field{
				{Name: "id", ColumnID: "observation-ids", Expr: recipe.Expression{Select: "observations.id"}, ValueMode: recipe.ValueModeAll},
				{Name: "value", ColumnID: "observation-values", Expr: recipe.Expression{Select: "observations.valueInteger"}, ValueMode: recipe.ValueModeAll},
			}},
			{Name: "subject_Patient", Alias: "conditions", OccurrenceID: "conditions", ToResourceType: "Condition", MatchMode: recipe.MatchOptional, Fields: []recipe.Field{{Name: "id", ColumnID: "condition-ids", Expr: recipe.Expression{Select: "conditions.id"}, ValueMode: recipe.ValueModeAll}}},
		}
	}
	return recipe.Output{Name: "subjects", RootResourceType: "ResearchSubject", RowGrain: "study_enrollment", RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingAlias, Fields: []recipe.Field{
		{Name: "subject_id", ColumnID: "subject-id", Expr: recipe.Expression{Select: "root.id"}},
		{Name: "subject_status", ColumnID: "subject-status", Expr: recipe.Expression{Select: "root.status"}},
	}, Traversals: []recipe.Traversal{patient}}
}

func graphRowsFixtureSourceColumns() []recipe.StageColumn {
	return []recipe.StageColumn{
		{ID: "subject-id", Name: "subject_id", Type: "string"}, {ID: "subject-status", Name: "subject_status", Type: "string"},
		{ID: "patient-id", Name: "patient__id", Type: "string", Nullable: true}, {ID: "patient-gender", Name: "patient__gender", Type: "string", Nullable: true},
	}
}

func graphRowsFixtureExpanded(policy recipe.ExpansionEmptyPolicy) recipe.Output {
	output := graphRowsFixtureSource(false, false)
	columns := graphRowsFixtureSourceColumns()
	expanded := append(append([]recipe.StageColumn{}, columns...), recipe.StageColumn{ID: "observation-id", Name: "observation_id", Type: "string", Nullable: policy == recipe.ExpansionPreserveParent})
	output.Construction = &recipe.Construction{Version: 1, SourceColumns: columns, Steps: []recipe.ConstructionStep{{ID: "expand-observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
		AnchorColumnID: "_key", ChoiceID: "observation-route", TargetNodeID: "observation", TargetResourceType: "Observation", ContributorPolicy: "ALL_MATCHES", EmptyPolicy: policy, RelatedRecordColumnID: "observation-id", Route: []recipe.ConstructionRelatedRouteStep{
			{EdgeID: "subject-patient", FromNodeID: "subject", ToNodeID: "patient", FromResourceType: "ResearchSubject", ToResourceType: "Patient", Relationship: "subject_Patient", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL"},
			{EdgeID: "patient-observation", FromNodeID: "patient", ToNodeID: "observation", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"},
		},
	}}, Outputs: expanded}, {ID: "observation-value", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand-observations"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedFieldOp, RelatedField: &recipe.ConstructionRelatedField{
		ChoiceID: "observation-value", Source: recipe.ConstructionRelatedFieldSource{CandidateID: "observation-value", NodeID: "observation", ResourceType: "Observation", Path: "Observation.valueInteger", LogicalType: "integer", Cardinality: "optional_one"}, OutputColumnID: "observation-value",
	}}, Outputs: append(append([]recipe.StageColumn{}, expanded...), recipe.StageColumn{ID: "observation-value", Name: "observation_value", Type: "integer", Nullable: true})}}}
	return output
}

func graphRowsFixtureGrouped() recipe.Output {
	output := graphRowsFixtureSource(false, true)
	output.Construction = &recipe.Construction{Version: 1, SourceColumns: append(graphRowsFixtureSourceColumns(), []recipe.StageColumn{{ID: "observation-ids", Name: "observations__id", Type: "array"}, {ID: "observation-values", Name: "observations__value", Type: "array"}, {ID: "condition-ids", Name: "conditions__id", Type: "array"}}...), Steps: []recipe.ConstructionStep{{ID: "group-subjects", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
		ConstructionID: "group-subjects", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup, Keys: []recipe.ConstructionGroupKey{{InputColumnID: "patient-gender", OutputColumnID: "gender"}, {InputColumnID: "subject-status", OutputColumnID: "status"}}, Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "count"}},
	}}, RowValues: []recipe.ConstructionRowValue{{InputColumnID: "subject-id", OutputColumnID: "subjects", Policy: recipe.ConstructionRowValueAll}, {InputColumnID: "observation-values", OutputColumnID: "observation-values-retained", Policy: recipe.ConstructionRowValueAll}}, Outputs: []recipe.StageColumn{{ID: "gender", Name: "gender", Type: "string", Nullable: true}, {ID: "status", Name: "status", Type: "string"}, {ID: "count", Name: "count", Type: "integer"}, {ID: "subjects", Name: "subjects", Type: "array"}, {ID: "observation-values-retained", Name: "observation_values", Type: "array"}}}}}
	return output
}

func graphRowsFixtureObservationRoute() []recipe.ConstructionRelatedRouteStep {
	return []recipe.ConstructionRelatedRouteStep{
		{EdgeID: "subject-patient", FromNodeID: "subject", ToNodeID: "patient", FromResourceType: "ResearchSubject", ToResourceType: "Patient", Relationship: "subject_Patient", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL"},
		{EdgeID: "patient-observation", FromNodeID: "patient", ToNodeID: "observation", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"},
	}
}

func graphRowsFixtureSummaries() recipe.Output {
	output := graphRowsFixtureSource(false, false)
	columns := graphRowsFixtureSourceColumns()
	output.Construction = &recipe.Construction{Version: 1, SourceColumns: columns}
	for index, form := range []string{"COUNT", "PRESENCE"} {
		id, name, kind := "observation-count", "observation_count", "integer"
		if form == "PRESENCE" {
			id, name, kind = "has-observation", "has_observation", "boolean"
		}
		input := recipe.ConstructionInputRef{Kind: recipe.ConstructionSourceProjectionInput}
		if index > 0 {
			input = recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: "observation-count"}
		}
		columns = append(append([]recipe.StageColumn{}, columns...), recipe.StageColumn{ID: id, Name: name, Type: kind})
		output.Construction.Steps = append(output.Construction.Steps, recipe.ConstructionStep{ID: id, Inputs: []recipe.ConstructionInputRef{input}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
			AnchorColumnID: "_key", ChoiceID: form, SourceOccurrenceID: "observation", Source: recipe.ConstructionRelatedFieldSource{CandidateID: "observation-id", NodeID: "observation", ResourceType: "Observation", Path: "Observation.id", Cardinality: "required_one", LogicalType: "string"}, Route: graphRowsFixtureObservationRoute(), ContributorPolicy: "ALL_MATCHES", Form: form, OutputColumnID: id,
		}}, Outputs: columns})
	}
	return output
}

func graphRowsFixtureSourceExpanded(policy recipe.ExpansionEmptyPolicy) recipe.Output {
	return recipe.Output{Name: "subject_items", RootResourceType: "ResearchSubject", RootOccurrenceID: "subject", RowGrain: "expanded", RootColumnNaming: recipe.RootColumnNamingExact,
		Expand: &recipe.Expansion{OwnerOccurrenceID: "subject", From: recipe.Expression{Select: "root.identifier[]"}, As: "identifier_item", Ordinality: "identifier_ordinal", EmptyPolicy: policy}, Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
		Fields: []recipe.Field{{Name: "subject_id", ColumnID: "subject-id", Expr: recipe.Expression{Select: "root.id"}}, {Name: "identifier_value", ColumnID: "identifier-value", Expr: recipe.Expression{Select: "identifier_item.value"}}},
	}
}

func graphRowsFixtureOneDistinct(subject string) recipe.Output {
	output := graphRowsFixtureSource(false, false)
	output.Filters = []recipe.Filter{{Select: "root.id", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &subject}}}}
	columns := graphRowsFixtureSourceColumns()
	output.Construction = &recipe.Construction{Version: 1, SourceColumns: columns, Steps: []recipe.ConstructionStep{{ID: "one-distinct-observation", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
		AnchorColumnID: "_key", ChoiceID: "one-distinct-observation", SourceOccurrenceID: "observation", Source: recipe.ConstructionRelatedFieldSource{CandidateID: "observation-value", NodeID: "observation", ResourceType: "Observation", Path: "Observation.valueInteger", Cardinality: "optional_one", LogicalType: "integer"}, Route: graphRowsFixtureObservationRoute(), ContributorPolicy: "ALL_MATCHES", Form: "ALL", RowValuePolicy: recipe.ConstructionRowValueOne, OutputColumnID: "observation-value",
	}}, Outputs: append(append([]recipe.StageColumn{}, columns...), recipe.StageColumn{ID: "observation-value", Name: "observation_value", Type: "integer", Nullable: true})}}}
	return output
}

// This fixture preserves existing source ALL columns while adding an explicit
// related-record scalar. Their stable identities are intentionally distinct.
func graphRowsFixtureExpandedWithLists(policy recipe.ExpansionEmptyPolicy) recipe.Output {
	output := graphRowsFixtureExpanded(policy)
	output.Traversals = graphRowsFixtureSource(false, true).Traversals
	columns := append(graphRowsFixtureSourceColumns(),
		recipe.StageColumn{ID: "observation-ids", Name: "observations__id", Type: "array"},
		recipe.StageColumn{ID: "observation-values", Name: "observations__value", Type: "array"},
		recipe.StageColumn{ID: "condition-ids", Name: "conditions__id", Type: "array"},
	)
	output.Construction.SourceColumns = columns
	expanded := append(append([]recipe.StageColumn{}, columns...), recipe.StageColumn{ID: "observation-id", Name: "observation_id", Type: "string", Nullable: policy == recipe.ExpansionPreserveParent})
	output.Construction.Steps[0].Outputs = expanded
	output.Construction.Steps[1].Outputs = append(append([]recipe.StageColumn{}, expanded...), recipe.StageColumn{ID: "observation-value", Name: "observation_value", Type: "integer", Nullable: true})
	return output
}
