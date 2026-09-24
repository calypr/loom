package ingest

import (
	"context"
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

type FieldSourceMembershipBackfillOptions struct {
	Project           string
	DatasetGeneration string
	MaxResources      int
	PageSize          int
	BatchSize         int
}

type FieldSourceMembershipBackfillReport struct {
	Project           string                                  `json:"project"`
	DatasetGeneration string                                  `json:"dataset_generation"`
	State             catalog.FieldSourceMembershipState      `json:"state"`
	NoOp              bool                                    `json:"no_op,omitempty"`
	ScannedThisRun    int                                     `json:"scanned_this_run"`
	ScannedTotal      int64                                   `json:"scanned_total"`
	Checkpoint        catalog.FieldSourceMembershipCheckpoint `json:"checkpoint"`
	ElapsedSeconds    float64                                 `json:"elapsed_seconds"`
}

type fieldSourceMembershipBackfillBackend interface {
	PrepareFieldSourceMembership(context.Context) error
	ReadFieldSourceMembershipBuild(context.Context, string, string) (catalog.FieldSourceMembershipBuild, bool, error)
	WriteFieldSourceMembershipBuild(context.Context, catalog.FieldSourceMembershipBuild) error
	ReadRetainedSemanticInventoryPage(context.Context, string, string, string, string, int) (catalogarango.RetainedSemanticInventoryPage, error)
	WriteFieldSourceMemberships(context.Context, []catalog.FieldSourceMembership, int) error
}

// BackfillFieldSourceMembership advances an opt-in, resource-bounded scan of
// retained immutable vertices. Sidecars are persisted before the independent
// checkpoint, so retrying a page replaces the same deterministic keys.
func BackfillFieldSourceMembership(ctx context.Context, backend fieldSourceMembershipBackfillBackend, manifest publication.Manifest, opts FieldSourceMembershipBackfillOptions) (report FieldSourceMembershipBackfillReport, err error) {
	started := time.Now()
	if backend == nil {
		return report, errors.New("field-source membership backfill backend is required")
	}
	if err := validateFieldSourceMembershipBackfillRequest(manifest, opts); err != nil {
		return report, err
	}
	if opts.PageSize == 0 {
		opts.PageSize = 250
	}
	if opts.BatchSize == 0 {
		opts.BatchSize = 500
	}
	if err := backend.PrepareFieldSourceMembership(ctx); err != nil {
		return report, fmt.Errorf("prepare field-source membership storage: %w", err)
	}
	collections := append([]string(nil), manifest.SchemaIdentity.GeneratedResourceTypes...)
	sort.Strings(collections)
	for index, collection := range collections {
		if _, ok := fhirschema.ConcreteResourceType(collection); !ok || strings.TrimSpace(collection) != collection {
			return report, fmt.Errorf("invalid retained resource collection %q", collection)
		}
		if index > 0 && collection == collections[index-1] {
			return report, fmt.Errorf("duplicate retained source collection %q", collection)
		}
	}

	build, found, err := backend.ReadFieldSourceMembershipBuild(ctx, opts.Project, opts.DatasetGeneration)
	if err != nil {
		return report, err
	}
	if !found || build.SchemaVersion != catalog.FieldSourceMembershipSchemaVersion {
		build = catalog.NewFieldSourceMembershipBuild(opts.Project, opts.DatasetGeneration)
	}
	report = FieldSourceMembershipBackfillReport{
		Project:           opts.Project,
		DatasetGeneration: catalog.NormalizeDatasetGeneration(opts.DatasetGeneration),
		State:             build.State,
		ScannedTotal:      build.ScannedResources,
		Checkpoint:        build.Checkpoint,
	}
	if found && build.State == catalog.FieldSourceMembershipComplete && build.SchemaVersion == catalog.FieldSourceMembershipSchemaVersion {
		report.NoOp = true
		report.ElapsedSeconds = time.Since(started).Seconds()
		return report, nil
	}
	build.State = catalog.FieldSourceMembershipBuilding
	build.Diagnostic = ""
	if err := backend.WriteFieldSourceMembershipBuild(ctx, build); err != nil {
		return report, fmt.Errorf("begin field-source membership backfill: %w", err)
	}
	buildStarted := true
	defer func() {
		if err == nil || !buildStarted {
			return
		}
		build.State = catalog.FieldSourceMembershipFailed
		build.Diagnostic = err.Error()
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if writeErr := backend.WriteFieldSourceMembershipBuild(cleanupCtx, build); writeErr != nil {
			err = errors.Join(err, fmt.Errorf("mark field-source membership backfill failed: %w", writeErr))
		}
	}()

	checkpoint := build.Checkpoint
	collectionIndex := 0
	if checkpoint.Collection != "" {
		collectionIndex = collectionIndexFor(collections, checkpoint.Collection)
		if collectionIndex == len(collections) {
			return report, fmt.Errorf("field-source membership checkpoint collection %q is not generated", checkpoint.Collection)
		}
	}
	var scannedThisRun int
	for collectionIndex < len(collections) && scannedThisRun < opts.MaxResources {
		collection := collections[collectionIndex]
		afterKey := ""
		if checkpoint.Collection == collection {
			afterKey = checkpoint.Key
		}
		limit := min(opts.PageSize, opts.MaxResources-scannedThisRun)
		page, readErr := backend.ReadRetainedSemanticInventoryPage(ctx, opts.Project, opts.DatasetGeneration, collection, afterKey, limit)
		if readErr != nil {
			return report, readErr
		}
		if page.SourceState != catalogarango.RetainedSemanticSourcePresent && page.SourceState != catalogarango.RetainedSemanticSourceVerifiedEmpty {
			return report, fmt.Errorf("retained %s source returned invalid state %q", collection, page.SourceState)
		}
		if page.SourceState == catalogarango.RetainedSemanticSourceVerifiedEmpty && (len(page.Rows) != 0 || page.HasMore) {
			return report, fmt.Errorf("retained %s source marked verified empty with rows", collection)
		}
		if len(page.Rows) == 0 {
			collectionIndex++
			checkpoint = fieldSourceMembershipCheckpointAfterCollection(collections, collectionIndex)
			build.Checkpoint = checkpoint
			build.ScannedResources = report.ScannedTotal
			if err = backend.WriteFieldSourceMembershipBuild(ctx, build); err != nil {
				return report, fmt.Errorf("advance field-source membership checkpoint: %w", err)
			}
			continue
		}
		if len(page.Rows) > limit {
			return report, fmt.Errorf("retained %s page returned %d rows above limit %d", collection, len(page.Rows), limit)
		}
		memberships := make([]catalog.FieldSourceMembership, 0, len(page.Rows))
		semanticEmitter := catalog.NewSemanticInventoryEmitter(opts.Project, opts.DatasetGeneration)
		for _, source := range page.Rows {
			payload, payloadErr := retainedSemanticPayload(source, collection)
			if payloadErr != nil {
				return report, payloadErr
			}
			authPath := ""
			if source.AuthResourcePath != nil {
				authPath = *source.AuthResourcePath
			}
			membership, membershipErr := catalog.NewFieldSourceMembership(opts.Project, opts.DatasetGeneration, authPath, collection, collection+"/"+source.Key, payload)
			if membershipErr != nil {
				return report, membershipErr
			}
			contributions := make([]catalog.SemanticInventoryContribution, 0)
			if emitErr := semanticEmitter.ObservePayload(payload, collection, authPath, "retained:"+membership.VertexID, func(contribution catalog.SemanticInventoryContribution) {
				contributions = append(contributions, contribution)
			}); emitErr != nil {
				return report, fmt.Errorf("profile semantic features for %s: %w", membership.VertexID, emitErr)
			}
			membership.SemanticFeatures = catalog.SemanticFeatureReferences(contributions)
			memberships = append(memberships, membership)
		}
		if err = backend.WriteFieldSourceMemberships(ctx, memberships, opts.BatchSize); err != nil {
			return report, fmt.Errorf("persist field-source memberships for %s: %w", collection, err)
		}
		lastKey := page.Rows[len(page.Rows)-1].Key
		if lastKey <= afterKey {
			return report, fmt.Errorf("retained %s source cursor did not advance", collection)
		}
		if page.HasMore {
			checkpoint = catalog.FieldSourceMembershipCheckpoint{Collection: collection, Key: lastKey}
		} else {
			collectionIndex++
			checkpoint = fieldSourceMembershipCheckpointAfterCollection(collections, collectionIndex)
		}
		scannedThisRun += len(page.Rows)
		report.ScannedThisRun = scannedThisRun
		report.ScannedTotal += int64(len(page.Rows))
		build.Checkpoint = checkpoint
		build.ScannedResources = report.ScannedTotal
		if err = backend.WriteFieldSourceMembershipBuild(ctx, build); err != nil {
			return report, fmt.Errorf("advance field-source membership checkpoint: %w", err)
		}
	}
	if checkpoint.Collection == "" && collectionIndex >= len(collections) {
		build.State = catalog.FieldSourceMembershipComplete
		build.Checkpoint = catalog.FieldSourceMembershipCheckpoint{}
		build.Diagnostic = ""
		if err = backend.WriteFieldSourceMembershipBuild(ctx, build); err != nil {
			return report, fmt.Errorf("complete field-source membership backfill: %w", err)
		}
	}
	report.State = build.State
	report.ScannedTotal = build.ScannedResources
	report.Checkpoint = build.Checkpoint
	report.ElapsedSeconds = time.Since(started).Seconds()
	return report, nil
}

func validateFieldSourceMembershipBackfillRequest(manifest publication.Manifest, opts FieldSourceMembershipBackfillOptions) error {
	if strings.TrimSpace(opts.Project) == "" || catalog.NormalizeDatasetGeneration(opts.DatasetGeneration) == "" {
		return errors.New("field-source membership backfill requires project and generation")
	}
	if opts.MaxResources < 1 {
		return errors.New("field-source membership backfill requires a positive --max-resources budget")
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("validate retained generation manifest: %w", err)
	}
	if !manifest.IsStaged() {
		return fmt.Errorf("field-source membership backfill requires an immutable STAGED or READY generation, got %s", manifest.State)
	}
	if manifest.Dataset.Project != opts.Project || catalog.NormalizeDatasetGeneration(manifest.Dataset.Generation) != catalog.NormalizeDatasetGeneration(opts.DatasetGeneration) {
		return errors.New("field-source membership backfill request does not match manifest identity")
	}
	if opts.PageSize < 0 || opts.PageSize > 1000 || opts.BatchSize < 0 || opts.BatchSize > 5000 {
		return errors.New("field-source membership backfill page or batch size is outside supported bounds")
	}
	return nil
}

func fieldSourceMembershipCheckpointAfterCollection(collections []string, nextIndex int) catalog.FieldSourceMembershipCheckpoint {
	if nextIndex >= len(collections) {
		return catalog.FieldSourceMembershipCheckpoint{}
	}
	return catalog.FieldSourceMembershipCheckpoint{Collection: collections[nextIndex]}
}
