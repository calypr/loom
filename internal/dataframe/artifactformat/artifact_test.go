package artifactformat

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func artifactTestRequest() ArtifactRequest {
	return ArtifactRequest{
		Identity: ArtifactIdentity{
			Project:              "study/project",
			DatasetGeneration:    "generation-a",
			ReceiptID:            "receipt-a",
			ExecutionID:          "execution-a",
			OutputID:             "patients",
			RevisionID:           "revision-a",
			SchemaDigest:         "schema-a",
			OutputContractDigest: "contract-a",
		},
		Descriptor: ArtifactDescriptor{
			Version: 1, OutputKey: "patients", ReceiptFormatVersion: 2,
			CompilerContractVersion: "compiler-v2", RecipeSchemaVersion: 1,
			TranslationVersion: "translation-v1", SourceGeneration: "generation-a",
			PublishedSchemaDigest: "schema-a", ResolvedSchemaDigest: "resolved-schema-a",
			OutputContractDigest: "contract-a", RowGrain: "patient",
			RowIdentity: ArtifactRowIdentity{Key: artifactRowIdentityKey, SourceResourceType: "Patient", SourceIDColumn: "patient_id"},
		},
		Columns: []ArtifactColumn{
			{Name: "patient_id", OutputKey: "patient_id", LogicalType: "string", Shape: "scalar", Nullable: false},
			{Name: "note", OutputKey: "note", LogicalType: "string", Shape: "scalar", Nullable: true},
		},
		SelectionMetadata:      json.RawMessage(`{"digest":"selection-a","members":2}`),
		InterpretationMetadata: json.RawMessage(`{"revisionIds":["interpretation-a"]}`),
		Provenance:             json.RawMessage(`{"source":"fixture"}`),
		Quality:                json.RawMessage(`{"status":"COMPLETE","rows":2}`),
	}
}

func artifactTestRows(visitor ArtifactRowVisitor) error {
	rows := []map[string]any{
		{"patient_id": "patient-a", "note": nil, "values": []string{}},
		{"patient_id": "patient-b", "note": "", "values": []string{"x", "y"}},
		{"patient_id": "patient-c", "note": `\N`, "values": []string{"x", "y"}},
	}
	for _, row := range rows {
		if err := visitor(row); err != nil {
			return err
		}
	}
	return nil
}

func readArtifactMembers(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	result := make(map[string][]byte, len(archive.File))
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		value, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", file.Name, readErr)
		}
		result[file.Name] = value
	}
	return result
}

func TestWriteArtifactMembersChecksumsAndIdentity(t *testing.T) {
	request := artifactTestRequest()
	var archive bytes.Buffer
	result, err := WriteArtifact(context.Background(), &archive, request, artifactTestRows)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Rows != 3 || result.ArchiveBytes != int64(archive.Len()) {
		t.Fatalf("result = %#v, archive bytes = %d", result, archive.Len())
	}
	if got := hex.EncodeToString(sumBytes(archive.Bytes())); got != result.ArchiveSHA256 {
		t.Fatalf("archive digest = %s, result = %s", got, result.ArchiveSHA256)
	}
	members := readArtifactMembers(t, archive.Bytes())
	wantNames := []string{"README.md", "data.csv", "manifest.json", "provenance.json", "quality.json", "schema.json"}
	gotNames := make([]string, 0, len(members))
	for name := range members {
		gotNames = append(gotNames, name)
	}
	sort.Strings(gotNames)
	sort.Strings(wantNames)
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("members = %#v, want %#v", gotNames, wantNames)
	}

	var manifest struct {
		Version         int              `json:"version"`
		Identity        ArtifactIdentity `json:"identity"`
		Selection       json.RawMessage  `json:"selection"`
		Interpretations json.RawMessage  `json:"interpretations"`
		Rows            int64            `json:"rows"`
		Features        int              `json:"features"`
		Null            string           `json:"nullEncoding"`
		Array           string           `json:"arrayEncoding"`
		Members         []ArtifactMember `json:"members"`
	}
	if err := json.Unmarshal(members["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != artifactManifestVersion || manifest.Identity != request.Identity || manifest.Rows != 3 || manifest.Features != 2 || manifest.Null != defaultNullEncoding || manifest.Array != defaultArrayEncoding {
		t.Fatalf("manifest = %#v", manifest)
	}
	if string(manifest.Selection) != `{"digest":"selection-a","members":2}` || string(manifest.Interpretations) != `{"revisionIds":["interpretation-a"]}` {
		t.Fatalf("manifest metadata selection=%s interpretations=%s", manifest.Selection, manifest.Interpretations)
	}
	if len(manifest.Members) != len(wantNames)-1 {
		t.Fatalf("manifest members = %#v", manifest.Members)
	}
	for _, member := range manifest.Members {
		value, ok := members[member.Name]
		if !ok || member.Name == "manifest.json" {
			t.Fatalf("manifest references missing/unsafe member %#v", member)
		}
		if member.Bytes != int64(len(value)) || member.SHA256 != hex.EncodeToString(sumBytes(value)) {
			t.Fatalf("member %s = %#v, bytes=%d digest=%s", member.Name, member, len(value), hex.EncodeToString(sumBytes(value)))
		}
	}
	if strings.Contains(string(members["manifest.json"]), "manifest.json") {
		t.Fatalf("manifest must not self-reference: %s", members["manifest.json"])
	}
}

