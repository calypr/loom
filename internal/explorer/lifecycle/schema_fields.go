package lifecycle

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/projectid"
)

const (
	schemaFieldDefaultLimit = 20
	schemaFieldMaxLimit     = 50
	schemaFieldMaxQuery     = 256
	schemaFieldMaxCursor    = 4096
)

// SearchSchemaFieldsRequest lists generated primitive paths for one populated
// node in the current authorized capability snapshot.
type SearchSchemaFieldsRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	NodeID        string
	Query         string
	Cursor        string
	Limit         int
}

// SchemaFieldOption carries generated-schema metadata and a proved default
// projection choice, without observed counts, examples, or suggestions.
type SchemaFieldOption struct {
	Origin             string                         `json:"origin"`
	NodeID             string                         `json:"nodeId"`
	ResourceType       string                         `json:"resourceType"`
	Path               string                         `json:"path"`
	PrimitiveType      string                         `json:"primitiveType"`
	Cardinality        string                         `json:"cardinality"`
	RepeatedPaths      []string                       `json:"repeatedPaths,omitempty"`
	ConstructionChoice *capability.ConstructionChoice `json:"constructionChoice,omitempty"`
}

// SchemaFieldSearchResponse pages generated paths separately from the
// evidence-backed capability snapshot. SchemaDigest is copied from the active
// schema identity already pinned into that authorized snapshot.
type SchemaFieldSearchResponse struct {
	SnapshotToken string              `json:"snapshotToken"`
	SchemaDigest  string              `json:"schemaDigest"`
	NodeID        string              `json:"nodeId"`
	ResourceType  string              `json:"resourceType"`
	Query         string              `json:"query"`
	Fields        []SchemaFieldOption `json:"fields"`
	Complete      bool                `json:"complete"`
	Truncated     bool                `json:"truncated"`
	NextCursor    string              `json:"nextCursor,omitempty"`
}

type schemaFieldCursor struct {
	Version                  int    `json:"version"`
	Project                  string `json:"project"`
	ExplorerID               string `json:"explorerId"`
	SnapshotToken            string `json:"snapshotToken"`
	Generation               string `json:"generation"`
	AuthorizationScopeDigest string `json:"authorizationScopeDigest"`
	SchemaDigest             string `json:"schemaDigest"`
	NodeID                   string `json:"nodeId"`
	ResourceType             string `json:"resourceType"`
	Query                    string `json:"query"`
	Limit                    int    `json:"limit"`
	LastPath                 string `json:"lastPath"`
}

