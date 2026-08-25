package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/projectid"
)

var authoringIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type ExplorerAuthoringV1CompileRequest struct {
	Bundle        explorer.ExplorerAuthoringBundleV1
	SnapshotToken string
	RequestID     string
}

type ExplorerAuthoringV1CompileResult struct {
	Bundle               explorer.ExplorerAuthoringBundleV1
	CanonicalBundle      []byte
	Receipt              explorer.CompilationReceipt
	RecipeDigest         string
	ResolvedSchemaDigest string
	SourceGeneration     string
	EmittedColumns       []explorer.EmittedColumn
	ResolvedBindings     []explorer.ExplorerResolvedBindingV1
	// Snapshot is the exact catalog snapshot used for route/candidate
	// resolution. Builder responses reuse it so bindings and catalog metadata
	// cannot drift across two catalog reads.
	Snapshot    explorer.CatalogSnapshot
	Diagnostics []explorer.AuthoringDiagnostic
}

// ResolveAuthoringBundle is the single authoritative authoring phase used by
// Builder reads, preview, publication, and compilation. It resolves opaque
// intent against exactly one catalog snapshot, validates route occurrences and
// candidates, lowers the native recipe, compiles it, and returns the resolved
// Builder bindings alongside the executable receipt.
func ResolveAuthoringBundle(ctx context.Context, recipeEngine *engine.Engine, catalogReader explorerV2CatalogReader, request ExplorerAuthoringV1CompileRequest) (ExplorerAuthoringV1CompileResult, error) {
	bundle := request.Bundle
	// Intent digests describe the pre-resolution document. Legacy documents
	// may have been persisted before candidateOccurrences became canonical, so
	// validate the strict shape first and verify the digest only after the
	// catalog-aware normalizer has produced the canonical document.
	shape := bundle
	shape.IntentDigest = ""
	if err := shape.Validate(); err != nil {
		return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(err, request.RequestID)
	}
	bundle.Project = projectid.Canonical(bundle.Project)
	documents := bundle.AuthoringDocuments()
	if len(documents) == 0 {
		return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(authoringSemantic("intent", "$.bundle.documents", "EMPTY_AUTHORING_BUNDLE", "preview and publish require at least one output", nil), request.RequestID)
	}
	// Normalize compatibility single-document input before it is stored in a
	// receipt or returned to the Builder. One Explorer may contain multiple
	// output documents; the canonical wire form is always documents[].
	bundle.Document = explorer.ExplorerBuilderDocumentV1{}
	bundle.Documents = documents
	if request.SnapshotToken == "" {
		return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(explorerConflict("snapshot", "SNAPSHOT_REQUIRED", "a catalog snapshot token is required", nil), request.RequestID)
	}
	if catalogReader == nil {
		return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(explorerUnavailable("catalog", "CATALOG_UNAVAILABLE", "Explorer authoring catalog is not configured"), request.RequestID)
	}
	snapshot, err := catalogReader(ctx, bundle.Project, bundle.ExplorerID, "")
	if err != nil {
		if snapshot.Token != "" && !snapshot.Complete {
			return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(explorerUnavailable("catalog", "CATALOG_INCOMPLETE", "Explorer authoring catalog is incomplete"), request.RequestID)
		}
		return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(explorerConflict("catalog", "CATALOG_SNAPSHOT_FAILED", err.Error(), nil), request.RequestID)
	}
	if err := snapshot.ValidateToken(request.SnapshotToken); err != nil {
		code := "STALE_CATALOG_SNAPSHOT"
		if errors.Is(err, explorer.ErrIncompleteCatalog) {
			code = "CATALOG_INCOMPLETE"
		}
		return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(explorerConflict("catalog", code, err.Error(), map[string]any{"snapshotToken": request.SnapshotToken}), request.RequestID)
	}
	documents, err = normalizeStructuralAuthoringRoutes(documents, snapshot.Catalog)
	if err != nil {
		return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(err, request.RequestID)
	}
	documents, err = normalizeAuthoringDocuments(documents, snapshot.Catalog)
	if err != nil {
		return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(err, request.RequestID)
	}
	bundle.Documents = documents
	digest, err := bundle.DocumentDigest()
	if err != nil {
		return ExplorerAuthoringV1CompileResult{}, err
	}
	// The compatibility normalization above changes canonical authoring intent.
	// Return the matching digest with the normalized bundle so a subsequent
	// preview or publish can round-trip the Builder response without a 409.
	bundle.IntentDigest = digest

	var native recipe.Bundle
	var emitted []explorer.EmittedColumn
	var mappings []explorer.IdentityMapping
	var bindings []explorer.ExplorerResolvedBindingV1
	for _, document := range documents {
		path, routeErr := resolveAuthoringRoute(document, snapshot.Catalog)
		if routeErr != nil {
			return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(routeErr, request.RequestID)
		}
		assignments, candidateErr := resolveAuthoringCandidates(document, snapshot.Catalog, path)
		if candidateErr != nil {
			return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(candidateErr, request.RequestID)
		}
		documentEmitted, documentMappings := authoringEmissions(document, snapshot.Catalog, assignments)
		if presentationErr := validateAuthoringPresentation(document, documentEmitted); presentationErr != nil {
			return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(presentationErr, request.RequestID)
		}
		part, lowerErr := lowerAuthoringDocument(bundle.Project, bundle.ExplorerID, document, snapshot.Catalog, path, assignments)
		if lowerErr != nil {
			return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(lowerErr, request.RequestID)
		}
		if len(part.Outputs) != 1 {
			return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(authoringSemantic("lower", "$.documents", "AUTHORING_OUTPUT_LOWERING_FAILED", "each authoring document must lower to exactly one recipe output", nil), request.RequestID)
		}
		if len(native.Outputs) == 0 {
			native = part
		} else {
			native.Outputs = append(native.Outputs, part.Outputs...)
		}
		emitted = append(emitted, documentEmitted...)
		mappings = append(mappings, documentMappings...)
		bindings = append(bindings, resolvedAuthoringBinding(document, snapshot.Catalog, path, assignments, documentEmitted))
	}
	if err := native.Validate(); err != nil {
		return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(authoringSemanticFromRecipe(err), request.RequestID)
	}

	recipeDigest, resolvedSchemaDigest := "", snapshot.ResolvedSchemaDigest
	if recipeEngine != nil {
		resolved, resolveErr := recipeEngine.ResolveBundle(ctx, native, recipe.RuntimeBindings{Project: projectid.Legacy(bundle.Project), DatasetGeneration: snapshot.Generation})
		if resolveErr != nil {
			return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(authoringSemanticFromCompile(resolveErr), request.RequestID)
		}
		recipeDigest = resolved.StoredRecipeDigest
		resolvedSchemaDigest = resolved.ResolvedSchemaDigest
		// The lowerer is authoritative for physical public columns. Keep the
		// intent-to-emission IDs stable, while using the compiler schema only to
		// fill logical type metadata.
		kindByColumn := map[string]string{}
		for _, output := range resolved.Compiled.Outputs {
			for _, column := range output.OutputSchema {
				if !column.Internal {
					kindByColumn[column.Name] = column.Kind
				}
			}
		}
		for i := range emitted {
			if kind := kindByColumn[generatedFieldName(emitted[i].CandidateID, emitted[i].OccurrenceID)]; kind != "" {
				emitted[i].LogicalType = kind
			}
		}
	} else {
		recipeDigest, _ = native.Digest()
	}
	if recipeDigest == "" {
		recipeDigest, _ = native.Digest()
	}
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		return ExplorerAuthoringV1CompileResult{}, withRequestAuthoringError(err, request.RequestID)
	}
	compiledConfig, err := authoringCompiledConfig(bundle, native, emitted)
	if err != nil {
		return ExplorerAuthoringV1CompileResult{}, err
	}
	receipt := explorer.CompilationReceipt{
		Project: bundle.Project, ExplorerID: bundle.ExplorerID, IntentDigest: digest,
		SnapshotToken: request.SnapshotToken, SourceGeneration: snapshot.Generation,
		RecipeDigest: recipeDigest, ResolvedSchemaDigest: resolvedSchemaDigest,
		NormalizedBundle: append([]byte(nil), canonical...), Bundle: native,
		CompiledConfig: compiledConfig, IdentityMappings: mappings, EmittedColumns: emitted,
		CreatedAt: time.Now().UTC(), RequestID: request.RequestID,
	}
	receipt.OutputFingerprints = map[string]string{}
	for _, output := range native.Outputs {
		receipt.OutputFingerprints[output.Name] = outputFingerprint(native, bundle.Project, snapshot.Generation)
	}
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		return ExplorerAuthoringV1CompileResult{}, err
	}
	return ExplorerAuthoringV1CompileResult{Bundle: bundle, CanonicalBundle: canonical, Receipt: receipt, RecipeDigest: recipeDigest, ResolvedSchemaDigest: resolvedSchemaDigest, SourceGeneration: snapshot.Generation, EmittedColumns: emitted, ResolvedBindings: bindings, Snapshot: snapshot, Diagnostics: []explorer.AuthoringDiagnostic{}}, nil
}

