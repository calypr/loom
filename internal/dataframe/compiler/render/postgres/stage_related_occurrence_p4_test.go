package postgres

// P4 production-compiled leg for the G3(c) RelatedOccurrence group adapter
// (plan §8 S9, §9): the recipe authoring channel — recipe.Bundle →
// semantic.BuildRecipePlan → semantic.ResolveRecipePlan →
// lower.CompileResolvedRecipePlan → RenderPhysicalPlan — with ZERO
// hand-shaping of IR. The field sources ride
// recipe.ConstructionGroupFieldSourceInput.RelatedOccurrence through the
// compiler's own authoring APIs (lower.DescribeConstructionSourceStage and
// lower.BindGroupRelatedOccurrenceSource, the lifecycle choice channel), so
// plan.Validate's ">=1 bound field source referencing the adapter" law is
// satisfied by construction, never by hand-attached adapters. Unlike the
// hand-built legs, the adapter-internal columns arrive as a stage EXTENSION
// of the lowered source projection (ir physicalGroupInputColumnsExtendSource,
// physical_stage_validation.go:2659) — exactly the shape a producer emits.
//
// Door scope, per the wave law as integrated: the composition door registers
// the RelatedOccurrence adapter (L-13.5, stage_sequence.go GROUP arm), while
// a route-sourced Group.CodingKey stays the L-13.4 named gap and is refused
// fail-closed at the composition payload gate. P4 therefore pins BOTH sides:
// the registered SCALAR-keyed shape renders and executes through the door,
// and the CODING_IDENTITY plan refuses with the registered twin text —
// never a silent drop.
//
// The live cells execute the door-rendered statement on real PostgreSQL 18
// under the owned verify_sql16_g3_p4_* namespace and prove the S9 share law
// THROUGH the composition door: two prior scans on one engine never borrow
// each other's expansion.

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"

	pgstore "github.com/calypr/loom/internal/backends/postgres/control"
)

const (
	sql16roP4Project    = "sql16ro-proj"
	sql16roP4Generation = "gen-1"
	// sql16roP4CodingSelector is the repeated Coding path the identity key
	// unnests; the route scalar key reads Observation.status.
	sql16roP4CodingSelector = "valueCodeableConcept.coding[]"
	// sql16roP4CodingRefusal is the composition payload gate's registered
	// wave-law twin (stage_sequence.go GROUP arm, group.go:444 twin) for a
	// route-sourced CodingKey — the L-13.4 named gap.
	sql16roP4CodingRefusal = "CodingKey group keys are refused at the composition payload gate until their registered arm lands (group.go:444 twin, wave law)"
)

// sql16roP4Authoring is the two-phase recipe authoring the lifecycle channel
// performs: compile the base source output once, describe its compiler-owned
// GroupFieldSourceContext, then bind the related field sources against it.
type sql16roP4Authoring struct {
	base    recipe.Output
	binding recipe.RuntimeBindings
	context *lower.CompiledGroupFieldSourceContext
}

func sql16roP4BaseOutput(project, generation string) (recipe.Output, recipe.RuntimeBindings) {
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthResourcePaths: []string{"FHIR/Patient", "FHIR/Observation"},
	}
	base := recipe.Output{
		Name: "patient_related_coding_group", RootResourceType: "Patient",
		RootOccurrenceID: "patient-root", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
	}
	return base, bindings
}

func sql16roP4AuthoringFor(t *testing.T, project, generation string) sql16roP4Authoring {
	t.Helper()
	base, bindings := sql16roP4BaseOutput(project, generation)
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: base.Name,
		TranslationVersion: "test", Outputs: []recipe.Output{base},
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan (base): %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan (base): %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileResolvedRecipePlan (base): %v", err)
	}
	descriptor, err := lower.DescribeConstructionSourceStage(
		compiled.Outputs[0].OutputSchema, base.RootResourceType, &compiled.Outputs[0].Plan)
	if err != nil || descriptor.GroupFieldSourceContext == nil {
		t.Fatalf("describe Patient source context: descriptor=%#v err=%v", descriptor, err)
	}
	return sql16roP4Authoring{base: base, binding: bindings, context: descriptor.GroupFieldSourceContext}
}

