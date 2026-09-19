package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	store "github.com/calypr/loom/internal/store/arango"
)

var (
	ErrInvalidSemanticInventoryPage  = errors.New("invalid semantic inventory page request")
	ErrSemanticInventoryClaimed      = errors.New("semantic inventory backfill is already claimed")
	ErrSemanticInventoryClaimLost    = errors.New("semantic inventory backfill claim was lost")
	ErrRetainedSemanticSourceMissing = errors.New("retained semantic inventory source is missing despite field-profile evidence")
)

type RetainedSemanticInventoryRow struct {
	Key              string          `json:"key"`
	AuthResourcePath *string         `json:"auth_resource_path"`
	Payload          json.RawMessage `json:"payload"`
}

type RetainedSemanticInventoryPage struct {
	Rows         []RetainedSemanticInventoryRow
	HasMore      bool
	SourceExists bool
}

// PrepareSemanticInventoryBackfill bootstraps only the derived inventory
// collections. Retained resource collections are never created or changed.
func (s *Store) PrepareSemanticInventoryBackfill(ctx context.Context) error {
	return s.client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: catalog.SemanticInventoryCollection, Indexes: [][]string{
			{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"},
			{"project", "dataset_generation", "build_id", "source_id"},
		}},
		{Name: catalog.SemanticInventoryBuildCollection, Indexes: [][]string{
			{"project", "dataset_generation"},
		}},
	}})
}

// ReadRetainedSemanticInventoryPage returns at most limit retained source
// vertices in stable _key order, without loading a resource collection into
// application memory.
func (s *Store) ReadRetainedSemanticInventoryPage(ctx context.Context, project, generation, collection, afterKey string, limit int) (RetainedSemanticInventoryPage, error) {
	canonical, ok := fhirschema.ConcreteResourceType(collection)
	if !ok || canonical != collection || strings.TrimSpace(project) == "" || catalog.NormalizeDatasetGeneration(generation) == "" || limit < 1 || limit > 1000 {
		return RetainedSemanticInventoryPage{}, ErrInvalidSemanticInventoryPage
	}
	exists, err := s.client.CollectionExists(ctx, collection)
	if err != nil {
		return RetainedSemanticInventoryPage{}, err
	}
	if !exists {
		profileEvidence, err := s.retainedSourceFieldProfileExists(ctx, project, generation, collection)
		if err != nil {
			return RetainedSemanticInventoryPage{}, err
		}
		if profileEvidence {
			return RetainedSemanticInventoryPage{}, fmt.Errorf("%w: %s/%s/%s", ErrRetainedSemanticSourceMissing, project, generation, collection)
		}
		return RetainedSemanticInventoryPage{Rows: []RetainedSemanticInventoryRow{}}, nil
	}
	page := RetainedSemanticInventoryPage{Rows: make([]RetainedSemanticInventoryRow, 0, limit), SourceExists: true}
	err = s.client.QueryRows(ctx, retainedSemanticInventorySourcePageAQL, limit+1, map[string]interface{}{
		"@collection":        collection,
		"project":            project,
		"dataset_generation": catalog.NormalizeDatasetGeneration(generation),
		"after_key":          afterKey,
		"limit":              limit + 1,
	}, func(row map[string]any) error {
		var source RetainedSemanticInventoryRow
		if err := decodeInventoryRow(row, &source); err != nil {
			return err
		}
		if source.Key == "" {
			return fmt.Errorf("retained %s source row is missing _key", collection)
		}
		page.Rows = append(page.Rows, source)
		return nil
	})
	if err != nil {
		return RetainedSemanticInventoryPage{}, fmt.Errorf("read retained %s source page: %w", collection, err)
	}
	if len(page.Rows) > limit {
		page.Rows = page.Rows[:limit]
		page.HasMore = true
	}
	if len(page.Rows) == 0 && afterKey == "" {
		profileEvidence, err := s.retainedSourceFieldProfileExists(ctx, project, generation, collection)
		if err != nil {
			return RetainedSemanticInventoryPage{}, err
		}
		if profileEvidence {
			return RetainedSemanticInventoryPage{}, fmt.Errorf("%w: %s/%s/%s has no retained rows", ErrRetainedSemanticSourceMissing, project, generation, collection)
		}
	}
	return page, nil
}

