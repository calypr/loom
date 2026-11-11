package postgres

// Offline battery for the pg-sql-16 G3(c) related-occurrence Group adapter
// (stage_related_occurrence.go). The arm renders a GROUP stage's
// related-occurrence adapter as ONE correlated lateral expansion shared by
// every adapter-owned read of that stage: the anchor-prefixed hop chain
// over fhir_edge × target occurrence (traversal.go contracts), the Coding
// expansion in the unnest slice's registered WITH-ORDINALITY shape, the
// selected scalars, the occurrence key, and the has-item/route-has-item/
// rank witnesses that pin the registered total order.
//
// Cells: fragment-class goldens (anchor scan, one-hop route, coding
// expansion, scalar projections, occurrence key, witnesses, policies), the
// share-law cell (each route binds to its own prior relation; a borrow is
// structurally impossible because no cross-call memo exists), determinism,
// the parameterization law (plan data only through $n), and the typed
// refusal battery for every shape outside the closed slice.
//
// The arm is called directly (the composition-door wiring is the group.go
// half of this wave): the integration hunk documented in the file header
// threads region.sourceText / region.columns / region.orderTerms through
// renderStageGroup.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// ---------------------------------------------------------------------------
// Fixture: the production shape of lower/recipe_construction_related_expand.go
// (anchor scan + root-key equality + canonical scope blocks + one validated
// traversal + optional contributor FILTER) and
// lower/group_related_occurrence_lower.go (the adapter itself), hand-carried
// because the compiled route to an adapter-bearing composition stage is the
// production-unreachable receipt this slice mirrors (L-13.5 pins).
// ---------------------------------------------------------------------------

const (
	sql16roProject    = "sql16ro-proj"
	sql16roGeneration = "gen-1"
	sql16roAuthPath   = "FHIR/Patient"
)

// sql16roScopeBlock spells the canonical project/dataset_generation/auth
// legs appendProjectScope/appendDatasetGenerationScope/appendAuthScope emit
// (population.go matchCanonicalScopeBlock is the contract).
func sql16roScopeBlock(variables []string, authVariable string) []ir.PhysicalOperation {
	operations := make([]ir.PhysicalOperation, 0, 2*len(variables)+2)
	for _, field := range []struct {
		path string
		bind string
	}{{"project", "project"}, {"dataset_generation", "dataset_generation"}} {
		for _, variable := range variables {
			operations = append(operations, ir.PhysicalOperation{
				Kind: ir.PhysicalFilterOp,
				Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
					Operator: "EQUALS",
					Left:     ir.PhysicalValue{Variable: variable, Path: []string{field.path}},
					Right:    &ir.PhysicalValue{BindKey: field.bind},
				}},
			})
		}
	}
	authInputs := make([]ir.PhysicalValue, 0, len(variables)+2)
	for _, variable := range variables {
		authInputs = append(authInputs, ir.PhysicalValue{Variable: variable, Path: []string{"auth_resource_path"}})
	}
	authInputs = append(authInputs,
		ir.PhysicalValue{BindKey: "auth_resource_paths"},
		ir.PhysicalValue{BindKey: "auth_resource_paths_unrestricted"})
	operations = append(operations,
		ir.PhysicalOperation{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
			Variable: authVariable, Operator: "AUTH_RESOURCE_PATH_ALLOWED", Inputs: authInputs,
		}},
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS",
			Left:     ir.PhysicalValue{Variable: authVariable},
			Right:    &ir.PhysicalValue{BindKey: "scope_allowed"},
		}}},
	)
	return operations
}

func sql16roValueExpression(variable string, path ...string) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: variable, Path: path},
	}
}

func sql16roExtract(variable, resourceType, selector string, cardinality ir.PhysicalCardinality,
	nullBehavior ir.PhysicalNullBehavior,
) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: cardinality, NullBehavior: nullBehavior,
		Extract: &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: variable, Path: []string{"payload"}},
			ResourceType:  resourceType,
			Selector:      sql16roSelectorNameOnly(selector),
			ExecutionMode: ir.PhysicalSelectorDirectScalar,
		},
	}
}

func sql16roSelectorNameOnly(text string) spec.Selector {
	// Fixed paths used by every fixture; parse once through the real
	// grammar so a fixture can never hand-shape an ungrammatical selector.
	selector, err := spec.ParseSelector(text)
	if err != nil {
		panic("sql16ro fixture selector " + text + ": " + err.Error())
	}
	return selector
}

