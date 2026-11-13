package recipeimport

import (
	"encoding/json"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"reflect"
	"testing"
)

func TestDefaultSourceParameterCoverageAndAtomicEdits(t *testing.T) {
	bundle := defaultSourcePlanFixture(t)
	for _, out := range bundle.Outputs {
		t.Run(out.Name, func(t *testing.T) {
			before, _ := json.Marshal(out)
			plan := SourcePlan(out)
			for _, step := range plan.Steps {
				if step.Parameters == nil {
					t.Fatalf("missing settings for %s", step.ID)
				}
				if err := step.Parameters.Validate(""); err != nil {
					t.Fatalf("%s: %v", step.ID, err)
				}
			}
			applied, err := ApplySourcePlan(out, &plan)
			if err != nil {
				t.Fatal(err)
			}
			// Freezing occurrence identities is existing import behavior; settings must
			// not change any other pinned syntax on a no-op hydration.
			legacy := cloneSourcePlan(plan)
			for i := range legacy.Steps {
				legacy.Steps[i].Parameters = nil
			}
			want, err := ApplySourcePlan(out, &legacy)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(applied, want) {
				t.Fatal("hydrated effective parameters changed source semantics")
			}
			after, _ := json.Marshal(out)
			if string(before) != string(after) {
				t.Fatal("source parameter projection mutated pin")
			}
		})
	}
	cases := []struct {
		name, output, step string
		mutate             func(*authoringv2.ImportedSourceParameters)
		check              func(recipe.Output) bool
	}{
		{"output", "ResearchSubject", "output", func(p *authoringv2.ImportedSourceParameters) { p.Output.CollisionPolicy = "error" }, func(o recipe.Output) bool { return o.CollisionPolicy == "error" }},
		{"field", "ResearchSubject", "fields", func(p *authoringv2.ImportedSourceParameters) {
			p.FieldGroup.Fields[1].Name = "subject_identifier"
			p.FieldGroup.Fields[1].Expr.Args[0].Select = "root.identifier[].system"
		}, func(o recipe.Output) bool {
			return o.Fields[1].Name == "subject_identifier" && o.Fields[1].Expr.Args[0].Select == "root.identifier[].system"
		}},
		{"catalog", "ResearchSubject", "catalogProjections[0]", func(p *authoringv2.ImportedSourceParameters) {
			p.Catalog.IncludePaths = []string{"status"}
			p.Catalog.Naming = recipe.ColumnNamingPathSuffix
		}, func(o recipe.Output) bool {
			return reflect.DeepEqual(o.CatalogProjections[0].IncludePaths, []string{"status"}) && o.CatalogProjections[0].Naming == recipe.ColumnNamingPathSuffix
		}},
		{"dynamic", "DocumentReference", "dynamicColumns[0]", func(p *authoringv2.ImportedSourceParameters) {
			p.Dynamic.Key = &recipe.Expression{Select: "item.system"}
			prefix := "ident_"
			p.Dynamic.ColumnPrefix = &prefix
		}, func(o recipe.Output) bool {
			return o.DynamicColumns[0].Key.Select == "item.system" && *o.DynamicColumns[0].ColumnPrefix == "ident_"
		}},
		{"extension", "DocumentReference", "extensionColumns[1]", func(p *authoringv2.ImportedSourceParameters) { p.Extension.Source.Select = "root.extension[]" }, func(o recipe.Output) bool { return o.ExtensionColumns[1].Source.Select == "root.extension[]" }},
		{"pivot", "ResearchSubject", "traversals[0].traversals[0].pivots[0]", func(p *authoringv2.ImportedSourceParameters) { p.Pivot.Discovery.Path = "component[].code" }, func(o recipe.Output) bool {
			return o.Traversals[0].Traversals[0].Pivots[0].Discovery.Path == "component[].code"
		}},
		{"traversal", "ResearchSubject", "traversals[0]", func(p *authoringv2.ImportedSourceParameters) { p.Traversal.Alias = "person" }, func(o recipe.Output) bool { return o.Traversals[0].Alias == "person" }},
		{"expansion", "GroupMember", "expand", func(p *authoringv2.ImportedSourceParameters) {
			p.Expansion.EmptyPolicy = recipe.ExpansionPreserveParent
		}, func(o recipe.Output) bool { return o.Expand.EmptyPolicy == recipe.ExpansionPreserveParent }},
		{"identity", "GroupMember", "identity", func(p *authoringv2.ImportedSourceParameters) {
			p.Identity.Expr.Args[0].Literal = json.RawMessage(`"project-namespace"`)
		}, func(o recipe.Output) bool { return string(o.Identity.Expr.Args[0].Literal) == `"project-namespace"` }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := outputNamed(t, bundle, c.output)
			before, _ := json.Marshal(out)
			plan := SourcePlan(out)
			for i := range plan.Steps {
				if plan.Steps[i].ID == c.step {
					c.mutate(plan.Steps[i].Parameters)
				}
			}
			got, err := ApplySourcePlan(out, &plan)
			if err != nil {
				t.Fatal(err)
			}
			if !c.check(got) {
				t.Fatal("parameter edit did not update intended operation")
			}
			after, _ := json.Marshal(out)
			if string(before) != string(after) {
				t.Fatal("edit mutated pin")
			}
			again, err := HydrateSourcePlan(out, &plan)
			if err != nil {
				t.Fatal(err)
			}
			got2, err := ApplySourcePlan(out, &again)
			if err != nil || !reflect.DeepEqual(got, got2) {
				t.Fatalf("overlay reload lost exact settings: %v", err)
			}
		})
	}
}
func TestSourceParametersRetainLegacyPathsAndRejectFieldRetargeting(t *testing.T) {
	out := outputNamed(t, defaultSourcePlanFixture(t), "ResearchSubject")
	p := SourcePlan(out)
	// A genuine pre-extension plan omitted output settings and parameters.
	p.Steps = p.Steps[1:]
	for i := range p.Steps {
		p.Steps[i].Parameters = nil
	}
	hydrated, err := HydrateSourcePlan(out, &p)
	if err != nil {
		t.Fatal(err)
	}
	if hydrated.Steps[0].ID != "output" || hydrated.Steps[1].ID != "fields" {
		t.Fatal("legacy source identities were renumbered")
	}
	for i := range hydrated.Steps {
		if hydrated.Steps[i].ID == "fields" {
			f := hydrated.Steps[i].Parameters.FieldGroup.Fields
			f[0], f[1] = f[1], f[0]
		}
	}
	if _, err := ApplySourcePlan(out, &hydrated); err == nil {
		t.Fatal("field reorder changed intended occurrence without rejection")
	}
}

func TestRenamedSourceFamilyUsesEffectiveNameWithoutChangingOriginalStepID(t *testing.T) {
	plan := SourcePlan(outputNamed(t, defaultSourcePlanFixture(t), "DocumentReference"))
	var step authoringv2.ImportedSourceStep
	for _, s := range plan.Steps {
		if s.ID == "dynamicColumns[0]" {
			step = s
		}
	}
	if step.Parameters == nil || step.Parameters.Dynamic == nil {
		t.Fatal("fixture missing original source family")
	}
	originalID, originalKey := step.ID, step.SourceKey
	step.Parameters.Dynamic.Name = "project_identifier_keys"
	if effectiveSourceKey(step) != "project_identifier_keys" || step.ID != originalID || step.SourceKey != originalKey {
		t.Fatal("mutable family name changed original operation identity")
	}
}
