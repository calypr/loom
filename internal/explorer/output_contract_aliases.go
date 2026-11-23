package explorer

import "github.com/calypr/loom/internal/explorer/receiptmodel"

var ErrReceiptRecompileRequired = receiptmodel.ErrReceiptRecompileRequired

type PublicOutputContracts = receiptmodel.PublicOutputContracts
type PublicOutputContract = receiptmodel.PublicOutputContract
type PublicOutputColumn = receiptmodel.PublicOutputColumn

var DecodePublicOutputContracts = receiptmodel.DecodePublicOutputContracts
