package explorer

import "github.com/calypr/loom/internal/explorer/viewer"

const (
	ManagementRepository  = viewer.ManagementRepository
	ManagementInteractive = viewer.ManagementInteractive

	ExplorerStateV1APIVersion     = viewer.ExplorerStateV1APIVersion
	ExplorerStateV1Kind           = viewer.ExplorerStateV1Kind
	ExplorerRuntimeV1NotPublished = viewer.ExplorerRuntimeV1NotPublished
)

type ManagementMode = viewer.ManagementMode
type Diagnostic = viewer.Diagnostic
type DatasetMetadata = viewer.DatasetMetadata
type DatasetOutput = viewer.DatasetOutput
type PublicationMetadata = viewer.PublicationMetadata
type Materialization = viewer.Materialization
type EmittedColumn = viewer.EmittedColumn
type PublicUnitRuleIdentity = viewer.PublicUnitRuleIdentity
type PublicUnitNormalization = viewer.PublicUnitNormalization
type ExplorerSummaryV1 = viewer.ExplorerSummaryV1
type ExplorerStateV1 = viewer.ExplorerStateV1
type ExplorerStateV1Draft = viewer.ExplorerStateV1Draft
type ExplorerStateV1Active = viewer.ExplorerStateV1Active
type ExplorerStateV1Generated = viewer.ExplorerStateV1Generated
type ExplorerRuntimeV1 = viewer.ExplorerRuntimeV1
type ExplorerRuntimeSchemaV1 = viewer.ExplorerRuntimeSchemaV1
type ExplorerRuntimeOutputV1 = viewer.ExplorerRuntimeOutputV1
type ExplorerRuntimeColumnV1 = viewer.ExplorerRuntimeColumnV1
type ExplorerRuntimeTableV1 = viewer.ExplorerRuntimeTableV1
type ExplorerRuntimeTableColumnV1 = viewer.ExplorerRuntimeTableColumnV1
type ExplorerRuntimeBindingV1 = viewer.ExplorerRuntimeBindingV1
