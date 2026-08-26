package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/capabilitystore"
	"github.com/calypr/loom/internal/projectid"
	"golang.org/x/sync/singleflight"
)

const (
	explorerCapabilityCompilerVersion = "loom-dataframe-compiler-v2"
	explorerCapabilityProtocolVersion = "loom.calypr.org/explorer-authoring/v2"
	explorerTraversalPolicyVersion    = "finite-unbounded-v1"
	explorerProjectionPolicyVersion   = "compiler-probed-v1"
)

// explorerCapabilityResolver is the one construction boundary for Builder
// capabilities. Catalog observations are evidence only; every public item is
// intersected with the real dataframe compiler before the immutable snapshot
// is persisted or returned.
type explorerCapabilityResolver struct {
	evidence  catalog.CapabilityEvidenceReader
	scopes    *authscope.ScopeResolver
	manifests dataset.ActiveResolver
	snapshots capabilitystore.Repository
	builds    singleflight.Group
}

type ExplorerCapabilityReader func(context.Context, string, string, string) (capability.Snapshot, error)
type ExplorerCapabilityTokenReader func(context.Context, string, string) (capability.Snapshot, error)

// AuthorizedCapability is the capability contract handed to compilation and
// execution.  The scope is part of the contract: callers must not derive it
// again from a token or accidentally turn a restricted-empty scope into an
// unrestricted one.  Both values are copied before they cross this boundary.
type AuthorizedCapability struct {
	Snapshot capability.Snapshot
	Scope    authscope.ReadScope
}

// Clone returns an independent authorized capability.  Snapshot.Clone also
// copies nested diagnostic values through the capability package's immutable
// accessor, while ReadScope.Clone protects the authorization path slice.
func (a AuthorizedCapability) Clone() AuthorizedCapability {
	return AuthorizedCapability{Snapshot: a.Snapshot.Clone(), Scope: a.Scope.Clone()}
}

// ExplorerAuthorizedCapabilityCompilationReader accepts an opaque snapshot
// token for a new compile. Implementations must require that token to belong
// to the currently active dataset generation.
type ExplorerAuthorizedCapabilityCompilationReader func(context.Context, string, string) (AuthorizedCapability, error)

// ExplorerAuthorizedCapabilityExecutionReader loads an exact retained token
// for preview/materialization. Implementations may accept inactive generations
// while retained, but must re-authorize the caller against the token's exact
// generation and scope digest.
type ExplorerAuthorizedCapabilityExecutionReader func(context.Context, string, string) (AuthorizedCapability, error)

// ExplorerCapabilityCompilationResolver documents the compile authorization
// seam for callers that want a resolver object rather than a function field.
type ExplorerCapabilityCompilationResolver interface {
	ResolveForCompilation(context.Context, string, string) (AuthorizedCapability, error)
}

// ExplorerCapabilityExecutionResolver documents the execution authorization
// seam for callers that want a resolver object rather than a function field.
type ExplorerCapabilityExecutionResolver interface {
	ResolveForExecution(context.Context, string, string) (AuthorizedCapability, error)
}

func newExplorerCapabilityResolver(evidence catalog.CapabilityEvidenceReader, scopes *authscope.ScopeResolver, manifests dataset.ActiveResolver, snapshots capabilitystore.Repository) (*explorerCapabilityResolver, error) {
	if evidence == nil || manifests == nil || snapshots == nil {
		return nil, fmt.Errorf("Explorer capability evidence, manifest resolver, and snapshot repository are required")
	}
	return &explorerCapabilityResolver{evidence: evidence, scopes: scopes, manifests: manifests, snapshots: snapshots}, nil
}

