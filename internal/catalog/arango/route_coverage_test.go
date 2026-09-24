package arango

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestRouteCoverageQueryFollowsPinnedStorageDirectionAndCountsDistinctRoots(t *testing.T) {
	opts := catalog.RouteCoverageOptions{
		Project: "p", DatasetGeneration: "g", BuildID: "build", AuthResourcePathsUnrestricted: true,
		RootResourceType: "Patient", SourceResourceType: "Observation",
		Route:  []catalog.RouteCoverageStep{{FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND"}},
		Source: catalog.RouteCoverageSource{Kind: catalog.RouteCoverageSemantic, ConceptID: "height", BindingID: "height-value"},
	}
	query, vars, err := routeCoverageQuery(opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"contribution.source_kind == 'retained_vertex'",
		"COLLECT source_id = contribution.source_id",
		"edge_0._from == source._id",
		"DOCUMENT(edge_0._to)",
		"COLLECT root_id = vertex_0._id",
		"COLLECT WITH COUNT INTO rows_with_value",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("route coverage omitted %q: %s", fragment, query)
		}
	}
	if vars["concept_id"] != "height" || vars["binding_id"] != "height-value" || vars["label_0"] != "subject_Patient" {
		t.Fatalf("route coverage bind variables = %#v", vars)
	}
	if strings.Contains(query, "height") || strings.Contains(query, "height-value") {
		t.Fatal("source identity was interpolated into AQL")
	}
	query, _, err = routeCoverageQueryWithHint(opts, "idx_semantic_exact")
	if err != nil || !strings.Contains(query, `OPTIONS { indexHint: "idx_semantic_exact", forceIndexHint: true }`) {
		t.Fatalf("exact semantic index hint = %q, %v", query, err)
	}
	if _, _, err := routeCoverageQueryWithHint(opts, `idx_bad"; REMOVE`); err == nil {
		t.Fatal("untrusted index hint accepted")
	}
	opts.Route[0].StorageDirection = "OUTBOUND"
	query, _, err = routeCoverageQuery(opts)
	if err != nil || !strings.Contains(query, "edge_0._to == source._id") || !strings.Contains(query, "DOCUMENT(edge_0._from)") {
		t.Fatalf("opposite storage direction = %q, %v", query, err)
	}
}

func TestRouteCoverageFieldPresencePreservesFalseZeroAndEmptyString(t *testing.T) {
	presence, err := routeCoverageFieldPresence("component[].valueInteger")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(presence, "FOR value_1 IN (IS_ARRAY(value_0.component) ? value_0.component : [])") ||
		!strings.Contains(presence, "LET value_2 = value_1.valueInteger") ||
		!strings.Contains(presence, "FILTER value_2 != null") {
		t.Fatalf("field presence expression = %q", presence)
	}
	if _, err := routeCoverageFieldPresence("component[].valueInteger FILTER true"); err == nil {
		t.Fatal("untrusted field path accepted")
	}
}

func TestRouteCoverageExistenceQueryShortCircuitsAfterAuthorizedRoot(t *testing.T) {
	opts := catalog.RouteCoverageOptions{
		Project: "p", DatasetGeneration: "g", BuildID: "build", AuthResourcePaths: []string{"scope-a"},
		RootResourceType: "Specimen", SourceResourceType: "Observation",
		Route:  []catalog.RouteCoverageStep{{FromResourceType: "Specimen", ToResourceType: "Observation", Relationship: "specimen_observation", StorageDirection: "OUTBOUND"}},
		Source: catalog.RouteCoverageSource{Kind: catalog.RouteCoverageSemantic, ConceptID: "concept", BindingID: "binding"},
	}
	query, vars, err := routeCoverageExistenceQuery(opts, "idx_semantic_exact")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"contribution.build_id == @build_id AND contribution.source_kind == 'retained_vertex'",
		"contribution.concept_id == @concept_id AND contribution.binding_id == @binding_id",
		"source.project == @project AND source.dataset_generation == @generation",
		"@unrestricted == true OR source.auth_resource_path IN @auth_paths",
		"edge_0.project == @project AND edge_0.dataset_generation == @generation",
		"@unrestricted == true OR edge_0.auth_resource_path IN @auth_paths",
		"vertex_0.resourceType == @parent_type_0",
		"vertex_0.project == @project AND vertex_0.dataset_generation == @generation",
		"@unrestricted == true OR vertex_0.auth_resource_path IN @auth_paths",
		"LIMIT 1",
		"RETURN { matched: true }",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("existence query omitted %q: %s", fragment, query)
		}
	}
	if strings.Contains(query, "COLLECT") {
		t.Fatalf("existence query should not aggregate all source rows: %s", query)
	}
	if !strings.Contains(query, `OPTIONS { indexHint: "idx_semantic_exact", forceIndexHint: true }`) || vars["auth_paths"].([]string)[0] != "scope-a" {
		t.Fatalf("existence query lost index or authorization binds: query=%s vars=%#v", query, vars)
	}
}

