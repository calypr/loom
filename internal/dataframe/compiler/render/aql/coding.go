package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func codedGroupIdentityExpression(coded *ir.PhysicalStageCodedGroup, system, version, code string) string {
	return fmt.Sprintf(
		"TO_STRING([\"construction\", @%s, \"operation\", \"CODED_GROUP\", \"occurrence\", @%s, \"path\", @%s, \"system\", %s, \"version\", %s, \"code\", %s])",
		coded.ConstructionIDBindKey, coded.OccurrenceIDBindKey, coded.CodingPathBindKey, system, version, code,
	)
}

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
		"FILTER " + selected + " != null",
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
		"RETURN {found: "+selected+" != null, contributors: SLICE("+page+", 0, @"+terminal.LimitBindKey+"), hasMore: LENGTH("+page+") > @"+terminal.LimitBindKey+"}",
	)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

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

func (r *physicalPlanRenderer) renderConstructionCodedGroupStage(stage ir.PhysicalConstructionStage, inputRows, sourceRootVariable string) ([]string, error) {
	coded := stage.CodedGroup
	if coded == nil {
		return nil, fmt.Errorf("coded-group stage is missing payload")
	}
	if _, ok := r.collectionKeys[coded.RootCollectionBindKey]; !ok {
		return nil, fmt.Errorf("root collection bind %q is not an authorized collection binding", coded.RootCollectionBindKey)
	}
	rootDocument := sourceRootVariable
	if rootDocument == "" {
		rootDocument = r.newInternalVariable("construction_coded_group_root")
	}
	rawCodings := r.newInternalVariable("construction_coded_group_raw")
	candidate := r.newInternalVariable("construction_coded_group_candidate")
	valid := r.newInternalVariable("construction_coded_group_valid")
	sourceTuples := r.newInternalVariable("construction_coded_group_source_tuples")
	tuple := r.newInternalVariable("construction_coded_group_tuple")
	var rootKeyBind string
	if sourceRootVariable == "" {
		rootKeyBind = r.newInternalBindKeyWithValue("construction_coded_group_source_id", coded.SourceIdentityColumn)
	}
	systemKeyVariable := r.newInternalVariable("construction_coded_system")
	versionKeyVariable := r.newInternalVariable("construction_coded_version")
	codeKeyVariable := r.newInternalVariable("construction_coded_code")
	rowValuesSource := stage.InputRowVariable
	lines := make([]string, 0, 24)
	if sourceRootVariable == "" {
		lines = append(lines,
			fmt.Sprintf("  FOR %s IN %s", stage.InputRowVariable, inputRows),
			fmt.Sprintf("    FILTER ASSERT(TYPENAME(%s[@%s]) == \"string\" AND %s[@%s] != \"\", \"CODED_GROUP_SOURCE_ID_MISSING\")",
				stage.InputRowVariable, rootKeyBind, stage.InputRowVariable, rootKeyBind),
			fmt.Sprintf("    LET %s = DOCUMENT(@@%s, %s[@%s])", rootDocument, coded.RootCollectionBindKey, stage.InputRowVariable, rootKeyBind),
			fmt.Sprintf("    FILTER %s != null", rootDocument),
			fmt.Sprintf("    FILTER %s.project == @project", rootDocument),
			fmt.Sprintf("    FILTER %s.dataset_generation == @dataset_generation", rootDocument),
		)
	}
	if len(coded.RowValues) != 0 && sourceRootVariable != "" {
		projection, err := r.renderReturn(ir.PhysicalReturn{Projections: coded.RowValueProjections})
		if err != nil {
			return nil, fmt.Errorf("row value source projection: %w", err)
		}
		rowValuesSource = r.newInternalVariable("construction_coded_group_row_values_source")
		lines = append(lines, fmt.Sprintf("    LET %s = %s", rowValuesSource, projection))
	}
	lines = append(lines, fmt.Sprintf("    LET %s = (", rawCodings))
	pathExpression := rootDocument + ".payload"
	for index, segment := range coded.PathSegments {
		attribute := fmt.Sprintf("%s[%q]", pathExpression, segment.Name)
		if !segment.Repeated {
			pathExpression = attribute
			continue
		}
		item := r.newInternalVariable(fmt.Sprintf("construction_coded_group_path_%d", index))
		lines = append(lines,
			fmt.Sprintf("      FOR %s IN (%s == null ? [] : (IS_ARRAY(%s) ? %s : []))", item, attribute, attribute, attribute),
		)
		pathExpression = item
	}
	lines = append(lines,
		fmt.Sprintf("        RETURN %s", pathExpression),
		"    )",
	)
	missingCandidates := r.newInternalVariable("construction_coded_group_missing_candidate")
	lines = append(lines,
		fmt.Sprintf("    LET %s = (", sourceTuples),
		fmt.Sprintf("      LET %s = (LENGTH(%s) == 0 ? [null] : %s)", missingCandidates, rawCodings, rawCodings),
		fmt.Sprintf("      FOR %s IN %s", candidate, missingCandidates),
		fmt.Sprintf("        LET %s = (%s != null AND TYPENAME(%s) == \"object\" AND TYPENAME(%s.system) == \"string\" AND TRIM(%s.system) != \"\" AND TYPENAME(%s.code) == \"string\" AND TRIM(%s.code) != \"\")",
			valid, candidate, candidate, candidate, candidate, candidate, candidate),
	)
	switch coded.MissingKeyPolicy {
	case ir.PhysicalStageGroupMissingKeyGroup:
	case ir.PhysicalStageGroupMissingKeyExclude:
		lines = append(lines, fmt.Sprintf("        FILTER %s", valid))
	case ir.PhysicalStageGroupMissingKeyError:
		lines = append(lines, fmt.Sprintf("        FILTER ASSERT(%s, \"CONSTRUCTION_CODED_GROUP_MISSING_KEY\")", valid))
	default:
		return nil, fmt.Errorf("unsupported coded-group missing-key policy %q", coded.MissingKeyPolicy)
	}
	rowValuesObject := "{}"
	seenRowValueInputs := make(map[string]bool, len(coded.RowValues))
	for _, rowValue := range coded.RowValues {
		if seenRowValueInputs[rowValue.InputColumn] {
			continue
		}
		columnBind := r.newInternalBindKeyWithValue("construction_coded_group_row_value_column", rowValue.InputColumn)
		rowValuesObject = fmt.Sprintf("MERGE(%s, {[@%s]: %s[@%s]})", rowValuesObject, columnBind, rowValuesSource, columnBind)
		seenRowValueInputs[rowValue.InputColumn] = true
	}
	lines = append(lines,
		fmt.Sprintf("        LET %s = (%s ? %s.system : null)", coded.SystemVariable, valid, candidate),
		fmt.Sprintf("        LET %s = (%s AND TYPENAME(%s.version) == \"string\" AND TRIM(%s.version) != \"\" ? %s.version : null)", coded.VersionVariable, valid, candidate, candidate, candidate),
		fmt.Sprintf("        LET %s = (%s ? %s.code : null)", coded.CodeVariable, valid, candidate),
		fmt.Sprintf("        COLLECT %s = %s, %s = %s, %s = %s",
			systemKeyVariable, coded.SystemVariable, versionKeyVariable, coded.VersionVariable,
			codeKeyVariable, coded.CodeVariable),
		fmt.Sprintf("        RETURN {system: %s, version: %s, code: %s, rowValues: %s}", systemKeyVariable, versionKeyVariable, codeKeyVariable, rowValuesObject),
		"    )",
		fmt.Sprintf("    FOR %s IN %s", tuple, sourceTuples),
	)
	if len(coded.RowValues) == 0 {
		lines = append(lines, fmt.Sprintf("    COLLECT %s = %s.system, %s = %s.version, %s = %s.code AGGREGATE %s = SUM(1)",
			coded.SystemVariable, tuple, coded.VersionVariable, tuple, coded.CodeVariable, tuple, coded.CountVariable))
	} else {
		lines = append(lines,
			fmt.Sprintf("    COLLECT %s = %s.system, %s = %s.version, %s = %s.code INTO %s = %s",
				coded.SystemVariable, tuple, coded.VersionVariable, tuple, coded.CodeVariable, tuple, coded.GroupRowsVariable, tuple),
			fmt.Sprintf("    LET %s = LENGTH(%s)", coded.CountVariable, coded.GroupRowsVariable),
		)
		rowValueLines, rowValueErr := r.renderConstructionRowValueLets(coded.RowValues, coded.GroupRowsVariable, "rowValues")
		if rowValueErr != nil {
			return nil, rowValueErr
		}
		lines = append(lines, rowValueLines...)
	}
	lines = append(lines,
		fmt.Sprintf("    SORT %s ASC, %s ASC, %s ASC", coded.SystemVariable, coded.VersionVariable, coded.CodeVariable),
		fmt.Sprintf("  LET %s = TO_STRING([\"construction\", @%s, \"operation\", \"CODED_GROUP\", \"occurrence\", @%s, \"path\", @%s, \"system\", %s, \"version\", %s, \"code\", %s])",
			coded.IdentityVariable, coded.ConstructionIDBindKey, coded.OccurrenceIDBindKey, coded.CodingPathBindKey,
			coded.SystemVariable, coded.VersionVariable, coded.CodeVariable),
	)
	output, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
	if err != nil {
		return nil, fmt.Errorf("output projection: %w", err)
	}
	lines = append(lines,
		fmt.Sprintf("    LET %s = %s", stage.OutputRowVariable, output),
		"    RETURN "+stage.OutputRowVariable,
	)
	return lines, nil
}

func (r *physicalPlanRenderer) newInternalBindKeyWithValue(base, value string) string {
	key := r.newInternalBindKey(base)
	r.bindVars[key] = value
	return key
}
