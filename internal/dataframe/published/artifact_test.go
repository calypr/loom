package published

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
			Project:           "study/project",
			DatasetGeneration: "generation-a",
			ReceiptID:         "receipt-a",
			ExecutionID:       "execution-a",
			OutputID:          "patients",
			RevisionID:        "revision-a",
		},
		Columns: []ArtifactColumn{
			{Name: "patient_id", LogicalType: "string", Nullable: false},
			{Name: "note", LogicalType: "string", Nullable: true},
			{Name: "values", LogicalType: "string", Nullable: true, Repeated: true},
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
	if manifest.Version != artifactManifestVersion || manifest.Identity != request.Identity || manifest.Rows != 3 || manifest.Features != 3 || manifest.Null != defaultNullEncoding || manifest.Array != defaultArrayEncoding {
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

func TestWriteArtifactPreservesNullEmptyAndArrayEncoding(t *testing.T) {
	var archive bytes.Buffer
	if _, err := WriteArtifact(context.Background(), &archive, artifactTestRequest(), artifactTestRows); err != nil {
		t.Fatal(err)
	}
	data := readArtifactMembers(t, archive.Bytes())["data.csv"]
	want := "patient_id,note,values\npatient-a,\\N,[]\npatient-b,\"\",\"[\"\"x\"\",\"\"y\"\"]\"\npatient-c,\"\\N\",\"[\"\"x\"\",\"\"y\"\"]\"\n"
	if string(data) != want {
		t.Fatalf("data.csv = %q, want %q", data, want)
	}
	if strings.Contains(string(data), "patient-a,\"\"") {
		t.Fatal("null was encoded as empty string")
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
