package explorer

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
)

const (
	CompilationReceiptFormatVersion                    = receiptmodel.CompilationReceiptFormatVersion
	previousCompilationReceiptFormatVersion            = 5
	legacyCompilationReceiptFormatVersion              = 3
	CompilationReceiptCompilerContractVersion          = receiptmodel.CompilationReceiptCompilerContractVersion
	legacyCompilationReceiptV20CompilerContractVersion = "loom.explorer.compiler/v20"
	legacyCompilationReceiptV19CompilerContractVersion = "loom.explorer.compiler/v19"
	legacyCompilationReceiptV18CompilerContractVersion = "loom.explorer.compiler/v18"
	legacyCompilationReceiptV17CompilerContractVersion = "loom.explorer.compiler/v17"
	legacyCompilationReceiptV16CompilerContractVersion = "loom.explorer.compiler/v16"
	previousCompilationReceiptCompilerContractVersion  = "loom.explorer.compiler/v15"
	legacyCompilationReceiptB06CompilerContractVersion = "loom.explorer.compiler/v14"
	legacyCompilationReceiptV13CompilerContractVersion = "loom.explorer.compiler/v13"
	legacyCompilationReceiptCompilerContractVersion    = "loom.explorer.compiler/v12"
	legacyCompilationReceiptOlderContractVersion       = "loom.explorer.compiler/v11"
	legacyCompilationReceiptV10ContractVersion         = "loom.explorer.compiler/v10"
	CurrentReceiptFormatVersion                        = receiptmodel.CurrentReceiptFormatVersion
	CurrentCompilerContractVersion                     = receiptmodel.CurrentCompilerContractVersion
	CurrentCompilationContractVersion                  = receiptmodel.CurrentCompilationContractVersion
)

type CompilationReceipt = receiptmodel.CompilationReceipt
type ReceiptCompiledOutputColumn = receiptmodel.ReceiptCompiledOutputColumn
type ReceiptConstructionStage = receiptmodel.ReceiptConstructionStage
type ReceiptConstructionStageColumn = receiptmodel.ReceiptConstructionStageColumn
type ReceiptConstructionOperationChoice = receiptmodel.ReceiptConstructionOperationChoice
type ReceiptConstructionRelatedExpandAnchor = receiptmodel.ReceiptConstructionRelatedExpandAnchor
type ReceiptConstructionRelatedExpand = receiptmodel.ReceiptConstructionRelatedExpand
type ReceiptConstructionActiveRelatedRecord = receiptmodel.ReceiptConstructionActiveRelatedRecord
type ReceiptConstructionCodedGroupChoice = receiptmodel.ReceiptConstructionCodedGroupChoice
type ReceiptConstructionGroupFieldSourceContext = receiptmodel.ReceiptConstructionGroupFieldSourceContext
type ReceiptConstructionGroupFieldSourceExpansion = receiptmodel.ReceiptConstructionGroupFieldSourceExpansion
type ReceiptConstructionGroupFieldSourceRouteStep = receiptmodel.ReceiptConstructionGroupFieldSourceRouteStep
type ReceiptConstructionGroupFieldSourceAncestor = receiptmodel.ReceiptConstructionGroupFieldSourceAncestor
type ReceiptConstructionGroupFieldOwner = receiptmodel.ReceiptConstructionGroupFieldOwner
type IdentityMapping = receiptmodel.IdentityMapping
type CompilationWarning = receiptmodel.CompilationWarning

var (
	ReceiptContractSupportedForExecution = receiptmodel.ReceiptContractSupportedForExecution
	CompilationArtifactDigest            = receiptmodel.CompilationArtifactDigest
	CompilationKey                       = receiptmodel.CompilationKey
	ReceiptID                            = receiptmodel.ReceiptID
)

func validateReceiptConstructionStages(stagesByOutput map[string][]ReceiptConstructionStage, bundle recipe.Bundle) error {
	return receiptmodel.ValidateReceiptConstructionStages(stagesByOutput, bundle)
}

func validateReceiptCompiledOutputSchemas(schemas map[string][]ReceiptCompiledOutputColumn, bundle recipe.Bundle) error {
	return receiptmodel.ValidateReceiptCompiledOutputSchemas(schemas, bundle)
}

func validateReceiptRawJSON(raw []byte) error {
	return receiptmodel.ValidateReceiptRawJSON(raw)
}
