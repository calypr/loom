package selection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	loomtext "github.com/calypr/loom/internal/foundation/text"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/projectid"
	"github.com/google/uuid"
)

func (flow *selectionFlow) createSelectionWithMaterialization(ctx context.Context, req selectionCreateRequest, materialization published.Materialization, resolver SelectionSourceResolver) (SelectionCreateResult, error) {
	return flow.createSelection(ctx, req, materializationSelectionReader{materialization: materialization, resolver: resolver})
}

func (flow *selectionFlow) createSelection(parent context.Context, req selectionCreateRequest, reader SelectionPublishedReader) (SelectionCreateResult, error) {
	if flow.store == nil {
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
		req.Source.Kind = SelectionSourceExplicit
	}
	if reader == nil && req.Rule.Kind != SelectionRuleExplicit {
		return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("explicit selection source is required"))
	}
	if reader != nil && req.Source.Kind != SelectionSourcePublished {
		return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("published source identity is required"))
	}
	if reader == nil && req.Source.Kind != SelectionSourceExplicit && req.Source.Kind != SelectionSourceRevision {
		return SelectionCreateResult{}, selectionMalformed(fmt.Errorf("explicit source identity is required"))
	}
	if err := req.Source.Validate(req.Project, req.Generation, req.ResourceType); err != nil && reader == nil {
		return SelectionCreateResult{}, selectionMalformed(err)
	}
	for i := range req.Exclusions {
		req.Exclusions[i] = req.Exclusions[i].Canonical()
		if err := req.Exclusions[i].Validate(req.Project, req.Generation, req.ResourceType); err != nil {
			if errors.Is(err, ErrResourceRefScopeMismatch) {
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
			return SelectionCreateResult{}, ErrSelectionNotAddressable
		}
		if materialization.Revision == "" || materialization.SourceRow.IDColumn == "" {
			return SelectionCreateResult{}, ErrSelectionNotAddressable
		}
		req.Source.Generation = materialization.DatasetGeneration
		if materialization.SourceRevision != "" && req.Source.RevisionID != materialization.SourceRevision {
			return SelectionCreateResult{}, selectionStale(fmt.Errorf("published selection revision changed"))
		}
		req.Source.RevisionID = loomtext.FirstTrimmed(req.Source.RevisionID, materialization.SourceRevision)
		req.Source.ExecutionID = materialization.Revision
		req.Source.ReceiptID = materialization.ReceiptID
		req.Source.SchemaDigest = materialization.SchemaDigest
		req.Source.ResourceType = materialization.SourceRow.ResourceType
		req.Source.SourceIDColumn = materialization.SourceRow.IDColumn
		if req.Source.ReceiptID == "" || req.Source.SchemaDigest == "" {
			return SelectionCreateResult{}, ErrSelectionNotAddressable
		}
	}
	var requestMembers []ResourceRef
	if req.Rule.Kind == SelectionRuleExplicit && req.Source.Kind == SelectionSourceExplicit {
		requestMembers = req.Members
	}
	ruleDigest, err := SelectionRuleDigestWithMembers(req.Rule, req.Source, req.Exclusions, requestMembers)
	if err != nil {
		return SelectionCreateResult{}, err
	}
	selectionID := selectionID(req.Project, req.Generation, req.IdempotencyKey)
	header := SelectionRevision{ID: selectionID, Project: req.Project, Generation: req.Generation, ResourceType: req.ResourceType, Rule: req.Rule, Source: req.Source, Exclusions: req.Exclusions, ScopeDigest: req.ScopeDigest, RuleDigest: ruleDigest, IdempotencyKey: strings.TrimSpace(req.IdempotencyKey), CreatedAt: time.Now().UTC()}
	writerToken := uuid.NewString()
	started, err := flow.store.BeginSelection(parent, header, writerToken)
	if err != nil {
		return SelectionCreateResult{}, err
	}
	if started.Complete {
		return SelectionCreateResult{Header: started}, nil
	}
	ctx, cancel := context.WithTimeout(parent, DefaultSelectionMaxDuration)
	defer cancel()
	if req.Source.Kind == SelectionSourceRevision {
		if err := flow.appendSelectionRevision(ctx, header, writerToken, req.BaseSelectionID); err != nil {
			return SelectionCreateResult{}, errors.Join(err, flow.abortSelectionBounded(parent, header.ID, writerToken))
		}
	} else if reader == nil {
		if err := flow.appendExplicit(ctx, header, writerToken, req.Members); err != nil {
			return SelectionCreateResult{}, errors.Join(err, flow.abortSelectionBounded(parent, header.ID, writerToken))
		}
	} else {
		if err := flow.appendPublished(ctx, header, writerToken, req, reader); err != nil {
			return SelectionCreateResult{}, errors.Join(err, flow.abortSelectionBounded(parent, header.ID, writerToken))
		}
	}
	digest, count, bytes, err := flow.store.DigestSelectionMembers(ctx, header.Project, header.ID)
	if err != nil || count > DefaultSelectionMaxRows || bytes > DefaultSelectionMaxBytes {
		if err == nil {
			err = ErrSelectionLimit
		}
		return SelectionCreateResult{}, errors.Join(err, flow.abortSelectionBounded(parent, header.ID, writerToken))
	}
	completed, err := flow.store.CompleteSelection(ctx, header.ID, writerToken, digest, count, bytes, time.Now().UTC())
	if err != nil {
		return SelectionCreateResult{}, errors.Join(err, flow.abortSelectionBounded(parent, header.ID, writerToken))
	}
	return SelectionCreateResult{Header: completed}, nil
}

