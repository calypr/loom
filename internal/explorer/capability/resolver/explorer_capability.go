package resolver

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
	"github.com/calypr/loom/internal/catalog/codedconcept"
	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/projectid"
	"golang.org/x/sync/singleflight"
)

const (
	CompilerVersion         = "loom-dataframe-compiler-v4"
	ProtocolVersion         = "loom.calypr.org/explorer-authoring/v2"
	cacheVersion            = "observed-availability-v2"
	TraversalPolicyVersion  = "finite-unbounded-v1"
	ProjectionPolicyVersion = "compiler-probed-v2"
)

func ScopeDigest(scope authscope.ReadScope) string {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	return hex.EncodeToString(sum[:])
}

type Resolver struct {
	evidence  catalog.CapabilityEvidenceReader
	scopes    *authscope.ScopeResolver
	manifests dataset.ActiveResolver
	snapshots capability.Repository
	builds    singleflight.Group
}

func NewResolver(evidence catalog.CapabilityEvidenceReader, scopes *authscope.ScopeResolver, manifests dataset.ActiveResolver, snapshots capability.Repository) (*Resolver, error) {
	if evidence == nil || manifests == nil || snapshots == nil {
		return nil, fmt.Errorf("Explorer capability evidence, manifest resolver, and snapshot repository are required")
	}
	return &Resolver{evidence: evidence, scopes: scopes, manifests: manifests, snapshots: snapshots}, nil
}

