// Package authoringv2 contains the hard-cut Explorer Builder authoring
// protocol. It is deliberately separate from the V1 HTTP and persistence
// contracts: this package describes user intent and catalog facts only.
package authoringv2

const (
	APIVersion       = "loom.calypr.org/explorer-authoring/v2"
	Kind             = "ExplorerBuilderDocument"
	WorkspaceKind    = "ExplorerBuilderWorkspace"
	StateKind        = "ExplorerBuilderState"
	CatalogKind      = "ExplorerBuilderCatalog"
	RootOccurrenceID = "base"
)

// Document is the complete durable Builder intent. Route occurrences form a
// semantic resource tree rooted at RootResourceType.
type Document struct {
	APIVersion       string        `json:"-"`
	Kind             string        `json:"kind"`
	Output           Output        `json:"output"`
	RootResourceType string        `json:"rootResourceType,omitempty"`
	Route            RouteNode     `json:"route,omitempty"`
	Columns          []Column      `json:"columns"`
	FixedFilters     []FixedFilter `json:"fixedFilters,omitempty"`
	Actions          []Action      `json:"actions,omitempty"`
}

type Output struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	RowLabel string `json:"rowLabel,omitempty"`
}

// Workspace is the atomic authoring unit. Documents are independent table
// intents; tabs provide their ordered, visible runtime presentation.
type Workspace struct {
	APIVersion    string                           `json:"apiVersion"`
	Kind          string                           `json:"kind"`
	Explorer      ExplorerMetadata                 `json:"explorer"`
	Documents     []Document                       `json:"documents"`
	Tabs          []Tab                            `json:"tabs"`
	SharedFilters map[string][]SharedFilterBinding `json:"sharedFilters,omitempty"`
	FileActions   *FileActions                     `json:"fileActions,omitempty"`
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

// CatalogSnapshot is an immutable, authorization-scoped projection. The
// token identifies the exact snapshot used to validate a document.
type CatalogSnapshot struct {
	APIVersion               string              `json:"-"`
	Kind                     string              `json:"-"`
	Project                  string              `json:"-"`
	ExplorerID               string              `json:"-"`
	SourceGeneration         string              `json:"generation"`
	AuthorizationScopeDigest string              `json:"authorizationScopeDigest"`
	ResolvedSchemaDigest     string              `json:"resolvedSchemaDigest,omitempty"`
	SnapshotToken            string              `json:"snapshotToken"`
	Complete                 bool                `json:"complete"`
	Truncated                bool                `json:"-"`
	Diagnostics              []CatalogDiagnostic `json:"-"`
	Nodes                    []CatalogNode       `json:"nodes"`
	Edges                    []CatalogEdge       `json:"edges"`
	Candidates               []CatalogCandidate  `json:"candidates"`
	RoutePolicy              RoutePolicy         `json:"routePolicy"`
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
	ID         string `json:"edgeId"`
	FromNodeID string `json:"fromNodeId"`
	ToNodeID   string `json:"toNodeId"`
	Label      string `json:"label"`
	Populated  bool   `json:"populated"`
}

type CatalogCandidate struct {
	ID                    string   `json:"candidateId"`
	NodeID                string   `json:"nodeId"`
	FieldPath             string   `json:"fieldPath"`
	Label                 string   `json:"label"`
	LogicalType           string   `json:"logicalType"`
	Repeated              bool     `json:"repeated"`
	Filterable            bool     `json:"filterable"`
	Chartable             bool     `json:"chartable"`
	ProjectionModes       []string `json:"projectionModes"`
	DefaultProjectionMode string   `json:"defaultProjectionMode"`
	FilterOperators       []string `json:"-"`
	ChartOperations       []string `json:"-"`
	Cardinality           string   `json:"-"`
	Populated             bool     `json:"-"`
	Count                 *int64   `json:"-"`
	SuggestionsAvailable  bool     `json:"-"`
	SuggestionsComplete   bool     `json:"-"`
	SuggestionsTruncated  bool     `json:"-"`
	SuggestionCount       int      `json:"-"`
}

// RoutePolicy has no default hop ceiling: nil MaxHops means every finite
// route is valid. Repeated edges and self-loops are explicit capabilities.
type RoutePolicy struct {
	MaxHops            *int `json:"maxSteps"`
	Unbounded          bool `json:"-"`
	AllowRepeatedEdges bool `json:"allowRepeatedEdges"`
	AllowSelfLoops     bool `json:"allowSelfLoops"`
}

// BuilderState joins one workspace to the one catalog snapshot that proves it.
type BuilderState struct {
	APIVersion     string          `json:"apiVersion"`
	Kind           string          `json:"kind"`
	LifecycleState string          `json:"lifecycleState"`
	DraftVersion   int64           `json:"draftVersion"`
	DraftDigest    string          `json:"draftDigest"`
	Workspace      *Workspace      `json:"workspace"`
	Catalog        CatalogSnapshot `json:"catalog"`
}