func (r *explorerCapabilityResolver) Resolve(ctx context.Context, project, requestedGeneration string) (capability.Snapshot, error) {
	project = projectid.Canonical(project)
	storageProject := projectid.Legacy(project)
	principal, _ := authscope.PrincipalFromContext(ctx)
	// Project membership is independent of whether generation-level Fence
	// scopes are configured. A nil ScopeResolver means unrestricted dataframe
	// reads, not that a principal's explicit project allowlist is ignored.
	if err := authscope.AuthorizeProject(principal, project, false); err != nil {
		return capability.Snapshot{}, err
	}
	manifest, err := dataset.ResolveActive(ctx, r.manifests, storageProject)
	if err != nil {
		return capability.Snapshot{}, fmt.Errorf("resolve capability generation: %w", err)
	}
	generation := manifest.Dataset.Generation
	if requestedGeneration = strings.TrimSpace(requestedGeneration); requestedGeneration != "" && requestedGeneration != generation {
		return capability.Snapshot{}, fmt.Errorf("requested generation %q is not the active immutable generation %q", requestedGeneration, generation)
	}
	scope, err := r.resolveScope(ctx, principal, storageProject, generation)
	if err != nil {
		return capability.Snapshot{}, err
	}
	unrestricted := scope.Unrestricted()
	evidence, err := catalog.ReadCapabilityEvidence(ctx, r.evidence, catalog.CapabilityEvidenceOptions{
		Project: storageProject, DatasetGeneration: generation,
		AuthResourcePaths:             append([]string(nil), scope.AuthResourcePaths...),
		AuthResourcePathsUnrestricted: &unrestricted,
	})
	if err != nil {
		return capability.Snapshot{}, fmt.Errorf("read capability evidence: %w", err)
	}
	if err := requireCompleteCapabilityEvidence(evidence); err != nil {
		return capability.Snapshot{}, err
	}
	identity := capability.SnapshotIdentity{
		Project: project, Generation: generation,
		AuthorizationScopeDigest: explorerScopeDigest(scope),
		SchemaDigest:             manifest.SchemaIdentity.SchemaSHA256,
		ResourceInventoryDigest:  evidence.ResourceInventory.Digest,
		RelationshipDigest:       evidence.Relationships.Digest,
		FieldDigest:              evidence.FieldEnrichment.Digest,
		ProtocolVersion:          explorerCapabilityProtocolVersion,
		CompilerVersion:          explorerCapabilityCompilerVersion,
		TraversalPolicyVersion:   explorerTraversalPolicyVersion,
		ProjectionPolicyVersion:  explorerProjectionPolicyVersion,
	}
	key, err := capabilityIdentityKey(identity)
	if err != nil {
		return capability.Snapshot{}, err
	}
	value, err, _ := r.builds.Do(key, func() (any, error) {
		if existing, getErr := r.snapshots.GetByIdentity(ctx, identity); getErr == nil && existing.Usable() {
			return existing.Clone(), nil
		} else if getErr != nil && !errors.Is(getErr, capabilitystore.ErrNotFound) {
			return nil, getErr
		}
		observer := capabilityObserverFromEvidence(evidence)
		builder := capability.NewBuilder(identity, observer, observer, observer, explorerCapabilityCompiler{scope: compilerprobe.Scope{
			Project: storageProject, DatasetGeneration: generation,
			AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...), AuthScopeMode: scope.Mode,
		}})
		builder.Policy = capability.Policy{
			Route:      capability.RoutePolicy{Version: explorerTraversalPolicyVersion, MaxHops: 0, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: explorerProjectionPolicyVersion, SuggestionLimit: capability.DefaultSuggestionLimit},
		}
		built, buildErr := builder.Build(ctx)
		if buildErr != nil {
			_, _ = r.snapshots.Put(ctx, built)
			return nil, buildErr
		}
		if !built.Usable() {
			_, _ = r.snapshots.Put(ctx, built)
			return nil, capability.ErrSnapshotUnavailable
		}
		stored, putErr := r.snapshots.Put(ctx, built)
		if putErr != nil {
			return nil, putErr
		}
		return stored.Clone(), nil
	})
	if err != nil {
		return capability.Snapshot{}, err
	}
	return value.(capability.Snapshot).Clone(), nil
}

