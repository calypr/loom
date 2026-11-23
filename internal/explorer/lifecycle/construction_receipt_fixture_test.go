package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/semantic"
)

type lifecycleConstructionColumn struct {
	name, label, logical string
	nullable             bool
	construction         string
}

func lifecycleConstructionReceipt(snapshot capability.Snapshot, workspace ws.Workspace) *receiptmodel.CompilationReceipt {
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
	public := receiptmodel.PublicOutputContract{OutputID: "patients", Columns: make([]receiptmodel.PublicOutputColumn, 0, len(columns))}
	receipt.EmittedColumns = make([]viewer.EmittedColumn, 0, len(columns))
	provenance := make(map[string]string, len(columns))
	for index, column := range columns {
		constructionID := column.construction
		public.Columns = append(public.Columns, receiptmodel.PublicOutputColumn{
			Column: column.name, ConstructionID: constructionID, Label: column.label, LogicalType: column.logical,
			Cardinality: "optional_one", Nullable: column.nullable, Shape: "scalar",
		})
		receipt.EmittedColumns = append(receipt.EmittedColumns, viewer.EmittedColumn{
			EmissionID: fmt.Sprintf("em_%02d", index), OutputID: "patients", PublicColumn: column.name,
			ConstructionID: constructionID, Label: column.label, LogicalType: column.logical,
			Cardinality: "optional_one", Nullable: column.nullable, Shape: "scalar",
		})
		provenance[column.name] = "EXPLICIT"
	}
	contract, _ := json.Marshal(receiptmodel.PublicOutputContracts{Outputs: []receiptmodel.PublicOutputContract{public}})
	receipt.PublicOutputContract = contract
	receipt.OutputContractDigest, _ = receiptmodel.CompilationArtifactDigest(contract)
	receipt.OutputFingerprints = map[string]string{"patients": "fingerprint-" + receipt.IntentDigest}
	receipt.OutputColumnProvenance = map[string]map[string]string{"patients": provenance}
	receipt.CompilationKey, _ = receiptmodel.CompilationKey(*receipt)
	receipt.ID, _ = receiptmodel.ReceiptID(*receipt)
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

func lifecycleTestStringScalar(value string) ws.TableScalar {
	return ws.TableScalar{Kind: ws.TableScalarString, String: &value}
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
