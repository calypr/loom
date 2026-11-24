package workspace

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/capability"
)

const (
	APIVersion              = "loom.calypr.org/explorer-authoring/v2"
	Kind                    = "ExplorerBuilderDocument"
	WorkspaceKind           = "ExplorerBuilderWorkspace"
	StateKind               = "ExplorerBuilderState"
	CatalogKind             = "ExplorerBuilderCatalog"
	RootOccurrenceID        = "base"
	CurrentSemanticsVersion = 12

	contributorSemanticsVersion             = 4
	aggregateTemporalPolicySemanticsVersion = 9
	tableShapeSemanticsVersion              = 9
)

type Document struct {
	APIVersion          string            `json:"-"`
	Kind                string            `json:"kind"`
	Output              Output            `json:"output"`
	RootResourceType    string            `json:"rootResourceType,omitempty"`
	Route               RouteNode         `json:"route,omitempty"`
	Rows                RowDefinition     `json:"rows"`
	Population          *Population       `json:"population,omitempty"`
	Frames              []FrameDefinition `json:"frames,omitempty"`
	Columns             []Column          `json:"columns"`
	ArchiveRetiredShape *storedTableShape `json:"tableShape,omitempty"`

	Construction   *Construction       `json:"construction,omitempty"`
	FixedFilters   []FixedFilter       `json:"fixedFilters,omitempty"`
	Actions        []Action            `json:"actions,omitempty"`
	ImportedRecipe *ImportedRecipeRef  `json:"importedRecipe,omitempty"`
	ImportedSource *ImportedSourcePlan `json:"importedSource,omitempty"`
}

type ImportedRecipeRef struct {
	Name               string `json:"name"`
	TranslationVersion string `json:"translationVersion"`
	Digest             string `json:"digest"`
	Output             string `json:"output"`
}

func (r ImportedRecipeRef) Validate() error {
	if !requiredID(r.Name) || !requiredID(r.TranslationVersion) || !requiredID(r.Output) {
		return fmt.Errorf("imported recipe name, translationVersion, and output are required and must be trimmed")
	}
	if len(r.Digest) != 64 {
		return fmt.Errorf("imported recipe digest must be a lowercase SHA-256 digest")
	}
	decoded, err := hex.DecodeString(r.Digest)
	if err != nil || len(decoded) != 32 || strings.ToLower(r.Digest) != r.Digest {
		return fmt.Errorf("imported recipe digest must be a lowercase SHA-256 digest")
	}
	return nil
}

type Population struct {
	SelectionRevisionID string                `json:"selectionRevisionId"`
	Route               []PopulationRouteStep `json:"route"`
}

type PopulationRouteStep struct {
	ResourceType     string `json:"resourceType"`
	Relationship     string `json:"relationship"`
	CatalogEdgeID    string `json:"catalogEdgeId,omitempty"`
	StorageDirection string `json:"storageDirection,omitempty"`
}

func (p Population) Validate() error {
	if strings.TrimSpace(p.SelectionRevisionID) == "" {
		return fmt.Errorf("population.selectionRevisionId is required")
	}
	for i, step := range p.Route {
		if strings.TrimSpace(step.ResourceType) == "" || strings.TrimSpace(step.Relationship) == "" {
			return fmt.Errorf("population.route[%d] requires resourceType and relationship", i)
		}
		if step.StorageDirection != "" && step.StorageDirection != "INBOUND" && step.StorageDirection != "OUTBOUND" {
			return fmt.Errorf("population.route[%d] has an unsupported storageDirection", i)
		}
	}
	return nil
}

type Output struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	RowLabel string `json:"rowLabel,omitempty"`
}

type Workspace struct {
	APIVersion       string `json:"apiVersion"`
	Kind             string `json:"kind"`
	SemanticsVersion int    `json:"semanticsVersion,omitempty"`

	MigrationDecisions []string                         `json:"migrationDecisions,omitempty"`
	Explorer           ExplorerMetadata                 `json:"explorer"`
	Documents          []Document                       `json:"documents"`
	Tabs               []Tab                            `json:"tabs"`
	SharedFilters      map[string][]SharedFilterBinding `json:"sharedFilters,omitempty"`
	FileActions        *FileActions                     `json:"fileActions,omitempty"`
}

type Tab struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	OutputID string `json:"outputId"`
	Order    int    `json:"order"`
	Visible  bool   `json:"visible"`
}