// normalizeStructuralAuthoringRoutes lets a client append exact catalog edge
// IDs without inventing occurrence identities. Existing route prefixes retain
// their IDs; Loom deterministically owns only the newly appended occurrences.
func normalizeStructuralAuthoringRoutes(documents []explorer.ExplorerBuilderDocumentV1, catalog explorer.Catalog) ([]explorer.ExplorerBuilderDocumentV1, error) {
	edges := make(map[string]explorer.CatalogEdge, len(catalog.Edges))
	for _, edge := range catalog.Edges {
		edges[edge.ID] = edge
	}
	normalized := append([]explorer.ExplorerBuilderDocumentV1(nil), documents...)
	for documentIndex := range normalized {
		document := &normalized[documentIndex]
		path := fmt.Sprintf("$.documents[%d]", documentIndex)
		current := document.BaseNodeID
		targets := make([]string, 0, len(document.RouteEdgeIDs))
		for edgeIndex, edgeID := range document.RouteEdgeIDs {
			edge, ok := edges[edgeID]
			if !ok {
				return nil, authoringSemantic("route", fmt.Sprintf("%s.routeEdgeIds[%d]", path, edgeIndex), "STALE_ROUTE_EDGE", "route edge is not present in the catalog snapshot", map[string]any{"edgeId": edgeID})
			}
			if edge.FromNodeID != current {
				return nil, authoringSemantic("route", fmt.Sprintf("%s.routeEdgeIds[%d]", path, edgeIndex), "INVALID_ROUTE_CONTINUITY", "ordered route edges do not form a continuous path", map[string]any{"edgeId": edgeID, "currentNodeId": current})
			}
			current = edge.ToNodeID
			targets = append(targets, current)
		}
		provided := append([]explorer.ExplorerRouteOccurrenceV1(nil), document.RouteOccurrences...)
		providedOccurrenceIDs := make(map[string]bool, len(provided))
		for _, occurrence := range provided {
			providedOccurrenceIDs[occurrence.ID] = true
		}
		if len(provided) > 0 && (provided[0].ID == "base" || provided[0].NodeID == document.BaseNodeID && len(provided) == len(document.RouteEdgeIDs)+1) {
			provided = provided[1:]
		}
		// A Builder can retain route/candidate state from a longer route while
		// the user shortens the route. Keep the surviving prefix and let the
		// candidate normalizer remove selections tied only to occurrences that
		// no longer exist.
		if len(provided) > len(targets) {
			provided = provided[:len(targets)]
		}
		occurrences := []explorer.ExplorerRouteOccurrenceV1{{ID: "base", Index: 0, NodeID: document.BaseNodeID}}
		for index, nodeID := range targets {
			edgeID := document.RouteEdgeIDs[index]
			occurrence := explorer.ExplorerRouteOccurrenceV1{}
			if index < len(provided) {
				occurrence = provided[index]
				if occurrence.NodeID != nodeID || occurrence.IncomingEdgeID != "" && occurrence.IncomingEdgeID != edgeID {
					return nil, authoringSemantic("route", fmt.Sprintf("%s.routeOccurrences[%d]", path, index), "INVALID_ROUTE_OCCURRENCE_NODE", "route occurrence does not match its exact edge target", map[string]any{"expectedNodeId": nodeID, "receivedNodeId": occurrence.NodeID})
				}
			} else {
				occurrence.ID = explorer.OpaqueID("occ_", document.Output.ID+"\x00"+strconv.Itoa(index+1)+"\x00"+edgeID+"\x00"+nodeID)
			}
			occurrence.Index = index + 1
			occurrence.NodeID = nodeID
			occurrence.IncomingEdgeID = edgeID
			occurrences = append(occurrences, occurrence)
		}
		document.RouteOccurrences = occurrences
		keptOccurrenceIDs := make(map[string]bool, len(occurrences))
		for _, occurrence := range occurrences {
			keptOccurrenceIDs[occurrence.ID] = true
		}
		candidateOccurrences := make([]explorer.ExplorerCandidateOccurrenceV1, 0, len(document.CandidateOccurrences))
		removedCandidateIDs := map[string]bool{}
		for _, reference := range document.CandidateOccurrences {
			if reference.OccurrenceID != "base" && providedOccurrenceIDs[reference.OccurrenceID] && !keptOccurrenceIDs[reference.OccurrenceID] {
				removedCandidateIDs[reference.CandidateID] = true
				continue
			}
			candidateOccurrences = append(candidateOccurrences, reference)
		}
		document.CandidateOccurrences = candidateOccurrences
		if len(removedCandidateIDs) > 0 {
			candidateIDs := make([]string, 0, len(document.CandidateIDs))
			for _, candidateID := range document.CandidateIDs {
				if removedCandidateIDs[candidateID] && !candidateOccurrenceHasID(candidateOccurrences, candidateID) {
					continue
				}
				candidateIDs = append(candidateIDs, candidateID)
			}
			document.CandidateIDs = candidateIDs
		}
	}
	return normalized, nil
}

