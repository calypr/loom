package postgres

// pg-sql-16 G2 (L-13.3 closure) offline legs: the registered semantic-family
// Group key renders the membership walk, matcher, per-row dedup, zero-match
// fallback row, tuple identity, code policy, and the group label over the
// production-lowered inline GROUP plan with a hand-carried resolved family
// (production authoring stays refused at recipe.Bundle.Validate —
// "semanticCategory is not a resolved BINDING" — which is L-13.3's own
// probe receipt, cited in group.go's register).

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

const (
	sql16SemanticOutput   = "category"
	sql16SemanticSystem   = "http://loom.test/fhir/CodeSystem/finding"
	sql16SemanticPath     = "code.coding[]"
	sql16SemanticBinding  = "binding-observation-finding"
	sql16SemanticRule     = "rule-v3"
	sql16SemanticFamily   = "family-observation-finding"
	sql16SemanticIdentity = "1.4.2"
)

// sql16SemanticAttach mounts a fully resolved family onto an already
// lowered inline GROUP plan and declares the label column in both the stage
// and the terminal schema (ir/physical_stage_validation.go:167-171,286).
func sql16SemanticAttach(t *testing.T, plan *ir.PhysicalPlan, version string) {
	t.Helper()
	if plan.StageSequence == nil || len(plan.StageSequence.Stages) != 1 {
		t.Fatalf("expected the inline single-stage group shape")
	}
	stage := &plan.StageSequence.Stages[0]
	group := stage.Group
	if group == nil {
		t.Fatalf("group payload missing")
	}
	if len(plan.Operations) == 0 || plan.Operations[0].RootScan == nil {
		t.Fatalf("root scan missing")
	}
	scan := plan.Operations[0].RootScan
	resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string)
	if !ok || resourceType == "" {
		t.Fatalf("root collection bind %q carries no resource type", scan.CollectionBindKey)
	}
	// The label is a public string column: clone the lowered string output's
	// compiled shape rather than inventing one.
	template := ir.PhysicalStageColumn{}
	for _, column := range stage.OutputColumns {
		if column.Kind == "string" {
			template = column
			break
		}
	}
	if template.Name == "" {
		t.Fatalf("the base plan carries no string output to clone")
	}
	label := template
	label.ID = sql16SemanticOutput
	label.Name = sql16SemanticOutput
	label.Internal = false
	insertBefore := func(columns []ir.PhysicalStageColumn) []ir.PhysicalStageColumn {
		for index := range columns {
			if columns[index].Name == groupRowIDColumn {
				columns = append(columns, ir.PhysicalStageColumn{})
				copy(columns[index+1:], columns[index:])
				columns[index] = label
				return columns
			}
		}
		return append(columns, label)
	}
	stage.OutputColumns = insertBefore(stage.OutputColumns)
	plan.StageSequence.FinalColumns = insertBefore(plan.StageSequence.FinalColumns)
	projection := ir.PhysicalProjection{Name: sql16SemanticOutput,
		Value: ir.PhysicalValue{Variable: "group_semantic_label_9"}}
	inserted := false
	for index := range stage.OutputProjections {
		if stage.OutputProjections[index].Name == groupRowIDColumn {
			stage.OutputProjections = append(stage.OutputProjections, ir.PhysicalProjection{})
			copy(stage.OutputProjections[index+1:], stage.OutputProjections[index:])
			stage.OutputProjections[index] = projection
			inserted = true
			break
		}
	}
	if !inserted {
		stage.OutputProjections = append(stage.OutputProjections, projection)
	}
	group.SemanticKey = &ir.PhysicalStageGroupSemanticKey{
		OutputColumn:  sql16SemanticOutput,
		TupleVariable: "group_semantic_tuple_9", LabelVariable: "group_semantic_label_9",
		SourceRootVariable: scan.Variable, SourceIdentityColumn: groupSemanticSourceIdentity,
		RootCollectionBindKey: scan.CollectionBindKey, ResourceType: resourceType,
		CodingPath: sql16SemanticPath,
		PathSegments: []ir.PhysicalCodedGroupPathSegment{
			{Name: "code"}, {Name: "coding", Repeated: true},
		},
		BindingID: sql16SemanticBinding, RuleVersion: sql16SemanticRule,
		FamilyID: sql16SemanticFamily, System: sql16SemanticSystem, Version: version,
	}
}

