package explorer

import (
	"encoding/json"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type RevisionStatus string

const (
	RevisionReady      RevisionStatus = "READY"
	RevisionActive     RevisionStatus = "ACTIVE"
	RevisionSuperseded RevisionStatus = "SUPERSEDED"
	RevisionFailed     RevisionStatus = "FAILED"
)

type Explorer struct {
	Project        string         `json:"project"`
	ExplorerID     string         `json:"explorerId"`
	Title          string         `json:"title"`
	ManagementMode ManagementMode `json:"managementMode"`
	ProjectConfig  *ProjectConfig `json:"projectConfig,omitempty"`

	DraftConfig                   json.RawMessage `json:"draftConfig,omitempty"`
	DraftVersion                  int64           `json:"draftVersion"`
	DraftDigest                   string          `json:"draftDigest,omitempty"`
	DraftSnapshotToken            string          `json:"draftSnapshotToken,omitempty"`
	DraftSourceGeneration         string          `json:"draftSourceGeneration,omitempty"`
	DraftAuthorizationScopeDigest string          `json:"draftAuthorizationScopeDigest,omitempty"`
	PreviousDraftRevisionID       string          `json:"previousDraftRevisionId,omitempty"`
	LastAuthoringCommandID        string          `json:"lastAuthoringCommandId,omitempty"`
	LastAuthoringCommandDigest    string          `json:"lastAuthoringCommandDigest,omitempty"`
	LastAuthoringCommandResults   json.RawMessage `json:"lastAuthoringCommandResults,omitempty"`
	ActiveRevisionID              string          `json:"activeRevisionId,omitempty"`
	UpdatedBy                     string          `json:"updatedBy,omitempty"`
	UpdatedAt                     time.Time       `json:"updatedAt"`
}

type DraftRevision struct {
	ID                       string          `json:"id"`
	Project                  string          `json:"project"`
	ExplorerID               string          `json:"explorerId"`
	DraftVersion             int64           `json:"draftVersion"`
	DraftDigest              string          `json:"draftDigest,omitempty"`
	DraftConfig              json.RawMessage `json:"draftConfig,omitempty"`
	Title                    string          `json:"title"`
	SnapshotToken            string          `json:"snapshotToken,omitempty"`
	SourceGeneration         string          `json:"sourceGeneration,omitempty"`
	AuthorizationScopeDigest string          `json:"authorizationScopeDigest,omitempty"`
	UpdatedBy                string          `json:"updatedBy,omitempty"`
	UpdatedAt                time.Time       `json:"updatedAt"`
}

type Revision struct {
	ID         string `json:"id"`
	Project    string `json:"project"`
	ExplorerID string `json:"explorerId"`

	Config               json.RawMessage             `json:"config,omitempty"`
	AuthoringBundle      json.RawMessage             `json:"authoringBundle,omitempty"`
	IntentDigest         string                      `json:"intentDigest,omitempty"`
	CompilationReceiptID string                      `json:"compilationReceiptId,omitempty"`
	PublicOutputContract json.RawMessage             `json:"publicOutputContract,omitempty"`
	Recipe               recipe.Bundle               `json:"canonicalRecipe"`
	RecipeDigest         string                      `json:"recipeDigest"`
	ResolvedSchemaDigest string                      `json:"resolvedSchemaDigest"`
	SourceGeneration     string                      `json:"sourceGeneration"`
	Materializations     []Materialization           `json:"materializations"`
	EmittedColumns       []EmittedColumn             `json:"emittedColumns"`
	Dataset              DatasetMetadata             `json:"dataset,omitempty"`
	QualityReports       []publication.QualityReport `json:"qualityReports,omitempty"`
	Publication          PublicationMetadata         `json:"publication,omitempty"`
	Diagnostics          []Diagnostic                `json:"diagnostics"`
	Status               RevisionStatus              `json:"status"`
	CreatedBy            string                      `json:"createdBy,omitempty"`
	CreatedAt            time.Time                   `json:"createdAt"`
	ReadyAt              *time.Time                  `json:"readyAt,omitempty"`
	ActivatedAt          *time.Time                  `json:"activatedAt,omitempty"`
	FailedAt             *time.Time                  `json:"failedAt,omitempty"`
}
