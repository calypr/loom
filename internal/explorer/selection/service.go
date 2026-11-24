package selection

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/authscope"
	bundlepublication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

type Store interface {
	BeginSelection(context.Context, SelectionRevision, string) (*SelectionRevision, error)
	AppendSelectionMembers(context.Context, string, string, []SelectionMember) ([]SelectionMember, error)
	DigestSelectionMembers(context.Context, string, string) (string, int64, int64, error)
	CompleteSelection(context.Context, string, string, string, int64, int64, time.Time) (*SelectionRevision, error)
	AbortSelection(context.Context, string, string) error
	GetSelection(context.Context, string, string) (*SelectionRevision, error)
	VisitSelectionMembers(context.Context, string, string, string, int, func(SelectionMember) error) (string, error)
}

type PublishedRevision struct {
	Project              string
	ExplorerID           string
	CompilationReceiptID string
	ExecutionID          string
	Outputs              []string
}

type CapabilityResolver struct {
	Current        func(context.Context, string, string, string) (capability.Snapshot, error)
	ForCompilation func(context.Context, string, string) (AuthorizedCapability, error)
	ForExecution   func(context.Context, string, string) (AuthorizedCapability, error)
}

type AuthorizedCapability struct {
	Snapshot capability.Snapshot
	Scope    authscope.ReadScope
}

type SelectionReferenceValidator func(context.Context, string, string, authscope.ReadScope, []ResourceRef) error

type ErrorFactory struct {
	Malformed          func(stage, message string, cause error) error
	Unavailable        func(stage, code, message string, cause error) error
	Conflict           func(stage, code, message string, details map[string]any, cause error) error
	Forbidden          func(stage, message string, cause error) error
	NotFound           func(stage, code, message string, cause error) error
	Unprocessable      func(stage, code, message string, cause error) error
	PersistenceFailure func(stage, message string, err error) error
}

type Config struct {
	Store                               Store
	ReadPublishedRevision               func(context.Context, string) (*PublishedRevision, error)
	Capability                          CapabilityResolver
	CapabilityProvider                  func() CapabilityResolver
	SelectionSourceResolver             SelectionSourceResolver
	SelectionReferenceValidator         SelectionReferenceValidator
	SelectionReferenceValidatorProvider func() SelectionReferenceValidator
	Errors                              ErrorFactory
	IsNotFound                          func(error) bool
}

type Service struct {
	store      Store
	config     Config
	errors     ErrorFactory
	isNotFound func(error) bool
}

func NewService(config Config) (*Service, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("selection store is required")
	}
	if config.Errors.Malformed == nil {
		return nil, fmt.Errorf("selection error factory is required")
	}
	if config.IsNotFound == nil {
		config.IsNotFound = func(error) bool { return false }
	}
	return &Service{store: config.Store, config: config, errors: config.Errors, isNotFound: config.IsNotFound}, nil
}

func (s *Service) selectionReferenceValidator() SelectionReferenceValidator {
	if s.config.SelectionReferenceValidatorProvider != nil {
		return s.config.SelectionReferenceValidatorProvider()
	}
	return s.config.SelectionReferenceValidator
}

func (s *Service) capability() CapabilityResolver {
	if s.config.CapabilityProvider != nil {
		return s.config.CapabilityProvider()
	}
	return s.config.Capability
}

func (s *Service) capabilityResolutionFailure(stage, message string, err error) error {
	switch {
	case errors.Is(err, capability.ErrStaleSnapshot), errors.Is(err, capability.ErrSnapshotUnavailable), errors.Is(err, capability.ErrNotFound):
		return s.errors.Conflict(stage, "STALE_CATALOG_SNAPSHOT", message, nil, err)
	case errors.Is(err, authscope.ErrUnauthenticated), errors.Is(err, authscope.ErrForbidden):
		return s.errors.Forbidden(stage, message, err)
	default:
		return s.errors.Unavailable(stage, "CAPABILITY_UNAVAILABLE", message, err)
	}
}

