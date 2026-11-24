package config

import "testing"

func TestProjectConfigDigestTreatsNilAndEmptyColumnsEquivalently(t *testing.T) {
	config := ProjectConfig{
		ID: "documents", Title: "Documents", RecipeRef: ProjectConfigRecipeRef{Name: "metadata"},
		Views: []ConfigView{{ID: "documents", Output: "Documents", Table: ConfigTable{Columns: []ConfigColumn{}}}},
	}
	first, err := config.Digest()
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	config.Views[0].Table.Columns = nil
	second, err := config.Digest()
	if err != nil {
		t.Fatalf("Digest() for nil columns error = %v", err)
	}
	if first != second {
		t.Fatalf("nil and empty columns changed digest: %q != %q", first, second)
	}
}

func TestProjectConfigRejectsUnsupportedChartType(t *testing.T) {
	config := ProjectConfig{
		ID: "documents", Title: "Documents", RecipeRef: ProjectConfigRecipeRef{Name: "metadata"},
		Views: []ConfigView{{
			ID: "documents", Output: "Documents",
			Charts: []ConfigChart{{Column: "status", Type: "scatter"}},
		}},
	}
	const want = `views[0].charts[0].type "scatter" is unsupported`
	if err := config.Validate(); err == nil || err.Error() != want {
		t.Fatalf("Validate() error = %v, want %q", err, want)
	}
}
