package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func sourceValidationFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "BodyStructure.ndjson")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSourceFileValidationRejectsLateInvalidRows(t *testing.T) {
	valid := `{"resourceType":"BodyStructure","id":"fixture","patient":{"reference":"Patient/p1"},"includedStructure":[{"structure":{"text":"body"}}]}` + "\n"
	body := strings.Repeat(valid, 30) + "\n" + `{"resourceType":"BodyStructure","id":"bad","patient":{"reference":"Patient/p1"},"private":"do-not-disclose"}` + "\n"
	digest, rows, rejected, err := validateSourceLoadFile(context.Background(), sourceValidationFile(t, body), "META/BodyStructure.ndjson", "fixture", "", "candidate", "../../schemas/graph-fhir.json")
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
	_, rows, rejected, err := validateSourceLoadFile(context.Background(), sourceValidationFile(t, body), "META/BodyStructure.ndjson", "fixture", "", "candidate", "../../schemas/graph-fhir.json")
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
			digest, rows, rejected, err := validateSourceLoadFile(context.Background(), sourceValidationFile(t, body), "META/BodyStructure.ndjson", "fixture", "", "candidate", "../../schemas/graph-fhir.json")
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
	_, _, rejected, err := validateSourceLoadFile(context.Background(), sourceValidationFile(t, `{"resourceType":"Patient","id":"p1"}`), "META/BodyStructure.ndjson", "fixture", "", "candidate", "../../schemas/graph-fhir.json")
	if err != nil || rejected == nil || rejected.TotalErrors != 1 {
		t.Fatalf("mismatched resource type = %#v %v", rejected, err)
	}
}
