package construction

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframecompiler "github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/rowdef"
)

func failure(class ports.ErrorClass, stage, code, message string, cause error) error {
	return support.Failure(class, stage, code, message, cause)
}

func failureDetails(class ports.ErrorClass, stage, code, message string, details map[string]any, cause error) error {
	return support.FailureDetails(class, stage, code, message, details, cause)
}

func malformed(stage, message string, cause error) error {
	return support.Malformed(stage, message, cause)
}

func unavailable(stage, code, message string, cause error) error {
	return support.Unavailable(stage, code, message, cause)
}

func conflict(stage, code, message string, details map[string]any, cause error) error {
	return support.Conflict(stage, code, message, details, cause)
}

func forbidden(stage, message string, cause error) error {
	return support.Forbidden(stage, message, cause)
}

func notFound(stage, code, message string, cause error) error {
	return support.NotFound(stage, code, message, cause)
}

func unprocessable(stage, code, message string, cause error) error {
	return support.Unprocessable(stage, code, message, cause)
}

func internal(stage, code, message string, cause error) error {
	return support.Internal(stage, code, message, cause)
}

func persistenceFailure(stage, message string, err error) error {
	return support.PersistenceFailure(stage, message, err)
}

func validateAuthorizedReadScope(scope authscope.ReadScope, expectedDigest string) error {
	return support.ValidateAuthorizedReadScope(scope, expectedDigest)
}

func requireExactIdentity(value, name string) error {
	return support.RequireExactIdentity(value, name)
}

func firstNonEmpty(values ...string) string {
	return support.FirstNonEmpty(values...)
}

func previewWorkspace(ctx context.Context, store *explorer.Service, owner *explorer.Explorer) (authoringv2.Workspace, error) {
	return support.PreviewWorkspace(ctx, store, owner)
}

func verifyPreviewReceiptIntent(receipt *explorer.CompilationReceipt, workspace authoringv2.Workspace) error {
	return support.VerifyPreviewReceiptIntent(receipt, workspace)
}

func applyAuthorizedScope(bindings *recipe.RuntimeBindings, authorized ports.AuthorizedCapability, includeAuthResourcePath bool) {
	support.ApplyAuthorizedScope(bindings, authorized, includeAuthResourcePath)
}

func validateReceiptOutputContract(receipt *explorer.CompilationReceipt, outputID string) error {
	return support.ValidateReceiptOutputContract(receipt, outputID)
}

func receiptHasOutput(bundle recipe.Bundle, id string) bool {
	return support.ReceiptHasOutput(bundle, id)
}

func capabilityFailure(stage, code, message string, err error) error {
	return support.CapabilityFailure(stage, code, message, err)
}

func receiptCapabilityFailure(stage, message string, err error) error {
	return support.ReceiptCapabilityFailure(stage, message, err)
}

func resolveCapabilityRouteEdge(snapshot capability.Snapshot, parentNodeID string, parent, child authoringv2.RouteNode) (capability.Edge, error) {
	return support.ResolveCapabilityRouteEdge(snapshot, parentNodeID, parent, child)
}

func reauthorizeConstructionRoute(snapshot capability.Snapshot, rootResourceType, targetNodeID string, route []capability.ConstructionRouteStep) ([]capability.ConstructionRouteStep, error) {
	return support.ReauthorizeConstructionRoute(snapshot, rootResourceType, targetNodeID, route)
}

func reauthorizeConstructionRouteFromAnchor(snapshot capability.Snapshot, anchorNodeID, targetNodeID string, route []capability.ConstructionRouteStep) ([]capability.ConstructionRouteStep, error) {
	return support.ReauthorizeConstructionRouteFromAnchor(snapshot, anchorNodeID, targetNodeID, route)
}

func routePathToOccurrence(root authoringv2.RouteNode, occurrenceID string) ([]authoringv2.RouteNode, bool) {
	return support.RoutePathToOccurrence(root, occurrenceID)
}

func fhirIdentifierPath(path string) bool { return rowdef.FHIRIdentifierPath(path) }

func resolveRowChoiceOccurrence(snapshot capability.Snapshot, rootResourceType string, authoredRoot authoringv2.RouteNode, occurrenceID, expectedNodeID string) (capability.RowChoiceOccurrence, error) {
	return rowdef.ResolveRowChoiceOccurrence(snapshot, rootResourceType, authoredRoot, occurrenceID, expectedNodeID)
}

func validateResolvedRowChoice(route authoringv2.RouteNode, resolved rowdef.ResolvedRowChoice, expected rowdef.RowChoiceKind) error {
	return rowdef.ValidateResolvedRowChoice(route, resolved, expected)
}

func documentDigest(document authoringv2.Document) (string, error) {
	return rowdef.DocumentDigest(document)
}

func findSemanticOutput(workspace authoringv2.Workspace, outputID string) *authoringv2.Document {
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == outputID {
			return &workspace.Documents[index]
		}
	}
	return nil
}

func workspaceValidationCode(err error) string {
	message := err.Error()
	for _, code := range []string{"DUPLICATE_OUTPUT_ID", "DUPLICATE_TAB_ID", "INVALID_TAB_OUTPUT_MAPPING", "INVALID_TAB_ORDER", "ROW_ROOT_NOT_ELIGIBLE", "UNSUPPORTED_FILTER", "UNSUPPORTED_CHART", "NO_VISIBLE_COLUMNS", "UNACKNOWLEDGED_RELATED_FIRST"} {
		if strings.Contains(message, code) {
			return code
		}
	}
	switch {
	case strings.Contains(message, "rootNodeId"):
		return "INVALID_ROOT_NODE"
	case strings.Contains(message, "route") || strings.Contains(message, "edge"):
		return "INVALID_ROUTE"
	case strings.Contains(message, "occurrence"):
		return "INVALID_OCCURRENCE"
	case strings.Contains(message, "projection mode"):
		return "INVALID_PROJECTION_MODE"
	case strings.Contains(message, "duplicate selection"):
		return "DUPLICATE_SELECTION"
	default:
		return "INVALID_AUTHORING_INTENT"
	}
}

func schemaFieldCandidateID(snapshotToken, nodeID, path string) string {
	identity, _ := json.Marshal([3]string{snapshotToken, nodeID, path})
	return "schema_field." + base64.RawURLEncoding.EncodeToString(identity)
}

func resolveSchemaFieldCandidate(snapshot capability.Snapshot, candidateID string) (capability.Candidate, bool) {
	const prefix = "schema_field."
	if !strings.HasPrefix(candidateID, prefix) || len(candidateID) > 4096 {
		return capability.Candidate{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(candidateID, prefix))
	if err != nil {
		return capability.Candidate{}, false
	}
	var identity [3]string
	if json.Unmarshal(payload, &identity) != nil || identity[0] != snapshot.Token ||
		candidateID != schemaFieldCandidateID(identity[0], identity[1], identity[2]) {
		return capability.Candidate{}, false
	}
	node, ok := snapshot.Node(identity[1])
	if !ok || !node.Populated || !validSHA256Digest(snapshot.Identity.SchemaDigest) {
		return capability.Candidate{}, false
	}
	for _, field := range dataframecompiler.SchemaFieldDescriptors(node.ResourceType) {
		if field.Path != identity[2] {
			continue
		}
		return capability.Candidate{
			ID: candidateID, NodeID: node.ID, ResourceType: node.ResourceType,
			FieldPath: field.Path, Label: node.ResourceType + "." + field.Path,
			LogicalType: field.PrimitiveType, Cardinality: field.Cardinality,
		}, true
	}
	return capability.Candidate{}, false
}

func validSHA256Digest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
