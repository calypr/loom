package postgres

// The bounded exact-row lineage contributor page (pg-sql-23) is registered here
// as its own closed plan slice: a plan whose stage sequence carries the
// compiler-minted PhysicalRowLineageReturn terminal over one direct root scan
// and one ordinary GROUP construction stage. Dispatch is the additive
// row-lineage door in RenderPhysicalPlan (after the graph door, before the
// composition door) plus the additive arm in validateStageSequencePlanSurface;
// both call this one renderer, and validateRowLineagePlanSurface is the single
// total contract behind them (render.go / graph.go / group.go / pivot.go /
// stage_sequence.go precedent; entry==dispatch message equality).
//
// AQL ground truth is render/aql/row_lineage.go :10-184 — the canonical
// scalar-Group contributor page, ported branch-for-branch:
//
//   - the two LET sub-queries over the SAME source projection map to two reads
//     of one registered source CTE: __loom_l_grp selects the ONE group whose
//     construction identity equals the requested row id (the AQL
//     FIRST(FOR … COLLECT … SORT … LET identity … FILTER identity == rowID)),
//     and __loom_l_page streams the contributing source rows (the AQL
//     FOR contributor … FILTER key == selected.keyN … SORT occurrenceKey ASC
//     LIMIT @offset, @fetchLimit);
//   - the terminal RETURN {found, contributors, hasMore} maps to the one-row
//     final SELECT, with SLICE(page, 0, @limit) and LENGTH(page) > @limit
//     reproduced by a capped ordered jsonb_agg and a page count over the
//     fetch-limit relation;
//   - the identity is the group slice's OWN construction identity composition
//     (group.go :1005-1044, AQL construction_group.go:113,143,339):
//     TO_STRING([["construction",@cid],["operation","GROUP"],[@columnID_i,key_i],…])
//     rendered through the same to_jsonb/COALESCE-‘null’ member form over the
//     same registered typed key reads. This is the property the whole surface
//     rests on: a row identity the preview statement published must be the
//     only thing a contributor page can be requested by, so the two emitters
//     may not drift. The live round-trip cells (row_lineage_test.go L1) take
//     the identity OUT of the executed preview statement and spend it as the
//     lineage request against the same service; an offline cell pins the
//     rendered identity text against the group slice's own rendering.
//
// Registered surface (rendered):
//
//   - Source region: the group slice's own closed source list, reused through
//     the SAME arms — one direct ROOT_SCAN (validateGroupOperations twin,
//     population/cohort/candidate-seed roots refused), the canonical scope
//     filters, the canonical AUTH_RESOURCE_PATH_ALLOWED let, registered spine
//     filters, at most one SORT then one LIMIT, and the terminal RETURN
//     materialized positionally by materializeGroupSource. The compiler mints
//     the two hidden contributor columns the page reads — the resource-id
//     column over root payload.id and the occurrence-key column over the root
//     document key (compiler/row_lineage.go:842-860) — and both must be among
//     those projections.
//   - Group: one GROUP stage, scalar STRING/INTEGER/DECIMAL/BOOLEAN keys, the
//     three missing-key policies (GROUP groups nulls, EXCLUDE drops them,
//     ERROR raises CONSTRUCTION_GROUP_MISSING_KEY through the guardCast
//     barrier), and the registered key-type witness raising
//     CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH. Reducers and row values are NOT
//     evaluated: the AQL lineage collector groups keys only
//     (render/aql/row_lineage.go:117-129), so a contributor page must raise
//     nothing a contributor page does not also raise in AQL.
//
// Registered page laws:
//
//   - contributor key matching is IS NOT DISTINCT FROM, never =: AQL's == is
//     null-equal (render/aql/row_lineage.go:163), so a GROUP-policy null key
//     must still resolve its own group; SQL = would silently drop it.
//   - the page order is the occurrence-key column COLLATE "C" ASC, the locale-
//     free twin of AQL's byte-wise SORT contributor[@occurrenceKey] ASC
//     (:176); the group selection order reuses the registered key-ASC
//     NULLS-FIRST list with the identity COLLATE "C" tie-break (group.go
//     :1091-1099, D-13c), so a first-wins selection is total, not arbitrary.
//   - the page is capped twice, exactly like the AQL twin: the relation bounds
//     OFFSET @offset / LIMIT @fetch_limit (= limit+1, the lookahead), the
//     published array is the first @limit of it, and hasMore compares the
//     bounded relation's length against @limit. A page can never exceed the
//     requested limit (execution/row_lineage.go:73-75 refuses one that did).
//   - a missing row is not an empty aggregate: found=false with an empty
//     array and hasMore=false is one distinguishable row, and the zero-key
//     group exists only when at least one source row exists (the AQL COLLECT
//     WITH COUNT INTO shape, :86-95), which the HAVING COUNT(*) > 0 arm
//     reproduces — an empty population yields no selectable group, so found
//     stays false rather than becoming a valid empty aggregate row.
//   - contributor identity fields keep the missing / JSON-null / SQL-NULL
//     reading of the source columns: a JSON null or absent payload id reaches
//     the object as JSON null, never as the string "null" and never as a
//     dropped member.
//
// Refusal register (typed ErrUnsupportedPostgresPlan, never partially
// rendered, never an AQL fallback; every one of these is refused on the
// PostgreSQL target TODAY by target.rejectPostgreSQL at compiler/
// row_lineage.go:655, so registering this slice removes no refusal):
//
//   - a traced terminal: the RELATED_EXPAND owner chain, the staged-Pivot
//     preimage and the GROUP→Pivot preimage all carry
//     PhysicalRowLineageTrace; they need the Pivot/related slices and refuse
//     here by name.
//   - any other terminal companion: CellTraceReturn and
//     PopulationMappingReturn keep their registered voices (cell_trace.go:54,
//     population_mapping.go:38; siblings pg-sql-22 and the population-mapping
//     terminal own them).
//   - a Group whose contributors are not ordinary source rows: the semantic
//     family key, the CodingKey identity, the related-occurrence adapter, and
//     the root-contributor set each own a distinct AQL lineage renderer
//     (render/aql/row_lineage.go:186+, :349+; row_lineage_coded_group.go,
//     row_lineage_related_coding.go) with no PostgreSQL route yet.
//   - CODED_GROUP / COHORT_GROUP stages and the composed cohort-group lineage:
//     the coded-group and composition voices (coded_group.go:436,
//     stage_sequence.go:295) are kept verbatim behind the door.
//   - a trailing FILTER suffix over the Group: the AQL group-filter lineage
//     candidate (render/aql/row_lineage.go:532-600) re-renders the whole Group
//     stage over the selected rows; that composition registers with the
//     terminal slice that owns the filter-candidate contract.
//   - preview windowing, OutputAuthResourcePathBindKey, ClickHouse payloads,
//     deferred expression lets, and a non-AQL engine: the slice twins with the
//     same citations.
//
// Registered divergences:
//
//   - D-23a the page reads ONE materialized source relation where AQL re-walks
//     the source query per branch: EQUIVALENT, because the branches are pure
//     reads of the same registered window and the guard raises are static-tag
//     and deterministic.
//   - D-23b the published contributors are one jsonb array value rather than
//     an AQL array document: EQUIVALENT at the declared contract — the
//     execution adapter reads one row with the found/contributors/hasMore
//     columns and decodes the array (execution/row_lineage.go:57-99).

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// Renderer-minted relation names of the contributor-page statement. Like the
// composition prefix they are constants of this file: no plan string ever
// reaches SQL text through them.
const (
	rowLineageSourceRelation = "__loom_l_src"
	rowLineageRowsRelation   = "__loom_l_rows"
	rowLineageGroupRelation  = "__loom_l_grp"
	rowLineageSelectedRow    = "__loom_l_sel"
	rowLineagePageRelation   = "__loom_l_page"
)

