package arango

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestProfileCorpusAgainstArango is opt-in because it reads a provisioned
// Arango/META database. The queries are deliberately generic physical shapes,
// not compiler or GDC fixtures: each protects a class of AQL operation that a
// FHIR dataframe compiler must be able to emit for any schema route.
func TestProfileCorpusAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run Arango profile corpus")
	}
	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project := os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}

	client, err := Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())

	corpus := profileCorpus(project)
	for _, shape := range corpus {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			explain, err := client.Explain(ctx, ExplainRequest{Query: shape.query, BindVars: shape.bindVars})
			if err != nil {
				t.Fatalf("EXPLAIN %s: %v\nAQL:\n%s", shape.name, err, shape.query)
			}
			if explain.Plan == nil && len(explain.Plans) == 0 {
				t.Fatalf("EXPLAIN %s returned no plan", shape.name)
			}
			assessment := AssessExplainResult(explain)
			t.Logf("%s explain: plans=%d full_scans=%d indexes=%d", shape.name, len(assessment.Plans), len(assessment.FullCollectionScans), len(assessment.Indexes))
			if shape.name == "root" && !assessmentHasIndexField(assessment, "Patient", "project") {
				t.Fatalf("root profile corpus did not select a project index: %#v", assessment.Indexes)
			}
			if shape.name != "root" && !assessmentHasIndexCollection(assessment, "fhir_edge") {
				t.Fatalf("%s profile corpus did not report an edge index: %#v", shape.name, assessment.Indexes)
			}

			profile, err := client.Profile(ctx, ProfileRequest{
				Query:     shape.query,
				BindVars:  shape.bindVars,
				BatchSize: 1000,
				Count:     true,
				Options:   ProfileOptions{Profile: 2},
			})
			if err != nil {
				t.Fatalf("PROFILE %s: %v\nAQL:\n%s", shape.name, err, shape.query)
			}
			if len(profile.Extra.Stats.Nodes) == 0 {
				t.Fatalf("PROFILE %s returned no execution-node statistics", shape.name)
			}
			summary := SummarizeProfile(profile)
			t.Logf("%s profile: rows=%d runtime=%0.6fs scanned_full=%d scanned_index=%d top=%s", shape.name, len(profile.Result), summary.RuntimeSeconds, summary.ScannedFull, summary.ScannedIndex, profileTopNode(summary))
		})
	}
}

// TestCompoundEdgeIndexExplainAgainstArango audits the endpoint-first
// persistent indexes without mutating the database. Equality filters should
// select the complete inbound/outbound compound index. Multi-type filters are
// recorded as an observation because Arango may choose the edge index for one
// direction when the IN predicate is less selective.
func TestCompoundEdgeIndexExplainAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run Arango index audit")
	}
	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project := os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}
	client, err := Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())
	cases := []struct {
		name          string
		query         string
		bindVars      map[string]any
		expectedIndex []string
	}{
		{
			name: "inbound-equality",
			query: `FOR edge IN fhir_edge
  FILTER edge._to == @root
  FILTER edge.project == @project
  FILTER edge.dataset_generation == @generation
  FILTER edge.label == @label
  FILTER edge.from_type == @from_type
  RETURN edge._key`,
			bindVars:      map[string]any{"root": "Patient/nope", "project": project, "generation": nil, "label": "subject_Patient", "from_type": "Specimen"},
			expectedIndex: []string{"_to", "project", "dataset_generation", "label", "from_type"},
		},
		{
			name: "outbound-equality",
			query: `FOR edge IN fhir_edge
  FILTER edge._from == @root
  FILTER edge.project == @project
  FILTER edge.dataset_generation == @generation
  FILTER edge.label == @label
  FILTER edge.to_type == @to_type
  RETURN edge._key`,
			bindVars:      map[string]any{"root": "ResearchSubject/nope", "project": project, "generation": nil, "label": "study", "to_type": "ResearchStudy"},
			expectedIndex: []string{"_from", "project", "dataset_generation", "label", "to_type"},
		},
		{
			name: "inbound-multi-type",
			query: `FOR edge IN fhir_edge
  FILTER edge._to == @root
  FILTER edge.project == @project
  FILTER edge.dataset_generation == @generation
  FILTER edge.label == @label
  FILTER edge.from_type IN @from_types
  RETURN edge._key`,
			bindVars: map[string]any{"root": "Patient/nope", "project": project, "generation": nil, "label": "subject_Patient", "from_types": []string{"Condition", "Specimen", "Observation"}},
		},
		{
			name: "outbound-multi-type",
			query: `FOR edge IN fhir_edge
  FILTER edge._from == @root
  FILTER edge.project == @project
  FILTER edge.dataset_generation == @generation
  FILTER edge.label == @label
  FILTER edge.to_type IN @to_types
  RETURN edge._key`,
			bindVars: map[string]any{"root": "ResearchSubject/nope", "project": project, "generation": nil, "label": "study", "to_types": []string{"ResearchStudy", "DocumentReference"}},
		},
	}
	for _, shape := range cases {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			explain, err := client.Explain(ctx, ExplainRequest{Query: shape.query, BindVars: shape.bindVars})
			if err != nil {
				t.Fatal(err)
			}
			assessment := AssessExplainResult(explain)
			if len(assessment.FullCollectionScans) != 0 {
				t.Fatalf("compound edge shape used full scan: %#v", assessment.FullCollectionScans)
			}
			if len(assessment.Indexes) == 0 {
				t.Fatalf("compound edge shape selected no index: %#v", assessment)
			}
			if len(shape.expectedIndex) != 0 && !assessmentHasExactIndex(assessment, "fhir_edge", shape.expectedIndex) {
				t.Fatalf("equality shape did not select endpoint-first compound index %v: %#v", shape.expectedIndex, assessment.Indexes)
			}
			t.Logf("indexes=%#v plans=%#v optimizer_rules=%#v", assessment.Indexes, assessment.Plans, assessment.AppliedOptimizerRules)
		})
	}
}

