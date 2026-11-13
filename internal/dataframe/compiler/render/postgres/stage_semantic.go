package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const groupSemanticCarrierName = "__loom_semantic_raw_codings"

const groupSemanticSourceIdentity = "_key"

const groupSemanticSourceStageID = "source_projection"

func validateGroupSemanticKey(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage) error {
	semantic := stage.Group.SemanticKey
	if semantic == nil {
		return nil
	}
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
	for _, name := range groupRegisteredOutputNames(stage.Group) {
		if name == semantic.OutputColumn {
			return fmt.Errorf("%w: the semantic family output %q is already a registered key, reducer, or row-value output",
				ErrUnsupportedPostgresPlan, semantic.OutputColumn)
		}
	}
	return nil
}

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
		return nil, fmt.Errorf("%w: the semantic Coding path must end in a repeated Coding array", ErrUnsupportedPostgresPlan)
	}
	expr := "COALESCE((SELECT jsonb_agg(" + lastAlias + ".item) FROM " +
		strings.Join(steps, " CROSS JOIN LATERAL ") + "), '[]'::jsonb)"
	return &groupColumn{
		name: groupSemanticCarrierName, alias: "csem", expr: expr, ref: `"csem"`, jsonb: true,
	}, nil
}

type groupSemanticRegion struct {
	lateral      string
	tupleReads   []string
	tupleAliases []string
	label        string
	identityPart string
	codeRead     string
}

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

	systemKey := r.textParameter("system", "text:system")
	versionKey := r.textParameter("version", "text:version")
	codeKey := r.textParameter("code", "text:code")
	displayKey := r.textParameter("display", "text:display")
	nonBlank := r.codedGroupTrimmedNonBlank
	familySystem := r.textParameter(semantic.System, "")
	familyVersion := r.textParameter(semantic.Version, "")

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

	aggCode := prefix + "sc0"
	aggDisplay := prefix + "sc1"
	dedup := "SELECT " + blankGated("_mc.coding", codeKey) + " AS " + aggCode +
		", MIN(" + blankGated("_mc.coding", displayKey) + ` COLLATE "C") AS ` + aggDisplay +
		" FROM jsonb_array_elements(" + carrier.ref + ") AS _mc(coding)" +
		" WHERE " + matchFor("_mc.coding") +
		" GROUP BY 1"

	versionMember := dollar(familyVersion) + "::text"
	if semantic.Version == "" {
		versionMember = "NULL::text"
	}
	matched := "SELECT CASE WHEN " + aggCode + " IS NOT NULL THEN " + dollar(familySystem) + "::text END," +
		" CASE WHEN " + aggCode + " IS NOT NULL THEN " + versionMember + " END," +
		aggCode + ", " + aggDisplay +
		" FROM (" + dedup + ") AS " + prefix + "sd"
	fallback := "SELECT NULL::text, NULL::text, NULL::text, NULL::text" +
		" WHERE NOT EXISTS (SELECT 1 FROM jsonb_array_elements(" + carrier.ref + ") AS _me(coding)" +
		" WHERE " + matchFor("_me.coding") + ")"

	tupleAliases := []string{prefix + "ssy", prefix + "svr", prefix + "sco", prefix + "sdi"}
	lateral := " CROSS JOIN LATERAL (" + matched + " UNION ALL " + fallback + ") AS " + prefix + "sm(" +
		strings.Join(tupleAliases, ", ") + ")"

	minDisplay := "MIN(" + tupleAliases[3] + ` COLLATE "C")`
	label := "CASE WHEN " + minDisplay + " IS NULL THEN " + tupleAliases[2] + " ELSE " + minDisplay + " END"

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