func (s *Store) retainedSourceFieldProfileExists(ctx context.Context, project, generation, resourceType string) (bool, error) {
	exists, err := s.client.CollectionExists(ctx, catalog.FieldCatalogCollection)
	if err != nil || !exists {
		return false, err
	}
	found := false
	err = s.client.QueryRows(ctx, retainedSemanticInventoryFieldProfileAQL, 1, map[string]interface{}{
		"project": project, "dataset_generation": catalog.NormalizeDatasetGeneration(generation), "resource_type": resourceType,
	}, func(map[string]any) error {
		found = true
		return nil
	})
	return found, err
}

// ClaimSemanticInventoryBackfill atomically acquires or resumes the retained
// source scanner. A complete build is returned with claimed=false and is not
// rewritten, which prevents file and retained-vertex contribution sets from
// being mixed by a second scan.
func (s *Store) ClaimSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, lease time.Duration) (catalog.SemanticInventoryBuild, bool, error) {
	if err := catalog.ValidateSemanticInventoryBuild(build); err != nil {
		return catalog.SemanticInventoryBuild{}, false, err
	}
	if build.SourceKind != catalog.SemanticInventorySourceRetained || strings.TrimSpace(token) == "" || lease <= 0 {
		return catalog.SemanticInventoryBuild{}, false, errors.New("invalid semantic inventory backfill claim")
	}
	build.State = catalog.SemanticInventoryRunning
	build.SourceCheckpoint = catalog.SemanticInventoryCheckpoint{}
	build.ScannedResources = 0
	build.Checkpoint = ""
	build.Diagnostics = nil
	now := time.Now()
	var claimed catalog.SemanticInventoryBuild
	found := false
	err := s.client.QueryRows(ctx, claimSemanticInventoryBackfillAQL, 1, map[string]interface{}{
		"key":              build.Key,
		"build":            build,
		"source_kind":      catalog.SemanticInventorySourceRetained,
		"token":            token,
		"now":              now.UnixMilli(),
		"lease_expires_at": now.Add(lease).UnixMilli(),
	}, func(row map[string]any) error {
		if err := decodeInventoryRow(row, &claimed); err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return catalog.SemanticInventoryBuild{}, false, fmt.Errorf("claim semantic inventory backfill: %w", err)
	}
	if found {
		return claimed, true, nil
	}
	current, exists, err := s.semanticInventoryBuildByKey(ctx, build.Key)
	if err != nil {
		return catalog.SemanticInventoryBuild{}, false, err
	}
	if exists && current.State == catalog.SemanticInventoryComplete {
		return current, false, nil
	}
	return catalog.SemanticInventoryBuild{}, false, ErrSemanticInventoryClaimed
}

func (s *Store) AdvanceSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, expected catalog.SemanticInventoryCheckpoint, next catalog.SemanticInventoryCheckpoint, scanned int64, lease time.Duration) (catalog.SemanticInventoryBuild, error) {
	now := time.Now()
	return s.executeSemanticInventoryBackfill(ctx, advanceSemanticInventoryBackfillAQL, map[string]interface{}{
		"key": build.Key, "token": token,
		"expected_collection": expected.Collection, "expected_key": expected.Key,
		"next_collection": next.Collection, "next_key": next.Key,
		"scanned_resources": max(scanned, 0), "now": now.UnixMilli(),
		"lease_expires_at": now.Add(lease).UnixMilli(), "source_availability": build.SourceAvailability,
	})
}

func (s *Store) CompleteSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, expected catalog.SemanticInventoryCheckpoint, scanned int64) (catalog.SemanticInventoryBuild, error) {
	return s.executeSemanticInventoryBackfill(ctx, completeSemanticInventoryBackfillAQL, map[string]interface{}{
		"key": build.Key, "token": token,
		"expected_collection": expected.Collection, "expected_key": expected.Key,
		"scanned_resources": max(scanned, 0), "now": time.Now().UnixMilli(),
		"source_availability": build.SourceAvailability,
	})
}

func (s *Store) FailSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token, diagnostic string) error {
	if strings.TrimSpace(diagnostic) == "" {
		diagnostic = "backfill failed"
	}
	if len(diagnostic) > 512 {
		diagnostic = diagnostic[:512]
	}
	_, err := s.executeSemanticInventoryBackfill(ctx, failSemanticInventoryBackfillAQL, map[string]interface{}{
		"key": build.Key, "token": token, "diagnostic": diagnostic,
	})
	return err
}