func (r *Resolver) Resolve(ctx context.Context, project, requestedGeneration string) (capability.Snapshot, error) {
	project = projectid.Canonical(project)
	storageProject := projectid.Legacy(project)
	principal, _ := authscope.PrincipalFromContext(ctx)

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
		AuthorizationScopeDigest: ScopeDigest(scope),
		CacheVersion:             cacheVersion,
		SchemaDigest:             manifest.SchemaIdentity.SchemaSHA256,
		ResourceInventoryDigest:  evidence.ResourceInventory.Digest,
		RelationshipDigest:       evidence.Relationships.Digest,
		FieldDigest:              evidence.FieldEnrichment.Digest,
		ProtocolVersion:          ProtocolVersion,
		CompilerVersion:          CompilerVersion,
		TraversalPolicyVersion:   TraversalPolicyVersion,
		ProjectionPolicyVersion:  ProjectionPolicyVersion,
	}
	identity.ShapeDigest, err = catalog.RepeatedShapeDigest(evidence.FieldEnrichment.Values)
	if err != nil {
		return capability.Snapshot{}, fmt.Errorf("digest repeated shape evidence: %w", err)
	}
	entityIdentity := identity
	entityIdentity.CacheVersion = ""
	entityIdentity.FieldDigest, err = catalog.FieldEnrichmentDigest(evidence.FieldEnrichment.Values)
	if err != nil {
		return capability.Snapshot{}, fmt.Errorf("digest semantic field identity: %w", err)
	}
	key, err := capabilityIdentityKey(identity)
	if err != nil {
		return capability.Snapshot{}, err
	}
	value, err, _ := r.builds.Do(key, func() (any, error) {
		if existing, getErr := r.snapshots.GetByIdentity(ctx, identity); getErr == nil && existing.Usable() {
			return existing.Clone(), nil
		} else if getErr != nil && !errors.Is(getErr, capability.ErrNotFound) {
			return nil, getErr
		}
		compiler := explorerCapabilityCompiler{scope: compilerprobe.Scope{
			Project: storageProject, DatasetGeneration: generation,
			AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...), AuthScopeMode: scope.Mode,
		}}
		builder := capability.NewBuilder(identity, capabilityEvidenceFromCatalog(evidence), capability.CompilerCallbacks{
			Node: compiler.ProbeNode, Edge: compiler.ProbeEdge, Candidate: compiler.ProbeCandidate,
		})
		builder.EntityIdentity = entityIdentity
		builder.Policy = capability.Policy{
			Route:      capability.RoutePolicy{Version: TraversalPolicyVersion, MaxHops: 0, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: ProjectionPolicyVersion, SuggestionLimit: capability.DefaultSuggestionLimit},
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

func (r *Resolver) ResolveToken(ctx context.Context, project, token string) (capability.Snapshot, error) {
	authorized, err := r.ResolveForExecution(ctx, project, token)
	if err != nil {
		return capability.Snapshot{}, err
	}
	return authorized.Snapshot.Clone(), nil
}

func (r *Resolver) ResolveForExecution(ctx context.Context, project, token string) (ports.AuthorizedCapability, error) {
	project = projectid.Canonical(project)
	token = strings.TrimSpace(token)
	snapshot, err := r.snapshots.GetByToken(ctx, token)
	if err != nil {
		return ports.AuthorizedCapability{}, err
	}
	if snapshot.Identity.Project != project {
		return ports.AuthorizedCapability{}, capability.ErrStaleSnapshot
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authscope.AuthorizeProject(principal, project, false); err != nil {
		return ports.AuthorizedCapability{}, err
	}
	scope, err := r.resolveScope(ctx, principal, projectid.Legacy(project), snapshot.Identity.Generation)
	if err != nil {
		return ports.AuthorizedCapability{}, err
	}
	if ScopeDigest(scope) != snapshot.Identity.AuthorizationScopeDigest {
		return ports.AuthorizedCapability{}, capability.ErrStaleSnapshot
	}
	if err := snapshot.ValidateToken(token); err != nil {
		return ports.AuthorizedCapability{}, err
	}
	return ports.AuthorizedCapability{Snapshot: snapshot.Clone(), Scope: scope.Clone()}, nil
}

func (r *Resolver) ResolveForCompilation(ctx context.Context, project, token string) (ports.AuthorizedCapability, error) {
	project = projectid.Canonical(project)
	authorized, err := r.ResolveForExecution(ctx, project, token)
	if err != nil {
		return ports.AuthorizedCapability{}, err
	}
	manifest, err := dataset.ResolveActive(ctx, r.manifests, projectid.Legacy(project))
	if err != nil {
		return ports.AuthorizedCapability{}, fmt.Errorf("resolve current capability generation: %w", err)
	}
	if authorized.Snapshot.Identity.Generation != manifest.Dataset.Generation {
		return ports.AuthorizedCapability{}, capability.ErrStaleSnapshot
	}
	return authorized.Clone(), nil
}

func (r *Resolver) resolveScope(ctx context.Context, principal *authscope.Principal, project, generation string) (authscope.ReadScope, error) {
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
		status := strings.ToLower(strings.TrimSpace(string(stringStatusForCapabilityEvidence(value, check.name))))
		if status == "building" || status == "incomplete" || status == "truncated" || status == "unavailable" || status == "failed" || status == "invalidated" {
			return fmt.Errorf("%w: %s", capability.ErrSnapshotUnavailable, capabilityEvidenceUnavailableReason(check.name))
		}
		if !check.available || !check.complete || check.truncated {
			return fmt.Errorf("%w: %s", capability.ErrSnapshotUnavailable, capabilityEvidenceUnavailableReason(check.name))
		}
	}
	if strings.EqualFold(string(value.ResourceInventory.Status), "unknown") && len(value.ResourceInventory.Values) == 0 {
		return fmt.Errorf("%w: resource inventory readiness is unknown and no observed resource facts are available; rebuild the generation inventory before opening Builder", capability.ErrSnapshotUnavailable)
	}
	var documents int64
	for _, item := range value.ResourceInventory.Values {
		documents += item.DocumentCount
	}

	if documents > 0 && len(value.FieldEnrichment.Values) == 0 {
		return fmt.Errorf("%w: field enrichment artifact is empty for a populated generation", capability.ErrSnapshotUnavailable)
	}
	return nil
}

func capabilityEvidenceUnavailableReason(source string) string {
	switch source {
	case "resource inventory":
		return "resource inventory is unavailable or incomplete; rebuild the generation inventory before opening Builder"
	case "relationship evidence":
		return "relationship evidence is unavailable or incomplete; rebuild the generation catalog before opening Builder"
	default:
		return "field profile enrichment is unavailable or incomplete; rebuild the generation profiles before opening Builder"
	}
}

func stringStatusForCapabilityEvidence(value catalog.CapabilityEvidence, source string) catalog.EvidenceStatus {
	switch source {
	case "resource inventory":
		return value.ResourceInventory.Status
	case "relationship evidence":
		return value.Relationships.Status
	default:
		return value.FieldEnrichment.Status
	}
}

func capabilityIdentityKey(identity capability.SnapshotIdentity) (string, error) {
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func capabilityEvidenceFromCatalog(value catalog.CapabilityEvidence) capability.Evidence {
	out := capability.Evidence{
		ResourcesAvailable:     value.ResourceInventory.Available,
		RelationshipsAvailable: value.Relationships.Available,
		FieldsAvailable:        value.FieldEnrichment.Available,
		AvailabilityReadiness:  catalogAvailabilityReadiness(value),
	}
	for _, item := range value.ResourceInventory.Values {
		out.Resources = append(out.Resources, capability.ResourceObservation{ResourceType: item.ResourceType, Populated: item.DocumentCount > 0, DocumentCount: item.DocumentCount})
	}
	relationships := map[string]capability.RelationshipObservation{}
	observedRelationships := map[string]capability.ObservedRelationship{}
	for _, item := range value.Relationships.Values {
		source, target := item.BuilderFromType, item.BuilderToType
		if source == "" {
			source = item.FromType
		}
		if target == "" {
			target = item.ToType
		}
		builderDirection := item.BuilderDirection
		if builderDirection == "" {
			builderDirection = item.StorageDirection
		}
		observedKey := strings.Join([]string{source, item.Label, target, item.StorageDirection, builderDirection}, "\x00")
		observed := observedRelationships[observedKey]
		observed.SourceResourceType, observed.TargetResourceType = source, target
		observed.Label, observed.StorageDirection, observed.BuilderDirection = item.Label, item.StorageDirection, builderDirection
		observed.ObservedEdgeCount += item.EdgeCount
		observedRelationships[observedKey] = observed

		for _, route := range []struct {
			from, to, direction string
		}{
			{item.StorageFromType, item.StorageToType, "OUTBOUND"},
			{item.StorageToType, item.StorageFromType, "INBOUND"},
		} {
			key := strings.Join([]string{route.from, item.Label, route.to, route.direction}, "\x00")
			observation := relationships[key]
			observation.SourceResourceType = route.from
			observation.TargetResourceType = route.to
			observation.Label = item.Label
			observation.StorageDirection = route.direction
			observation.ObservedEdgeCount += item.EdgeCount
			observation.AllowsRepeatedTarget = true
			relationships[key] = observation
		}
	}
	for _, relationship := range observedRelationships {
		out.ObservedRelationships = append(out.ObservedRelationships, relationship)
	}
	sort.Slice(out.ObservedRelationships, func(i, j int) bool {
		a, b := out.ObservedRelationships[i], out.ObservedRelationships[j]
		return strings.Join([]string{a.SourceResourceType, a.Label, a.TargetResourceType, a.StorageDirection, a.BuilderDirection}, "\x00") < strings.Join([]string{b.SourceResourceType, b.Label, b.TargetResourceType, b.StorageDirection, b.BuilderDirection}, "\x00")
	})
	for _, item := range relationships {
		out.Relationships = append(out.Relationships, item)
	}
	sort.Slice(out.Relationships, func(i, j int) bool {
		a, b := out.Relationships[i], out.Relationships[j]
		return strings.Join([]string{a.SourceResourceType, a.Label, a.TargetResourceType, a.StorageDirection}, "\x00") < strings.Join([]string{b.SourceResourceType, b.Label, b.TargetResourceType, b.StorageDirection}, "\x00")
	})
	type observedFieldAggregate struct {
		observation capability.ObservedField
		examples    map[string]struct{}
		semantics   map[string]struct{}
	}
	observedFields := map[string]*observedFieldAggregate{}
	type fieldAggregate struct {
		observation capability.FieldObservation
		values      map[string]struct{}
		maxItems    int
	}
	fields := map[string]*fieldAggregate{}
	pendingConcepts := map[string][]capability.ConceptCandidate{}
	semanticOwnerFields := map[string]struct{}{}
	for _, item := range value.FieldEnrichment.Values {
		key := item.ResourceType + "\x00" + item.Path
		observed := observedFields[key]
		if observed == nil {
			observed = &observedFieldAggregate{
				observation: capability.ObservedField{ResourceType: item.ResourceType, Path: item.Path, ShapeKind: observedShapeKind(item.Kind), Examples: []string{}, SemanticObservations: []capability.ConceptCandidate{}},
				examples:    map[string]struct{}{}, semantics: map[string]struct{}{},
			}
			observedFields[key] = observed
		}
		observed.observation.PathObservedCount += item.DocCount
		observed.observation.ExamplesTruncated = observed.observation.ExamplesTruncated || item.DistinctTruncated
		for _, example := range item.DistinctValues {
			observed.examples[example] = struct{}{}
		}
		for _, observation := range item.SemanticObservations {
			semantic := capabilitySemanticCandidate(observation)
			encoded, _ := json.Marshal(semantic)
			if _, ok := observed.semantics[string(encoded)]; !ok {
				observed.semantics[string(encoded)] = struct{}{}
				observed.observation.SemanticObservations = append(observed.observation.SemanticObservations, semantic)
			}
		}
		aggregate := fields[key]
		if aggregate == nil {
			aggregate = &fieldAggregate{observation: capability.FieldObservation{ResourceType: item.ResourceType, Path: item.Path, Label: item.Path, LogicalType: item.Kind, ObservedShapeKind: observedShapeKind(item.Kind), Examples: []string{}, Observed: true, Populated: item.DocCount > 0, SuggestionsComplete: true}, values: map[string]struct{}{}}
			fields[key] = aggregate
		}
		aggregate.observation.ObservedDocumentCount += item.DocCount
		aggregate.maxItems = max(aggregate.maxItems, item.MaxItems)
		aggregate.observation.Populated = aggregate.observation.Populated || item.DocCount > 0
		if item.DistinctTruncated {
			aggregate.observation.SuggestionsComplete = false
			aggregate.observation.SuggestionsTruncated = true
		}
		for _, suggestion := range item.DistinctValues {
			aggregate.values[suggestion] = struct{}{}
			aggregate.observation.Examples = append(aggregate.observation.Examples, suggestion)
		}
		aggregate.observation.ExamplesTruncated = aggregate.observation.ExamplesTruncated || item.DistinctTruncated
		for _, observation := range item.SemanticObservations {
			concept := capabilitySemanticCandidate(observation)
			if semanticConceptOwnerPath(item.Path, observation) {
				semanticOwnerFields[key] = struct{}{}
			}
			targetPath := semanticConceptValuePath(observation)
			if targetPath == "" {
				continue
			}
			metadata, scalar := fhirschema.ResolveTerminalScalarMetadata(item.ResourceType, targetPath)
			if !scalar || metadata.Primitive == fhirschema.PrimitiveUnknown {
				continue
			}
			targetKey := item.ResourceType + "\x00" + targetPath
			pendingConcepts[targetKey] = append(pendingConcepts[targetKey], concept)
		}
	}
	for key, concepts := range pendingConcepts {
		aggregate := fields[key]
		if aggregate == nil {
			resourceType, targetPath, ok := strings.Cut(key, "\x00")
			if !ok {
				continue
			}
			metadata, scalar := fhirschema.ResolveTerminalScalarMetadata(resourceType, targetPath)
			if !scalar || metadata.Primitive == fhirschema.PrimitiveUnknown {

				continue
			}
			maxPopulation := int64(0)
			for _, concept := range concepts {
				if concept.Population > maxPopulation {
					maxPopulation = concept.Population
				}
			}
			aggregate = &fieldAggregate{observation: capability.FieldObservation{
				ResourceType: resourceType, Path: targetPath, Label: targetPath, LogicalType: string(metadata.Primitive),
				Observed: true, ObservedDocumentCount: maxPopulation, Populated: maxPopulation > 0,
				SuggestionsComplete: true,
			}, values: map[string]struct{}{}}
			fields[key] = aggregate
		}
		aggregate.observation.ConceptCandidates = append(aggregate.observation.ConceptCandidates, concepts...)
	}
	for _, aggregate := range fields {
		parts := strings.Split(aggregate.observation.Path, ".")
		prefix := make([]string, 0, len(parts))
		for _, part := range parts {
			prefix = append(prefix, part)
			if !strings.HasSuffix(part, "[]") {
				continue
			}
			boundaryPath := strings.Join(prefix, ".")
			boundary := fields[aggregate.observation.ResourceType+"\x00"+boundaryPath]
			if boundary != nil && boundary.maxItems > 0 {
				aggregate.observation.RepeatedBoundaries = append(aggregate.observation.RepeatedBoundaries, capability.RepeatedBoundary{Path: boundaryPath, MaxItems: boundary.maxItems})
			}
		}
	}
	for _, aggregate := range fields {
		key := aggregate.observation.ResourceType + "\x00" + aggregate.observation.Path
		if _, semanticOwner := semanticOwnerFields[key]; semanticOwner {

			continue
		}
		for suggestion := range aggregate.values {
			aggregate.observation.SuggestedValues = append(aggregate.observation.SuggestedValues, suggestion)
		}
		sort.Strings(aggregate.observation.SuggestedValues)
		out.Fields = append(out.Fields, aggregate.observation)
	}
	sort.Slice(out.Fields, func(i, j int) bool {
		return out.Fields[i].ResourceType+"\x00"+out.Fields[i].Path < out.Fields[j].ResourceType+"\x00"+out.Fields[j].Path
	})
	for _, aggregate := range observedFields {
		for example := range aggregate.examples {
			aggregate.observation.Examples = append(aggregate.observation.Examples, example)
		}
		sort.Strings(aggregate.observation.Examples)
		out.ObservedFields = append(out.ObservedFields, aggregate.observation)
	}
	sort.Slice(out.ObservedFields, func(i, j int) bool {
		return out.ObservedFields[i].ResourceType+"\x00"+out.ObservedFields[i].Path < out.ObservedFields[j].ResourceType+"\x00"+out.ObservedFields[j].Path
	})
	return out
}

func semanticConceptValuePath(observation codedconcept.Observation) string {
	selector := strings.Trim(strings.TrimSpace(observation.Value.Selector), ".")
	selector = strings.TrimPrefix(selector, "root.")
	if selector == "" {
		return ""
	}
	scope := strings.Trim(strings.TrimSpace(observation.OwningScope), ".")
	scope = strings.TrimPrefix(scope, "root.")
	if scope == "" || selector == scope || strings.HasPrefix(selector, scope+".") {
		return selector
	}
	return scope + "." + selector
}

func semanticConceptOwnerPath(fieldPath string, observation codedconcept.Observation) bool {
	fieldPath = strings.TrimPrefix(strings.Trim(strings.TrimSpace(fieldPath), "."), "root.")
	ownerPath := strings.TrimPrefix(strings.Trim(strings.TrimSpace(observation.OwningScope), "."), "root.")
	return fieldPath != "" && ownerPath != "" && fieldPath == ownerPath
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

	input := capability.AggregateInput{
		LogicalType:     string(result.Candidate.Primitive),
		Cardinality:     string(result.Candidate.Cardinality),
		HasField:        true,
		RelatedResource: true,
	}
	valueOperations := make([]capability.AggregateOperationCapability, 0, len(result.Candidate.ValueAggregateOperations)*3)
	for _, rows := range []capability.AggregateRowContext{capability.AggregateRowsRecords, capability.AggregateRowsGroups, capability.AggregateRowsExpanded} {
		valueOperations = append(valueOperations, capability.DeriveAggregateOperationCapabilities(input, rows)...)
	}
	compilerOperations := make(map[recipe.AggregateOperation]struct{}, len(result.Candidate.ValueAggregateOperations))
	for _, operation := range result.Candidate.ValueAggregateOperations {
		compilerOperations[operation] = struct{}{}
	}
	for index := range valueOperations {
		choice := &valueOperations[index]
		if !choice.Supported || choice.Operation == capability.AggregateFirstOrdered {
			continue
		}
		if _, ok := compilerOperations[recipe.AggregateOperation(choice.Operation)]; !ok {
			choice.Supported = false
			choice.ReasonCode = "COMPILER_NOT_PROVEN"
			choice.Reason = "the compiler did not prove this operation for the candidate field"
			choice.RequiresConfiguration = nil
		}
	}
	proof.AggregateOperations = valueOperations
	return proof, nil
}

func catalogAvailabilityReadiness(value catalog.CapabilityEvidence) capability.AvailabilityReadiness {
	readiness := capability.AvailabilityReadiness{
		ResourceInventory: sourceReadiness(value.ResourceInventory.Available, value.ResourceInventory.Complete, value.ResourceInventory.Truncated, string(value.ResourceInventory.Status)),
		Relationships:     sourceReadiness(value.Relationships.Available, value.Relationships.Complete, value.Relationships.Truncated, string(value.Relationships.Status)),
		FieldProfiles:     fieldProfileReadiness(value.FieldEnrichment),
	}
	readiness.Status = "COMPLETE"
	for _, source := range []capability.AvailabilitySourceReadiness{readiness.ResourceInventory, readiness.Relationships, readiness.FieldProfiles} {
		if source.Status == "BLOCKED" {
			readiness.Status = "BLOCKED"
			break
		}
		if source.Status != "COMPLETE" {
			readiness.Status = "UNKNOWN"
		}
	}
	for _, source := range []capability.AvailabilitySourceReadiness{readiness.ResourceInventory, readiness.Relationships, readiness.FieldProfiles} {
		if source.Reason != "" {
			readiness.Reason = source.Reason
			break
		}
	}
	return readiness
}

func fieldProfileReadiness(value catalog.FieldEnrichmentResult) capability.AvailabilitySourceReadiness {
	readiness := sourceReadiness(value.Available, value.Complete, value.Truncated, string(value.Status))
	if !value.FieldCoverageKnown && readiness.Status == "COMPLETE" {
		return capability.AvailabilitySourceReadiness{Status: "UNKNOWN", Reason: "field profile coverage is not proven for this generation"}
	}
	return readiness
}

func sourceReadiness(available, complete, truncated bool, status string) capability.AvailabilitySourceReadiness {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "unknown", "unproven":
		return capability.AvailabilitySourceReadiness{Status: "UNKNOWN", Reason: "source completeness is not proven"}
	case "building", "incomplete", "truncated", "unavailable", "failed", "invalidated":
		return capability.AvailabilitySourceReadiness{Status: "BLOCKED", Reason: "source artifact is not complete and ready"}
	}
	if truncated {
		return capability.AvailabilitySourceReadiness{Status: "BLOCKED", Reason: "source artifact is truncated"}
	}
	if !available {
		return capability.AvailabilitySourceReadiness{Status: "BLOCKED", Reason: "source artifact is unavailable"}
	}
	if !complete {
		return capability.AvailabilitySourceReadiness{Status: "UNKNOWN", Reason: "source completeness is not established"}
	}
	return capability.AvailabilitySourceReadiness{Status: "COMPLETE"}
}

func observedShapeKind(value string) string {
	if value == "codeable_concept" {
		return "object"
	}
	return value
}

func capabilitySemanticCandidate(value codedconcept.Observation) capability.ConceptCandidate {
	return capability.ConceptCandidate{
		SourceResourceType: value.Source.Type, SourceCanonical: value.Source.Canonical,
		SourceProfile: value.Source.Profile, SourcePath: value.Source.Path,
		OwningScope: value.OwningScope, ExtensionURLPath: append([]string(nil), value.ExtensionURLPath...),
		KeySelector: value.Key.Selector, System: value.Key.System, Code: value.Key.Code, Display: value.Key.Display,
		ValueSelector: value.Value.Selector, ChoiceArm: value.ChoiceArm, LogicalType: value.LogicalType,
		ObservedUnits: append([]string(nil), value.ObservedUnits...), ObservedUnitsTruncated: value.ObservedUnitsTruncated,
		Completeness: string(value.Completeness), Status: value.Status, Population: value.Population,
		Examples: append([]string(nil), value.Examples...), ExamplesTruncated: value.ExamplesTruncated,
		RuleHint: value.RuleHint, RuleVersion: value.RuleVersion,
	}
}
