package explorer

import (
	"encoding/json"
	"time"

	"github.com/calypr/loom/internal/dataset"
)

const (
	ExplorerStateV1APIVersion  = "loom.calypr.org/explorer-state/v1"
	ExplorerStateV1Kind        = "ExplorerState"
	ExplorerBuilderStateV1Kind = "ExplorerBuilderState"
)

// ExplorerSummaryV1 is the intentionally small collection projection. Builder
// intent, catalog data, and resolved bindings are available only from the
// selected Explorer's /authoring/v1/builder resource.
type ExplorerSummaryV1 struct {
	Project          string         `json:"project"`
	ExplorerID       string         `json:"explorerId"`
	Title            string         `json:"title"`
	Management       ManagementMode `json:"management"`
	ActiveRevisionID string         `json:"activeRevisionId,omitempty"`
	UpdatedAt        time.Time      `json:"updatedAt"`
}

// ExplorerBuilderStateV1 is the complete, server-resolved editing model. The
// catalog and bindings are from the same immutable snapshot as Bundle.
type ExplorerBuilderStateV1 struct {
	APIVersion  string                       `json:"apiVersion"`
	Kind        string                       `json:"kind"`
	Project     string                       `json:"project"`
	ExplorerID  string                       `json:"explorerId"`
	Title       string                       `json:"title"`
	Bundle      ExplorerAuthoringBundleV1    `json:"bundle"`
	Catalog     ExplorerBuilderCatalogV1     `json:"catalog"`
	Bindings    []ExplorerResolvedBindingV1  `json:"bindings"`
	Active      ExplorerBuilderPublicationV1 `json:"active"`
	Diagnostics []AuthoringDiagnostic        `json:"diagnostics"`
}

type ExplorerBuilderCatalogV1 struct {
	SnapshotToken            string                  `json:"snapshotToken"`
	Generation               string                  `json:"generation"`
	ResolvedSchemaDigest     string                  `json:"resolvedSchemaDigest,omitempty"`
	AuthorizationScopeDigest string                  `json:"authorizationScopeDigest,omitempty"`
	Nodes                    []ExplorerCatalogNodeV1 `json:"nodes"`
	Edges                    []ExplorerCatalogEdgeV1 `json:"edges"`
	Candidates               []ExplorerCandidateV1   `json:"candidates"`
}