func (s *Store) executeSemanticInventoryBackfill(ctx context.Context, query string, bindVars map[string]interface{}) (catalog.SemanticInventoryBuild, error) {
	var updated catalog.SemanticInventoryBuild
	found := false
	err := s.client.QueryRows(ctx, query, 1, bindVars, func(row map[string]any) error {
		if err := decodeInventoryRow(row, &updated); err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return catalog.SemanticInventoryBuild{}, fmt.Errorf("update semantic inventory backfill: %w", err)
	}
	if !found {
		return catalog.SemanticInventoryBuild{}, ErrSemanticInventoryClaimLost
	}
	return updated, nil
}

func (s *Store) BeginSemanticInventoryBuild(ctx context.Context, build catalog.SemanticInventoryBuild) error {
	if err := catalog.ValidateSemanticInventoryBuild(build); err != nil {
		return err
	}
	current, found, err := s.semanticInventoryBuildByKey(ctx, build.Key)
	if err != nil {
		return err
	}
	if found && current.State == catalog.SemanticInventoryComplete {
		return nil
	}
	build.State = catalog.SemanticInventoryRunning
	build.ScannedResources = 0
	build.Checkpoint = ""
	build.Diagnostics = nil
	return s.writeSemanticInventoryBuild(ctx, build)
}

func (s *Store) AdvanceSemanticInventoryBuild(ctx context.Context, build catalog.SemanticInventoryBuild, scannedResources int64, checkpoint string) error {
	if err := catalog.ValidateSemanticInventoryBuild(build); err != nil {
		return err
	}
	current, found, err := s.semanticInventoryBuildByKey(ctx, build.Key)
	if err != nil {
		return err
	}
	if found && current.State == catalog.SemanticInventoryComplete {
		return nil
	}
	build.State = catalog.SemanticInventoryRunning
	build.ScannedResources = max(scannedResources, 0)
	build.Checkpoint = checkpoint
	build.Diagnostics = nil
	return s.writeSemanticInventoryBuild(ctx, build)
}

func (s *Store) CompleteSemanticInventoryBuild(ctx context.Context, build catalog.SemanticInventoryBuild, scannedResources int64, checkpoint string) error {
	if err := catalog.ValidateSemanticInventoryBuild(build); err != nil {
		return err
	}
	build.State = catalog.SemanticInventoryComplete
	build.ScannedResources = max(scannedResources, 0)
	build.Checkpoint = checkpoint
	build.Diagnostics = nil
	return s.writeSemanticInventoryBuild(ctx, build)
}

func (s *Store) FailSemanticInventoryBuild(ctx context.Context, build catalog.SemanticInventoryBuild, diagnostic string) error {
	if err := catalog.ValidateSemanticInventoryBuild(build); err != nil {
		return err
	}
	current, found, err := s.semanticInventoryBuildByKey(ctx, build.Key)
	if err != nil {
		return err
	}
	if found && current.State == catalog.SemanticInventoryComplete {
		return nil
	}
	build.State = catalog.SemanticInventoryFailed
	if found {
		build.ScannedResources = current.ScannedResources
		build.Checkpoint = current.Checkpoint
	}
	if text := strings.TrimSpace(diagnostic); text != "" {
		if len(text) > 512 {
			text = text[:512]
		}
		build.Diagnostics = []string{text}
	}
	return s.writeSemanticInventoryBuild(ctx, build)
}

func (s *Store) WriteSemanticInventoryContributions(ctx context.Context, contributions []catalog.SemanticInventoryContribution, batchSize int) error {
	if len(contributions) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	encoded := make([]json.RawMessage, 0, min(batchSize, len(contributions)))
	for start := 0; start < len(contributions); start += batchSize {
		end := min(start+batchSize, len(contributions))
		encoded = encoded[:0]
		for _, contribution := range contributions[start:end] {
			if err := validateSemanticInventoryContribution(contribution); err != nil {
				return err
			}
			data, err := json.Marshal(contribution)
			if err != nil {
				return fmt.Errorf("encode semantic inventory contribution: %w", err)
			}
			encoded = append(encoded, data)
		}
		if err := s.client.InsertBatchRaw(ctx, catalog.SemanticInventoryCollection, encoded, true, ""); err != nil {
			return fmt.Errorf("write semantic inventory contributions: %w", err)
		}
	}
	return nil
}

func (s *Store) PageSemanticInventory(ctx context.Context, opts catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
	if strings.TrimSpace(opts.Project) == "" || catalog.NormalizeDatasetGeneration(opts.DatasetGeneration) == "" {
		return catalog.SemanticInventoryPage{}, ErrInvalidSemanticInventoryPage
	}
	paths := append([]string(nil), opts.AuthResourcePaths...)
	sort.Strings(paths)
	paths = compactAuthPaths(paths)
	unrestricted := catalog.EffectiveAuthResourcePathsUnrestricted(paths, opts.AuthResourcePathsUnrestricted)
	limit := semanticInventoryLimit(opts.Limit)
	buildsExist, err := s.client.CollectionExists(ctx, catalog.SemanticInventoryBuildCollection)
	if err != nil {
		return catalog.SemanticInventoryPage{}, err
	}
	if !buildsExist {
		return catalog.SemanticInventoryPage{Entries: []catalog.SemanticInventoryEntry{}, State: catalog.SemanticInventoryUnknown}, nil
	}
	builds, err := s.semanticInventoryBuilds(ctx, opts.Project, catalog.NormalizeDatasetGeneration(opts.DatasetGeneration))
	if err != nil {
		return catalog.SemanticInventoryPage{}, err
	}
	build, found := selectSemanticInventoryBuild(builds, catalog.SemanticInventoryBuildID(opts.Project, opts.DatasetGeneration))
	if !found {
		return catalog.SemanticInventoryPage{Entries: []catalog.SemanticInventoryEntry{}, State: catalog.SemanticInventoryUnknown}, nil
	}
	page := catalog.SemanticInventoryPage{Entries: []catalog.SemanticInventoryEntry{}, Build: build, State: build.State}
	if !unrestricted {
		page.Build.ScannedResources = 0
		page.Build.Checkpoint = ""
		page.Build.SourceCheckpoint = catalog.SemanticInventoryCheckpoint{}
		page.Build.LeaseToken = ""
		page.Build.LeaseExpiresAt = 0
		page.Build.Diagnostics = nil
		page.Build.AuthResourcePath = ""
	}
	if build.State != catalog.SemanticInventoryComplete || build.BuildID == "" {
		return page, nil
	}
	sourceKind := build.SourceKind
	if sourceKind == "" {
		sourceKind = catalog.SemanticInventorySourceFile
	}
	inventoryExists, err := s.client.CollectionExists(ctx, catalog.SemanticInventoryCollection)
	if err != nil {
		return catalog.SemanticInventoryPage{}, err
	}
	if !inventoryExists {
		return catalog.SemanticInventoryPage{Entries: []catalog.SemanticInventoryEntry{}, State: catalog.SemanticInventoryUnknown}, nil
	}
	afterBindingID, afterConceptID, err := catalog.DecodeSemanticInventoryCursor(opts.Cursor, opts, build.BuildID)
	if err != nil {
		return catalog.SemanticInventoryPage{}, err
	}
	vars := map[string]interface{}{
		"project":                          opts.Project,
		"dataset_generation":               catalog.NormalizeDatasetGeneration(opts.DatasetGeneration),
		"build_id":                         build.BuildID,
		"source_kind":                      sourceKind,
		"auth_resource_paths_unrestricted": unrestricted,
		"auth_resource_paths":              paths,
		"resource_type":                    opts.ResourceType,
		"query":                            opts.Query,
		"observation_schema":               build.ObservationSchema,
		"after_binding_id":                 afterBindingID,
		"after_concept_id":                 afterConceptID,
		"limit":                            limit + 1,
	}
	if err := s.client.QueryRows(ctx, semanticInventoryPageAQL, limit+1, vars, func(row map[string]any) error {
		var entry catalog.SemanticInventoryEntry
		if err := decodeInventoryRow(row, &entry); err != nil {
			return err
		}
		page.Entries = append(page.Entries, entry)
		return nil
	}); err != nil {
		return catalog.SemanticInventoryPage{}, fmt.Errorf("page semantic inventory: %w", err)
	}
	if len(page.Entries) > limit {
		page.Entries = page.Entries[:limit]
		last := page.Entries[len(page.Entries)-1]
		page.NextCursor = catalog.EncodeSemanticInventoryCursor(opts, build.BuildID, last.BindingID, last.ConceptID)
	}
	return page, nil
}

func (s *Store) semanticInventoryBuildByKey(ctx context.Context, key string) (catalog.SemanticInventoryBuild, bool, error) {
	var build catalog.SemanticInventoryBuild
	found := false
	err := s.client.QueryRows(ctx, semanticInventoryBuildByKeyAQL, 1, map[string]interface{}{"key": key}, func(row map[string]any) error {
		if err := decodeInventoryRow(row, &build); err != nil {
			return err
		}
		found = true
		return nil
	})
	return build, found, err
}

func (s *Store) semanticInventoryBuilds(ctx context.Context, project, generation string) ([]catalog.SemanticInventoryBuild, error) {
	builds := make([]catalog.SemanticInventoryBuild, 0, 2)
	err := s.client.QueryRows(ctx, semanticInventoryBuildsAQL, 32, map[string]interface{}{
		"project":            project,
		"dataset_generation": generation,
	}, func(row map[string]any) error {
		var build catalog.SemanticInventoryBuild
		if err := decodeInventoryRow(row, &build); err != nil {
			return err
		}
		builds = append(builds, build)
		return nil
	})
	return builds, err
}

func (s *Store) writeSemanticInventoryBuild(ctx context.Context, build catalog.SemanticInventoryBuild) error {
	encoded, err := json.Marshal(build)
	if err != nil {
		return fmt.Errorf("encode semantic inventory build: %w", err)
	}
	if err := s.client.InsertBatchRaw(ctx, catalog.SemanticInventoryBuildCollection, []json.RawMessage{encoded}, true, ""); err != nil {
		return fmt.Errorf("write semantic inventory build: %w", err)
	}
	return nil
}

func decodeInventoryRow(row map[string]any, out any) error {
	encoded, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("encode semantic inventory row: %w", err)
	}
	if err := json.Unmarshal(encoded, out); err != nil {
		return fmt.Errorf("decode semantic inventory row: %w", err)
	}
	return nil
}