// ResolveActiveAuthorized is the scope-preserving form of Resolve. It is used
// by capability/catalog reads that select the active generation directly. The
// legacy Resolve method remains available for adapters that only need the
// public snapshot projection.
func (r *explorerCapabilityResolver) ResolveActiveAuthorized(ctx context.Context, project, requestedGeneration string) (AuthorizedCapability, error) {
	snapshot, err := r.Resolve(ctx, project, requestedGeneration)
	if err != nil {
		return AuthorizedCapability{}, err
	}
	canonicalProject := projectid.Canonical(project)
	principal, _ := authscope.PrincipalFromContext(ctx)
	scope, err := r.resolveScope(ctx, principal, projectid.Legacy(canonicalProject), snapshot.Identity.Generation)
	if err != nil {
		return AuthorizedCapability{}, err
	}
	if explorerScopeDigest(scope) != snapshot.Identity.AuthorizationScopeDigest {
		return AuthorizedCapability{}, capability.ErrStaleSnapshot
	}
	return AuthorizedCapability{Snapshot: snapshot.Clone(), Scope: scope.Clone()}, nil
}

// ResolveToken loads an exact retained snapshot. It never substitutes the
// current active generation, and it re-authorizes the caller against the
// snapshot's generation and exact authorization-scope digest.
func (r *explorerCapabilityResolver) ResolveToken(ctx context.Context, project, token string) (capability.Snapshot, error) {
	authorized, err := r.ResolveForExecution(ctx, project, token)
	if err != nil {
		return capability.Snapshot{}, err
	}
	return authorized.Snapshot.Clone(), nil
}