// SearchSchemaFields returns a stable depth-first, then lexicographic page of generated
// primitive paths. The path list is derived from generated FHIR schema
// metadata, then bound to the exact snapshot, populated node, generation,
// schema digest, authorization scope, query, and page size.
func (s *Service) SearchSchemaFields(ctx context.Context, request SearchSchemaFieldsRequest) (SchemaFieldSearchResponse, error) {
	result := SchemaFieldSearchResponse{
		SnapshotToken: request.SnapshotToken,
		NodeID:        request.NodeID,
		Fields:        []SchemaFieldOption{},
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" ||
		strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.NodeID) == "" ||
		strings.TrimSpace(request.Query) != request.Query || len(request.Query) > schemaFieldMaxQuery ||
		strings.TrimSpace(request.Cursor) != request.Cursor || len(request.Cursor) > schemaFieldMaxCursor ||
		request.Limit < 0 || request.Limit > schemaFieldMaxLimit {
		return result, malformed("schema-fields", "project, explorer, snapshotToken, nodeId, and a page limit of at most 50 are required", nil)
	}
	if s.config.Capability.ForCompilation == nil {
		return result, unavailable("schema-fields", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	limit := request.Limit
	if limit == 0 {
		limit = schemaFieldDefaultLimit
	}
	project := projectid.Canonical(request.Project)
	query := strings.ToLower(strings.TrimSpace(request.Query))
	authorized, err := s.config.Capability.ForCompilation(ctx, project, request.SnapshotToken)
	if errors.Is(err, capability.ErrStaleSnapshot) || errors.Is(err, capability.ErrSnapshotUnavailable) {
		return result, conflict("schema-fields", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing generated fields", nil, err)
	}
	if err != nil {
		return result, err
	}
	snapshot := authorized.Snapshot
	if snapshot.ValidateToken(request.SnapshotToken) != nil ||
		projectid.Canonical(snapshot.Identity.Project) != project || snapshot.Identity.Generation == "" ||
		!validSHA256Digest(snapshot.Identity.SchemaDigest) {
		return result, conflict("schema-fields", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing generated fields", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("schema-fields", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	node, ok := snapshot.Node(request.NodeID)
	if !ok {
		return result, unprocessable("schema-fields", "UNKNOWN_CAPABILITY_NODE", "nodeId is not available in this authorized capability snapshot", nil)
	}
	if !node.Populated {
		return result, unprocessable("schema-fields", "NODE_NOT_POPULATED", "generated fields are available only for populated capability nodes", nil)
	}
	resourceType, ok := fhirschema.ConcreteResourceType(node.ResourceType)
	if !ok || resourceType != node.ResourceType {
		return result, unprocessable("schema-fields", "UNSUPPORTED_RESOURCE_TYPE", "node resource type is not a concrete generated FHIR resource", nil)
	}
	result.SchemaDigest = snapshot.Identity.SchemaDigest
	result.ResourceType = resourceType
	result.Query = query

	observedPaths := make(map[string]struct{})
	for _, candidate := range snapshot.Candidates {
		if candidate.NodeID == node.ID && candidate.ResourceType == resourceType && candidate.Observed &&
			len(candidate.ProjectionModes) > 0 && strings.TrimSpace(candidate.FieldPath) != "" {
			observedPaths[candidate.FieldPath] = struct{}{}
		}
	}
	descriptors := compilerprobe.SchemaFieldDescriptors(resourceType)
	matched := make([]compilerprobe.SchemaFieldDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if _, observed := observedPaths[descriptor.Path]; observed {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(descriptor.Path), query) {
			continue
		}
		matched = append(matched, descriptor)
	}
	sort.Slice(matched, func(i, j int) bool {
		left, right := strings.Count(matched[i].Path, "."), strings.Count(matched[j].Path, ".")
		if left != right {
			return left < right
		}
		return matched[i].Path < matched[j].Path
	})

	start := 0
	if request.Cursor != "" {
		cursor, decodeErr := decodeSchemaFieldCursor(request.Cursor)
		if decodeErr != nil || cursor.Project != project || cursor.ExplorerID != request.ExplorerID ||
			cursor.SnapshotToken != request.SnapshotToken || cursor.Generation != snapshot.Identity.Generation ||
			cursor.AuthorizationScopeDigest != snapshot.Identity.AuthorizationScopeDigest ||
			cursor.SchemaDigest != snapshot.Identity.SchemaDigest || cursor.NodeID != node.ID ||
			cursor.ResourceType != resourceType || cursor.Query != query || cursor.Limit != limit {
			return result, conflict("schema-fields", "STALE_SCHEMA_FIELD_CURSOR", "restart generated field search with an empty cursor", nil, decodeErr)
		}
		found := false
		for index := range matched {
			if matched[index].Path == cursor.LastPath {
				start, found = index+1, true
				break
			}
		}
		if !found {
			return result, conflict("schema-fields", "STALE_SCHEMA_FIELD_CURSOR", "restart generated field search with an empty cursor", nil, nil)
		}
	}
	end := start + limit
	if end > len(matched) {
		end = len(matched)
	}
	for _, descriptor := range matched[start:end] {
		candidate := capability.Candidate{
			ID:     schemaFieldCandidateID(snapshot.Token, node.ID, descriptor.Path),
			NodeID: node.ID, ResourceType: resourceType, FieldPath: descriptor.Path,
			Label: resourceType + "." + descriptor.Path, LogicalType: descriptor.PrimitiveType,
			Cardinality: descriptor.Cardinality,
		}
		proof, proofErr := compilerprobe.ProbeProjection(ctx, compilerprobe.CandidateRequest{
			Scope: constructionCompilerScope(authorized), RootResourceType: resourceType,
			ResourceType: resourceType, Selector: descriptor.Path,
			FieldRef: resourceType + "." + descriptor.Path,
		})
		if proofErr != nil || proof.Candidate == nil {
			return result, unprocessable("schema-fields", "SCHEMA_FIELD_COMPILE_FAILED", "generated field could not be compiled: "+descriptor.Path, proofErr)
		}
		candidate.ProjectionModes = []capability.ProjectionMode{capability.ProjectionMode(strings.ToUpper(string(proof.Candidate.ProjectionModes[0])))}
		choice, choiceErr := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, nil, candidate)
		if choiceErr != nil {
			return result, internal("schema-fields", "SCHEMA_FIELD_CHOICE_FAILED", "generated field choice could not be created", choiceErr)
		}
		result.Fields = append(result.Fields, SchemaFieldOption{
			Origin: "GENERATED_SCHEMA", NodeID: node.ID, ResourceType: resourceType,
			Path: descriptor.Path, PrimitiveType: descriptor.PrimitiveType,
			Cardinality: descriptor.Cardinality, RepeatedPaths: append([]string(nil), descriptor.RepeatedPaths...),
			ConstructionChoice: &choice,
		})
	}
	result.Complete = end == len(matched)
	result.Truncated = !result.Complete
	if result.Truncated {
		cursor, encodeErr := encodeSchemaFieldCursor(schemaFieldCursor{
			Version: 1, Project: project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
			Generation: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
			SchemaDigest: snapshot.Identity.SchemaDigest, NodeID: node.ID, ResourceType: resourceType,
			Query: query, Limit: limit, LastPath: matched[end-1].Path,
		})
		if encodeErr != nil {
			return SchemaFieldSearchResponse{}, internal("schema-fields", "SCHEMA_FIELD_CURSOR_FAILED", "generated field page cursor could not be created", encodeErr)
		}
		result.NextCursor = cursor
	}
	return result, nil
}

func validSHA256Digest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func encodeSchemaFieldCursor(cursor schemaFieldCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "sf1." + base64.RawURLEncoding.EncodeToString(raw) + "." + hex.EncodeToString(digest[:]), nil
}

func decodeSchemaFieldCursor(value string) (schemaFieldCursor, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != "sf1" || len(value) > schemaFieldMaxCursor {
		return schemaFieldCursor{}, fmt.Errorf("schema field cursor is invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return schemaFieldCursor{}, fmt.Errorf("schema field cursor is invalid")
	}
	provided, err := hex.DecodeString(parts[2])
	if err != nil || len(provided) != sha256.Size {
		return schemaFieldCursor{}, fmt.Errorf("schema field cursor is invalid")
	}
	digest := sha256.Sum256(raw)
	if subtle.ConstantTimeCompare(provided, digest[:]) != 1 {
		return schemaFieldCursor{}, fmt.Errorf("schema field cursor is invalid")
	}
	var cursor schemaFieldCursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.Version != 1 || cursor.Project == "" ||
		cursor.ExplorerID == "" || cursor.SnapshotToken == "" || cursor.Generation == "" ||
		cursor.AuthorizationScopeDigest == "" || !validSHA256Digest(cursor.SchemaDigest) ||
		cursor.NodeID == "" || cursor.ResourceType == "" || cursor.Limit < 1 || cursor.Limit > schemaFieldMaxLimit || cursor.LastPath == "" {
		return schemaFieldCursor{}, fmt.Errorf("schema field cursor is invalid")
	}
	return cursor, nil
}
