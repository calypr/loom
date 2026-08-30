// Package compilation translates semantic Builder intent into the persistence-
// neutral recipe understood by the dataframe compiler. It deliberately has no
// HTTP, store, or server dependencies: a capability snapshot is its authority.
package compilation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

const TranslationVersion = "authoring-v2-native-5"

// Error is a structured translation failure. Stage, Code, Path, and Details
// remain transport-neutral so adapters never parse error strings.
type Error struct {
	Stage   string
	Code    string
	Path    string
	Message string
	Details map[string]any
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "compilation error"
	}
	if e.Path == "" {
		return e.Code + ": " + e.Message
	}
	return e.Code + " at " + e.Path + ": " + e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func fail(stage, code, path, message string, details map[string]any, cause error) error {
	return &Error{Stage: stage, Code: code, Path: path, Message: message, Details: details, Cause: cause}
}

// PresentationConfig is compiler-owned presentation data. The Builder may
// provide labels, visibility, and order, but never executable selectors.
type PresentationConfig struct {
	OutputID string               `json:"outputId"`
	Title    string               `json:"title"`
	Columns  []PresentationColumn `json:"columns"`
}

type PresentationColumn struct {
	EmissionID   string `json:"emissionId"`
	PublicColumn string `json:"publicColumn"`
	Label        string `json:"label"`
	Visible      bool   `json:"visible"`
	Order        int    `json:"order"`
	Pinned       bool   `json:"pinned"`
	FilterLabel  string `json:"filterLabel,omitempty"`
	FilterOrder  int    `json:"filterOrder,omitempty"`
	ChartType    string `json:"chartType,omitempty"`
	ChartTitle   string `json:"chartTitle,omitempty"`
	ChartOrder   int    `json:"chartOrder,omitempty"`
}

// Result is one deterministic semantic document compilation.
type Result struct {
	Bundle           recipe.Bundle
	RecipeDigest     string
	EmittedColumns   []explorer.EmittedColumn
	IdentityMappings []explorer.IdentityMapping
	Presentation     PresentationConfig
	OutputContract   explorer.PublicOutputContract
}

// WorkspaceResult combines independently compiled table documents into one
// recipe and one public Explorer configuration.
type WorkspaceResult struct {
	Bundle           recipe.Bundle
	RecipeDigest     string
	EmittedColumns   []explorer.EmittedColumn
	IdentityMappings []explorer.IdentityMapping
	Presentations    []PresentationConfig
	OutputContracts  []explorer.PublicOutputContract
	Workspace        authoringv2.Workspace
}

func CompileWorkspace(ctx context.Context, project, explorerID string, workspace authoringv2.Workspace, snapshot capability.Snapshot) (WorkspaceResult, error) {
	wire := catalogFromCapability(snapshot, explorerID)
	if err := (authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Workspace: &workspace, Catalog: wire}).Validate(); err != nil {
		return WorkspaceResult{}, fail("intent", "INVALID_AUTHORING_INTENT", "$.workspace", err.Error(), nil, err)
	}
	workspace = workspace.NormalizePresentationOrders()
	result := WorkspaceResult{
		Workspace: workspace,
		Bundle: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                "explorer_" + safeName(project) + "_" + safeName(explorerID),
			TranslationVersion:  TranslationVersion,
		},
		EmittedColumns:   []explorer.EmittedColumn{},
		IdentityMappings: []explorer.IdentityMapping{},
		Presentations:    []PresentationConfig{},
		OutputContracts:  []explorer.PublicOutputContract{},
	}
	for i, document := range workspace.Documents {
		compiled, err := Compile(ctx, project, explorerID, document, snapshot)
		if err != nil {
			return WorkspaceResult{}, fail("compile", "DOCUMENT_COMPILE_FAILED", fmt.Sprintf("$.workspace.documents[%d]", i), err.Error(), nil, err)
		}
		result.Bundle.Outputs = append(result.Bundle.Outputs, compiled.Bundle.Outputs...)
		result.EmittedColumns = append(result.EmittedColumns, compiled.EmittedColumns...)
		result.IdentityMappings = append(result.IdentityMappings, compiled.IdentityMappings...)
		result.Presentations = append(result.Presentations, compiled.Presentation)
		result.OutputContracts = append(result.OutputContracts, compiled.OutputContract)
	}
	if err := result.Bundle.Validate(); err != nil {
		return WorkspaceResult{}, err
	}
	digest, err := result.Bundle.Digest()
	if err != nil {
		return WorkspaceResult{}, err
	}
	result.RecipeDigest = digest
	return result, nil
}