func validateAuthorizedReadScope(scope authscope.ReadScope, expectedDigest string) error {
	switch scope.Mode {
	case authscope.ReadScopeUnrestricted:
		if len(scope.AuthResourcePaths) != 0 {
			return fmt.Errorf("unrestricted scope must not carry resource paths")
		}
	case authscope.ReadScopeRestricted:
	default:
		return fmt.Errorf("authorization scope mode is missing or invalid")
	}
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sortStrings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	if hex.EncodeToString(sum[:]) != expectedDigest {
		return fmt.Errorf("authorized scope does not match receipt scope digest")
	}
	return nil
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

const (
	DefaultSelectionMaxRows     int64 = 100_000
	DefaultSelectionMaxBytes    int64 = 128 << 20
	DefaultSelectionMaxDuration       = 30 * time.Second
	selectionBatchSize                = 256
)

type selectionCreateRequest struct {
	Project         string
	ExplorerID      string
	SnapshotToken   string
	Actor           string
	Generation      string
	IdempotencyKey  string
	ResourceType    string
	Scope           authscope.ReadScope
	ScopeDigest     string
	Rule            SelectionRule
	Members         []ResourceRef
	Source          SelectionSource
	Exclusions      []ResourceRef
	BaseSelectionID string
}

type SelectionSourceIntent struct {
	Kind                string
	Resources           []ResourceRef
	PublishedOutput     *PublishedOutputIntent
	SelectionRevisionID string
}

type PublishedOutputIntent struct {
	RevisionID string
	OutputID   string
	Filters    []published.Filter
}

const (
	SelectionSourceResources         = "resources"
	SelectionSourcePublishedOutput   = "publishedOutput"
	SelectionSourceSelectionRevision = "selectionRevision"
)

type SelectionIntentCreateRequest struct {
	Project        string
	ExplorerID     string
	Actor          string
	SnapshotToken  string
	IdempotencyKey string
	ResourceType   string
	Source         SelectionSourceIntent
	Exclusions     []ResourceRef
}

type SelectionSourceResolver interface {
	ResolveSelectionSource(context.Context, string, string, string, string) (published.Materialization, error)
	StreamSelectionSource(context.Context, published.Materialization, published.StreamRequest, func(map[string]any) error) error
}

type materializationSelectionReader struct {
	materialization published.Materialization
	resolver        SelectionSourceResolver
}

func (r materializationSelectionReader) ExactExecutionMaterialization(_ context.Context, executionID, _ string) (published.Materialization, error) {
	if executionID != r.materialization.Revision {
		return published.Materialization{}, fmt.Errorf("%w: published selection execution changed", bundlepublication.ErrSelectionSourceIdentityChanged)
	}
	return r.materialization, nil
}

func (r materializationSelectionReader) StreamExactExecution(ctx context.Context, _ string, _ string, req published.StreamRequest, visit func(map[string]any) error) error {
	return r.resolver.StreamSelectionSource(ctx, r.materialization, req, visit)
}

type SelectionCreateResult struct {
	Header *SelectionRevision
}

type selectionReadRequest struct {
	Project     string
	ExplorerID  string
	RevisionID  string
	Generation  string
	Cursor      string
	Scope       authscope.ReadScope
	ScopeDigest string
	Limit       int
}

type SelectionReadIntentRequest struct {
	Project    string
	ExplorerID string
	RevisionID string
	Cursor     string
	Limit      int
}

type SelectionReadResult struct {
	Header     *SelectionRevision
	Members    []SelectionMember
	NextCursor string
}

type SelectionPublishedReader interface {
	ExactExecutionMaterialization(context.Context, string, string) (published.Materialization, error)
	StreamExactExecution(context.Context, string, string, published.StreamRequest, func(map[string]any) error) error
}

type selectionCursor struct {
	Version          int    `json:"version"`
	Project          string `json:"project"`
	RevisionID       string `json:"revisionId"`
	Generation       string `json:"generation"`
	Scope            string `json:"scope"`
	MemberKey        string `json:"memberKey"`
	ReceiptID        string `json:"receiptId,omitempty"`
	OutputID         string `json:"outputId,omitempty"`
	MembershipDigest string `json:"membershipDigest,omitempty"`
}

func (s *Service) createSelectionResolved(ctx context.Context, req selectionCreateRequest) (SelectionCreateResult, error) {
	if req.Rule.Kind == SelectionRuleAllMatching {
		return SelectionCreateResult{}, fmt.Errorf("all-matching selections require an exact published reader")
	}
	return s.createSelection(ctx, req, nil)
}

func (s *Service) CreateSelection(ctx context.Context, req SelectionIntentCreateRequest) (result SelectionCreateResult, err error) {
	result, err = s.createSelectionIntent(ctx, req)
	if err != nil {
		err = s.normalizeSelectionError(err)
	}
	return result, err
}

func (s *Service) createSelectionIntent(ctx context.Context, req SelectionIntentCreateRequest) (SelectionCreateResult, error) {
	if s == nil || s.capability().ForCompilation == nil {
		return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("selection authorization resolver is required"))
	}
	resolver := s.config.SelectionSourceResolver
	snapshotToken := strings.TrimSpace(req.SnapshotToken)

	authorized, err := s.capability().ForCompilation(ctx, req.Project, snapshotToken)
	if err != nil {
		return SelectionCreateResult{}, s.capabilityResolutionFailure("selection", "the selection capability snapshot is unavailable", err)
	}
	if snapshotToken == "" || authorized.Snapshot.Token != snapshotToken {
		return SelectionCreateResult{}, selectionStale(fmt.Errorf("selection snapshot is stale"))
	}
	project := projectid.Canonical(req.Project)
	generation := strings.TrimSpace(authorized.Snapshot.Identity.Generation)
	scopeDigest := authorized.Snapshot.Identity.AuthorizationScopeDigest
	resolved := selectionCreateRequest{Project: project, ExplorerID: req.ExplorerID, Actor: req.Actor, SnapshotToken: req.SnapshotToken, Generation: generation, IdempotencyKey: req.IdempotencyKey, ResourceType: strings.TrimSpace(req.ResourceType), Scope: authorized.Scope, ScopeDigest: scopeDigest}
	switch req.Source.Kind {
	case SelectionSourceResources:
		if req.Source.PublishedOutput != nil {
			return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("resources source cannot include published output payload"))
		}
		resolved.Rule = SelectionRule{Kind: SelectionRuleExplicit}
		resolved.Source = SelectionSource{Kind: SelectionSourceExplicit}
		var err error
		resolved.Members, resolved.ResourceType, err = canonicalSelectionRefs(req.Source.Resources, project, generation, resolved.ResourceType)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		resolved.Exclusions, resolved.ResourceType, err = canonicalSelectionRefs(req.Exclusions, project, generation, resolved.ResourceType)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		validator := s.selectionReferenceValidator()
		if validator == nil {
			return SelectionCreateResult{}, fmt.Errorf("selection reference validator is required")
		}
		refs := append(append([]ResourceRef(nil), resolved.Members...), resolved.Exclusions...)
		if err := validator(ctx, project, generation, authorized.Scope, refs); err != nil {
			return SelectionCreateResult{}, err
		}
		return s.createSelectionResolved(ctx, resolved)
	case SelectionSourcePublishedOutput:
		if req.ResourceType != "" {
			return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("published output source cannot override resource type"))
		}
		if req.Source.Resources != nil {
			return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("published output source cannot include resource refs"))
		}
		if req.Source.PublishedOutput == nil {
			return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("published selection source is required"))
		}
		if resolver == nil {
			return SelectionCreateResult{}, s.errors.Unavailable("selection", "PUBLISHED_SELECTION_UNAVAILABLE", "published selection sources are not configured", nil)
		}
		intent := req.Source.PublishedOutput
		revision, err := s.config.ReadPublishedRevision(ctx, intent.RevisionID)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		if revision == nil || projectid.Canonical(revision.Project) != project || revision.ExplorerID != req.ExplorerID {
			return SelectionCreateResult{}, ErrSelectionNotFound
		}
		executionID := strings.TrimSpace(revision.ExecutionID)
		if executionID == "" {
			return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("published selection revision has no exact execution"))
		}
		outputKnown := false
		for _, output := range revision.Outputs {
			if output == intent.OutputID {
				outputKnown = true
				break
			}
		}
		if !outputKnown {
			return SelectionCreateResult{}, ErrSelectionNotFound
		}
		materialization, err := resolver.ResolveSelectionSource(ctx, project, req.ExplorerID, executionID, intent.OutputID)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		if projectid.Canonical(materialization.Project) != project || materialization.DatasetGeneration != generation || materialization.Revision != executionID {
			return SelectionCreateResult{}, selectionStale(fmt.Errorf("published selection source identity changed"))
		}
		if materialization.SourceRow == nil || !materialization.SourceRow.Valid() {
			return SelectionCreateResult{}, ErrSelectionNotAddressable
		}
		if revision.CompilationReceiptID != "" && materialization.ReceiptID != revision.CompilationReceiptID {
			return SelectionCreateResult{}, selectionStale(fmt.Errorf("published selection receipt changed"))
		}
		if resolved.ResourceType == "" {
			resolved.ResourceType = materialization.SourceRow.ResourceType
		}
		if resolved.ResourceType != materialization.SourceRow.ResourceType || materialization.Revision == "" || materialization.ReceiptID == "" || materialization.SchemaDigest == "" {
			return SelectionCreateResult{}, ErrSelectionNotAddressable
		}
		resolved.Rule = SelectionRule{Kind: SelectionRuleAllMatching, Filters: append([]published.Filter(nil), intent.Filters...)}
		resolved.Source = SelectionSource{Kind: SelectionSourcePublished, RevisionID: intent.RevisionID, ReceiptID: materialization.ReceiptID, ExecutionID: materialization.Revision, OutputID: intent.OutputID, SchemaDigest: materialization.SchemaDigest, Generation: materialization.DatasetGeneration, ResourceType: materialization.SourceRow.ResourceType, SourceIDColumn: materialization.SourceRow.IDColumn}
		resolved.Exclusions, resolved.ResourceType, err = canonicalSelectionRefs(req.Exclusions, project, generation, resolved.ResourceType)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		return s.createSelectionWithMaterialization(ctx, resolved, materialization, resolver)
	case SelectionSourceSelectionRevision:
		if req.ResourceType != "" || req.Source.Resources != nil || req.Source.PublishedOutput != nil || strings.TrimSpace(req.Source.SelectionRevisionID) == "" {
			return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("selection revision source requires only selectionRevisionId"))
		}
		base, err := s.store.GetSelection(ctx, project, strings.TrimSpace(req.Source.SelectionRevisionID))
		if err != nil {
			return SelectionCreateResult{}, err
		}
		if base == nil || !base.Complete {
			return SelectionCreateResult{}, ErrSelectionIncomplete
		}
		if base.Generation != generation || base.ScopeDigest != scopeDigest {
			return SelectionCreateResult{}, selectionStale(fmt.Errorf("base selection is outside the active generation or authorization scope"))
		}
		resolved.ResourceType = base.ResourceType
		resolved.Rule = SelectionRule{Kind: SelectionRuleExplicit}
		resolved.Source = SelectionSource{Kind: SelectionSourceRevision, RevisionID: base.ID, Generation: base.Generation, ResourceType: base.ResourceType, MembershipDigest: base.MembershipDigest}
		resolved.BaseSelectionID = base.ID
		resolved.Exclusions, resolved.ResourceType, err = canonicalSelectionRefs(req.Exclusions, project, generation, resolved.ResourceType)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		validator := s.selectionReferenceValidator()
		if validator == nil {
			return SelectionCreateResult{}, fmt.Errorf("selection reference validator is required")
		}
		if err := validator(ctx, project, generation, authorized.Scope, resolved.Exclusions); err != nil {
			return SelectionCreateResult{}, err
		}
		return s.createSelectionResolved(ctx, resolved)
	default:
		return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("unsupported selection source %q", req.Source.Kind))
	}
}