// sql16roP4Source binds one related field source through the compiler's own
// binding channel (never hand-shaped).
func (a sql16roP4Authoring) sql16roP4Source(t *testing.T, valueKind, resourceType, candidateID, canonicalPath, codingSelector string) recipe.ConstructionGroupFieldSource {
	t.Helper()
	related := &recipe.ConstructionGroupRelatedOccurrenceFieldSource{
		AnchorNodeID: "patient-node", AnchorResourceType: "Patient",
		Route: []recipe.ConstructionGroupRelatedRouteStep{{
			StepIndex: 0, FromNodeID: "patient-node", FromResourceType: "Patient",
			EdgeID: "patient-observation-edge", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			ToNodeID: "observation-node", ToResourceType: "Observation",
		}},
		TargetNodeID: "observation-node", TargetResourceType: "Observation",
		ResourceType: resourceType, CandidateID: candidateID, CanonicalPath: canonicalPath,
		EmptyPolicy:  recipe.ExpansionPreserveParent,
		Multiplicity: recipe.ConstructionGroupRelatedMultiplicityDistinctTargetPerInput,
	}
	if valueKind == "CODING_IDENTITY" {
		related.ResourceType = "Coding"
		related.CodingSelector = codingSelector
		related.CodingEmptyPolicy = recipe.ConstructionGroupCodingEmptyPolicyGroup
		related.OrdinalSemantics = recipe.ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition
	}
	source := recipe.ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: valueKind,
		Input: &recipe.ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: related},
	}
	bound, err := lower.BindGroupRelatedOccurrenceSource(a.context, source)
	if err != nil {
		t.Fatalf("bind %s related Group source: %v", valueKind, err)
	}
	return bound
}

// sql16roP4Compiled runs the full channel to the compiled plan for one
// related Group output over Observation.status: the SCALAR route key +
// row count, plus the CODING_IDENTITY key when withCoding is set (the
// L-13.4 composition gap). Zero IR hand-shaping: every structure below
// comes from the recipe channel or the compiler.
func (a sql16roP4Authoring) sql16roP4Compiled(t *testing.T, withCoding bool) lower.CompiledRecipeOutput {
	t.Helper()
	status := a.sql16roP4Source(t, "SCALAR", "string", "observation-status", "status", "")
	keys := []recipe.ConstructionGroupKey{
		{OutputColumnID: "status_group_id", FieldSource: &status},
	}
	outputs := []recipe.StageColumn{
		{ID: "status_group_id", Name: "status_group", Type: "string"},
	}
	if withCoding {
		coding := a.sql16roP4Source(t, "CODING_IDENTITY", "Coding", "", "", sql16roP4CodingSelector)
		keys = append(keys, recipe.ConstructionGroupKey{OutputColumnID: "coding_group_id", FieldSource: &coding})
		outputs = append(outputs, recipe.StageColumn{ID: "coding_group_id", Name: "coding_group", Type: "string"})
	}
	outputs = append(outputs, recipe.StageColumn{ID: "row_count_id", Name: "row_count", Type: "integer"})
	output := a.base
	output.Name = "patient_related_coding_group_p4"
	output.Construction = &recipe.Construction{
		Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}},
		Steps: []recipe.ConstructionStep{{
			ID: "group_related", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
				ConstructionID: "group_related_id", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
				Keys:       keys,
				Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row_count_id"}},
			}},
			Outputs: outputs,
		}},
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name,
		TranslationVersion: "test", Outputs: []recipe.Output{output},
	}
	plan, err := semantic.BuildRecipePlan(bundle, a.binding)
	if err != nil {
		t.Fatalf("BuildRecipePlan (group): %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, a.binding.Project, a.binding.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan (group): %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileResolvedRecipePlan (group): %v", err)
	}
	return compiled.Outputs[0]
}