// Compile translates one semantic V2 document against the exact capability
// snapshot. It performs no discovery, profiling, or recipe execution.
func Compile(ctx context.Context, project, explorerID string, document authoringv2.Document, snapshot capability.Snapshot) (Result, error) {
	if err := contextErr(ctx); err != nil {
		return Result{}, err
	}
	project = strings.TrimSpace(project)
	explorerID = strings.TrimSpace(explorerID)
	if project == "" {
		return Result{}, fail("protocol", "PROJECT_REQUIRED", "$.project", "project is required", nil, nil)
	}
	if explorerID == "" {
		return Result{}, fail("protocol", "EXPLORER_REQUIRED", "$.explorerId", "explorerId is required", nil, nil)
	}
	if err := snapshot.ValidateToken(snapshot.Token); err != nil {
		return Result{}, fail("capability", "CAPABILITY_SNAPSHOT_UNUSABLE", "$.snapshot", "capability snapshot is unavailable or stale", map[string]any{"token": snapshot.Token}, err)
	}
	if snapshot.Identity.Project != project {
		return Result{}, fail("capability", "PROJECT_MISMATCH", "$.project", "capability snapshot belongs to a different project", map[string]any{"snapshotProject": snapshot.Identity.Project, "project": project}, nil)
	}
	if err := document.Validate(); err != nil {
		return Result{}, fail("intent", "INVALID_DOCUMENT", "$", err.Error(), nil, err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Result{}, err
	}
	return compileSemanticDocument(ctx, project, explorerID, document, snapshot)
}

func catalogFromCapability(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
	catalog := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind,
		Project: snapshot.Identity.Project, ExplorerID: explorerID,
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		ResolvedSchemaDigest: snapshot.Identity.SchemaDigest, SnapshotToken: snapshot.Token, Complete: snapshot.Complete,
		RoutePolicy: authoringv2.RoutePolicy{Unbounded: snapshot.Policy.Route.MaxHops == 0, AllowRepeatedEdges: snapshot.Policy.Route.AllowsRepeatedEdges, AllowSelfLoops: snapshot.Policy.Route.AllowsSelfLoops},
	}
	if snapshot.Policy.Route.MaxHops > 0 {
		max := snapshot.Policy.Route.MaxHops
		catalog.RoutePolicy.MaxHops = &max
	}
	for _, node := range snapshot.Nodes {
		count := node.DocumentCount
		catalog.Nodes = append(catalog.Nodes, authoringv2.CatalogNode{ID: node.ID, ResourceType: node.ResourceType, RowRootEligible: node.RowRootEligible, RowGrain: node.RowGrain, Populated: node.Populated, DocumentCount: &count})
	}
	for _, edge := range snapshot.Edges {
		catalog.Edges = append(catalog.Edges, authoringv2.CatalogEdge{ID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID, Label: edge.Label, Populated: edge.ObservedEdgeCount > 0})
	}
	for _, candidate := range snapshot.Candidates {
		modes := make([]string, len(candidate.ProjectionModes))
		for i, mode := range candidate.ProjectionModes {
			modes[i] = wireProjectionMode(mode)
		}
		defaultMode := ""
		if len(modes) > 0 {
			defaultMode = modes[0]
		}
		catalog.Candidates = append(catalog.Candidates, authoringv2.CatalogCandidate{
			ID: candidate.ID, NodeID: candidate.NodeID, Label: candidate.Label, LogicalType: candidate.LogicalType,
			Repeated: candidate.Cardinality != "scalar", Filterable: supportsOperation(candidate.SupportedOperations, capability.OperationFilter), Chartable: supportsOperation(candidate.SupportedOperations, capability.OperationChart),
			ProjectionModes: modes, DefaultProjectionMode: defaultMode, Populated: candidate.Populated,
		})
	}
	return catalog
}