func TestFieldRouteCoverageExistenceAnchorsLastEdgeAndChecksEveryVertexAndEdge(t *testing.T) {
	tests := []struct {
		name             string
		route            []catalog.RouteCoverageStep
		anchorSource     string
		anchorParent     string
		anchorParentType string
		anchorChildType  string
		backwardMatch    string
	}{
		{
			name:             "inbound",
			route:            []catalog.RouteCoverageStep{{FromResourceType: "Specimen", ToResourceType: "Observation", Relationship: "specimen_observation", StorageDirection: "INBOUND"}},
			anchorSource:     "LET source = DOCUMENT(edge_0._from)",
			anchorParent:     "LET vertex_0 = DOCUMENT(edge_0._to)",
			anchorParentType: "edge_0.to_type == @parent_type_0",
			anchorChildType:  "edge_0.from_type == @child_type_0",
		},
		{
			name:             "outbound",
			route:            []catalog.RouteCoverageStep{{FromResourceType: "Specimen", ToResourceType: "Observation", Relationship: "specimen_observation", StorageDirection: "OUTBOUND"}},
			anchorSource:     "LET source = DOCUMENT(edge_0._to)",
			anchorParent:     "LET vertex_0 = DOCUMENT(edge_0._from)",
			anchorParentType: "edge_0.from_type == @parent_type_0",
			anchorChildType:  "edge_0.to_type == @child_type_0",
		},
		{
			name: "mixed route walks backward",
			route: []catalog.RouteCoverageStep{
				{FromResourceType: "Patient", ToResourceType: "ResearchStudy", Relationship: "patient_study", StorageDirection: "OUTBOUND"},
				{FromResourceType: "ResearchStudy", ToResourceType: "Observation", Relationship: "study_observation", StorageDirection: "INBOUND"},
			},
			anchorSource:     "LET source = DOCUMENT(edge_1._from)",
			anchorParent:     "LET vertex_1 = DOCUMENT(edge_1._to)",
			anchorParentType: "edge_1.to_type == @parent_type_1",
			anchorChildType:  "edge_1.from_type == @child_type_1",
			backwardMatch:    "edge_0._to == vertex_1._id",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := catalog.RouteCoverageOptions{
				Project: "project-a", DatasetGeneration: "generation-a", AuthResourcePaths: []string{"scope-a"},
				RootResourceType: test.route[0].FromResourceType, SourceResourceType: "Observation",
				Route: test.route, Source: catalog.RouteCoverageSource{Kind: catalog.RouteCoverageField, FieldPath: "status"},
			}
			query, vars, err := routeCoverageExistenceQuery(opts, "")
			if err != nil {
				t.Fatal(err)
			}
			last := len(test.route) - 1
			if !strings.HasPrefix(query, "FOR edge_"+strconv.Itoa(last)+" IN fhir_edge") ||
				!strings.Contains(query, test.anchorSource) || !strings.Contains(query, test.anchorParent) ||
				!strings.Contains(query, test.anchorParentType) || !strings.Contains(query, test.anchorChildType) {
				t.Fatalf("edge-first endpoint mapping = %s", query)
			}
			if test.backwardMatch != "" && (!strings.Contains(query, test.backwardMatch) || !strings.Contains(query, "LET vertex_0 = DOCUMENT(edge_0._from)")) {
				t.Fatalf("remaining route steps were not walked backward: %s", query)
			}
			for index := range test.route {
				edge := "edge_" + strconv.Itoa(index)
				vertex := "vertex_" + strconv.Itoa(index)
				for _, fragment := range []string{
					edge + ".project == @project AND " + edge + ".dataset_generation == @generation",
					"@unrestricted == true OR " + edge + ".auth_resource_path IN @auth_paths",
					vertex + ".project == @project AND " + vertex + ".dataset_generation == @generation",
					"@unrestricted == true OR " + vertex + ".auth_resource_path IN @auth_paths",
				} {
					if !strings.Contains(query, fragment) {
						t.Errorf("query omitted per-edge/per-vertex filter %q: %s", fragment, query)
					}
				}
				if vars["label_"+strconv.Itoa(index)] != test.route[index].Relationship ||
					vars["parent_type_"+strconv.Itoa(index)] != test.route[index].FromResourceType ||
					vars["child_type_"+strconv.Itoa(index)] != test.route[index].ToResourceType {
					t.Errorf("route binds for step %d = %#v", index, vars)
				}
			}
			for _, fragment := range []string{
				"source.project == @project AND source.dataset_generation == @generation",
				"source.resourceType == @source_type",
				"@unrestricted == true OR source.auth_resource_path IN @auth_paths",
				"FILTER value_1 != null",
				"LIMIT 1",
			} {
				if !strings.Contains(query, fragment) {
					t.Errorf("query omitted source/presence filter %q: %s", fragment, query)
				}
			}
			if vars["project"] != "project-a" || vars["generation"] != "generation-a" || vars["root_type"] != opts.RootResourceType || vars["source_type"] != "Observation" {
				t.Errorf("route query lost project, generation, or endpoint type binds: %#v", vars)
			}
		})
	}
}

