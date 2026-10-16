package semantic

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRecipePlanStoresRootProjectionOrderOnlyOnRootNode(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: 1, Name: "canonical-fields", TranslationVersion: "test",
		Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{
			{Name: "id", Expr: recipe.Expression{Select: "id"}},
			{Name: "gender", Expr: recipe.Expression{Select: "gender"}},
		}}},
	}
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	output := plan.Outputs[0]
	if got := len(output.Root.Fields); got != 2 || output.Root.Fields[0].Name != "id" || output.Root.Fields[1].Name != "gender" {
		t.Fatalf("root fields = %#v, want authored order", output.Root.Fields)
	}
	typ := reflect.TypeOf(output)
	if _, ok := typ.FieldByName("Fields"); ok {
		t.Fatal("OutputPlan retains duplicate root Fields storage")
	}
	if _, ok := typ.FieldByName("DeclaredOrder"); ok {
		t.Fatal("OutputPlan retains duplicate declared order storage")
	}
}

func TestRecipePlanCarriesPersistenceNeutralDerivedColumns(t *testing.T) {
	integer := int64(3)
	definition := recipe.DerivedColumn{
		ConstructionID: "calc_double", Name: "double_value", Label: "Double value", Operation: recipe.DerivedMultiply,
		Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "value"},
		Right:              recipe.DerivedOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &integer}},
		MissingInputPolicy: recipe.MissingInputPropagateNull,
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: 1, Name: "derived-columns", TranslationVersion: "test",
		Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", DerivedColumns: []recipe.DerivedColumn{definition}}},
	}
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Outputs[0].DerivedColumns; len(got) != 1 || !reflect.DeepEqual(got[0], definition) {
		t.Fatalf("semantic derived columns = %#v, want %#v", got, definition)
	}
}

func TestRecipePlanPinsExplicitGroupRowsToGroupIdentity(t *testing.T) {
	output := recipe.Output{
		Name: "GroupedPatients", RootResourceType: "Patient", RowGrain: "groups",
		GroupRows: &recipe.GroupRows{RevisionID: "grouprev_a", UnassignedMemberPolicy: "EXCLUDE"},
	}
	plan, err := BuildRecipePlan(recipe.Bundle{RecipeSchemaVersion: 1, Name: "groups", TranslationVersion: "test", Outputs: []recipe.Output{output}}, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	groupRows := plan.Outputs[0].GroupRows
	if groupRows == nil || groupRows.RevisionID != "grouprev_a" || groupRows.UnassignedMemberPolicy != "EXCLUDE" {
		t.Fatalf("group rows = %#v", groupRows)
	}
	output.GroupRows = nil
	if _, err := BuildRecipePlan(recipe.Bundle{RecipeSchemaVersion: 1, Name: "groups", TranslationVersion: "test", Outputs: []recipe.Output{output}}, recipe.RuntimeBindings{Project: "p"}); err == nil || !strings.Contains(err.Error(), "pinned group revision") {
		t.Fatalf("missing group revision error = %v", err)
	}
}

func TestSemanticRowExpansionRejectsInvalidSourceAndBindings(t *testing.T) {
	base := SemanticRowExpansion{
		Owner:       SemanticOccurrence{OccurrenceID: "root-1", Alias: "root", ResourceType: "Patient"},
		Source:      SemanticExpression{Type: expression.Type{Kind: expression.KindString, Cardinality: expression.OptionalOne}},
		ItemBinding: "item", EmptyPolicy: ExpansionExclude,
	}
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "repeated") {
		t.Fatalf("expected repeated-source validation error, got %v", err)
	}
	base.Source.Type.Cardinality = expression.Many
	base.ItemBinding = "item.value"
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "safe logical name") {
		t.Fatalf("expected safe-binding validation error, got %v", err)
	}
}

