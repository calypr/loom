package explorer

import (
	"encoding/json"
	"time"

	"github.com/calypr/loom/internal/dataset"
)

const (
	ExplorerStateV1APIVersion     = "loom.calypr.org/explorer-state/v1"
	ExplorerStateV1Kind           = "ExplorerState"
	ExplorerRuntimeV1NotPublished = "NOT_PUBLISHED"
)

type ExplorerSummaryV1 struct {
	Project          string         `json:"project"`
	ExplorerID       string         `json:"explorerId"`
	Title            string         `json:"title"`
	Management       ManagementMode `json:"management"`
	ActiveRevisionID string         `json:"activeRevisionId,omitempty"`
	UpdatedAt        time.Time      `json:"updatedAt"`
}

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
	// Runtime is explicit so an Explorer with no executable query is still a
	// valid response. Clients can inspect generated.publication.state or the
	// null runtime instead of treating a missing field as a malformed payload.
	Runtime *ExplorerRuntimeV1 `json:"runtime"`

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

type ExplorerRuntimeV1 struct {
	Status        string                                `json:"status"`
	Generation    string                                `json:"generation,omitempty"`
	Publication   PublicationMetadata                   `json:"publication,omitempty"`
	Schema        ExplorerRuntimeSchemaV1               `json:"schema,omitempty"`
	Outputs       []ExplorerRuntimeOutputV1             `json:"outputs"`
	SharedFilters map[string][]ExplorerRuntimeBindingV1 `json:"sharedFilters"`
	FileActions   FileActions                           `json:"fileActions,omitempty"`
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
	Actions         []ConfigAction             `json:"actions,omitempty"`
	Materialization *Materialization           `json:"materialization,omitempty"`
}

type ExplorerRuntimeColumnV1 struct {
	Column       string `json:"column"`
	EmissionID   string `json:"-"`
	Name         string `json:"-"`
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
	Column       string `json:"column"`
	Visible      bool   `json:"visible"`
	Pinned       bool   `json:"pinned,omitempty"`
	CellRenderer string `json:"cellRenderer,omitempty"`
	EmissionID   string `json:"-"`
}

type ExplorerRuntimeBindingV1 struct {
	Column     string `json:"column"`
	EmissionID string `json:"-"`
	OutputID   string `json:"outputId,omitempty"`
	Label      string `json:"label,omitempty"`
	Type       string `json:"type,omitempty"`
	Title      string `json:"title,omitempty"`
}
