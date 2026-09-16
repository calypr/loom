package lifecycle

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
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
	"github.com/google/uuid"
)

const (
	DefaultSelectionMaxRows     int64 = 100_000
	DefaultSelectionMaxBytes    int64 = 128 << 20
	DefaultSelectionMaxDuration       = 30 * time.Second
	selectionBatchSize                = 256
)

type selectionCreateRequest struct {
	Project        string
	ExplorerID     string
	SnapshotToken  string
	Actor          string
	Generation     string
	IdempotencyKey string
	ResourceType   string
	Scope          authscope.ReadScope
	ScopeDigest    string
	Rule           explorer.SelectionRule
	Members        []explorer.ResourceRef
	Source         explorer.SelectionSource
	Exclusions     []explorer.ResourceRef
}

// SelectionSourceIntent is the closed authoring input. It contains no
// execution, receipt, schema, or addressability metadata; lifecycle resolves
// those identities from the authorized snapshot and exact publication source.
type SelectionSourceIntent struct {
	Kind            string
	Resources       []explorer.ResourceRef
	PublishedOutput *PublishedOutputIntent
}

type PublishedOutputIntent struct {
	RevisionID string
	OutputID   string
	Filters    []published.Filter
}

const (
	SelectionSourceResources       = "resources"
	SelectionSourcePublishedOutput = "publishedOutput"
)

type SelectionIntentCreateRequest struct {
	Project        string
	ExplorerID     string
	Actor          string
	SnapshotToken  string
	IdempotencyKey string
	ResourceType   string
	Source         SelectionSourceIntent
	Exclusions     []explorer.ResourceRef
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
	Header *explorer.SelectionRevision
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
	Header     *explorer.SelectionRevision
	Members    []explorer.SelectionMember
	NextCursor string
}

// SelectionPublishedReader is the exact-output adapter required for
// all-matching selections. *published.Reader satisfies this interface.
type SelectionPublishedReader interface {
	ExactExecutionMaterialization(context.Context, string, string) (published.Materialization, error)
	StreamExactExecution(context.Context, string, string, published.StreamRequest, func(map[string]any) error) error
}

type selectionCursor struct {
	Version    int    `json:"version"`
	Project    string `json:"project"`
	RevisionID string `json:"revisionId"`
	Generation string `json:"generation"`
	Scope      string `json:"scope"`
	MemberKey  string `json:"memberKey"`
}

func (s *Service) createSelectionResolved(ctx context.Context, req selectionCreateRequest) (SelectionCreateResult, error) {
	if req.Rule.Kind == explorer.SelectionRuleAllMatching {
		return SelectionCreateResult{}, fmt.Errorf("all-matching selections require an exact published reader")
	}
	return s.createSelection(ctx, req, nil)
}

// CreateSelection authenticates and resolves the closed source intent before
// any header or membership count can be disclosed.
func (s *Service) CreateSelection(ctx context.Context, req SelectionIntentCreateRequest) (result SelectionCreateResult, err error) {
	result, err = s.createSelectionIntent(ctx, req)
	if err != nil {
		err = normalizeSelectionError(err)
	}
	return result, err
}

