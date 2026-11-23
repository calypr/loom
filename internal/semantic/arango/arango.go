package arango

import catalogarango "github.com/calypr/loom/internal/catalog/arango"

type SemanticInventoryStore = catalogarango.SemanticInventoryStore
type SemanticCoverageStore = catalogarango.SemanticCoverageStore
type RetainedSemanticInventoryRow = catalogarango.RetainedSemanticInventoryRow
type RetainedSemanticInventoryPage = catalogarango.RetainedSemanticInventoryPage
type SemanticCoverageSourceSetAccumulator = catalogarango.SemanticCoverageSourceSetAccumulator

var (
	NewSemanticCoverageSourceSetAccumulator = catalogarango.NewSemanticCoverageSourceSetAccumulator
	EncodeSemanticCoverageSourceEnvelope    = catalogarango.EncodeSemanticCoverageSourceEnvelope
	ErrInvalidSemanticInventoryPage         = catalogarango.ErrInvalidSemanticInventoryPage
	ErrInvalidSemanticInventorySelection    = catalogarango.ErrInvalidSemanticInventorySelection
	ErrSemanticInventoryClaimed             = catalogarango.ErrSemanticInventoryClaimed
	ErrSemanticInventoryClaimLost           = catalogarango.ErrSemanticInventoryClaimLost
)
