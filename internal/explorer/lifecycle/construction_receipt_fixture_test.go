package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/semantic"
)

type lifecycleConstructionColumn struct {
	name, label, logical string
	nullable             bool
	construction         string
}

func lifecycleConstructionReceipt(snapshot capability.Snapshot, workspace authoringv2.Workspace) *explorer.CompilationReceipt {
	receipt := nativeReceipt(snapshot)
	receipt.IntentDigest, _ = workspace.Digest()
	receipt.NormalizedBundle, _ = workspace.CanonicalJSON()
	columns := []lifecycleConstructionColumn{
		{name: "patient_id", label: "Patient ID", logical: "string"},
		{name: "category", label: "Category", logical: "string", nullable: true},
		{name: "value", label: "Value", logical: "decimal", nullable: true},
		{name: "value_alt", label: "Alternate value", logical: "decimal"},
		{name: "category_number", label: "Numeric category", logical: "integer"},
		{name: "category_boolean", label: "Boolean category", logical: "boolean"},
		{name: "text_value", label: "Text value", logical: "string"},
		{name: "overflow", label: "Overflow category", logical: "string"},
	}
	public := explorer.PublicOutputContract{OutputID: "patients", Columns: make([]explorer.PublicOutputColumn, 0, len(columns))}
	receipt.EmittedColumns = make([]explorer.EmittedColumn, 0, len(columns))
	provenance := make(map[string]string, len(columns))
	for index, column := range columns {
		constructionID := column.construction
		public.Columns = append(public.Columns, explorer.PublicOutputColumn{
			Column: column.name, ConstructionID: constructionID, Label: column.label, LogicalType: column.logical,
			Cardinality: "optional_one", Nullable: column.nullable, Shape: "scalar",
		})
		receipt.EmittedColumns = append(receipt.EmittedColumns, explorer.EmittedColumn{
			EmissionID: fmt.Sprintf("em_%02d", index), OutputID: "patients", PublicColumn: column.name,
			ConstructionID: constructionID, Label: column.label, LogicalType: column.logical,
			Cardinality: "optional_one", Nullable: column.nullable, Shape: "scalar",
		})
		provenance[column.name] = "EXPLICIT"
	}
	contract, _ := json.Marshal(explorer.PublicOutputContracts{Outputs: []explorer.PublicOutputContract{public}})
	receipt.PublicOutputContract = contract
	receipt.OutputContractDigest, _ = explorer.CompilationArtifactDigest(contract)
	receipt.OutputFingerprints = map[string]string{"patients": "fingerprint-" + receipt.IntentDigest}
	receipt.OutputColumnProvenance = map[string]map[string]string{"patients": provenance}
	receipt.CompilationKey, _ = explorer.CompilationKey(*receipt)
	receipt.ID, _ = explorer.ReceiptID(*receipt)
	return receipt
}

func lifecycleErrorCode(err error) string {
	var lifecycleErr *ports.Error
	if errors.As(err, &lifecycleErr) {
		return lifecycleErr.Code
	}
	return ""
}

func proposalErrorIs(err error, class ports.ErrorClass, code string) bool {
	var lifecycleErr *ports.Error
	return errors.As(err, &lifecycleErr) && lifecycleErr.Class == class && lifecycleErr.Code == code
}

func lifecycleTestStringScalar(value string) authoringv2.TableScalar {
	return authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &value}
}

func testCodedCategoryFamily(sourceKind, resourceType, sourcePath, system, version string) semantic.Family {
	reference := semantic.FamilyReference{
		BindingID: "binding-" + resourceType, SourceKind: sourceKind, ResourceType: resourceType,
		SourcePath: sourcePath, System: system, Version: version,
	}
	reference.FamilyID = semantic.FamilyID(reference)
	return semantic.Family{
		FamilyReference: reference,
		Source:          semantic.Source{Type: resourceType, Path: sourcePath},
		KeySelector:     "code.coding[]",
		Value: semantic.Value{
			Selector: "code.coding[].display", Type: "string",
			FallbackSelectors: []string{"code.coding[].code"}, FallbackOnBlankString: true,
		},
		LogicalType: "string", SchemaVersion: semantic.ObservationSchemaVersion,
		RuleVersion:  strconv.Itoa(semantic.SemanticObservationRuleVersion),
		Completeness: semantic.CompletenessComplete, Status: "SUPPORTED", Population: 2,
		Examples: []string{"CARBOXYAMIDOTRIAZOLE", "another compound"},
	}
}