// ResolveForExecution loads an exact retained snapshot and its effective
// generation-scoped read authorization. Retention is intentional here:
// preview and other execution paths may use an inactive generation while its
// immutable capability remains available. The caller's current effective
// scope must still match the scope digest captured by the snapshot.
func (r *explorerCapabilityResolver) ResolveForExecution(ctx context.Context, project, token string) (AuthorizedCapability, error) {
	project = projectid.Canonical(project)
	token = strings.TrimSpace(token)
	snapshot, err := r.snapshots.GetByToken(ctx, token)
	if err != nil {
		return AuthorizedCapability{}, err
	}
	if snapshot.Identity.Project != project {
		return AuthorizedCapability{}, capability.ErrStaleSnapshot
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authscope.AuthorizeProject(principal, project, false); err != nil {
		return AuthorizedCapability{}, err
	}
	scope, err := r.resolveScope(ctx, principal, projectid.Legacy(project), snapshot.Identity.Generation)
	if err != nil {
		return AuthorizedCapability{}, err
	}
	if explorerScopeDigest(scope) != snapshot.Identity.AuthorizationScopeDigest {
		return AuthorizedCapability{}, capability.ErrStaleSnapshot
	}
	if err := snapshot.ValidateToken(token); err != nil {
		return AuthorizedCapability{}, err
	}
	return AuthorizedCapability{Snapshot: snapshot.Clone(), Scope: scope.Clone()}, nil
}

// ResolveForCompilation loads an exact token for a new compile. Unlike
// execution, compilation is only valid against the currently active immutable
// dataset generation; a retained inactive token is rejected even when its
// scope is otherwise still authorized.
func (r *explorerCapabilityResolver) ResolveForCompilation(ctx context.Context, project, token string) (AuthorizedCapability, error) {
	project = projectid.Canonical(project)
	authorized, err := r.ResolveForExecution(ctx, project, token)
	if err != nil {
		return AuthorizedCapability{}, err
	}
	manifest, err := dataset.ResolveActive(ctx, r.manifests, projectid.Legacy(project))
	if err != nil {
		return AuthorizedCapability{}, fmt.Errorf("resolve current capability generation: %w", err)
	}
	if authorized.Snapshot.Identity.Generation != manifest.Dataset.Generation {
		return AuthorizedCapability{}, capability.ErrStaleSnapshot
	}
	return authorized.Clone(), nil
}

func (r *explorerCapabilityResolver) resolveScope(ctx context.Context, principal *authscope.Principal, project, generation string) (authscope.ReadScope, error) {
	if r.scopes == nil {
		return authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, nil
	}
	return r.scopes.ResolveReadScopeForGeneration(ctx, principal, project, generation, nil)
}

func requireCompleteCapabilityEvidence(value catalog.CapabilityEvidence) error {
	checks := []struct {
		name      string
		available bool
		complete  bool
		truncated bool
	}{
		{"resource inventory", value.ResourceInventory.Available, value.ResourceInventory.Complete, value.ResourceInventory.Truncated},
		{"relationship evidence", value.Relationships.Available, value.Relationships.Complete, value.Relationships.Truncated},
		{"field enrichment", value.FieldEnrichment.Available, value.FieldEnrichment.Complete, value.FieldEnrichment.Truncated},
	}
	for _, check := range checks {
		if !check.available || !check.complete || check.truncated {
			return fmt.Errorf("%w: %s is unavailable, incomplete, or truncated", capability.ErrSnapshotUnavailable, check.name)
		}
	}
	var documents int64
	for _, item := range value.ResourceInventory.Values {
		documents += item.DocumentCount
	}
	// A staged generation proves the catalog write phase completed, and every
	// non-empty FHIR resource necessarily profiles at least its identity fields.
	// Therefore a populated inventory with zero enrichment rows is a missing
	// artifact, not a legitimate empty catalog (notably for older generations
	// created before capability evidence existed).
	if documents > 0 && len(value.FieldEnrichment.Values) == 0 {
		return fmt.Errorf("%w: field enrichment artifact is empty for a populated generation", capability.ErrSnapshotUnavailable)
	}
	return nil
}

func capabilityIdentityKey(identity capability.SnapshotIdentity) (string, error) {
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type capabilityEvidenceObserver struct {
	resources     []capability.ResourceObservation
	relationships []capability.RelationshipObservation
	fields        []capability.FieldObservation
}

func (o capabilityEvidenceObserver) ListResources(context.Context) ([]capability.ResourceObservation, error) {
	return append([]capability.ResourceObservation(nil), o.resources...), nil
}
func (o capabilityEvidenceObserver) ListRelationships(context.Context) ([]capability.RelationshipObservation, error) {
	return append([]capability.RelationshipObservation(nil), o.relationships...), nil
}
func (o capabilityEvidenceObserver) ListFields(context.Context) ([]capability.FieldObservation, error) {
	out := append([]capability.FieldObservation(nil), o.fields...)
	for i := range out {
		out[i].SuggestedValues = append([]string(nil), out[i].SuggestedValues...)
	}
	return out, nil
}

func capabilityObserverFromEvidence(value catalog.CapabilityEvidence) capabilityEvidenceObserver {
	out := capabilityEvidenceObserver{}
	for _, item := range value.ResourceInventory.Values {
		out.resources = append(out.resources, capability.ResourceObservation{ResourceType: item.ResourceType, Populated: item.DocumentCount > 0, DocumentCount: item.DocumentCount})
	}
	relationships := map[string]capability.RelationshipObservation{}
	for _, item := range value.Relationships.Values {
		key := strings.Join([]string{item.BuilderFromType, item.Label, item.BuilderToType, item.BuilderDirection}, "\x00")
		observation := relationships[key]
		observation.SourceResourceType = item.BuilderFromType
		observation.TargetResourceType = item.BuilderToType
		observation.Label = item.Label
		observation.StorageDirection = item.BuilderDirection
		observation.ObservedEdgeCount += item.EdgeCount
		observation.AllowsRepeatedTarget = true
		relationships[key] = observation
	}
	for _, item := range relationships {
		out.relationships = append(out.relationships, item)
	}
	sort.Slice(out.relationships, func(i, j int) bool {
		a, b := out.relationships[i], out.relationships[j]
		return strings.Join([]string{a.SourceResourceType, a.Label, a.TargetResourceType, a.StorageDirection}, "\x00") < strings.Join([]string{b.SourceResourceType, b.Label, b.TargetResourceType, b.StorageDirection}, "\x00")
	})
	type fieldAggregate struct {
		observation capability.FieldObservation
		values      map[string]struct{}
	}
	fields := map[string]*fieldAggregate{}
	for _, item := range value.FieldEnrichment.Values {
		key := item.ResourceType + "\x00" + item.Path
		aggregate := fields[key]
		if aggregate == nil {
			aggregate = &fieldAggregate{observation: capability.FieldObservation{ResourceType: item.ResourceType, Path: item.Path, Label: item.Path, LogicalType: item.Kind, Observed: true, Populated: item.DocCount > 0, SuggestionsComplete: true}, values: map[string]struct{}{}}
			fields[key] = aggregate
		}
		aggregate.observation.ObservedDocumentCount += item.DocCount
		aggregate.observation.Populated = aggregate.observation.Populated || item.DocCount > 0
		if item.DistinctTruncated {
			aggregate.observation.SuggestionsComplete = false
			aggregate.observation.SuggestionsTruncated = true
		}
		for _, suggestion := range item.DistinctValues {
			aggregate.values[suggestion] = struct{}{}
		}
	}
	for _, aggregate := range fields {
		for suggestion := range aggregate.values {
			aggregate.observation.SuggestedValues = append(aggregate.observation.SuggestedValues, suggestion)
		}
		sort.Strings(aggregate.observation.SuggestedValues)
		out.fields = append(out.fields, aggregate.observation)
	}
	sort.Slice(out.fields, func(i, j int) bool {
		return out.fields[i].ResourceType+"\x00"+out.fields[i].Path < out.fields[j].ResourceType+"\x00"+out.fields[j].Path
	})
	return out
}

type explorerCapabilityCompiler struct{ scope compilerprobe.Scope }

func (c explorerCapabilityCompiler) ProbeNode(ctx context.Context, node capability.Node) (capability.NodeProof, error) {
	result, err := compilerprobe.ProbeRoot(ctx, compilerprobe.RootRequest{Scope: c.scope, ResourceType: node.ResourceType})
	if err != nil {
		return capability.NodeProof{}, err
	}
	return capability.NodeProof{Allowed: true, RowRootEligible: result.Root.RowRootEligible, RowGrain: string(result.Root.RowGrain), SupportedOperations: []capability.Operation{capability.OperationSelect}}, nil
}

func (c explorerCapabilityCompiler) ProbeEdge(ctx context.Context, edge capability.Edge) (capability.EdgeProof, error) {
	result, err := compilerprobe.ProbeTraversal(ctx, compilerprobe.TraversalRequest{Scope: c.scope, RootResourceType: edge.SourceResourceType, Traversal: compilerprobe.Traversal{FromResourceType: edge.SourceResourceType, EdgeLabel: edge.Label, ToResourceType: edge.TargetResourceType, MatchMode: spec.TraversalMatchOptional}})
	if err != nil {
		return capability.EdgeProof{}, err
	}
	if result.Traversal == nil || string(result.Traversal.StorageDirection) != strings.ToUpper(edge.StorageDirection) {
		return capability.EdgeProof{Reason: "catalog storage direction does not match compiler lowering"}, nil
	}
	return capability.EdgeProof{Allowed: true}, nil
}

func (c explorerCapabilityCompiler) ProbeCandidate(ctx context.Context, candidate capability.Candidate) (capability.CandidateProof, error) {
	result, err := compilerprobe.ProbeCandidate(ctx, compilerprobe.CandidateRequest{Scope: c.scope, RootResourceType: candidate.ResourceType, ResourceType: candidate.ResourceType, Selector: candidate.FieldPath})
	if err != nil {
		return capability.CandidateProof{}, err
	}
	if result.Candidate == nil {
		return capability.CandidateProof{}, fmt.Errorf("compiler returned no candidate proof")
	}
	proof := capability.CandidateProof{Allowed: true, LogicalType: string(result.Candidate.Primitive), Cardinality: string(result.Candidate.Cardinality), SupportedOperations: []capability.Operation{capability.OperationSelect}}
	for _, mode := range result.Candidate.ProjectionModes {
		proof.ProjectionModes = append(proof.ProjectionModes, capability.ProjectionMode(strings.ToUpper(string(mode))))
	}
	for _, operator := range result.Candidate.FilterOperators {
		proof.FilterOperators = append(proof.FilterOperators, capability.FilterOperator(operator))
	}
	if len(proof.FilterOperators) > 0 {
		proof.SupportedOperations = append(proof.SupportedOperations, capability.OperationFilter)
	}
	for _, operation := range result.Candidate.ChartOperations {
		proof.ChartAggregations = append(proof.ChartAggregations, capability.ChartAggregation(operation))
	}
	if len(proof.ChartAggregations) > 0 {
		proof.SupportedOperations = append(proof.SupportedOperations, capability.OperationChart, capability.OperationAggregate)
	}
	return proof, nil
}

func legacyCatalogSnapshot(snapshot capability.Snapshot) explorer.CatalogSnapshot {
	catalogValue := explorer.Catalog{Selections: map[string]explorer.CatalogSelection{}}
	for _, node := range snapshot.Nodes {
		catalogValue.Nodes = append(catalogValue.Nodes, explorer.CatalogNode{ID: node.ID, ResourceType: node.ResourceType})
	}
	for _, edge := range snapshot.Edges {
		catalogValue.Edges = append(catalogValue.Edges, explorer.CatalogEdge{ID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID, Label: edge.Label})
	}
	for _, candidate := range snapshot.Candidates {
		catalogValue.Selections[candidate.ID] = explorer.CatalogSelection{
			ID: candidate.ID, NodeID: candidate.NodeID,
			FieldRef: explorerFieldRef(candidate.ResourceType, candidate.FieldPath), Select: candidate.FieldPath,
			LogicalType:           candidate.LogicalType,
			Cardinality:           candidate.Cardinality,
			ProjectionModes:       stringProjectionModes(candidate.ProjectionModes),
			DefaultProjectionMode: defaultProjectionMode(stringProjectionModes(candidate.ProjectionModes)),
			FilterOperators:       stringFilterOperators(candidate.FilterOperators),
			ChartOperations:       stringChartOperations(candidate.ChartAggregations),
			Filterable:            len(candidate.FilterOperators) > 0,
			Chartable:             len(candidate.ChartAggregations) > 0,
		}
	}
	diagnostics := make([]explorer.Diagnostic, 0, len(snapshot.Diagnostics))
	for _, diagnostic := range snapshot.Diagnostics {
		diagnostics = append(diagnostics, explorer.Diagnostic{Severity: diagnostic.Severity, Code: diagnostic.Code, Message: diagnostic.Message, Retryable: diagnostic.Retryable})
	}
	return explorer.CatalogSnapshot{
		Project: snapshot.Identity.Project, Generation: snapshot.Identity.Generation,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		ResolvedSchemaDigest:     snapshot.Identity.SchemaDigest,
		Catalog:                  catalogValue, Complete: snapshot.Complete, Truncated: snapshot.Truncated,
		Diagnostics: diagnostics, Token: snapshot.Token,
	}
}

func authoringV2Catalog(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
	result := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind,
		Project: snapshot.Identity.Project, ExplorerID: explorerID,
		SourceGeneration:         snapshot.Identity.Generation,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		ResolvedSchemaDigest:     snapshot.Identity.SchemaDigest,
		SnapshotToken:            snapshot.Token, Complete: snapshot.Complete, Truncated: snapshot.Truncated,
		Nodes: []authoringv2.CatalogNode{}, Edges: []authoringv2.CatalogEdge{}, Candidates: []authoringv2.CatalogCandidate{}, Diagnostics: []authoringv2.CatalogDiagnostic{},
		RoutePolicy: authoringv2.RoutePolicy{Unbounded: snapshot.Policy.Route.MaxHops == 0, AllowRepeatedEdges: snapshot.Policy.Route.AllowsRepeatedEdges, AllowSelfLoops: snapshot.Policy.Route.AllowsSelfLoops},
	}
	if snapshot.Policy.Route.MaxHops > 0 {
		maxHops := snapshot.Policy.Route.MaxHops
		result.RoutePolicy.MaxHops = &maxHops
	}
	for _, node := range snapshot.Nodes {
		count := node.DocumentCount
		result.Nodes = append(result.Nodes, authoringv2.CatalogNode{
			ID: node.ID, ResourceType: node.ResourceType, RowRootEligible: node.RowRootEligible,
			RowGrain: node.RowGrain, Populated: node.Populated, DocumentCount: &count,
		})
	}
	for _, edge := range snapshot.Edges {
		result.Edges = append(result.Edges, authoringv2.CatalogEdge{ID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID, Label: edge.Label, Populated: edge.ObservedEdgeCount > 0})
	}
	for _, candidate := range snapshot.Candidates {
		projectionModes := stringProjectionModes(candidate.ProjectionModes)
		count := candidate.ObservedDocumentCount
		wire := authoringv2.CatalogCandidate{
			ID: candidate.ID, NodeID: candidate.NodeID, Label: candidate.Label,
			LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality, Repeated: candidate.Cardinality != "scalar",
			Filterable: len(candidate.FilterOperators) > 0, Chartable: len(candidate.ChartAggregations) > 0,
			ProjectionModes: projectionModes, DefaultProjectionMode: defaultProjectionMode(projectionModes),
			FilterOperators: stringFilterOperators(candidate.FilterOperators), ChartOperations: stringChartOperations(candidate.ChartAggregations),
			Populated: candidate.Populated, Count: &count,
			SuggestionsAvailable: len(candidate.SuggestedValues) > 0,
			SuggestionsComplete:  candidate.SuggestionsComplete,
			SuggestionsTruncated: candidate.SuggestionsTruncated,
			SuggestionCount:      len(candidate.SuggestedValues),
		}
		result.Candidates = append(result.Candidates, wire)
	}
	for _, diagnostic := range snapshot.Diagnostics {
		severity := diagnostic.Severity
		if snapshot.Usable() && strings.EqualFold(severity, "ERROR") {
			severity = "WARNING"
		}
		result.Diagnostics = append(result.Diagnostics, authoringv2.CatalogDiagnostic{Severity: severity, Code: diagnostic.Code, Message: diagnostic.Message})
	}
	return result
}

func stringProjectionModes(values []capability.ProjectionMode) []string {
	out := make([]string, len(values))
	for i, value := range values {
		switch value {
		case capability.ProjectionScalar:
			out[i] = "VALUE"
		case capability.ProjectionArray, capability.ProjectionDistinctArray:
			out[i] = "ALL"
		default:
			out[i] = string(value)
		}
	}
	return out
}
func stringFilterOperators(values []capability.FilterOperator) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}
func stringChartOperations(values []capability.ChartAggregation) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}
func defaultProjectionMode(values []string) string {
	for _, preferred := range []string{"VALUE", "FIRST", "ALL", "COUNT"} {
		for _, value := range values {
			if value == preferred {
				return value
			}
		}
	}
	return ""
}
