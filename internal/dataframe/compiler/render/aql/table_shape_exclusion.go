package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderTableShapeExclusionReturn(terminal ir.PhysicalTableShapeExclusionReturn) ([]string, error) {
	pivot := terminal.Pivot
	input, err := r.renderReturn(ir.PhysicalReturn{Projections: pivot.InputProjections})
	if err != nil {
		return nil, fmt.Errorf("render exclusion pivot inputs: %w", err)
	}
	input, err = r.renderTraceSourceInput(input, pivot.InputProjections, pivot.OutputRowVariable)
	if err != nil {
		return nil, fmt.Errorf("render exclusion source identity: %w", err)
	}

	categoryColumnBind := r.newInternalBindKey("exclusion_category_column")
	r.bindVars[categoryColumnBind] = pivot.CategoryColumn
	categoryPresenceBind := ""
	if pivot.CategoryPresence != nil {
		presence, presenceErr := r.renderGroupedPivotPresence(*pivot.CategoryPresence)
		if presenceErr != nil {
			return nil, fmt.Errorf("render exclusion category presence: %w", presenceErr)
		}
		presenceNameBind := r.newInternalBindKey("exclusion_category_presence_column")
		r.bindVars[presenceNameBind] = pivot.CategoryPresenceColumn
		categoryPresenceBind = presenceNameBind
		input = fmt.Sprintf("MERGE(%s, {[@%s]: %s})", input, presenceNameBind, presence)
	}
	categoryType, err := aqlTableScalarType(pivot.CategoryType)
	if err != nil {
		return nil, err
	}
	categoryTypeBind := r.newInternalBindKey("exclusion_category_type")
	r.bindVars[categoryTypeBind] = categoryType
	categoryKindBind := r.newInternalBindKey("exclusion_category_kind")
	r.bindVars[categoryKindBind] = pivot.CategoryType

	inputVariable := pivot.InputRowVariable
	listed, err := groupedPivotListedPredicate(pivot, inputVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind)
	if err != nil {
		return nil, err
	}
	lines := []string{
		fmt.Sprintf("LET %s = %s", inputVariable, input),
		fmt.Sprintf("FILTER NOT %s", listed),
	}

	groupValues := make([]string, 0, len(pivot.GroupKeys))
	for index, key := range pivot.GroupKeys {
		columnBind := r.newInternalBindKey(fmt.Sprintf("exclusion_group_column_%d", index))
		r.bindVars[columnBind] = key.Column
		groupValues = append(groupValues, fmt.Sprintf("%s[@%s]", inputVariable, columnBind))
	}
	rowIdentity, err := r.renderGroupedPivotIdentity(pivot, groupValues)
	if err != nil {
		return nil, err
	}
	rowIdentityVariable := r.newInternalVariable("exclusion_output_row_id")
	lines = append(lines, fmt.Sprintf("LET %s = %s", rowIdentityVariable, rowIdentity))

	documentFieldBind := r.newInternalBindKey("exclusion_source_document_field")
	r.bindVars[documentFieldBind] = ir.PhysicalCellTraceSourceDocumentField
	documentVariable := r.newInternalVariable("exclusion_source_document")
	lines = append(lines, fmt.Sprintf("LET %s = %s[@%s]", documentVariable, inputVariable, documentFieldBind))
	identityAvailable := r.newInternalVariable("exclusion_identity_available")
	lines = append(lines, fmt.Sprintf("LET %s = IS_OBJECT(%s) AND IS_STRING(%s.resourceType) AND %s.resourceType != \"\" AND IS_STRING(%s.id) AND %s.id != \"\"", identityAvailable, documentVariable, documentVariable, documentVariable, documentVariable, documentVariable))

	categoryPresent := "HAS(" + inputVariable + ", @" + categoryColumnBind + ")"
	if categoryPresenceBind != "" {
		categoryPresent = fmt.Sprintf("%s[@%s] == true", inputVariable, categoryPresenceBind)
	}
	categoryPresentVariable := r.newInternalVariable("exclusion_category_present")
	categoryValueVariable := r.newInternalVariable("exclusion_category_value")
	stableRowColumn := ""
	for _, projection := range pivot.InputProjections {
		if projection.Name == "__loom_row_id" {
			stableRowColumn = projection.Name
			break
		}
		if projection.Name == "_key" {
			stableRowColumn = projection.Name
		}
	}
	if stableRowColumn == "" {
		return nil, fmt.Errorf("table-shape exclusion input has no stable row tie-breaker")
	}
	stableRowColumnBind := r.newInternalBindKey("exclusion_stable_row_column")
	r.bindVars[stableRowColumnBind] = stableRowColumn
	lines = append(lines,
		fmt.Sprintf("LET %s = %s", categoryPresentVariable, categoryPresent),
		fmt.Sprintf("LET %s = %s ? %s[@%s] : null", categoryValueVariable, categoryPresentVariable, inputVariable, categoryColumnBind),
		fmt.Sprintf("SORT %s ASC, %s DESC, %s.resourceType ASC, %s.id ASC, %s ASC, %s ASC, %s[@%s] ASC", rowIdentityVariable, identityAvailable, documentVariable, documentVariable, categoryPresentVariable, categoryValueVariable, inputVariable, stableRowColumnBind),
		fmt.Sprintf("LIMIT @%s, @%s", terminal.OffsetBindKey, terminal.FetchLimitBindKey),
	)

	field := func(prefix, name string) string {
		key := r.newInternalBindKey(prefix)
		r.bindVars[key] = name
		return key
	}
	resourceTypeField := field("exclusion_resource_type_name", ir.PhysicalTableShapeExclusionResourceTypeField)
	resourceIDField := field("exclusion_resource_id_name", ir.PhysicalTableShapeExclusionResourceIDField)
	identityStatusField := field("exclusion_identity_status_name", ir.PhysicalTableShapeExclusionIdentityStatusField)
	categoryValueField := field("exclusion_category_value_name", ir.PhysicalTableShapeExclusionCategoryValueField)
	categoryPresentField := field("exclusion_category_present_name", ir.PhysicalTableShapeExclusionCategoryPresentField)
	categoryTypeField := field("exclusion_category_type_name", ir.PhysicalTableShapeExclusionCategoryTypeField)
	rowIdentityField := field("exclusion_row_identity_name", ir.PhysicalTableShapeExclusionOutputRowIDField)
	reasonField := field("exclusion_reason_name", ir.PhysicalTableShapeExclusionReasonField)
	omissionField := field("exclusion_omission_name", ir.PhysicalTableShapeExclusionOmissionField)
	lines = append(lines, fmt.Sprintf(
		"RETURN { [@%s]: (%s ? %s.resourceType : null), [@%s]: (%s ? %s.id : null), [@%s]: (%s ? @%s : @%s), [@%s]: %s, [@%s]: %s, [@%s]: @%s, [@%s]: %s, [@%s]: @%s, [@%s]: (%s ? null : @%s) }",
		resourceTypeField, identityAvailable, documentVariable,
		resourceIDField, identityAvailable, documentVariable,
		identityStatusField, identityAvailable,
		field("exclusion_identity_exact", ir.PhysicalTableShapeExclusionIdentityExact),
		field("exclusion_identity_unavailable", ir.PhysicalTableShapeExclusionIdentityUnavailable),
		categoryValueField, categoryValueVariable,
		categoryPresentField, categoryPresentVariable,
		categoryTypeField, categoryKindBind,
		rowIdentityField, rowIdentityVariable,
		reasonField, field("exclusion_unlisted_reason", ir.PhysicalTableShapeExclusionReasonUnlistedCategory),
		omissionField, identityAvailable,
		field("exclusion_missing_identity_omission", ir.PhysicalTableShapeExclusionSourceIdentityUnavailable),
	))
	return lines, nil
}

func (r *physicalPlanRenderer) renderGroupedPivotIdentity(pivot ir.PhysicalGroupedPivot, keyValues []string) (string, error) {
	if len(keyValues) != len(pivot.GroupKeys) {
		return "", fmt.Errorf("grouped pivot identity has %d key values, want %d", len(keyValues), len(pivot.GroupKeys))
	}
	if _, ok := r.bindVars[pivot.ConstructionIDBindKey]; !ok {
		return "", fmt.Errorf("grouped pivot construction ID bind %q is missing", pivot.ConstructionIDBindKey)
	}
	parts := make([]string, 0, 2+len(pivot.GroupKeys))
	parts = append(parts, `"GROUPED_PIVOT"`, "@"+pivot.ConstructionIDBindKey)
	for index, key := range pivot.GroupKeys {
		parts = append(parts, `["`+key.Kind+`", `+keyValues[index]+`]`)
	}
	return "TO_STRING([" + strings.Join(parts, ", ") + "])", nil
}
