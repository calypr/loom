package recipeimport

import (
	"context"
	"encoding/json"
	"fmt"
	recipeschema "github.com/calypr/loom/internal/dataframe/recipe/schema"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestNetworkHydratesAllDefaultOutputsWithoutChangingSource(t *testing.T) {
	bundle := defaultSourcePlanFixture(t)
	catalog := catalogForBundle(bundle)
	schemas := map[string][]recipe.StageColumn{}
	for _, o := range bundle.Outputs {
		schemas[o.Name] = []recipe.StageColumn{{ID: "field_" + o.Name, Name: "id", Type: "string"}}
	}
	w, err := ImportWorkspace(bundle, catalog, schemas)
	if err != nil {
		t.Fatal(err)
	}
	for i, o := range bundle.Outputs {
		n, err := HydrateNetwork(w.Documents[i], o, catalog)
		if err != nil {
			t.Fatal(err)
		}
		if n.Owner != "IMPORTED_SOURCE" || n.OutputID != o.Name || !reflect.DeepEqual(n.SourceRows, w.Documents[i].Rows) {
			t.Fatalf("wrong authoritative ownership: %+v", n)
		}
		original, err := ImportRoute(o, catalog)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		var walk func(authoringv2.RouteNode)
		walk = func(r authoringv2.RouteNode) {
			want = append(want, r.OccurrenceID)
			for _, c := range r.Children {
				walk(c)
			}
		}
		walk(original)
		var got []string
		for _, r := range n.Occurrences {
			got = append(got, r.OccurrenceID)
			if !r.PreservesRowIdentity {
				t.Fatalf("traversal implicitly expands rows: %+v", r)
			}
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s occurrence mapping = %v want %v", o.Name, got, want)
		}
		applied, err := ApplySourcePlan(o, w.Documents[i].ImportedSource)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(SourcePlan(applied), SourcePlan(o)) {
			t.Fatalf("%s hydration changed native source", o.Name)
		}
	}
}

func TestRelationshipOverlayPreservesPinAndOccurrenceBindingsAfterRemoval(t *testing.T) {
	output := recipe.Output{Name: "subjects", RootResourceType: "ResearchSubject", RowGrain: "study_enrollment", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}, Traversals: []recipe.Traversal{{Name: "subject", ToResourceType: "Patient"}, {Name: "individual", ToResourceType: "Patient"}}}
	catalog := authoringv2.CatalogSnapshot{Nodes: []authoringv2.CatalogNode{{ID: "subject", ResourceType: "ResearchSubject"}, {ID: "patient", ResourceType: "Patient"}}, Edges: []authoringv2.CatalogEdge{{ID: "edge_subject", FromNodeID: "subject", ToNodeID: "patient", Label: "subject"}, {ID: "edge_individual", FromNodeID: "subject", ToNodeID: "patient", Label: "individual"}}}
	original, err := ImportRoute(output, catalog)
	if err != nil {
		t.Fatal(err)
	}
	plan := SourcePlan(output)
	plan.Relationships = &authoringv2.ImportedRelationshipOverlay{Removals: []string{original.Children[0].OccurrenceID}, MatchOverrides: []authoringv2.ImportedRelationshipMatch{{OccurrenceID: original.Children[1].OccurrenceID, MatchMode: authoringv2.RouteMatchRequired}}}
	raw, _ := json.Marshal(output)
	encoded, _ := json.Marshal(plan)
	var restored authoringv2.ImportedSourcePlan
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRelationshipOverlay(output, &restored, catalog); err != nil {
		t.Fatal(err)
	}
	effective, err := ApplySourcePlan(output, &restored)
	if err != nil {
		t.Fatal(err)
	}
	route, err := ImportRoute(effective, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(route.Children) != 1 || route.Children[0].OccurrenceID != original.Children[1].OccurrenceID || route.Children[0].MatchMode != authoringv2.RouteMatchRequired {
		t.Fatalf("removal retargeted remaining occurrence: %+v", route)
	}
	after, _ := json.Marshal(output)
	if string(raw) != string(after) {
		t.Fatal("overlay mutated immutable source")
	}
	if len(restored.Steps) != len(SourcePlan(output).Steps) {
		t.Fatal("overlay rewrote original positional step set")
	}
}

func TestRelationshipOverlayReauthorizesAdditionAndRejectsAmbiguousCatalog(t *testing.T) {
	output := recipe.Output{Name: "subjects", RootResourceType: "ResearchSubject", RowGrain: "study_enrollment", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}}
	catalog := authoringv2.CatalogSnapshot{Nodes: []authoringv2.CatalogNode{{ID: "subject", ResourceType: "ResearchSubject"}, {ID: "patient", ResourceType: "Patient"}}, Edges: []authoringv2.CatalogEdge{{ID: "edge", FromNodeID: "subject", ToNodeID: "patient", Label: "subject", StorageDirection: "OUTBOUND"}}, Candidates: []authoringv2.CatalogCandidate{{ID: "patient_id", NodeID: "patient", FieldPath: "id", Label: "ID", ProjectionModes: []string{"VALUE"}}}}
	plan := SourcePlan(output)
	plan.Relationships = &authoringv2.ImportedRelationshipOverlay{Additions: []authoringv2.ImportedRelationship{{OccurrenceID: "project_patient", ParentOccurrenceID: "base", CatalogEdgeID: "edge", Relationship: "subject", ResourceType: "Patient", StorageDirection: "OUTBOUND", MatchMode: authoringv2.RouteMatchOptional, Fields: []authoringv2.ImportedRelationshipField{{CandidateID: "patient_id", ColumnID: "patient_ids", Column: "patient_ids", FieldPath: "id", Label: "ID", ValueMode: "ALL"}}}}}
	if err := ValidateRelationshipOverlay(output, &plan, catalog); err != nil {
		t.Fatal(err)
	}
	effective, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.Traversals) != 1 || effective.Traversals[0].Fields[0].Expr.Select != "project_patient.id" || effective.Expand != nil {
		t.Fatalf("overlay did not preserve row identity: %+v", effective)
	}
	plan.Relationships.Additions[0].CatalogEdgeID = "forged"
	if err := ValidateRelationshipOverlay(output, &plan, catalog); err == nil {
		t.Fatal("accepted unauthorized edge")
	}
	plan.Relationships.Additions[0].CatalogEdgeID = "edge"
	catalog.Edges = append(catalog.Edges, authoringv2.CatalogEdge{ID: "parallel", FromNodeID: "subject", ToNodeID: "patient", Label: "subject", StorageDirection: "INBOUND"})
	if err := ValidateRelationshipOverlay(output, &plan, catalog); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous edge not diagnosed: %v", err)
	}
}

