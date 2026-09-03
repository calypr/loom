package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalWorkspaceWriterIsOptInAndAtomic(t *testing.T) {
	if writer, err := localWorkspaceWriter("", ""); err != nil || writer != nil {
		t.Fatalf("disabled writer should be nil; error = %v", err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "workspace.json")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer, err := localWorkspaceWriter(path, "HTAN_INT/BForePC")
	if err != nil {
		t.Fatal(err)
	}
	if err := writer(context.Background(), "HTAN_INT-BForePC", "default", []byte(`{"kind":"workspace"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{\"kind\":\"workspace\"}\n" {
		t.Fatalf("workspace = %q", got)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".loom-workspace-*.json"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files = %#v, %v", matches, err)
	}
}

func TestLocalWorkspaceWriterRequiresExactAbsoluteJSONPath(t *testing.T) {
	for _, path := range []string{"relative.json", filepath.Join(t.TempDir(), "workspace.txt"), filepath.Join(t.TempDir(), "missing", "workspace.json")} {
		if _, err := localWorkspaceWriter(path, "project-a"); err == nil {
			t.Fatalf("localWorkspaceWriter(%q) unexpectedly succeeded", path)
		}
	}
}

func TestLocalWorkspaceWriterRejectsAnotherProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	writer, err := localWorkspaceWriter(path, "HTAN_INT/BForePC")
	if err != nil {
		t.Fatal(err)
	}
	if err := writer(context.Background(), "HTAN_INT/Other", "default", []byte(`{}`)); err == nil {
		t.Fatal("another project updated the configured workspace")
	}
}