func candidateOccurrenceHasID(references []explorer.ExplorerCandidateOccurrenceV1, candidateID string) bool {
	for _, reference := range references {
		if reference.CandidateID == candidateID {
			return true
		}
	}
	return false
}

// normalizeAuthoringDocuments resolves every legacy candidate selection
// against the exact route occurrences that structural normalization produced.
// It is deliberately separate from recipe lowering so GET, Builder POST,
// preview, publish, and the admin migration all share the same repair rules.
func normalizeAuthoringDocuments(documents []explorer.ExplorerBuilderDocumentV1, catalog explorer.Catalog) ([]explorer.ExplorerBuilderDocumentV1, error) {
	normalized := append([]explorer.ExplorerBuilderDocumentV1(nil), documents...)
	for index := range normalized {
		document := normalized[index]
		path := fmt.Sprintf("$.documents[%d]", index)
		if _, err := resolveAuthoringRoute(document, catalog); err != nil {
			return nil, err
		}
		binding := explorer.ExplorerResolvedBindingV1{OutputID: document.Output.ID, BaseNodeID: document.BaseNodeID, RouteOccurrences: make([]explorer.ExplorerResolvedOccurrenceV1, 0, len(document.RouteOccurrences))}
		for _, occurrence := range document.RouteOccurrences {
			binding.RouteOccurrences = append(binding.RouteOccurrences, explorer.ExplorerResolvedOccurrenceV1{OccurrenceID: occurrence.ID, Index: occurrence.Index, NodeID: occurrence.NodeID, IncomingEdgeID: occurrence.IncomingEdgeID})
		}
		var err error
		normalized[index], err = normalizeDocument(document, catalog, binding)
		if err != nil {
			return nil, withDocumentPath(err, path)
		}
	}
	return normalized, nil
}

