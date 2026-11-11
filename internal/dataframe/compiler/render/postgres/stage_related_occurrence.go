package postgres

// This file registers the G3(c) related-occurrence Group adapter
// (ir.PhysicalStageGroupRelatedOccurrenceAdapter) as an implemented, closed
// PostgreSQL operation slice, closing the L-13.5 named gap for the composed
// construction door (pg-sql-16 plan §5 G3(c)).
//
// The AQL counterpart is render/aql/construction_group.go
// renderConstructionGroupRelatedOccurrenceInput (:445-615): for every input
// row it runs the Group-owned route as a bounded related-expand subplan
// (renderConstructionRelatedExpandStage, lowered by
// lower/recipe_construction_related_expand.go lowerConstructionRelatedExpand),
// merges the reached target's selected scalars, the unnested Coding
// membership, and the occurrence key into the Group input row, and lets the
// FOR loop multiply the rows the GROUP then aggregates.
//
// The registered PostgreSQL spelling keeps that reading with ONE correlated
// expansion per (stage, route), materialized inside the group slice's own
// source CTE:
//
//   - The hop chain reuses the traversal slice's validated contracts:
//     validateTraversalContract re-reads every discriminator (direction,
//     strategy, EdgeTargetTypeField, the fhir_edge bind, the label and
//     endpoint type binds) and edgeOccurrencePredicates renders both
//     predicate chains of the edge x target occurrence join — the exact
//     INNER edge x target spelling sets.go:82-95 cites as the hop contract.
//     The anchor is the prior relation's materialized root _key, so the
//     first hop correlates through an explicit anchor scan of the root
//     collection: the counterpart of the anchor-prefixed subplan the
//     lowering emits (recipe_construction_related_expand.go:98-130), which
//     sets.go refuses for its hop-only surfaces. Every hop closes with its
//     canonical scope block, consumed by matchCanonicalScopeBlock and
//     ignored (scope.go is the single scope authority, D5).
//   - The route row set is UNIQUE by terminal identity and ordered by it:
//     DISTINCT ON (target logical_id) ORDER BY the same expression under
//     COLLATE "C". That is the subplan's Sort={terminal _id} + Unique
//     contract (recipe_construction_related_expand.go:215-217), and
//     logical_id is the registered counterpart of the Arango document
//     identity (graph.go:99-107 and canonicalKeyColumn, render.go:171-184):
//     the store never materializes the sanitized _key and identity is the
//     raw id by the same ingest contract, so terminal_id and resource_id
//     both read logical_id — the registered residual graph.go registers for
//     ids that required Arango sanitization.
//   - The Coding expansion reuses the unnest slice's registered shape
//     (unnest.go:603-656): jsonb_array_elements(...) WITH ORDINALITY, the
//     terminal stored-JSON-null item filter, and a ROW_NUMBER over the
//     WITH-ORDINALITY position, whose dense survivor position minus one is
//     the zero-based ordinal ir pins
//     (ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY); "rn IS NOT NULL"
//     is the two-valued has-item witness.
//   - The declared Coding empty policy applies only when a target matched,
//     exactly as the AQL reference does (construction_group.go:532-554):
//     GROUP keeps the pad row, EXCLUDE drops it through a WHERE conjunct,
//     ERROR raises CONSTRUCTION_EXPANSION_EMPTY through the registered
//     guard cast.
//   - The occurrence key is the AQL object of construction_group.go:599-607
//     — {root_id, terminal_id, item_present, ordinal} — materialized as
//     jsonb next to the has-item, route-has-item, and rank witnesses. The
//     rank pins the registered total order (terminal identity under
//     COLLATE "C", then the Coding ordinal), it is the ORDER BY of the
//     group slice's source CTE, and it is handed to the composition
//     terminal order as the registered refinement, so a route's row order
//     never depends on PostgreSQL's unspecified scan order.
//
// The one-shared-expansion law: every adapter-owned read of one GROUP stage
// materializes from the SAME lateral subquery — the expansion is rendered
// once per stage and its materialized columns are what that stage's keys,
// reducers, and row values address. There is deliberately NO cross-call
// memo of expansions: a memo keyed by route ID would let one route borrow
// another route's (or another stage's) correlation, which is exactly the
// share violation the S9 mutant plants. The binding of an expansion to its
// own prior relation is therefore structural, and a test re-renders the same
// route over two relations and requires the two expansions to differ.
//
// Refusals (all ErrUnsupportedPostgresPlan, never an AQL fallback, never a
// partial render): a non-root anchor (the root-contributor set is
// production-unreachable, L-13.6; an active related-record anchor is a
// related-route contract), a route subplan carrying an operation outside the
// anchor prefix + canonical hops + typed contributor FILTER, a non-canonical
// anchor equality, a target projection field outside the {terminal_id,
// resource_id} identity pair plus the selected scalars and the one Coding
// array, ANY direction or endpoint-lookup traversal (the traversal.go
// twins), and every empty policy outside the three registered forms.
//
// All plan data still crosses only the $n boundary: resource types, edge
// labels, selector paths, and the occurrence/membership object keys are
// bound parameters; only registry-validated storage columns and
// renderer-minted aliases reach SQL text.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	// stageRelatedCodingOrdinalSemantics is the ir-pinned ordinal contract
	// (physical_stage_validation.go:2078); the adapter refuses anything else
	// rather than number items in the wrong base.
	stageRelatedCodingOrdinalSemantics = "ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY"

	// stageRelatedTerminalField and stageRelatedResourceField are the two
	// identity fields every related-expand target projection carries
	// (recipe_construction_related_expand.go:215-225).
	stageRelatedTerminalField = "terminal_id"
	stageRelatedResourceField = "resource_id"

	// stageRelatedMembershipFields is the AQL membership object's field set
	// (construction_group.go:580-587); the CODING_IDENTITY consumer reads
	// exactly these four names. Its typeof asserts under
	// CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH belong to that key arm (the AQL
	// codingIdentityMembershipTypeAssertions twin, group.go's CODING_IDENTITY
	// registration), not to this emitter: the AQL adapter input rendering
	// carries no membership assert either.
)