func TestFieldSourceMembershipExistenceUsesIndexedPathAndRechecksWholeAuthorizedRoute(t *testing.T) {
	opts := catalog.RouteCoverageOptions{
		Project: "project-a", DatasetGeneration: "generation-a", AuthResourcePaths: []string{"scope-a"},
		RootResourceType: "Patient", SourceResourceType: "Observation",
		Route: []catalog.RouteCoverageStep{
			{FromResourceType: "Patient", ToResourceType: "ResearchStudy", Relationship: "patient_study", StorageDirection: "OUTBOUND"},
			{FromResourceType: "ResearchStudy", ToResourceType: "Observation", Relationship: "study_observation", StorageDirection: "INBOUND"},
		},
		Source: catalog.RouteCoverageSource{Kind: catalog.RouteCoverageField, FieldPath: "valueString"},
	}
	query, vars, err := routeCoverageMembershipFieldExistenceQuery(opts, "idx_membership_scalar_paths")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`FOR membership IN fhir_field_source_membership OPTIONS { indexHint: "idx_membership_scalar_paths", forceIndexHint: true }`,
		"membership.project == @project AND membership.dataset_generation == @generation",
		"membership.resource_type == @source_type",
		"@field_path IN membership.scalar_paths",
		"@unrestricted == true OR membership.auth_resource_path IN @auth_paths",
		"LET source = DOCUMENT(membership.vertex_id)",
		"source._id == membership.vertex_id",
		"source.project == @project AND source.dataset_generation == @generation",
		"source.resourceType == @source_type",
		"@unrestricted == true OR source.auth_resource_path IN @auth_paths",
		"FILTER value_1 != null",
		"edge_1._from == source._id AND edge_1.label == @label_1",
		"LET vertex_1 = DOCUMENT(edge_1._to)",
		"edge_0._to == vertex_1._id AND edge_0.label == @label_0",
		"LET vertex_0 = DOCUMENT(edge_0._from)",
		"FILTER vertex_0.resourceType == @root_type",
		"LIMIT 1",
	} {
		if !strings.Contains(query, fragment) {
			t.Errorf("membership route query omitted %q: %s", fragment, query)
		}
	}
	for index := range opts.Route {
		edge := "edge_" + strconv.Itoa(index)
		parent := "vertex_" + strconv.Itoa(index)
		_, _, parentTypeField, childTypeField, err := routeCoveragePhysicalEndpoints(opts.Route[index])
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range []string{
			edge + ".project == @project AND " + edge + ".dataset_generation == @generation",
			edge + ".auth_resource_path IN @auth_paths",
			edge + "." + parentTypeField + " == @parent_type_" + strconv.Itoa(index),
			edge + "." + childTypeField + " == @child_type_" + strconv.Itoa(index),
			parent + ".project == @project AND " + parent + ".dataset_generation == @generation",
			parent + ".resourceType == @parent_type_" + strconv.Itoa(index),
			parent + ".auth_resource_path IN @auth_paths",
		} {
			if !strings.Contains(query, fragment) {
				t.Errorf("membership route query omitted scope/type guard %q: %s", fragment, query)
			}
		}
	}
	if vars["field_path"] != "valueString" || vars["generation"] != "generation-a" || !reflect.DeepEqual(vars["auth_paths"], []string{"scope-a"}) {
		t.Fatalf("membership route binds = %#v", vars)
	}
}