// The compiler channel, offline: the lowered plan is one GROUP stage whose
// adapter is entirely compiler-minted, the extension columns are the exact
// adapter column set, and plan.Validate accepts the shape — including the
// CODING_IDENTITY key (a lowering-side fact; its door status is the
// composition gate's, pinned separately below).
func TestSQL16RelatedOccurrenceP4CompiledChannel(t *testing.T) {
	output := sql16roP4AuthoringFor(t, sql16roP4Project, sql16roP4Generation).sql16roP4Compiled(t, true)
	if err := output.Plan.Validate(); err != nil {
		t.Fatalf("compiled plan Validate: %v", err)
	}
	sequence := output.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 1 {
		t.Fatalf("compiled plan is not the single-stage GROUP shape: %#v", sequence)
	}
	stage := sequence.Stages[0]
	if stage.Kind != ir.PhysicalStageGroupOp || stage.Group == nil || stage.Group.RelatedOccurrence == nil {
		t.Fatalf("compiled stage is not a RelatedOccurrence GROUP: kind=%s", stage.Kind)
	}
	adapter := stage.Group.RelatedOccurrence
	if adapter.RouteID == "" || adapter.OccurrenceKeyColumn == "" || adapter.CodingMembershipColumn == "" {
		t.Fatalf("compiler identity columns missing: %#v", adapter)
	}
	// The recipe GROUP coding-empty policy lowers to the registered unnest
	// spelling: parents without items are preserved.
	if adapter.CodingSelector != sql16roP4CodingSelector ||
		adapter.CodingEmptyPolicy != ir.PhysicalUnnestPreserveParent ||
		adapter.OrdinalSemantics != stageRelatedCodingOrdinalSemantics {
		t.Fatalf("coding legs drifted: selector=%q empty=%v ordinal=%q",
			adapter.CodingSelector, adapter.CodingEmptyPolicy, adapter.OrdinalSemantics)
	}
	if len(adapter.ScalarProjections) != 1 || adapter.ScalarProjections[0].InputColumn == "" {
		t.Fatalf("scalar projection set = %#v, want exactly the status leg", adapter.ScalarProjections)
	}
	if len(adapter.RelatedRecords.Route) != 1 ||
		adapter.RelatedRecords.Route[0].Relationship != "subject_Patient" ||
		adapter.RelatedRecords.Route[0].MatchMode != "OPTIONAL" {
		t.Fatalf("route facts drifted: %#v", adapter.RelatedRecords.Route)
	}
	// The extension law as the producer emits it: prior + exactly the
	// adapter columns, each Internal with ID == Name.
	prior := sequence.SourceColumns
	extra := stage.InputColumns[len(prior):]
	if len(stage.InputColumns) != len(prior)+3 {
		t.Fatalf("extension width = %d over prior %d", len(stage.InputColumns), len(prior))
	}
	allowed := map[string]bool{
		adapter.OccurrenceKeyColumn:              true,
		adapter.CodingMembershipColumn:           true,
		adapter.ScalarProjections[0].InputColumn: true,
	}
	for _, column := range extra {
		if !column.Internal || column.ID != column.Name || !allowed[column.Name] {
			t.Fatalf("extension column %q is not a compiler-minted adapter column", column.Name)
		}
		delete(allowed, column.Name)
	}
	if len(allowed) != 0 {
		t.Fatalf("adapter column %v absent from the stage input extension", allowed)
	}
	// The compiler lowers the SCALAR status key into Keys[] and the
	// CODING_IDENTITY key into Group.CodingKey.RelatedSource; both carry the
	// compiler-issued stage/shape identity, so plan.Validate's ">=1 bound
	// source" law is satisfied by construction (one per key, never hand-set).
	bound := 0
	for _, key := range stage.Group.Keys {
		if key.FieldSource != nil && key.FieldSource.Kind == ir.PhysicalStageGroupFieldSourceRelatedOccurrence &&
			key.FieldSource.RelatedOccurrence != nil && key.FieldSource.RelatedOccurrence.RouteID != "" &&
			key.FieldSource.RelatedOccurrence.SourceShapeID != "" {
			bound++
		}
	}
	if codingKey := stage.Group.CodingKey; codingKey != nil && codingKey.RelatedSource != nil &&
		codingKey.RelatedSource.RouteID != "" && codingKey.RelatedSource.SourceShapeID != "" {
		bound++
	}
	if bound != 2 {
		t.Fatalf("bound related field sources = %d, want 2 (status key + coding key)", bound)
	}
}

