package ingest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	fhir "github.com/calypr/loom/fhirstructs"

	jsgarango "github.com/bmeg/jsonschemagraph/arango"
	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/bytedance/sonic"
)

type EdgeDocument = jsgarango.EdgeDocument

func TestGenericAndGeneratedParity(t *testing.T) {
	schemaPath := repoPath(t, "..", "iceberg", "schemas", "graph", "graph-fhir.json")
	schema, err := graph.Load(schemaPath)
	if err != nil {
		t.Fatalf("failed to load schema: %v", err)
	}

	hotResources := []string{
		"Condition",
		"DocumentReference",
		"MedicationAdministration",
		"Observation",
		"Group",
		"Specimen",
		"Patient",
	}

	metaDir := repoPath(t, "META")

	for _, resourceType := range hotResources {
		t.Run(resourceType, func(t *testing.T) {
			path := filepath.Join(metaDir, resourceType+".ndjson")
			file, err := os.Open(path)
			if err != nil {
				t.Fatalf("failed to open ndjson for %s: %v", resourceType, err)
			}
			defer file.Close()

			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

			class := schema.GetClass(resourceType)
			if class == nil {
				t.Fatalf("failed to get class schema for %s", resourceType)
			}

			lineCount := 0
			for scanner.Scan() {
				lineCount++
				if lineCount > 200 { // Check first 200 rows of each hot resource
					break
				}

				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}

				raw := []byte(line)

				// 1. Generic Engine Validation & ID & Edge Build
				var genericPayload map[string]any
				genericDecodeErr := sonic.ConfigFastest.Unmarshal(raw, &genericPayload)

				var genericValid bool
				var genericObjectID string
				var genericEdges []any

				if genericDecodeErr == nil {
					if err := class.ValidateFast(genericPayload); err == nil {
						genericValid = true
						genericObjectID, err = graphObjectID(genericPayload, class)
						if err != nil {
							t.Errorf("row %d: generic graphObjectID error: %v", lineCount, err)
							continue
						}
						gripEdges, err := schema.BuildEdgesWithID(resourceType, genericObjectID, genericPayload, nil, true)
						if err != nil {
							t.Errorf("row %d: generic BuildEdgesWithID error: %v", lineCount, err)
							continue
						}
						// Convert to Arango edge documents
						for _, ge := range gripEdges {
							edgeDoc, err := EdgeFromGrip("PARITY_TEST", resourceType, ge)
							if err != nil {
								t.Errorf("row %d: generic EdgeFromGrip error: %v", lineCount, err)
								continue
							}
							genericEdges = append(genericEdges, edgeDoc)
						}
					}
				}

				// 2. Generated Engine Validation & ID & Edge Build
				stageTiming := make(map[string]float64)
				genVertex, genEdges, genErr := loadRowGenerated(resourceType, raw, "PARITY_TEST", stageTiming)
				genValid := genErr == nil

				// 3. Assert Parity
				if genericValid != genValid {
					t.Fatalf("row %d: acceptance mismatch. generic_valid=%t, generated_valid=%t. genErr: %v",
						lineCount, genericValid, genValid, genErr)
				}

				if !genericValid {
					// Both rejected, which is correct parity
					continue
				}

				// Verify Object ID
				if genericObjectID != genVertex.ID {
					t.Fatalf("row %d: Object ID mismatch. generic=%q, generated=%q",
						lineCount, genericObjectID, genVertex.ID)
				}

				// Verify edges content (order might differ, so we compare as sets)
				genericEdgeMap := make(map[string]EdgeDocument)
				for _, e := range genericEdges {
					doc := e.(EdgeDocument)
					genericEdgeMap[doc.Key] = doc
				}

				genEdgeMap := make(map[string]EdgeDocument)
				for _, rawBytes := range genEdges {
					var doc fhir.EdgeDocument
					if err := sonic.Unmarshal(rawBytes, &doc); err != nil {
						t.Fatalf("row %d: failed to unmarshal generated edge: %v", lineCount, err)
					}
					genDoc := EdgeDocument{
						Key:      doc.Key,
						From:     doc.From,
						To:       doc.To,
						Label:    doc.Label,
						Project:  doc.Project,
						FromType: doc.FromType,
						ToType:   doc.ToType,
					}
					genEdgeMap[genDoc.Key] = genDoc
				}

				if len(genericEdgeMap) != len(genEdgeMap) {
					t.Fatalf("row %d: Edge count mismatch (after dedup). generic=%d edges, generated=%d edges",
						lineCount, len(genericEdgeMap), len(genEdgeMap))
				}

				for key, genDoc := range genEdgeMap {
					matching, found := genericEdgeMap[key]
					if !found {
						t.Fatalf("row %d: generated edge key %q not found in generic edges", lineCount, key)
					}

					if !reflect.DeepEqual(matching, genDoc) {
						t.Fatalf("row %d: edge mismatch for key %q.\nGeneric:   %+v\nGenerated: %+v",
							lineCount, key, matching, genDoc)
					}
				}
			}

			if err := scanner.Err(); err != nil {
				t.Fatalf("scanner error: %v", err)
			}
		})
	}
}

// EdgeFromGrip helper wrapper to avoid dependency issues in parity tests
func EdgeFromGrip(project, sourceType string, edge any) (EdgeDocument, error) {
	data, err := json.Marshal(edge)
	if err != nil {
		return EdgeDocument{}, err
	}
	var doc struct {
		ID    string `json:"id"`
		From  string `json:"from"`
		To    string `json:"to"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return EdgeDocument{}, err
	}

	targetType, targetID := "", doc.To
	if strings.Contains(doc.To, "/") {
		parts := strings.Split(doc.To, "/")
		targetType, targetID = parts[0], parts[1]
	} else {
		parts := strings.Split(doc.Label, "_")
		targetType = parts[len(parts)-1]
	}

	sanitize := func(value string) string {
		value = strings.TrimSpace(value)
		if value == "" {
			return "_"
		}
		var sb bytes.Buffer
		for _, r := range value {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
				r == '_' || r == '-' || r == ':' || r == '.' || r == '@' || r == '(' || r == ')' ||
				r == '+' || r == ',' || r == '=' || r == ';' || r == '$' || r == '!' || r == '*' ||
				r == '\'' || r == '%' {
				sb.WriteRune(r)
			} else {
				sb.WriteRune('_')
			}
		}
		return sb.String()
	}

	return EdgeDocument{
		Key:      sanitize(doc.ID),
		From:     sanitize(sourceType) + "/" + sanitize(doc.From),
		To:       sanitize(targetType) + "/" + sanitize(targetID),
		Label:    doc.Label,
		Project:  project,
		FromType: sourceType,
		ToType:   targetType,
	}, nil
}
