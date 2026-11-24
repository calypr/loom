package sourceload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest/identity"
	"github.com/calypr/loom/internal/ingest/source"
	"github.com/calypr/loom/internal/semantic"
)

func sourceValidationFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "BodyStructure.ndjson")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sourceValidationSchema(t *testing.T) *source.RuntimeFHIRSchema {
	t.Helper()
	schema := source.LoadRuntimeFHIRSchema()
	return schema
}

func TestSourceFileValidationRejectsLateInvalidRows(t *testing.T) {
	valid := `{"resourceType":"BodyStructure","id":"fixture","patient":{"reference":"Patient/p1"},"includedStructure":[{"structure":{"text":"body"}}]}` + "\n"
	body := strings.Repeat(valid, 30) + "\n" + `{"resourceType":"BodyStructure","id":"bad","patient":{"reference":"Patient/p1"},"private":"do-not-disclose"}` + "\n"
	digest, rows, rejected, err := validateSourceLoadFile(context.Background(), sourceValidationFile(t, body), "META/BodyStructure.ndjson", "fixture", "", "candidate", sourceValidationSchema(t))
	if err != nil {
		t.Fatal(err)
	}
	if digest != "" || rows != 31 || rejected == nil || rejected.TotalErrors != 1 || len(rejected.Issues) != 1 {
		t.Fatalf("validation = %q %d %#v", digest, rows, rejected)
	}
	issue := rejected.Issues[0]
	if issue.Line != 32 || issue.File != "BodyStructure.ndjson" || !strings.Contains(issue.Message, "includedStructure") || strings.Contains(issue.Message, "do-not-disclose") {
		t.Fatalf("unsafe or unhelpful issue: %#v", issue)
	}
}

func TestSourceFileValidationBoundsIssuesAndCountsAllFailures(t *testing.T) {
	body := strings.Repeat(`{"resourceType":"BodyStructure","id":"bad"}`+"\n", 125)
	_, rows, rejected, err := validateSourceLoadFile(context.Background(), sourceValidationFile(t, body), "META/BodyStructure.ndjson", "fixture", "", "candidate", sourceValidationSchema(t))
	if err != nil {
		t.Fatal(err)
	}
	if rows != 125 || rejected == nil || rejected.TotalErrors != 125 || len(rejected.Issues) != 100 || !rejected.Truncated {
		t.Fatalf("validation = %d %#v", rows, rejected)
	}
}

func TestSourceFileValidationHashesExactBytesAndAcceptsEmptyReplacement(t *testing.T) {
	for _, body := range []string{"", "\n", `{"resourceType":"BodyStructure","id":"fixture","patient":{"reference":"Patient/p1"},"includedStructure":[{"structure":{"text":"body"}}]}` + "\r\n\n"} {
		t.Run(strconv.Itoa(len(body)), func(t *testing.T) {
			digest, rows, rejected, err := validateSourceLoadFile(context.Background(), sourceValidationFile(t, body), "META/BodyStructure.ndjson", "fixture", "", "candidate", sourceValidationSchema(t))
			if err != nil || rejected != nil {
				t.Fatalf("validation = %#v %v", rejected, err)
			}
			expected := sha256.Sum256([]byte(body))
			if digest != hex.EncodeToString(expected[:]) {
				t.Fatalf("hash %s doesn't bind exact bytes", digest)
			}
			expectedRows := int64(0)
			if len(body) > 1 {
				expectedRows = 1
			}
			if rows != expectedRows {
				t.Fatalf("rows=%d want%d", rows, expectedRows)
			}
		})
	}
}

func TestSourceFileValidationChecksEveryResourceType(t *testing.T) {
	_, _, rejected, err := validateSourceLoadFile(context.Background(), sourceValidationFile(t, `{"resourceType":"Patient","id":"p1"}`), "META/BodyStructure.ndjson", "fixture", "", "candidate", sourceValidationSchema(t))
	if err != nil || rejected == nil || rejected.TotalErrors != 1 {
		t.Fatalf("mismatched resource type = %#v %v", rejected, err)
	}
}

