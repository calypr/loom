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
