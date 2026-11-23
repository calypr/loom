package authoringv2

import "github.com/calypr/loom/internal/explorer/semanticauthoring"

type SemanticReadinessStatus = semanticauthoring.SemanticReadinessStatus
type SemanticSelectionReadiness = semanticauthoring.SemanticSelectionReadiness
type SemanticSelectionPlan = semanticauthoring.SemanticSelectionPlan

const (
	SemanticReadinessReady            = semanticauthoring.SemanticReadinessReady
	SemanticReadinessReadyWithWarning = semanticauthoring.SemanticReadinessReadyWithWarning
	SemanticReadinessNeedsMapping     = semanticauthoring.SemanticReadinessNeedsMapping
	SemanticReadinessUnsupported      = semanticauthoring.SemanticReadinessUnsupported
)

var ResolveSemanticSelectionPlan = semanticauthoring.ResolveSemanticSelectionPlan
