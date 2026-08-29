// Package authoringv2 contains the hard-cut Explorer Builder authoring
// protocol. It is deliberately separate from the V1 HTTP and persistence
// contracts: this package describes user intent and catalog facts only.
package authoringv2

import (
	"net/url"
	"strings"
)

const (
	APIVersion       = "loom.calypr.org/explorer-authoring/v2"
	Kind             = "ExplorerBuilderDocument"
	WorkspaceKind    = "ExplorerBuilderWorkspace"
	StateKind        = "ExplorerBuilderState"
	CatalogKind      = "ExplorerBuilderCatalog"
	RootOccurrenceID = "base"
)

// Document is the complete durable Builder intent. A route is a finite edge
// path from RootNodeID. Its root occurrence is always "base" and its tail is
// computed from the path; neither is a second authored node identity.
type Document struct {
	APIVersion       string        `json:"-"`
	Kind             string        `json:"kind"`
	Output           Output        `json:"output"`
	RootResourceType string        `json:"rootResourceType,omitempty"`
	Route            RouteNode     `json:"route,omitempty"`
	Columns          []Column      `json:"columns"`
	FixedFilters     []FixedFilter `json:"fixedFilters,omitempty"`
	Actions          []Action      `json:"actions,omitempty"`

	// The opaque, linear authoring model is retained only for source-compatible
	// internal tests while the hard-cut wire decoder rejects those fields.
	RootNodeID   string                  `json:"-"`
	RouteSteps   []RouteStep             `json:"-"`
	Selections   []Selection             `json:"-"`
	Presentation map[string]Presentation `json:"-"`
}

type Output struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	RowLabel string `json:"rowLabel,omitempty"`
}

type RouteStep struct {
	EdgeID       string `json:"edgeId"`
	OccurrenceID string `json:"occurrenceId,omitempty"`
}

type Selection struct {
	CandidateID    string `json:"candidateId"`
	OccurrenceID   string `json:"occurrenceId,omitempty"`
	ProjectionMode string `json:"projectionMode"`
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

// Presentation contains display intent only. In particular, it has no
// selector, expression, physical collection, or generated-column fields.
type Presentation struct {
	Label   string              `json:"label,omitempty"`
	Visible *bool               `json:"visible,omitempty"`
	Order   *int                `json:"order,omitempty"`
	Table   *TablePresentation  `json:"table,omitempty"`
	Filter  *FilterPresentation `json:"filter,omitempty"`
	Chart   *ChartPresentation  `json:"chart,omitempty"`
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

func PresentationKey(candidateID, occurrenceID, projectionMode string) string {
	encode := func(value string) string { return strings.ReplaceAll(url.QueryEscape(value), "+", "%20") }
	return encode(candidateID) + "::" + encode(occurrenceID) + "::" + encode(projectionMode)
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
	APIVersion     string     `json:"apiVersion"`
	Kind           string     `json:"kind"`
	LifecycleState string     `json:"lifecycleState"`
	DraftVersion   int64      `json:"draftVersion"`
	DraftDigest    string     `json:"draftDigest"`
	Workspace      *Workspace `json:"workspace"`
	// Document is retained only as a source-compatible internal migration aid.
	Document *Document       `json:"-"`
	Catalog  CatalogSnapshot `json:"catalog"`
}

// RouteOccurrence is derived, never wire-authored. The first occurrence has
// ID "base". Unnamed route steps receive stable step-N IDs; TailOccurrence
// returns the final derived occurrence (base for an empty route).
type RouteOccurrence struct {
	ID             string
	NodeID         string
	IncomingEdgeID string
}