// sql16roFixture is the canonical one-hop (Patient ← Observation, INBOUND
// subject_Reference) adapter with the Coding expansion, EXCLUDE policy, and
// one selected scalar.
func sql16roFixture(t *testing.T) (ir.PhysicalPlan, ir.PhysicalConstructionStage, map[string]stageColumnFact) {
	t.Helper()
	plan := ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"project":                          sql16roProject,
			"dataset_generation":               sql16roGeneration,
			"auth_resource_paths":              []string{sql16roAuthPath, "FHIR/Observation"},
			"auth_resource_paths_unrestricted": false,
			"scope_allowed":                    true,
			"ro_anchor_collection":             "Patient",
			"ro_edge_collection":               "fhir_edge",
			"ro_label":                         "subject_Reference",
			"ro_source_type":                   "Patient",
			"ro_target_type":                   "Observation",
			"ro_cid":                           "group_related_coding",
			"ro_contributor_value":             "final",
		},
	}
	route := []ir.PhysicalStageRelatedRouteStep{{
		StepIndex: 0, FromOccurrenceID: "patient-occ", FromNodeID: "patient-node", FromResourceType: "Patient",
		EdgeID: "subject-edge", Relationship: "subject_Reference", StorageDirection: "INBOUND", MatchMode: "REQUIRED",
		ToOccurrenceID: "observation-occ", ToNodeID: "observation-node", ToResourceType: "Observation",
	}}
	subplan := ir.PhysicalSubplan{
		Captures: []string{"inputrow"},
		Unique:   true,
		Sort:     &ir.PhysicalValue{Variable: "observation_doc", Path: []string{"_id"}},
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
				{Name: "terminal_id", Expression: sql16roValueExpression("observation_doc", "_id")},
				{Name: "resource_id", Expression: sql16roValueExpression("observation_doc", "id")},
				{Name: "status", Expression: sql16roExtract("observation_doc", "Observation", "status",
					ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)},
				{Name: "__loom_group_related_codings", Expression: sql16roExtract("observation_doc", "Observation",
					"valueCodeableConcept.coding[]", ir.PhysicalArrayCardinality, ir.PhysicalEmptyOnNull)},
			}}},
	}
	subplan.Operations = append(subplan.Operations,
		// The anchor scan and root-key equality (recipe_construction_related_expand.go).
		ir.PhysicalOperation{Kind: ir.PhysicalCollectionScanOp,
			CollectionScan: &ir.PhysicalCollectionScan{Variable: "anchor_doc", CollectionBindKey: "ro_anchor_collection"}},
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS",
			Left:     ir.PhysicalValue{Variable: "anchor_doc", Path: []string{"_key"}},
			Right:    &ir.PhysicalValue{Variable: "inputrow", Path: []string{"_key"}},
		}}},
	)
	subplan.Operations = append(subplan.Operations, sql16roScopeBlock([]string{"anchor_doc"}, "anchor_auth")...)
	subplan.Operations = append(subplan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "anchor_doc", TargetVariable: "observation_doc", EdgeVariable: "subject_edge",
			Direction: ir.PhysicalInbound, EdgeCollectionBindKey: "ro_edge_collection",
			EdgeLabelBindKey: "ro_label", SourceTypeBindKey: "ro_source_type", TargetTypeBindKey: "ro_target_type",
			EdgeTargetTypeField: "from_type", Strategy: ir.PhysicalTraversalNative,
		}},
	)
	subplan.Operations = append(subplan.Operations, sql16roScopeBlock([]string{"subject_edge", "observation_doc"}, "hop_auth")...)

	adapter := &ir.PhysicalStageGroupRelatedOccurrenceAdapter{
		RouteID: "related-route-1",
		RelatedRecords: ir.PhysicalStageRelatedExpand{
			RouteID: "related-route-1", Multiplicity: "DISTINCT_TARGET_PER_INPUT",
			AnchorColumnID: "_key", AnchorKind: "root", AnchorOccurrenceID: "patient-occ",
			AnchorNodeID: "patient-node", AnchorResourceType: "Patient",
			TargetNodeID: "observation-node", TargetResourceType: "Observation",
			ParentIdentityColumn:   "_key",
			RelatedRecordsVariable: "related_records", IndexVariable: "related_index",
			ItemVariable: "related_item", IdentityVariable: "related_identity",
			ConstructionIDBindKey: "ro_cid", EmptyPolicy: ir.PhysicalUnnestPreserveParent,
			RelatedRecords: subplan,
			Route:          route,
		},
		ScalarProjections: []ir.PhysicalStageGroupRelatedProjection{{
			InputColumn: "related_status", ResultField: "status",
			OwnerResourceType: "Observation", ValueResourceType: "string",
		}},
		CodingSelector:    "valueCodeableConcept.coding[]",
		CodingArrayColumn: "__loom_group_related_codings",
		CodingEmptyPolicy: ir.PhysicalUnnestExclude,
		OrdinalSemantics:  stageRelatedCodingOrdinalSemantics,
		CodingUnnest: &ir.PhysicalUnnest{
			OutputVariable: "coding_item", Ordinality: "coding_ordinal", HasItemVariable: "coding_has_item",
			EmptyPolicy: ir.PhysicalUnnestExclude,
			Expression: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalEmptyOnNull,
				Value:        &ir.PhysicalValue{Variable: "related_item", Path: []string{"__loom_group_related_codings"}}},
		},
		CodingMembershipColumn: "membership",
		OccurrenceKeyColumn:    "occurrence",
		RouteHasItemVariable:   "route_has_item",
	}
	stage := ir.PhysicalConstructionStage{
		ID: "group_related", InputStageID: "source_projection", Kind: ir.PhysicalStageGroupOp,
		InputRowVariable:  "inputrow",
		OutputRowVariable: "group_row",
		InputColumns: []ir.PhysicalStageColumn{
			{ID: "key_id", Name: "_key", Kind: "string", Cardinality: "required_one"},
			{ID: "related_status_id", Name: "related_status", Kind: "string", Cardinality: "optional_one", Internal: true},
			{ID: "membership_id", Name: "membership", Kind: "object", Cardinality: "optional_one", Internal: true},
			{ID: "occurrence_id", Name: "occurrence", Kind: "object", Cardinality: "required_one", Internal: true},
		},
		OutputColumns:     []ir.PhysicalStageColumn{{ID: "occurrence_id", Name: "occurrence", Kind: "object", Cardinality: "required_one", Internal: true}},
		RowIdentityColumn: "occurrence",
		Group: &ir.PhysicalStageGroup{
			ConstructionIDBindKey: "ro_cid",
			MissingKeyPolicy:      ir.PhysicalStageGroupMissingKeyExclude,
			RelatedOccurrence:     adapter,
		},
	}
	facts := map[string]stageColumnFact{
		"_key":           {alias: "c0"},
		"related_status": {alias: "c1"},
		"membership":     {alias: "c2", jsonb: true},
		"occurrence":     {alias: "c3", jsonb: true},
	}
	return plan, stage, facts
}

