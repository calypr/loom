package explorer

import (
	"encoding/json"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
)

// RepositoryConfig is the V2 repository-owned Explorer contract frozen at
// successful deployment. It is intentionally separate from the retired V1
// authoring aggregate: the repository packet carries the executable baseline;
// live schema/readiness and publication metadata are stored alongside it.
type RepositoryConfig struct {
	Project              string              `json:"project"`
	ExplorerID           string              `json:"explorerId"`
	Management           ManagementMode      `json:"management"`
	Config               json.RawMessage     `json:"config"`
	Workspace            json.RawMessage     `json:"workspace"`
	ConfigDigest         string              `json:"configDigest,omitempty"`
	IntentDigest         string              `json:"intentDigest,omitempty"`
	CompilationReceiptID string              `json:"compilationReceiptId,omitempty"`
	PublicOutputContract json.RawMessage     `json:"publicOutputContract,omitempty"`
	SourceGeneration     string              `json:"sourceGeneration"`
	SourceCommit         string              `json:"sourceCommit"`
	ExecutionID          string              `json:"executionId"`
	ActiveRevisionID     string              `json:"activeRevisionId,omitempty"`
	DraftVersion         int64               `json:"-"`
	Materializations     []Materialization   `json:"materializations"`
	Dataset              DatasetMetadata     `json:"dataset"`
	Publication          PublicationMetadata `json:"publication"`
	Diagnostics          []Diagnostic        `json:"diagnostics,omitempty"`
	UpdatedAt            time.Time           `json:"updatedAt"`
}

type ManagementMode string

const (
	ManagementRepository  ManagementMode = "REPOSITORY"
	ManagementInteractive ManagementMode = "INTERACTIVE"
)

type RevisionStatus string

const (
	RevisionPending       RevisionStatus = "PENDING"
	RevisionMaterializing RevisionStatus = "MATERIALIZING"
	RevisionReady         RevisionStatus = "READY"
	RevisionActive        RevisionStatus = "ACTIVE"
	RevisionSuperseded    RevisionStatus = "SUPERSEDED"
	RevisionFailed        RevisionStatus = "FAILED"
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
	DraftConfig          json.RawMessage     `json:"draftConfig,omitempty"`
	DraftVersion         int64               `json:"draftVersion"`
	DraftDigest          string              `json:"draftDigest,omitempty"`
	ActiveRevisionID     string              `json:"activeRevisionId,omitempty"`
	ActiveConfig         json.RawMessage     `json:"activeConfig,omitempty"`
	RecipeDigest         string              `json:"recipeDigest,omitempty"`
	ResolvedSchemaDigest string              `json:"resolvedSchemaDigest,omitempty"`
	SourceGeneration     string              `json:"sourceGeneration,omitempty"`
	EmittedColumns       []EmittedColumn     `json:"emittedColumns,omitempty"`
	Materializations     []Materialization   `json:"materializations,omitempty"`
	Dataset              DatasetMetadata     `json:"dataset,omitempty"`
	Publication          PublicationMetadata `json:"publication,omitempty"`
	Diagnostics          []Diagnostic        `json:"diagnostics,omitempty"`
	UpdatedBy            string              `json:"updatedBy,omitempty"`
	UpdatedAt            time.Time           `json:"updatedAt"`
}
type Materialization struct {
	OutputID          string                       `json:"outputId"`
	Output            string                       `json:"output"`
	MaterializationID string                       `json:"materializationId"`
	Fingerprint       string                       `json:"fingerprint,omitempty"`
	Selector          *dataset.DataframeSelector   `json:"selector,omitempty"`
	Columns           []publication.PhysicalColumn `json:"columns"`
}

// WithDataframeSelectors clones canonical publication metadata while
// preserving the selectors that identify the active dataset release. Missing
// selectors are invalid and are not synthesized.
func WithDataframeSelectors(_ recipe.Bundle, materializations []Materialization, metadata DatasetMetadata) ([]Materialization, DatasetMetadata) {
	materializations = append([]Materialization(nil), materializations...)
	metadata.Outputs = append([]DatasetOutput(nil), metadata.Outputs...)
	return materializations, metadata
}

type Revision struct {
	ID         string `json:"id"`
	Project    string `json:"project"`
	ExplorerID string `json:"explorerId"`
	// Config is the exact immutable ExplorerConfigV2 packet for this revision.
	Config               json.RawMessage     `json:"config,omitempty"`
	ConfigDigest         string              `json:"configDigest,omitempty"`
	AuthoringBundle      json.RawMessage     `json:"authoringBundle,omitempty"`
	IntentDigest         string              `json:"intentDigest,omitempty"`
	CompilationReceiptID string              `json:"compilationReceiptId,omitempty"`
	PublicOutputContract json.RawMessage     `json:"publicOutputContract,omitempty"`
	Recipe               recipe.Bundle       `json:"canonicalRecipe"`
	RecipeDigest         string              `json:"recipeDigest"`
	ResolvedSchemaDigest string              `json:"resolvedSchemaDigest"`
	SourceGeneration     string              `json:"sourceGeneration"`
	SourceCommit         string              `json:"sourceCommit,omitempty"`
	Materializations     []Materialization   `json:"materializations"`
	EmittedColumns       []EmittedColumn     `json:"emittedColumns"`
	Dataset              DatasetMetadata     `json:"dataset,omitempty"`
	Publication          PublicationMetadata `json:"publication,omitempty"`
	Diagnostics          []Diagnostic        `json:"diagnostics"`
	// Migration records the lossless source packet used to create this
	// revision.  The authoring bundle is the executable Builder intent, while
	// these raw packets provide an auditable rollback path for migrations from
	// frontend/repository configuration formats (including fields Loom does not
	// yet interpret).
	Migration   *MigrationMetadata `json:"migration,omitempty"`
	Status      RevisionStatus     `json:"status"`
	CreatedBy   string             `json:"createdBy,omitempty"`
	CreatedAt   time.Time          `json:"createdAt"`
	ReadyAt     *time.Time         `json:"readyAt,omitempty"`
	ActivatedAt *time.Time         `json:"activatedAt,omitempty"`
	FailedAt    *time.Time         `json:"failedAt,omitempty"`
}

// MigrationMetadata is intentionally attached to the immutable revision.  A
// migration must never discard the source document merely because its known
// fields were translated into authoring-v1.  Raw JSON is retained so unknown
// or frontend-specific fields remain available for rollback and future
// translators without leaking them into the executable recipe contract.
type MigrationMetadata struct {
	Kind                  string          `json:"kind"`
	Source                string          `json:"source"`
	SourceProject         string          `json:"sourceProject"`
	SourceExplorerID      string          `json:"sourceExplorerId"`
	OriginalConfig        json.RawMessage `json:"originalConfig,omitempty"`
	OriginalMapping       json.RawMessage `json:"originalMapping,omitempty"`
	OriginalConfigDigest  string          `json:"originalConfigDigest,omitempty"`
	OriginalMappingDigest string          `json:"originalMappingDigest,omitempty"`
	Actor                 string          `json:"actor,omitempty"`
	RequestID             string          `json:"requestId,omitempty"`
	MigratedAt            time.Time       `json:"migratedAt"`
}