type TablePresentation struct {
	Visible      *bool  `json:"visible,omitempty"`
	Order        *int   `json:"order,omitempty"`
	Pinned       bool   `json:"pinned,omitempty"`
	CellRenderer string `json:"cellRenderer,omitempty"`
}
type FilterPresentation struct {
	Label string `json:"label,omitempty"`
	Order *int   `json:"order,omitempty"`
}
type ChartPresentation struct {
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
	Order *int   `json:"order,omitempty"`
}

type CatalogSnapshot struct {
	APIVersion               string               `json:"-"`
	Kind                     string               `json:"-"`
	Project                  string               `json:"-"`
	ExplorerID               string               `json:"-"`
	SourceGeneration         string               `json:"generation"`
	AuthorizationScopeDigest string               `json:"authorizationScopeDigest"`
	ResolvedSchemaDigest     string               `json:"resolvedSchemaDigest,omitempty"`
	SnapshotToken            string               `json:"snapshotToken"`
	Complete                 bool                 `json:"complete"`
	Truncated                bool                 `json:"-"`
	Diagnostics              []CatalogDiagnostic  `json:"-"`
	Availability             *CatalogAvailability `json:"availability,omitempty"`
	Nodes                    []CatalogNode        `json:"nodes"`
	Edges                    []CatalogEdge        `json:"edges"`
	Candidates               []CatalogCandidate   `json:"candidates"`
	RoutePolicy              RoutePolicy          `json:"routePolicy"`
}

type CatalogDiagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type CatalogNode struct {
	ID              string `json:"nodeId"`
	ResourceType    string `json:"resourceType"`
	RowRootEligible bool   `json:"rowRootEligible"`
	RowGrain        string `json:"rowGrain,omitempty"`
	Populated       bool   `json:"populated"`
	DocumentCount   *int64 `json:"documentCount,omitempty"`
}

type CatalogEdge struct {
	ID               string `json:"edgeId"`
	FromNodeID       string `json:"fromNodeId"`
	ToNodeID         string `json:"toNodeId"`
	Label            string `json:"label"`
	StorageDirection string `json:"storageDirection,omitempty"`
	Populated        bool   `json:"populated"`
}

type CatalogCandidate struct {
	ID                    string                                    `json:"candidateId"`
	NodeID                string                                    `json:"nodeId"`
	FieldPath             string                                    `json:"fieldPath"`
	Label                 string                                    `json:"label"`
	LogicalType           string                                    `json:"logicalType"`
	Cardinality           string                                    `json:"cardinality"`
	Repeated              bool                                      `json:"repeated"`
	Filterable            bool                                      `json:"filterable"`
	Chartable             bool                                      `json:"chartable"`
	ProjectionModes       []string                                  `json:"projectionModes"`
	DefaultProjectionMode string                                    `json:"defaultProjectionMode"`
	RepeatedBoundaries    []RepeatedBoundary                        `json:"repeatedBoundaries,omitempty"`
	ConstructionChoice    *capability.ConstructionChoice            `json:"constructionChoice"`
	FilterOperators       []string                                  `json:"-"`
	ChartOperations       []string                                  `json:"-"`
	AggregateOperations   []capability.AggregateOperationCapability `json:"aggregateOperations"`
	Transformations       AggregateTransformationCapabilities       `json:"transformations"`
	ValueTransformations  ColumnValueTransformationCapabilities     `json:"valueTransformations"`
	Populated             bool                                      `json:"populated"`
	Count                 *int64                                    `json:"-"`
	SuggestionsAvailable  bool                                      `json:"-"`
	SuggestionsComplete   bool                                      `json:"-"`
	SuggestionsTruncated  bool                                      `json:"-"`
	SuggestionCount       int                                       `json:"-"`
	ConceptCandidates     []capability.ConceptCandidate             `json:"conceptCandidates,omitempty"`
}

type RepeatedBoundary struct {
	Path     string `json:"path"`
	MaxItems int    `json:"maxItems"`
}

type RoutePolicy struct {
	MaxHops            *int `json:"maxSteps"`
	Unbounded          bool `json:"-"`
	AllowRepeatedEdges bool `json:"allowRepeatedEdges"`
	AllowSelfLoops     bool `json:"allowSelfLoops"`
}

type BuilderState struct {
	APIVersion              string          `json:"apiVersion"`
	Kind                    string          `json:"kind"`
	LifecycleState          string          `json:"lifecycleState"`
	DraftVersion            int64           `json:"draftVersion"`
	DraftDigest             string          `json:"draftDigest"`
	PreviousDraftRevisionID string          `json:"previousDraftRevisionId,omitempty"`
	Workspace               *Workspace      `json:"workspace"`
	Catalog                 CatalogSnapshot `json:"catalog"`
}