// normalizeDocument upgrades the legacy candidateIds-only representation to
// the canonical candidateIds + candidateOccurrences representation. Existing
// valid pairs are retained verbatim. A missing pair is inferred only when the
// catalog identifies exactly one route occurrence; repeated resource nodes
// remain an explicit user choice.
func normalizeDocument(document explorer.ExplorerBuilderDocumentV1, catalog explorer.Catalog, resolvedBinding explorer.ExplorerResolvedBindingV1) (explorer.ExplorerBuilderDocumentV1, error) {
	selectionByID := catalog.Selections
	routeOccurrences := append([]explorer.ExplorerResolvedOccurrenceV1(nil), resolvedBinding.RouteOccurrences...)
	occurrenceNodes := make(map[string]string, len(routeOccurrences)+1)
	for _, occurrence := range resolvedBinding.RouteOccurrences {
		if strings.TrimSpace(occurrence.OccurrenceID) == "" {
			continue
		}
		occurrenceNodes[occurrence.OccurrenceID] = occurrence.NodeID
	}
	baseNodeID := resolvedBinding.BaseNodeID
	if baseNodeID == "" {
		baseNodeID = document.BaseNodeID
	}
	if _, ok := occurrenceNodes["base"]; !ok {
		occurrenceNodes["base"] = baseNodeID
		routeOccurrences = append([]explorer.ExplorerResolvedOccurrenceV1{{OccurrenceID: "base", Index: 0, NodeID: baseNodeID}}, routeOccurrences...)
	}

	byCandidate := make(map[string][]string, len(document.CandidateOccurrences))
	seenPairs := make(map[string]bool, len(document.CandidateOccurrences))
	for index, reference := range document.CandidateOccurrences {
		if _, ok := selectionByID[reference.CandidateID]; !ok {
			return document, authoringSemantic("candidates", fmt.Sprintf("$.document.candidateOccurrences[%d].candidateId", index), "STALE_CANDIDATE_ID", "candidate is not present in the catalog snapshot", map[string]any{"candidateId": reference.CandidateID})
		}
		occurrenceNode, ok := occurrenceNodes[reference.OccurrenceID]
		if !ok {
			return document, authoringSemantic("candidates", fmt.Sprintf("$.document.candidateOccurrences[%d].occurrenceId", index), "STALE_ROUTE_OCCURRENCE", "candidate occurrence references an unknown route occurrence", map[string]any{"occurrenceId": reference.OccurrenceID})
		}
		key := reference.CandidateID + "\x00" + reference.OccurrenceID
		if seenPairs[key] {
			return document, authoringSemantic("candidates", "$.document.candidateOccurrences", "DUPLICATE_CANDIDATE_OCCURRENCE", "a candidate occurrence may be listed only once", nil)
		}
		seenPairs[key] = true
		if selectionByID[reference.CandidateID].NodeID != occurrenceNode {
			return document, authoringSemantic("candidates", "$.document.candidateOccurrences", "SELECTION_NODE_MISMATCH", "candidate is not valid at the referenced route occurrence", map[string]any{"candidateId": reference.CandidateID, "occurrenceId": reference.OccurrenceID, "selectionNodeId": selectionByID[reference.CandidateID].NodeID, "occurrenceNodeId": occurrenceNode})
		}
		byCandidate[reference.CandidateID] = append(byCandidate[reference.CandidateID], reference.OccurrenceID)
	}

	occurrences := append([]explorer.ExplorerCandidateOccurrenceV1(nil), document.CandidateOccurrences...)
	for index, candidateID := range document.CandidateIDs {
		selection, ok := selectionByID[candidateID]
		if !ok {
			return document, authoringSemantic("candidates", fmt.Sprintf("$.document.candidateIds[%d]", index), "STALE_CANDIDATE_ID", "candidate is not present in the catalog snapshot", map[string]any{"candidateId": candidateID})
		}
		if len(byCandidate[candidateID]) > 0 {
			continue
		}
		matches := make([]string, 0, len(occurrenceNodes))
		for _, occurrence := range routeOccurrences {
			if occurrence.NodeID == selection.NodeID {
				matches = append(matches, occurrence.OccurrenceID)
			}
		}
		if len(matches) == 0 {
			return document, authoringSemantic("candidates", fmt.Sprintf("$.document.candidateIds[%d]", index), "SELECTION_NODE_MISMATCH", "candidate does not belong to any route occurrence in this document", map[string]any{"candidateId": candidateID, "selectionNodeId": selection.NodeID, "routeOccurrences": matches})
		}
		if len(matches) > 1 {
			return document, authoringSemantic("candidates", "$.document.candidateOccurrences", "CANDIDATE_OCCURRENCE_REQUIRED", "every selected candidate must identify its exact route occurrence", map[string]any{"candidateId": candidateID, "validOccurrences": matches})
		}
		occurrences = append(occurrences, explorer.ExplorerCandidateOccurrenceV1{CandidateID: candidateID, OccurrenceID: matches[0]})
		byCandidate[candidateID] = append(byCandidate[candidateID], matches[0])
	}

	represented := make(map[string]bool, len(occurrences))
	for _, reference := range occurrences {
		represented[reference.CandidateID] = true
	}
	candidateIDs := make([]string, 0, len(represented))
	for _, candidateID := range document.CandidateIDs {
		if represented[candidateID] {
			candidateIDs = append(candidateIDs, candidateID)
		}
	}
	document.CandidateIDs = candidateIDs
	document.CandidateOccurrences = occurrences
	return document, nil
}

func withDocumentPath(err error, path string) error {
	var authoringErr *explorer.AuthoringError
	if !errors.As(err, &authoringErr) || authoringErr == nil {
		return err
	}
	if authoringErr.Diagnostic.JSONPath == "" || strings.HasPrefix(authoringErr.Diagnostic.JSONPath, "$.documents[") {
		return err
	}
	authoringErr.Diagnostic.JSONPath = path + authoringErr.Diagnostic.JSONPath[len("$.document"):]
	return err
}

// compileExplorerAuthoringV1 remains as the internal compiler capability seam;
// all behavior is owned by ResolveAuthoringBundle.
func compileExplorerAuthoringV1(ctx context.Context, recipeEngine *engine.Engine, catalogReader explorerV2CatalogReader, request ExplorerAuthoringV1CompileRequest) (ExplorerAuthoringV1CompileResult, error) {
	return ResolveAuthoringBundle(ctx, recipeEngine, catalogReader, request)
}

type authoringRouteStep struct {
	Occurrence explorer.ExplorerRouteOccurrenceV1
	Edge       explorer.CatalogEdge
	NodeID     string
}