func (s *Service) createSelectionWithMaterialization(ctx context.Context, req selectionCreateRequest, materialization published.Materialization, resolver SelectionSourceResolver) (SelectionCreateResult, error) {
	flow := s.selectionFlow()
	return flow.createSelectionWithMaterialization(ctx, req, materialization, resolver)
}

func (s *Service) createSelection(parent context.Context, req selectionCreateRequest, reader SelectionPublishedReader) (SelectionCreateResult, error) {
	flow := s.selectionFlow()
	return flow.createSelection(parent, req, reader)
}

func (s *Service) readSelectionResolved(ctx context.Context, req selectionReadRequest) (SelectionReadResult, error) {
	flow := s.selectionFlow()
	return flow.readSelectionResolved(ctx, req)
}

func (s *Service) selectionFlow() selectionFlow {
	if s == nil {
		return selectionFlow{}
	}
	return selectionFlow{store: s.store}
}

type selectionFlow struct {
	store Store
}

func (s *Service) ReadSelection(ctx context.Context, req SelectionReadIntentRequest) (result SelectionReadResult, err error) {
	result, err = s.readSelectionIntent(ctx, req)
	if err != nil {
		err = s.normalizeSelectionError(err)
	}
	return result, err
}

func (s *Service) readSelectionIntent(ctx context.Context, req SelectionReadIntentRequest) (SelectionReadResult, error) {
	if s == nil || s.capability().Current == nil || s.capability().ForExecution == nil {
		return SelectionReadResult{}, fmt.Errorf("selection authorization resolver is required")
	}
	project := projectid.Canonical(req.Project)
	current, err := s.capability().Current(ctx, project, req.ExplorerID, "")
	if err != nil {
		return SelectionReadResult{}, s.capabilityResolutionFailure("selection", "the current capability snapshot is unavailable", err)
	}
	currentToken := strings.TrimSpace(current.Token)
	if currentToken == "" {
		return SelectionReadResult{}, selectionStale(fmt.Errorf("current capability snapshot is unavailable"))
	}
	authorized, err := s.capability().ForExecution(ctx, project, currentToken)
	if err != nil {
		return SelectionReadResult{}, s.capabilityResolutionFailure("selection", "the selection capability snapshot is unavailable", err)
	}
	return s.readSelectionResolved(ctx, selectionReadRequest{Project: project, ExplorerID: req.ExplorerID, RevisionID: req.RevisionID, Generation: authorized.Snapshot.Identity.Generation, Cursor: req.Cursor, Limit: req.Limit, Scope: authorized.Scope, ScopeDigest: authorized.Snapshot.Identity.AuthorizationScopeDigest})
}

