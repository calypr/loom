package postgres

// The GROUP stage's semantic-family key, registered here for pg-sql-16 G2
// (closing the L-13.3 named gap this package's group.go header carried).
//
// AQL ground truth is render/aql/construction_group.go: the membership walk
// renderConstructionGroupSemanticMembership (:663-700 — LET payload, then one
// guarded FOR per repeated path segment inside ONE subquery whose RETURN is
// the row's raw Coding projection), the matcher and per-row dedup
// renderConstructionGroupSemanticMembershipFromRaw (:713-746 — TYPENAME ==
// "object", system equality against @system, the version equality with the
// string-non-blank trim gate (version IS NULL twin when the family version is
// blank), then COLLECT code AGGREGATE display = MIN(display) with the
// zero-match {null,null,null,null} fallback row), the identity member
// [@[semanticOutputColumn], "semantic_family", @binding, @rule, @family,
// @system, @version, @path, tuple] (:170-171), and the missing-key policy
// over membership.code (:172-180). The group-level label is
// MIN(non-null displays) else tuple[2] (:225-228, :253-256, :300-310 — the
// three COLLECT arms agree on that one reading, so this slice emits it once).
//
// Scope of the registered arm (the L-13.3 closure boundary, stated so no
// reader has to infer it):
//
//   - The inline single-stage GROUP door only. The AQL twin renders the walk
//     over the in-scope source row (aql/stage_sequence.go:314 sets
//     SourceRowInScope exactly for the inline first stage), so the SQL reads
//     root.payload of the plan's OWN direct root scan — there is no DOCUMENT
//     re-read here, and the composed door keeps its own byte-identical
//     refusal (stage_sequence.go validateStageKindPayload GROUP-arm gate) until the composition carrier lands.
//   - The plan's root scan must be the scan the semantic family was resolved
//     against: same collection bind, same resource type, same variable. A
//     mismatch is a hand-assembled plan and fails closed.
//   - Every family string (binding, rule, family, system, version, coding
//     path, member key names, the trim set) crosses as one ordered $n
//     parameter through textParameter, never as SQL text. The path segments
//     cross through documentPathFromExpression, the package's single
//     path-binding authority.
//
// Oracle-attested parity (D-16e comparison-class register line,
// string-comparator honesty law per the D-13b finding): the banked
// ArangoDB 3.12.4-3 receipts (artifacts/
// loom-experiment-161/logs/) pin Arango's comparator to COLLATE "C" ONLY on
// the ASCII-letters class: o2b-compare.wire shows "a" < "b" true, "A" < "a"
// true, and "a" == "A" false, and o2b-sort.wire sorts ["b","A","_u","~t"] as
// ["_u","~t","A","b"] — so underscore and tilde sort BEFORE letters in Arango
// (whereas COLLATE "C" sorts them between "Z" and "a"; o2b-compare.wire's
// "~t" > "b" == false is the divergence witness). No byte-order parity is
// claimed beyond ASCII letters: the semantic display MIN and the tuple-key
// COLLATE "C" terms are pinned by ASCII-lowercase fixtures, and a code or
// display containing symbols or mixed case is an out-of-parity class whose
// order is not asserted equal to Arango (same registration family as
// D-13b/D-13h; the sibling string-reducer comparator audit,
// pg-sql-13-d13b-comparator-audit, is scheduled and this registration does
// not depend on its outcome). o2-min-raw.wire shows MIN SKIPPING nulls (MIN([null,"b","a"])
// == "a", MIN([null]) == MIN([]) == null) — PostgreSQL MIN ignores SQL NULL
// the same way; o2b-minmax.wire pins the per-code MIN(display) collapse with
// a blank-display code reading null (this file's fallback to the code).

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// groupSemanticCarrierName is the sentinel NAME of the renderer-minted g_src
// column that carries one source row's raw Coding projection (the AQL
// rawCodings LET). It is renderer-composed, never plan data, and no source
// RETURN projection may carry it (validateGroupOperations refuses it), so the
// walk's carrier can never be addressed as a key, reducer, or row-value
// input by name.
const groupSemanticCarrierName = "__loom_semantic_raw_codings"

// groupSemanticSourceIdentity is the one source-identity column the semantic
// lowering accepts (lower/recipe_construction_reshape.go:69 refuses anything
// else); the registered inline arm does not read it, it re-reads the law.
const groupSemanticSourceIdentity = "_key"

