package arango

import (
	"context"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/explorer"
)

func (s *Store) InsertRevision(ctx context.Context, revision explorer.Revision) (*explorer.Revision, error) {
	if revision.ID == "" {
		return nil, fmt.Errorf("revision ID is required")
	}
	doc, err := document(revision, revision.ID)
	if err != nil {
		return nil, err
	}
	var out *explorer.Revision
	err = s.client.QueryRows(ctx, `UPSERT { _key: @key } INSERT @doc UPDATE {} IN @@c RETURN NEW`, 1, map[string]any{"@c": RevisionsCollection, "key": revision.ID, "doc": doc}, func(row map[string]any) error { value, err := decode[explorer.Revision](row); out = &value; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

func (s *Store) GetRevision(ctx context.Context, id string) (*explorer.Revision, error) {
	var out *explorer.Revision
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": RevisionsCollection, "key": id}, func(row map[string]any) error { value, err := decode[explorer.Revision](row); out = &value; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

// TransitionRevision updates lifecycle fields only. The AQL never accepts a
// replacement definition/recipe document, preserving revision immutability.
func (s *Store) TransitionRevision(ctx context.Context, id string, status explorer.RevisionStatus, diagnostics []explorer.Diagnostic) (*explorer.Revision, error) {
	now := time.Now().UTC()
	patch := map[string]any{"status": status, "diagnostics": diagnostics}
	if status == explorer.RevisionReady {
		patch["readyAt"] = now
	}
	if status == explorer.RevisionFailed {
		patch["failedAt"] = now
	}
	var out *explorer.Revision
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key UPDATE d WITH @patch IN @@c RETURN NEW`, 1, map[string]any{"@c": RevisionsCollection, "key": id, "patch": patch}, func(row map[string]any) error { value, err := decode[explorer.Revision](row); out = &value; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}