// sql16SemanticPlan is the healthy registered shape: the keyed COUNT_ROWS
// base plus a versioned semantic family key.
func sql16SemanticPlan(t *testing.T, policy recipe.ConstructionGroupMissingKeyPolicy, version string) ir.PhysicalPlan {
	t.Helper()
	plan := group13CountRowsPlan(t, policy)
	sql16SemanticAttach(t, &plan, version)
	return plan
}

func sql16SemanticRender(t *testing.T, plan ir.PhysicalPlan) (string, []string) {
	t.Helper()
	rendered, err := renderGroupPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("the registered semantic arm must render: %v", err)
	}
	return rendered.SQL, rendered.OutputColumns
}

// sql16RegionRenderer builds the minimal renderer the grouped-aggregate
// region needs, with the root scan's row scope registered the way
// renderRootScan does (population.go:473).
func sql16RegionRenderer(t *testing.T, plan ir.PhysicalPlan) *planRenderer {
	t.Helper()
	renderer := &planRenderer{
		storage: mustBinding(t), parameters: map[string]int{},
		rows: map[string]rowScope{}, canonicalAuthLets: map[string]string{}, rootAlias: "r0",
	}
	renderer.rows[plan.Operations[0].RootScan.Variable] = rowScope{alias: renderer.rootAlias, resourceType: "Observation"}
	return renderer
}

// group13ReturnOp returns the lowered plan's RETURN operation, the source
// projection the inline door materializes columns from.
func group13ReturnOp(t *testing.T, plan ir.PhysicalPlan) *ir.PhysicalReturn {
	t.Helper()
	var ret *ir.PhysicalReturn
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalReturnOp {
			ret = operation.Return
		}
	}
	if ret == nil {
		t.Fatalf("the lowered plan carries no RETURN operation")
	}
	return ret
}

func sql16SemanticRefusal(t *testing.T, plan ir.PhysicalPlan, want string) {
	t.Helper()
	_, err := renderGroupPhysicalPlan(plan, mustBinding(t))
	if err == nil {
		t.Fatalf("refusal required, want %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %q, want substring %q", err.Error(), want)
	}
}

