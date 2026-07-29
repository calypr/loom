package recipe

import "testing"

func TestFragmentExpansionIsHygienicAndDigestable(t *testing.T) {
	lib := FragmentLibrary{Fragments: map[string]Fragment{"reference_id": {Name: "reference_id", Version: "1", Params: []string{"value"}, Expr: Expression{Call: "path_segment", Args: []Expression{{Select: "$value"}}}}}}
	input := Expression{Call: "fragment:reference_id", Args: []Expression{{Select: "root.subject.reference"}}}
	expanded, err := lib.ExpandExpression(input)
	if err != nil {
		t.Fatal(err)
	}
	if expanded.Call != "path_segment" || expanded.Args[0].Select != "root.subject.reference" {
		t.Fatalf("unexpected expansion: %#v", expanded)
	}
	if _, err := lib.Digest(); err != nil {
		t.Fatal(err)
	}
}

func TestFragmentCycleRejected(t *testing.T) {
	lib := FragmentLibrary{Fragments: map[string]Fragment{"a": {Name: "a", Version: "1", Expr: Expression{Call: "fragment:b"}}, "b": {Name: "b", Version: "1", Expr: Expression{Call: "fragment:a"}}}}
	if _, err := lib.ExpandExpression(Expression{Call: "fragment:a"}); err == nil {
		t.Fatal("expected fragment cycle")
	}
}

func TestBundleExpandFragmentsProducesStandaloneDigestInput(t *testing.T) {
	bundle := Bundle{RecipeSchemaVersion: 1, Name: "fragments", TranslationVersion: "v", Fragments: &FragmentLibrary{Fragments: map[string]Fragment{
		"id": {Name: "id", Version: "1", Params: []string{"ctx"}, Expr: Expression{Select: "$ctx.id"}},
	}}, Outputs: []Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []Field{{Name: "id", Expr: Expression{Call: "fragment:id", Args: []Expression{{Select: "root"}}}}}}}}
	expanded, err := bundle.ExpandFragments()
	if err != nil {
		t.Fatal(err)
	}
	if expanded.Fragments != nil || expanded.Outputs[0].Fields[0].Expr.Select != "root.id" {
		t.Fatalf("bundle was not standalone: %#v", expanded)
	}
}
