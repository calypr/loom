package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

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
	lines = append(lines,
		fmt.Sprintf("        LET %s = (%s ? %s.system : null)", coded.SystemVariable, valid, candidate),
		fmt.Sprintf("        LET %s = (%s AND TYPENAME(%s.version) == \"string\" AND TRIM(%s.version) != \"\" ? %s.version : null)", coded.VersionVariable, valid, candidate, candidate, candidate),
		fmt.Sprintf("        LET %s = (%s ? %s.code : null)", coded.CodeVariable, valid, candidate),
		fmt.Sprintf("        COLLECT %s = %s, %s = %s, %s = %s",
			systemKeyVariable, coded.SystemVariable, versionKeyVariable, coded.VersionVariable,
			codeKeyVariable, coded.CodeVariable),
		fmt.Sprintf("        RETURN {system: %s, version: %s, code: %s}", systemKeyVariable, versionKeyVariable, codeKeyVariable),
		"    )",
		fmt.Sprintf("    FOR %s IN %s", tuple, sourceTuples),
		fmt.Sprintf("    COLLECT %s = %s.system, %s = %s.version, %s = %s.code AGGREGATE %s = SUM(1)",
			coded.SystemVariable, tuple, coded.VersionVariable, tuple, coded.CodeVariable, tuple, coded.CountVariable),
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