// rowLineageContributorAlias is the single relation alias of the contributor
// read; the selected-group read has one row and needs no qualifier.
const rowLineageContributorAlias = "p"

// The two hidden contributor identity columns are minted by the compiler
// (compiler/row_lineage.go:23-24 rowLineageResourceID/rowLineageOccurrenceKey,
// attached at :939-956) with fixed names; a registered page addresses exactly
// these. The texts are renderer-owned constants (groupSemanticCarrierName and
// the composition prefix precedent) and the live L1 round-trip cell ties them
// to the compiler's mint: a name drift makes the page refuse, never render.
const (
	rowLineageResourceIDColumn    = "__loom_row_lineage_resource_id"
	rowLineageOccurrenceKeyColumn = "__loom_row_lineage_occurrence_key"
)

// rowLineageHiddenRead is the registered shape of one compiler-minted
// contributor identity projection (compiler/row_lineage.go:939-956): hidden,
// no expression, no presence claim, a bound root row variable, no bind key,
// and the exact minted document path — ["payload","id"] for the resource id
// and ["_key"] for the document key. The name decides which path is legal, so
// neither column can impersonate the other posture. The two reads never
// reach the shared projection classifier (which would read a leading
// "payload" segment as a payload property): the slice materializes them
// itself, onto the storage forms the spine canonicalizes — the payload
// column's "id" property (the AQL root.payload.id twin) and logical_id
// (canonicalKeyColumn, render.go:172-182).
func rowLineageHiddenRead(projection ir.PhysicalProjection, name string, rootVariable string) bool {
	if !projection.Hidden || projection.Name != name || projection.Expression != nil ||
		projection.PresenceOutput || projection.Presence != nil ||
		projection.Value.Variable != rootVariable || projection.Value.BindKey != "" {
		return false
	}
	switch name {
	case rowLineageResourceIDColumn:
		return len(projection.Value.Path) == 2 &&
			projection.Value.Path[0] == "payload" && projection.Value.Path[1] == "id"
	case rowLineageOccurrenceKeyColumn:
		return len(projection.Value.Path) == 1 && projection.Value.Path[0] == "_key"
	default:
		return false
	}
}