// The composition door renders the REGISTERED compiled shape — the
// SCALAR-keyed RelatedOccurrence GROUP — with the route legs present and no
// plan data leaked as text.
func TestSQL16RelatedOccurrenceP4DoorRender(t *testing.T) {
	output := sql16roP4AuthoringFor(t, sql16roP4Project, sql16roP4Generation).sql16roP4Compiled(t, false)
	rendered, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(output.Plan), mustBinding(t))
	if err != nil {
		t.Fatalf("door render of the compiled plan: %v", err)
	}
	for _, want := range []string{relAlias, routeAlias, "LEFT JOIN", `ORDER BY`} {
		if !strings.Contains(rendered.SQL, want) {
			t.Fatalf("door statement is missing %q:\n%s", want, rendered.SQL)
		}
	}
	if strings.Contains(rendered.SQL, "__loom_group_related_coding_membership") {
		t.Fatalf("a SCALAR-only plan must not grow the coding membership leg:\n%s", rendered.SQL)
	}
	// Zero hand-shaping includes the statement: no plan data leaks as text.
	for _, leaked := range []string{sql16roP4Project, sql16roP4Generation, "subject_Patient", "observation-node", "group_related_id"} {
		if strings.Contains(rendered.SQL, leaked) {
			t.Fatalf("door statement leaked plan data %q:\n%s", leaked, rendered.SQL)
		}
	}
}

// The wave law through the compiled channel: the CODING_IDENTITY plan
// (route-sourced Group.CodingKey, the L-13.4 named gap) refuses fail-closed
// at the composition payload gate with the registered twin text — the door
// never partially renders or silently drops the key.
func TestSQL16RelatedOccurrenceP4CodingKeyGateRefusal(t *testing.T) {
	output := sql16roP4AuthoringFor(t, sql16roP4Project, sql16roP4Generation).sql16roP4Compiled(t, true)
	rendered, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(output.Plan), mustBinding(t))
	if err == nil {
		t.Fatalf("the composition gate must refuse the route-sourced CodingKey, got:\n%s", rendered.SQL)
	}
	if !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("the refusal must be the typed door refusal, got: %v", err)
	}
	if !strings.Contains(err.Error(), sql16roP4CodingRefusal) {
		t.Fatalf("refusal drifted from the registered twin text: %v", err)
	}
	if rendered.SQL != "" || len(rendered.Args) != 0 {
		t.Fatalf("a refused render must produce no statement: sql=%q args=%d", rendered.SQL, len(rendered.Args))
	}
	// Alive control: the registered SCALAR shape renders on the same authoring.
	if _, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(
		sql16roP4AuthoringFor(t, sql16roP4Project, sql16roP4Generation).sql16roP4Compiled(t, false).Plan), mustBinding(t)); err != nil {
		t.Fatalf("the registered SCALAR shape must render while the CodingKey refuses: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Live: the door statement on real rows (skips without the owned URL).
// ---------------------------------------------------------------------------

// sql16roP4Seed seeds one project partition under the plan's own
// compiler-minted edge label (lockstep, as in the L5 leg): p-<tag>1 carries
// statuses {final, final, preliminary}, p-<tag>2 carries no route. The
// status vocabulary is partition-disjoint so the share killer can read
// borrowing from opaque group identities.
func sql16roP4Seed(t *testing.T, svc *pgir02Service, project, label string, statuses []string) {
	t.Helper()
	tag := p4Tag(project)
	svc.insertResource(t, svc.schema, project, sql16roP4Generation, "Patient", tag+"1", "FHIR/Patient", map[string]any{"gender": "female"})
	svc.insertResource(t, svc.schema, project, sql16roP4Generation, "Patient", tag+"2", "FHIR/Patient", map[string]any{"gender": "male"})
	for index, status := range statuses {
		observation := tag + "o" + string(rune('1'+index))
		svc.insertResource(t, svc.schema, project, sql16roP4Generation, "Observation", observation, "FHIR/Observation", map[string]any{"status": status})
		svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
			Project: project, DatasetGeneration: sql16roP4Generation, AuthResourcePath: "FHIR/Observation",
			Label: label, SourceResourceType: "Observation", SourceLogicalID: observation,
			TargetResourceType: "Patient", TargetLogicalID: tag + "1", SourcePath: "subject[0]", SourceOrdinal: int64(index),
		})
	}
}

func p4Tag(project string) string {
	if strings.HasSuffix(project, "-b") {
		return "b-"
	}
	return "a-"
}

// sql16roP4LivePlan compiles the registered SCALAR shape and returns the
// compiler-minted edge label bind (lockstep seed value).
func sql16roP4LivePlan(t *testing.T, project string) (lower.CompiledRecipeOutput, string) {
	t.Helper()
	output := sql16roP4AuthoringFor(t, project, sql16roP4Generation).sql16roP4Compiled(t, false)
	label, ok := output.Plan.BindVars["related_expand_0_hop_1_label"].(string)
	if !ok || label == "" {
		t.Fatalf("compiled plan lost its route label bind: %#v", output.Plan.BindVars)
	}
	return output, label
}

