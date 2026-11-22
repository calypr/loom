package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/loom/internal/testkit/arangotest"
)

func TestGenerationIdentityKeysMatchArangoAQL(t *testing.T) {
	owned := arangotest.Owned(t, "loom_test_gen_id_")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const project, generation = "identity-project", "identity-generation"
	for _, test := range []struct {
		name          string
		logicalKey    string
		fromID        string
		toID          string
		logicalEdgeID string
	}{
		{name: "FHIR identifiers", logicalKey: "patient-1", fromID: "observation-1", toID: "patient-1", logicalEdgeID: "edge-1"},
		{name: "sanitized identifiers", logicalKey: "patient id", fromID: "observation id", toID: "patient id", logicalEdgeID: "edge id"},
		{name: "slash sanitization", logicalKey: "coll/1", fromID: "observation/1", toID: "patient/1", logicalEdgeID: "edge/1"},
		{name: "allowed punctuation", logicalKey: "a_-.@()+,=;$!*'%:", fromID: "obs-1", toID: "pat-1", logicalEdgeID: "edge-1"},
		{name: "backslash sanitization", logicalKey: `patient\id`, fromID: `obs\id`, toID: `pat\id`, logicalEdgeID: `edge\id`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var result map[string]any
			err := owned.Client.QueryRows(ctx, `
LET sanitized_key = REGEX_REPLACE(@logical_key, "[^A-Za-z0-9_:.@()+,=;$!*%'-]", "_")
LET sanitized_edge_key = REGEX_REPLACE(@edge_key, "[^A-Za-z0-9_:.@()+,=;$!*%'-]", "_")
LET from = CONCAT("Observation/", REGEX_REPLACE(@from_id, "[^A-Za-z0-9_:.@()+,=;$!*%'-]", "_"))
LET to = CONCAT("Patient/", REGEX_REPLACE(@to_id, "[^A-Za-z0-9_:.@()+,=;$!*%'-]", "_"))
LET vertex_input = CONCAT(CONCAT_SEPARATOR(@separator, "vertex", @project, @generation, "Patient", sanitized_key), @separator)
LET edge_input = CONCAT(CONCAT_SEPARATOR(@separator, "edge", @project, @generation, sanitized_edge_key, from, to), @separator)
RETURN {
  vertex: CONCAT("g2", SUBSTRING(SHA256(vertex_input), 0, 32)),
  edge: CONCAT("g2", SUBSTRING(SHA256(edge_input), 0, 32))
		}`, 1, map[string]any{
				"project": project, "generation": generation,
				"logical_key": test.logicalKey, "edge_key": test.logicalEdgeID,
				"from_id": test.fromID, "to_id": test.toID, "separator": "\x00",
			}, func(row map[string]any) error {
				result = row
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			logicalKey := SanitizeKey(test.logicalKey)
			if result["vertex"] != generationDocumentKey(project, generation, "Patient", logicalKey) {
				t.Fatalf("AQL vertex key = %v, Go key = %q", result["vertex"], generationDocumentKey(project, generation, "Patient", logicalKey))
			}
			from := "Observation/" + SanitizeKey(test.fromID)
			to := "Patient/" + SanitizeKey(test.toID)
			edgeKey := SanitizeKey(test.logicalEdgeID)
			if result["edge"] != generationEdgeKey(project, generation, edgeKey, from, to) {
				t.Fatalf("AQL edge key = %v, Go key = %q", result["edge"], generationEdgeKey(project, generation, edgeKey, from, to))
			}
		})
	}
}