// rowLineageTerminalMentions reports whether RenderPhysicalPlan's row-lineage
// door claims a plan. The predicate claims EXACTLY the registered shape and
// nothing else, so every shape this slice does not render keeps speaking the
// merged voice it spoke before this door existed (the #132 register-split
// voice law, and the reason a hand-assembled or empty terminal, a companion
// CellTraceReturn/PopulationMappingReturn, a CODED_GROUP stage, or a composed
// lineage sequence still falls through to the group, coded-group, pivot, or
// composition door and its byte-identical refusal). The full contract lives
// behind validateRowLineagePlanSurface; this predicate is the claim test, not
// the contract.
func rowLineageTerminalMentions(plan ir.PhysicalPlan) bool {
	if plan.StageSequence == nil {
		return false
	}
	sequence := plan.StageSequence
	terminal := sequence.RowLineageReturn
	if terminal == nil || sequence.CellTraceReturn != nil || sequence.PopulationMappingReturn != nil {
		return false
	}
	if terminal.Trace != nil || terminal.RowIDBindKey == "" || terminal.OffsetBindKey == "" ||
		terminal.LimitBindKey == "" || terminal.FetchLimitBindKey == "" ||
		terminal.ResourceType == "" ||
		terminal.ResourceIDColumn != rowLineageResourceIDColumn ||
		terminal.OccurrenceKeyColumn != rowLineageOccurrenceKeyColumn {
		return false
	}
	if len(sequence.Stages) != 1 {
		return false
	}
	stage := sequence.Stages[0]
	return stage.Kind == ir.PhysicalStageGroupOp && stage.Group != nil
}