// groupSemanticSourceStageID is the direct-root input stage the semantic
// family requires (ir/physical_stage_validation.go:1480 re-read at the door;
// recipe.ConstructionSourceProjectionID).
const groupSemanticSourceStageID = "source_projection"

// validateGroupSemanticKey re-reads the door-bearing semantic contract
// before plan.Validate can speak: the resolved family identity, the
// direct-root scan equality, and the canonical Coding path. It is the L-13.3
// arm's fail-closed half — an incomplete or inconsistent family never
// renders, and never renders partially.
func validateGroupSemanticKey(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage) error {
	semantic := stage.Group.SemanticKey
	if semantic == nil {
		return nil
	}
	// The server-resolved authoring channel is still open (its receipt is
	// L-13.3's own Bundle.Validate probe, group.go:164): only a fully
	// resolved, pre-approved family renders, and an unresolved one is
	// refused with the registered-gap wording the refusal register pins.
	for _, field := range []struct{ name, value string }{
		{"family binding", semantic.BindingID},
		{"family rule version", semantic.RuleVersion},
		{"family ID", semantic.FamilyID},
		{"family system", semantic.System},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%w: the unresolved semantic family Group key is a registered named gap (L-13.3; the server-resolved authoring channel stays refused at recipe.Bundle.Validate; missing %s) and is refused, never partially rendered",
				ErrUnsupportedPostgresPlan, field.name)
		}
	}
	for _, field := range []struct {
		name, value string
	}{
		{"output column", semantic.OutputColumn},
		{"tuple variable", semantic.TupleVariable},
		{"label variable", semantic.LabelVariable},
		{"source root variable", semantic.SourceRootVariable},
		{"root collection bind", semantic.RootCollectionBindKey},
		{"resource type", semantic.ResourceType},
		{"coding path", semantic.CodingPath},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%w: the semantic family key is missing its %s (ir/physical_stage_validation.go:1478-1486 re-read at the door)",
				ErrUnsupportedPostgresPlan, field.name)
		}
	}
	if stage.InputStageID != groupSemanticSourceStageID {
		return fmt.Errorf("%w: the semantic family key requires the direct-root %q input stage (ir/physical_stage_validation.go:1480)",
			ErrUnsupportedPostgresPlan, groupSemanticSourceStageID)
	}
	if semantic.SourceIdentityColumn != groupSemanticSourceIdentity {
		return fmt.Errorf("%w: the semantic family key requires source rows identified by %q (lower/recipe_construction_reshape.go:69)",
			ErrUnsupportedPostgresPlan, groupSemanticSourceIdentity)
	}
	if collection, ok := plan.BindVars[semantic.RootCollectionBindKey].(string); !ok || collection != semantic.ResourceType {
		return fmt.Errorf("%w: the semantic family root collection bind %q does not carry the exact root resource type %q",
			ErrUnsupportedPostgresPlan, semantic.RootCollectionBindKey, semantic.ResourceType)
	}
	// The registered arm walks the payload of the plan's OWN root scan (the
	// AQL in-scope source row); a family resolved against another collection
	// or variable is not that row and never renders.
	if len(plan.Operations) == 0 || plan.Operations[0].RootScan == nil {
		return fmt.Errorf("%w: the semantic family key requires one leading ROOT_SCAN", ErrUnsupportedPostgresPlan)
	}
	scan := plan.Operations[0].RootScan
	if scan.CollectionBindKey != semantic.RootCollectionBindKey {
		return fmt.Errorf("%w: the semantic family root collection bind %q must be the root scan's own collection bind %q",
			ErrUnsupportedPostgresPlan, semantic.RootCollectionBindKey, scan.CollectionBindKey)
	}
	if scan.Variable != semantic.SourceRootVariable {
		return fmt.Errorf("%w: the semantic family source root variable %q must be the root scan variable %q",
			ErrUnsupportedPostgresPlan, semantic.SourceRootVariable, scan.Variable)
	}
	if len(semantic.PathSegments) == 0 || !semantic.PathSegments[len(semantic.PathSegments)-1].Repeated {
		return fmt.Errorf("%w: the semantic Coding path must end in a repeated Coding array (lower/recipe_construction_reshape.go:85-105 demands a generated repeated Coding path)",
			ErrUnsupportedPostgresPlan)
	}
	var canonical strings.Builder
	for index, segment := range semantic.PathSegments {
		if !payloadPathSegmentPattern.MatchString(segment.Name) {
			return fmt.Errorf("%w: semantic Coding path segment %q is not renderer-safe",
				ErrUnsupportedPostgresPlan, segment.Name)
		}
		if index > 0 {
			canonical.WriteByte('.')
		}
		canonical.WriteString(segment.Name)
		if segment.Repeated {
			canonical.WriteString("[]")
		}
	}
	if canonical.String() != semantic.CodingPath {
		return fmt.Errorf("%w: the semantic Coding path must be the canonical join of its segments", ErrUnsupportedPostgresPlan)
	}
	// The semantic output is a key of its own: it must not address a second
	// registered output (the final projection would bind two readings to one
	// column name).
	for _, name := range groupRegisteredOutputNames(stage.Group) {
		if name == semantic.OutputColumn {
			return fmt.Errorf("%w: the semantic family output %q is already a registered key, reducer, or row-value output",
				ErrUnsupportedPostgresPlan, semantic.OutputColumn)
		}
	}
	return nil
}