// stageRelatedOccurrenceRegion is one rendered adapter: the source SELECT the
// group slice materializes over, the adapter-owned materialized columns
// (keyed by the stage input column that addresses them), and the registered
// total-order refinements the composition terminal appends after the group
// slice's own key + identity order.
type stageRelatedOccurrenceRegion struct {
	// sourceText is the group slice's source SELECT: the prior relation's
	// positional columns plus this adapter's materialized columns.
	sourceText string
	// columns maps each adapter-owned stage input column name onto its
	// materialized column in the group slice's groupColumn form.
	columns map[string]groupColumn
	// orderTerms are the registered total-order refinements, already
	// qualified for the composition terminal alias.
	orderTerms []string
}

// renderStageGroupRelatedOccurrence renders a GROUP stage's
// related-occurrence adapter over the prior stage's relation. The
// composition's GROUP emitter calls it only when
// stage.Group.RelatedOccurrence is non-nil, and then:
//
//   - builds its groupColumn list from region.columns for every
//     adapter-owned input column, and from the prior facts otherwise;
//   - passes region.sourceText to renderGroupAggregateRegion as sourceText
//     in place of "SELECT * FROM <relation>";
//   - appends region.orderTerms to the region's registered ORDER BY.
func (r *planRenderer) renderStageGroupRelatedOccurrence(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	index int,
	relation string,
	facts map[string]stageColumnFact,
) (stageRelatedOccurrenceRegion, error) {
	adapter := stage.Group.RelatedOccurrence
	if adapter == nil {
		return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related-occurrence adapter arm requires a Group payload that names one",
			ErrUnsupportedPostgresPlan)
	}
	if err := validateStageRelatedOccurrenceAdapter(plan, stage); err != nil {
		return stageRelatedOccurrenceRegion{}, err
	}
	related := adapter.RelatedRecords

	// --- the anchor read: the prior relation's materialized root _key. ---
	keyFact, ok := facts[related.AnchorColumnID]
	if !ok {
		return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route anchor column %q is not among the prior stage's registered columns (L-16.1)",
			ErrUnsupportedPostgresPlan, related.AnchorColumnID)
	}
	if keyFact.jsonb {
		return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route anchor column %q is jsonb, not the materialized root-key scalar",
			ErrUnsupportedPostgresPlan, related.AnchorColumnID)
	}
	anchorRead := stageTerminalAlias + "." + `"` + keyFact.alias + `"`

	// --- the route: anchor scan, then the validated edge x target hops. ---
	// The anchor equality correlates through the prior relation's terminal
	// alias: one plan-text substitution, so the route never carries a
	// caller-specific column name of its own.
	route, err := r.renderStageRelatedRoute(plan, stage, anchorRead)
	if err != nil {
		return stageRelatedOccurrenceRegion{}, err
	}

	// Every read of the reached document addresses the materialized route
	// row, never the hop's storage alias: the DISTINCT ON dedup happens
	// below the laterals, so the target projection is evaluated once per
	// deduped item (the UNIQUE-by-terminal contract above).
	docBase := route.alias + ".doc"

	// --- the target projection. ---
	// The identity and selected-scalar fields go through the shared
	// classification contract (selectors.go), which registers the SCALAR
	// selector and value-expression forms only. The one Coding array field
	// is the adapter's own registered arm (the stage slice's
	// stageArraySelectorExpression precedent: a many column is read by the
	// arm that owns its expansion, never by the RETURN classifier), so it
	// splits out before classification and every other field — including an
	// undeclared array — keeps the classifier's typed refusal.
	fields := related.RelatedRecords.Return.Object.Fields
	scalarFields := make([]ir.PhysicalProjection, 0, len(fields))
	var codingProjection *ir.PhysicalProjection
	for fieldIndex := range fields {
		field := fields[fieldIndex]
		if adapter.CodingUnnest != nil && field.Name == adapter.CodingArrayColumn {
			expression := field.Expression
			codingProjection = &ir.PhysicalProjection{Name: field.Name, Expression: &expression}
			continue
		}
		expression := field.Expression
		scalarFields = append(scalarFields, ir.PhysicalProjection{Name: field.Name, Expression: &expression})
	}
	classifiedFields, err := classifyExtractionProjections(scalarFields,
		func(variable string) (string, bool) {
			if variable == route.variable {
				return route.targetType, true
			}
			return "", false
		})
	if err != nil {
		return stageRelatedOccurrenceRegion{}, fmt.Errorf("related route target projection: %w", err)
	}
	classified := make(map[string]classifiedExtractionProjection, len(classifiedFields))
	for _, field := range classifiedFields {
		classified[field.projection.Name] = field
	}
	for _, name := range []string{stageRelatedTerminalField, stageRelatedResourceField} {
		field, present := classified[name]
		if !present {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route target projection lacks its %s identity field (recipe_construction_related_expand.go:218-225 parity)",
				ErrUnsupportedPostgresPlan, name)
		}
		value := identityFieldValue(field)
		if value == nil || value.BindKey != "" || value.Variable != route.variable || len(value.Path) != 1 {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route %s field must read the reached target row directly",
				ErrUnsupportedPostgresPlan, name)
		}
		want := "_id"
		if name == stageRelatedResourceField {
			want = "id"
		}
		if value.Path[0] != want {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route %s field must read the target's {%s} (logical_id is its registered counterpart, graph.go:99-107)",
				ErrUnsupportedPostgresPlan, name, want)
		}
	}

	prefix := "__loom_r" + strconv.Itoa(index) + "_"
	columns := map[string]groupColumn{}
	adapterSelects := make([]string, 0, len(adapter.ScalarProjections)+6)

	// --- the selected scalar projections. ---
	for _, projection := range adapter.ScalarProjections {
		field, present := classified[projection.ResultField]
		if !present {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route scalar projection %q reads target field %q, which the target projection does not declare",
				ErrUnsupportedPostgresPlan, projection.InputColumn, projection.ResultField)
		}
		if field.kind != extractionSelector || field.expression == nil ||
			field.expression.Cardinality != ir.PhysicalScalarCardinality || field.expression.Extract == nil {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route scalar projection %q is not a scalar selector extraction of the reached target document",
				ErrUnsupportedPostgresPlan, projection.InputColumn)
		}
		if field.expression.Extract.ResourceType != projection.OwnerResourceType {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route scalar projection %q validates its selector against %q, not the owner type %q",
				ErrUnsupportedPostgresPlan, projection.InputColumn, field.expression.Extract.ResourceType, projection.OwnerResourceType)
		}
		extraction, err := r.documentPathFromExpression(docBase, field.path)
		if err != nil {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("related route scalar projection %q: %w", projection.InputColumn, err)
		}
		value := "(" + extraction + ")"
		switch field.expression.NullBehavior {
		case ir.PhysicalPreserveNull:
		case ir.PhysicalOmitNulls:
			value = "NULLIF(" + extraction + ", 'null'::jsonb)"
		default:
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route scalar projection %q has null behavior %q; the selected scalar reads are PRESERVE_NULL (the OmitNulls twin is registered, anything else is a capability gap)",
				ErrUnsupportedPostgresPlan, projection.InputColumn, string(field.expression.NullBehavior))
		}
		alias := prefix + "s" + strconv.Itoa(len(columns))
		adapterSelects = append(adapterSelects, value+` AS "`+alias+`"`)
		columns[projection.InputColumn] = groupColumn{
			name: projection.InputColumn, alias: alias, expr: value, ref: `"` + alias + `"`, jsonb: true,
		}
	}

	// --- the Coding expansion, or its documented absence. ---
	codingAlias := prefix + "cod"
	codingSelect := ""
	if adapter.CodingUnnest != nil {
		if codingProjection == nil {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route Coding array column %q is not declared by the target projection",
				ErrUnsupportedPostgresPlan, adapter.CodingArrayColumn)
		}
		expression := codingProjection.Expression
		if expression == nil || expression.Kind != ir.PhysicalExtractExpression ||
			expression.Cardinality != ir.PhysicalArrayCardinality ||
			expression.NullBehavior != ir.PhysicalEmptyOnNull || expression.Extract == nil {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route Coding column %q is not an EMPTY_ON_NULL array selector extraction of the reached target document",
				ErrUnsupportedPostgresPlan, adapter.CodingArrayColumn)
		}
		if expression.Extract.Source.Variable != route.variable {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route Coding column %q must read the reached target row directly",
				ErrUnsupportedPostgresPlan, adapter.CodingArrayColumn)
		}
		if expression.Extract.ResourceType != related.TargetResourceType {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route Coding selector validates against %q, not the target type %q",
				ErrUnsupportedPostgresPlan, expression.Extract.ResourceType, related.TargetResourceType)
		}
		arrayForm, err := r.stageRelatedCodingArrayExpression(docBase, adapter.CodingArrayColumn, expression.Extract)
		if err != nil {
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("related route Coding array: %w", err)
		}
		// The unnest slice's registered expansion (unnest.go:603-656): the
		// WITH-ORDINALITY position distinguishes equal items, the terminal
		// filter drops stored JSON null items so the ROW_NUMBER is dense
		// over survivors, and AQL's item index is that dense position
		// minus one.
		elementAlias := codingAlias + "_e"
		codingSelect = "(SELECT " + elementAlias + ".item AS item, ROW_NUMBER() OVER (ORDER BY " +
			elementAlias + ".ord) AS rn FROM jsonb_array_elements(" + arrayForm +
			") WITH ORDINALITY AS " + elementAlias + "(item, ord) WHERE jsonb_typeof(" + elementAlias +
			".item) <> 'null')"
		membershipArgs := make([]string, 0, 4)
		for _, member := range []string{"system", "version", "code", "display"} {
			memberRead, memberErr := r.documentPathFromExpression(codingAlias+".item", []string{member})
			if memberErr != nil {
				return stageRelatedOccurrenceRegion{}, fmt.Errorf("related route Coding membership: %w", memberErr)
			}
			bind := r.textParameter(member, "text:coding:"+member)
			membershipArgs = append(membershipArgs, "$"+strconv.Itoa(bind)+"::text, "+memberRead)
		}
		membershipSelect := "jsonb_build_object(" + strings.Join(membershipArgs, ", ") + ")"
		membershipAlias := prefix + "m"
		adapterSelects = append(adapterSelects, membershipSelect+` AS "`+membershipAlias+`"`)
		columns[adapter.CodingMembershipColumn] = groupColumn{
			name: adapter.CodingMembershipColumn, alias: membershipAlias, expr: membershipSelect,
			ref: `"` + membershipAlias + `"`, jsonb: true,
		}
	} else if _, declared := facts[adapter.CodingMembershipColumn]; declared {
		return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: the related route declares a Coding membership column without a Coding selector",
			ErrUnsupportedPostgresPlan)
	}

	// --- the occurrence key and its witnesses. ---
	routeHasItem := route.alias + ".term_id IS NOT NULL"
	itemPresent := "false"
	ordinalRead := "NULL::int"
	codingHasItem := ""
	if adapter.CodingUnnest != nil {
		codingHasItem = codingAlias + ".rn IS NOT NULL"
		itemPresent = "(" + routeHasItem + " AND " + codingHasItem + ")"
		ordinalRead = "(" + codingAlias + ".rn - 1)::int"
	}
	keyArgs := make([]string, 0, 4)
	for _, member := range []struct {
		name  string
		value string
	}{
		{"root_id", anchorRead},
		{"terminal_id", route.alias + ".term_id"},
		{"item_present", itemPresent},
		{"ordinal", ordinalRead},
	} {
		bind := r.textParameter(member.name, "text:occurrence:"+member.name)
		keyArgs = append(keyArgs, "$"+strconv.Itoa(bind)+"::text, "+toRelatedJSONBMember(member.value))
	}
	occurrenceSelect := "jsonb_build_object(" + strings.Join(keyArgs, ", ") + ")"
	occurrenceAlias := prefix + "k"
	adapterSelects = append(adapterSelects, occurrenceSelect+` AS "`+occurrenceAlias+`"`)
	columns[adapter.OccurrenceKeyColumn] = groupColumn{
		name: adapter.OccurrenceKeyColumn, alias: occurrenceAlias, expr: occurrenceSelect,
		ref: `"` + occurrenceAlias + `"`, jsonb: true,
	}

	// The two witnesses and the rank. has-item is total (never SQL NULL),
	// so the ORDER BY arm needs no null treatment; the rank is the
	// registered total order of one route's rows.
	hasItemAlias := prefix + "h"
	adapterSelects = append(adapterSelects,
		"(CASE WHEN "+itemPresent+" THEN true ELSE false END) AS "+`"`+hasItemAlias+`"`)
	routeHasItemAlias := prefix + "r"
	adapterSelects = append(adapterSelects,
		"(CASE WHEN "+routeHasItem+" THEN true ELSE false END) AS "+`"`+routeHasItemAlias+`"`)
	// The total order follows the AQL FOR nesting (construction_group.go:
	// the input-row FOR, then the route FOR, then the Coding FOR): parent
	// identity first, then the reached terminal, then the dense Coding
	// position. Leading with the terminal would tie two parents that reach
	// the same target — and two unmatched parents — into an arbitrary order.
	rankOrder := anchorRead + ` COLLATE "C" ASC NULLS FIRST, ` + route.alias + `.term_id COLLATE "C" ASC NULLS FIRST`
	if adapter.CodingUnnest != nil {
		rankOrder += ", " + codingAlias + ".rn ASC NULLS FIRST"
	}
	rankAlias := prefix + "n"
	adapterSelects = append(adapterSelects,
		"ROW_NUMBER() OVER (ORDER BY "+rankOrder+") - 1 AS "+`"`+rankAlias+`"`)

	// --- the policy conjuncts (construction_group.go:532-554 parity). ---
	// These are the only conjuncts the outer lateral may carry: they address
	// the materialized route row and the Coding expansion, both of which
	// exist at this level. The anchor scope legs, the root-key correlation,
	// and the contributor FILTERs belong to the route subquery, whose hop
	// aliases they address.
	where := []string{}
	if adapter.RelatedRecords.EmptyPolicy == ir.PhysicalUnnestError {
		// The route join keeps the sentinel row (LEFT JOIN) so the guard has
		// a row to raise on, exactly where AQL's empty-expansion ASSERT
		// fires.
		where = append(where, "(CASE WHEN "+routeHasItem+" THEN 1 ELSE "+guardCast(stageExpandEmptyTag)+" END) > 0")
	}
	if adapter.CodingUnnest != nil {
		switch adapter.CodingEmptyPolicy {
		case ir.PhysicalUnnestPreserveParent:
		case ir.PhysicalUnnestExclude:
			// A matched route with no Coding item is excluded; an unmatched
			// route keeps its pad row (the AQL `!routeHasItem OR hasItem`).
			where = append(where, "(CASE WHEN NOT ("+routeHasItem+") OR "+codingHasItem+" THEN 1 ELSE 0 END) > 0")
		case ir.PhysicalUnnestError:
			where = append(where, "(CASE WHEN NOT ("+routeHasItem+") OR "+codingHasItem+" THEN 1 ELSE "+
				guardCast(stageExpandEmptyTag)+" END) > 0")
		default:
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: related Coding empty policy %q has no registered PostgreSQL form",
				ErrUnsupportedPostgresPlan, string(adapter.CodingEmptyPolicy))
		}
	}

	// --- the one shared expansion, joined once for this stage. ---
	innerSelect := "SELECT " + strings.Join(adapterSelects, ", ") + " FROM (SELECT 1) AS " + unitAlias +
		" " + route.joinKeyword + " (" + route.selectText + ") AS " + route.alias + " ON TRUE"
	if adapter.CodingUnnest != nil {
		innerSelect += " LEFT JOIN LATERAL " + codingSelect + " AS " + codingAlias + " ON TRUE"
	}
	if len(where) > 0 {
		innerSelect += " WHERE " + strings.Join(where, " AND ")
	}
	// An unmatched route drops the parent under EXCLUDE (AQL emits no
	// sentinel), keeps its pad row under PRESERVE_PARENT, and reaches its
	// guard row under ERROR.
	outerJoin := "LEFT JOIN LATERAL (" + innerSelect + ") AS " + relAlias + " ON TRUE"
	if adapter.RelatedRecords.EmptyPolicy == ir.PhysicalUnnestExclude {
		outerJoin = "INNER JOIN LATERAL (" + innerSelect + ") AS " + relAlias + " ON TRUE"
	}

	// The rank is the position WITHIN one parent's expansion (the lateral is
	// re-evaluated per parent row, the AQL FOR nesting), so the relation's
	// own order must lead with the parent identity: ranking alone interleaves
	// every parent's first item, which is not a total order.
	sourceText := "SELECT " + stageTerminalAlias + ".*, " + relAlias + ".* FROM " +
		stageRelationSource(relation) + " " + outerJoin +
		" ORDER BY " + anchorRead + " ASC, " + relAlias + `."` + rankAlias + `" ASC`
	return stageRelatedOccurrenceRegion{
		sourceText: sourceText,
		columns:    columns,
		orderTerms: []string{anchorRead + ` ASC`, stageTerminalAlias + `."` + rankAlias + `" ASC`},
	}, nil
}

