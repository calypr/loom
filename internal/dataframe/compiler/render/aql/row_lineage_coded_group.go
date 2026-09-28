package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderCodedGroupRowLineage(
	sourceQuery string,
	stage ir.PhysicalConstructionStage,
	terminal ir.PhysicalRowLineageReturn,
) (RenderedPhysicalPlan, error) {
	coded := stage.CodedGroup
	if coded == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("row lineage CODED_GROUP payload is required")
	}
	if _, ok := r.collectionKeys[coded.RootCollectionBindKey]; !ok {
		return RenderedPhysicalPlan{}, fmt.Errorf("root collection bind %q is not an authorized collection binding", coded.RootCollectionBindKey)
	}
	identity := r.newInternalVariable("row_lineage_coded_group_identity")
	selected := r.newInternalVariable("row_lineage_coded_group_selected")
	page := r.newInternalVariable("row_lineage_coded_group_page")
	resourceTypeBind := r.newInternalBindKey("row_lineage_resource_type")
	r.bindVars[resourceTypeBind] = terminal.ResourceType
	requestedRowID := "@" + terminal.RowIDBindKey

	selectedTuples, err := r.codedGroupTupleRows(stage, sourceQuery)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	selectedTuple := r.newInternalVariable("row_lineage_coded_group_selected_tuple")
	identityExpr := codedGroupIdentityExpression(coded, selectedTuple+".system", selectedTuple+".version", selectedTuple+".code")
	lines := []string{
		"LET " + selected + " = FIRST(",
		"  FOR " + selectedTuple + " IN (\n" + indentQuery(strings.Join(selectedTuples, "\n"), "    ") + "\n  )",
		"  LET " + identity + " = " + identityExpr,
		"  FILTER " + identity + " == " + requestedRowID,
		"  RETURN " + selectedTuple,
		")",
	}

	contributors, err := r.codedGroupTupleRows(stage, sourceQuery)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	contributor := r.newInternalVariable("row_lineage_coded_group_contributor")
	contributorKey := r.newInternalVariable("row_lineage_coded_group_contributor_key")
	contributorDocument := r.newInternalVariable("row_lineage_coded_group_contributor_document")
	contributorRows := []string{
		"FOR " + contributor + " IN (\n" + indentQuery(strings.Join(contributors, "\n"), "  ") + "\n)",
		"FILTER " + contributor + ".system == " + selected + ".system",
		"FILTER " + contributor + ".version == " + selected + ".version",
		"FILTER " + contributor + ".code == " + selected + ".code",
		"RETURN " + contributor + ".source_id",
	}
	pageLines := []string{
		"LET " + page + " = (",
		"  FOR " + contributorKey + " IN (",
		"    " + strings.Join(contributorRows, "\n    "),
		"  )",
		"  SORT " + contributorKey + " ASC",
		"  LIMIT @" + terminal.OffsetBindKey + ", @" + terminal.FetchLimitBindKey,
		"  LET " + contributorDocument + " = DOCUMENT(@@" + coded.RootCollectionBindKey + ", " + contributorKey + ")",
		"  FILTER " + contributorDocument + " != null",
		"  FILTER " + contributorDocument + ".project == @project",
		"  FILTER " + contributorDocument + ".dataset_generation == @dataset_generation",
		"  RETURN {resourceType: @" + resourceTypeBind + ", resourceId: " + contributorDocument + ".payload.id, occurrenceKey: " + contributorKey + "}",
		")",
	}
	lines = append(lines, pageLines...)
	lines = append(lines,
		"RETURN {found: "+selected+" != null, contributors: SLICE("+page+", @"+terminal.OffsetBindKey+", @"+terminal.LimitBindKey+"), hasMore: LENGTH("+page+") > @"+terminal.LimitBindKey+"}",
	)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