// TestSQL16SemanticGolden pins the registered region shape on the healthy
// plan: the carrier walk, the matcher chain, the per-row dedup, the
// zero-match fallback, the tuple terms, the label, the identity member, and
// the terminal order — with every family string a bound parameter.
func TestSQL16SemanticGolden(t *testing.T) {
	plan := sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyGroup, sql16SemanticIdentity)
	sql, names := sql16SemanticRender(t, plan)

	// --- the walk (construction_group.go:676-693). ---
	for _, want := range []string{
		// IS_ARRAY-guarded expansion per repeated segment, inside one
		// correlated subquery whose aggregate is the raw projection.
		`COALESCE((SELECT jsonb_agg(semx1.item) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(`,
		`) = 'array' THEN `, ` ELSE '[]'::jsonb END) AS semx1(item)), '[]'::jsonb)`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("walk fragment missing: %s\n%s", want, sql)
		}
	}

	// --- the matcher (construction_group.go:713-718). ---
	for _, want := range []string{
		`jsonb_typeof(_mc.coding) = 'object'`,
		`_mc.coding -> $`,
		`= 'string'`,
		`_mc.coding ->> $`,
		`IS NULL`, // the version branch is present in one of its two forms
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("matcher fragment missing: %s", want)
		}
	}
	// The per-row dedup + collapse, and the zero-match fallback row.
	for _, want := range []string{
		`MIN(`, ` COLLATE "C") AS g_sc1`,
		`FROM jsonb_array_elements("csem") AS _mc(coding)`,
		`GROUP BY 1`,
		`UNION ALL SELECT NULL::text, NULL::text, NULL::text, NULL::text WHERE NOT EXISTS (SELECT 1 FROM jsonb_array_elements("csem") AS _me(coding)`,
		`CROSS JOIN LATERAL (`, `) AS g_sm(g_ssy, g_svr, g_sco, g_sdi)`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("dedup/fallback fragment missing: %s\n%s", want, sql)
		}
	}
	// The tuple terms join the key terms and the label is the MIN-or-code
	// reading (:225-228, :253-256, :300-310).
	for _, want := range []string{
		`g_ssy AS g_ssy`, `g_svr AS g_svr`, `g_sco AS g_sco`,
		`CASE WHEN MIN(g_sdi COLLATE "C") IS NULL THEN g_sco ELSE MIN(g_sdi COLLATE "C") END AS g_slb`,
		`GROUP BY 1, 2, 3, 4`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("grouping fragment missing: %s\n%s", want, sql)
		}
	}
	// The identity member (:170-171) and the registered terminal order.
	for _, want := range []string{
		`"semantic_family"`,
		`to_jsonb($`,
		`COALESCE(to_jsonb(g.g_ssy)::text, 'null')`,
		`COALESCE(to_jsonb(g.g_svr)::text, 'null')`,
		`COALESCE(to_jsonb(g.g_sco)::text, 'null')`,
		`g.g_ssy COLLATE "C" ASC NULLS FIRST`,
		`g.g_svr COLLATE "C" ASC NULLS FIRST`,
		`g.g_sco COLLATE "C" ASC NULLS FIRST`,
		` COLLATE "C" ASC`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("identity/order fragment missing: %s\n%s", want, sql)
		}
	}
	// No family string reaches SQL text: they are parameters only.
	for _, secret := range []string{sql16SemanticSystem, sql16SemanticBinding, sql16SemanticRule,
		sql16SemanticFamily, sql16SemanticIdentity, sql16SemanticPath, "coding", "code"} {
		if strings.Contains(sql, "'"+secret+"'") {
			t.Errorf("family string %q reached SQL text", secret)
		}
	}
	// The label is one emitted column beside keys, reducers, and identity.
	if len(names) != 4 || names[2] != sql16SemanticOutput || names[3] != groupRowIDColumn {
		t.Fatalf("output columns = %v", names)
	}
}

// TestSQL16SemanticWalkChain pins the multi-repeated-segment form of the
// walk: each repeated segment adds one IS_ARRAY-guarded
// jsonb_array_elements step, chained CROSS JOIN LATERAL so an emptied
// branch prunes the row exactly as the AQL nested FOR does, inside the one
// correlated carrier subquery.
func TestSQL16SemanticWalkChain(t *testing.T) {
	plan := sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyGroup, sql16SemanticIdentity)
	key := plan.StageSequence.Stages[0].Group.SemanticKey
	key.PathSegments = []ir.PhysicalCodedGroupPathSegment{
		{Name: "extension", Repeated: true}, {Name: "coding", Repeated: true},
	}
	key.CodingPath = "extension[].coding[]"
	sql, _ := sql16SemanticRender(t, plan)
	for _, want := range []string{
		`COALESCE((SELECT jsonb_agg(semx2.item) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(`,
		`) = 'array' THEN `, ` ELSE '[]'::jsonb END) AS semx1(item) CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(semx1.item #> `,
		`::text[]) = 'array' THEN `, ` ELSE '[]'::jsonb END) AS semx2(item)), '[]'::jsonb)`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("chain fragment missing: %s", want)
		}
	}
}

