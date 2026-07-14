package recipecompile

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileSelectorSubset(t *testing.T) {
	bundle, err := recipe.Parse([]byte(`{"recipeSchemaVersion":1,"name":"x","translationVersion":"1","outputs":[{"name":"patients","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"select":"root.id"}},{"name":"code","expr":{"call":"coalesce","args":[{"select":"root.identifier[].value"},{"select":"root.id"}]}}],"traversals":[{"name":"subject_Patient","toResourceType":"Condition","alias":"condition","fields":[{"name":"condition_code","expr":{"call":"first","args":[{"select":"condition.code.text"}]}}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileBundle(bundle, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 1 || compiled[0].Builder.Fields[0].Select != "id" || compiled[0].Builder.Fields[1].FallbackSelects[0] != "id" {
		t.Fatalf("unexpected builder: %#v", compiled)
	}
	if compiled[0].Builder.Traversals[0].Alias != "condition" {
		t.Fatalf("traversal alias lost: %#v", compiled[0].Builder.Traversals)
	}
}

func TestCompileRejectsRichOperatorsExplicitly(t *testing.T) {
	bundle, err := recipe.Parse([]byte(`{"recipeSchemaVersion":1,"name":"x","translationVersion":"1","outputs":[{"name":"x","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"call":"reference_id","args":[{"select":"root.id"}]}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompileBundle(bundle, recipe.RuntimeBindings{Project: "p"}); err == nil {
		t.Fatal("expected unsupported rich expression")
	}
}