func TestSemanticRowExpansionPoliciesAndOrdinalityAreExplicit(t *testing.T) {
	for _, policy := range []ExpansionEmptyPolicy{ExpansionError, ExpansionExclude, ExpansionPreserveParent} {
		expansion := SemanticRowExpansion{
			Owner:       SemanticOccurrence{OccurrenceID: "root-1", Alias: "root", ResourceType: "Patient"},
			Source:      SemanticExpression{Type: expression.Type{Kind: expression.KindObject, Cardinality: expression.Many}},
			ItemBinding: "item", Ordinality: "item_index", EmptyPolicy: policy,
		}
		if err := expansion.Validate(); err != nil {
			t.Fatalf("policy %q: %v", policy, err)
		}
		if expansion.EmptyPolicy != policy || expansion.Ordinality != "item_index" {
			t.Fatalf("row expansion policy/ordinality changed: %#v", expansion)
		}
	}
	expansion := SemanticRowExpansion{
		Owner:       SemanticOccurrence{OccurrenceID: "root-1", Alias: "root", ResourceType: "Patient"},
		Source:      SemanticExpression{Type: expression.Type{Kind: expression.KindObject, Cardinality: expression.Many}},
		ItemBinding: "item", Ordinality: "item", EmptyPolicy: ExpansionExclude,
	}
	if err := expansion.Validate(); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatalf("expected ordinality collision error, got %v", err)
	}
}

func TestDynamicItemExpressionsUseItemScope(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic-item", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "x", RootResourceType: "Patient", RowGrain: "patient", DynamicColumns: []recipe.DynamicColumn{{Name: "extension", Source: recipe.Expression{Select: "root.extension[]"}, Key: &recipe.Expression{Select: "item.url"}, Value: &recipe.Expression{Select: "item.url"}, Columns: []string{"http://example.org/code"}}}}}}
	if _, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"}); err != nil {
		t.Fatalf("dynamic item expressions were not scoped: %v", err)
	}
}

func TestRecipeRejectsUndefinedAndShadowedAliases(t *testing.T) {
	base := `{"recipeSchemaVersion":1,"name":"x","translationVersion":"1","outputs":[{"name":"x","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"select":"missing.id"}}]}]}`
	bundle, err := recipe.Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"}); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected undefined context error, got %v", err)
	}

	shadowed := `{"recipeSchemaVersion":1,"name":"x","translationVersion":"1","outputs":[{"name":"x","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"select":"root.id"}}],"traversals":[{"name":"subject","alias":"root","toResourceType":"Patient"}]}]}`
	bundle, err = recipe.Parse([]byte(shadowed))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"}); err == nil || !strings.Contains(err.Error(), "shadows") {
		t.Fatalf("expected shadowing error, got %v", err)
	}
}

func TestRecipeRejectsRepeatedIdentity(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "x", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "x", RootResourceType: "Patient", RowGrain: "expanded", Expand: &recipe.Expansion{From: recipe.Expression{Select: "identifier[]"}, As: "item"}, Identity: &recipe.Identity{Name: "id", Expr: recipe.Expression{Select: "root.identifier[].value"}}}}}
	if _, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"}); err == nil || !strings.Contains(err.Error(), "scalar") {
		t.Fatalf("expected scalar identity error, got %v", err)
	}
}

func TestRootOccurrenceExpansionAndTypedIdentity(t *testing.T) {
	bundle, err := recipe.Parse([]byte(`{"recipeSchemaVersion":1,"name":"root-expansion","translationVersion":"1","outputs":[{"name":"x","rootResourceType":"Patient","rootOccurrenceId":"patient-root","rowGrain":"expanded","expand":{"ownerOccurrenceId":"patient-root","from":{"select":"root.identifier[]"},"as":"item","ordinality":"position","emptyPolicy":"PRESERVE_PARENT"},"identity":{"name":"row","expansion":{}}}]} `))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	output := plan.Outputs[0]
	if output.Root.OccurrenceID != "patient-root" || output.RowExpansion == nil {
		t.Fatalf("root expansion was not bound to its occurrence: %#v", output)
	}
	if got := output.RowExpansion.Owner; got != (SemanticOccurrence{OccurrenceID: "patient-root", Alias: "root", ResourceType: "Patient"}) {
		t.Fatalf("row expansion owner = %#v", got)
	}
	if output.RowExpansion.ItemBinding != "item" || output.RowExpansion.Ordinality != "position" || output.RowExpansion.EmptyPolicy != ExpansionPreserveParent {
		t.Fatalf("row expansion details = %#v", output.RowExpansion)
	}
	if !output.ExpansionIdentity || output.Identity != nil {
		t.Fatalf("expansion identity was not represented as a distinct alternative: %#v", output)
	}
}