func TestSQL16RelatedOccurrenceP4LiveRows(t *testing.T) {
	svc := newOwnedService(t, 0, "verify_sql16_g3_p4")
	output, label := sql16roP4LivePlan(t, sql16roP4Project)
	sql16roP4Seed(t, svc, sql16roP4Project, label, []string{"final", "final", "preliminary"})
	rendered := coded19RenderSvc(t, svc, ir.ClonePhysicalPlan(output.Plan))
	rows := svc.runRendered(t, rendered)

	// Expected multiset: the three reachable observations group to
	// (id(final),2) and (id(preliminary),1); the route-less p-a2 anchor is
	// preserved (route EmptyPolicy PRESERVE_PARENT) as the all-key-null
	// grand-total group (null,1) under MissingKeyPolicy GROUP.
	type tuple struct{ status, count string }
	got := make([]tuple, 0, len(rows))
	for _, row := range rows {
		if len(row) != 2 {
			t.Fatalf("row = %#v, want 2 cells", row)
		}
		got = append(got, tuple{row[0], row[1]})
	}
	sort.Slice(got, func(i, j int) bool { return got[i].status < got[j].status })
	if len(got) != 3 {
		t.Fatalf("row multiset = %v, want 3 rows", got)
	}
	var pad string
	counts := map[string]string{}
	distinct := map[string]bool{}
	for _, row := range got {
		if row.status == "<null>" {
			pad = row.count
			continue
		}
		counts[row.status] = row.count
		distinct[row.status] = true
	}
	if pad != "1" {
		t.Fatalf("the preserved-parent grand-total group must count 1, got %q", pad)
	}
	if len(counts) != 2 {
		t.Fatalf("the three observations must group to two distinct status identities: %v", got)
	}
	sum := 0
	for _, count := range counts {
		if count != "1" && count != "2" {
			t.Fatalf("unexpected group count %q in %v", count, got)
		}
		sum += int(count[0] - '0')
	}
	if sum != 3 {
		t.Fatalf("group counts must partition the three reachable observations: %v", got)
	}
}

// The S9 share killer through the door: two prior scans (two project
// partitions on one engine) render through the same door code and their
// expansions never borrow each other's rows. Status vocabularies are
// partition-disjoint, so any identity from A appearing in B (or vice
// versa) is a borrowed expansion.
func TestSQL16RelatedOccurrenceP4LiveShareLaw(t *testing.T) {
	svc := newOwnedService(t, 0, "verify_sql16_g3_p4")
	const projectA, projectB = "sql16ro-share-a", "sql16ro-share-b"
	planA, labelA := sql16roP4LivePlan(t, projectA)
	planB, labelB := sql16roP4LivePlan(t, projectB)
	sql16roP4Seed(t, svc, projectA, labelA, []string{"final", "final"})
	sql16roP4Seed(t, svc, projectB, labelB, []string{"preliminary", "amended"})

	render := func(output lower.CompiledRecipeOutput) [][]string {
		return svc.runRendered(t, coded19RenderSvc(t, svc, ir.ClonePhysicalPlan(output.Plan)))
	}
	rowsA, rowsB := render(planA), render(planB)
	identitiesOf := func(rows [][]string) []string {
		var out []string
		for _, row := range rows {
			if row[0] != "<null>" {
				out = append(out, row[0])
			}
		}
		sort.Strings(out)
		return out
	}
	idsA, idsB := identitiesOf(rowsA), identitiesOf(rowsB)
	// A groups its two final observations to ONE identity with count 2;
	// B groups its two distinct statuses to TWO identities with count 1.
	if len(idsA) != 1 || len(idsB) != 2 {
		t.Fatalf("each partition must group only its own route: A=%v B=%v (A=%v B=%v)", idsA, idsB, rowsA, rowsB)
	}
	var countsA []string
	for _, row := range rowsA {
		countsA = append(countsA, row[1])
	}
	sort.Strings(countsA)
	if strings.Join(countsA, ",") != "1,2" {
		t.Fatalf("A's multiset = %v, want the final group (2) plus the preserved pad (1)", countsA)
	}
	for _, identity := range idsA {
		for _, other := range idsB {
			if identity == other {
				t.Fatalf("partitions borrowed one expansion (share violation): %s", identity)
			}
		}
	}
}