func (s *Service) normalizeSelectionError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrSelectionNotAddressable):
		return s.errors.Unprocessable("selection", "SELECTION_SOURCE_NOT_ADDRESSABLE", "published output cannot be mapped to source resources", err)
	case errors.Is(err, ErrSelectionLimit), errors.Is(err, context.DeadlineExceeded):
		return s.errors.Unprocessable("selection", "SELECTION_LIMIT_EXCEEDED", "selection exceeded configured row, byte, or duration limits", err)
	case errors.Is(err, ErrSelectionConflict):
		return s.errors.Conflict("selection", "SELECTION_IDEMPOTENCY_CONFLICT", "selection idempotency key is bound to different immutable intent", nil, err)
	case errors.Is(err, ErrSelectionIncomplete):
		return s.errors.Conflict("selection", "SELECTION_NOT_COMPLETE", "selection membership is not complete", nil, err)
	case errors.Is(err, ErrSelectionStaleScope):
		return s.errors.Conflict("selection", "SELECTION_STALE_SCOPE", "selection authorization or immutable source identity changed", nil, err)
	case errors.Is(err, bundlepublication.ErrSelectionSourceIdentityChanged):
		return s.errors.Conflict("selection", "SELECTION_STALE_SCOPE", "selection authorization or immutable source identity changed", nil, err)
	case errors.Is(err, ErrResourceRefScopeMismatch):
		return s.errors.Conflict("selection", "SELECTION_STALE_SCOPE", "selection references are outside the authorized snapshot", nil, err)
	case errors.Is(err, ErrSelectionMalformed):
		return s.errors.Malformed("selection", "selection request is malformed", err)
	case s.isNotFound(err), errors.Is(err, ErrSelectionNotFound), errors.Is(err, bundlepublication.ErrBundleNotFound):
		return s.errors.NotFound("selection", "SELECTION_NOT_FOUND", "selection source or revision was not found", err)
	default:
		return s.errors.PersistenceFailure("selection", "the selection operation could not be completed", err)
	}
}

