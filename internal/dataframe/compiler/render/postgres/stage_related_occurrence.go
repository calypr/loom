package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	stageRelatedCodingOrdinalSemantics = "ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY"

	stageRelatedTerminalField = "terminal_id"
	stageRelatedResourceField = "resource_id"
)

type stageRelatedOccurrenceRegion struct {
	sourceText string
	columns    map[string]groupColumn
	orderTerms []string
}

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

	route, err := r.renderStageRelatedRoute(plan, stage, anchorRead)
	if err != nil {
		return stageRelatedOccurrenceRegion{}, err
	}

	docBase := route.alias + ".doc"

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

	hasItemAlias := prefix + "h"
	adapterSelects = append(adapterSelects,
		"(CASE WHEN "+itemPresent+" THEN true ELSE false END) AS "+`"`+hasItemAlias+`"`)
	routeHasItemAlias := prefix + "r"
	adapterSelects = append(adapterSelects,
		"(CASE WHEN "+routeHasItem+" THEN true ELSE false END) AS "+`"`+routeHasItemAlias+`"`)
	rankOrder := anchorRead + ` COLLATE "C" ASC NULLS FIRST, ` + route.alias + `.term_id COLLATE "C" ASC NULLS FIRST`
	if adapter.CodingUnnest != nil {
		rankOrder += ", " + codingAlias + ".rn ASC NULLS FIRST"
	}
	rankAlias := prefix + "n"
	adapterSelects = append(adapterSelects,
		"ROW_NUMBER() OVER (ORDER BY "+rankOrder+") - 1 AS "+`"`+rankAlias+`"`)

	where := []string{}
	if adapter.RelatedRecords.EmptyPolicy == ir.PhysicalUnnestError {
		where = append(where, "(CASE WHEN "+routeHasItem+" THEN 1 ELSE "+guardCast(stageExpandEmptyTag)+" END) > 0")
	}
	if adapter.CodingUnnest != nil {
		switch adapter.CodingEmptyPolicy {
		case ir.PhysicalUnnestPreserveParent:
		case ir.PhysicalUnnestExclude:
			where = append(where, "(CASE WHEN NOT ("+routeHasItem+") OR "+codingHasItem+" THEN 1 ELSE 0 END) > 0")
		case ir.PhysicalUnnestError:
			where = append(where, "(CASE WHEN NOT ("+routeHasItem+") OR "+codingHasItem+" THEN 1 ELSE "+
				guardCast(stageExpandEmptyTag)+" END) > 0")
		default:
			return stageRelatedOccurrenceRegion{}, fmt.Errorf("%w: related Coding empty policy %q has no registered PostgreSQL form",
				ErrUnsupportedPostgresPlan, string(adapter.CodingEmptyPolicy))
		}
	}

	innerSelect := "SELECT " + strings.Join(adapterSelects, ", ") + " FROM (SELECT 1) AS " + unitAlias +
		" " + route.joinKeyword + " (" + route.selectText + ") AS " + route.alias + " ON TRUE"
	if adapter.CodingUnnest != nil {
		innerSelect += " LEFT JOIN LATERAL " + codingSelect + " AS " + codingAlias + " ON TRUE"
	}
	if len(where) > 0 {
		innerSelect += " WHERE " + strings.Join(where, " AND ")
	}
	outerJoin := "LEFT JOIN LATERAL (" + innerSelect + ") AS " + relAlias + " ON TRUE"
	if adapter.RelatedRecords.EmptyPolicy == ir.PhysicalUnnestExclude {
		outerJoin = "INNER JOIN LATERAL (" + innerSelect + ") AS " + relAlias + " ON TRUE"
	}

	sourceText := "SELECT " + stageTerminalAlias + ".*, " + relAlias + ".* FROM " +
		stageRelationSource(relation) + " " + outerJoin +
		" ORDER BY " + anchorRead + " ASC, " + relAlias + `."` + rankAlias + `" ASC`
	return stageRelatedOccurrenceRegion{
		sourceText: sourceText,
		columns:    columns,
		orderTerms: []string{anchorRead + ` ASC`, stageTerminalAlias + `."` + rankAlias + `" ASC`},
	}, nil
}

type stageRelatedRoute struct {
	selectText  string
	joinKeyword string
	alias       string
	variable    string
	targetType  string
}

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

	if err := matchCanonicalScopeBlock(subplan.Operations, &index, []string{scan.Variable}); err != nil {
		return stageRelatedRoute{}, fmt.Errorf("%w: related route anchor scope block: %s", ErrUnsupportedPostgresPlan, err.Error())
	}

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

	logicalID := resource.MustColumn("logical_id").SQLText()
	payload := resource.MustColumn("payload").SQLText()
	terminalRead := currentAlias + "." + logicalID
	selectText := "SELECT DISTINCT ON (" + terminalRead + ` COLLATE "C") ` +
		terminalRead + ` AS term_id, ` + currentAlias + "." + payload + ` AS doc` +
		" " + strings.Join(fromParts, " ")
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

func identityFieldValue(field classifiedExtractionProjection) *ir.PhysicalValue {
	if field.kind == extractionValueExpression && field.projection.Expression != nil {
		return field.projection.Expression.Value
	}
	if field.kind == extractionDirectValue {
		return &field.projection.Value
	}
	return nil
}

func toRelatedJSONBMember(value string) string {
	return "to_jsonb((" + value + "))"
}

const (
	relAlias   = "__loom_rel"
	routeAlias = "__loom_route"
	unitAlias  = "__loom_one"
)

const anchorCorrelationPlaceholder = "__loom_anchor_key"
