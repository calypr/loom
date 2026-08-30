package explorer

import (
	"encoding/json"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
)

type ManagementMode string

const (
	ManagementRepository  ManagementMode = "REPOSITORY"
	ManagementInteractive ManagementMode = "INTERACTIVE"
)

type RevisionStatus string

const (
	RevisionReady      RevisionStatus = "READY"
	RevisionActive     RevisionStatus = "ACTIVE"
	RevisionSuperseded RevisionStatus = "SUPERSEDED"
	RevisionFailed     RevisionStatus = "FAILED"
)

type Diagnostic struct {
	Severity  string         `json:"severity"`
	Stage     string         `json:"stage,omitempty"`
	Code      string         `json:"code"`
	FieldPath string         `json:"fieldPath,omitempty"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	Retryable bool           `json:"retryable,omitempty"`
	RequestID string         `json:"requestId,omitempty"`
}

// DatasetMetadata is the live, server-derived shape of a published recipe.
// It is deliberately separate from ConfigV2: physical columns and readiness
// are properties of a materialized generation, not presentation authored by a
// repository or Builder user.
type DatasetMetadata struct {
	Generation   string          `json:"generation,omitempty"`
	SchemaDigest string          `json:"schemaDigest,omitempty"`
	Outputs      []DatasetOutput `json:"outputs"`
}

type DatasetOutput struct {
	Name        string                       `json:"name"`
	State       string                       `json:"state"`
	Queryable   bool                         `json:"queryable"`
	Fingerprint string                       `json:"fingerprint,omitempty"`
	Selector    *dataset.DataframeSelector   `json:"selector,omitempty"`
	Columns     []publication.PhysicalColumn `json:"columns,omitempty"`
}

type PublicationMetadata struct {
	State       string    `json:"state"`
	Generation  string    `json:"generation,omitempty"`
	ExecutionID string    `json:"executionId,omitempty"`
	RevisionID  string    `json:"revisionId,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
}

type Explorer struct {
	Project        string         `json:"project"`
	ExplorerID     string         `json:"explorerId"`
	Title          string         `json:"title"`
	ManagementMode ManagementMode `json:"managementMode"`
	// DraftConfig is the lossless, canonical ExplorerConfigV2 packet used by
	// the Builder.
	DraftConfig                 json.RawMessage `json:"draftConfig,omitempty"`
	DraftVersion                int64           `json:"draftVersion"`
	DraftDigest                 string          `json:"draftDigest,omitempty"`
	LastAuthoringCommandID      string          `json:"lastAuthoringCommandId,omitempty"`
	LastAuthoringCommandDigest  string          `json:"lastAuthoringCommandDigest,omitempty"`
	LastAuthoringCommandResults json.RawMessage `json:"lastAuthoringCommandResults,omitempty"`
	ActiveRevisionID            string          `json:"activeRevisionId,omitempty"`
	UpdatedBy                   string          `json:"updatedBy,omitempty"`
	UpdatedAt                   time.Time       `json:"updatedAt"`
}
type Materialization struct {
	OutputID          string                       `json:"outputId"`
	Output            string                       `json:"output"`
	MaterializationID string                       `json:"materializationId"`
	Fingerprint       string                       `json:"fingerprint,omitempty"`
	Selector          *dataset.DataframeSelector   `json:"selector,omitempty"`
	Columns           []publication.PhysicalColumn `json:"columns"`
}

type Revision struct {
	ID         string `json:"id"`
	Project    string `json:"project"`
	ExplorerID string `json:"explorerId"`
	// Config is the exact immutable ExplorerConfigV2 packet for this revision.
	Config               json.RawMessage     `json:"config,omitempty"`
	AuthoringBundle      json.RawMessage     `json:"authoringBundle,omitempty"`
	IntentDigest         string              `json:"intentDigest,omitempty"`
	CompilationReceiptID string              `json:"compilationReceiptId,omitempty"`
	PublicOutputContract json.RawMessage     `json:"publicOutputContract,omitempty"`
	Recipe               recipe.Bundle       `json:"canonicalRecipe"`
	RecipeDigest         string              `json:"recipeDigest"`
	ResolvedSchemaDigest string              `json:"resolvedSchemaDigest"`
	SourceGeneration     string              `json:"sourceGeneration"`
	Materializations     []Materialization   `json:"materializations"`
	EmittedColumns       []EmittedColumn     `json:"emittedColumns"`
	Dataset              DatasetMetadata     `json:"dataset,omitempty"`
	Publication          PublicationMetadata `json:"publication,omitempty"`
	Diagnostics          []Diagnostic        `json:"diagnostics"`
	Status               RevisionStatus      `json:"status"`
	CreatedBy            string              `json:"createdBy,omitempty"`
	CreatedAt            time.Time           `json:"createdAt"`
	ReadyAt              *time.Time          `json:"readyAt,omitempty"`
	ActivatedAt          *time.Time          `json:"activatedAt,omitempty"`
	FailedAt             *time.Time          `json:"failedAt,omitempty"`
}