func TestCopySemanticInventoryEntryRekeysAndRespectsSourceVersion(t *testing.T) {
	project, sourceGeneration, targetGeneration := "project", "source", "target"
	buildID := semantic.BuildID(project, sourceGeneration)
	document := map[string]any{
		"_key": "old-key", "project": project, "dataset_generation": sourceGeneration, "build_id": buildID,
		"source_kind": semantic.SourceFile, "auth_resource_path": "scope", "resource_type": "Observation",
		"binding_id": "binding", "concept_id": "concept", "observation": map[string]any{"population": 4},
	}
	transform := copySemanticInventoryEntryDocument(project, targetGeneration, []string{"Patient"}, true)
	encoded, err := transform(sourceCopyRow{Document: document})
	if err != nil {
		t.Fatal(err)
	}
	var copied map[string]any
	if err := json.Unmarshal(encoded, &copied); err != nil {
		t.Fatal(err)
	}
	targetBuild := semantic.BuildID(project, targetGeneration)
	wantKey := semantic.EntryKey(project, targetGeneration, targetBuild, semantic.SourceFile, "scope", "binding", "concept")
	if copied["_key"] != wantKey || copied["_key"] == "old-key" || copied["dataset_generation"] != targetGeneration || copied["build_id"] != targetBuild {
		t.Fatalf("copied entry identity = %#v", copied)
	}
	if omitted, err := copySemanticInventoryEntryDocument(project, targetGeneration, nil, false)(sourceCopyRow{Document: map[string]any{
		"project": project, "resource_type": "Observation", "binding_id": "binding", "concept_id": "concept", "source_kind": semantic.SourceFile,
	}}); err != nil || omitted != nil {
		t.Fatalf("stale file entry copy = %s, %v, want omitted", omitted, err)
	}
}

func TestSourceCopyPreservesTheIdentityFormatOfAnInProgressManifest(t *testing.T) {
	for _, format := range []struct {
		name  string
		value string
	}{
		{name: "legacy omitted manifest field", value: ""},
		{name: "explicit legacy", value: publication.IdentityFormatLegacy},
		{name: "compact", value: publication.IdentityFormatCompact},
	} {
		t.Run(format.name, func(t *testing.T) {
			doc := map[string]any{
				"_key": "old-physical-key", "logical_key": "patient-1", "project": "project-a",
				"payload": map[string]any{"id": "patient-1"},
			}
			copied, err := copyVertexDocument("Patient", "project-a", "generation-a", format.value)(sourceCopyRow{Document: doc})
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Key string `json:"_key"`
			}
			if err := json.Unmarshal(copied, &decoded); err != nil {
				t.Fatal(err)
			}
			wantFormat := format.value
			if wantFormat == "" {
				wantFormat = publication.IdentityFormatLegacy
			}
			want := identity.GenerationDocumentKeyWithFormat("project-a", "generation-a", "Patient", "patient-1", wantFormat)
			if got := decoded.Key; got != want {
				t.Fatalf("copied _key = %q, want %q", got, want)
			}
		})
	}
}

func TestSourceSchemaChangeRequiresFullReload(t *testing.T) {
	current := publication.SchemaSnapshot{
		SchemaSHA256: "current", GeneratedResourceTypes: []string{"Observation", "Patient"},
	}
	previous := publication.SchemaSnapshot{SchemaSHA256: "previous"}
	if copySource, err := sourceSchemaAllowsCopy(current, current, []string{"Patient"}); err != nil || !copySource {
		t.Fatalf("same schema partial update copy = %t, err = %v", copySource, err)
	}
	if copySource, err := sourceSchemaAllowsCopy(previous, current, []string{"Patient"}); err == nil || copySource {
		t.Fatalf("different schema partial update copy = %t, err = %v", copySource, err)
	}
	if copySource, err := sourceSchemaAllowsCopy(previous, current, []string{"Patient", "Observation"}); err != nil || copySource {
		t.Fatalf("different schema full update copy = %t, err = %v", copySource, err)
	}
}