func resolveAuthoringRoute(document explorer.ExplorerBuilderDocumentV1, catalog explorer.Catalog) ([]authoringRouteStep, error) {
	nodes := map[string]explorer.CatalogNode{}
	for _, node := range catalog.Nodes {
		nodes[node.ID] = node
	}
	if _, ok := nodes[document.BaseNodeID]; !ok {
		return nil, authoringSemantic("route", "$.document.baseNodeId", "STALE_BASE_NODE", "base node is not present in the catalog snapshot", map[string]any{"nodeId": document.BaseNodeID})
	}
	if _, ok := nodes[document.RowNodeID]; !ok {
		return nil, authoringSemantic("route", "$.document.rowNodeId", "STALE_ROW_NODE", "row node is not present in the catalog snapshot", map[string]any{"nodeId": document.RowNodeID})
	}
	edges := map[string]explorer.CatalogEdge{}
	for _, edge := range catalog.Edges {
		edges[edge.ID] = edge
	}
	if len(document.RouteEdgeIDs) == 0 {
		if document.BaseNodeID != document.RowNodeID {
			return nil, authoringSemantic("route", "$.document.rowNodeId", "INVALID_ZERO_HOP_ROUTE", "a zero-hop route must end at its base node", nil)
		}
		if len(document.RouteOccurrences) > 1 {
			return nil, authoringSemantic("route", "$.document.routeOccurrences", "INVALID_ZERO_HOP_ROUTE", "a zero-hop route has no child occurrences", nil)
		}
		if len(document.RouteOccurrences) == 1 && (document.RouteOccurrences[0].Index != 0 || document.RouteOccurrences[0].NodeID != document.BaseNodeID) {
			return nil, authoringSemantic("route", "$.document.routeOccurrences[0]", "INVALID_ZERO_HOP_ROUTE", "the optional zero-hop occurrence must reference the base node at index zero", nil)
		}
		return nil, nil
	}
	for index, occurrence := range document.RouteOccurrences {
		if _, ok := nodes[occurrence.NodeID]; !ok {
			return nil, authoringSemantic("route", fmt.Sprintf("$.document.routeOccurrences[%d].nodeId", index), "STALE_ROUTE_NODE", "route occurrence node is not present in the catalog snapshot", map[string]any{"nodeId": occurrence.NodeID})
		}
	}
	if len(document.RouteOccurrences) != len(document.RouteEdgeIDs) && len(document.RouteOccurrences) != len(document.RouteEdgeIDs)+1 {
		return nil, authoringSemantic("route", "$.document.routeOccurrences", "ROUTE_OCCURRENCES_REQUIRED", "route occurrence references must contain one target per edge, or include the base occurrence", map[string]any{"edges": len(document.RouteEdgeIDs), "occurrences": len(document.RouteOccurrences)})
	}
	if len(document.RouteOccurrences) == len(document.RouteEdgeIDs)+1 && document.RouteOccurrences[0].NodeID != document.BaseNodeID {
		return nil, authoringSemantic("route", "$.document.routeOccurrences[0].nodeId", "INVALID_ROUTE_ORIGIN", "the first route occurrence must reference baseNodeId", nil)
	}
	current := document.BaseNodeID
	steps := make([]authoringRouteStep, 0, len(document.RouteEdgeIDs))
	for index, edgeID := range document.RouteEdgeIDs {
		edge, ok := edges[edgeID]
		if !ok {
			return nil, authoringSemantic("route", fmt.Sprintf("$.document.routeEdgeIds[%d]", index), "STALE_ROUTE_EDGE", "route edge is not present in the catalog snapshot", map[string]any{"edgeId": edgeID})
		}
		if edge.FromNodeID != current {
			return nil, authoringSemantic("route", fmt.Sprintf("$.document.routeEdgeIds[%d]", index), "INVALID_ROUTE_CONTINUITY", "ordered route edges do not form a continuous path", map[string]any{"edgeId": edgeID, "currentNodeId": current})
		}
		next := edge.ToNodeID
		occurrenceIndex := index
		if len(document.RouteOccurrences) == len(document.RouteEdgeIDs)+1 {
			occurrenceIndex++
		}
		occurrence := document.RouteOccurrences[occurrenceIndex]
		if occurrence.Index != occurrenceIndex {
			return nil, authoringSemantic("route", fmt.Sprintf("$.document.routeOccurrences[%d].index", occurrenceIndex), "INVALID_ROUTE_OCCURRENCE_ORDER", "route occurrence index does not match ordered route edges", nil)
		}
		if occurrence.NodeID != next {
			return nil, authoringSemantic("route", fmt.Sprintf("$.document.routeOccurrences[%d].nodeId", occurrenceIndex), "INVALID_ROUTE_OCCURRENCE_NODE", "route occurrence does not name the edge target", map[string]any{"expectedNodeId": next, "receivedNodeId": occurrence.NodeID})
		}
		if occurrence.IncomingEdgeID != "" && occurrence.IncomingEdgeID != edgeID {
			return nil, authoringSemantic("route", fmt.Sprintf("$.document.routeOccurrences[%d].incomingEdgeId", occurrenceIndex), "INVALID_ROUTE_OCCURRENCE_EDGE", "route occurrence does not reference its incoming edge", nil)
		}
		steps = append(steps, authoringRouteStep{Occurrence: occurrence, Edge: edge, NodeID: next})
		current = next
	}
	if current != document.RowNodeID {
		return nil, authoringSemantic("route", "$.document.rowNodeId", "INVALID_ROUTE_TERMINAL_NODE", "route does not terminate at rowNodeId", map[string]any{"terminalNodeId": current, "rowNodeId": document.RowNodeID})
	}
	return steps, nil
}

type authoringAssignment struct {
	CandidateID, OccurrenceID, ProjectionMode string
	Selection                                 explorer.CatalogSelection
}