func validateSemanticInventoryContribution(contribution catalog.SemanticInventoryContribution) error {
	if contribution.Key == "" || contribution.Project == "" || contribution.DatasetGeneration == "" || contribution.BuildID == "" || contribution.SourceID == "" || contribution.ConceptID == "" || contribution.BindingID == "" || contribution.Ordinal < 0 {
		return fmt.Errorf("semantic inventory contribution is missing stable identity")
	}
	if contribution.BuildID != catalog.SemanticInventoryBuildID(contribution.Project, contribution.DatasetGeneration) {
		return fmt.Errorf("semantic inventory contribution build does not match its generation")
	}
	return nil
}

func selectSemanticInventoryBuild(builds []catalog.SemanticInventoryBuild, currentBuildID string) (catalog.SemanticInventoryBuild, bool) {
	if len(builds) == 0 {
		return catalog.SemanticInventoryBuild{}, false
	}
	sort.Slice(builds, func(i, j int) bool {
		if builds[i].InventorySchema != builds[j].InventorySchema {
			return builds[i].InventorySchema > builds[j].InventorySchema
		}
		if builds[i].ObservationSchema != builds[j].ObservationSchema {
			return builds[i].ObservationSchema > builds[j].ObservationSchema
		}
		if builds[i].RuleVersion != builds[j].RuleVersion {
			return builds[i].RuleVersion > builds[j].RuleVersion
		}
		return builds[i].BuildID > builds[j].BuildID
	})
	for _, build := range builds {
		if build.BuildID == currentBuildID && build.State == catalog.SemanticInventoryComplete {
			return build, true
		}
	}
	for _, build := range builds {
		if build.State == catalog.SemanticInventoryComplete {
			return build, true
		}
	}
	for _, build := range builds {
		if build.BuildID == currentBuildID {
			return build, true
		}
	}
	return builds[0], true
}