// groupRegisteredOutputNames lists the output column names the group region
// already binds, so the semantic output can be checked for collisions.
func groupRegisteredOutputNames(group *ir.PhysicalStageGroup) []string {
	names := make([]string, 0, len(group.Keys)+len(group.Aggregates)+len(group.RowValues))
	for _, key := range group.Keys {
		names = append(names, key.OutputColumn)
	}
	for _, aggregate := range group.Aggregates {
		names = append(names, aggregate.Output)
	}
	for _, rowValue := range group.RowValues {
		names = append(names, rowValue.Output)
	}
	return names
}

// groupSemanticCarrierColumn mints the hidden g_src column carrying one
// source row's raw Coding projection — the SQL twin of the AQL
//
//	LET rawCodings = (FOR item IN (attr == null ? [] : (IS_ARRAY(attr) ? attr
//	  : [])) … RETURN path)
//
// (construction_group.go:676-693). Consecutive scalar segments collapse into
// one #> text[] extraction through documentPathFromExpression; each repeated
// segment contributes one IS_ARRAY-guarded jsonb_array_elements step, chained
// CROSS JOIN LATERAL so an emptied branch prunes the row exactly as the AQL
// nested FOR does, and jsonb_agg over zero rows plus COALESCE reproduces the
// empty-array reading (AQL yields [] when any guard fails). The subquery is
// correlated to the g_src row, so g_src stays one row per source row: the
// row multiplication belongs to the membership join, exactly where AQL's
// FOR-in-memberships sits.
func (r *planRenderer) groupSemanticCarrierColumn(semantic *ir.PhysicalStageGroupSemanticKey) (*groupColumn, error) {
	current := r.documentColumn(r.rootAlias)
	var steps []string
	var pending []string
	lastAlias := ""
	sequence := 0
	for _, segment := range semantic.PathSegments {
		pending = append(pending, segment.Name)
		if !segment.Repeated {
			continue
		}
		extraction, err := r.documentPathFromExpression(current, pending)
		if err != nil {
			return nil, fmt.Errorf("semantic Coding path: %w", err)
		}
		current = extraction
		pending = nil
		sequence++
		alias := "semx" + strconv.Itoa(sequence)
		guard := "CASE WHEN jsonb_typeof(" + current + ") = 'array' THEN " + current + " ELSE '[]'::jsonb END"
		steps = append(steps, "jsonb_array_elements("+guard+") AS "+alias+"(item)")
		current = alias + ".item"
		lastAlias = alias
	}
	if len(pending) != 0 || lastAlias == "" {
		// Unreachable past validateGroupSemanticKey (the path ends in a
		// repeated array); the fail-closed refusal stays.
		return nil, fmt.Errorf("%w: the semantic Coding path must end in a repeated Coding array", ErrUnsupportedPostgresPlan)
	}
	expr := "COALESCE((SELECT jsonb_agg(" + lastAlias + ".item) FROM " +
		strings.Join(steps, " CROSS JOIN LATERAL ") + "), '[]'::jsonb)"
	return &groupColumn{
		name: groupSemanticCarrierName, alias: "csem", expr: expr, ref: `"csem"`, jsonb: true,
	}, nil
}