func resolveAuthoringCandidates(document explorer.ExplorerBuilderDocumentV1, catalog explorer.Catalog, route []authoringRouteStep) ([]authoringAssignment, error) {
	selections := catalog.Selections
	pathNodes := map[string][]string{"base": {document.BaseNodeID}}
	for _, step := range route {
		pathNodes[step.Occurrence.ID] = append(pathNodes[step.Occurrence.ID], step.NodeID)
	}
	byCandidateOccurrence := map[string][]string{}
	projectionByPair := map[string]string{}
	seenAssignment := map[string]bool{}
	for _, reference := range document.CandidateOccurrences {
		key := reference.CandidateID + "\x00" + reference.OccurrenceID
		if seenAssignment[key] {
			return nil, authoringSemantic("candidates", "$.document.candidateOccurrences", "DUPLICATE_CANDIDATE_OCCURRENCE", "a candidate occurrence may be listed only once", nil)
		}
		seenAssignment[key] = true
		byCandidateOccurrence[reference.CandidateID] = append(byCandidateOccurrence[reference.CandidateID], reference.OccurrenceID)
		projectionByPair[key] = strings.TrimSpace(reference.ProjectionMode)
	}
	assignments := make([]authoringAssignment, 0, len(document.CandidateIDs))
	for i, candidateID := range document.CandidateIDs {
		selection, ok := selections[candidateID]
		if !ok {
			return nil, authoringSemantic("candidates", fmt.Sprintf("$.document.candidateIds[%d]", i), "STALE_CANDIDATE_ID", "candidate is not present in the catalog snapshot", map[string]any{"candidateId": candidateID})
		}
		occurrenceIDs := byCandidateOccurrence[candidateID]
		matches := make([]string, 0, 2)
		for id, nodeIDs := range pathNodes {
			for _, nodeID := range nodeIDs {
				if nodeID == selection.NodeID {
					matches = append(matches, id)
					break
				}
			}
		}
		if len(matches) == 0 {
			return nil, authoringSemantic("candidates", fmt.Sprintf("$.document.candidateIds[%d]", i), "SELECTION_NODE_MISMATCH", "candidate does not belong to the base node or any route occurrence in this document", map[string]any{"candidateId": candidateID, "selectionNodeId": selection.NodeID, "routeNodeIds": pathNodes})
		}
		if len(occurrenceIDs) == 0 {
			return nil, authoringSemantic("candidates", fmt.Sprintf("$.document.candidateOccurrences"), "CANDIDATE_OCCURRENCE_REQUIRED", "every selected candidate must identify its exact route occurrence", map[string]any{"candidateId": candidateID, "validOccurrences": matches})
		}
		for _, occurrenceID := range occurrenceIDs {
			if occurrenceID != "base" && !containsRouteOccurrence(route, occurrenceID) {
				return nil, authoringSemantic("candidates", "$.document.candidateOccurrences", "STALE_ROUTE_OCCURRENCE", "candidate references a stale route occurrence", map[string]any{"occurrenceId": occurrenceID})
			}
			var occurrenceNode string
			if occurrenceID == "base" {
				occurrenceNode = document.BaseNodeID
			} else {
				for _, step := range route {
					if step.Occurrence.ID == occurrenceID {
						occurrenceNode = step.NodeID
						break
					}
				}
			}
			if occurrenceNode != selection.NodeID {
				return nil, authoringSemantic("candidates", "$.document.candidateOccurrences", "SELECTION_NODE_MISMATCH", "candidate is not valid at the referenced route occurrence", map[string]any{"candidateId": candidateID, "occurrenceId": occurrenceID})
			}
			projectionMode := projectionByPair[candidateID+"\x00"+occurrenceID]
			if projectionMode == "" {
				projectionMode = selection.DefaultProjectionMode
			}
			if projectionMode != "" && !containsString(selection.ProjectionModes, projectionMode) {
				return nil, authoringSemantic("candidates", "$.document.candidateOccurrences", "UNSUPPORTED_PROJECTION_MODE", "candidate projection mode is not advertised by the catalog snapshot", map[string]any{"candidateId": candidateID, "projectionMode": projectionMode, "supported": selection.ProjectionModes})
			}
			assignments = append(assignments, authoringAssignment{CandidateID: candidateID, OccurrenceID: occurrenceID, ProjectionMode: projectionMode, Selection: selection})
		}
	}
	return assignments, nil
}

func containsRouteOccurrence(route []authoringRouteStep, id string) bool {
	for _, step := range route {
		if step.Occurrence.ID == id {
			return true
		}
	}
	return false
}

func authoringEmissions(document explorer.ExplorerBuilderDocumentV1, _ explorer.Catalog, assignments []authoringAssignment) ([]explorer.EmittedColumn, []explorer.IdentityMapping) {
	byCandidateOccurrence := map[string][]string{}
	emitted := make([]explorer.EmittedColumn, 0, len(assignments))
	for _, assignment := range assignments {
		emissionID := explorer.OpaqueID("em_", document.Output.ID+"\x00"+assignment.OccurrenceID+"\x00"+assignment.CandidateID)
		// PublicColumn must be the logical field name emitted by the native
		// recipe. Publication may add the root-resource prefix, but the runtime
		// projection can resolve that qualification only when the unqualified
		// suffix is identical. Deriving this name from EmissionID instead gives
		// it a different hash and leaves previews/runtime tables with column
		// metadata that cannot address any returned row value.
		publicColumn := generatedFieldName(assignment.CandidateID, assignment.OccurrenceID)
		column := explorer.EmittedColumn{EmissionID: emissionID, OutputID: document.Output.ID, NodeID: assignment.Selection.NodeID, SelectionID: assignment.CandidateID, CandidateID: assignment.CandidateID, OccurrenceID: assignment.OccurrenceID, PublicColumn: publicColumn, Filterable: assignment.Selection.Filterable, Chartable: assignment.Selection.Chartable}
		emitted = append(emitted, column)
		key := assignment.CandidateID + "\x00" + assignment.OccurrenceID
		byCandidateOccurrence[key] = append(byCandidateOccurrence[key], emissionID)
	}
	mappings := make([]explorer.IdentityMapping, 0, len(byCandidateOccurrence))
	for _, assignment := range assignments {
		key := assignment.CandidateID + "\x00" + assignment.OccurrenceID
		if ids := byCandidateOccurrence[key]; len(ids) > 0 {
			mappings = append(mappings, explorer.IdentityMapping{CandidateID: assignment.CandidateID, OccurrenceID: assignment.OccurrenceID, EmissionIDs: append([]string(nil), ids...)})
			delete(byCandidateOccurrence, key)
		}
	}
	return emitted, mappings
}

func resolvedAuthoringBinding(document explorer.ExplorerBuilderDocumentV1, catalog explorer.Catalog, route []authoringRouteStep, assignments []authoringAssignment, emitted []explorer.EmittedColumn) explorer.ExplorerResolvedBindingV1 {
	base, _ := catalogNode(catalog, document.BaseNodeID)
	row, _ := catalogNode(catalog, document.RowNodeID)
	rowGrain, _ := spec.InferRowGrain(base.ResourceType)
	kind := "ROUTE"
	if len(route) == 0 {
		kind = "ZERO_HOP"
	}
	result := explorer.ExplorerResolvedBindingV1{
		OutputID: document.Output.ID, OutputTitle: document.Output.Title,
		BaseNodeID: document.BaseNodeID, BaseResourceType: base.ResourceType,
		RowNodeID: document.RowNodeID, RowResourceType: row.ResourceType,
		RowGrain: string(rowGrain), RouteKind: kind,
		RouteOccurrences:   []explorer.ExplorerResolvedOccurrenceV1{{OccurrenceID: "base", Index: 0, NodeID: document.BaseNodeID, ResourceType: base.ResourceType}},
		CandidateEmissions: []explorer.ExplorerCandidateEmissionV1{},
	}
	for i, step := range route {
		result.RouteOccurrences = append(result.RouteOccurrences, explorer.ExplorerResolvedOccurrenceV1{OccurrenceID: step.Occurrence.ID, Index: i + 1, NodeID: step.NodeID, ResourceType: nodeResourceType(catalog, step.NodeID), IncomingEdgeID: step.Edge.ID})
	}
	emissionByKey := map[string]explorer.EmittedColumn{}
	for _, column := range emitted {
		emissionByKey[column.CandidateID+"\x00"+column.OccurrenceID] = column
	}
	for _, assignment := range assignments {
		column := emissionByKey[assignment.CandidateID+"\x00"+assignment.OccurrenceID]
		label := assignment.Selection.FieldRef
		if presentation, ok := document.Presentation[column.EmissionID]; ok && strings.TrimSpace(presentation.Label) != "" {
			label = presentation.Label
		}
		result.CandidateEmissions = append(result.CandidateEmissions, explorer.ExplorerCandidateEmissionV1{CandidateID: assignment.CandidateID, OccurrenceID: assignment.OccurrenceID, EmissionID: column.EmissionID, PublicColumn: column.PublicColumn, Label: label, LogicalType: firstNonEmpty(column.LogicalType, assignment.Selection.LogicalType), Filterable: column.Filterable, Chartable: column.Chartable})
	}
	return result
}