// stageRelatedRoute is the rendered correlated hop chain of one adapter,
// spelled as one lateral SELECT correlated to the prior relation.
type stageRelatedRoute struct {
	selectText  string
	joinKeyword string
	alias       string
	variable    string
	targetType  string
}

// renderStageRelatedRoute validates and renders the adapter's correlated
// route, the anchor-prefixed subplan the lowering emits
// (recipe_construction_related_expand.go:98-164), through the traversal
// slice's hop contracts. anchorRead is the prior relation's materialized
// root-key read the anchor equality correlates through.
func (r *planRenderer) renderStageRelatedRoute(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	anchorRead string,
) (stageRelatedRoute, error) {
	adapter := stage.Group.RelatedOccurrence
	related := adapter.RelatedRecords
	subplan := related.RelatedRecords
	if len(subplan.Captures) != 1 || subplan.Captures[0] != stage.InputRowVariable {
		return stageRelatedRoute{}, fmt.Errorf("%w: the related route subplan must capture exactly the Group stage's input row %q, got %v",
			ErrUnsupportedPostgresPlan, stage.InputRowVariable, subplan.Captures)
	}
	if subplan.Sort == nil || subplan.Sort.Variable == "" || len(subplan.Sort.Path) != 1 ||
		subplan.Sort.Path[0] != "_id" || !subplan.Unique {
		return stageRelatedRoute{}, fmt.Errorf("%w: the related route subplan must be UNIQUE and sorted by the reached target's {_id} (recipe_construction_related_expand.go:215-217)",
			ErrUnsupportedPostgresPlan)
	}
	if subplan.DistinctBy != nil {
		return stageRelatedRoute{}, fmt.Errorf("%w: the related route subplan cannot carry a DISTINCT BY modifier", ErrUnsupportedPostgresPlan)
	}
	if len(subplan.Operations) == 0 {
		return stageRelatedRoute{}, fmt.Errorf("%w: the related route subplan has no operations", ErrUnsupportedPostgresPlan)
	}

	// --- the anchor prefix: COLLECTION_SCAN + the root-key equality FILTER. ---
	anchorOp := subplan.Operations[0]
	if anchorOp.Kind != ir.PhysicalCollectionScanOp || anchorOp.CollectionScan == nil {
		return stageRelatedRoute{}, fmt.Errorf("%w: the related route subplan must open with the root-collection anchor scan the lowering emits, got kind %q",
			ErrUnsupportedPostgresPlan, string(anchorOp.Kind))
	}
	scan := anchorOp.CollectionScan
	if scan.Variable == "" || scan.CollectionBindKey == "" {
		return stageRelatedRoute{}, fmt.Errorf("%w: the related route anchor scan must name its variable and root collection bind",
			ErrUnsupportedPostgresPlan)
	}
	if bound, ok := plan.BindVars[scan.CollectionBindKey].(string); !ok || bound != related.AnchorResourceType {
		return stageRelatedRoute{}, fmt.Errorf("%w: the related route anchor collection bind %q must be bound to the anchor resource type %q",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey, related.AnchorResourceType)
	}
	index := 1
	if index >= len(subplan.Operations) {
		return stageRelatedRoute{}, fmt.Errorf("%w: the related route anchor scan must be followed by its root-key equality filter",
			ErrUnsupportedPostgresPlan)
	}
	anchorFilter := subplan.Operations[index]
	if anchorFilter.Kind != ir.PhysicalFilterOp || anchorFilter.Filter == nil ||
		anchorFilter.Filter.Expression != nil || anchorFilter.Filter.Predicate.Right == nil {
		return stageRelatedRoute{}, fmt.Errorf("%w: the related route anchor filter must be the typed Predicate form the lowering emits",
			ErrUnsupportedPostgresPlan)
	}
	predicate := anchorFilter.Filter.Predicate
	if predicate.Operator != "EQUALS" || predicate.Quantifier != "" || predicate.Right == nil ||
		predicate.Left.Variable != scan.Variable || len(predicate.Left.Path) != 1 || predicate.Left.Path[0] != "_key" ||
		predicate.Right.Variable != stage.InputRowVariable || len(predicate.Right.Path) != 1 ||
		predicate.Right.Path[0] != related.AnchorColumnID {
		return stageRelatedRoute{}, fmt.Errorf("%w: the related route anchor filter must be the exact root-key equality %s._key == inputRow[%q] the lowering emits",
			ErrUnsupportedPostgresPlan, scan.Variable, related.AnchorColumnID)
	}
	index++

	resource := r.storage.Resource
	anchorAlias := "a" + strconv.Itoa(r.nextTraversalSequence())
	anchorTypeIndex := r.textParameter(related.AnchorResourceType, "text:"+related.AnchorResourceType)
	anchorWhere := r.scopedResourcePredicate(anchorAlias, anchorTypeIndex)
	rows := map[string]rowScope{scan.Variable: {alias: anchorAlias, resourceType: related.AnchorResourceType}}

	// The anchor's canonical scope block (recipe_construction_related_expand.go:128-130).
	if err := matchCanonicalScopeBlock(subplan.Operations, &index, []string{scan.Variable}); err != nil {
		return stageRelatedRoute{}, fmt.Errorf("%w: related route anchor scope block: %s", ErrUnsupportedPostgresPlan, err.Error())
	}

	// --- the hops, each closing with its canonical scope block. ---
	fromParts := []string{"FROM " + resource.Identifier().SQLText() + " AS " + anchorAlias}
	var conjuncts []string
	currentVar, currentAlias, currentType := scan.Variable, anchorAlias, related.AnchorResourceType
	for hopIndex, hop := range related.Route {
		if index >= len(subplan.Operations) {
			return stageRelatedRoute{}, fmt.Errorf("%w: related route hop %d is missing its operation", ErrUnsupportedPostgresPlan, hopIndex)
		}
		operation := subplan.Operations[index]
		if operation.Kind != ir.PhysicalTraversalOp || operation.Traversal == nil {
			return stageRelatedRoute{}, fmt.Errorf("%w: related route hop %d has kind %q; the route is exactly its canonical hops",
				ErrUnsupportedPostgresPlan, hopIndex, string(operation.Kind))
		}
		traversal := *operation.Traversal
		// The endpoint-lookup strategy is a planner label: PostgreSQL
		// renders the registered native typed-identity join for it, exactly
		// as the graph spine normalizes (graph.go:340-349 payload proof +
		// graph20NativeStrategyTraversal). The payload the AQL endpoint arm
		// would read must be complete first, so a hand-assembled
		// truncation fails closed instead of silently rendering native.
		if traversal.Strategy == ir.PhysicalTraversalEndpointLookup {
			if strings.TrimSpace(traversal.EndpointField) == "" || strings.TrimSpace(traversal.EndpointJoinField) == "" {
				return stageRelatedRoute{}, fmt.Errorf("%w: related route hop %d endpoint-lookup traversal payload is incomplete (the planner always names EndpointField and EndpointJoinField, lower/traversal.go:143-147)",
					ErrUnsupportedPostgresPlan, hopIndex)
			}
			traversal.Strategy = ir.PhysicalTraversalNative
		}
		if traversal.SourceVariable != currentVar {
			return stageRelatedRoute{}, fmt.Errorf("%w: related route hop %d must anchor on %q, got source %q",
				ErrUnsupportedPostgresPlan, hopIndex, currentVar, traversal.SourceVariable)
		}
		source, ok := rows[currentVar]
		if !ok {
			return stageRelatedRoute{}, fmt.Errorf("%w: related route hop %d anchors on %q, which binds no readable row",
				ErrUnsupportedPostgresPlan, hopIndex, currentVar)
		}
		verified, err := r.validateTraversalContract(plan, traversal, source)
		if err != nil {
			return stageRelatedRoute{}, fmt.Errorf("related route hop %d: %w", hopIndex, err)
		}
		if hop.Relationship != verified.label || hop.ToResourceType != verified.targetType {
			return stageRelatedRoute{}, fmt.Errorf("%w: related route hop %d names relationship %q and type %q, but its traversal payload carries label %q and type %q",
				ErrUnsupportedPostgresPlan, hopIndex, hop.Relationship, hop.ToResourceType, verified.label, verified.targetType)
		}
		if strings.ToUpper(string(traversal.Direction)) != hop.StorageDirection {
			return stageRelatedRoute{}, fmt.Errorf("%w: related route hop %d storage direction changed", ErrUnsupportedPostgresPlan, hopIndex)
		}
		seq := r.nextTraversalSequence()
		edgeAlias := "e" + strconv.Itoa(seq)
		targetAlias := "x" + strconv.Itoa(seq)
		edgeWhere, targetOn := r.edgeOccurrencePredicates(verified, edgeAlias, targetAlias, currentAlias)
		fromParts = append(fromParts,
			"INNER JOIN "+r.storage.Edge.Identifier().SQLText()+" AS "+edgeAlias+" ON "+edgeWhere,
			"INNER JOIN "+resource.Identifier().SQLText()+" AS "+targetAlias+" ON "+targetOn)
		rows[traversal.TargetVariable] = rowScope{alias: targetAlias, resourceType: verified.targetType}
		currentVar, currentAlias, currentType = traversal.TargetVariable, targetAlias, verified.targetType
		index++
		if err := matchCanonicalScopeBlock(subplan.Operations, &index,
			[]string{traversal.EdgeVariable, traversal.TargetVariable}); err != nil {
			return stageRelatedRoute{}, fmt.Errorf("%w: related route hop %d scope block: %s", ErrUnsupportedPostgresPlan, hopIndex, err.Error())
		}
	}
	if currentType != related.TargetResourceType {
		return stageRelatedRoute{}, fmt.Errorf("%w: the related route reaches %q, not its declared target type %q",
			ErrUnsupportedPostgresPlan, currentType, related.TargetResourceType)
	}

	// --- the optional contributor FILTER over the reached row. ---
	for ; index < len(subplan.Operations); index++ {
		operation := subplan.Operations[index]
		if operation.Kind != ir.PhysicalFilterOp || operation.Filter == nil || operation.Filter.Expression == nil {
			return stageRelatedRoute{}, fmt.Errorf("%w: related route operation %d has kind %q; after its hops the route carries only typed contributor FILTERs (shared subsets, nested sets and unnests, keyed lookups, document lookups, and non-canonical scope operations are capability gaps)",
				ErrUnsupportedPostgresPlan, index, string(operation.Kind))
		}
		fragment, err := r.renderPredicateFragmentScoped(plan, *operation.Filter.Expression, rows, true)
		if err != nil {
			return stageRelatedRoute{}, fmt.Errorf("related route contributor filter: %w", err)
		}
		conjuncts = append(conjuncts, fragment.sql)
	}

	// The dedup and its order: the subplan's Sort={terminal _id} + Unique
	// contract, one item per reached target (two edge occurrences to the same
	// target contribute one row — the AQL UNIQUE(set) reading sets.go
	// registers for the child set).
	logicalID := resource.MustColumn("logical_id").SQLText()
	payload := resource.MustColumn("payload").SQLText()
	terminalRead := currentAlias + "." + logicalID
	selectText := "SELECT DISTINCT ON (" + terminalRead + ` COLLATE "C") ` +
		terminalRead + ` AS term_id, ` + currentAlias + "." + payload + ` AS doc` +
		" " + strings.Join(fromParts, " ")
	// The route carries its own WHERE: the anchor scope legs, the root-key
	// correlation to the prior relation, and the contributor FILTERs all
	// address the hop aliases (a1/e2/x2), which exist only inside this
	// subquery. Promoting them to the outer lateral is a scope error the
	// planner rejects (42P01), so they are composed here, below the joins
	// and above the dedup order.
	anchorPredicate := anchorWhere + " AND " + anchorAlias + "." + logicalID + " = " + anchorCorrelationPlaceholder
	if !strings.Contains(anchorPredicate, anchorCorrelationPlaceholder) {
		return stageRelatedRoute{}, fmt.Errorf("%w: internal error: the related route anchor equality lost its correlation marker",
			ErrUnsupportedPostgresPlan)
	}
	anchorPredicate = strings.ReplaceAll(anchorPredicate, anchorCorrelationPlaceholder, anchorRead)
	routeWhere := append([]string{anchorPredicate}, conjuncts...)
	selectText += " WHERE " + strings.Join(routeWhere, " AND ") +
		` ORDER BY ` + terminalRead + ` COLLATE "C"`

	joinKeyword := "LEFT JOIN LATERAL"
	if adapter.RelatedRecords.EmptyPolicy == ir.PhysicalUnnestExclude {
		joinKeyword = "INNER JOIN LATERAL"
	}
	return stageRelatedRoute{
		selectText:  selectText,
		joinKeyword: joinKeyword,
		alias:       routeAlias,
		variable:    currentVar,
		targetType:  currentType,
	}, nil
}