func sql16roRender(t *testing.T, plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage,
	relation string, facts map[string]stageColumnFact,
) (stageRelatedOccurrenceRegion, *planRenderer) {
	t.Helper()
	r := &planRenderer{
		storage: mustBinding(t), rows: map[string]rowScope{},
		parameters: map[string]int{}, canonicalAuthLets: map[string]string{},
	}
	if err := r.loadScope(plan); err != nil {
		t.Fatalf("loadScope: %v", err)
	}
	region, err := r.renderStageGroupRelatedOccurrence(plan, stage, 1, relation, facts)
	if err != nil {
		t.Fatalf("renderStageGroupRelatedOccurrence: %v", err)
	}
	return region, r
}

func sql16roBoundTexts(r *planRenderer) map[string]bool {
	bound := map[string]bool{}
	for _, parameter := range r.args {
		if text, ok := parameter.Value.(string); ok {
			bound[text] = true
		}
	}
	return bound
}

// ---------------------------------------------------------------------------
// Golden: fragment classes of the canonical one-hop adapter.
// ---------------------------------------------------------------------------

func TestSQL16RelatedOccurrenceGoldenOneHop(t *testing.T) {
	plan, stage, facts := sql16roFixture(t)
	region, r := sql16roRender(t, plan, stage, "__loom_stage_0", facts)
	sql := region.sourceText

	// The shared-expansion spine: the prior relation under the terminal
	// alias, one lateral expansion, positional carry-through.
	for _, want := range []string{
		"SELECT s.*, __loom_rel.* FROM __loom_stage_0 AS s LEFT JOIN LATERAL",
		"FROM (SELECT 1) AS __loom_one LEFT JOIN LATERAL (",
		") AS __loom_route ON TRUE",
		"LEFT JOIN LATERAL (SELECT __loom_r1_cod_e.item AS item, ROW_NUMBER() OVER (ORDER BY __loom_r1_cod_e.ord) AS rn FROM jsonb_array_elements(",
		") WITH ORDINALITY AS __loom_r1_cod_e(item, ord) WHERE jsonb_typeof(__loom_r1_cod_e.item) <> 'null') AS __loom_r1_cod ON TRUE",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("source text missing spine fragment:\n want: %s\n got: %s", want, sql)
		}
	}

	// The route: DISTINCT ON terminal identity, the target document, the
	// anchor scan correlated through the materialized root key, and the
	// INNER edge × target occurrence join of the hop contract.
	for _, want := range []string{
		`SELECT DISTINCT ON (`, ` COLLATE "C") `, ` AS term_id, `, ` AS doc`,
		` ORDER BY `, ` COLLATE "C"`,
		`"logical_id" = s."c0"`,    // anchor equality through the prior relation's key column
		`target_logical_id" = a1.`, // INBOUND: anchored endpoint is the edge target side
		`"source_logical_id"`,      // reached endpoint carried to the target join
		` INNER JOIN `,             // edge and target joins are INNER
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("source text missing route fragment:\n want: %s\n got: %s", want, sql)
		}
	}
	// The conjunct-placement law: the anchor scope legs, the root-key
	// correlation, and the contributor FILTERs address the hop aliases
	// (a1/e2/x2), so they must be composed INSIDE the route subquery. The
	// planner rejects them at the outer lateral (42P01), which the live leg
	// proved; this cell is the offline guard.
	routeEnd := strings.Index(sql, "AS __loom_route ON TRUE")
	relStart := strings.Index(sql, "AS __loom_rel ON TRUE")
	if routeEnd < 0 || relStart < 0 || routeEnd > relStart {
		t.Fatalf("the route/coding lateral structure drifted:\n%s", sql)
	}
	for _, inside := range []string{`a1."logical_id" = s."c0"`, `WHERE a1."project" = $`,
		`a1."auth_resource_path" = ANY`} {
		at := strings.Index(sql, inside)
		if at < 0 || at > routeEnd {
			t.Fatalf("conjunct %q is not composed inside the route subquery:\n%s", inside, sql)
		}
	}
	// The outer lateral's WHERE carries only the registered policy
	// conjuncts: nothing that names a hop alias may appear after the route.
	if outer := sql[relStart:]; strings.Contains(outer, "a1.") || strings.Contains(outer, "x2.") || strings.Contains(outer, "e2.") {
		t.Fatalf("a hop-alias conjunct escaped into the outer lateral:\n%s", outer)
	}

	// The total order is the AQL FOR nesting: parent identity, then the
	// reached terminal, then the dense Coding position. Leading with the
	// terminal would tie two parents that reach one target (and two
	// unmatched parents) into an arbitrary order.
	for _, want := range []string{
		`ROW_NUMBER() OVER (ORDER BY s."c0" COLLATE "C" ASC NULLS FIRST, ` +
			`__loom_route.term_id COLLATE "C" ASC NULLS FIRST, __loom_r1_cod.rn ASC NULLS FIRST) - 1`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("source text lost the total order:\n want: %s\n got: %s", want, sql)
		}
	}

	// The Coding array is read AT the iterated selector step, inclusive of
	// its own field (unnest.go:540-560, whose AQL twin is the FOR over
	// current.field): reading the container above the [] step would gather
	// an empty array on real data, silently.
	if got := r.args[7]; string(got.Type) != "text" || got.Value != "{valueCodeableConcept,coding}" {
		t.Fatalf("the Coding array must bind the path through the iterated step, got %#v", got)
	}

	// The scope chains: anchor and reached-target rows each carry the
	// canonical project/generation/auth/type predicates (scope.go emitted,
	// not plan text): count of resource_type binds ≥ 2 (anchor + target).
	if got := strings.Count(sql, "resource_type\" = $"); got < 2 {
		t.Fatalf("anchor and target scope predicates missing (resource_type conjuncts = %d):\n%s", got, sql)
	}

	// Selected scalar projection reads the materialized route document.
	if !strings.Contains(sql, "__loom_route.doc #> $") {
		t.Fatalf("scalar projection must read the materialized route document:\n%s", sql)
	}
	if _, ok := region.columns["related_status"]; !ok {
		t.Fatalf("adapter column related_status missing: %v", region.columns)
	}

	// The Coding expansion is iterated from the route document, and the
	// zero-based ordinal is the dense survivor position minus one.
	if !strings.Contains(sql, `(__loom_r1_cod.rn - 1)::int`) {
		t.Fatalf("Coding ordinal must be the dense position minus one:\n%s", sql)
	}

	// The two has-item witnesses, exactly: has-item conjoins the route
	// arrival WITH the Coding survivor (the AQL routeHasItem && hasItem),
	// and the route-item witness is the arrival alone. Dropping either half
	// silently changes what the downstream aggregate can filter on.
	for _, want := range []string{
		`(CASE WHEN (__loom_route.term_id IS NOT NULL AND __loom_r1_cod.rn IS NOT NULL) THEN true ELSE false END) AS "__loom_r1_h"`,
		`(CASE WHEN __loom_route.term_id IS NOT NULL THEN true ELSE false END) AS "__loom_r1_r"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("has-item witness drifted:\n want: %s\n got: %s", want, sql)
		}
	}

	// The occurrence key object carries the four AQL members through
	// to_jsonb, and the witnesses are total two-valued booleans.
	for _, want := range []string{
		"jsonb_build_object(", "to_jsonb((s.\"c0\"))", "to_jsonb((__loom_route.term_id))",
		`"__loom_r1_k"`, `"__loom_r1_h"`, `"__loom_r1_r"`, `"__loom_r1_n"`,
		"__loom_route.term_id IS NOT NULL",
		"__loom_r1_cod.rn IS NOT NULL",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("source text missing witness fragment:\n want: %s\n got: %s", want, sql)
		}
	}

	// The Coding EXCLUDE policy drops matched-route-empty rows but keeps
	// the unmatched pad row (AQL `!routeHasItem OR hasItem`).
	if !strings.Contains(sql, "CASE WHEN NOT (__loom_route.term_id IS NOT NULL) OR __loom_r1_cod.rn IS NOT NULL THEN 1 ELSE 0 END") {
		t.Fatalf("Coding EXCLUDE conjunct drifted:\n%s", sql)
	}

	// The registered total order: source ORDER BY the parent key then the
	// rank, and the composition refinement names the same materialized
	// columns in the same order.
	if !strings.HasSuffix(sql, `ORDER BY s."c0" ASC, __loom_rel."__loom_r1_n" ASC`) {
		t.Fatalf("source text must end in the parent-then-rank order:\n%s", sql)
	}
	// The order terms carry the parent identity BEFORE the rank: the rank is
	// the position within one parent's expansion, so the terms must be the
	// parent key then the rank (one term would interleave every parent's
	// first item — not a total order).
	if len(region.orderTerms) != 2 || region.orderTerms[0] != `s."c0" ASC` ||
		region.orderTerms[1] != `s."__loom_r1_n" ASC` {
		t.Fatalf("order terms = %v, want [s.\"c0\" ASC s.\"__loom_r1_n\" ASC]", region.orderTerms)
	}

	// Adapter-owned reads address materialized columns only (the share law
	// at the reference level): every adapter column ref is unqualified.
	for name, column := range region.columns {
		if !strings.HasPrefix(column.ref, `"`) || strings.Contains(column.ref, ".") {
			t.Fatalf("adapter column %q ref %q is not the materialized unqualified read", name, column.ref)
		}
	}

	// Parameterization law: plan data crosses only through $n.
	for _, forbidden := range []string{"'Patient'", "'Observation'", "'subject_Reference'",
		"'valueCodeableConcept'", "'coding'", "'final'", "'inputrow'", "'observation_doc'", "'fhir_edge'"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("plan data reached SQL text (%s):\n%s", forbidden, sql)
		}
	}
	bound := sql16roBoundTexts(r)
	// The registry-validated names (the edge collection, the anchor
	// collection) never reach SQL text at all: they are checked against the
	// registry and the physical relation name is registry-derived
	// (traversal.go:170-173), so they are not parameters either.
	for _, want := range []string{"Patient", "Observation", "subject_Reference",
		"{valueCodeableConcept,coding}", "{}", "root_id", "terminal_id", "item_present", "ordinal",
		"system", "version", "code", "display", sql16roProject, sql16roGeneration} {
		if !bound[want] {
			t.Fatalf("plan value %q was never bound as a parameter; args=%v", want, r.args)
		}
	}
}

