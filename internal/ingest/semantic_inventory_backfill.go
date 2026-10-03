package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	publication "github.com/calypr/loom/internal/dataset"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const semanticInventoryBackfillLease = 30 * time.Minute

type SemanticInventoryBackfillOptions struct {
	Project           string
	DatasetGeneration string
	PageSize          int
	BatchSize         int
	LeaseDuration     time.Duration
	Progress          func(SemanticInventoryBackfillProgress)
}

type SemanticInventoryBackfillProgress struct {
	Collection       string  `json:"collection"`
	CollectionRows   int64   `json:"collection_rows"`
	CollectionEvents int64   `json:"collection_contributions"`
	TotalRows        int64   `json:"total_rows"`
	TotalEvents      int64   `json:"total_contributions"`
	ElapsedSeconds   float64 `json:"elapsed_seconds"`
}

type SemanticInventoryBackfillCollectionResult struct {
	Rows           int64   `json:"rows"`
	Contributions  int64   `json:"contributions"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
}

type SemanticInventoryBackfillReport struct {
	Project             string                                               `json:"project"`
	DatasetGeneration   string                                               `json:"dataset_generation"`
	BuildID             string                                               `json:"build_id"`
	State               catalog.SemanticInventoryState                       `json:"state"`
	NoOp                bool                                                 `json:"no_op"`
	EntriesMaterialized bool                                                 `json:"entries_materialized,omitempty"`
	ScannedThisRun      int64                                                `json:"scanned_this_run"`
	ScannedTotal        int64                                                `json:"scanned_total"`
	Contributions       int64                                                `json:"contributions_written"`
	Checkpoint          catalog.SemanticInventoryCheckpoint                  `json:"checkpoint"`
	SourceAvailability  string                                               `json:"source_availability"`
	MissingCollections  []string                                             `json:"source_availability_unproven_collections,omitempty"`
	Collections         map[string]SemanticInventoryBackfillCollectionResult `json:"collections"`
	ElapsedSeconds      float64                                              `json:"elapsed_seconds"`
}

type semanticInventoryBackfillBackend interface {
	PrepareSemanticInventoryBackfill(context.Context) error
	ReadRetainedSemanticInventoryPage(context.Context, string, string, string, string, int) (catalogarango.RetainedSemanticInventoryPage, error)
	ClaimSemanticInventoryBackfill(context.Context, catalog.SemanticInventoryBuild, string, time.Duration) (catalog.SemanticInventoryBuild, bool, error)
	AdvanceSemanticInventoryBackfill(context.Context, catalog.SemanticInventoryBuild, string, catalog.SemanticInventoryCheckpoint, catalog.SemanticInventoryCheckpoint, int64, time.Duration) (catalog.SemanticInventoryBuild, error)
	CompleteSemanticInventoryBackfill(context.Context, catalog.SemanticInventoryBuild, string, catalog.SemanticInventoryCheckpoint, int64) (catalog.SemanticInventoryBuild, error)
	FailSemanticInventoryBackfill(context.Context, catalog.SemanticInventoryBuild, string, string) error
	WriteSemanticInventoryContributions(context.Context, []catalog.SemanticInventoryContribution, int) error
	EnsureSemanticInventoryEntries(context.Context, catalog.SemanticInventoryBuild) (catalog.SemanticInventoryBuild, error)
}

// BackfillSemanticInventory reconstructs the inventory from retained immutable
// generation vertices. Source rows are read in bounded pages; contribution
// batches are replaced by deterministic keys before the typed checkpoint moves.
func BackfillSemanticInventory(ctx context.Context, backend semanticInventoryBackfillBackend, manifest publication.Manifest, opts SemanticInventoryBackfillOptions) (SemanticInventoryBackfillReport, error) {
	started := time.Now()
	if backend == nil {
		return SemanticInventoryBackfillReport{}, errors.New("semantic inventory backfill backend is required")
	}
	if err := validateSemanticInventoryBackfillRequest(manifest, opts); err != nil {
		return SemanticInventoryBackfillReport{}, err
	}
	if opts.PageSize == 0 {
		opts.PageSize = 250
	}
	if opts.BatchSize == 0 {
		opts.BatchSize = 500
	}
	if opts.LeaseDuration <= 0 {
		opts.LeaseDuration = semanticInventoryBackfillLease
	}
	collections := append([]string(nil), manifest.SchemaIdentity.GeneratedResourceTypes...)
	sort.Strings(collections)
	for i := 1; i < len(collections); i++ {
		if collections[i] == collections[i-1] {
			return SemanticInventoryBackfillReport{}, fmt.Errorf("duplicate retained source collection %q", collections[i])
		}
	}
	build := catalog.NewSemanticInventoryBuild(opts.Project, opts.DatasetGeneration, "")
	build.SourceKind = catalog.SemanticInventorySourceRetained
	if err := backend.PrepareSemanticInventoryBackfill(ctx); err != nil {
		return SemanticInventoryBackfillReport{}, fmt.Errorf("prepare semantic inventory storage: %w", err)
	}
	token, err := newSemanticInventoryBackfillToken()
	if err != nil {
		return SemanticInventoryBackfillReport{}, err
	}
	build, claimed, err := backend.ClaimSemanticInventoryBackfill(ctx, build, token, opts.LeaseDuration)
	if err != nil {
		return SemanticInventoryBackfillReport{}, err
	}
	report := SemanticInventoryBackfillReport{
		Project:            opts.Project,
		DatasetGeneration:  catalog.NormalizeDatasetGeneration(opts.DatasetGeneration),
		BuildID:            build.BuildID,
		State:              build.State,
		SourceAvailability: build.SourceAvailability,
		Collections:        map[string]SemanticInventoryBackfillCollectionResult{},
	}
	if !claimed {
		if build.EntryIndexVersion < catalog.SemanticInventoryEntryIndexVersion {
			build, err = backend.EnsureSemanticInventoryEntries(ctx, build)
			if err != nil {
				return SemanticInventoryBackfillReport{}, err
			}
			report.EntriesMaterialized = true
		}
		report.NoOp = true
		report.State = build.State
		report.ScannedTotal = build.ScannedResources
		report.Checkpoint = build.SourceCheckpoint
		report.SourceAvailability = build.SourceAvailability
		report.ElapsedSeconds = time.Since(started).Seconds()
		return report, nil
	}
	if build.SourceAvailability == "" || build.SourceAvailability == catalog.SemanticInventorySourceAvailabilityUnknown {
		build.SourceAvailability = catalog.SemanticInventorySourceAvailabilityVerified
	}
	emitter := catalog.NewSemanticInventoryEmitter(opts.Project, opts.DatasetGeneration)
	currentCheckpoint := build.SourceCheckpoint
	startCollectionIndex := 0
	if currentCheckpoint.Collection != "" {
		startCollectionIndex = collectionIndexFor(collections, currentCheckpoint.Collection)
		if startCollectionIndex == len(collections) {
			return failSemanticInventoryBackfill(backend, build, token, report, fmt.Errorf("checkpoint collection %q is not a generated source", currentCheckpoint.Collection))
		}
	}
	var scannedThisRun, eventsThisRun int64
	for collectionIndex, collection := range collections {
		if _, ok := fhirschema.ConcreteResourceType(collection); !ok {
			return failSemanticInventoryBackfill(backend, build, token, report, fmt.Errorf("invalid retained resource collection %q", collection))
		}
		if collectionIndex < startCollectionIndex {
			continue
		}
		afterKey := ""
		if currentCheckpoint.Collection == collection {
			afterKey = currentCheckpoint.Key
		}
		collectionStarted := time.Now()
		for {
			page, readErr := backend.ReadRetainedSemanticInventoryPage(ctx, opts.Project, opts.DatasetGeneration, collection, afterKey, opts.PageSize)
			if readErr != nil {
				return failSemanticInventoryBackfill(backend, build, token, report, readErr)
			}
			if !page.SourceExists {
				build.SourceAvailability = catalog.SemanticInventorySourceAvailabilityUnproven
				report.SourceAvailability = build.SourceAvailability
				report.MissingCollections = append(report.MissingCollections, collection)
				break
			}
			if len(page.Rows) == 0 {
				break
			}
			pageEvents := int64(0)
			buffer := make([]catalog.SemanticInventoryContribution, 0, opts.BatchSize)
			var persistErr error
			flush := func() {
				if persistErr != nil || len(buffer) == 0 {
					return
				}
				persistErr = backend.WriteSemanticInventoryContributions(ctx, buffer, opts.BatchSize)
				if persistErr == nil {
					pageEvents += int64(len(buffer))
				}
				buffer = buffer[:0]
			}
			for _, source := range page.Rows {
				payload, payloadErr := retainedSemanticPayload(source, collection)
				if payloadErr != nil {
					persistErr = payloadErr
					break
				}
				authPath := ""
				if source.AuthResourcePath != nil {
					authPath = *source.AuthResourcePath
				}
				sourceID := "retained:" + collection + "/" + source.Key
				emitter.ObservePayload(payload, collection, authPath, sourceID, func(contribution catalog.SemanticInventoryContribution) {
					if persistErr != nil {
						return
					}
					buffer = append(buffer, contribution)
					if len(buffer) == opts.BatchSize {
						flush()
					}
				})
				if persistErr != nil {
					break
				}
			}
			flush()
			if persistErr != nil {
				return failSemanticInventoryBackfill(backend, build, token, report, persistErr)
			}
			lastKey := page.Rows[len(page.Rows)-1].Key
			if lastKey <= afterKey {
				return failSemanticInventoryBackfill(backend, build, token, report, fmt.Errorf("retained %s source cursor did not advance", collection))
			}
			nextCheckpoint := catalog.SemanticInventoryCheckpoint{Collection: collection, Key: lastKey}
			nextScanned := build.ScannedResources + int64(len(page.Rows))
			updated, advanceErr := backend.AdvanceSemanticInventoryBackfill(ctx, build, token, currentCheckpoint, nextCheckpoint, nextScanned, opts.LeaseDuration)
			if advanceErr != nil {
				return failSemanticInventoryBackfill(backend, build, token, report, advanceErr)
			}
			build = updated
			currentCheckpoint = nextCheckpoint
			afterKey = lastKey
			scannedThisRun += int64(len(page.Rows))
			eventsThisRun += pageEvents
			report.ScannedThisRun = scannedThisRun
			report.ScannedTotal = build.ScannedResources
			report.Contributions = eventsThisRun
			report.Checkpoint = currentCheckpoint
			report.SourceAvailability = build.SourceAvailability
			collectionReport := report.Collections[collection]
			collectionReport.Rows += int64(len(page.Rows))
			collectionReport.Contributions += pageEvents
			collectionReport.ElapsedSeconds = time.Since(collectionStarted).Seconds()
			report.Collections[collection] = collectionReport
			if opts.Progress != nil {
				opts.Progress(SemanticInventoryBackfillProgress{
					Collection:       collection,
					CollectionRows:   collectionReport.Rows,
					CollectionEvents: collectionReport.Contributions,
					TotalRows:        scannedThisRun,
					TotalEvents:      eventsThisRun,
					ElapsedSeconds:   time.Since(started).Seconds(),
				})
			}
			if !page.HasMore {
				break
			}
		}
	}
	completed, err := backend.CompleteSemanticInventoryBackfill(ctx, build, token, currentCheckpoint, build.ScannedResources)
	if err != nil {
		return failSemanticInventoryBackfill(backend, build, token, report, err)
	}
	report.State = completed.State
	report.ScannedThisRun = scannedThisRun
	report.ScannedTotal = completed.ScannedResources
	report.Contributions = eventsThisRun
	report.Checkpoint = completed.SourceCheckpoint
	report.SourceAvailability = completed.SourceAvailability
	report.ElapsedSeconds = time.Since(started).Seconds()
	return report, nil
}

func validateSemanticInventoryBackfillRequest(manifest publication.Manifest, opts SemanticInventoryBackfillOptions) error {
	if strings.TrimSpace(opts.Project) == "" || catalog.NormalizeDatasetGeneration(opts.DatasetGeneration) == "" {
		return errors.New("semantic inventory backfill requires project and generation")
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("validate retained generation manifest: %w", err)
	}
	if !manifest.IsStaged() {
		return fmt.Errorf("semantic inventory backfill requires an immutable STAGED or READY generation, got %s", manifest.State)
	}
	if manifest.Dataset.Project != opts.Project || catalog.NormalizeDatasetGeneration(manifest.Dataset.Generation) != catalog.NormalizeDatasetGeneration(opts.DatasetGeneration) {
		return errors.New("semantic inventory backfill request does not match manifest identity")
	}
	if opts.PageSize < 0 || opts.PageSize > 1000 || opts.BatchSize < 0 || opts.BatchSize > 5000 {
		return errors.New("semantic inventory backfill page or batch size is outside supported bounds")
	}
	return nil
}

func retainedSemanticPayload(source catalogarango.RetainedSemanticInventoryRow, resourceType string) (map[string]any, error) {
	if source.Key == "" || len(source.Payload) == 0 || string(source.Payload) == "null" {
		return nil, fmt.Errorf("retained %s/%s source is missing payload", resourceType, source.Key)
	}
	var payload map[string]any
	if err := json.Unmarshal(source.Payload, &payload); err != nil || payload == nil {
		if err == nil {
			err = errors.New("payload is not an object")
		}
		return nil, fmt.Errorf("decode retained %s/%s payload: %w", resourceType, source.Key, err)
	}
	if actual, _ := payload["resourceType"].(string); actual != resourceType {
		return nil, fmt.Errorf("retained %s/%s payload resourceType is %q", resourceType, source.Key, actual)
	}
	return payload, nil
}

func failSemanticInventoryBackfill(backend semanticInventoryBackfillBackend, build catalog.SemanticInventoryBuild, token string, report SemanticInventoryBackfillReport, cause error) (SemanticInventoryBackfillReport, error) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := backend.FailSemanticInventoryBackfill(cleanupCtx, build, token, cause.Error()); err != nil {
		cause = errors.Join(cause, fmt.Errorf("mark semantic inventory backfill failed: %w", err))
	}
	report.State = catalog.SemanticInventoryFailed
	report.ScannedTotal = build.ScannedResources
	report.Checkpoint = build.SourceCheckpoint
	report.SourceAvailability = build.SourceAvailability
	return report, cause
}

func collectionIndexFor(collections []string, target string) int {
	for index, collection := range collections {
		if collection == target {
			return index
		}
	}
	return len(collections)
}

func newSemanticInventoryBackfillToken() (string, error) {
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("create semantic inventory backfill claim token: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}