// TestReceiptPreviewExplainAgainstArango is an opt-in contract smoke test for
// the canonical query shapes emitted from immutable compilation receipts. It
// deliberately asserts only that Arango can produce a plan (and records index
// observations); physical index choice can vary with the provisioned corpus
// and optimizer version.
func TestReceiptPreviewExplainAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run receipt preview EXPLAIN fixtures")
	}
	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project := os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}
	client, err := Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())
	for _, shape := range receiptPreviewCorpus(project) {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := client.Explain(ctx, ExplainRequest{Query: shape.query, BindVars: shape.bindVars})
			if err != nil {
				t.Fatalf("EXPLAIN %s: %v\nAQL:\n%s", shape.name, err, shape.query)
			}
			assessment := AssessExplainResult(result)
			if result.Plan == nil && len(result.Plans) == 0 {
				t.Fatalf("EXPLAIN %s returned no plan", shape.name)
			}
			t.Logf("receipt preview shape=%s indexes=%#v full_scans=%#v", shape.name, assessment.Indexes, assessment.FullCollectionScans)
		})
	}
}

func receiptPreviewCorpus(project string) []profileCorpusShape {
	return []profileCorpusShape{
		{
			name: "project-generation-sort-limit",
			query: `FOR root IN Patient
  FILTER root.project == @project
  FILTER root.dataset_generation == @generation
  SORT root._key ASC
  LIMIT @limit
  RETURN {"_key": root._key}`,
			bindVars: map[string]any{"project": project, "generation": "generation-a", "limit": 5},
		},
		{
			name: "restricted-scope-sort-limit",
			query: `FOR root IN Patient
  FILTER root.project == @project
  FILTER root.dataset_generation == @generation
  FILTER root.auth_resource_path IN @auth_paths
  SORT root._key ASC
  LIMIT @limit
  RETURN {"_key": root._key}`,
			bindVars: map[string]any{"project": project, "generation": "generation-a", "auth_paths": []string{"Patient/one", "Patient/two"}, "limit": 5},
		},
		{
			name: "relationship-inbound-direction",
			query: `FOR root IN Patient
  FILTER root.project == @project
  FILTER root.dataset_generation == @generation
  SORT root._key ASC
  LIMIT @limit
  LET related = (FOR node, edge IN 1..1 INBOUND root fhir_edge
    FILTER edge._to == root._id
    FILTER edge.project == @project AND edge.dataset_generation == @generation
    FILTER edge.label == @label
    RETURN node._key)
  RETURN {"_key": root._key, "related": related}`,
			bindVars: map[string]any{"project": project, "generation": "generation-a", "label": "subject_Patient", "limit": 5},
		},
		{
			name: "relationship-outbound-direction",
			query: `FOR root IN ResearchSubject
  FILTER root.project == @project
  FILTER root.dataset_generation == @generation
  SORT root._key ASC
  LIMIT @limit
  LET related = (FOR node, edge IN 1..1 OUTBOUND root fhir_edge
    FILTER edge._from == root._id
    FILTER edge.project == @project AND edge.dataset_generation == @generation
    FILTER edge.label == @label
    RETURN node._key)
  RETURN {"_key": root._key, "related": related}`,
			bindVars: map[string]any{"project": project, "generation": "generation-a", "label": "study", "limit": 5},
		},
	}
}

