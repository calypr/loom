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
			p.Catalog.Outputs = []authoringv2.ImportedCatalogOutput{{FieldPath: "status", Enabled: true, Name: "study_status"}}
		}, func(o recipe.Output) bool {
			projection := o.CatalogProjections[0]
			return reflect.DeepEqual(projection.IncludePaths, []string{"status"}) && projection.Naming == recipe.ColumnNamingPathSuffix && reflect.DeepEqual(projection.OutputOverrides, []recipe.CatalogProjectionOutputOverride{{FieldPath: "status", Enabled: true, Name: "study_status"}})
		}},
		{"dynamic", "DocumentReference", "dynamicColumns[0]", func(p *authoringv2.ImportedSourceParameters) {
			p.Dynamic.Key = &recipe.Expression{Select: "item.system"}
			prefix := "ident_"
			p.Dynamic.ColumnPrefix = &prefix
			p.Dynamic.Outputs = []authoringv2.ImportedDynamicOutput{{Key: "urn:disabled", Enabled: false}}
		}, func(o recipe.Output) bool {
			return o.DynamicColumns[0].Key.Select == "item.system" && *o.DynamicColumns[0].ColumnPrefix == "ident_" && reflect.DeepEqual(o.DynamicColumns[0].OutputOverrides, []recipe.DynamicColumnOutputOverride{{Key: "urn:disabled", Enabled: false}})
		}},
		{"extension", "DocumentReference", "extensionColumns[1]", func(p *authoringv2.ImportedSourceParameters) {
			p.Extension.Source.Select = "root.extension[]"
			p.Extension.Outputs = []authoringv2.ImportedExtensionOutput{{Key: recipe.ExtensionColumnOutputKey(recipe.ExtensionColumnMapping{URL: "https://example.org/value", URLPath: []string{"https://example.org/parent"}, SourcePath: "extension[].extension[]"}), Enabled: false}}
		}, func(o recipe.Output) bool {
			return o.ExtensionColumns[1].Source.Select == "root.extension[]" && len(o.ExtensionColumns[1].OutputOverrides) == 1 && !o.ExtensionColumns[1].OutputOverrides[0].Enabled
		}},
		{"pivot", "ResearchSubject", "traversals[0].traversals[0].pivots[0]", func(p *authoringv2.ImportedSourceParameters) {
			p.Pivot.Discovery.Path = "component[].code"
			p.Pivot.Outputs = []authoringv2.ImportedPivotOutput{{Key: "http://loinc.org|8480-6", Enabled: false}}
		}, func(o recipe.Output) bool {
			pivot := o.Traversals[0].Traversals[0].Pivots[0]
			return pivot.Discovery.Path == "component[].code" && reflect.DeepEqual(pivot.OutputOverrides, []recipe.PivotOutputOverride{{Key: "http://loinc.org|8480-6", Enabled: false}})
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

func TestCatalogOutputOverrideDisableReloadAndReenableByFieldPath(t *testing.T) {
	output := recipe.Output{
		Name: "Patient", RootResourceType: "Patient", RootOccurrenceID: "base", RowGrain: "resource",
		CatalogProjections: []recipe.CatalogProjection{{Name: "populated_fields", IncludePaths: []string{"*"}, MaxColumns: 4}},
	}
	plan := SourcePlan(output)
	if err := updateSourcePlanStep(&plan, "catalogProjections[0]", func(step *authoringv2.ImportedSourceStep) {
		step.Parameters.Catalog.Outputs = []authoringv2.ImportedCatalogOutput{{FieldPath: "gender", Enabled: false}}
	}); err != nil {
		t.Fatal(err)
	}
	disabled, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled.CatalogProjections[0].OutputOverrides) != 1 || disabled.CatalogProjections[0].OutputOverrides[0].Enabled {
		t.Fatalf("disable was not applied: %#v", disabled.CatalogProjections[0].OutputOverrides)
	}
	reloaded, err := HydrateSourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	step, ok := sourcePlanStep(reloaded, "catalogProjections[0]")
	if !ok || len(step.Parameters.Catalog.Outputs) != 1 || step.Parameters.Catalog.Outputs[0].FieldPath != "gender" || step.Parameters.Catalog.Outputs[0].Enabled {
		t.Fatalf("disabled output identity was lost on reload: %+v", step)
	}
	if err := updateSourcePlanStep(&reloaded, "catalogProjections[0]", func(step *authoringv2.ImportedSourceStep) {
		step.Parameters.Catalog.Outputs[0].Enabled = true
		step.Parameters.Catalog.Outputs[0].Name = "patient_gender"
	}); err != nil {
		t.Fatal(err)
	}
	reenabled, err := ApplySourcePlan(output, &reloaded)
	if err != nil {
		t.Fatal(err)
	}
	want := recipe.CatalogProjectionOutputOverride{FieldPath: "gender", Enabled: true, Name: "patient_gender"}
	if !reflect.DeepEqual(reenabled.CatalogProjections[0].OutputOverrides, []recipe.CatalogProjectionOutputOverride{want}) {
		t.Fatalf("re-enabled output changed identity: %#v", reenabled.CatalogProjections[0].OutputOverrides)
	}
}

func TestDiscoveredPivotOutputOverridePersistsThroughReloadAndReenable(t *testing.T) {
	output := recipe.Output{
		Name: "Observation", RootResourceType: "Observation", RootOccurrenceID: "base", RowGrain: "resource",
		Pivots: []recipe.Pivot{{Name: "observation_values", Discovery: &recipe.PivotDiscovery{Family: "observation_code_value", MaxColumns: 8}}},
	}
	plan := SourcePlan(output)
	if err := updateSourcePlanStep(&plan, "pivots[0]", func(step *authoringv2.ImportedSourceStep) {
		step.Parameters.Pivot.Outputs = []authoringv2.ImportedPivotOutput{{Key: "http://loinc.org|8480-6", Enabled: false, Name: stringPointer("systolic")}}
	}); err != nil {
		t.Fatal(err)
	}
	disabled, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled.Pivots[0].OutputOverrides) != 1 || disabled.Pivots[0].OutputOverrides[0].Enabled || disabled.Pivots[0].OutputOverrides[0].Name == nil || *disabled.Pivots[0].OutputOverrides[0].Name != "systolic" {
		t.Fatalf("pivot output disable = %#v", disabled.Pivots[0].OutputOverrides)
	}
	reloaded, err := HydrateSourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	step, ok := sourcePlanStep(reloaded, "pivots[0]")
	if !ok || len(step.Parameters.Pivot.Outputs) != 1 || step.Parameters.Pivot.Outputs[0].Key != "http://loinc.org|8480-6" || step.Parameters.Pivot.Outputs[0].Enabled || step.Parameters.Pivot.Outputs[0].Name == nil || *step.Parameters.Pivot.Outputs[0].Name != "systolic" {
		t.Fatalf("disabled pivot output identity was lost on reload: %+v", step)
	}
	if err := updateSourcePlanStep(&reloaded, "pivots[0]", func(step *authoringv2.ImportedSourceStep) {
		step.Parameters.Pivot.Outputs[0].Enabled = true
	}); err != nil {
		t.Fatal(err)
	}
	reenabled, err := ApplySourcePlan(output, &reloaded)
	if err != nil {
		t.Fatal(err)
	}
	got := reenabled.Pivots[0].OutputOverrides[0]
	if got.Key != "http://loinc.org|8480-6" || !got.Enabled || got.Name == nil || *got.Name != "systolic" {
		t.Fatalf("re-enabled raw key or alias changed: %#v", got)
	}
}

func stringPointer(value string) *string { return &value }
