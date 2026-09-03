package repositoryseed

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const workspace = `{"apiVersion":"loom.calypr.org/explorer-authoring/v2","kind":"ExplorerBuilderWorkspace","explorer":{"title":"Patients"},"documents":[{"kind":"ExplorerBuilderDocument","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[{"column":"patient_id","label":"Patient ID","logicalType":"string","occurrenceId":"base","source":{"kind":"field","fieldPath":"id","projectionMode":"VALUE"},"table":{"visible":true,"order":0}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`

func TestPrepareReportsEveryPointerBeforeParsing(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "META/Patient.ndjson", pointer("a"))
	mustWrite(t, root, "META/Specimen.ndjson", pointer("b"))
	mustWrite(t, root, "CONFIG/HTAN_INT-BForePC.json", pointer("c"))
	_, err := Prepare(Options{Repository: root})
	var unresolved *UnmaterializedError
	if !errors.As(err, &unresolved) {
		t.Fatalf("Prepare error = %v, want UnmaterializedError", err)
	}
	want := []string{"META/Patient.ndjson", "META/Specimen.ndjson", "CONFIG/HTAN_INT-BForePC.json"}
	if !reflect.DeepEqual(unresolved.Paths, want) {
		t.Fatalf("pointer paths = %#v, want %#v", unresolved.Paths, want)
	}
	if !strings.Contains(err.Error(), "does not download repository objects") {
		t.Fatalf("error omitted materialization instruction: %v", err)
	}
}

func TestPrepareValidatesAndSeparatesDataIdentity(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "META/Patient.ndjson", `{"resourceType":"Patient","id":"p1"}`+"\n")
	mustWrite(t, root, "CONFIG/HTAN_INT-BForePC.json", workspace)
	first, err := Prepare(Options{Repository: root})
	if err != nil {
		t.Fatal(err)
	}
	if first.Project != "HTAN_INT/BForePC" {
		t.Fatalf("project = %q", first.Project)
	}
	if !strings.HasPrefix(first.Generation, "repo-") || len(first.Generation) != len("repo-")+24 {
		t.Fatalf("generation = %q", first.Generation)
	}
	mustWrite(t, root, "CONFIG/HTAN_INT-BForePC.json", " \n"+workspace+"\n")
	formatted, err := Prepare(Options{Repository: root})
	if err != nil {
		t.Fatal(err)
	}
	if formatted.ConfigDigest != first.ConfigDigest || formatted.SourceCommit != first.SourceCommit {
		t.Fatalf("formatting changed workspace identity: %#v != %#v", formatted, first)
	}
	mustWrite(t, root, "CONFIG/HTAN_INT-BForePC.json", strings.Replace(workspace, "Patients", "People", 1))
	second, err := Prepare(Options{Repository: root})
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != first.Generation {
		t.Fatalf("CONFIG change changed data generation: %s != %s", second.Generation, first.Generation)
	}
	if second.ConfigDigest == first.ConfigDigest {
		t.Fatal("CONFIG change did not change config digest")
	}
}

func TestPrepareRejectsLegacyAndMalformedInputs(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "META/Patient.ndjson", `{"resourceType":"Patient"}`+"\n")
	mustWrite(t, root, "CONFIG/HTAN_INT-BForePC.json", workspace)
	if _, err := Prepare(Options{Repository: root}); err == nil || !strings.Contains(err.Error(), "resourceType and id") {
		t.Fatalf("malformed META error = %v", err)
	}
	mustWrite(t, root, "META/Patient.ndjson", `{"resourceType":"Patient","id":"p1"}`+"\n")
	mustWrite(t, root, "CONFIG/HTAN_INT-BForePC.json", `{"explorerConfig":[]}`)
	if _, err := Prepare(Options{Repository: root}); err == nil || !strings.Contains(err.Error(), "not a valid native Loom V2 workspace") {
		t.Fatalf("legacy CONFIG error = %v", err)
	}
}

func TestPrepareRequiresConfigSelectionWhenAmbiguous(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "META/Patient.ndjson", `{"resourceType":"Patient","id":"p1"}`+"\n")
	mustWrite(t, root, "CONFIG/a.json", workspace)
	mustWrite(t, root, "CONFIG/b.json", workspace)
	if _, err := Prepare(Options{Repository: root}); err == nil || !strings.Contains(err.Error(), "select one with --config") {
		t.Fatalf("ambiguous CONFIG error = %v", err)
	}
}

func mustWrite(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func pointer(id string) string {
	return "version https://git-lfs.github.com/spec/v1\noid sha256:" + strings.Repeat(id, 64) + "\nsize 123\n"
}