// ---------------------------------------------------------------------------
// Share law (S9 killer cell): the expansion binds to its own prior relation.
// There is no cross-call memo, so borrowing is structurally impossible; a
// reintroduced memo keyed by route ID would make this cell RED because the
// second relation's expansion would come back carrying the first relation
// and its key column.
// ---------------------------------------------------------------------------

func TestSQL16RelatedOccurrenceShareLaw(t *testing.T) {
	plan, stage, facts := sql16roFixture(t)

	regionA, _ := sql16roRender(t, plan, stage, "__loom_stage_0", facts)
	otherFacts := map[string]stageColumnFact{"_key": {alias: "c7"}}
	cloneStage := stage
	regionB, _ := sql16roRender(t, plan, cloneStage, "__loom_stage_7", otherFacts)

	if !strings.Contains(regionA.sourceText, "__loom_stage_0 AS s") ||
		!strings.Contains(regionA.sourceText, `s."c0"`) {
		t.Fatalf("expansion A must bind relation 0 with its own key column:\n%s", regionA.sourceText)
	}
	if !strings.Contains(regionB.sourceText, "__loom_stage_7 AS s") ||
		!strings.Contains(regionB.sourceText, `s."c7"`) {
		t.Fatalf("expansion B must bind relation 7 with its own key column:\n%s", regionB.sourceText)
	}
	// Neither expansion carries the other's relation or key read.
	if strings.Contains(regionA.sourceText, "__loom_stage_7") || strings.Contains(regionA.sourceText, `s."c7"`) {
		t.Fatalf("expansion A borrowed the second relation:\n%s", regionA.sourceText)
	}
	if strings.Contains(regionB.sourceText, "__loom_stage_0") || strings.Contains(regionB.sourceText, `s."c0"`) {
		t.Fatalf("expansion B borrowed the first relation (share violation):\n%s", regionB.sourceText)
	}

	// Determinism: a fresh renderer over the same inputs renders byte-
	// identical SQL.
	regionC, _ := sql16roRender(t, plan, stage, "__loom_stage_0", facts)
	if regionC.sourceText != regionA.sourceText {
		t.Fatalf("rendering is not deterministic:\nA: %s\nC: %s", regionA.sourceText, regionC.sourceText)
	}
}

