package recipe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func TestDocumentedRecipeExamplesValidate(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "../../../docs/DATAFRAMER_RECIPES.md"))
	if err != nil {
		t.Fatal(err)
	}

	blocks := regexp.MustCompile("(?s)```json\\n(.*?)\\n```").FindAllSubmatch(data, -1)
	recipes := 0
	for index, block := range blocks {
		if !json.Valid(block[1]) {
			t.Fatalf("JSON block %d is invalid", index+1)
		}
		var header struct {
			RecipeSchemaVersion int `json:"recipeSchemaVersion"`
		}
		if err := json.Unmarshal(block[1], &header); err != nil {
			t.Fatal(err)
		}
		if header.RecipeSchemaVersion == 0 {
			continue
		}
		recipes++
		if _, err := Parse(block[1]); err != nil {
			t.Fatalf("recipe block %d: %v", index+1, err)
		}
	}
	if recipes != 2 {
		t.Fatalf("documented recipe count = %d, want 2", recipes)
	}
}