func (s *Service) createSelectionIntent(ctx context.Context, req SelectionIntentCreateRequest) (SelectionCreateResult, error) {
	if s == nil || s.config.Capability.ForCompilation == nil {
		return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("selection authorization resolver is required"))
	}
	resolver := s.config.SelectionSourceResolver
	snapshotToken := strings.TrimSpace(req.SnapshotToken)
	// Creation is bound to the active immutable dataset generation. Execution
	// authorization intentionally supports retained generations, so using it
	// here would allow a stale snapshot to create a new selection.
	authorized, err := s.config.Capability.ForCompilation(ctx, req.Project, snapshotToken)
	if err != nil {
		return SelectionCreateResult{}, err
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
		resolved.Rule = explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}
		resolved.Source = explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit}
		var err error
		resolved.Members, resolved.ResourceType, err = canonicalSelectionRefs(req.Source.Resources, project, generation, resolved.ResourceType)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		resolved.Exclusions, resolved.ResourceType, err = canonicalSelectionRefs(req.Exclusions, project, generation, resolved.ResourceType)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		if s.config.SelectionReferenceValidator == nil {
			return SelectionCreateResult{}, fmt.Errorf("selection reference validator is required")
		}
		refs := append(append([]explorer.ResourceRef(nil), resolved.Members...), resolved.Exclusions...)
		if err := s.config.SelectionReferenceValidator(ctx, project, generation, authorized.Scope, refs); err != nil {
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
		if resolver == nil || req.Source.PublishedOutput == nil {
			return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("published selection resolver is required"))
		}
		intent := req.Source.PublishedOutput
		revision, err := s.store.GetRevision(ctx, intent.RevisionID)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		if revision == nil || projectid.Canonical(revision.Project) != project || revision.ExplorerID != req.ExplorerID {
			return SelectionCreateResult{}, explorer.ErrNotFound
		}
		executionID := strings.TrimSpace(revision.Publication.ExecutionID)
		if executionID == "" {
			return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("published selection revision has no exact execution"))
		}
		outputKnown := false
		for _, output := range revision.Dataset.Outputs {
			if output.Name == intent.OutputID {
				outputKnown = true
				break
			}
		}
		if !outputKnown {
			return SelectionCreateResult{}, explorer.ErrNotFound
		}
		materialization, err := resolver.ResolveSelectionSource(ctx, project, req.ExplorerID, executionID, intent.OutputID)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		if projectid.Canonical(materialization.Project) != project || materialization.DatasetGeneration != generation || materialization.Revision != executionID {
			return SelectionCreateResult{}, selectionStale(fmt.Errorf("published selection source identity changed"))
		}
		if materialization.SourceRow == nil || !materialization.SourceRow.Valid() {
			return SelectionCreateResult{}, explorer.ErrSelectionNotAddressable
		}
		if revision.CompilationReceiptID != "" && materialization.ReceiptID != revision.CompilationReceiptID {
			return SelectionCreateResult{}, selectionStale(fmt.Errorf("published selection receipt changed"))
		}
		if resolved.ResourceType == "" {
			resolved.ResourceType = materialization.SourceRow.ResourceType
		}
		if resolved.ResourceType != materialization.SourceRow.ResourceType || materialization.Revision == "" || materialization.ReceiptID == "" || materialization.SchemaDigest == "" {
			return SelectionCreateResult{}, explorer.ErrSelectionNotAddressable
		}
		resolved.Rule = explorer.SelectionRule{Kind: explorer.SelectionRuleAllMatching, Filters: append([]published.Filter(nil), intent.Filters...)}
		resolved.Source = explorer.SelectionSource{Kind: explorer.SelectionSourcePublished, RevisionID: intent.RevisionID, ReceiptID: materialization.ReceiptID, ExecutionID: materialization.Revision, OutputID: intent.OutputID, SchemaDigest: materialization.SchemaDigest, Generation: materialization.DatasetGeneration, ResourceType: materialization.SourceRow.ResourceType, SourceIDColumn: materialization.SourceRow.IDColumn}
		resolved.Exclusions, resolved.ResourceType, err = canonicalSelectionRefs(req.Exclusions, project, generation, resolved.ResourceType)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		return s.createSelectionWithMaterialization(ctx, resolved, materialization, resolver)
	default:
		return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("unsupported selection source %q", req.Source.Kind))
	}
}

func (s *Service) createSelectionWithMaterialization(ctx context.Context, req selectionCreateRequest, materialization published.Materialization, resolver SelectionSourceResolver) (SelectionCreateResult, error) {
	return s.createSelection(ctx, req, materializationSelectionReader{materialization: materialization, resolver: resolver})
}