func validateAuthoringPresentation(document explorer.ExplorerBuilderDocumentV1, emitted []explorer.EmittedColumn) error {
	known := map[string]explorer.EmittedColumn{}
	for _, column := range emitted {
		known[column.EmissionID] = column
	}
	for emissionID := range document.Presentation {
		column, ok := known[emissionID]
		if !ok {
			return authoringSemantic("presentation", "$.document.presentation."+emissionID, "STALE_EMISSION", "presentation references an emission that is not in this compilation", map[string]any{"emissionId": emissionID})
		}
		binding := document.Presentation[emissionID]
		if binding.Filter != nil && !column.Filterable {
			return authoringSemantic("presentation", "$.document.presentation."+emissionID+".filter", "UNSUPPORTED_FILTER_PRESENTATION", "the selected emission is not filterable", map[string]any{"emissionId": emissionID})
		}
		if binding.Chart != nil && !column.Chartable {
			return authoringSemantic("presentation", "$.document.presentation."+emissionID+".chart", "UNSUPPORTED_CHART_PRESENTATION", "the selected emission is not chartable", map[string]any{"emissionId": emissionID})
		}
	}
	return nil
}

func lowerAuthoringDocument(project, explorerID string, document explorer.ExplorerBuilderDocumentV1, catalog explorer.Catalog, route []authoringRouteStep, assignments []authoringAssignment) (recipe.Bundle, error) {
	base, ok := catalogNode(catalog, document.BaseNodeID)
	if !ok {
		return recipe.Bundle{}, authoringSemantic("route", "$.document.baseNodeId", "STALE_BASE_NODE", "base node is not present in the catalog snapshot", nil)
	}
	outputID := document.Output.ID
	if !authoringIDPattern.MatchString(outputID) {
		return recipe.Bundle{}, authoringSemantic("intent", "$.document.output.id", "INVALID_OUTPUT_ID", "output.id must be a stable lower-case identifier", nil)
	}
	rootResourceType, ok := fhirschema.CanonicalResourceType(base.ResourceType)
	if !ok {
		return recipe.Bundle{}, authoringSemantic("lower", "$.document.baseNodeId", "UNSUPPORTED_ROW_GRAIN", "the selected base resource has no supported row grain", map[string]any{"resourceType": base.ResourceType, "nodeId": base.ID})
	}
	rowGrain, ok := spec.InferRowGrain(rootResourceType)
	if !ok {
		return recipe.Bundle{}, authoringSemantic("lower", "$.document.baseNodeId", "UNSUPPORTED_ROW_GRAIN", "the selected base resource has no supported row grain", map[string]any{"resourceType": base.ResourceType, "nodeId": base.ID})
	}
	native := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "explorer_" + safeAuthoringName(project) + "_" + safeAuthoringName(explorerID), TranslationVersion: "authoring-v1", Outputs: []recipe.Output{{Name: outputID, RootResourceType: rootResourceType, RowGrain: string(rowGrain)}}}
	byOccurrence := map[string][]authoringAssignment{}
	for _, assignment := range assignments {
		byOccurrence[assignment.OccurrenceID] = append(byOccurrence[assignment.OccurrenceID], assignment)
	}
	appendFields := func(fields *[]recipe.Field, occurrenceID, alias string) {
		for _, assignment := range byOccurrence[occurrenceID] {
			fieldName := generatedFieldName(assignment.CandidateID, occurrenceID)
			selectPath := strings.TrimSpace(assignment.Selection.Select)
			selectPath = strings.TrimPrefix(selectPath, "root.")
			*fields = append(*fields, recipe.Field{Name: fieldName, FieldRef: assignment.Selection.FieldRef, Expr: recipe.Expression{Select: alias + "." + selectPath}, ValueMode: authoringValueMode(assignment.ProjectionMode)})
		}
	}
	appendFields(&native.Outputs[0].Fields, "base", "root")
	var appendTraversals func(*[]recipe.Traversal, int, string)
	appendTraversals = func(target *[]recipe.Traversal, index int, parentAlias string) {
		if index >= len(route) {
			return
		}
		step := route[index]
		alias := "route_" + strconv.Itoa(index)
		toResourceType, _ := fhirschema.CanonicalResourceType(nodeResourceType(catalog, step.NodeID))
		traversal := recipe.Traversal{Name: step.Edge.Label, Alias: alias, ToResourceType: toResourceType, MatchMode: recipe.MatchOptional}
		appendFields(&traversal.Fields, step.Occurrence.ID, alias)
		appendTraversals(&traversal.Traversals, index+1, alias)
		*target = append(*target, traversal)
	}
	appendTraversals(&native.Outputs[0].Traversals, 0, "root")
	return native, nil
}