// ---------------------------------------------------------------------------
// Empty-policy cells (construction_group.go:532-554 parity).
// ---------------------------------------------------------------------------

func TestSQL16RelatedOccurrencePolicies(t *testing.T) {
	cases := []struct {
		name         string
		routePolicy  ir.PhysicalUnnestEmptyPolicy
		codingPolicy ir.PhysicalUnnestEmptyPolicy
		want         []string
		deny         []string
	}{
		{"preserve-exclude", ir.PhysicalUnnestPreserveParent, ir.PhysicalUnnestExclude, []string{
			"FROM (SELECT 1) AS __loom_one",
			"LEFT JOIN LATERAL (SELECT",
			"(CASE WHEN NOT (__loom_route.term_id IS NOT NULL) OR __loom_r1_cod.rn IS NOT NULL THEN 1 ELSE 0 END) > 0",
		}, []string{"CONSTRUCTION_EXPANSION_EMPTY", "INNER JOIN LATERAL"}},
		{"exclude-route", ir.PhysicalUnnestExclude, ir.PhysicalUnnestPreserveParent, []string{
			"INNER JOIN LATERAL (SELECT DISTINCT ON",
			") AS __loom_route ON TRUE",
			"INNER JOIN LATERAL (SELECT (__loom_route.doc",
		}, []string{"CONSTRUCTION_EXPANSION_EMPTY"}},
		{"error-route", ir.PhysicalUnnestError, ir.PhysicalUnnestPreserveParent, []string{
			"FROM (SELECT 1) AS __loom_one",
			"CONSTRUCTION_EXPANSION_EMPTY",
			"(CASE WHEN __loom_route.term_id IS NOT NULL THEN 1 ELSE CAST('CONSTRUCTION_EXPANSION_EMPTY'",
		}, nil},
		{"error-coding", ir.PhysicalUnnestPreserveParent, ir.PhysicalUnnestError, []string{
			"(CASE WHEN NOT (__loom_route.term_id IS NOT NULL) OR __loom_r1_cod.rn IS NOT NULL THEN 1 ELSE CAST('CONSTRUCTION_EXPANSION_EMPTY'",
			"CONSTRUCTION_EXPANSION_EMPTY",
		}, []string{"THEN 1 ELSE 0 END) > 0"}},
	}
	for _, cell := range cases {
		t.Run(cell.name, func(t *testing.T) {
			plan, stage, facts := sql16roFixture(t)
			adapter := stage.Group.RelatedOccurrence
			adapter.RelatedRecords.EmptyPolicy = cell.routePolicy
			if adapter.CodingUnnest != nil {
				adapter.CodingUnnest.EmptyPolicy = cell.codingPolicy
			}
			adapter.CodingEmptyPolicy = cell.codingPolicy
			region, _ := sql16roRender(t, plan, stage, "__loom_stage_0", facts)
			for _, want := range cell.want {
				if !strings.Contains(region.sourceText, want) {
					t.Fatalf("missing policy fragment %q in:\n%s", want, region.sourceText)
				}
			}
			for _, denied := range cell.deny {
				if strings.Contains(region.sourceText, denied) {
					t.Fatalf("policy must not emit %q in:\n%s", denied, region.sourceText)
				}
			}
		})
	}
}

