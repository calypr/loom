package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/calypr/loom/internal/catalog"
)

const availableColumnWitnessBatchSize = 1000

func (s *Store) PersistAvailableColumnWitnesses(ctx context.Context, build catalog.AvailableColumnWitnessBuild, witnesses []catalog.AvailabilityWitness) error {
	if err := catalog.ValidateAvailableColumnWitnessBuild(build); err != nil {
		return fmt.Errorf("persist available-column witnesses: %w", err)
	}
	existing, found, err := s.readAvailableColumnWitnessBuild(ctx, build.Key)
	if err != nil {
		return fmt.Errorf("read available-column witness build before write: %w", err)
	}
	if found && existing.Project == build.Project && existing.DatasetGeneration == build.DatasetGeneration &&
		existing.RootResourceType == build.RootResourceType && existing.SchemaVersion == build.SchemaVersion &&
		existing.InputDigest == build.InputDigest && existing.State == catalog.AvailableColumnWitnessComplete && existing.Exhaustive {
		return nil
	}
	if build.State == catalog.AvailableColumnWitnessFailed {
		return s.writeAvailableColumnWitnessBuild(ctx, build)
	}

	building := build
	building.State = catalog.AvailableColumnWitnessBuilding
	if err := s.writeAvailableColumnWitnessBuild(ctx, building); err != nil {
		return fmt.Errorf("write available-column witness building marker: %w", err)
	}

	rowsByKey := make(map[string]catalog.AvailableColumnWitness, len(witnesses))
	for _, witness := range witnesses {
		row, err := catalog.NewAvailableColumnWitness(build, witness)
		if err != nil {
			return s.failAvailableColumnWitnessBuild(ctx, build, fmt.Errorf("construct available-column witness: %w", err))
		}
		rowsByKey[row.Key] = row
	}
	keys := make([]string, 0, len(rowsByKey))
	for key := range rowsByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for start := 0; start < len(keys); start += availableColumnWitnessBatchSize {
		end := min(start+availableColumnWitnessBatchSize, len(keys))
		docs := make([]json.RawMessage, 0, end-start)
		for _, key := range keys[start:end] {
			encoded, err := json.Marshal(rowsByKey[key])
			if err != nil {
				return s.failAvailableColumnWitnessBuild(ctx, build, fmt.Errorf("encode available-column witness %s: %w", key, err))
			}
			docs = append(docs, encoded)
		}
		if err := s.client.InsertBatchRaw(ctx, catalog.AvailableColumnWitnessCollection, docs, true, ""); err != nil {
			return s.failAvailableColumnWitnessBuild(ctx, build, fmt.Errorf("write available-column witness batch: %w", err))
		}
	}

	if build.State == catalog.AvailableColumnWitnessComplete {
		if err := s.writeAvailableColumnWitnessBuild(ctx, build); err != nil {
			return s.failAvailableColumnWitnessBuild(ctx, build, fmt.Errorf("write available-column witness complete marker: %w", err))
		}
		return nil
	}
	return nil
}

func (s *Store) ReadAvailableColumnWitnesses(ctx context.Context, project, generation, rootResourceType, inputDigest string, source *catalog.RouteCoverageSource) (catalog.AvailableColumnWitnessBuild, []catalog.AvailableColumnWitness, bool, error) {
	build := catalog.NewAvailableColumnWitnessBuild(project, generation, rootResourceType, inputDigest)
	if err := catalog.ValidateAvailableColumnWitnessBuild(build); err != nil {
		return catalog.AvailableColumnWitnessBuild{}, nil, false, fmt.Errorf("read available-column witnesses: %w", err)
	}
	if source != nil {
		if err := source.Validate(); err != nil {
			return catalog.AvailableColumnWitnessBuild{}, nil, false, fmt.Errorf("read available-column witnesses: invalid source: %w", err)
		}
	}
	project = build.Project
	generation = build.DatasetGeneration
	rootResourceType = build.RootResourceType
	inputDigest = build.InputDigest

	stored, found, err := s.readAvailableColumnWitnessBuild(ctx, build.Key)
	if err != nil {
		return catalog.AvailableColumnWitnessBuild{}, nil, false, fmt.Errorf("read available-column witness build: %w", err)
	}
	if !found || stored.Project != build.Project || stored.DatasetGeneration != build.DatasetGeneration ||
		stored.RootResourceType != build.RootResourceType || stored.InputDigest != build.InputDigest ||
		stored.SchemaVersion != build.SchemaVersion {
		return catalog.AvailableColumnWitnessBuild{}, nil, false, nil
	}
	if err := catalog.ValidateAvailableColumnWitnessBuild(stored); err != nil {
		return catalog.AvailableColumnWitnessBuild{}, nil, false, fmt.Errorf("decode available-column witness build: %w", err)
	}
	if stored.State != catalog.AvailableColumnWitnessComplete || !stored.Exhaustive {
		return stored, nil, true, nil
	}

	vars := map[string]interface{}{
		"project": project, "generation": catalog.NormalizeDatasetGeneration(generation), "root_type": rootResourceType,
		"input_digest": inputDigest, "schema_version": catalog.AvailableColumnWitnessSchemaVersion,
		"source_kind": "", "source_field_path": "", "source_concept_id": "", "source_binding_id": "",
	}
	if source != nil {
		vars["source_kind"] = string(source.Kind)
		vars["source_field_path"] = source.FieldPath
		vars["source_concept_id"] = source.ConceptID
		vars["source_binding_id"] = source.BindingID
	}
	rows := make([]catalog.AvailableColumnWitness, 0)
	err = s.client.QueryRows(ctx, availableColumnWitnessesByScopeAQL, 1000, vars, func(row map[string]any) error {
		var witness catalog.AvailableColumnWitness
		if err := decodeAvailableColumnWitnessRow(row, &witness); err != nil {
			return err
		}
		if err := catalog.ValidateAvailableColumnWitness(witness); err != nil {
			return fmt.Errorf("invalid available-column witness row: %w", err)
		}
		if witness.Project != project || witness.DatasetGeneration != catalog.NormalizeDatasetGeneration(generation) ||
			witness.RootResourceType != rootResourceType || witness.InputDigest != inputDigest ||
			witness.SchemaVersion != catalog.AvailableColumnWitnessSchemaVersion {
			return fmt.Errorf("available-column witness escaped its requested scope")
		}
		rows = append(rows, witness)
		return nil
	})
	if err != nil {
		return stored, nil, true, fmt.Errorf("read available-column witness rows: %w", err)
	}
	return stored, rows, true, nil
}

