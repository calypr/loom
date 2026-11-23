package receiptmodel

import "github.com/calypr/loom/internal/explorer"

type CompilationReceipt = explorer.CompilationReceipt
type ReceiptCompiledOutputColumn = explorer.ReceiptCompiledOutputColumn
type ReceiptConstructionStage = explorer.ReceiptConstructionStage
type ReceiptConstructionStageColumn = explorer.ReceiptConstructionStageColumn
type ReceiptConstructionOperationChoice = explorer.ReceiptConstructionOperationChoice
type ReceiptConstructionGroupFieldSourceContext = explorer.ReceiptConstructionGroupFieldSourceContext
type ReceiptConstructionGroupFieldSourceExpansion = explorer.ReceiptConstructionGroupFieldSourceExpansion
type ReceiptConstructionGroupFieldSourceRouteStep = explorer.ReceiptConstructionGroupFieldSourceRouteStep
type ReceiptConstructionGroupFieldSourceAncestor = explorer.ReceiptConstructionGroupFieldSourceAncestor
type ReceiptConstructionGroupFieldOwner = explorer.ReceiptConstructionGroupFieldOwner
type ReceiptConstructionCodedGroupChoice = explorer.ReceiptConstructionCodedGroupChoice
type ReceiptConstructionRelatedExpandAnchor = explorer.ReceiptConstructionRelatedExpandAnchor
type ReceiptConstructionRelatedExpand = explorer.ReceiptConstructionRelatedExpand
type ReceiptConstructionActiveRelatedRecord = explorer.ReceiptConstructionActiveRelatedRecord
type PublicOutputContracts = explorer.PublicOutputContracts
type PublicOutputContract = explorer.PublicOutputContract
type PublicOutputColumn = explorer.PublicOutputColumn

var (
	DecodePublicOutputContracts          = explorer.DecodePublicOutputContracts
	ReceiptContractSupportedForExecution = explorer.ReceiptContractSupportedForExecution
	ReceiptID                            = explorer.ReceiptID
)