// renderRowLineagePhysicalPlan is the single row-lineage door behind both
// entry points (the RenderPhysicalPlan hook and the stage-sequence arm). Gate
// order mirrors the other slice doors: storage validation, then
// validateRowLineagePlanSurface as the first plan-facing act so both doors
// refuse the same shapes with the same typed errors BEFORE plan.Validate can
// speak, then the plan-wide gates, then the builder.
func renderRowLineagePhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateRowLineagePlanSurface(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := plan.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}
	if plan.Engine != "" && plan.Engine != ir.PhysicalEngineAQL {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: engine %q has no PostgreSQL source; the source graph IR is backend-neutral and ClickHouse plans are not PostgreSQL-rendered",
			ErrUnsupportedPostgresPlan, string(plan.Engine))
	}
	if plan.ClickHouseCombine != nil || plan.ClickHousePrefix != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: ClickHouse combine/prefix payloads have no PostgreSQL rendering", ErrUnsupportedPostgresPlan)
	}
	if len(plan.DeferredExpressionLets) != 0 {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: completed plans must not carry deferred expression lets", ErrUnsupportedPostgresPlan)
	}
	renderer := &planRenderer{
		storage:           storage,
		rows:              map[string]rowScope{},
		parameters:        map[string]int{},
		canonicalAuthLets: map[string]string{},
	}
	if err := renderer.loadScope(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	sql, err := renderer.renderRowLineageStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

// validateRowLineagePlanSurface is the total closed-slice contract behind both
// doors. It speaks typed refusals over ANY plan the door claims before plan
// validation, scope loading, or any rendering can reject with an off-type
// error, and it reproduces the registered voices of shapes whose ownership
// stays in another slice (#132 register-split voice law).
func validateRowLineagePlanSurface(plan ir.PhysicalPlan) error {
	if plan.StageSequence == nil {
		return fmt.Errorf("%w: row-lineage dispatch requires a stage sequence", ErrUnsupportedPostgresPlan)
	}
	sequence := plan.StageSequence
	terminal := sequence.RowLineageReturn
	if terminal == nil {
		return fmt.Errorf("%w: row-lineage dispatch requires the bounded contributor-page terminal", ErrUnsupportedPostgresPlan)
	}
	// Companion terminals keep their own doors' registered voices. The
	// row-lineage arm must therefore never claim them: a plan carrying both
	// speaks the companion voice, which is the voice its own ticket tests.
	if sequence.CellTraceReturn != nil {
		return fmt.Errorf("%w: cell-trace returns have no PostgreSQL compiled route (cell_trace.go:54)", ErrUnsupportedPostgresPlan)
	}
	if sequence.PopulationMappingReturn != nil {
		return fmt.Errorf("%w: population-mapping returns have no PostgreSQL compiled route (population_mapping.go:38)", ErrUnsupportedPostgresPlan)
	}
	if len(sequence.Stages) != 1 {
		return fmt.Errorf("%w: the registered contributor-page slice admits exactly one GROUP construction stage; a composed lineage sequence (filter suffix, Pivot preimage) is a separate terminal registration (render/aql/row_lineage.go:532-600, :189+)",
			ErrUnsupportedPostgresPlan,
		)
	}
	if terminal.Trace != nil {
		return fmt.Errorf("%w: a traced row-lineage terminal (RELATED_EXPAND owner chain or Pivot preimage identities) has no PostgreSQL compiled route; it needs the Pivot and related-record terminal surfaces (render/aql/row_lineage_pivot.go, row_lineage_group_pivot.go, row_lineage_composed.go)",
			ErrUnsupportedPostgresPlan)
	}
	if terminal.RowIDBindKey == "" || terminal.OffsetBindKey == "" || terminal.LimitBindKey == "" || terminal.FetchLimitBindKey == "" {
		return fmt.Errorf("%w: the contributor-page terminal requires its row-id, offset, limit, and fetch-limit bind keys", ErrUnsupportedPostgresPlan)
	}
	if strings.TrimSpace(terminal.ResourceType) == "" {
		return fmt.Errorf("%w: the contributor-page terminal requires its resource type", ErrUnsupportedPostgresPlan)
	}
	if terminal.ResourceIDColumn != rowLineageResourceIDColumn || terminal.OccurrenceKeyColumn != rowLineageOccurrenceKeyColumn {
		return fmt.Errorf("%w: the contributor page addresses the compiler-minted identity columns %q and %q (compiler/row_lineage.go:23-24); a page addressing other columns has no registered rendering",
			ErrUnsupportedPostgresPlan, rowLineageResourceIDColumn, rowLineageOccurrenceKeyColumn)
	}
	if _, ok := plan.BindVars[terminal.RowIDBindKey].(string); !ok {
		return fmt.Errorf("%w: contributor-page row id bind %q must be a string", ErrUnsupportedPostgresPlan, terminal.RowIDBindKey)
	}
	for _, bind := range []struct {
		key   string
		label string
	}{
		{terminal.OffsetBindKey, "offset"},
		{terminal.LimitBindKey, "limit"},
		{terminal.FetchLimitBindKey, "fetch limit"},
	} {
		if value, ok := plan.BindVars[bind.key].(int); !ok || value < 0 {
			return fmt.Errorf("%w: contributor-page %s bind %q must be a non-negative int", ErrUnsupportedPostgresPlan, bind.label, bind.key)
		}
	}
	limit := plan.BindVars[terminal.LimitBindKey].(int)
	if limit < 1 {
		return fmt.Errorf("%w: contributor-page limit bind %q must be at least 1", ErrUnsupportedPostgresPlan, terminal.LimitBindKey)
	}
	if plan.BindVars[terminal.FetchLimitBindKey].(int) <= limit {
		return fmt.Errorf("%w: contributor-page fetch limit bind %q must exceed the limit (the hasMore lookahead)", ErrUnsupportedPostgresPlan, terminal.FetchLimitBindKey)
	}
	stage := sequence.Stages[0]
	if stage.Kind != ir.PhysicalStageGroupOp || stage.Group == nil {
		return fmt.Errorf("%w: operation %q has no PostgreSQL contributor-page rendering; only the ordinary GROUP terminal page is registered",
			ErrUnsupportedPostgresPlan, string(stage.Kind))
	}
	if stage.CodedGroup != nil || stage.CohortGroup != nil || stage.Filter != nil || stage.Expand != nil ||
		stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedSource != nil ||
		stage.RelatedExpand != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%w: GROUP requires only a group payload (ir/physical_stage_validation.go:187 re-read at the door)",
			ErrUnsupportedPostgresPlan)
	}
	if stage.InputStageID != sequence.SourceStageID || stage.ID != sequence.FinalStageID {
		return fmt.Errorf("%w: the registered slice admits only the inline first-stage GROUP (InputStageID must equal %q)",
			ErrUnsupportedPostgresPlan, sequence.SourceStageID)
	}
	if stage.RowIdentityColumn == "" || stage.RowIdentityColumn != sequence.FinalRowIdentity {
		return fmt.Errorf("%w: the contributor page is addressed by the final row identity, which stage %q does not carry",
			ErrUnsupportedPostgresPlan, stage.ID)
	}
	group := stage.Group
	if group.SemanticKey != nil {
		return fmt.Errorf("%w: the semantic-family contributor page needs the semantic Group lineage renderer (render/aql/row_lineage.go:349+) and is refused, never partially rendered",
			ErrUnsupportedPostgresPlan)
	}
	if group.CodingKey != nil {
		return fmt.Errorf("%w: the CodingKey contributor page needs the coding-identity lineage renderer (render/aql/row_lineage_coded_group.go, row_lineage_related_coding.go) and is refused, never partially rendered",
			ErrUnsupportedPostgresPlan)
	}
	if group.RelatedOccurrence != nil {
		return fmt.Errorf("%w: the related-occurrence contributor page needs the correlated-hop contracts (L-13.5) and is refused, never partially rendered",
			ErrUnsupportedPostgresPlan)
	}
	if group.RootContributorInputColumn != "" {
		return fmt.Errorf("%w: the root-contributor set is production-unreachable through the PostgreSQL compile target (L-13.6) and is refused, never partially rendered",
			ErrUnsupportedPostgresPlan)
	}
	if group.GroupRowsVariable == "" || group.IdentityVariable == "" {
		return fmt.Errorf("%w: group plan variables must be compiled and non-empty", ErrUnsupportedPostgresPlan)
	}
	if value, ok := plan.BindVars[group.ConstructionIDBindKey].(string); !ok || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: group construction identity bind %q must be a non-empty string",
			ErrUnsupportedPostgresPlan, group.ConstructionIDBindKey)
	}
	switch group.MissingKeyPolicy {
	case ir.PhysicalStageGroupMissingKeyGroup, ir.PhysicalStageGroupMissingKeyExclude, ir.PhysicalStageGroupMissingKeyError:
	default:
		return fmt.Errorf("%w: unsupported missing-key policy %q (construction_group.go:131 parity)",
			ErrUnsupportedPostgresPlan, string(group.MissingKeyPolicy))
	}
	for index, key := range group.Keys {
		switch key.Kind {
		case "STRING", "INTEGER", "DECIMAL", "BOOLEAN":
		default:
			return fmt.Errorf("%w: group key %d has unsupported scalar kind %q",
				ErrUnsupportedPostgresPlan, index, key.Kind)
		}
		if key.InputColumn == "" || key.OutputColumn == "" || key.Variable == "" {
			return fmt.Errorf("%w: group key %d must be fully compiled (input, output, variable)",
				ErrUnsupportedPostgresPlan, index)
		}
		if key.InputInternal || key.FieldSource != nil {
			return fmt.Errorf("%w: group key %d uses a hidden or field-sourced input surface, which the public-scalar lowering gate never emits (L-13.11)",
				ErrUnsupportedPostgresPlan, index)
		}
	}
	if plan.PreviewSourceWindowByRootID || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow {
		return fmt.Errorf("%w: preview source windowing and the terminal Pivot window are terminal windowing contracts and are refused, never silently rendered",
			ErrUnsupportedPostgresPlan)
	}
	if sequence.OutputAuthResourcePathBindKey != "" {
		return fmt.Errorf("%w: the composite Combine prefix authorization path is not registered for the contributor page (ir/physical_stage_sequence.go:25-28; the auth path column registers with pg-ir-03)",
			ErrUnsupportedPostgresPlan)
	}
	if sequence.PreviewLimitBindKey != "" {
		if value, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int); !ok || value <= 0 {
			return fmt.Errorf("%w: contributor-page preview limit bind %q must be a positive int",
				ErrUnsupportedPostgresPlan, sequence.PreviewLimitBindKey)
		}
	}
	returnNames, err := validateGroupOperations(plan)
	if err != nil {
		return err
	}
	if err := validateGroupPayload(stage, plan.BindVars, returnNames); err != nil {
		return err
	}
	// The compiler mints the two hidden contributor identity columns
	// (compiler/row_lineage.go:939-956); a page whose identity columns are not
	// exactly those materialized projections cannot name a contributor at all.
	// The shape is re-read here so the renderer's synthetic materialization
	// (renderRowLineageStatement) only ever consumes the minted form.
	rootVariable := plan.Operations[0].RootScan.Variable
	hiddenSeen := map[string]bool{}
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			switch projection.Name {
			case rowLineageResourceIDColumn, rowLineageOccurrenceKeyColumn:
				if !rowLineageHiddenRead(projection, projection.Name, rootVariable) {
					return fmt.Errorf("%w: contributor identity column %q is not the compiler-minted hidden root read (compiler/row_lineage.go:939-956)",
						ErrUnsupportedPostgresPlan, projection.Name)
				}
				hiddenSeen[projection.Name] = true
			}
		}
	}
	for _, column := range []struct{ name, label string }{
		{rowLineageResourceIDColumn, "resource-id"},
		{rowLineageOccurrenceKeyColumn, "occurrence-key"},
	} {
		if !hiddenSeen[column.name] || !returnNames[column.name] {
			return fmt.Errorf("%w: contributor-page %s column %q is not among the source projections (compiler/row_lineage.go:939-956 mints it)",
				ErrUnsupportedPostgresPlan, column.label, column.name)
		}
	}
	return nil
}