func TestHasRouteValueUsesMembershipOnlyForCompleteCurrentBuild(t *testing.T) {
	base := catalog.RouteCoverageOptions{
		Project: "project-a", DatasetGeneration: "generation-a", AuthResourcePaths: []string{"scope-a"},
		RootResourceType: "Specimen", SourceResourceType: "Observation",
		Route:  []catalog.RouteCoverageStep{{FromResourceType: "Specimen", ToResourceType: "Observation", Relationship: "specimen_observation", StorageDirection: "INBOUND"}},
		Source: catalog.RouteCoverageSource{Kind: catalog.RouteCoverageField, FieldPath: "valueString"},
	}
	for _, test := range []struct {
		name      string
		buildRows []map[string]any
		rows      []map[string]any
		want      bool
		wantIndex bool
	}{
		{name: "complete positive", buildRows: []map[string]any{{"schema_version": int64(catalog.FieldSourceMembershipSchemaVersion), "state": string(catalog.FieldSourceMembershipComplete)}}, rows: []map[string]any{{"matched": true}}, want: true, wantIndex: true},
		{name: "complete negative", buildRows: []map[string]any{{"schema_version": int64(catalog.FieldSourceMembershipSchemaVersion), "state": string(catalog.FieldSourceMembershipComplete)}}, want: false, wantIndex: true},
		{name: "partial marker falls back", buildRows: []map[string]any{{"schema_version": int64(catalog.FieldSourceMembershipSchemaVersion), "state": string(catalog.FieldSourceMembershipBuilding)}}, want: false},
		{name: "old marker version falls back", buildRows: []map[string]any{{"schema_version": int64(catalog.FieldSourceMembershipSchemaVersion - 1), "state": string(catalog.FieldSourceMembershipComplete)}}, want: false},
		{name: "missing marker falls back", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &routeCoverageQueryClient{
				evidenceClient: &evidenceClient{},
				rows:           test.rows,
				buildRows:      test.buildRows,
				indexName:      "idx_membership_scalar_paths",
			}
			adapter, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			got, err := adapter.HasRouteValue(context.Background(), base)
			if err != nil || got != test.want {
				t.Fatalf("HasRouteValue = %t, %v; want %t", got, err, test.want)
			}
			if len(client.queries) != 2 || client.maxRows != 1 {
				t.Fatalf("query count/max rows = %d/%d, want marker plus one bounded existence query", len(client.queries), client.maxRows)
			}
			usedMembership := strings.Contains(client.queries[1], "FOR membership IN fhir_field_source_membership")
			if usedMembership != test.wantIndex {
				t.Fatalf("membership path selected=%v want %v; query=%s", usedMembership, test.wantIndex, client.queries[1])
			}
			if !usedMembership && !strings.Contains(client.queries[1], "FOR edge_0 IN fhir_edge") {
				t.Fatalf("incomplete index did not retain exact edge-first fallback: %s", client.queries[1])
			}
		})
	}
}