func TestCompiledNetworkProvenanceDistinguishesRepeatedPatientOccurrences(t *testing.T) {
	output := recipe.Output{Name: "subjects", RootResourceType: "Observation", RootColumnNaming: recipe.RootColumnNamingExact, Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}, RowGrain: "observation", Traversals: []recipe.Traversal{{Name: "subject_Patient", Alias: "patient_a", ToResourceType: "Patient", OccurrenceID: "patient_first", Fields: []recipe.Field{{Name: "id", ValueMode: recipe.ValueModeAll, Expr: recipe.Expression{Select: "patient_a.id"}}}}, {Name: "performer_Patient", Alias: "patient_b", ToResourceType: "Patient", OccurrenceID: "patient_second", Fields: []recipe.Field{{Name: "id", ValueMode: recipe.ValueModeFirst, Expr: recipe.Expression{Select: "patient_b.id"}}}}}}
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "oracle", TranslationVersion: "v1", Outputs: []recipe.Output{output}}, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range compiled.Outputs[0].OutputSchema {
		if c.SourceOrigin != nil {
			got[c.Name] = c.SourceOrigin.OccurrenceID
		}
	}
	if got["patient_a__id"] != "patient_first" || got["patient_b__id"] != "patient_second" {
		t.Fatalf("compiled origin lost distinct paths: %#v", got)
	}
}

// This fixture supplies independently chosen catalog fields. The resolver and
// compiler must retain exact owning occurrences for all five default outputs.
type networkDiscovery struct{}

func (networkDiscovery) Fields(_ context.Context, _ recipeschema.Scope, resource string) ([]recipeschema.FieldCandidate, error) {
	return []recipeschema.FieldCandidate{{ResourceType: resource, Path: "id", Kind: "scalar"}}, nil
}