func (s *Service) createSelection(parent context.Context, req selectionCreateRequest, reader SelectionPublishedReader) (SelectionCreateResult, error) {
	if s == nil || s.store == nil {
		return SelectionCreateResult{}, fmt.Errorf("Explorer lifecycle store is required")
	}
	req.Project = strings.TrimSpace(req.Project)
	req.Generation = strings.TrimSpace(req.Generation)
	req.ResourceType = strings.TrimSpace(req.ResourceType)
	if req.Project == "" || req.Generation == "" || req.ResourceType == "" || strings.TrimSpace(req.IdempotencyKey) == "" {
		return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("selection project, generation, resource type, and idempotency key are required"))
	}
	if err := validateAuthorizedReadScope(req.Scope, req.ScopeDigest); err != nil {
		return SelectionCreateResult{}, selectionStale(err)
	}
	req.Rule = req.Rule.Canonical()
	if err := req.Rule.Validate(); err != nil {
		return SelectionCreateResult{}, selectionMalformed(err)
	}
	req.Source = req.Source.Canonical()
	if req.Source.Kind == "" {
		req.Source.Kind = explorer.SelectionSourceExplicit
	}
	if reader == nil && req.Rule.Kind != explorer.SelectionRuleExplicit {
		return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("explicit selection source is required"))
	}
	if reader != nil && req.Source.Kind != explorer.SelectionSourcePublished {
		return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("published source identity is required"))
	}
	if reader == nil && req.Source.Kind != explorer.SelectionSourceExplicit {
		return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("explicit source identity is required"))
	}
	if err := req.Source.Validate(req.Project, req.Generation, req.ResourceType); err != nil && reader == nil {
		return SelectionCreateResult{}, selectionMalformed(err)
	}
	for i := range req.Exclusions {
		req.Exclusions[i] = req.Exclusions[i].Canonical()
		if err := req.Exclusions[i].Validate(req.Project, req.Generation, req.ResourceType); err != nil {
			if errors.Is(err, explorer.ErrResourceRefScopeMismatch) {
				return SelectionCreateResult{}, selectionStale(err)
			}
			return SelectionCreateResult{}, selectionMalformed(err)
		}
	}
	if reader != nil {
		materialization, err := reader.ExactExecutionMaterialization(parent, req.Source.ExecutionID, req.Source.OutputID)
		if err != nil {
			return SelectionCreateResult{}, err
		}
		if projectid.Canonical(materialization.Project) != req.Project || materialization.DatasetGeneration != req.Generation || materialization.Revision != req.Source.ExecutionID {
			return SelectionCreateResult{}, selectionStale(fmt.Errorf("published selection source identity changed"))
		}
		if materialization.SourceRow == nil || !materialization.SourceRow.Valid() || materialization.SourceRow.ResourceType != req.ResourceType {
			return SelectionCreateResult{}, explorer.ErrSelectionNotAddressable
		}
		if materialization.Revision == "" || materialization.SourceRow.IDColumn == "" {
			return SelectionCreateResult{}, explorer.ErrSelectionNotAddressable
		}
		req.Source.Generation = materialization.DatasetGeneration
		if materialization.SourceRevision != "" && req.Source.RevisionID != materialization.SourceRevision {
			return SelectionCreateResult{}, selectionStale(fmt.Errorf("published selection revision changed"))
		}
		req.Source.RevisionID = firstNonEmpty(req.Source.RevisionID, materialization.SourceRevision)
		req.Source.ExecutionID = materialization.Revision
		req.Source.ReceiptID = materialization.ReceiptID
		req.Source.SchemaDigest = materialization.SchemaDigest
		req.Source.ResourceType = materialization.SourceRow.ResourceType
		req.Source.SourceIDColumn = materialization.SourceRow.IDColumn
		if req.Source.ReceiptID == "" || req.Source.SchemaDigest == "" {
			return SelectionCreateResult{}, explorer.ErrSelectionNotAddressable
		}
	}
	var requestMembers []explorer.ResourceRef
	if req.Rule.Kind == explorer.SelectionRuleExplicit {
		requestMembers = req.Members
	}
	ruleDigest, err := explorer.SelectionRuleDigestWithMembers(req.Rule, req.Source, req.Exclusions, requestMembers)
	if err != nil {
		return SelectionCreateResult{}, err
	}
	selectionID := selectionID(req.Project, req.Generation, req.IdempotencyKey)
	header := explorer.SelectionRevision{ID: selectionID, Project: req.Project, Generation: req.Generation, ResourceType: req.ResourceType, Rule: req.Rule, Source: req.Source, Exclusions: req.Exclusions, ScopeDigest: req.ScopeDigest, RuleDigest: ruleDigest, IdempotencyKey: strings.TrimSpace(req.IdempotencyKey), CreatedAt: time.Now().UTC()}
	writerToken := uuid.NewString()
	started, err := s.store.BeginSelection(parent, header, writerToken)
	if err != nil {
		return SelectionCreateResult{}, err
	}
	if started.Complete {
		return SelectionCreateResult{Header: started}, nil
	}
	ctx, cancel := context.WithTimeout(parent, DefaultSelectionMaxDuration)
	defer cancel()
	if reader == nil {
		if err := s.appendExplicit(ctx, header, writerToken, req.Members); err != nil {
			return SelectionCreateResult{}, errors.Join(err, s.abortSelectionBounded(parent, header.ID, writerToken))
		}
	} else {
		if err := s.appendPublished(ctx, header, writerToken, req, reader); err != nil {
			return SelectionCreateResult{}, errors.Join(err, s.abortSelectionBounded(parent, header.ID, writerToken))
		}
	}
	digest, count, bytes, err := s.store.DigestSelectionMembers(ctx, header.Project, header.ID)
	if err != nil || count > DefaultSelectionMaxRows || bytes > DefaultSelectionMaxBytes {
		if err == nil {
			err = explorer.ErrSelectionLimit
		}
		return SelectionCreateResult{}, errors.Join(err, s.abortSelectionBounded(parent, header.ID, writerToken))
	}
	completed, err := s.store.CompleteSelection(ctx, header.ID, writerToken, digest, count, bytes, time.Now().UTC())
	if err != nil {
		return SelectionCreateResult{}, errors.Join(err, s.abortSelectionBounded(parent, header.ID, writerToken))
	}
	return SelectionCreateResult{Header: completed}, nil
}