type ExplorerCatalogNodeV1 struct {
	NodeID       string `json:"nodeId"`
	ResourceType string `json:"resourceType"`
}
type ExplorerCatalogEdgeV1 struct {
	EdgeID     string `json:"edgeId"`
	FromNodeID string `json:"fromNodeId"`
	ToNodeID   string `json:"toNodeId"`
	Label      string `json:"label"`
}
type ExplorerCandidateV1 struct {
	CandidateID string `json:"candidateId"`
	NodeID      string `json:"nodeId"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	Filterable  bool   `json:"filterable"`
	Chartable   bool   `json:"chartable"`
}

type ExplorerResolvedBindingV1 struct {
	OutputID           string                         `json:"outputId"`
	OutputTitle        string                         `json:"outputTitle,omitempty"`
	BaseNodeID         string                         `json:"baseNodeId"`
	BaseResourceType   string                         `json:"baseResourceType"`
	RowNodeID          string                         `json:"rowNodeId"`
	RowResourceType    string                         `json:"rowResourceType"`
	RowGrain           string                         `json:"rowGrain"`
	RouteKind          string                         `json:"routeKind"`
	RouteOccurrences   []ExplorerResolvedOccurrenceV1 `json:"routeOccurrences"`
	CandidateEmissions []ExplorerCandidateEmissionV1  `json:"candidateEmissions"`
}
type ExplorerResolvedOccurrenceV1 struct {
	OccurrenceID   string `json:"occurrenceId"`
	Index          int    `json:"index"`
	NodeID         string `json:"nodeId"`
	ResourceType   string `json:"resourceType"`
	IncomingEdgeID string `json:"incomingEdgeId,omitempty"`
}
type ExplorerCandidateEmissionV1 struct {
	CandidateID  string `json:"candidateId"`
	OccurrenceID string `json:"occurrenceId"`
	EmissionID   string `json:"emissionId"`
	// PublicColumn is the exact logical key returned in Builder preview rows.
	// Browsers must never reconstruct this value from a label or identity.
	PublicColumn string `json:"publicColumn"`
	Label        string `json:"label"`
	LogicalType  string `json:"logicalType"`
	Filterable   bool   `json:"filterable"`
	Chartable    bool   `json:"chartable"`
}

// ExplorerPreviewColumnV1 is the authoritative bridge between an authoring
// emission and a preview row. EmissionID owns presentation; PublicColumn owns
// row lookup.
type ExplorerPreviewColumnV1 struct {
	OutputID     string `json:"outputId"`
	CandidateID  string `json:"candidateId"`
	OccurrenceID string `json:"occurrenceId"`
	EmissionID   string `json:"emissionId"`
	PublicColumn string `json:"publicColumn"`
	Label        string `json:"label"`
	LogicalType  string `json:"logicalType"`
	Filterable   bool   `json:"filterable"`
	Chartable    bool   `json:"chartable"`
}
type ExplorerBuilderPublicationV1 struct {
	RevisionID   string    `json:"revisionId,omitempty"`
	State        string    `json:"state,omitempty"`
	Generation   string    `json:"generation,omitempty"`
	IntentDigest string    `json:"intentDigest,omitempty"`
	PublishedAt  time.Time `json:"publishedAt,omitempty"`
	// Migration is a small, non-sensitive projection of immutable revision
	// provenance. Raw source packets remain on the revision for rollback, but
	// the Builder can still tell that its default was imported and identify the
	// source/digests without downloading those packets.
	Migration *ExplorerMigrationSummaryV1 `json:"migration,omitempty"`
}

type ExplorerMigrationSummaryV1 struct {
	Kind                  string    `json:"kind"`
	Source                string    `json:"source"`
	SourceProject         string    `json:"sourceProject"`
	SourceExplorerID      string    `json:"sourceExplorerId"`
	OriginalConfigDigest  string    `json:"originalConfigDigest,omitempty"`
	OriginalMappingDigest string    `json:"originalMappingDigest,omitempty"`
	MigratedAt            time.Time `json:"migratedAt"`
}

// ExplorerStateV1 is the server-owned lifecycle projection consumed by the
// Builder. It carries authoring bundles and publication metadata, but never
// exposes the legacy ExplorerConfig packet or the compiled recipe AST.
type ExplorerStateV1 struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Project    string         `json:"project"`
	ExplorerID string         `json:"explorerId"`
	Title      string         `json:"title"`
	Management ManagementMode `json:"management"`

	Draft     ExplorerStateV1Draft     `json:"draft"`
	Active    ExplorerStateV1Active    `json:"active"`
	Generated ExplorerStateV1Generated `json:"generated"`
	Runtime   *ExplorerRuntimeV1       `json:"runtime,omitempty"`

	ActiveURL string    `json:"activeUrl"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ExplorerStateV1Draft struct {
	Bundle       json.RawMessage `json:"bundle,omitempty"`
	Version      int64           `json:"version"`
	Digest       string          `json:"digest"`
	IntentDigest string          `json:"intentDigest,omitempty"`
	ReceiptID    string          `json:"receiptId,omitempty"`
}

type ExplorerStateV1Active struct {
	Bundle       json.RawMessage `json:"bundle,omitempty"`
	RevisionID   string          `json:"revisionId,omitempty"`
	IntentDigest string          `json:"intentDigest,omitempty"`
	Status       string          `json:"status,omitempty"`
}

type ExplorerStateV1Generated struct {
	RecipeDigest         string              `json:"recipeDigest,omitempty"`
	ResolvedSchemaDigest string              `json:"resolvedSchemaDigest,omitempty"`
	SourceGeneration     string              `json:"sourceGeneration,omitempty"`
	EmittedColumns       []EmittedColumn     `json:"emittedColumns,omitempty"`
	Materializations     []Materialization   `json:"materializations,omitempty"`
	Dataset              DatasetMetadata     `json:"dataset,omitempty"`
	Publication          PublicationMetadata `json:"publication,omitempty"`
	Diagnostics          []Diagnostic        `json:"diagnostics,omitempty"`
}

// ExplorerRuntimeV1 is the renderer-ready projection of one active Explorer
// release. Loom owns this projection so browsers never need to reconstruct
// runtime bindings from authoring intent, compiled configuration, or physical
// publication metadata.
type ExplorerRuntimeV1 struct {
	Generation    string                                `json:"generation,omitempty"`
	Publication   PublicationMetadata                   `json:"publication,omitempty"`
	Schema        ExplorerRuntimeSchemaV1               `json:"schema,omitempty"`
	Outputs       []ExplorerRuntimeOutputV1             `json:"outputs"`
	SharedFilters map[string][]ExplorerRuntimeBindingV1 `json:"sharedFilters"`
	Diagnostics   []Diagnostic                          `json:"diagnostics"`
}

type ExplorerRuntimeSchemaV1 struct {
	Digest  string `json:"digest,omitempty"`
	Version string `json:"version,omitempty"`
}

type ExplorerRuntimeOutputV1 struct {
	OutputID        string                     `json:"outputId"`
	Name            string                     `json:"name"`
	Title           string                     `json:"title"`
	RowLabel        string                     `json:"rowLabel"`
	Selector        dataset.DataframeSelector  `json:"selector"`
	Columns         []ExplorerRuntimeColumnV1  `json:"columns"`
	Table           ExplorerRuntimeTableV1     `json:"table"`
	Filters         []ExplorerRuntimeBindingV1 `json:"filters"`
	Charts          []ExplorerRuntimeBindingV1 `json:"charts"`
	FixedFilters    map[string][]string        `json:"fixedFilters"`
	Materialization *Materialization           `json:"materialization,omitempty"`
}

type ExplorerRuntimeColumnV1 struct {
	EmissionID   string `json:"emissionId"`
	Name         string `json:"name"`
	Label        string `json:"label"`
	LogicalType  string `json:"logicalType"`
	Visible      bool   `json:"visible"`
	Order        int    `json:"order"`
	Repeated     bool   `json:"repeated,omitempty"`
	Filterable   bool   `json:"filterable"`
	Sortable     bool   `json:"sortable,omitempty"`
	Chartable    bool   `json:"chartable"`
	Aggregatable bool   `json:"aggregatable,omitempty"`
}

type ExplorerRuntimeTableV1 struct {
	Columns []ExplorerRuntimeTableColumnV1 `json:"columns"`
}

type ExplorerRuntimeTableColumnV1 struct {
	EmissionID string `json:"emissionId"`
	Visible    bool   `json:"visible"`
}

type ExplorerRuntimeBindingV1 struct {
	EmissionID string `json:"emissionId"`
	OutputID   string `json:"outputId,omitempty"`
	Label      string `json:"label,omitempty"`
	Type       string `json:"type,omitempty"`
	Title      string `json:"title,omitempty"`
}