// codedGroupTupleRows emits one row per source record and exact coding tuple.
// Its COLLECT has no INTO clause, so duplicate Coding entries from one root
// collapse without materializing contributor lists.
func (r *physicalPlanRenderer) codedGroupTupleRows(
	stage ir.PhysicalConstructionStage,
	sourceQuery string,
) ([]string, error) {
	coded := stage.CodedGroup
	input := r.newInternalVariable("row_lineage_coded_group_input")
	document := r.newInternalVariable("row_lineage_coded_group_document")
	rawCodings := r.newInternalVariable("row_lineage_coded_group_raw_codings")
	candidate := r.newInternalVariable("row_lineage_coded_group_candidate")
	valid := r.newInternalVariable("row_lineage_coded_group_valid")
	missingCandidate := r.newInternalVariable("row_lineage_coded_group_missing_candidate")
	sourceID := r.newInternalVariable("row_lineage_coded_group_source_id")
	rawSystem := r.newInternalVariable("row_lineage_coded_group_raw_system")
	rawVersion := r.newInternalVariable("row_lineage_coded_group_raw_version")
	rawCode := r.newInternalVariable("row_lineage_coded_group_raw_code")
	system := r.newInternalVariable("row_lineage_coded_group_system")
	version := r.newInternalVariable("row_lineage_coded_group_version")
	code := r.newInternalVariable("row_lineage_coded_group_code")
	rootKeyBind := r.newInternalBindKeyWithValue("row_lineage_coded_group_source_key", coded.SourceIdentityColumn)
	lines := []string{
		"FOR " + input + " IN (\n" + indentQuery(sourceQuery, "  ") + "\n)",
		fmt.Sprintf("FILTER ASSERT(TYPENAME(%s[@%s]) == \"string\" AND %s[@%s] != \"\", \"CODED_GROUP_SOURCE_ID_MISSING\")",
			input, rootKeyBind, input, rootKeyBind),
		"LET " + document + " = DOCUMENT(@@" + coded.RootCollectionBindKey + ", " + input + "[@" + rootKeyBind + "])",
		"FILTER " + document + " != null",
		"FILTER " + document + ".project == @project",
		"FILTER " + document + ".dataset_generation == @dataset_generation",
		"LET " + rawCodings + " = (",
	}
	pathExpression := document + ".payload"
	for index, segment := range coded.PathSegments {
		attribute := fmt.Sprintf("%s[%q]", pathExpression, segment.Name)
		if !segment.Repeated {
			pathExpression = attribute
			continue
		}
		item := r.newInternalVariable(fmt.Sprintf("row_lineage_coded_group_path_%d", index))
		lines = append(lines, fmt.Sprintf("  FOR %s IN (%s == null ? [] : (IS_ARRAY(%s) ? %s : []))", item, attribute, attribute, attribute))
		pathExpression = item
	}
	lines = append(lines, "  RETURN "+pathExpression, ")")
	lines = append(lines,
		"LET "+missingCandidate+" = (LENGTH("+rawCodings+") == 0 ? [null] : "+rawCodings+")",
		"FOR "+candidate+" IN "+missingCandidate,
		fmt.Sprintf("LET %s = (%s != null AND TYPENAME(%s) == \"object\" AND TYPENAME(%s.system) == \"string\" AND TRIM(%s.system) != \"\" AND TYPENAME(%s.code) == \"string\" AND TRIM(%s.code) != \"\")",
			valid, candidate, candidate, candidate, candidate, candidate, candidate),
	)
	switch coded.MissingKeyPolicy {
	case ir.PhysicalStageGroupMissingKeyGroup:
	case ir.PhysicalStageGroupMissingKeyExclude:
		lines = append(lines, "FILTER "+valid)
	case ir.PhysicalStageGroupMissingKeyError:
		lines = append(lines, "FILTER ASSERT("+valid+", \"CONSTRUCTION_CODED_GROUP_MISSING_KEY\")")
	default:
		return nil, fmt.Errorf("unsupported coded-group missing-key policy %q", coded.MissingKeyPolicy)
	}
	lines = append(lines,
		"LET "+rawSystem+" = ("+valid+" ? "+candidate+".system : null)",
		"LET "+rawVersion+" = ("+valid+" AND TYPENAME("+candidate+".version) == \"string\" AND TRIM("+candidate+".version) != \"\" ? "+candidate+".version : null)",
		"LET "+rawCode+" = ("+valid+" ? "+candidate+".code : null)",
		"COLLECT "+sourceID+" = "+input+"[@"+rootKeyBind+"], "+system+" = "+rawSystem+", "+version+" = "+rawVersion+", "+code+" = "+rawCode,
		"RETURN {source_id: "+sourceID+", system: "+system+", version: "+version+", code: "+code+"}",
	)
	return lines, nil
}

func codedGroupIdentityExpression(coded *ir.PhysicalStageCodedGroup, system, version, code string) string {
	return fmt.Sprintf(
		"TO_STRING([\"construction\", @%s, \"operation\", \"CODED_GROUP\", \"occurrence\", @%s, \"path\", @%s, \"system\", %s, \"version\", %s, \"code\", %s])",
		coded.ConstructionIDBindKey, coded.OccurrenceIDBindKey, coded.CodingPathBindKey, system, version, code,
	)
}
