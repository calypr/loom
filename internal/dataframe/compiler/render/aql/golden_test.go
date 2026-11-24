package aql

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func assertGroupRenderGolden(t *testing.T, query string, binds map[string]any) {
	t.Helper()
	actual, err := json.MarshalIndent(struct {
		Query string         `json:"query"`
		Binds map[string]any `json:"binds"`
	}{query, binds}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual = append(actual, '\n')
	path := filepath.Join("testdata", t.Name()+".json")
	if os.Getenv("LOOM_UPDATE_RENDER_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, actual, 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatalf("rendered query/binds differ from %s; inspect the change before updating with LOOM_UPDATE_RENDER_GOLDENS=1\nactual:\n%s", path, actual)
	}
}