func (s *Service) abortSelectionBounded(parent context.Context, selectionID, writerToken string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	return s.store.AbortSelection(cleanupCtx, selectionID, writerToken)
}

func (s *Service) appendExplicit(ctx context.Context, header explorer.SelectionRevision, writerToken string, members []explorer.ResourceRef) error {
	excluded := exclusionSet(header.Exclusions)
	for offset := 0; offset < len(members); offset += selectionBatchSize {
		end := offset + selectionBatchSize
		if end > len(members) {
			end = len(members)
		}
		batch := make([]explorer.SelectionMember, 0, end-offset)
		for _, ref := range members[offset:end] {
			ref = ref.Canonical()
			if err := ref.Validate(header.Project, header.Generation, header.ResourceType); err != nil {
				return err
			}
			if _, skip := excluded[refKey(ref)]; skip {
				continue
			}
			batch = append(batch, explorer.SelectionMember{Ref: ref})
		}
		if _, err := s.store.AppendSelectionMembers(ctx, header.ID, writerToken, batch); err != nil {
			return err
		}
		if int64(end) > DefaultSelectionMaxRows {
			return explorer.ErrSelectionLimit
		}
	}
	return nil
}

func (s *Service) appendPublished(ctx context.Context, header explorer.SelectionRevision, writerToken string, req selectionCreateRequest, reader SelectionPublishedReader) error {
	materialization, err := reader.ExactExecutionMaterialization(ctx, req.Source.ExecutionID, req.Source.OutputID)
	if err != nil {
		return err
	}
	excluded := exclusionSet(header.Exclusions)
	batch := make([]explorer.SelectionMember, 0, selectionBatchSize)
	var sourceRows, sourceBytes int64
	appendBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		_, err := s.store.AppendSelectionMembers(ctx, header.ID, writerToken, batch)
		batch = batch[:0]
		return err
	}
	streamReq := published.StreamRequest{Filters: req.Rule.Filters, AuthResourcePaths: append([]string(nil), req.Scope.AuthResourcePaths...), Unrestricted: req.Scope.Mode == authscope.ReadScopeUnrestricted}
	if err := reader.StreamExactExecution(ctx, req.Source.ExecutionID, req.Source.OutputID, streamReq, func(row map[string]any) error {
		sourceRows++
		if sourceRows > DefaultSelectionMaxRows {
			return explorer.ErrSelectionLimit
		}
		encoded, encodeErr := json.Marshal(row)
		if encodeErr != nil {
			return encodeErr
		}
		sourceBytes += int64(len(encoded))
		if sourceBytes > DefaultSelectionMaxBytes {
			return explorer.ErrSelectionLimit
		}
		resourceType, id, err := published.SourceResourceRef(materialization, row)
		if err != nil {
			return err
		}
		ref := explorer.ResourceRef{Project: header.Project, Generation: header.Generation, ResourceType: resourceType, ID: id}
		if _, skip := excluded[refKey(ref)]; skip {
			return nil
		}
		batch = append(batch, explorer.SelectionMember{Ref: ref})
		if len(batch) >= selectionBatchSize {
			return appendBatch()
		}
		return nil
	}); err != nil {
		return err
	}
	// The reader callback runs synchronously, so flush a final bounded batch
	// after the stream completes.
	return appendBatch()
}