// No-Coding adapter: item_present is the AQL false literal, ordinal null,
// and no membership or coding lateral is emitted.
func TestSQL16RelatedOccurrenceGoldenNoCoding(t *testing.T) {
	plan, stage, facts := sql16roFixture(t)
	adapter := stage.Group.RelatedOccurrence
	adapter.CodingSelector = ""
	adapter.CodingUnnest = nil
	adapter.CodingArrayColumn = ""
	adapter.CodingMembershipColumn = ""
	adapter.CodingEmptyPolicy = ""
	adapter.OrdinalSemantics = ""
	delete(facts, "membership")
	// An adapter without a Coding selector declares only scalars: the
	// target projection drops the array field with it (an unread array
	// outside the adapter arm is a typed refusal, tested in the battery).
	projections := stage.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Return.Object.Fields
	stage.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Return.Object.Fields = projections[:3]
	region, _ := sql16roRender(t, plan, stage, "__loom_stage_0", facts)
	sql := region.sourceText
	if strings.Contains(sql, "jsonb_array_elements") && strings.Contains(sql, "WITH ORDINALITY") {
		t.Fatalf("no-Coding adapter must not emit a Coding lateral:\n%s", sql)
	}
	if strings.Contains(sql, "__loom_r1_cod") {
		t.Fatalf("no-Coding adapter must not reference the Coding lateral:\n%s", sql)
	}
	if !strings.Contains(sql, "to_jsonb((false))") || !strings.Contains(sql, "to_jsonb((NULL::int))") {
		t.Fatalf("AQL parity: item_present false and ordinal null without a Coding (construction_group.go:590-593):\n%s", sql)
	}
	// Without the Coding the rank refines only on the terminal identity.
	if strings.Contains(sql, "__loom_r1_cod.rn ASC") {
		t.Fatalf("rank must not order by the absent Coding:\n%s", sql)
	}
	if _, ok := region.columns["occurrence"]; !ok {
		t.Fatalf("occurrence key column must remain registered")
	}
}

// ---------------------------------------------------------------------------
// Typed refusal battery: every shape outside the closed slice refuses with
// ErrUnsupportedPostgresPlan and no partial render (the cell renders, the
// mutation must refuse).
// ---------------------------------------------------------------------------

