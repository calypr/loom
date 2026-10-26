package compiler

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// TestPivotPresenceAudit is a diagnostic matrix, not a regression gate. Run it
// with LOOM_PIVOT_PRESENCE_AUDIT=1 when changing category discovery support.
func TestPivotPresenceAudit(t *testing.T) {
	if os.Getenv("LOOM_PIVOT_PRESENCE_AUDIT") == "" {
		t.Skip("set LOOM_PIVOT_PRESENCE_AUDIT=1 to print the Pivot category presence matrix")
	}
	fixtures := []struct {
		name   string
		output recipe.Output
	}{
		{name: "source-derive-filter", output: categoryScanConstructionRecipeOutput()},
		{name: "related", output: relatedCategoryScanOutput(relatedCategoryScanOptions{})},
		{name: "related-unsafe", output: relatedCategoryScanOutput(relatedCategoryScanOptions{unsupportedStage: true})},
		{name: "related-root-filter", output: relatedCategoryScanOutput(relatedCategoryScanOptions{rootFilter: true})},
		{name: "related-value-from-patient", output: relatedCategoryScanOutput(relatedCategoryScanOptions{valueFromPatient: true})},
		{name: "related-empty-exclude", output: relatedCategoryScanOutput(relatedCategoryScanOptions{secondEmptyPolicy: recipe.ExpansionExclude})},
		{name: "expand", output: constructionExpandOutput()},
		{name: "expand-group", output: constructionExpandGroupOutput()},
		{name: "group", output: constructionGroupRowValuesOutput()},
		{name: "group-pivot", output: constructionCountRowsGroupPivotLineageOutput()},
		{name: "pivot", output: constructionPreviewMultiKeyPivotOutput()},
		{name: "unpivot", output: constructionOracleOutput()},
	}
	var rows []string
	for _, fixture := range fixtures {
		base := compilePopulationMappingOutput(t, fixture.output)
		for _, stage := range base.Stages {
			eligible := eligibleCategoryColumns(stage.Columns)
			for _, category := range eligible {
				var value *lower.CompiledOutputColumn
				for index := range eligible {
					if eligible[index].ID != category.ID {
						value = &eligible[index]
						break
					}
				}
				if value == nil {
					continue
				}
				audited := appendPresenceAuditPivot(fixture.output, stage, category, *value)
				compiled := compilePopulationMappingOutput(t, audited)
				result := auditCategoryAtStage(compiled, stage, category)
				if fixture.name == "unpivot" && stage.ID == "unpivot" && (category.Name == "amount" || category.Name == "measure") {
					scanned, err := CompileCategoryScanStageWithPolicy(compiled, stage.ID, category.ID, value.ID, MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy())
					if err == nil {
						for _, line := range strings.Split(scanned.Query, "\n") {
							if strings.Contains(line, "__loom_category_scan_presence") {
								t.Logf("MARKER %s/%s: %s", stage.ID, category.Name, strings.TrimSpace(line))
							}
						}
					}
				}
				rows = append(rows, fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%d\t%d\t%t\t%t\t%t\t%s",
					fixture.name, stage.ID, stage.Operation, category.Name, result.status,
					result.queryLen, result.forLoops, result.presence, result.indexed, result.witness, result.detail))
			}
		}
	}
	sort.Strings(rows)
	for _, row := range rows {
		t.Log(row)
	}
}

type categoryAuditResult struct {
	status   string
	queryLen int
	forLoops int
	presence bool
	indexed  bool
	witness  bool
	detail   string
}

func eligibleCategoryColumns(columns []lower.CompiledOutputColumn) []lower.CompiledOutputColumn {
	var eligible []lower.CompiledOutputColumn
	for _, column := range columns {
		if _, err := categoryScanColumn(columns, column.Name); err == nil {
			eligible = append(eligible, column)
		}
	}
	return eligible
}

func auditCategoryAtStage(output lower.CompiledRecipeOutput, stage lower.CompiledStageDescriptor, category lower.CompiledOutputColumn) categoryAuditResult {
	lastCode := ""
	for _, value := range eligibleCategoryColumns(stage.Columns) {
		if value.ID == category.ID {
			continue
		}
		scanned, err := CompileCategoryScanStageWithPolicy(output, stage.ID, category.ID, value.ID, MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			if code, ok := CategoryScanRefusalCodeOf(err); ok {
				lastCode = string(code)
			} else {
				lastCode = err.Error()
			}
			continue
		}
		return categoryAuditResult{
			status:   "SUPPORTED",
			queryLen: len(scanned.Query),
			forLoops: strings.Count(scanned.Query, "FOR "),
			presence: scanned.Proof.PresenceTracked,
			indexed:  scanned.CategoryIndex != nil || scanned.PreviewCoveringIndex != nil,
			witness:  scanned.OverflowWitness != nil,
			detail:   "value=" + value.Name,
		}
	}
	return categoryAuditResult{status: "REFUSED", detail: lastCode}
}

func appendPresenceAuditPivot(output recipe.Output, stage lower.CompiledStageDescriptor, category, value lower.CompiledOutputColumn) recipe.Output {
	var groupKey *lower.CompiledOutputColumn
	for index := range stage.Columns {
		column := &stage.Columns[index]
		if column.ID == category.ID || column.ID == value.ID || column.Internal || column.Identity {
			continue
		}
		if _, err := categoryScanColumn(stage.Columns, column.Name); err == nil {
			groupKey = column
			break
		}
	}
	if groupKey == nil {
		return output
	}
	cloned := output
	construction := recipe.Construction{Version: 1}
	if output.Construction != nil {
		construction = *output.Construction
	}
	construction.Steps = append([]recipe.ConstructionStep(nil), construction.Steps...)
	input := recipe.ConstructionInputRef{Kind: recipe.ConstructionSourceProjectionInput}
	if stage.ID == recipe.ConstructionSourceProjectionID {
		construction.Steps = nil
	} else {
		for index := range construction.Steps {
			if construction.Steps[index].ID == stage.ID {
				construction.Steps = construction.Steps[:index+1]
				input = recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: stage.ID}
				break
			}
		}
	}
	probeID := "pivot_presence_audit_probe"
	construction.Steps = append(construction.Steps, recipe.ConstructionStep{
		ID:     "pivot_presence_audit",
		Inputs: []recipe.ConstructionInputRef{input},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
			ConstructionID:   "pivot_presence_audit",
			GroupKeyIDs:      []string{groupKey.ID},
			CategoryColumnID: category.ID,
			ValueColumnID:    value.ID,
			Categories: []recipe.ConstructionPivotCategory{{
				Key:            presenceAuditProbeKey(category.Kind),
				OutputColumnID: probeID,
			}},
			DuplicatePolicy:        recipe.PivotDuplicateError,
			MissingCellPolicy:      recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		}},
		Outputs: []recipe.StageColumn{
			{ID: groupKey.ID, Name: groupKey.Name, Type: groupKey.Kind},
			{ID: probeID, Name: "__category_discovery_probe", Type: value.Kind},
		},
	})
	cloned.Construction = &construction
	return cloned
}

func presenceAuditProbeKey(kind string) recipe.TableScalar {
	switch kind {
	case "integer":
		value := int64(1)
		return recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &value}
	case "decimal":
		value := 1.5
		return recipe.TableScalar{Kind: recipe.TableScalarDecimal, Decimal: &value}
	case "boolean":
		value := true
		return recipe.TableScalar{Kind: recipe.TableScalarBoolean, Boolean: &value}
	default:
		value := "__audit_value__"
		return recipe.TableScalar{Kind: recipe.TableScalarString, String: &value}
	}
}