func assessmentHasIndexCollection(assessment ExplainAssessment, collection string) bool {
	for _, index := range assessment.Indexes {
		if index.Collection == collection {
			return true
		}
	}
	return false
}

func assessmentHasIndexField(assessment ExplainAssessment, collection, field string) bool {
	for _, index := range assessment.Indexes {
		if index.Collection != collection {
			continue
		}
		for _, candidate := range index.Fields {
			if candidate == field {
				return true
			}
		}
	}
	return false
}

func assessmentHasExactIndex(assessment ExplainAssessment, collection string, fields []string) bool {
	for _, index := range assessment.Indexes {
		if index.Collection == collection && strings.Join(index.Fields, "\x00") == strings.Join(fields, "\x00") {
			return true
		}
	}
	return false
}

type profileCorpusShape struct {
	name     string
	query    string
	bindVars map[string]any
}

func profileCorpus(project string) []profileCorpusShape {
	bind := func(root string) map[string]any {
		return map[string]any{
			"@root_collection":                 root,
			"project":                          project,
			"dataset_generation":               "",
			"auth_resource_paths_unrestricted": true,
			"auth_resource_paths":              []string{},
			"limit":                            5,
			"label":                            "subject_Patient",
		}
	}
	root := `FOR root IN @@root_collection
  FILTER root.project == @project
  FILTER @dataset_generation == "" OR root.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR root.auth_resource_path IN @auth_resource_paths
  SORT root._key
  LIMIT @limit
  RETURN {"_key": root._key}`
	sibling := `FOR root IN @@root_collection
  FILTER root.project == @project
  SORT root._key
  LIMIT @limit
  LET conditions = (FOR node, edge IN 1..1 INBOUND root fhir_edge
    FILTER edge.project == @project AND node.project == @project
    FILTER edge.label == @label AND node.resourceType == "Condition"
    RETURN node._key)
  LET specimens = (FOR node, edge IN 1..1 INBOUND root fhir_edge
    FILTER edge.project == @project AND node.project == @project
    FILTER edge.label == @label AND node.resourceType == "Specimen"
    RETURN node._key)
  LET observations = (FOR node, edge IN 1..1 INBOUND root fhir_edge
    FILTER edge.project == @project AND node.project == @project
    FILTER edge.label == @label AND node.resourceType == "Observation"
    RETURN node._key)
  RETURN {"_key": root._key, "conditions": conditions, "specimens": specimens, "observations": observations}`
	deep := `FOR root IN @@root_collection
  FILTER root.project == @project
  SORT root._key
  LIMIT @limit
  LET descendants = (FOR first, firstEdge IN 1..1 INBOUND root fhir_edge
    FILTER firstEdge.project == @project AND first.project == @project
    FOR second, secondEdge IN 1..1 INBOUND first fhir_edge
      FILTER secondEdge.project == @project AND second.project == @project
      RETURN second._key)
  RETURN {"_key": root._key, "descendants": UNIQUE(descendants)}`
	required := `FOR root IN @@root_collection
  FILTER root.project == @project
  FILTER LENGTH(FOR node, edge IN 1..1 INBOUND root fhir_edge
    FILTER edge.project == @project AND node.project == @project
    FILTER edge.label == @label
    LIMIT 1
    RETURN 1) > 0
  SORT root._key
  LIMIT @limit
  RETURN {"_key": root._key}`
	pivot := `FOR root IN @@root_collection
  FILTER root.project == @project
  SORT root._key
  LIMIT @limit
  LET coding = root.payload.code.coding ? root.payload.code.coding : []
  LET pivot = (FOR value IN coding
    COLLECT key = value.system INTO grouped
    RETURN {"key": key, "value": FIRST(grouped).value.code})
  RETURN {"_key": root._key, "pivot": pivot}`
	return []profileCorpusShape{
		{name: "root", query: root, bindVars: bind("Patient")},
		{name: "sibling", query: sibling, bindVars: bind("Patient")},
		{name: "deep", query: deep, bindVars: bind("Patient")},
		{name: "required", query: required, bindVars: bind("Patient")},
		{name: "pivot", query: pivot, bindVars: bind("Observation")},
	}
}

func profileTopNode(summary ProfileSummary) string {
	if len(summary.Nodes) == 0 {
		return "none"
	}
	node := summary.Nodes[0]
	return strings.Join([]string{node.Type, "id=" + strconv.FormatInt(node.ID, 10), "runtime=" + strconv.FormatFloat(node.Runtime, 'f', 6, 64)}, " ")
}