func TestSQL16RelatedOccurrenceRefusals(t *testing.T) {
	type cell struct {
		name   string
		mutate func(*ir.PhysicalConstructionStage, map[string]stageColumnFact, *ir.PhysicalPlan)
		want   string
	}
	cells := []cell{
		{"no-adapter", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence = nil
		}, "names one"},
		{"anchor-kind", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.AnchorKind = "relatedRecord"
		}, "anchor kind"},
		{"anchor-column", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.AnchorColumnID = "_id"
		}, "materialized root _key"},
		{"contributor-set-drift", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.RootContributorColumn = "contributors"
		}, "materialized root _key"},
		{"multiplicity", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.Multiplicity = "ALL_TARGETS"
		}, "multiplicity"},
		{"route-id-mismatch", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.RouteID = "other-route"
		}, "same non-empty route"},
		{"cid-bind-missing", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			delete(p.BindVars, "ro_cid")
		}, "construction ID bind"},
		{"hop-discontinuous", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.Route[0].ToNodeID = "other-node"
		}, "does not end at its declared target"},
		{"hop-match-mode", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.Route[0].MatchMode = "MAYBE"
		}, "incomplete or discontinuous"},
		{"ordinal-semantics", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.OrdinalSemantics = "ONE_BASED"
		}, "zero-based flattened position"},
		{"coding-details-without-selector", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.CodingSelector = ""
		}, "without a Coding selector"},
		{"coding-expression-drift", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.CodingUnnest.Expression.Value.Variable = "someone_elses_item"
		}, "exact ordinal operation"},
		{"coding-policy-unknown", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.CodingEmptyPolicy = "SILENT"
			s.Group.RelatedOccurrence.CodingUnnest.EmptyPolicy = "SILENT"
		}, "related Coding empty policy"},
		{"route-policy-unknown", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.EmptyPolicy = "SILENT"
		}, "route empty policy"},
		{"captures-drift", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Captures = []string{"someone_else"}
		}, "capture exactly"},
		{"sort-not-terminal-id", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Sort.Path = []string{"_key"}
		}, "sorted by the reached target's {_id}"},
		{"not-unique", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Unique = false
		}, "UNIQUE and sorted"},
		{"anchor-scan-missing", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			ops := s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations = ops[1:]
		}, "open with the root-collection anchor scan"},
		{"anchor-collection-bind-drift", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			p.BindVars["ro_anchor_collection"] = "Observation"
		}, "bound to the anchor resource type"},
		{"anchor-equality-drift", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations[1].Filter.Predicate.Left.Path = []string{"_id"}
		}, "exact root-key equality"},
		{"anchor-equality-operator", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations[1].Filter.Predicate.Operator = "IN"
		}, "exact root-key equality"},
		{"anchor-scope-block-defect", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			ops := s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations = append(ops[:3], ops[4:]...)
		}, "anchor scope block"},
		{"traversal-any-direction", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations[6].Traversal.Direction = ir.PhysicalAny
		}, "ANY-direction"},
		{"traversal-endpoint-lookup", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations[6].Traversal.Strategy = ir.PhysicalTraversalEndpointLookup
		}, "endpoint-lookup traversal payload is incomplete"},
		{"endpoint-lookup-join-field-missing", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			traversal := s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations[6].Traversal
			traversal.Strategy = ir.PhysicalTraversalEndpointLookup
			traversal.EndpointField = "subject"
		}, "payload is incomplete"},
		{"edge-bind-drift", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			p.BindVars["ro_edge_collection"] = "fhir_edge_two"
		}, "must be bound to \"fhir_edge\""},
		{"hop-label-mismatch", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.Route[0].Relationship = "other_Reference"
		}, "names relationship"},
		{"hop-storage-direction-drift", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.Route[0].StorageDirection = "OUTBOUND"
		}, "storage direction changed"},
		{"trailing-operation-shape", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations = append(
				s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations,
				ir.PhysicalOperation{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
					Variable: "extra", Operator: "UNIQUE",
					Inputs: []ir.PhysicalValue{{Variable: "observation_doc", Path: []string{"payload"}}},
				}})
		}, "only typed contributor FILTERs"},
		{"terminal-id-field-missing", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			fields := s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Return.Object.Fields
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Return.Object.Fields = fields[1:]
		}, "lacks its terminal_id"},
		{"terminal-id-wrong-path", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			field := &s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Return.Object.Fields[0]
			field.Expression.Value.Path = []string{"_key"}
		}, "must read the target's {_id}"},
		{"scalar-unknown-field", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.ScalarProjections[0].ResultField = "no_such_field"
		}, "which the target projection does not declare"},
		{"scalar-owner-drift", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.ScalarProjections[0].OwnerResourceType = "Condition"
		}, "validates its selector against"},
		{"coding-column-unnest-drift", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			s.Group.RelatedOccurrence.CodingArrayColumn = "status"
		}, "exact ordinal operation"},
		{"coding-column-undeclared", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			fields := s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Return.Object.Fields
			s.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Return.Object.Fields = fields[:3]
		}, "is not declared by the target projection"},
		{"undeclared-array-field", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			adapter := s.Group.RelatedOccurrence
			adapter.CodingSelector = ""
			adapter.CodingUnnest = nil
			adapter.CodingArrayColumn = ""
			adapter.CodingMembershipColumn = ""
			adapter.CodingEmptyPolicy = ""
			adapter.OrdinalSemantics = ""
		}, "SCALAR cardinality only"},
		{"anchor-fact-missing", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			delete(f, "_key")
		}, "not among the prior stage's registered columns"},
		{"anchor-fact-jsonb", func(s *ir.PhysicalConstructionStage, f map[string]stageColumnFact, p *ir.PhysicalPlan) {
			f["_key"] = stageColumnFact{alias: "c0", jsonb: true}
		}, "not the materialized root-key scalar"},
	}
	// One ALIVE control first: the canonical fixture renders.
	{
		plan, stage, facts := sql16roFixture(t)
		sql16roRender(t, plan, stage, "__loom_stage_0", facts)
	}
	for _, item := range cells {
		t.Run(item.name, func(t *testing.T) {
			plan, stage, facts := sql16roFixture(t)
			item.mutate(&stage, facts, &plan)
			r := &planRenderer{
				storage: mustBinding(t), rows: map[string]rowScope{},
				parameters: map[string]int{}, canonicalAuthLets: map[string]string{},
			}
			if err := r.loadScope(plan); err != nil {
				t.Fatalf("loadScope: %v", err)
			}
			_, err := r.renderStageGroupRelatedOccurrence(plan, stage, 1, "__loom_stage_0", facts)
			wantUnsupported(t, err, item.name)
			if !strings.Contains(err.Error(), item.want) {
				t.Fatalf("%s: refusal must cite %q, got: %v", item.name, item.want, err)
			}
		})
	}
}