func selectionMalformed(err error) error {
	if err == nil {
		return ErrSelectionMalformed
	}
	return fmt.Errorf("%w: %v", ErrSelectionMalformed, err)
}

func selectionStale(err error) error {
	if err == nil {
		return ErrSelectionStaleScope
	}
	return fmt.Errorf("%w: %v", ErrSelectionStaleScope, err)
}

func exclusionSet(values []ResourceRef) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[refKey(value)] = struct{}{}
	}
	return result
}

func canonicalSelectionRefs(values []ResourceRef, project, generation, resourceType string) ([]ResourceRef, string, error) {
	result := make([]ResourceRef, len(values))
	for i, raw := range values {
		ref := raw.Canonical()
		if err := ref.Validate(project, generation, ""); err != nil {
			if errors.Is(err, ErrResourceRefScopeMismatch) {
				return nil, resourceType, selectionStale(err)
			}
			return nil, resourceType, selectionMalformed(err)
		}
		if resourceType == "" {
			resourceType = ref.ResourceType
		}
		if ref.ResourceType != resourceType {
			return nil, resourceType, selectionMalformed(fmt.Errorf("selection references must use one resource type"))
		}
		result[i] = ref
	}
	if resourceType == "" {
		return nil, resourceType, selectionMalformed(fmt.Errorf("selection resource type is required"))
	}
	return result, resourceType, nil
}

func refKey(ref ResourceRef) string {
	ref = ref.Canonical()
	return ref.Project + "\x00" + ref.Generation + "\x00" + ref.ResourceType + "\x00" + ref.ID
}

func selectionID(project, generation, idempotency string) string {

	sum := sha256.Sum256([]byte(strings.TrimSpace(project) + "\x00" + strings.TrimSpace(generation) + "\x00" + strings.TrimSpace(idempotency)))
	return "selection_" + hex.EncodeToString(sum[:])
}

func encodeSelectionCursor(cursor selectionCursor) (string, error) {
	b, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeSelectionCursor(value string) (*selectionCursor, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid selection cursor")
	}
	var cursor selectionCursor
	if json.Unmarshal(b, &cursor) != nil || cursor.Version != 1 || cursor.Project == "" || cursor.RevisionID == "" || cursor.Generation == "" || cursor.Scope == "" || cursor.MemberKey == "" {
		return nil, fmt.Errorf("invalid selection cursor")
	}
	return &cursor, nil
}