// TestSQL16SemanticVersionBranches pins the two matcher version arms
// (:715-718): a non-blank family version compares the trim-gated read to the
// bind; a blank family version demands the read be null.
func TestSQL16SemanticVersionBranches(t *testing.T) {
	t.Run("versioned-family", func(t *testing.T) {
		sql, _ := sql16SemanticRender(t, sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyGroup, sql16SemanticIdentity))
		if !strings.Contains(sql, "END = $") {
			t.Fatalf("versioned family must compare the gated version read against the bind:\n%s", sql)
		}
		if strings.Contains(sql, "END IS NULL") {
			t.Fatalf("a versioned family must not use the null twin")
		}
	})
	t.Run("versionless-family", func(t *testing.T) {
		sql, _ := sql16SemanticRender(t, sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyGroup, ""))
		if !strings.Contains(sql, "END IS NULL") {
			t.Fatalf("a blank-version family must demand the null twin:\n%s", sql)
		}
	})
}

// TestSQL16SemanticMissingKeyPolicy pins the code policy arms
// (construction_group.go:172-180) with their alive controls.
func TestSQL16SemanticMissingKeyPolicy(t *testing.T) {
	t.Run("group-keeps-null-code", func(t *testing.T) {
		sql, _ := sql16SemanticRender(t, sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyGroup, sql16SemanticIdentity))
		if strings.Contains(sql, "g_sco IS NOT NULL") {
			t.Fatalf("GROUP must keep the null-code membership row:\n%s", sql)
		}
	})
	t.Run("exclude", func(t *testing.T) {
		sql, _ := sql16SemanticRender(t, sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyExclude, sql16SemanticIdentity))
		if !strings.Contains(sql, "g_sco IS NOT NULL") {
			t.Fatalf("EXCLUDE must conjunct the membership code:\n%s", sql)
		}
	})
	t.Run("error", func(t *testing.T) {
		sql, _ := sql16SemanticRender(t, sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyError, sql16SemanticIdentity))
		if !strings.Contains(sql, `(CASE WHEN g_sco IS NOT NULL THEN 1 ELSE CAST('CONSTRUCTION_GROUP_MISSING_KEY'`) {
			t.Fatalf("ERROR must raise the canonical tag over the membership code:\n%s", sql)
		}
	})
	group13AliveControl(t)
}

// TestSQL16SemanticKeylessGrandTotal pins the door law the AQL gate
// (construction_group.go:50-53) states: an in-scope source row admits a
// keyless Group when a semantic family key is present, so the
// COUNT_ROWS-only grand-total restriction does not apply to it.
func TestSQL16SemanticKeylessGrandTotal(t *testing.T) {
	plan := group13KeylessPlan(t)
	sql16SemanticAttach(t, &plan, sql16SemanticIdentity)
	// The base keyless plan is COUNT_ROWS-only plus row values; add a SUM
	// reducer, which the keyless grand total would refuse without the
	// semantic key.
	stage := &plan.StageSequence.Stages[0]
	stage.Group.Aggregates = append(stage.Group.Aggregates, ir.PhysicalStageGroupAggregate{
		Operation: "MIN", InputColumn: "value", Output: "semantic_min", Variable: "group_semantic_min_9",
		InputKind: "DECIMAL", OutputKind: "decimal",
	})
	column := ir.PhysicalStageColumn{ID: "semantic_min", Name: "semantic_min", Kind: "decimal", Cardinality: "optional_one", Nullable: true}
	insert := func(columns []ir.PhysicalStageColumn) []ir.PhysicalStageColumn {
		for index := range columns {
			if columns[index].Name == groupRowIDColumn {
				columns = append(columns, ir.PhysicalStageColumn{})
				copy(columns[index+1:], columns[index:])
				columns[index] = column
				return columns
			}
		}
		return append(columns, column)
	}
	stage.OutputColumns = insert(stage.OutputColumns)
	plan.StageSequence.FinalColumns = insert(plan.StageSequence.FinalColumns)
	minProjection := ir.PhysicalProjection{Name: "semantic_min",
		Value: ir.PhysicalValue{Variable: "group_semantic_min_9"}}
	if stage.OutputProjections[len(stage.OutputProjections)-1].Name == groupRowIDColumn {
		last := len(stage.OutputProjections) - 1
		stage.OutputProjections = append(stage.OutputProjections, ir.PhysicalProjection{})
		copy(stage.OutputProjections[last+1:], stage.OutputProjections[last:])
		stage.OutputProjections[last] = minProjection
	} else {
		stage.OutputProjections = append(stage.OutputProjections, minProjection)
	}
	if _, err := renderGroupPhysicalPlan(plan, mustBinding(t)); err != nil {
		t.Fatalf("a keyless Group carrying a semantic family key must render: %v", err)
	}

	// Alive control on the refusal: the same shape WITHOUT the family key
	// stays the keyless grand-total restriction.
	without := group13KeylessPlan(t)
	withoutStage := &without.StageSequence.Stages[0]
	withoutStage.Group.Aggregates = append(withoutStage.Group.Aggregates, ir.PhysicalStageGroupAggregate{
		Operation: "MIN", InputColumn: "value", Output: "semantic_min", Variable: "group_semantic_min_9",
		InputKind: "DECIMAL", OutputKind: "decimal",
	})
	_, err := renderGroupPhysicalPlan(without, mustBinding(t))
	if err == nil || !strings.Contains(err.Error(), "only keyless COUNT_ROWS groups can consume the source scope") {
		t.Fatalf("keyless-without-family refusal = %v", err)
	}
}