func TestEdgeFirstFieldPresenceKeepsFalseZeroAndEmptyStringValues(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		value any
	}{
		{name: "false", path: "active", value: false},
		{name: "zero", path: "count", value: 0},
		{name: "empty string", path: "display", value: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := catalog.RouteCoverageOptions{
				Project: "p", DatasetGeneration: "g", AuthResourcePathsUnrestricted: true,
				RootResourceType: "Specimen", SourceResourceType: "Observation",
				Route:  []catalog.RouteCoverageStep{{FromResourceType: "Specimen", ToResourceType: "Observation", Relationship: "specimen_observation", StorageDirection: "INBOUND"}},
				Source: catalog.RouteCoverageSource{Kind: catalog.RouteCoverageField, FieldPath: test.path},
			}
			query, _, err := routeCoverageExistenceQuery(opts, "")
			if err != nil {
				t.Fatal(err)
			}
			if test.value == nil || !strings.Contains(query, "FILTER value_1 != null") || strings.Contains(query, "FILTER value_1 == true") {
				t.Fatalf("non-null presence query does not retain a %v value: %s", test.value, query)
			}
		})
	}
}

func TestHasRouteValueReturnsFirstMatchAndPreservesScopeAndGeneration(t *testing.T) {
	base := catalog.RouteCoverageOptions{
		Project: "p", DatasetGeneration: "g", BuildID: "build", AuthResourcePaths: []string{"scope-a"},
		RootResourceType: "Specimen", SourceResourceType: "Observation",
		Route:  []catalog.RouteCoverageStep{{FromResourceType: "Specimen", ToResourceType: "Observation", Relationship: "specimen_observation", StorageDirection: "OUTBOUND"}},
		Source: catalog.RouteCoverageSource{Kind: catalog.RouteCoverageSemantic, ConceptID: "concept", BindingID: "binding"},
	}
	for _, test := range []struct {
		name           string
		options        catalog.RouteCoverageOptions
		rows           []map[string]any
		want           bool
		wantGeneration string
		wantPaths      []string
	}{
		{name: "positive", options: base, rows: []map[string]any{{"matched": true}}, want: true, wantGeneration: "g", wantPaths: []string{"scope-a"}},
		{name: "zero", options: base, want: false, wantGeneration: "g", wantPaths: []string{"scope-a"}},
		{name: "wrong generation", options: func() catalog.RouteCoverageOptions { copy := base; copy.DatasetGeneration = "old"; return copy }(), want: false, wantGeneration: "old", wantPaths: []string{"scope-a"}},
		{name: "scope restricted", options: func() catalog.RouteCoverageOptions {
			copy := base
			copy.AuthResourcePaths = []string{"scope-denied"}
			return copy
		}(), want: false, wantGeneration: "g", wantPaths: []string{"scope-denied"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &routeCoverageQueryClient{evidenceClient: &evidenceClient{}, rows: test.rows, indexName: "idx_semantic_exact"}
			adapter, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			got, err := adapter.HasRouteValue(context.Background(), test.options)
			if err != nil || got != test.want {
				t.Fatalf("HasRouteValue = %t, %v; want %t", got, err, test.want)
			}
			if len(client.queries) != 1 || client.maxRows != 1 {
				t.Fatalf("query count/max rows = %d/%d, want one row-bounded query", len(client.queries), client.maxRows)
			}
			if !strings.Contains(client.queries[0], "LIMIT 1") || strings.Contains(client.queries[0], "COLLECT") {
				t.Fatalf("existence query must short-circuit without aggregation: %s", client.queries[0])
			}
			vars := client.vars[0]
			if vars["generation"] != test.wantGeneration || !reflect.DeepEqual(vars["auth_paths"], test.wantPaths) {
				t.Fatalf("generation/auth binds = %#v, want generation %q and paths %#v", vars, test.wantGeneration, test.wantPaths)
			}
		})
	}
}