// stageRelatedCodingArrayExpression renders one repeated Coding selector as
// the jsonb array the expansion iterates, in the composition slice's
// registered array-gathering shape (stage_sequence.go:1016-1091, whose AQL
// twin is the FLATTEN over the iterated step): the guarded array read through
// the path that ends at the single [] step, each element mapped through the
// tail path, stored nulls and a missing document gathering to '[]'.
func (r *planRenderer) stageRelatedCodingArrayExpression(
	base string,
	name string,
	extract *ir.PhysicalExtract,
) (string, error) {
	if extract.Selector.Filter != nil || len(extract.Fallbacks) != 0 || extract.Distinct ||
		extract.Prepared != nil || extract.UnitNormalization != nil {
		return "", fmt.Errorf("%w: Coding column %q uses a richer selector form (filter, fallbacks, DISTINCT, prepared, or unit normalization) that is not registered",
			ErrUnsupportedPostgresPlan, name)
	}
	if len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return "", fmt.Errorf("%w: Coding column %q must read the reached document at [\"payload\"], got %v",
			ErrUnsupportedPostgresPlan, name, extract.Source.Path)
	}
	if extract.ExecutionMode != "" && extract.ExecutionMode != ir.PhysicalSelectorGeneric &&
		extract.ExecutionMode != ir.PhysicalSelectorDirectScalar &&
		extract.ExecutionMode != ir.PhysicalSelectorConditionalArray {
		return "", fmt.Errorf("%w: Coding column %q selector execution mode %q is unknown",
			ErrUnsupportedPostgresPlan, name, string(extract.ExecutionMode))
	}
	iterate := -1
	for stepIndex, step := range extract.Selector.Steps {
		if step.Index != nil {
			return "", fmt.Errorf("%w: Coding column %q: indexed steps are the unit slice's registered reading, not the Coding gathering",
				ErrUnsupportedPostgresPlan, name)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return "", fmt.Errorf("%w: Coding column %q: selector step %q is not renderer-safe",
				ErrUnsupportedPostgresPlan, name, step.Field)
		}
		if step.Iterate {
			if iterate >= 0 {
				return "", fmt.Errorf("%w: Coding column %q: repeated steps beyond one iterate step are the later array-rendering capability",
					ErrUnsupportedPostgresPlan, name)
			}
			iterate = stepIndex
		}
	}
	if iterate < 0 {
		return "", fmt.Errorf("%w: Coding column %q has no iterated selector step; a Coding array needs exactly one [] step",
			ErrUnsupportedPostgresPlan, name)
	}
	// The array is read AT the iterated step, inclusive of its own field
	// (unnest.go:540-560, whose AQL twin is selectors.go:268 and
	// navigation_render.go:305): `valueCodeableConcept.coding[]` expands the
	// array at {valueCodeableConcept,coding}, never the container above it.
	prefixSteps := make([]string, 0, iterate+1)
	for _, step := range extract.Selector.Steps[:iterate+1] {
		prefixSteps = append(prefixSteps, step.Field)
	}
	tailSteps := make([]string, 0, len(extract.Selector.Steps)-iterate-1)
	for _, step := range extract.Selector.Steps[iterate+1:] {
		tailSteps = append(tailSteps, step.Field)
	}
	arr := base
	if len(prefixSteps) > 0 {
		var err error
		arr, err = r.documentPathFromExpression(arr, prefixSteps)
		if err != nil {
			return "", fmt.Errorf("Coding column %q: %w", name, err)
		}
	}
	leafTail := "{}"
	if len(tailSteps) > 0 {
		leafTail = "{" + strings.Join(mapSlice(tailSteps, arrayLiteralElement), ",") + "}"
	}
	leafTailParameter := r.textParameter(leafTail, "path:"+leafTail)
	leaf := "e #> $" + strconv.Itoa(leafTailParameter) + "::text[]"
	return "(CASE WHEN jsonb_typeof(" + arr + ") = 'array' THEN COALESCE((SELECT jsonb_agg(" + leaf +
		") FROM jsonb_array_elements(" + arr + ") AS e WHERE " + leaf + " IS NOT NULL), '[]'::jsonb) ELSE '[]'::jsonb END)", nil
}