func (s *Store) readAvailableColumnWitnessBuild(ctx context.Context, key string) (catalog.AvailableColumnWitnessBuild, bool, error) {
	var build catalog.AvailableColumnWitnessBuild
	found := false
	err := s.client.QueryRows(ctx, availableColumnWitnessBuildByKeyAQL, 1, map[string]interface{}{"key": key}, func(row map[string]any) error {
		if err := decodeAvailableColumnWitnessRow(row, &build); err != nil {
			return err
		}
		found = true
		return nil
	})
	return build, found, err
}

func (s *Store) writeAvailableColumnWitnessBuild(ctx context.Context, build catalog.AvailableColumnWitnessBuild) error {
	if build.State != catalog.AvailableColumnWitnessComplete {
		if err := s.client.ExecuteAQL(ctx, availableColumnWitnessBuildUnlessCompleteAQL, map[string]interface{}{
			"key": build.Key, "build": build, "complete_state": catalog.AvailableColumnWitnessComplete,
		}); err != nil {
			return fmt.Errorf("write available-column witness build: %w", err)
		}
		return nil
	}
	encoded, err := json.Marshal(build)
	if err != nil {
		return fmt.Errorf("encode available-column witness build: %w", err)
	}
	if err := s.client.InsertBatchRaw(ctx, catalog.AvailableColumnWitnessBuildCollection, []json.RawMessage{encoded}, true, ""); err != nil {
		return fmt.Errorf("write available-column witness build: %w", err)
	}
	return nil
}

func (s *Store) failAvailableColumnWitnessBuild(ctx context.Context, build catalog.AvailableColumnWitnessBuild, cause error) error {
	failed := build
	failed.State = catalog.AvailableColumnWitnessFailed
	failed.Diagnostic = cause.Error()
	failedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.writeAvailableColumnWitnessBuild(failedCtx, failed); err != nil {
		return fmt.Errorf("%w; write failed available-column witness marker: %v", cause, err)
	}
	return cause
}

func decodeAvailableColumnWitnessRow(row map[string]any, target any) error {
	encoded, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("encode available-column witness row: %w", err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		return fmt.Errorf("decode available-column witness row: %w", err)
	}
	return nil
}

const availableColumnWitnessBuildByKeyAQL = `
FOR d IN fhir_available_column_witness_builds
  FILTER d._key == @key
  LIMIT 1
  RETURN d`

const availableColumnWitnessBuildUnlessCompleteAQL = `
LET current = FIRST(
  FOR d IN fhir_available_column_witness_builds
    FILTER d._key == @key
    RETURN d
)
FILTER current == null OR current.state != @complete_state
UPSERT {_key: @key}
  INSERT @build
  UPDATE UNSET(@build, "_key") IN fhir_available_column_witness_builds`

const availableColumnWitnessesByScopeAQL = `
FOR d IN fhir_available_column_witnesses
  FILTER d.project == @project
    AND d.dataset_generation == @generation
    AND d.root_resource_type == @root_type
    AND d.schema_version == @schema_version
    AND d.input_digest == @input_digest
    AND (@source_kind == "" OR (
      d.source.Kind == @source_kind
      AND d.source.FieldPath == @source_field_path
      AND d.source.ConceptID == @source_concept_id
      AND d.source.BindingID == @source_binding_id
    ))
  SORT d._key
  RETURN d`