// groupSemanticRegion is the rendered semantic arm for one grouped-aggregate
// region. The three tuple reads are the region's key terms (the AQL
// collectKeys/sortKeys tuple), label is the group-level reading the AQL
// LabelVariable LETs carry, member is the LATERAL clause appended to the
// rows relation, and identityPart is the SQL expression producing the
// identity's semantic member text.
type groupSemanticRegion struct {
	lateral      string
	tupleReads   []string
	tupleAliases []string
	label        string
	identityPart string
	// codeRead is the unqualified membership-code column the missing-key
	// policy conjunct is built over.
	codeRead string
}

// renderGroupSemanticRegion renders the matcher + per-row dedup + fallback
// join over the carrier column, plus the tuple reads, group label, and
// identity member. prefix mints every internal relation and column name.
func (r *planRenderer) renderGroupSemanticRegion(
	semantic *ir.PhysicalStageGroupSemanticKey,
	carrier *groupColumn,
	stage ir.PhysicalConstructionStage,
	prefix string,
	dollar func(int) string,
) (groupSemanticRegion, error) {
	if carrier == nil || carrier.name != groupSemanticCarrierName {
		return groupSemanticRegion{}, fmt.Errorf("%w: the semantic family key requires the inline door's root-payload carrier; a composed sequence has none and stays refused at the composition door (stage_sequence.go validateStageKindPayload GROUP-arm gate)",
			ErrUnsupportedPostgresPlan)
	}

	// Member key names and the trim set cross as bound parameters (the
	// coded-group matcher binds the same values under the same cache keys).
	systemKey := r.textParameter("system", "text:system")
	versionKey := r.textParameter("version", "text:version")
	codeKey := r.textParameter("code", "text:code")
	displayKey := r.textParameter("display", "text:display")
	// The blank-string half of every gate below is the package's ONE
	// registered trim render (coded_group.go's gate — the only file the
	// single-trim-set law lets emit a btrim), bound to the same space-only
	// set the AQL TRIM of construction_group.go:643-648 uses.
	nonBlank := r.codedGroupTrimmedNonBlank
	familySystem := r.textParameter(semantic.System, "")
	familyVersion := r.textParameter(semantic.Version, "")

	// matchFor is the AQL matcher FILTER chain (:715-718) over one item
	// expression. The jsonb_typeof guards are the SQL reading of Arango's
	// typed ==: a non-string member never equals a string bind, so a stored
	// {"system": 123} against the family system "123" stays unmatched.
	matchFor := func(item string) string {
		systemJSON := item + " -> " + dollar(systemKey) + "::text"
		systemText := item + " ->> " + dollar(systemKey) + "::text"
		versionJSON := item + " -> " + dollar(versionKey) + "::text"
		versionText := item + " ->> " + dollar(versionKey) + "::text"
		versionRead := "CASE WHEN jsonb_typeof(" + versionJSON + ") = 'string'" +
			" AND " + nonBlank(versionText) + " THEN " + versionText + " END"
		versionPredicate := versionRead + " IS NULL"
		if semantic.Version != "" {
			versionPredicate = versionRead + " = " + dollar(familyVersion) + "::text"
		}
		return "jsonb_typeof(" + item + ") = 'object'" +
			" AND jsonb_typeof(" + systemJSON + ") = 'string'" +
			" AND " + systemText + " = " + dollar(familySystem) + "::text" +
			" AND " + versionPredicate
	}
	blankGated := func(item string, key int) string {
		valueJSON := item + " -> " + dollar(key) + "::text"
		valueText := item + " ->> " + dollar(key) + "::text"
		return "CASE WHEN jsonb_typeof(" + valueJSON + ") = 'string'" +
			" AND " + nonBlank(valueText) + " THEN " + valueText + " END"
	}

	// The per-row dedup (construction_group.go:727-735): COLLECT code =
	// localCode AGGREGATE display = MIN(localDisplay) over the matched set,
	// and the tuple's system/version members riding the code's presence.
	// NULLS FIRST is not an aggregate concern, so the only collation the
	// collapse needs is the pinned-class "C" collation (D-16e; symbols and
	// mixed case are the registered out-of-parity class in this file's
	// header).
	aggCode := prefix + "sc0"
	aggDisplay := prefix + "sc1"
	dedup := "SELECT " + blankGated("_mc.coding", codeKey) + " AS " + aggCode +
		", MIN(" + blankGated("_mc.coding", displayKey) + ` COLLATE "C") AS ` + aggDisplay +
		" FROM jsonb_array_elements(" + carrier.ref + ") AS _mc(coding)" +
		" WHERE " + matchFor("_mc.coding") +
		" GROUP BY 1"

	matched := "SELECT CASE WHEN " + aggCode + " IS NOT NULL THEN " + dollar(familySystem) + "::text END," +
		" CASE WHEN " + aggCode + " IS NOT NULL THEN " + dollar(familyVersion) + "::text END," +
		aggCode + ", " + aggDisplay +
		" FROM (" + dedup + ") AS " + prefix + "sd"
	// The zero-match fallback row (:733 — LENGTH(matched) == 0 ?
	// [{system: null, version: null, code: null, display: null}] : …). The
	// UNION ALL arm fires exactly when the matched set is empty, so the join
	// never drops a source row and never duplicates the fallback.
	fallback := "SELECT NULL::text, NULL::text, NULL::text, NULL::text" +
		" WHERE NOT EXISTS (SELECT 1 FROM jsonb_array_elements(" + carrier.ref + ") AS _me(coding)" +
		" WHERE " + matchFor("_me.coding") + ")"

	tupleAliases := []string{prefix + "ssy", prefix + "svr", prefix + "sco", prefix + "sdi"}
	lateral := " CROSS JOIN LATERAL (" + matched + " UNION ALL " + fallback + ") AS " + prefix + "sm(" +
		strings.Join(tupleAliases, ", ") + ")"

	// The group-level label (construction_group.go:225-228, :253-256,
	// :300-310): MIN over the group's non-null displays, else the tuple's
	// code. The CASE keeps both branches plain text so no collation merge is
	// ever asked of the planner.
	minDisplay := "MIN(" + tupleAliases[3] + ` COLLATE "C")`
	label := "CASE WHEN " + minDisplay + " IS NULL THEN " + tupleAliases[2] + " ELSE " + minDisplay + " END"

	// The identity member (:170-171): [@[semanticOutputColumn],
	// "semantic_family", @binding, @rule, @family, @system, @version, @path,
	// tuple] — tuple is the nested [system, version, code] array, every
	// member a string or null, rendered JSON-compact exactly as Arango's
	// TO_STRING does (the group.go identity precedent: to_jsonb per member,
	// never to_jsonb of a whole array, whose text form spaces after commas).
	semanticOutputID := ""
	for _, column := range stage.OutputColumns {
		if column.Name == semantic.OutputColumn {
			semanticOutputID = column.ID
			break
		}
	}
	if semanticOutputID == "" {
		return groupSemanticRegion{}, fmt.Errorf("%w: semantic Group output %q has no stable column ID (construction_group.go:167-169 parity)",
			ErrUnsupportedPostgresPlan, semantic.OutputColumn)
	}
	outputBind := r.textParameter(semanticOutputID, "")
	bindingBind := r.textParameter(semantic.BindingID, "")
	ruleBind := r.textParameter(semantic.RuleVersion, "")
	familyBind := r.textParameter(semantic.FamilyID, "")
	pathBind := r.textParameter(semantic.CodingPath, "")
	// The identity renders in the region's terminal SELECT, whose grouped
	// relation is aliased g (both doors), so the tuple members are read
	// through that alias — the same qualification appendKeyPart uses.
	tupleText := "'[' || COALESCE(to_jsonb(g." + tupleAliases[0] + ")::text, 'null')" +
		" || ',' || COALESCE(to_jsonb(g." + tupleAliases[1] + ")::text, 'null')" +
		" || ',' || COALESCE(to_jsonb(g." + tupleAliases[2] + ")::text, 'null') || ']'"
	identityPart := "'[' || to_jsonb(" + dollar(outputBind) + "::text)" +
		" || ',' || " + quoteSQLLiteral(`"semantic_family"`) +
		" || ',' || to_jsonb(" + dollar(bindingBind) + "::text)" +
		" || ',' || to_jsonb(" + dollar(ruleBind) + "::text)" +
		" || ',' || to_jsonb(" + dollar(familyBind) + "::text)" +
		" || ',' || to_jsonb(" + dollar(familySystem) + "::text)" +
		" || ',' || to_jsonb(" + dollar(familyVersion) + "::text)" +
		" || ',' || to_jsonb(" + dollar(pathBind) + "::text)" +
		" || ',' || " + tupleText + " || ']'"

	return groupSemanticRegion{
		lateral:      lateral,
		tupleReads:   []string{tupleAliases[0], tupleAliases[1], tupleAliases[2]},
		tupleAliases: tupleAliases,
		label:        label,
		identityPart: identityPart,
		codeRead:     tupleAliases[2],
	}, nil
}