func (flow *selectionFlow) appendSelectionRevision(ctx context.Context, header SelectionRevision, writerToken, baseSelectionID string) error {
	excluded := exclusionSet(header.Exclusions)
	after := ""
	for {
		batch := make([]SelectionMember, 0, selectionBatchSize)
		next, err := flow.store.VisitSelectionMembers(ctx, header.Project, baseSelectionID, after, selectionBatchSize, func(member SelectionMember) error {
			if _, skip := excluded[refKey(member.Ref)]; !skip {
				batch = append(batch, SelectionMember{Ref: member.Ref})
			}
			return nil
		})
		if err != nil {
			return err
		}
		if _, err := flow.store.AppendSelectionMembers(ctx, header.ID, writerToken, batch); err != nil {
			return err
		}
		if next == "" || next == after {
			return nil
		}
		after = next
	}
}

func (flow *selectionFlow) abortSelectionBounded(parent context.Context, selectionID, writerToken string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	return flow.store.AbortSelection(cleanupCtx, selectionID, writerToken)
}

func (flow *selectionFlow) appendExplicit(ctx context.Context, header SelectionRevision, writerToken string, members []ResourceRef) error {
	excluded := exclusionSet(header.Exclusions)
	for offset := 0; offset < len(members); offset += selectionBatchSize {
		end := offset + selectionBatchSize
		if end > len(members) {
			end = len(members)
		}
		batch := make([]SelectionMember, 0, end-offset)
		for _, ref := range members[offset:end] {
			ref = ref.Canonical()
			if err := ref.Validate(header.Project, header.Generation, header.ResourceType); err != nil {
				return err
			}
			if _, skip := excluded[refKey(ref)]; skip {
				continue
			}
			batch = append(batch, SelectionMember{Ref: ref})
		}
		if _, err := flow.store.AppendSelectionMembers(ctx, header.ID, writerToken, batch); err != nil {
			return err
		}
		if int64(end) > DefaultSelectionMaxRows {
			return ErrSelectionLimit
		}
	}
	return nil
}

func (flow *selectionFlow) appendPublished(ctx context.Context, header SelectionRevision, writerToken string, req selectionCreateRequest, reader SelectionPublishedReader) error {
	materialization, err := reader.ExactExecutionMaterialization(ctx, req.Source.ExecutionID, req.Source.OutputID)
	if err != nil {
		return err
	}
	excluded := exclusionSet(header.Exclusions)
	batch := make([]SelectionMember, 0, selectionBatchSize)
	var sourceRows, sourceBytes int64
	appendBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		_, err := flow.store.AppendSelectionMembers(ctx, header.ID, writerToken, batch)
		batch = batch[:0]
		return err
	}
	streamReq := published.StreamRequest{Filters: req.Rule.Filters, AuthResourcePaths: append([]string(nil), req.Scope.AuthResourcePaths...), Unrestricted: req.Scope.Mode == authscope.ReadScopeUnrestricted}
	if err := reader.StreamExactExecution(ctx, req.Source.ExecutionID, req.Source.OutputID, streamReq, func(row map[string]any) error {
		sourceRows++
		if sourceRows > DefaultSelectionMaxRows {
			return ErrSelectionLimit
		}
		encoded, encodeErr := json.Marshal(row)
		if encodeErr != nil {
			return encodeErr
		}
		sourceBytes += int64(len(encoded))
		if sourceBytes > DefaultSelectionMaxBytes {
			return ErrSelectionLimit
		}
		resourceType, id, err := published.SourceResourceRef(materialization, row)
		if err != nil {
			return err
		}
		ref := ResourceRef{Project: header.Project, Generation: header.Generation, ResourceType: resourceType, ID: id}
		if _, skip := excluded[refKey(ref)]; skip {
			return nil
		}
		batch = append(batch, SelectionMember{Ref: ref})
		if len(batch) >= selectionBatchSize {
			return appendBatch()
		}
		return nil
	}); err != nil {
		return err
	}

	return appendBatch()
}

func (flow *selectionFlow) readSelectionResolved(ctx context.Context, req selectionReadRequest) (SelectionReadResult, error) {
	if flow.store == nil {
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
	header, err := flow.store.GetSelection(ctx, req.Project, req.RevisionID)
	if err != nil {
		return SelectionReadResult{}, err
	}
	if !header.Complete {
		return SelectionReadResult{}, ErrSelectionIncomplete
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
	members := make([]SelectionMember, 0, limit)
	next, err := flow.store.VisitSelectionMembers(ctx, req.Project, req.RevisionID, after, limit, func(member SelectionMember) error {
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