// validateStageRelatedOccurrenceAdapter re-reads the adapter contract the
// composition payload gate cannot see: the anchor form this slice renders,
// the route/adapter agreement, and the ordinal semantics. ir already pins the
// field-source agreement (physical_stage_validation.go:2128-2226); these are
// the renderer-side boundaries.
func validateStageRelatedOccurrenceAdapter(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage) error {
	adapter := stage.Group.RelatedOccurrence
	related := adapter.RelatedRecords
	if related.AnchorKind != "root" {
		return fmt.Errorf("%w: the related route anchor kind %q has no registered PostgreSQL contract (the root-contributor set is production-unreachable, L-13.6; an active related-record anchor is a related-route capability)",
			ErrUnsupportedPostgresPlan, related.AnchorKind)
	}
	if related.AnchorColumnID != "_key" || related.ParentIdentityColumn != "_key" || related.RootContributorColumn != "" {
		return fmt.Errorf("%w: the related route must anchor on the materialized root _key (recipe_construction_related_expand.go:48-54), got anchor %q with contributor set %q",
			ErrUnsupportedPostgresPlan, related.AnchorColumnID, related.RootContributorColumn)
	}
	if related.Multiplicity != "DISTINCT_TARGET_PER_INPUT" {
		return fmt.Errorf("%w: the related route multiplicity %q has no registered PostgreSQL contract (only one DISTINCT target per input row renders)",
			ErrUnsupportedPostgresPlan, related.Multiplicity)
	}
	if adapter.RouteID == "" || related.RouteID != adapter.RouteID {
		return fmt.Errorf("%w: the related route adapter and its expansion must name the same non-empty route", ErrUnsupportedPostgresPlan)
	}
	if adapter.RouteHasItemVariable == "" || adapter.OccurrenceKeyColumn == "" {
		return fmt.Errorf("%w: the related route adapter lacks its has-item witness or its occurrence key column", ErrUnsupportedPostgresPlan)
	}
	if len(related.Route) == 0 {
		return fmt.Errorf("%w: the related route must carry at least one hop", ErrUnsupportedPostgresPlan)
	}
	if bindStringVar(plan.BindVars, related.ConstructionIDBindKey) == "" {
		return fmt.Errorf("%w: the related route expansion construction ID bind %q must be a non-empty string",
			ErrUnsupportedPostgresPlan, related.ConstructionIDBindKey)
	}
	if related.AnchorNodeID == "" || related.AnchorResourceType == "" ||
		related.TargetNodeID == "" || related.TargetResourceType == "" {
		return fmt.Errorf("%w: the related route must name its anchor and target nodes and resource types", ErrUnsupportedPostgresPlan)
	}
	if related.RelatedRecordsVariable == "" || related.IndexVariable == "" ||
		related.ItemVariable == "" || related.IdentityVariable == "" {
		return fmt.Errorf("%w: the related route expansion must name its subplan, index, item, and identity variables",
			ErrUnsupportedPostgresPlan)
	}
	if related.EmptyPolicy != ir.PhysicalUnnestPreserveParent && related.EmptyPolicy != ir.PhysicalUnnestExclude &&
		related.EmptyPolicy != ir.PhysicalUnnestError {
		return fmt.Errorf("%w: related route empty policy %q has no registered PostgreSQL form",
			ErrUnsupportedPostgresPlan, string(related.EmptyPolicy))
	}
	if related.RelatedRecords.Return.Kind != ir.PhysicalObjectExpression ||
		related.RelatedRecords.Return.Object == nil || len(related.RelatedRecords.Return.Object.Fields) == 0 {
		return fmt.Errorf("%w: the related route target projection must be a typed OBJECT expression", ErrUnsupportedPostgresPlan)
	}
	for hopIndex, hop := range related.Route {
		if hop.StepIndex != hopIndex || hop.EdgeID == "" || hop.Relationship == "" ||
			hop.FromNodeID == "" || hop.FromResourceType == "" || hop.ToNodeID == "" || hop.ToResourceType == "" ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") {
			return fmt.Errorf("%w: related route hop %d is incomplete or discontinuous", ErrUnsupportedPostgresPlan, hopIndex)
		}
		if hopIndex == 0 {
			if hop.FromNodeID != related.AnchorNodeID || hop.FromResourceType != related.AnchorResourceType {
				return fmt.Errorf("%w: related route hop 0 does not start at the anchor node", ErrUnsupportedPostgresPlan)
			}
			continue
		}
		prior := related.Route[hopIndex-1]
		if hop.FromNodeID != prior.ToNodeID || hop.FromResourceType != prior.ToResourceType {
			return fmt.Errorf("%w: related route hop %d is discontinuous", ErrUnsupportedPostgresPlan, hopIndex)
		}
	}
	last := related.Route[len(related.Route)-1]
	if last.ToNodeID != related.TargetNodeID || last.ToResourceType != related.TargetResourceType {
		return fmt.Errorf("%w: the related route does not end at its declared target", ErrUnsupportedPostgresPlan)
	}
	if adapter.CodingSelector == "" {
		if adapter.CodingUnnest != nil || adapter.CodingArrayColumn != "" || adapter.CodingMembershipColumn != "" ||
			adapter.OrdinalSemantics != "" || adapter.CodingEmptyPolicy != "" {
			return fmt.Errorf("%w: the related route declares Coding details without a Coding selector", ErrUnsupportedPostgresPlan)
		}
		return nil
	}
	if adapter.OrdinalSemantics != stageRelatedCodingOrdinalSemantics {
		return fmt.Errorf("%w: related route Coding ordinal semantics %q is not the registered zero-based flattened position",
			ErrUnsupportedPostgresPlan, adapter.OrdinalSemantics)
	}
	if adapter.CodingUnnest == nil || adapter.CodingArrayColumn == "" || adapter.CodingMembershipColumn == "" {
		return fmt.Errorf("%w: the related route Coding selector has no matching array column, membership column, or ordinal operation",
			ErrUnsupportedPostgresPlan)
	}
	coding := adapter.CodingUnnest
	if coding.OutputVariable == "" || coding.HasItemVariable == "" || coding.Ordinality == "" ||
		len(coding.Ancestors) != 0 || coding.EmptyPolicy != adapter.CodingEmptyPolicy ||
		coding.Expression.Kind != ir.PhysicalValueExpression || coding.Expression.Value == nil ||
		coding.Expression.Value.Variable != related.ItemVariable ||
		len(coding.Expression.Value.Path) != 1 || coding.Expression.Value.Path[0] != adapter.CodingArrayColumn {
		return fmt.Errorf("%w: the related route Coding expansion is not the exact ordinal operation over its own array column (the AQL renderer-shape twin, construction_group.go:386-401)",
			ErrUnsupportedPostgresPlan)
	}
	switch adapter.CodingEmptyPolicy {
	case ir.PhysicalUnnestPreserveParent, ir.PhysicalUnnestExclude, ir.PhysicalUnnestError:
		return nil
	default:
		return fmt.Errorf("%w: related Coding empty policy %q has no registered PostgreSQL form",
			ErrUnsupportedPostgresPlan, string(adapter.CodingEmptyPolicy))
	}
}