func (s *Service) readSelectionResolved(ctx context.Context, req selectionReadRequest) (SelectionReadResult, error) {
	if s == nil || s.store == nil {
		return SelectionReadResult{}, fmt.Errorf("Explorer lifecycle store is required")
	}
	if err := validateAuthorizedReadScope(req.Scope, req.ScopeDigest); err != nil {
		return SelectionReadResult{}, selectionStale(err)
	}
	cursor, err := decodeSelectionCursor(req.Cursor)
	if err != nil {
		return SelectionReadResult{}, selectionMalformed(err)
	}
	if cursor != nil && (cursor.Project != req.Project || cursor.RevisionID != req.RevisionID || cursor.Scope != req.ScopeDigest) {
		return SelectionReadResult{}, selectionStale(fmt.Errorf("selection cursor is stale"))
	}
	header, err := s.store.GetSelection(ctx, req.Project, req.RevisionID)
	if err != nil {
		return SelectionReadResult{}, err
	}
	if !header.Complete {
		return SelectionReadResult{}, explorer.ErrSelectionIncomplete
	}
	if header.ScopeDigest != req.ScopeDigest {
		return SelectionReadResult{}, selectionStale(fmt.Errorf("selection authorization scope changed"))
	}
	if strings.TrimSpace(req.Generation) != "" && header.Generation != strings.TrimSpace(req.Generation) {
		return SelectionReadResult{}, selectionStale(fmt.Errorf("selection generation changed"))
	}
	after := ""
	if cursor != nil {
		if cursor.Generation != header.Generation {
			return SelectionReadResult{}, selectionStale(fmt.Errorf("selection cursor is stale"))
		}
		after = cursor.MemberKey
	}
	limit := req.Limit
	if limit <= 0 || limit > selectionBatchSize {
		limit = selectionBatchSize
	}
	members := make([]explorer.SelectionMember, 0, limit)
	next, err := s.store.VisitSelectionMembers(ctx, req.Project, req.RevisionID, after, limit, func(member explorer.SelectionMember) error {
		members = append(members, member)
		return nil
	})
	if err != nil {
		return SelectionReadResult{}, err
	}
	nextCursor := ""
	if next != "" && len(members) == limit {
		nextCursor, err = encodeSelectionCursor(selectionCursor{Version: 1, Project: req.Project, RevisionID: req.RevisionID, Generation: header.Generation, Scope: req.ScopeDigest, MemberKey: next})
		if err != nil {
			return SelectionReadResult{}, err
		}
	}
	return SelectionReadResult{Header: header, Members: members, NextCursor: nextCursor}, nil
}

// ReadSelection derives the effective current scope before loading the
// immutable header, so a stale or narrowed scope cannot disclose counts.
func (s *Service) ReadSelection(ctx context.Context, req SelectionReadIntentRequest) (result SelectionReadResult, err error) {
	result, err = s.readSelectionIntent(ctx, req)
	if err != nil {
		err = normalizeSelectionError(err)
	}
	return result, err
}