func TestRootExpansionRejectsStaleOwnerOccurrence(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "stale-owner", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "x", RootResourceType: "Patient", RootOccurrenceID: "current-root", RowGrain: "expanded", Expand: &recipe.Expansion{OwnerOccurrenceID: "stale-root", From: recipe.Expression{Select: "identifier[]"}, As: "item"}, Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}}}}}
	if _, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"}); err == nil || !strings.Contains(err.Error(), "does not exist in the output route") {
		t.Fatalf("expected stale owner occurrence rejection, got %v", err)
	}
}

func TestDeepExpansionSelectsExactSameTypedOccurrence(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: 1, Name: "deep-expansion", TranslationVersion: "1",
		Outputs: []recipe.Output{{
			Name: "x", RootResourceType: "Patient", RootOccurrenceID: "root-patient", RowGrain: "expanded",
			Expand:   &recipe.Expansion{OwnerOccurrenceID: "guardian-patient", From: recipe.Expression{Select: "guardian.identifier[]"}, As: "item", EmptyPolicy: recipe.ExpansionExclude},
			Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
			Traversals: []recipe.Traversal{{
				Name: "subject", OccurrenceID: "subject-patient", ToResourceType: "Patient",
				Traversals: []recipe.Traversal{{Name: "guardian", OccurrenceID: "guardian-patient", ToResourceType: "Patient"}},
			}},
		}},
	}
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	output := plan.Outputs[0]
	if got := output.RowExpansion.Owner; got != (SemanticOccurrence{OccurrenceID: "guardian-patient", Alias: "guardian", ResourceType: "Patient"}) {
		t.Fatalf("expansion owner = %#v", got)
	}
	if output.RowExpansion.Source.Context != "guardian" {
		t.Fatalf("expansion source context = %q, want exact owner alias", output.RowExpansion.Source.Context)
	}
	if output.Root.Children[0].OccurrenceID != "subject-patient" || output.Root.Children[0].Children[0].OccurrenceID != "guardian-patient" {
		t.Fatalf("semantic route lost authored occurrence IDs: %#v", output.Root.Children)
	}
}

func TestExpansionRejectsAmbiguousMissingAndOutOfScopeOwnerIntent(t *testing.T) {
	base := recipe.Bundle{
		RecipeSchemaVersion: 1, Name: "invalid-owner", TranslationVersion: "1",
		Outputs: []recipe.Output{{
			Name: "x", RootResourceType: "Patient", RootOccurrenceID: "root-patient", RowGrain: "expanded",
			Expand:   &recipe.Expansion{OwnerOccurrenceID: "guardian-patient", From: recipe.Expression{Select: "guardian.identifier[]"}, As: "item"},
			Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
			Traversals: []recipe.Traversal{{
				Name: "subject", OccurrenceID: "subject-patient", ToResourceType: "Patient",
				Traversals: []recipe.Traversal{{Name: "guardian", OccurrenceID: "guardian-patient", ToResourceType: "Patient"}},
			}},
		}},
	}
	tests := []struct {
		name   string
		mutate func(*recipe.Output)
		want   string
	}{
		{name: "stale occurrence", mutate: func(output *recipe.Output) { output.Expand.OwnerOccurrenceID = "stale" }, want: "does not exist"},
		{name: "scalar source", mutate: func(output *recipe.Output) { output.Expand.From = recipe.Expression{Select: "guardian.gender"} }, want: "repeated selector"},
		{name: "source outside owner", mutate: func(output *recipe.Output) { output.Expand.From = recipe.Expression{Select: "root.identifier[]"} }, want: "does not match owner occurrence"},
		{name: "missing route occurrence", mutate: func(output *recipe.Output) { output.RootOccurrenceID = "" }, want: "missing an occurrence ID"},
		{name: "duplicate occurrence", mutate: func(output *recipe.Output) {
			output.Traversals = append(output.Traversals, recipe.Traversal{Name: "other", OccurrenceID: "guardian-patient", ToResourceType: "Patient"})
		}, want: "ambiguous"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := base
			bundle.Outputs = append([]recipe.Output(nil), base.Outputs...)
			output := bundle.Outputs[0]
			expansion := *base.Outputs[0].Expand
			output.Expand = &expansion
			output.Traversals = append([]recipe.Traversal(nil), base.Outputs[0].Traversals...)
			test.mutate(&output)
			bundle.Outputs[0] = output
			if _, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildRecipePlan() error = %v, want %q", err, test.want)
			}
		})
	}
}