// identityFieldValue returns one classified identity field's value read.
func identityFieldValue(field classifiedExtractionProjection) *ir.PhysicalValue {
	if field.kind == extractionValueExpression && field.projection.Expression != nil {
		return field.projection.Expression.Value
	}
	if field.kind == extractionDirectValue {
		return &field.projection.Value
	}
	return nil
}

// toRelatedJSONBMember renders one SQL value as the jsonb member the
// occurrence-key object carries. The AQL identity object writes each member
// through its own value form; to_jsonb keeps the same scalar/jsonb and
// SQL-NULL reading — a SQL NULL becomes the jsonb 'null', never a missing
// key.
func toRelatedJSONBMember(value string) string {
	return "to_jsonb((" + value + "))"
}

// The renderer-owned aliases of one adapter expansion. Constants, never plan
// data; one adapter renders per stage, so one spelling per role suffices.
const (
	relAlias   = "__loom_rel"
	routeAlias = "__loom_route"
	unitAlias  = "__loom_one"
)

// anchorCorrelationPlaceholder marks where the caller's materialized root
// key crosses into the route's WHERE; renderStageGroupRelatedOccurrence
// replaces it with that column read, so the route text never carries a
// caller-specific name of its own.
const anchorCorrelationPlaceholder = "__loom_anchor_key"