func semanticInventoryLimit(limit int) int {
	if limit <= 0 || limit > catalog.SemanticInventoryPageLimit {
		return catalog.SemanticInventoryPageLimit
	}
	return limit
}

func compactAuthPaths(paths []string) []string {
	if len(paths) < 2 {
		return paths
	}
	write := 1
	for read := 1; read < len(paths); read++ {
		if paths[read] != paths[write-1] {
			paths[write] = paths[read]
			write++
		}
	}
	return paths[:write]
}

const semanticInventoryBuildByKeyAQL = `
FOR d IN fhir_semantic_inventory_builds
  FILTER d._key == @key
  LIMIT 1
  RETURN d`

const semanticInventoryBuildsAQL = `
FOR d IN fhir_semantic_inventory_builds
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  SORT d.inventory_schema DESC, d.observation_schema DESC, d.rule_version DESC, d.build_id DESC
  RETURN d`

const semanticInventoryPageAQL = `
LET identities = (
  FOR candidate IN fhir_semantic_inventory
    FILTER candidate.project == @project
    FILTER candidate.dataset_generation == @dataset_generation
    FILTER candidate.build_id == @build_id
    FILTER NOT_NULL(candidate.source_kind, "file") == @source_kind
    FILTER @auth_resource_paths_unrestricted == true OR candidate.auth_resource_path IN @auth_resource_paths
    FILTER @resource_type == "" OR candidate.resource_type == @resource_type
    FILTER @query == "" OR CONTAINS(LOWER(NOT_NULL(candidate.observation.key.system, "")), LOWER(@query))
      OR CONTAINS(LOWER(NOT_NULL(candidate.observation.key.code, "")), LOWER(@query))
      OR CONTAINS(LOWER(NOT_NULL(candidate.observation.key.display, "")), LOWER(@query))
    FILTER @after_binding_id == "" OR candidate.binding_id > @after_binding_id
      OR (candidate.binding_id == @after_binding_id AND candidate.concept_id > @after_concept_id)
    COLLECT binding_id = candidate.binding_id, concept_id = candidate.concept_id
    AGGREGATE matched_display = MIN(candidate.observation.key.display)
    SORT binding_id, concept_id
    LIMIT @limit
    RETURN {binding_id: binding_id, concept_id: concept_id, matched_display: matched_display}
)
FOR identity IN identities
  LET observation_rows = (
    FOR d IN fhir_semantic_inventory
      FILTER d.project == @project
      FILTER d.dataset_generation == @dataset_generation
      FILTER d.build_id == @build_id
      FILTER NOT_NULL(d.source_kind, "file") == @source_kind
      FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
      FILTER @resource_type == "" OR d.resource_type == @resource_type
      FILTER d.binding_id == identity.binding_id
      FILTER d.concept_id == identity.concept_id
      COLLECT AGGREGATE population = SUM(d.observation.population),
        example = MIN(d.example),
        canonical = MIN(d.observation.source.canonical),
        resource_type = MIN(d.observation.source.type),
        profile = MIN(d.observation.source.profile),
        path = MIN(d.observation.source.path),
        key_selector = MIN(d.observation.key.selector),
        system = MIN(d.observation.key.system),
        version = MIN(d.observation.key.version),
        code = MIN(d.observation.key.code),
        value_selector = MIN(d.observation.value.selector),
        value_type = MIN(d.observation.value.type),
        owning_scope = MIN(d.observation.owning_scope),
        extension_url_path = MIN(d.observation.extension_url_path),
        choice_arm = MIN(d.observation.choice_arm),
        logical_type = MIN(d.observation.logical_type),
        units_min = MIN(d.observation.observed_units),
        units_max = MAX(d.observation.observed_units),
        completeness = MIN(d.observation.completeness),
        status = MIN(d.observation.status),
        rule_hint = MIN(d.observation.rule_hint),
        rule_version = MIN(d.observation.rule_version),
        examples_truncated = MAX(d.observation.examples_truncated ? 1 : 0)
      RETURN {
        concept_id: identity.concept_id,
        binding_id: identity.binding_id,
        observation: {
          schema_version: @observation_schema,
          source: {canonical: canonical, type: resource_type, profile: profile, path: path},
          key: {selector: key_selector, system: system, version: version, code: code, display: identity.matched_display},
          value: {selector: value_selector, type: value_type},
          owning_scope: owning_scope,
          extension_url_path: extension_url_path,
          choice_arm: choice_arm,
          logical_type: logical_type,
          observed_units: units_min == null ? [] : units_min,
          observed_units_truncated: units_min != units_max,
          completeness: completeness,
          status: status,
          population: population,
          examples: example == null || example == "" ? [] : [example],
          examples_truncated: examples_truncated > 0 OR population > 1,
          rule_hint: rule_hint,
          rule_version: rule_version
        }
      }
  )
  RETURN FIRST(observation_rows)`

