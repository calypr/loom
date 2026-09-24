package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

var ErrInvalidFieldSourceMembershipBuild = errors.New("invalid field-source membership build")

func (s *Store) PrepareFieldSourceMembership(ctx context.Context) error {
	return s.client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: catalog.FieldSourceMembershipCollection, Indexes: [][]string{
			{"project", "dataset_generation", "resource_type", "scalar_paths[*]"},
		}},
		{Name: catalog.FieldSourceMembershipBuildCollection, Indexes: [][]string{
			{"project", "dataset_generation"},
		}},
	}})
}

// WriteFieldSourceMemberships replaces deterministic source sidecars, making
// load replay and bounded backfill retries idempotent.
func (s *Store) WriteFieldSourceMemberships(ctx context.Context, memberships []catalog.FieldSourceMembership, batchSize int) error {
	if batchSize < 1 || len(memberships) == 0 {
		return nil
	}
	if batchSize > 5000 {
		batchSize = 5000
	}
	for start := 0; start < len(memberships); start += batchSize {
		end := min(start+batchSize, len(memberships))
		docs := make([]json.RawMessage, 0, end-start)
		for _, membership := range memberships[start:end] {
			if membership.Key == "" || membership.Project == "" || membership.DatasetGeneration == "" || membership.ResourceType == "" || membership.VertexID == "" {
				return fmt.Errorf("%w: sidecar identity is incomplete", ErrInvalidFieldSourceMembershipBuild)
			}
			encoded, err := json.Marshal(membership)
			if err != nil {
				return fmt.Errorf("encode field-source membership %s: %w", membership.Key, err)
			}
			docs = append(docs, encoded)
		}
		if err := s.client.InsertBatchRaw(ctx, catalog.FieldSourceMembershipCollection, docs, true, ""); err != nil {
			return fmt.Errorf("write field-source memberships: %w", err)
		}
	}
	return nil
}

func (s *Store) ReadFieldSourceMembershipBuild(ctx context.Context, project, generation string) (catalog.FieldSourceMembershipBuild, bool, error) {
	build := catalog.NewFieldSourceMembershipBuild(project, generation)
	var found bool
	err := s.client.QueryRows(ctx, fieldSourceMembershipBuildByKeyAQL, 1, map[string]interface{}{
		"key": build.Key,
	}, func(row map[string]any) error {
		if err := decodeInventoryRow(row, &build); err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return catalog.FieldSourceMembershipBuild{}, false, err
	}
	return build, found, nil
}

func (s *Store) WriteFieldSourceMembershipBuild(ctx context.Context, build catalog.FieldSourceMembershipBuild) error {
	if build.Key == "" || build.Project == "" || build.DatasetGeneration == "" || build.SchemaVersion != catalog.FieldSourceMembershipSchemaVersion ||
		(build.State != catalog.FieldSourceMembershipBuilding && build.State != catalog.FieldSourceMembershipComplete && build.State != catalog.FieldSourceMembershipFailed) {
		return ErrInvalidFieldSourceMembershipBuild
	}
	encoded, err := json.Marshal(build)
	if err != nil {
		return fmt.Errorf("encode field-source membership build: %w", err)
	}
	if err := s.client.InsertBatchRaw(ctx, catalog.FieldSourceMembershipBuildCollection, []json.RawMessage{encoded}, true, ""); err != nil {
		return fmt.Errorf("write field-source membership build: %w", err)
	}
	return nil
}

const fieldSourceMembershipBuildByKeyAQL = `
FOR d IN fhir_field_source_membership_builds
  FILTER d._key == @key
  LIMIT 1
  RETURN d`