func TestDefaultCompiledNetworkBindsEveryPublicColumn(t *testing.T) {
	bundle := defaultSourcePlanFixture(t)
	catalog := catalogForBundle(bundle)
	effective := bundle
	effective.Outputs = append([]recipe.Output(nil), bundle.Outputs...)
	for i, o := range bundle.Outputs {
		plan := SourcePlan(o)
		var err error
		effective.Outputs[i], err = ApplySourcePlan(o, &plan)
		if err != nil {
			t.Fatal(err)
		}
	}
	discovered, err := recipeschema.Resolve(context.Background(), effective, recipeschema.Scope{Project: "p", DatasetGeneration: "g"}, networkDiscovery{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := semantic.BuildRecipePlan(discovered.Bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "g")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	schemas := map[string][]recipe.StageColumn{}
	for oi := range compiled.Outputs {
		o := &compiled.Outputs[oi]
		for ci := range o.OutputSchema {
			c := &o.OutputSchema[ci]
			if c.Internal || c.Identity {
				continue
			}
			if c.ID == "" {
				c.ID = fmt.Sprintf("compiled_%s_%d", o.Name, ci)
			}
			schemas[o.Name] = append(schemas[o.Name], recipe.StageColumn{ID: c.ID, Name: c.Name, Type: c.Kind})
		}
	}
	workspace, err := ImportWorkspace(bundle, catalog, schemas)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range workspace.Documents {
		n, err := HydrateNetwork(d, bundle.Outputs[i], catalog)
		if err != nil {
			t.Fatal(err)
		}
		n, err = BindCompiledNetwork(n, compiled.Outputs[i].OutputSchema, d.ImportedSource)
		if err != nil {
			t.Fatal(err)
		}
		knownSteps := map[string]bool{}
		for _, step := range d.ImportedSource.Steps {
			knownSteps[step.ID] = true
		}
		for _, b := range n.Bindings {
			if b.Status == "EXACT" && !knownSteps[b.SourceStepID] {
				t.Errorf("%s column %s claims nonexistent source step %q", d.Output.ID, b.ColumnID, b.SourceStepID)
			}
			if b.Status != "EXACT" || b.OccurrenceID == "" {
				t.Errorf("%s column %s has no exact compiler binding: %+v", d.Output.ID, b.ColumnID, b)
			}
		}
	}
}

func TestRelationshipOverlayRejectsCatalogDepthAndAuthoredCycles(t *testing.T) {
	output := recipe.Output{Name: "subjects", RootResourceType: "ResearchSubject", RowGrain: "study_enrollment"}
	catalog := authoringv2.CatalogSnapshot{Nodes: []authoringv2.CatalogNode{{ID: "subject", ResourceType: "ResearchSubject"}, {ID: "patient", ResourceType: "Patient"}}, Edges: []authoringv2.CatalogEdge{{ID: "out", FromNodeID: "subject", ToNodeID: "patient", Label: "subject_Patient"}, {ID: "back", FromNodeID: "patient", ToNodeID: "subject", Label: "subject_Patient"}}}
	plan := SourcePlan(output)
	plan.Relationships = &authoringv2.ImportedRelationshipOverlay{Additions: []authoringv2.ImportedRelationship{{OccurrenceID: "project_patient", ParentOccurrenceID: "base", CatalogEdgeID: "out", Relationship: "subject_Patient", ResourceType: "Patient", MatchMode: authoringv2.RouteMatchOptional}}}
	limit := 0
	catalog.RoutePolicy.MaxHops = &limit
	if err := ValidateRelationshipOverlay(output, &plan, catalog); err == nil || !strings.Contains(err.Error(), "ROUTE_TOO_LONG") {
		t.Fatalf("depth limit not enforced: %v", err)
	}
	catalog.RoutePolicy.MaxHops = nil
	plan.Relationships.Additions = append(plan.Relationships.Additions, authoringv2.ImportedRelationship{OccurrenceID: "project_cycle", ParentOccurrenceID: "project_patient", CatalogEdgeID: "back", Relationship: "subject_Patient", ResourceType: "ResearchSubject", MatchMode: authoringv2.RouteMatchOptional})
	if err := ValidateRelationshipOverlay(output, &plan, catalog); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("authored cycle not rejected: %v", err)
	}
}

func TestImportRowsPreservesPinnedExplicitGroups(t *testing.T) {
	output := recipe.Output{GroupRows: &recipe.GroupRows{RevisionID: "revision_one", UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED", AfterStepID: "group_input", RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "contributor_id", Policy: recipe.ConstructionRowValueAll}, {ColumnID: "single_status", Policy: recipe.ConstructionRowValueOne}}}}
	got := ImportRows(output)
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	expected := authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionGroups, Groups: &authoringv2.GroupedRows{Source: authoringv2.GroupSource{Kind: authoringv2.GroupSourceExplicit, Explicit: &authoringv2.ExplicitGroupSource{RevisionID: "revision_one", UnassignedMemberPolicy: authoringv2.UnassignedMemberGroupAsUnassigned}}, AfterStepID: "group_input", RowValues: []authoringv2.ExplicitGroupRowValue{{ColumnID: "contributor_id", Policy: authoringv2.ConstructionRowValueAll}, {ColumnID: "single_status", Policy: authoringv2.ConstructionRowValueOne}}}}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("explicit group source semantics lost: %#v", got)
	}
	output.GroupRows.RevisionID = "changed_after_import"
	if got.Groups.Source.Explicit.RevisionID != "revision_one" {
		t.Fatal("row definition aliases mutable recipe")
	}
}

func TestNetworkRequiredUnderOptionalCannotExcludeSourceRows(t *testing.T) {
	root := authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Observation", Children: []authoringv2.RouteNode{{OccurrenceID: "optional_patient", ResourceType: "Patient", Relationship: "subject_Patient", MatchMode: authoringv2.RouteMatchOptional, Children: []authoringv2.RouteNode{{OccurrenceID: "required_child", ResourceType: "Condition", Relationship: "subject_Patient", MatchMode: authoringv2.RouteMatchRequired}}}}}
	d := authoringv2.Document{Output: authoringv2.Output{ID: "observations"}, Route: root, Rows: authoringv2.RecordsRowDefinition()}
	catalog := authoringv2.CatalogSnapshot{Nodes: []authoringv2.CatalogNode{{ID: "obs", ResourceType: "Observation", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient"}, {ID: "condition", ResourceType: "Condition"}}, Edges: []authoringv2.CatalogEdge{{ID: "patient_edge", FromNodeID: "obs", ToNodeID: "patient", Label: "subject_Patient"}, {ID: "condition_edge", FromNodeID: "patient", ToNodeID: "condition", Label: "subject_Patient"}}}
	n, err := authoringv2.NetworkForDocument(d, catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range n.Occurrences {
		if o.CanExcludeRows {
			t.Fatalf("optional ancestor means nested REQUIRED cannot remove root rows: %+v", o)
		}
	}
	output := recipe.Output{Name: "observations", RootResourceType: "Observation", RowGrain: "observation", Traversals: []recipe.Traversal{{Name: "subject_Patient", ToResourceType: "Patient", MatchMode: recipe.MatchOptional, Traversals: []recipe.Traversal{{Name: "subject_Patient", ToResourceType: "Condition", MatchMode: recipe.MatchRequired}}}}}
	bundle := recipe.Bundle{Outputs: []recipe.Output{output}}
	sourcePlan := SourcePlan(output)
	imported := d
	imported.ImportedRecipe = &authoringv2.ImportedRecipeRef{Output: "observations"}
	imported.ImportedSource = &sourcePlan
	importedNetwork, err := HydrateNetwork(imported, output, catalogForBundle(bundle))
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range importedNetwork.Occurrences {
		if o.CanExcludeRows {
			t.Fatalf("imported optional ancestor cannot exclude root rows: %+v", o)
		}
	}
	d.Route.Children[0].MatchMode = authoringv2.RouteMatchRequired
	n, err = authoringv2.NetworkForDocument(d, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !n.Occurrences[1].CanExcludeRows || !n.Occurrences[2].CanExcludeRows {
		t.Fatalf("fully required prefix must report source row loss: %+v", n.Occurrences)
	}
}

func TestHydrateSourcePlanMigratesLegacyMetadataAndRejectsRetargeting(t *testing.T) {
	output := outputNamed(t, defaultSourcePlanFixture(t), "DocumentReference")
	pristine := SourcePlan(output)
	legacy := cloneSourcePlan(pristine)
	for i := range legacy.Steps {
		legacy.Steps[i].OccurrenceID = ""
		legacy.Steps[i].MatchMode = ""
		legacy.Steps[i].SourceKey = ""
	}
	setSourcePlanEnabled(t, &legacy, "fields", false)
	legacy.Relationships = &authoringv2.ImportedRelationshipOverlay{}
	for _, s := range pristine.Steps {
		if s.Kind == SourcePlanTraversal {
			legacy.Relationships.MatchOverrides = []authoringv2.ImportedRelationshipMatch{{OccurrenceID: s.OccurrenceID, MatchMode: authoringv2.RouteMatchRequired}}
			break
		}
	}
	hydrated, err := HydrateSourcePlan(output, &legacy)
	if err != nil {
		t.Fatal(err)
	}
	fieldStep, _ := sourcePlanStep(hydrated, "fields")
	if fieldStep.Enabled || len(hydrated.Relationships.MatchOverrides) != 1 {
		t.Fatal("migration lost bounded overrides or authored match policy")
	}
	for i, s := range pristine.Steps {
		if hydrated.Steps[i].OccurrenceID != s.OccurrenceID || hydrated.Steps[i].MatchMode != s.MatchMode || hydrated.Steps[i].SourceKey != s.SourceKey {
			t.Fatalf("metadata migration differs at step %q", s.ID)
		}
	}
	for i, s := range hydrated.Steps {
		if s.Kind == SourcePlanTraversal {
			hydrated.Steps[i].OccurrenceID = "project_forged"
			if _, err := HydrateSourcePlan(output, &hydrated); err == nil {
				t.Fatal("provided occurrence identity was retargeted")
			}
			break
		}
	}
}

func TestCompiledNetworkRejectsAmbiguousLegacyCatalogOwners(t *testing.T) {
	network := authoringv2.AuthoringNetwork{Occurrences: []authoringv2.NetworkOccurrence{{OccurrenceID: "base"}}, Bindings: []authoringv2.NetworkColumnBinding{{ColumnID: "id", Status: "UNKNOWN"}}}
	columns := []lower.CompiledOutputColumn{{ID: "id", Name: "id", SourceOrigin: &lower.CompiledSourceOrigin{OccurrenceID: "base", SourcePath: "fields", SourceKind: SourcePlanCatalogProjection}}}
	plan := authoringv2.ImportedSourcePlan{Steps: []authoringv2.ImportedSourceStep{{ID: "catalogProjections[0]", Kind: SourcePlanCatalogProjection, Title: "First", Summary: "First", Enabled: true, Editable: true}, {ID: "catalogProjections[1]", Kind: SourcePlanCatalogProjection, Title: "Second", Summary: "Second", Enabled: true, Editable: true}}}
	if _, err := BindCompiledNetwork(network, columns, &plan); err == nil || !strings.Contains(err.Error(), "SOURCE_PROVENANCE_AMBIGUOUS") {
		t.Fatalf("ambiguous catalog provenance was guessed: %v", err)
	}
	plan.Steps = plan.Steps[:1]
	got, err := BindCompiledNetwork(network, columns, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bindings[0].SourceStepID != "catalogProjections[0]" {
		t.Fatal("unique original catalog descriptor was not bound")
	}
}

func TestCompiledNetworkFamilyKeysSurviveDiscoveryPruning(t *testing.T) {
	network := authoringv2.AuthoringNetwork{Occurrences: []authoringv2.NetworkOccurrence{{OccurrenceID: "base"}}, Bindings: []authoringv2.NetworkColumnBinding{{ColumnID: "pivot_result", Status: "UNKNOWN"}}}
	columns := []lower.CompiledOutputColumn{{ID: "pivot_result", SourceOrigin: &lower.CompiledSourceOrigin{OccurrenceID: "base", SourcePath: "pivots[0]", SourceKind: SourcePlanPivot, SourceKey: "second"}}}
	plan := authoringv2.ImportedSourcePlan{Steps: []authoringv2.ImportedSourceStep{{ID: "pivots[0]", SourceKey: "first", Kind: SourcePlanPivot, Title: "First", Summary: "First", Enabled: true, Editable: true}, {ID: "pivots[1]", SourceKey: "second", Kind: SourcePlanPivot, Title: "Second", Summary: "Second", Enabled: true, Editable: true}}}
	got, err := BindCompiledNetwork(network, columns, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bindings[0].SourceStepID != "pivots[1]" {
		t.Fatalf("pruned discovery retargeted pivot: %+v", got.Bindings)
	}
}

func TestCompiledExtensionFamiliesRetainNativeSourceStepKeys(t *testing.T) {
	extension := func(name, key string) recipe.ExtensionColumn {
		return recipe.ExtensionColumn{Name: name, Source: recipe.Expression{Select: "root.extension[]"}, MaxColumns: 1, Columns: []recipe.ExtensionColumnMapping{{Name: key, URL: "https://example.test/" + key, ValuePath: "valueString", ValueType: "string"}}}
	}
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RootOccurrenceID: "base", RowGrain: "patient", ExtensionColumns: []recipe.ExtensionColumn{extension("first", "one"), extension("second", "two")}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "source-families", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	semanticPlan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(semanticPlan, "scope", "g")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	n := authoringv2.AuthoringNetwork{Occurrences: []authoringv2.NetworkOccurrence{{OccurrenceID: "base"}}}
	columns := compiled.Outputs[0].OutputSchema
	sourceKeys := map[string]string{}
	for i := range columns {
		c := &columns[i]
		if c.Internal || c.Identity {
			continue
		}
		c.ID = fmt.Sprintf("column_%d", i)
		n.Bindings = append(n.Bindings, authoringv2.NetworkColumnBinding{ColumnID: c.ID, Status: "UNKNOWN"})
		if c.SourceOrigin == nil {
			t.Fatalf("extension %s lacks origin", c.Name)
		}
		sourceKeys[c.ID] = c.SourceOrigin.SourceKey
	}
	sourcePlan := SourcePlan(output)
	n, err = BindCompiledNetwork(n, columns, &sourcePlan)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, binding := range n.Bindings {
		key := sourceKeys[binding.ColumnID]
		found[key] = true
		want := "extensionColumns[0]"
		if key == "second" {
			want = "extensionColumns[1]"
		}
		if binding.SourceStepID != want {
			t.Fatalf("extension family %q retargeted to %q want %q", key, binding.SourceStepID, want)
		}
	}
	if !found["first"] || !found["second"] {
		t.Fatalf("compiler family keys lost: %v", found)
	}
}

func TestRelationshipOverlayRequiresExactParentCatalogNode(t *testing.T) {
	output := recipe.Output{Name: "subjects", RootResourceType: "ResearchSubject", RowGrain: "resource", Traversals: []recipe.Traversal{{Name: "subject", ToResourceType: "Patient", OccurrenceID: "patient_scope"}}}
	catalog := authoringv2.CatalogSnapshot{Nodes: []authoringv2.CatalogNode{{ID: "subject", ResourceType: "ResearchSubject"}, {ID: "patient_a", ResourceType: "Patient"}, {ID: "patient_b", ResourceType: "Patient"}, {ID: "observation", ResourceType: "Observation"}, {ID: "specimen", ResourceType: "Specimen"}}, Edges: []authoringv2.CatalogEdge{{ID: "subject_to_a", FromNodeID: "subject", ToNodeID: "patient_a", Label: "subject"}, {ID: "observations", FromNodeID: "patient_b", ToNodeID: "observation", Label: "observations"}, {ID: "specimens", FromNodeID: "observation", ToNodeID: "specimen", Label: "specimens"}}}
	plan := SourcePlan(output)
	plan.Relationships = &authoringv2.ImportedRelationshipOverlay{Additions: []authoringv2.ImportedRelationship{{OccurrenceID: "project_observation", ParentOccurrenceID: "patient_scope", CatalogEdgeID: "observations", Relationship: "observations", ResourceType: "Observation", MatchMode: authoringv2.RouteMatchOptional}}}
	if err := ValidateRelationshipOverlay(output, &plan, catalog); err == nil {
		t.Fatal("accepted an edge from patient_b under occurrence reached through patient_a")
	}
	catalog.Edges[1].FromNodeID = "patient_a"
	if err := ValidateRelationshipOverlay(output, &plan, catalog); err != nil {
		t.Fatalf("correct exact parent rejected: %v", err)
	}
	plan.Relationships.Additions = append(plan.Relationships.Additions, authoringv2.ImportedRelationship{OccurrenceID: "project_specimen", ParentOccurrenceID: "project_observation", CatalogEdgeID: "specimens", Relationship: "specimens", ResourceType: "Specimen", MatchMode: authoringv2.RouteMatchOptional})
	if err := ValidateRelationshipOverlay(output, &plan, catalog); err != nil {
		t.Fatalf("nested addition lost exact incoming target node: %v", err)
	}
	catalog.Edges[2].FromNodeID = "patient_b"
	if err := ValidateRelationshipOverlay(output, &plan, catalog); err == nil {
		t.Fatal("nested addition accepted a different exact parent node")
	}
	catalog.Nodes = append(catalog.Nodes, authoringv2.CatalogNode{ID: "subject_copy", ResourceType: "ResearchSubject"})
	if err := ValidateRelationshipOverlay(output, &plan, catalog); err == nil || !strings.Contains(err.Error(), "SOURCE_PARENT_AMBIGUOUS") {
		t.Fatalf("ambiguous root catalog identity not diagnosed: %v", err)
	}
}