const retainedSemanticInventorySourcePageAQL = `
FOR d IN @@collection
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @after_key == "" OR d._key > @after_key
  SORT d._key
  LIMIT @limit
  RETURN {key: d._key, auth_resource_path: d.auth_resource_path, payload: d.payload}`

const retainedSemanticInventoryFieldProfileAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.resource_type == @resource_type
  FILTER d.doc_count > 0
  LIMIT 1
  RETURN {doc_count: d.doc_count}`

const claimSemanticInventoryBackfillAQL = `
LET current_build = DOCUMENT("fhir_semantic_inventory_builds", @key)
FILTER current_build == null OR (
  current_build.state != "complete"
  AND (NOT_NULL(current_build.lease_expires_at, 0) <= @now OR current_build.lease_token == @token)
)
UPSERT {_key: @key}
  INSERT MERGE(@build, {
    state: "running",
    source_kind: @source_kind,
    source_availability: "unknown",
    source_checkpoint: {collection: "", key: ""},
    lease_token: @token,
    lease_expires_at: @lease_expires_at
  })
  UPDATE MERGE(OLD, {
    state: "running",
    source_kind: @source_kind,
    source_availability: OLD.source_kind == @source_kind ? NOT_NULL(OLD.source_availability, "unknown") : "unknown",
    scanned_resources: OLD.source_kind == @source_kind ? NOT_NULL(OLD.scanned_resources, 0) : 0,
    checkpoint: "",
    source_checkpoint: OLD.source_kind == @source_kind ? NOT_NULL(OLD.source_checkpoint, {collection: "", key: ""}) : {collection: "", key: ""},
    diagnostics: [],
    lease_token: @token,
    lease_expires_at: @lease_expires_at
  })
  IN fhir_semantic_inventory_builds
  OPTIONS {exclusive: true}