func authoringValueMode(projectionMode string) recipe.ValueMode {
	switch strings.ToUpper(strings.TrimSpace(projectionMode)) {
	case "FIRST":
		return recipe.ValueModeFirst
	case "ARRAY":
		return recipe.ValueModeAll
	case "DISTINCT_ARRAY":
		return recipe.ValueModeDistinct
	default:
		return recipe.ValueModeAuto
	}
}

func catalogNode(catalog explorer.Catalog, id string) (explorer.CatalogNode, bool) {
	for _, node := range catalog.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return explorer.CatalogNode{}, false
}
func nodeResourceType(catalog explorer.Catalog, id string) string {
	node, _ := catalogNode(catalog, id)
	return node.ResourceType
}
func safeAuthoringName(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	value = strings.Trim(b.String(), "_")
	if value == "" {
		return "x"
	}
	return value
}
func generatedFieldName(candidateID, occurrenceID string) string {
	sum := sha256.Sum256([]byte(candidateID + "\x00" + occurrenceID))
	return "c_" + hex.EncodeToString(sum[:])
}
func outputFingerprint(bundle recipe.Bundle, project, generation string) string {
	raw, _ := json.Marshal(struct {
		Bundle              recipe.Bundle
		Project, Generation string
	}{bundle, project, generation})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func authoringCompiledConfig(bundle explorer.ExplorerAuthoringBundleV1, native recipe.Bundle, emitted []explorer.EmittedColumn) ([]byte, error) {
	documents := bundle.AuthoringDocuments()
	documentsByOutput := make(map[string]explorer.ExplorerBuilderDocumentV1, len(documents))
	emittedByOutput := make(map[string][]explorer.EmittedColumn, len(documents))
	for _, document := range documents {
		documentsByOutput[document.Output.ID] = document
	}
	for _, column := range emitted {
		emittedByOutput[column.OutputID] = append(emittedByOutput[column.OutputID], column)
	}
	tabs := bundle.AuthoringTabs()
	sort.SliceStable(tabs, func(i, j int) bool { return tabs[i].Order < tabs[j].Order })
	views := make([]explorer.ConfigView, 0, len(tabs))
	for _, tab := range tabs {
		if tab.Visible != nil && !*tab.Visible {
			continue
		}
		document, ok := documentsByOutput[tab.OutputID]
		if !ok {
			return nil, authoringSemantic("presentation", "$.tabs", "TAB_OUTPUT_MISSING", "tab references an output that is not in the authoring bundle", map[string]any{"outputId": tab.OutputID})
		}
		columns := make([]explorer.ConfigColumn, 0, len(emittedByOutput[tab.OutputID]))
		filters := make([]explorer.ConfigFilter, 0)
		charts := make([]explorer.ConfigChart, 0)
		orderByColumn := map[string]int{}
		for _, column := range emittedByOutput[tab.OutputID] {
			binding := document.Presentation[column.EmissionID]
			visible := true
			if binding.Visible != nil {
				visible = *binding.Visible
			}
			if binding.Order != nil {
				orderByColumn[column.PublicColumn] = *binding.Order
			}
			label := firstNonEmpty(binding.Label, column.PublicColumn)
			columns = append(columns, explorer.ConfigColumn{Column: column.PublicColumn, Label: label, Visible: visible})
			if binding.Filter != nil {
				filters = append(filters, explorer.ConfigFilter{Column: column.PublicColumn, Label: binding.Filter.Label})
			}
			if binding.Chart != nil {
				charts = append(charts, explorer.ConfigChart{Column: column.PublicColumn, Type: binding.Chart.Type, Title: binding.Chart.Title})
			}
		}
		sort.SliceStable(columns, func(i, j int) bool {
			left, leftOK := orderByColumn[columns[i].Column]
			right, rightOK := orderByColumn[columns[j].Column]
			if leftOK && rightOK && left != right {
				return left < right
			}
			if leftOK != rightOK {
				return leftOK
			}
			return columns[i].Column < columns[j].Column
		})
		sort.Slice(filters, func(i, j int) bool { return filters[i].Column < filters[j].Column })
		sort.Slice(charts, func(i, j int) bool { return charts[i].Column < charts[j].Column })
		views = append(views, explorer.ConfigView{ID: tab.ID, Title: tab.Title, Output: tab.OutputID, Table: explorer.ConfigTable{Columns: columns}, Filters: filters, Charts: charts})
	}
	config := explorer.ConfigV2{APIVersion: explorer.ConfigV2APIVersion, Kind: "ExplorerConfig", Project: bundle.Project, Explorer: explorer.ConfigExplorer{ID: bundle.ExplorerID, Title: firstNonEmpty(bundle.Title, documents[0].Output.Title, documents[0].Output.ID), Management: explorer.ConfigManagementForID(bundle.ExplorerID)}, Recipe: mustJSON(native), Views: views}
	return json.Marshal(config)
}
func mustJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "Explorer"
}

func authoringSemantic(stage, path, code, message string, details map[string]any) error {
	return &explorer.AuthoringError{Status: 422, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, JSONPath: path, Message: message, Details: details}}
}
func explorerConflict(stage, code, message string, details map[string]any) error {
	return &explorer.AuthoringError{Status: 409, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, Message: message, Details: details}}
}
func explorerUnavailable(stage, code, message string) error {
	return &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, Message: message}}
}
func withRequestAuthoringError(err error, requestID string) error {
	var authoringErr *explorer.AuthoringError
	if errors.As(err, &authoringErr) {
		authoringErr.Diagnostic.RequestID = requestID
	}
	return err
}
func authoringSemanticFromRecipe(err error) error {
	var validation *recipe.ValidationError
	if errors.As(err, &validation) {
		return authoringSemantic("recipe", validation.Path, "INVALID_COMPILED_RECIPE", validation.Message, map[string]any{"recipeCode": validation.Code})
	}
	return authoringSemanticFromCompile(err)
}
func authoringSemanticFromCompile(err error) error {
	message := err.Error()
	code := "INVALID_AUTHORING_INTENT"
	if strings.Contains(strings.ToLower(message), "traversal") {
		code = "UNSUPPORTED_NESTED_TRAVERSAL"
	}
	return authoringSemantic("compile", "$", code, message, nil)
}