func TestWriteArtifactPreservesScalarNullEmptyAndLiteralNullMarker(t *testing.T) {
	var archive bytes.Buffer
	if _, err := WriteArtifact(context.Background(), &archive, artifactTestRequest(), artifactTestRows); err != nil {
		t.Fatal(err)
	}
	data := readArtifactMembers(t, archive.Bytes())["data.csv"]
	want := "patient_id,note\npatient-a,\\N\npatient-b,\"\"\npatient-c,\"\\N\"\n"
	if string(data) != want {
		t.Fatalf("data.csv = %q, want %q", data, want)
	}
	if strings.Contains(string(data), "patient-a,\"\"") {
		t.Fatal("null was encoded as empty string")
	}
}

func TestWriteArtifactRejectsLossyCSVShape(t *testing.T) {
	request := artifactTestRequest()
	request.Format = ArtifactFormatCSV
	request.Columns = append(request.Columns, ArtifactColumn{Name: "items", OutputKey: "items", LogicalType: "string", Shape: "array", Repeated: true})
	_, err := WriteArtifact(context.Background(), io.Discard, request, artifactTestRows)
	if err == nil || !strings.Contains(err.Error(), "use JSONL") {
		t.Fatalf("lossy CSV error = %v", err)
	}
}

func TestWriteArtifactIsDeterministic(t *testing.T) {
	request := artifactTestRequest()
	var first, second bytes.Buffer
	if _, err := WriteArtifact(context.Background(), &first, request, artifactTestRows); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteArtifact(context.Background(), &second, request, artifactTestRows); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("same artifact inputs produced different archive bytes")
	}
}

func TestWriteArtifactEnforcesRowAndByteLimits(t *testing.T) {
	request := artifactTestRequest()
	request.MaxRows = 2
	var rowsLimited bytes.Buffer
	result, err := WriteArtifact(context.Background(), &rowsLimited, request, artifactTestRows)
	if !errors.Is(err, ErrArtifactRowLimit) || result.Complete {
		t.Fatalf("row limit result=%#v err=%v", result, err)
	}

	var baseline bytes.Buffer
	if _, err := WriteArtifact(context.Background(), &baseline, artifactTestRequest(), artifactTestRows); err != nil {
		t.Fatal(err)
	}
	request = artifactTestRequest()
	request.MaxBytes = int64(baseline.Len() - 1)
	var bytesLimited bytes.Buffer
	result, err = WriteArtifact(context.Background(), &bytesLimited, request, artifactTestRows)
	if !errors.Is(err, ErrArtifactByteLimit) || result.Complete {
		t.Fatalf("byte limit result=%#v err=%v", result, err)
	}
}

func TestWriteArtifactCancellationAndLateFailureNeverComplete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var canceled bytes.Buffer
	_, err := WriteArtifact(ctx, &canceled, artifactTestRequest(), func(visitor ArtifactRowVisitor) error {
		if err := visitor(map[string]any{"patient_id": "patient-a", "note": "first", "values": []string{"x"}}); err != nil {
			return err
		}
		cancel()
		return visitor(map[string]any{"patient_id": "patient-b", "note": "second", "values": []string{"y"}})
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}

	var lateFailure bytes.Buffer
	result, err := WriteArtifact(context.Background(), &lateFailure, artifactTestRequest(), func(visitor ArtifactRowVisitor) error {
		if err := visitor(map[string]any{"patient_id": "patient-a", "note": "first", "values": []string{"x"}}); err != nil {
			return err
		}
		return fmt.Errorf("later page failed")
	})
	if err == nil || result.Complete || result.ArchiveSHA256 != "" {
		t.Fatalf("late failure result=%#v err=%v", result, err)
	}
}

func sumBytes(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}

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
			{Name: "code_value", OutputKey: "codedValue", Label: "Coded value", LogicalType: "code", Shape: "record", EmissionID: "emit-coded", CandidateID: "candidate-coded", OccurrenceID: "occ-coded", Construction: "VALUE", ReductionPolicy: "FIRST", AuthoredColumns: []string{"code"}},
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
	if column := manifest.Descriptor.Columns[2]; column.OutputKey != "codedValue" || column.Label != "Coded value" || column.EmissionID != "emit-coded" || column.OccurrenceID != "occ-coded" || column.ReductionPolicy != "FIRST" || !reflect.DeepEqual(column.AuthoredColumns, []string{"code"}) {
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