RETURN NEW`

const advanceSemanticInventoryBackfillAQL = `
FOR d IN fhir_semantic_inventory_builds
  FILTER d._key == @key
  FILTER d.state == "running"
  FILTER d.lease_token == @token
  FILTER d.lease_expires_at > @now
  FILTER NOT_NULL(d.source_checkpoint.collection, "") == @expected_collection
  FILTER NOT_NULL(d.source_checkpoint.key, "") == @expected_key
  UPDATE d WITH {
    source_checkpoint: {collection: @next_collection, key: @next_key},
    scanned_resources: @scanned_resources,
    source_availability: @source_availability,
    lease_expires_at: @lease_expires_at,
    diagnostics: []
  } IN fhir_semantic_inventory_builds
  OPTIONS {exclusive: true}
  RETURN NEW`

const completeSemanticInventoryBackfillAQL = `
FOR d IN fhir_semantic_inventory_builds
  FILTER d._key == @key
  FILTER d.state == "running"
  FILTER d.lease_token == @token
  FILTER d.lease_expires_at > @now
  FILTER NOT_NULL(d.source_checkpoint.collection, "") == @expected_collection
  FILTER NOT_NULL(d.source_checkpoint.key, "") == @expected_key
  UPDATE d WITH {
    state: "complete",
    scanned_resources: @scanned_resources,
    source_availability: @source_availability,
    lease_token: "",
    lease_expires_at: 0,
    diagnostics: []
  } IN fhir_semantic_inventory_builds
  OPTIONS {exclusive: true}
  RETURN NEW`

const failSemanticInventoryBackfillAQL = `
FOR d IN fhir_semantic_inventory_builds
  FILTER d._key == @key
  FILTER d.state == "running"
  FILTER d.lease_token == @token
  UPDATE d WITH {
    state: "failed",
    lease_token: "",
    lease_expires_at: 0,
    diagnostics: [@diagnostic]
  } IN fhir_semantic_inventory_builds
  OPTIONS {exclusive: true}
  RETURN NEW`