// TestSQL16SemanticDoorRefusals pins the registered arm's fail-closed half:
// every incomplete or inconsistent family is a typed refusal, and each cell
// is paired with the healthy render as the alive control.
func TestSQL16SemanticDoorRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, plan *ir.PhysicalPlan)
		want   string
	}{
		{"missing-binding", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.SemanticKey.BindingID = ""
		}, "the unresolved semantic family Group key is a registered named gap"},
		{"missing-system", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.SemanticKey.System = "  "
		}, "the unresolved semantic family Group key is a registered named gap"},
		{"missing-output", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.SemanticKey.OutputColumn = ""
		}, "missing its output column"},
		{"foreign-input-stage", func(t *testing.T, plan *ir.PhysicalPlan) {
			// The door's own inline law is InputStageID == SourceStageID;
			// moving both off the direct-root stage id keeps that law true
			// and reaches the semantic arm's ir:1480 re-read.
			plan.StageSequence.SourceStageID = "not_the_source_projection"
			plan.StageSequence.Stages[0].InputStageID = "not_the_source_projection"
		}, "direct-root"},
		{"identity-not-key", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.SemanticKey.SourceIdentityColumn = "id"
		}, `identified by "_key"`},
		{"collection-bind-mismatch", func(t *testing.T, plan *ir.PhysicalPlan) {
			key := plan.StageSequence.Stages[0].Group.SemanticKey
			plan.BindVars[key.RootCollectionBindKey] = "ObservationKind"
		}, "does not carry the exact root resource type"},
		{"foreign-collection-bind", func(t *testing.T, plan *ir.PhysicalPlan) {
			key := plan.StageSequence.Stages[0].Group.SemanticKey
			plan.BindVars["other_collection"] = key.ResourceType
			key.RootCollectionBindKey = "other_collection"
		}, "must be the root scan's own collection bind"},
		{"foreign-root-variable", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.SemanticKey.SourceRootVariable = "not_the_root"
		}, "must be the root scan variable"},
		{"path-not-canonical", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.SemanticKey.CodingPath = "code.codings[]"
		}, "canonical join of its segments"},
		{"path-not-repeated-tail", func(t *testing.T, plan *ir.PhysicalPlan) {
			key := plan.StageSequence.Stages[0].Group.SemanticKey
			key.PathSegments = []ir.PhysicalCodedGroupPathSegment{{Name: "code"}, {Name: "coding"}}
		}, "must end in a repeated Coding array"},
		{"path-segment-unsafe", func(t *testing.T, plan *ir.PhysicalPlan) {
			key := plan.StageSequence.Stages[0].Group.SemanticKey
			key.PathSegments = []ir.PhysicalCodedGroupPathSegment{{Name: "code"}, {Name: "coding[]", Repeated: true}}
			key.CodingPath = "code.coding[][]"
		}, "not renderer-safe"},
		{"output-addressed-twice", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.SemanticKey.OutputColumn = "status_group"
		}, "already a registered key, reducer, or row-value output"},
	}
	for _, cell := range cases {
		t.Run(cell.name, func(t *testing.T) {
			plan := sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyGroup, sql16SemanticIdentity)
			cell.mutate(t, &plan)
			sql16SemanticRefusal(t, plan, cell.want)
		})
	}
	t.Run("carrier-name-is-reserved", func(t *testing.T) {
		plan := sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyGroup, sql16SemanticIdentity)
		last := len(plan.Operations) - 1
		plan.Operations[last].Return.Projections = append(plan.Operations[last].Return.Projections,
			ir.PhysicalProjection{Name: groupSemanticCarrierName,
				Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "status"}}})
		sql16SemanticRefusal(t, plan, "reserved for the renderer's semantic carrier column")
	})
	t.Run("family-key-mints-no-carrier-at-composition", func(t *testing.T) {
		// The region refuses a family key whose carrier was never minted
		// (the composition door's own refusal stays the front door; this
		// is the belt behind it).
		plan := sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyGroup, sql16SemanticIdentity)
		stage := plan.StageSequence.Stages[0]
		renderer := sql16RegionRenderer(t, plan)
		columns, err := renderer.materializeGroupSource(plan, group13ReturnOp(t, plan))
		if err != nil {
			t.Fatalf("materialize: %v", err)
		}
		if _, err := renderer.renderGroupAggregateRegion(plan, stage, columns, "SELECT 1", "g_",
			plan.StageSequence.FinalColumns, groupRowIDColumn); err == nil ||
			!strings.Contains(err.Error(), "requires the inline door's root-payload carrier") {
			t.Fatalf("carrier-less family refusal = %v", err)
		}
	})
	t.Run("carrier-without-family", func(t *testing.T) {
		plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
		stage := plan.StageSequence.Stages[0]
		renderer := sql16RegionRenderer(t, plan)
		columns, err := renderer.materializeGroupSource(plan, group13ReturnOp(t, plan))
		if err != nil {
			t.Fatalf("materialize: %v", err)
		}
		carrier, err := renderer.groupSemanticCarrierColumn(
			&ir.PhysicalStageGroupSemanticKey{PathSegments: []ir.PhysicalCodedGroupPathSegment{{Name: "code"}, {Name: "coding", Repeated: true}}})
		if err != nil {
			t.Fatalf("carrier: %v", err)
		}
		if _, err := renderer.renderGroupAggregateRegion(plan, stage, append(columns, *carrier), "SELECT 1", "g_",
			plan.StageSequence.FinalColumns, groupRowIDColumn); err == nil ||
			!strings.Contains(err.Error(), "minted only beside a semantic family key") {
			t.Fatalf("carrier-without-family refusal = %v", err)
		}
	})
	group13AliveControl(t)
}

// TestSQL16SemanticNonSemanticDriftIsZero pins the byte-preservation half:
// the same base plan without a family key renders no membership relation, no
// carrier, and no semantic identity member.
func TestSQL16SemanticNonSemanticDriftIsZero(t *testing.T) {
	sql, _ := sql16SemanticRender(t, group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup))
	for _, forbidden := range []string{
		"_sm(", "_mc(", "_me(", "csem", "semantic_family", "semx1", "COLLECT",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("a plan without a semantic family key must not carry %q:\n%s", forbidden, sql)
		}
	}
}
