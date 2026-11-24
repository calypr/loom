package publication

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) OpenArtifact(ctx context.Context, request ArtifactOpenRequest) (io.ReadCloser, *explorer.ArtifactRecord, error) {
	if s.config.ArtifactStore == nil {
		return nil, nil, support.Unavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured", nil)
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" || strings.TrimSpace(request.ArtifactID) == "" {
		return nil, nil, support.Malformed("artifact", "project, explorerId, and artifactId are required", nil)
	}
	record, err := s.config.ArtifactStore.Get(ctx, request.ArtifactID)
	if err != nil {
		if errors.Is(err, explorer.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, nil, support.NotFound("artifact", "ARTIFACT_NOT_FOUND", "artifact was not found", err)
		}
		return nil, nil, support.Unavailable("artifact", "ARTIFACT_STORE_UNAVAILABLE", "the artifact store is unavailable", err)
	}
	if record == nil {
		return nil, nil, support.NotFound("artifact", "ARTIFACT_NOT_FOUND", "artifact was not found", nil)
	}
	project := projectid.Canonical(request.Project)
	if projectid.Canonical(record.Project) != project || record.ExplorerID != request.ExplorerID {
		return nil, nil, support.NotFound("artifact", "ARTIFACT_NOT_FOUND", "artifact was not found", explorer.ErrNotFound)
	}
	if record.State != explorer.ArtifactComplete {
		return nil, record, support.Unavailable("artifact", "ARTIFACT_NOT_COMPLETE", "artifact is not complete", nil)
	}
	if !record.ExpiresAt.After(s.now()) {
		return nil, record, support.Unavailable("artifact", "ARTIFACT_EXPIRED", "artifact has expired", nil)
	}
	receipt, err := s.core.LookupReceipt(ctx, project, request.ExplorerID, record.ReceiptID)
	if err != nil {
		return nil, record, err
	}
	if err := s.core.ValidateArchivedReceiptRoute(receipt, project, request.ExplorerID); err != nil {
		return nil, record, err
	}
	if err := validateArtifactReceiptIdentity(*record, receipt); err != nil {
		return nil, record, support.Conflict("artifact", "ARTIFACT_IDENTITY_CHANGED", "artifact no longer matches its immutable receipt", nil, err)
	}
	authorized, snapshot, err := s.resolveExecutionCapability(ctx, project, record.SnapshotToken)
	if err != nil {
		return nil, record, authoringcore.CapabilityResolutionFailure("artifact", "the artifact capability is unavailable or unauthorized", err)
	}
	if err := authoringcore.ValidateReceiptCapability(receipt, snapshot); err != nil {
		return nil, record, support.Conflict("artifact", "RECEIPT_STALE", "the artifact receipt is no longer authorized", nil, err)
	}
	if err := support.ValidateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return nil, record, support.Forbidden("artifact", "current caller is not authorized for this artifact", err)
	}
	revision, err := s.config.Revisions.GetRevision(ctx, strings.TrimSpace(record.RevisionID))
	if err != nil {
		if errors.Is(err, explorer.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, record, support.NotFound("artifact", "ARTIFACT_REVISION_NOT_FOUND", "artifact revision was not found", err)
		}
		return nil, record, support.Unavailable("artifact", "REVISION_STORE_UNAVAILABLE", "artifact revision could not be loaded", err)
	}
	if revision == nil {
		return nil, record, support.NotFound("artifact", "ARTIFACT_REVISION_NOT_FOUND", "artifact revision was not found", nil)
	}
	if err := validateArtifactRevisionIdentity(*record, *revision); err != nil {
		return nil, record, support.Conflict("artifact", "ARTIFACT_IDENTITY_CHANGED", "artifact no longer matches its immutable revision", nil, err)
	}
	reader, opened, err := s.config.ArtifactStore.Open(ctx, request.ArtifactID)
	if err != nil {
		return nil, record, support.Unavailable("artifact", "ARTIFACT_STORE_UNAVAILABLE", "the artifact store could not open the archive", err)
	}
	if reader == nil || opened == nil || opened.State != explorer.ArtifactComplete || !opened.ExpiresAt.After(s.now()) {
		return nil, record, support.Unavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact is not available for download", fmt.Errorf("artifact store returned an incomplete or expired archive"))
	}
	return reader, opened, nil
}
