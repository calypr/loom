package published

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestArtifactJSONLRoundTripPreservesTypesMembershipAndIdentity(t *testing.T) {
	request := ArtifactRequest{
		Identity: ArtifactIdentity{
			Project: "study/project", DatasetGeneration: "generation-7", ReceiptID: "receipt-7",
			ExecutionID: "execution-7", OutputID: "observations", RevisionID: "revision-7",
			SchemaDigest: "schema-7", OutputContractDigest: "contract-7",
		},
		Descriptor: ArtifactDescriptor{
			Version: 1, OutputKey: "observations", ReceiptFormatVersion: 2,
			CompilerContractVersion: "compiler-v2", RecipeSchemaVersion: 1,
			TranslationVersion: "translation-7", SourceGeneration: "generation-7",
			PublishedSchemaDigest: "schema-7", ResolvedSchemaDigest: "resolved-schema-7",
			OutputContractDigest: "contract-7", RowGrain: "observation",
			RowIdentity: ArtifactRowIdentity{Key: artifactRowIdentityKey, SourceResourceType: "Observation", SourceIDColumn: "observation_id"},
		},
		Format: ArtifactFormatAuto,
		Columns: []ArtifactColumn{
			{Name: "observation_id", OutputKey: "observation", LogicalType: "string", Shape: "scalar"},
			{Name: "count", OutputKey: "count", LogicalType: "integer", Shape: "scalar"},
			{Name: "code_value", OutputKey: "codedValue", LogicalType: "code", Shape: "record", EmissionID: "emit-coded", CandidateID: "candidate-coded", OccurrenceID: "occ-coded", Construction: "VALUE", ReductionPolicy: "FIRST"},
			{Name: "detail", OutputKey: "detail", LogicalType: "json", Shape: "record"},
			{Name: "items", OutputKey: "items", LogicalType: "string", Shape: "array", Repeated: true},
			{Name: "present_null", OutputKey: "presentNull", LogicalType: "string", Shape: "scalar", Nullable: true},
			{Name: "optional", OutputKey: "optional", LogicalType: "string", Shape: "scalar", Nullable: true},
		},
	}
	rows := []map[string]any{
		{
			artifactRowIdentityKey: "row-0001", "observation_id": "obs-1",
			"count":      json.Number("9007199254740993"),
			"code_value": map[string]any{"system": "https://example.test/codes", "code": "A-1", "display": "Alpha"},
			"detail":     map[string]any{"nested": map[string]any{"enabled": true}, "rank": json.Number("3")},
			"items":      []any{"x", json.Number("2"), false}, "present_null": nil,
		},
		{
			artifactRowIdentityKey: map[string]any{"groupRevisionId": "groups-1", "groupId": "group-2"}, "observation_id": "obs-2",
			"count":      uint64(18446744073709551615),
			"code_value": map[string]any{"system": "https://example.test/codes", "code": "B-2", "display": "Beta"},
			"detail":     map[string]any{"nested": map[string]any{"enabled": false}, "rank": json.Number("4")},
			"items":      []any{"y"}, "present_null": "value", "optional": "present",
		},
	}
	var archive bytes.Buffer
	result, err := WriteArtifact(context.Background(), &archive, request, func(visit ArtifactRowVisitor) error {
		for _, row := range rows {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Rows != 2 {
		t.Fatalf("artifact result = %#v", result)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var jsonl, manifestBytes []byte
	for _, file := range reader.File {
		if file.Name != "data.jsonl" && file.Name != "manifest.json" {
			continue
		}
		member, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		value, err := io.ReadAll(member)
		_ = member.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "data.jsonl" {
			jsonl = value
		} else {
			manifestBytes = value
		}
	}
	if len(jsonl) == 0 {
		t.Fatal("AUTO format did not produce data.jsonl for structured columns")
	}
	var manifest struct {
		Version    int                `json:"version"`
		Format     ArtifactFormat     `json:"format"`
		Descriptor ArtifactDescriptor `json:"descriptor"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != artifactManifestVersion || manifest.Format != ArtifactFormatJSONL || manifest.Descriptor.SourceGeneration != "generation-7" || manifest.Descriptor.PublishedSchemaDigest != "schema-7" || manifest.Descriptor.OutputKey != "observations" || manifest.Descriptor.RowIdentity.Key != artifactRowIdentityKey || len(manifest.Descriptor.Columns) != len(request.Columns) {
		t.Fatalf("typed manifest descriptor = %#v", manifest)
	}
	if column := manifest.Descriptor.Columns[2]; column.OutputKey != "codedValue" || column.EmissionID != "emit-coded" || column.OccurrenceID != "occ-coded" || column.ReductionPolicy != "FIRST" {
		t.Fatalf("column construction descriptor = %#v", column)
	}
	decoder := json.NewDecoder(bytes.NewReader(jsonl))
	decoder.UseNumber()
	decoded := make([]struct {
		RowID  any            `json:"rowId"`
		Values map[string]any `json:"values"`
	}, 0, 2)
	for decoder.More() {
		var row struct {
			RowID  any            `json:"rowId"`
			Values map[string]any `json:"values"`
		}
		if err := decoder.Decode(&row); err != nil {
			t.Fatal(err)
		}
		decoded = append(decoded, row)
	}
	if len(decoded) != 2 {
		t.Fatalf("decoded rows = %d, want 2", len(decoded))
	}
	if decoded[0].RowID != "row-0001" || !reflect.DeepEqual(decoded[1].RowID, map[string]any{"groupRevisionId": "groups-1", "groupId": "group-2"}) {
		t.Fatalf("row membership identity = %#v / %#v", decoded[0].RowID, decoded[1].RowID)
	}
	first, second := decoded[0].Values, decoded[1].Values
	if first["count"] != json.Number("9007199254740993") || second["count"] != json.Number("18446744073709551615") {
		t.Fatalf("integer precision lost: first=%#v second=%#v", first["count"], second["count"])
	}
	if value, exists := first["presentNull"]; !exists || value != nil {
		t.Fatalf("explicit null membership = %#v exists=%t", value, exists)
	}
	if _, exists := first["optional"]; exists {
		t.Fatalf("absent value was serialized as present: %#v", first)
	}
	if second["optional"] != "present" {
		t.Fatalf("present optional value = %#v", second["optional"])
	}
	wantCode := map[string]any{"system": "https://example.test/codes", "code": "A-1", "display": "Alpha"}
	if !reflect.DeepEqual(first["codedValue"], wantCode) {
		t.Fatalf("coded identity = %#v", first["codedValue"])
	}
	detail, ok := first["detail"].(map[string]any)
	if !ok || detail["rank"] != json.Number("3") || !reflect.DeepEqual(detail["nested"], map[string]any{"enabled": true}) {
		t.Fatalf("record shape = %#v", first["detail"])
	}
	items, ok := first["items"].([]any)
	if !ok || !reflect.DeepEqual(items, []any{"x", json.Number("2"), false}) {
		t.Fatalf("list type/values = %#v", first["items"])
	}
}

func TestArtifactJSONLNormalizesDriverScalarsToDeclaredTypes(t *testing.T) {
	request := artifactTestRequest()
	request.Format = ArtifactFormatJSONL
	request.Columns = []ArtifactColumn{
		{Name: "id", OutputKey: "id", LogicalType: "string", Shape: "scalar"},
		{Name: "count", OutputKey: "count", LogicalType: "integer", Shape: "scalar"},
		{Name: "ratio", OutputKey: "ratio", LogicalType: "decimal", Shape: "scalar"},
		{Name: "active", OutputKey: "active", LogicalType: "boolean", Shape: "scalar"},
		{Name: "label", OutputKey: "label", LogicalType: "string", Shape: "scalar"},
		{Name: "optional", OutputKey: "optional", LogicalType: "integer", Shape: "scalar", Nullable: true},
	}
	var archive bytes.Buffer
	_, err := WriteArtifact(context.Background(), &archive, request, func(visit ArtifactRowVisitor) error {
		return visit(map[string]any{
			artifactRowIdentityKey: "row-1",
			"id":                   "record-1",
			"count":                "0",
			"ratio":                "-1.25",
			"active":               "false",
			"label":                "0",
			"optional":             nil,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	data := readArtifactMembers(t, archive.Bytes())["data.jsonl"]
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var row struct {
		Values map[string]any `json:"values"`
	}
	if err := decoder.Decode(&row); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"id": "record-1", "count": json.Number("0"), "ratio": json.Number("-1.25"),
		"active": false, "label": "0", "optional": nil,
	}
	if !reflect.DeepEqual(row.Values, want) {
		t.Fatalf("typed JSONL values = %#v, want %#v", row.Values, want)
	}
}

func TestArtifactJSONLRejectsDriverScalarsThatContradictDeclaredTypes(t *testing.T) {
	tests := []struct {
		name        string
		logicalType string
		value       string
	}{
		{name: "integer fraction", logicalType: "integer", value: "1.5"},
		{name: "negative unsigned integer", logicalType: "uint64", value: "-1"},
		{name: "non-finite decimal", logicalType: "decimal", value: "NaN"},
		{name: "non-boolean", logicalType: "boolean", value: "yes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := artifactTestRequest()
			request.Format = ArtifactFormatJSONL
			request.Columns = []ArtifactColumn{
				{Name: "id", OutputKey: "id", LogicalType: "string", Shape: "scalar"},
				{Name: "value", OutputKey: "value", LogicalType: test.logicalType, Shape: "scalar"},
			}
			_, err := WriteArtifact(context.Background(), io.Discard, request, func(visit ArtifactRowVisitor) error {
				return visit(map[string]any{artifactRowIdentityKey: "row-1", "id": "record-1", "value": test.value})
			})
			if err == nil || !strings.Contains(err.Error(), "declared ") {
				t.Fatalf("contradictory %s value error = %v", test.logicalType, err)
			}
		})
	}
}