// The contributor FILTER renders through the shared scoped predicate
// machinery over the reached row, staying a parameterized conjunct.
func TestSQL16RelatedOccurrenceContributorFilter(t *testing.T) {
	plan, stage, facts := sql16roFixture(t)
	subplan := &stage.Group.RelatedOccurrence.RelatedRecords.RelatedRecords
	subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
		Kind: ir.PhysicalFilterOp,
		Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalComparisonPredicate,
			Comparison: &ir.PhysicalPredicate{
				Operator: "EQUALS",
				LeftExpression: &ir.PhysicalExpression{
					Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
					NullBehavior: ir.PhysicalEmptyOnNull,
					Extract: &ir.PhysicalExtract{
						Source:        ir.PhysicalValue{Variable: "observation_doc", Path: []string{"payload"}},
						ResourceType:  "Observation",
						Selector:      sql16roSelectorNameOnly("category.coding.system"),
						ExecutionMode: ir.PhysicalSelectorDirectScalar,
					},
				},
				Right:     &ir.PhysicalValue{BindKey: "ro_contributor_value"},
				ValueKind: spec.FilterString,
			},
		}},
	})
	region, renderer := sql16roRender(t, plan, stage, "__loom_stage_0", facts)
	// The conjunct is the package's single selector-predicate shape
	// (traversal_test.go:399-422 parity), evaluated over the reached
	// target row's payload: the value whole in a one-element list, ANY
	// quantified, correlated to x2 — never a second predicate authority.
	want := `AND (EXISTS (SELECT 1 FROM jsonb_array_elements((CASE WHEN (x2."payload" #> $7::text[]) IS NULL ` +
		`OR jsonb_typeof((x2."payload" #> $7::text[])) = 'null' THEN '[]'::jsonb ` +
		`ELSE jsonb_build_array((x2."payload" #> $7::text[])) END)) AS __sv(v) WHERE (__sv.v) = ($8::jsonb)))`
	if !strings.Contains(region.sourceText, want) {
		t.Fatalf("contributor conjunct missing %q:\n%s", want, region.sourceText)
	}
	// The conjunct addresses the reached row (x2), so it must be composed
	// inside the route subquery, never promoted to the outer lateral.
	conjunct := strings.Index(region.sourceText, "AS __sv(v)")
	routeEnd := strings.Index(region.sourceText, "AS __loom_route ON TRUE")
	if conjunct < 0 || routeEnd < 0 || conjunct > routeEnd {
		t.Fatalf("contributor conjunct escaped the route subquery:\n%s", region.sourceText)
	}
	// The value itself never reaches SQL text.
	if strings.Contains(region.sourceText, "'final'") {
		t.Fatalf("contributor value reached SQL text:\n%s", region.sourceText)
	}
	if len(renderer.args) < 9 {
		t.Fatalf("args = %d, want the contributor path and value parameters", len(renderer.args))
	}
	if got := renderer.args[6]; string(got.Type) != "text" || got.Value != "{category,coding,system}" {
		t.Fatalf("contributor path argument = %#v, want text {category,coding,system}", got)
	}
	if raw, ok := renderer.args[7].Value.(json.RawMessage); !ok || string(raw) != `"final"` {
		t.Fatalf("contributor value argument = %#v, want jsonb \"final\"", renderer.args[7].Value)
	}
}

// The endpoint-lookup strategy is a planner label: a fully named payload
// (the shape the default optimization policy emits, lower/traversal.go:143
// -147) must render byte-identically to the native join — the registered
// PostgreSQL posture normalizes it (graph.go:340-349 +
// graph20NativeStrategyTraversal precedent) instead of refusing a plan the
// production channel emits for every default-policy compile.
func TestSQL16RelatedOccurrenceEndpointLookupEquivalence(t *testing.T) {
	plan, stage, facts := sql16roFixture(t)
	baseline, baselineRenderer := sql16roRender(t, plan, stage, "__loom_stage_0", facts)
	traversal := stage.Group.RelatedOccurrence.RelatedRecords.RelatedRecords.Operations[6].Traversal
	if traversal.Strategy != ir.PhysicalTraversalNative {
		t.Fatalf("fixture hop is not native: %q", traversal.Strategy)
	}
	traversal.Strategy = ir.PhysicalTraversalEndpointLookup
	traversal.EndpointField = "subject"
	traversal.EndpointJoinField = "logical_id"
	traversal.EndpointIndexFields = []string{"project", "dataset_generation"}
	endpoint, endpointRenderer := sql16roRender(t, plan, stage, "__loom_stage_0", facts)
	if endpoint.sourceText != baseline.sourceText {
		t.Fatalf("endpoint-lookup normalization changed the statement:\n%s\n---\n%s", endpoint.sourceText, baseline.sourceText)
	}
	if len(endpoint.columns) != len(baseline.columns) || len(endpointRenderer.args) != len(baselineRenderer.args) {
		t.Fatalf("endpoint-lookup normalization changed the column/arg shape: %d/%d vs %d/%d",
			len(endpoint.columns), len(endpointRenderer.args), len(baseline.columns), len(baselineRenderer.args))
	}
	for name, column := range baseline.columns {
		if other, ok := endpoint.columns[name]; !ok || other.expr != column.expr {
			t.Fatalf("adapter column %q drifted under normalization", name)
		}
	}
}