type routeCoverageQueryClient struct {
	*evidenceClient
	rows           []map[string]any
	buildRows      []map[string]any
	indexName      string
	maxRows        int
	maxRowsByQuery []int
}

func (c *routeCoverageQueryClient) QueryRows(_ context.Context, query string, maxRows int, vars map[string]any, visit arangostore.RowVisitor) error {
	c.maxRows = maxRows
	c.maxRowsByQuery = append(c.maxRowsByQuery, maxRows)
	c.queries = append(c.queries, query)
	copied := make(map[string]any, len(vars))
	for key, value := range vars {
		copied[key] = value
	}
	c.vars = append(c.vars, copied)
	rows := c.rows
	if query == fieldSourceMembershipBuildStatusAQL {
		rows = c.buildRows
	}
	for _, row := range rows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func (c *routeCoverageQueryClient) PersistentIndexName(context.Context, string, []string) (string, error) {
	return c.indexName, nil
}

func TestRouteCoverageExistenceMatchesScalarAgainstConfiguredArango(t *testing.T) {
	optionsJSON := os.Getenv("LOOM_ROUTE_COVERAGE_OPTIONS")
	if optionsJSON == "" {
		t.Skip("set LOOM_ROUTE_COVERAGE_OPTIONS and LOOM_ROUTE_COVERAGE_DATABASE for a bounded live comparison")
	}
	endpoint := strings.TrimSpace(os.Getenv("LOOM_ROUTE_COVERAGE_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_ROUTE_COVERAGE_DATABASE"))
	if endpoint == "" || database == "" {
		t.Fatal("LOOM_ROUTE_COVERAGE_ARANGO_URL and LOOM_ROUTE_COVERAGE_DATABASE are required")
	}
	var options []catalog.RouteCoverageOptions
	if err := json.Unmarshal([]byte(optionsJSON), &options); err != nil {
		t.Fatalf("decode route coverage benchmark options: %v", err)
	}
	if len(options) < 1 || len(options) > 5 {
		t.Fatalf("benchmark requires 1–5 options, got %d", len(options))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	for index, option := range options {
		started := time.Now()
		coverage, measureErr := adapter.MeasureRouteCoverage(ctx, option)
		if measureErr != nil {
			t.Fatalf("scalar count %d: %v", index, measureErr)
		}
		scalarDuration := time.Since(started)
		t.Logf("pair[%d] concept=%s binding=%s scalarCount=%d scalar=%s", index, option.Source.ConceptID, option.Source.BindingID, coverage.RowsWithValue, scalarDuration)
		started = time.Now()
		exists, existenceErr := adapter.HasRouteValue(ctx, option)
		if existenceErr != nil {
			t.Fatalf("existence query %d: %v", index, existenceErr)
		}
		existenceDuration := time.Since(started)
		want := coverage.RowsWithValue > 0
		t.Logf("pair[%d] exists=%t existence=%s", index, exists, existenceDuration)
		if exists != want {
			t.Fatalf("pair %d existence=%t disagrees with scalar rowsWithValue=%d", index, exists, coverage.RowsWithValue)
		}
	}
}

func TestRouteCoverageExistenceMatchesRecordedScalarBaseline(t *testing.T) {
	optionsJSON := os.Getenv("LOOM_ROUTE_COVERAGE_OPTIONS")
	if optionsJSON == "" {
		t.Skip("set LOOM_ROUTE_COVERAGE_OPTIONS and LOOM_ROUTE_COVERAGE_EXPECTED_ROWS for a bounded baseline comparison")
	}
	endpoint := strings.TrimSpace(os.Getenv("LOOM_ROUTE_COVERAGE_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_ROUTE_COVERAGE_DATABASE"))
	if endpoint == "" || database == "" {
		t.Fatal("LOOM_ROUTE_COVERAGE_ARANGO_URL and LOOM_ROUTE_COVERAGE_DATABASE are required")
	}
	expectedRaw := strings.TrimSpace(os.Getenv("LOOM_ROUTE_COVERAGE_EXPECTED_ROWS"))
	expectedRows, err := strconv.ParseInt(expectedRaw, 10, 64)
	if err != nil || expectedRows < 0 {
		t.Fatalf("LOOM_ROUTE_COVERAGE_EXPECTED_ROWS must be a non-negative int64, got %q", expectedRaw)
	}
	var options []catalog.RouteCoverageOptions
	if err := json.Unmarshal([]byte(optionsJSON), &options); err != nil {
		t.Fatalf("decode route coverage benchmark options: %v", err)
	}
	if len(options) != 1 {
		t.Fatalf("recorded scalar baseline comparison requires exactly one option, got %d", len(options))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	want := expectedRows > 0
	durations := make([]time.Duration, 0, 5)
	for sample := 0; sample < 5; sample++ {
		started := time.Now()
		exists, queryErr := adapter.HasRouteValue(ctx, options[0])
		duration := time.Since(started)
		if queryErr != nil {
			t.Fatalf("existence sample %d: %v", sample+1, queryErr)
		}
		t.Logf("sample=%d concept=%s binding=%s recordedScalarCount=%d existence=%t elapsed=%s", sample+1, options[0].Source.ConceptID, options[0].Source.BindingID, expectedRows, exists, duration)
		if exists != want {
			t.Fatalf("sample %d existence=%t disagrees with recorded scalar rowsWithValue=%d", sample+1, exists, expectedRows)
		}
		durations = append(durations, duration)
	}
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	median := durations[len(durations)/2]
	p95 := durations[(95*len(durations)+99)/100-1]
	t.Logf("five warm samples: median=%s p95=%s", median, p95)
}

func TestRouteCoverageExistenceRejectsWrongGenerationAndUnauthorizedScopeAgainstConfiguredArango(t *testing.T) {
	optionsJSON := os.Getenv("LOOM_ROUTE_COVERAGE_OPTIONS")
	if optionsJSON == "" {
		t.Skip("set LOOM_ROUTE_COVERAGE_OPTIONS for a bounded live authorization comparison")
	}
	endpoint := strings.TrimSpace(os.Getenv("LOOM_ROUTE_COVERAGE_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_ROUTE_COVERAGE_DATABASE"))
	if endpoint == "" || database == "" {
		t.Fatal("LOOM_ROUTE_COVERAGE_ARANGO_URL and LOOM_ROUTE_COVERAGE_DATABASE are required")
	}
	var options []catalog.RouteCoverageOptions
	if err := json.Unmarshal([]byte(optionsJSON), &options); err != nil {
		t.Fatalf("decode route coverage benchmark options: %v", err)
	}
	if len(options) != 1 {
		t.Fatalf("authorization comparison requires exactly one positive baseline option, got %d", len(options))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		options catalog.RouteCoverageOptions
	}{
		{name: "wrong generation", options: func() catalog.RouteCoverageOptions {
			copy := options[0]
			copy.DatasetGeneration += "-not-present"
			return copy
		}()},
		{name: "unauthorized scope", options: func() catalog.RouteCoverageOptions {
			copy := options[0]
			copy.AuthResourcePathsUnrestricted = false
			copy.AuthResourcePaths = []string{"__route_coverage_denied_scope__"}
			return copy
		}()},
	}
	for _, test := range cases {
		started := time.Now()
		exists, queryErr := adapter.HasRouteValue(ctx, test.options)
		duration := time.Since(started)
		if queryErr != nil {
			t.Fatalf("%s query: %v", test.name, queryErr)
		}
		t.Logf("%s: exists=%t elapsed=%s", test.name, exists, duration)
		if exists {
			t.Fatalf("%s unexpectedly found a row", test.name)
		}
	}
}