// renderRowLineageStatement renders the whole contributor-page plan: the
// registered source region materialized once, the guarded rows, the keyed
// group relation with the construction identity, the FIRST group matching the
// requested row id, the bounded contributor page, and the one-row
// found/contributors/hasMore terminal.
func (r *planRenderer) renderRowLineageStatement(plan ir.PhysicalPlan) (string, error) {
	sequence := plan.StageSequence
	terminal := *sequence.RowLineageReturn
	stage := sequence.Stages[0]
	group := stage.Group
	dollar := func(index int) string { return "$" + strconv.Itoa(index) }

	// --- source region: the group slice's own arms, untouched. ---
	if err := r.renderRootScan(plan, *plan.Operations[0].RootScan); err != nil {
		return "", err
	}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			canonical, err := r.acceptCanonicalScopeFilter(operation.Filter)
			if err != nil {
				return "", err
			}
			if canonical {
				continue
			}
			clause, err := r.renderFilter(plan, *operation.Filter)
			if err != nil {
				return "", err
			}
			r.where = append(r.where, clause)
		case ir.PhysicalDerivedLetOp:
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", err
			}
			if !canonical {
				return "", fmt.Errorf("%w: operation %q is not part of the registered contributor-page source surface",
					ErrUnsupportedPostgresPlan, string(operation.Kind))
			}
		}
	}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalSortOp:
			if err := r.renderWindowSort(plan, *operation.Sort); err != nil {
				return "", err
			}
		case ir.PhysicalLimitOp:
			if err := r.renderWindowLimit(plan, *operation.Limit); err != nil {
				return "", err
			}
		}
	}
	var returnOp *ir.PhysicalReturn
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalReturnOp {
			returnOp = operation.Return
		}
	}
	// The two compiler-minted identity columns are materialized by this
	// slice, not the shared classifier: their minted document paths are the
	// AQL readings (root.payload.id, root._key) and their storage twins are
	// the payload column's "id" property and logical_id, exactly what the
	// spine's canonicalKeyColumn/documentColumn pair produces for those
	// references. The validator has already refused any other shape.
	kept := make([]ir.PhysicalProjection, 0, len(returnOp.Projections))
	for _, projection := range returnOp.Projections {
		if projection.Name == rowLineageResourceIDColumn || projection.Name == rowLineageOccurrenceKeyColumn {
			continue
		}
		kept = append(kept, projection)
	}
	filteredReturn := *returnOp
	filteredReturn.Projections = kept
	columns, err := r.materializeGroupSource(plan, &filteredReturn)
	if err != nil {
		return "", err
	}
	rootScope, ok := r.rows[plan.Operations[0].RootScan.Variable]
	if !ok || rootScope.derivedValue != "" {
		return "", fmt.Errorf("%w: the contributor page root row does not bind a readable relation", ErrUnsupportedPostgresPlan)
	}
	resourceExpr, err := r.documentPathExpression(rootScope.alias, []string{"id"})
	if err != nil {
		return "", fmt.Errorf("contributor resource-id column: %w", err)
	}
	occurrenceExpr := rootScope.alias + "." + r.storage.Resource.MustColumn("logical_id").SQLText()
	base := len(columns)
	for i, minted := range []struct {
		name  string
		expr  string
		jsonb bool
	}{
		{rowLineageResourceIDColumn, resourceExpr, true},
		{rowLineageOccurrenceKeyColumn, occurrenceExpr, false},
	} {
		alias := "c" + strconv.Itoa(base+i)
		columns = append(columns, groupColumn{
			name: minted.name, alias: alias, expr: minted.expr,
			ref: `"` + alias + `"`, jsonb: minted.jsonb,
		})
	}
	nameToColumn := map[string]*groupColumn{}
	for i := range columns {
		nameToColumn[columns[i].name] = &columns[i]
	}
	resourceIDColumn, err := groupResolveColumn(nameToColumn, terminal.ResourceIDColumn)
	if err != nil {
		return "", fmt.Errorf("contributor-page resource-id column: %w", err)
	}
	occurrenceColumn, err := groupResolveColumn(nameToColumn, terminal.OccurrenceKeyColumn)
	if err != nil {
		return "", fmt.Errorf("contributor-page occurrence-key column: %w", err)
	}

	selectList := make([]string, 0, len(columns))
	for i := range columns {
		selectList = append(selectList, columns[i].expr+` AS "`+columns[i].alias+`"`)
	}
	source := "SELECT " + strings.Join(selectList, ", ") +
		" FROM " + r.storage.Resource.Identifier().SQLText() + " AS " + r.rootAlias
	for _, join := range r.joins {
		source += " " + join
	}
	for _, lateral := range r.laterals {
		source += " " + lateral
	}
	if len(r.where) > 0 {
		source += " WHERE " + strings.Join(r.where, " AND ")
	}
	source += " ORDER BY " + strings.Join(r.windowOrder(), ", ") + r.window.limitClause

	// --- guarded rows: key-type witnesses plus the missing-key policy. ---
	keyReads := make([]string, len(group.Keys))
	keyCollates := make([]string, len(group.Keys))
	var conjuncts []string
	seenConjunct := map[string]bool{}
	addConjunct := func(conjunct string) {
		if seenConjunct[conjunct] {
			return
		}
		seenConjunct[conjunct] = true
		conjuncts = append(conjuncts, conjunct)
	}
	for i, key := range group.Keys {
		column, err := groupResolveColumn(nameToColumn, key.InputColumn)
		if err != nil {
			return "", fmt.Errorf("group key %q: %w", key.OutputColumn, err)
		}
		typedRead, witness, err := r.groupColumnRead(column, key.Kind, groupKeyTypeTag, dollar)
		if err != nil {
			return "", fmt.Errorf("group key %q: %w", key.OutputColumn, err)
		}
		keyReads[i] = typedRead
		if key.Kind == "STRING" {
			keyCollates[i] = ` COLLATE "C"`
		}
		if witness != "" {
			addConjunct(witness)
		}
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyExclude:
			addConjunct(typedRead + " IS NOT NULL")
		case ir.PhysicalStageGroupMissingKeyError:
			addConjunct("(CASE WHEN " + typedRead + " IS NOT NULL THEN 1 ELSE " +
				guardCast(groupMissingKeyTag) + " END) > 0")
		}
	}
	rows := "SELECT * FROM " + rowLineageSourceRelation
	if len(conjuncts) > 0 {
		rows += " WHERE " + strings.Join(conjuncts, " AND ")
	}

	// --- the keyed group relation with the construction identity. ---
	// Identity text is the group slice's own composition (group.go:1005-1044)
	// over the same registered typed key reads: an outer array of member
	// arrays, JSON-compact, every string member double-quoted through to_jsonb,
	// a missing key the literal null. The page must resolve the identity the
	// preview published, so this text is the group emitter's twin, cell for
	// cell, and the live L1 round-trip spends the preview's own identity here.
	constructionID := r.textParameter(bindStringVar(plan.BindVars, group.ConstructionIDBindKey), "")
	identityParts := []string{
		`'["construction",' || to_jsonb(` + dollar(constructionID) + "::text) || ']'",
		`'["operation","GROUP"]'`,
	}
	outputIDs := map[string]string{}
	for _, column := range stage.OutputColumns {
		if _, seen := outputIDs[column.Name]; !seen {
			outputIDs[column.Name] = column.ID
		}
	}
	grpSelects := make([]string, 0, len(keyReads)+1)
	keyAliases := make([]string, 0, len(keyReads))
	for i, key := range group.Keys {
		columnID, ok := outputIDs[key.OutputColumn]
		if !ok {
			return "", fmt.Errorf("%w: group key output %q has no stable column ID (construction_group.go:136-142 parity)",
				ErrUnsupportedPostgresPlan, key.OutputColumn)
		}
		idBind := r.textParameter(columnID, "")
		alias := "l_k" + strconv.Itoa(i)
		grpSelects = append(grpSelects, keyReads[i]+" AS "+alias)
		keyAliases = append(keyAliases, alias)
		// The identity repeats the registered typed read itself, never the
		// select-list alias: PostgreSQL cannot see an alias in a sibling
		// select-list expression, and the repeated expression is exactly
		// grouping key #i, so it stays aggregate-free under GROUP BY.
		identityParts = append(identityParts,
			"'[' || to_jsonb("+dollar(idBind)+"::text) || ',' || COALESCE(to_jsonb("+keyReads[i]+")::text, 'null') || ']'")
	}
	identity := "'[' || " + strings.Join(identityParts, " || ',' || ") + " || ']'"
	grp := "SELECT " + strings.Join(append(grpSelects, identity+" AS l_id"), ", ") +
		" FROM " + rowLineageRowsRelation
	if len(keyAliases) > 0 {
		positions := make([]string, len(keyAliases))
		for i := range positions {
			positions[i] = strconv.Itoa(i + 1)
		}
		grp += " GROUP BY " + strings.Join(positions, ", ")
	} else {
		// The AQL COLLECT WITH COUNT INTO shape (:86-95): the keyless group
		// exists exactly when at least one source row exists, so an empty
		// population has no selectable group and found stays false.
		grp += " HAVING COUNT(*) > 0"
	}

	// --- the FIRST group whose identity is the requested row id. ---
	selected := "SELECT * FROM " + rowLineageGroupRelation + " WHERE l_id = " +
		dollar(r.textParameter(bindStringVar(plan.BindVars, terminal.RowIDBindKey), ""))
	order := make([]string, 0, len(keyAliases)+1)
	for i, alias := range keyAliases {
		order = append(order, alias+keyCollates[i]+" ASC NULLS FIRST")
	}
	order = append(order, `l_id COLLATE "C" ASC`)
	selected += " ORDER BY " + strings.Join(order, ", ") + " LIMIT 1"

	// --- the bounded contributor page. ---
	// The contributor reads are the SAME registered typed reads repointed at
	// the aliased guarded-row relation (reuse by repointing, not string
	// surgery), matched with the null-equal IS NOT DISTINCT FROM that AQL's ==
	// carries.
	contributorMatch := make([]string, 0, len(keyReads))
	for i, key := range group.Keys {
		column, err := groupResolveColumn(nameToColumn, key.InputColumn)
		if err != nil {
			return "", fmt.Errorf("contributor key %q: %w", key.OutputColumn, err)
		}
		repointed := *column
		repointed.ref = rowLineageContributorAlias + "." + column.ref
		typedRead, _, err := r.groupColumnRead(&repointed, key.Kind, groupKeyTypeTag, dollar)
		if err != nil {
			return "", fmt.Errorf("contributor key %q: %w", key.OutputColumn, err)
		}
		contributorMatch = append(contributorMatch,
			typedRead+" IS NOT DISTINCT FROM "+keyAliases[i])
	}
	qualifiedOccurrence := rowLineageContributorAlias + "." + occurrenceColumn.ref
	qualifiedResourceID := rowLineageContributorAlias + "." + resourceIDColumn.ref
	resourceTypeBind := r.textParameter(terminal.ResourceType, "")
	pageSelects := []string{
		dollar(resourceTypeBind) + "::text AS l_resource_type",
		qualifiedResourceID + " AS l_resource_id",
		qualifiedOccurrence + " AS l_occurrence_key",
	}
	page := "SELECT " + strings.Join(pageSelects, ", ") + " FROM " +
		rowLineageRowsRelation + " AS " + rowLineageContributorAlias + ", " + rowLineageSelectedRow
	if len(contributorMatch) > 0 {
		page += " WHERE " + strings.Join(contributorMatch, " AND ")
	}
	// The page orders by the qualified source expression, not its output
	// alias: PostgreSQL admits an output-column name in ORDER BY only as a
	// standalone item, and COLLATE makes it an expression. The cap below
	// may name the alias because there it is an input column.
	page += " ORDER BY " + qualifiedOccurrence + ` COLLATE "C" ASC` +
		" OFFSET " + dollar(r.integerParameter(plan.BindVars[terminal.OffsetBindKey].(int))) +
		" LIMIT " + dollar(r.integerParameter(plan.BindVars[terminal.FetchLimitBindKey].(int)))

	// --- the one-row bounded result contract. ---
	limitBind := r.integerParameter(plan.BindVars[terminal.LimitBindKey].(int))
	contributors := "COALESCE((SELECT jsonb_agg(jsonb_build_object(" +
		"'resourceType', capped.l_resource_type, " +
		"'resourceId', capped.l_resource_id, " +
		"'occurrenceKey', capped.l_occurrence_key" +
		") ORDER BY capped.l_occurrence_key" + ` COLLATE "C" ASC` +
		") FROM (SELECT * FROM " + rowLineagePageRelation +
		" ORDER BY l_occurrence_key" + ` COLLATE "C" ASC` +
		" LIMIT " + dollar(limitBind) + ") AS capped), '[]'::jsonb)"
	found := "(SELECT COUNT(*) > 0 FROM " + rowLineageSelectedRow + ")"
	hasMore := "(SELECT COUNT(*) > " + dollar(limitBind) + " FROM " + rowLineagePageRelation + ")"
	r.outputColumns = []string{"found", "contributors", "hasMore"}

	ctes := []string{
		rowLineageSourceRelation + " AS (" + source + ")",
		rowLineageRowsRelation + " AS (" + rows + ")",
		rowLineageGroupRelation + " AS (" + grp + ")",
		rowLineageSelectedRow + " AS (" + selected + ")",
		rowLineagePageRelation + " AS (" + page + ")",
	}
	return "WITH " + strings.Join(ctes, ", ") + " SELECT " +
		found + ` AS "found", ` + contributors + ` AS "contributors", ` + hasMore + ` AS "hasMore"`, nil
}

// integerParameter binds one bounded page coordinate as a bigint parameter.
// The caller has already refused anything outside the registered range.
func (r *planRenderer) integerParameter(value int) int {
	return r.addParameter(command.PostgreSQLParameter{
		Type:  command.PostgreSQLParameterBigint,
		Value: int64(value),
	}, "")
}