func (s *Service) readSelectionIntent(ctx context.Context, req SelectionReadIntentRequest) (SelectionReadResult, error) {
	if s == nil || s.config.Capability.Current == nil || s.config.Capability.ForExecution == nil {
		return SelectionReadResult{}, fmt.Errorf("selection authorization resolver is required")
	}
	project := projectid.Canonical(req.Project)
	current, err := s.config.Capability.Current(ctx, project, req.ExplorerID, "")
	if err != nil {
		return SelectionReadResult{}, err
	}
	currentToken := strings.TrimSpace(current.Token)
	if currentToken == "" {
		return SelectionReadResult{}, selectionStale(fmt.Errorf("current capability snapshot is unavailable"))
	}
	authorized, err := s.config.Capability.ForExecution(ctx, project, currentToken)
	if err != nil {
		return SelectionReadResult{}, err
	}
	return s.readSelectionResolved(ctx, selectionReadRequest{Project: project, ExplorerID: req.ExplorerID, RevisionID: req.RevisionID, Generation: authorized.Snapshot.Identity.Generation, Cursor: req.Cursor, Limit: req.Limit, Scope: authorized.Scope, ScopeDigest: authorized.Snapshot.Identity.AuthorizationScopeDigest})
}

func normalizeSelectionError(err error) error {
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) {
		return err
	}
	switch {
	case errors.Is(err, explorer.ErrSelectionNotAddressable):
		return unprocessable("selection", "SELECTION_SOURCE_NOT_ADDRESSABLE", "published output cannot be mapped to source resources", err)
	case errors.Is(err, explorer.ErrSelectionLimit), errors.Is(err, context.DeadlineExceeded):
		return unprocessable("selection", "SELECTION_LIMIT_EXCEEDED", "selection exceeded configured row, byte, or duration limits", err)
	case errors.Is(err, explorer.ErrSelectionConflict):
		return conflict("selection", "SELECTION_IDEMPOTENCY_CONFLICT", "selection idempotency key is bound to different immutable intent", nil, err)
	case errors.Is(err, explorer.ErrSelectionIncomplete):
		return conflict("selection", "SELECTION_NOT_COMPLETE", "selection membership is not complete", nil, err)
	case errors.Is(err, explorer.ErrSelectionStaleScope):
		return conflict("selection", "SELECTION_STALE_SCOPE", "selection authorization or immutable source identity changed", nil, err)
	case errors.Is(err, bundlepublication.ErrSelectionSourceIdentityChanged):
		return conflict("selection", "SELECTION_STALE_SCOPE", "selection authorization or immutable source identity changed", nil, err)
	case errors.Is(err, explorer.ErrResourceRefScopeMismatch):
		return conflict("selection", "SELECTION_STALE_SCOPE", "selection references are outside the authorized snapshot", nil, err)
	case errors.Is(err, explorer.ErrSelectionMalformed):
		return malformed("selection", "selection request is malformed", err)
	case errors.Is(err, explorer.ErrNotFound), errors.Is(err, explorer.ErrSelectionNotFound), errors.Is(err, bundlepublication.ErrBundleNotFound):
		return notFound("selection", "SELECTION_NOT_FOUND", "selection source or revision was not found", err)
	default:
		return err
	}
}

func selectionMalformed(err error) error {
	if err == nil {
		return explorer.ErrSelectionMalformed
	}
	return fmt.Errorf("%w: %v", explorer.ErrSelectionMalformed, err)
}

func selectionStale(err error) error {
	if err == nil {
		return explorer.ErrSelectionStaleScope
	}
	return fmt.Errorf("%w: %v", explorer.ErrSelectionStaleScope, err)
}

func exclusionSet(values []explorer.ResourceRef) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[refKey(value)] = struct{}{}
	}
	return result
}

func canonicalSelectionRefs(values []explorer.ResourceRef, project, generation, resourceType string) ([]explorer.ResourceRef, string, error) {
	result := make([]explorer.ResourceRef, len(values))
	for i, raw := range values {
		ref := raw.Canonical()
		if err := ref.Validate(project, generation, ""); err != nil {
			if errors.Is(err, explorer.ErrResourceRefScopeMismatch) {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func refKey(ref explorer.ResourceRef) string {
	ref = ref.Canonical()
	return ref.Project + "\x00" + ref.Generation + "\x00" + ref.ResourceType + "\x00" + ref.ID
}

func selectionID(project, generation, idempotency string) string {
	// The idempotency key owns the durable selection identity. A retry with the
	// same key must collide with the same header even if a caller changes its
	// rule; BeginSelection then rejects that changed immutable intent.
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