func validateSnapshot(snapshot capability.Snapshot) error {
	nodes := map[string]capability.Node{}
	for i, node := range snapshot.Nodes {
		if strings.TrimSpace(node.ID) == "" || strings.TrimSpace(node.ResourceType) == "" {
			return fail("capability", "INVALID_NODE", fmt.Sprintf("$.snapshot.nodes[%d]", i), "node id and resourceType are required", nil, nil)
		}
		if _, duplicate := nodes[node.ID]; duplicate {
			return fail("capability", "DUPLICATE_NODE", fmt.Sprintf("$.snapshot.nodes[%d]", i), "duplicate node id", map[string]any{"nodeId": node.ID}, nil)
		}
		nodes[node.ID] = node
	}
	edges := map[string]bool{}
	for i, edge := range snapshot.Edges {
		if edge.ID == "" || nodes[edge.FromNodeID].ID == "" || nodes[edge.ToNodeID].ID == "" || strings.TrimSpace(edge.Label) == "" {
			return fail("capability", "INVALID_EDGE", fmt.Sprintf("$.snapshot.edges[%d]", i), "edge has stale endpoints or empty label", nil, nil)
		}
		if edges[edge.ID] {
			return fail("capability", "DUPLICATE_EDGE", fmt.Sprintf("$.snapshot.edges[%d]", i), "duplicate edge id", map[string]any{"edgeId": edge.ID}, nil)
		}
		edges[edge.ID] = true
	}
	candidates := map[string]bool{}
	for i, candidate := range snapshot.Candidates {
		if candidate.ID == "" || nodes[candidate.NodeID].ID == "" || strings.TrimSpace(candidate.FieldPath) == "" {
			return fail("capability", "INVALID_CANDIDATE", fmt.Sprintf("$.snapshot.candidates[%d]", i), "candidate has stale node or empty field path", nil, nil)
		}
		if candidates[candidate.ID] {
			return fail("capability", "DUPLICATE_CANDIDATE", fmt.Sprintf("$.snapshot.candidates[%d]", i), "duplicate candidate id", map[string]any{"candidateId": candidate.ID}, nil)
		}
		candidates[candidate.ID] = true
	}
	return nil
}

func supportsOperation(items []capability.Operation, want capability.Operation) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func projectionValueMode(mode string) recipe.ValueMode {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case string(capability.ProjectionFirst):
		return recipe.ValueModeFirst
	case "ALL", string(capability.ProjectionArray):
		return recipe.ValueModeAll
	case string(capability.ProjectionDistinctArray):
		return recipe.ValueModeDistinct
	default:
		return recipe.ValueModeAuto
	}
}

func wireProjectionMode(mode capability.ProjectionMode) string {
	switch mode {
	case capability.ProjectionScalar:
		return "VALUE"
	case capability.ProjectionArray, capability.ProjectionDistinctArray:
		return "ALL"
	default:
		return string(mode)
	}
}

var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func recipeName(label, id string) string {
	if namePattern.MatchString(label) {
		return label
	}
	return "edge_" + shortHash(id)
}

func safeName(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(value) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	value = strings.Trim(builder.String(), "_")
	if value == "" {
		return "x"
	}
	if value[0] >= '0' && value[0] <= '9' {
		return "x_" + value
	}
	return value
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return fail("compile", "CONTEXT_CANCELED", "$", ctx.Err().Error(), nil, ctx.Err())
	default:
		return nil
	}
}
