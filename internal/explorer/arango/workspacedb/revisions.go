package workspacedb

import (
	"context"
	"fmt"
	"github.com/calypr/loom/internal/explorer/arango/db"
	"reflect"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/explorer"
)

func (s *RevisionStore) InsertRevision(ctx context.Context, revision explorer.Revision) (*explorer.Revision, error) {
	if revision.ID == "" {
		return nil, fmt.Errorf("revision ID is required")
	}
	doc, err := db.Document(revision, revision.ID)
	if err != nil {
		return nil, err
	}
	var (
		out      *explorer.Revision
		existing *explorer.Revision
	)
	err = s.client.QueryRows(ctx, `UPSERT { _key: @key } INSERT @doc UPDATE {} IN @@c RETURN {new: NEW, old: OLD}`, 1, map[string]any{"@c": db.RevisionsCollection, "key": revision.ID, "doc": doc}, func(row map[string]any) error {
		if value, ok := row["new"]; ok && value != nil {
			decoded, err := db.Decode[explorer.Revision](value)
			if err != nil {
				return err
			}
			out = &decoded
		}
		if value, ok := row["old"]; ok && value != nil {
			decoded, err := db.Decode[explorer.Revision](value)
			if err != nil {
				return err
			}
			existing = &decoded
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if existing != nil && !SameRevisionContent(*existing, revision) {
		return nil, explorer.ErrImmutableRevision
	}
	if out == nil && existing != nil {
		out = existing
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

func SameRevisionContent(left, right explorer.Revision) bool {
	leftContent := revisionImmutableContent(left)
	rightContent := revisionImmutableContent(right)
	return reflect.DeepEqual(leftContent, rightContent)
}

type immutableRevision struct {
	ID                   string
	Project              string
	ExplorerID           string
	Config               []byte
	AuthoringBundle      []byte
	IntentDigest         string
	CompilationReceiptID string
	PublicOutputContract []byte
	Recipe               any
	RecipeDigest         string
	ResolvedSchemaDigest string
	SourceGeneration     string
	Materializations     []explorer.Materialization
	EmittedColumns       []explorer.EmittedColumn
	Dataset              explorer.DatasetMetadata
	QualityReports       []publication.QualityReport
}

func revisionImmutableContent(value explorer.Revision) immutableRevision {
	return immutableRevision{
		ID: value.ID, Project: value.Project, ExplorerID: value.ExplorerID,
		Config: append([]byte(nil), value.Config...), AuthoringBundle: append([]byte(nil), value.AuthoringBundle...), IntentDigest: value.IntentDigest,
		CompilationReceiptID: value.CompilationReceiptID, PublicOutputContract: append([]byte(nil), value.PublicOutputContract...), Recipe: value.Recipe,
		RecipeDigest: value.RecipeDigest, ResolvedSchemaDigest: value.ResolvedSchemaDigest, SourceGeneration: value.SourceGeneration,
		Materializations: append([]explorer.Materialization(nil), value.Materializations...), EmittedColumns: append([]explorer.EmittedColumn(nil), value.EmittedColumns...), Dataset: value.Dataset,
		QualityReports: publication.CloneQualityReports(value.QualityReports),
	}
}

func (s *RevisionStore) GetRevision(ctx context.Context, id string) (*explorer.Revision, error) {
	var out *explorer.Revision
	err := s.client.QueryRows(ctx, db.MigrationJSONQuery(`FOR d IN @@c FILTER d._key == @key RETURN d`, true), 1, map[string]any{"@c": db.RevisionsCollection, "key": id}, func(row map[string]any) error {
		value, err := db.DecodeMigrationJSON[explorer.Revision](row["migrationJSON"])
		out = &value
		return err
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

func (s *RevisionStore) FailRevision(ctx context.Context, id string, diagnostics []explorer.Diagnostic) (*explorer.Revision, error) {
	now := time.Now().UTC()
	patch := map[string]any{"status": explorer.RevisionFailed, "diagnostics": diagnostics, "failedAt": now}
	var out *explorer.Revision
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key UPDATE d WITH @patch IN @@c RETURN NEW`, 1, map[string]any{"@c": db.RevisionsCollection, "key": id, "patch": patch}, func(row map[string]any) error {
		value, err := db.Decode[explorer.Revision](row)
		out = &value
		return err
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}
