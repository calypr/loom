package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const (
	BundleExecutionsCollection = "loom_dataframe_bundle_executions"
	BundlePointersCollection   = "loom_dataframe_bundle_pointers"
	BundleLeasesCollection     = "loom_dataframe_bundle_leases"
	executionCleanupTTL        = 2 * time.Minute
)

func (r *Registry) SaveExecution(ctx context.Context, execution publication.BundleExecution, fenceOwner string) error {
	if execution.ID == "" || execution.Key == "" || fenceOwner == "" {
		return publication.ErrBundleLeaseLost
	}
	data, err := json.Marshal(execution)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	doc["_key"] = execution.ID
	data, err = json.Marshal(doc)
	if err != nil {
		return err
	}
	saved := false
	err = r.client.QueryRows(ctx, `LET lease = DOCUMENT(@@leases, @leaseKey)
FILTER lease != null
  AND lease.ownerId == @owner
  AND lease.expiresAt >= @now
UPSERT {_key: @executionKey}
INSERT @execution
REPLACE @execution IN @@collection
RETURN {saved: true}`, r.batchSize, map[string]interface{}{
		"@collection": BundleExecutionsCollection, "@leases": BundleLeasesCollection,
		"leaseKey": execution.Key, "owner": fenceOwner, "now": time.Now().UTC(),
		"executionKey": execution.ID, "execution": doc,
	}, func(map[string]any) error {
		saved = true
		return nil
	})
	if err != nil {
		return err
	}
	if !saved {
		return publication.ErrBundleLeaseLost
	}
	return nil
}

func (r *Registry) GetExecution(ctx context.Context, id string) (publication.BundleExecution, error) {
	execution, err := r.loadExecution(ctx, `FOR doc IN @@collection FILTER doc._key == @key RETURN doc`, map[string]interface{}{"@collection": BundleExecutionsCollection, "key": id})
	return execution.CanonicalizeLegacy(), err
}

func (r *Registry) FindExecutionByKey(ctx context.Context, key string) (publication.BundleExecution, error) {
	execution, err := r.loadExecution(ctx, `FOR doc IN @@collection FILTER doc.key == @key SORT doc.createdAt DESC LIMIT 1 RETURN doc`, map[string]interface{}{"@collection": BundleExecutionsCollection, "key": key})
	return execution.CanonicalizeLegacy(), err
}

func (r *Registry) ListExecutions(ctx context.Context, state publication.BundleState, before time.Time) ([]publication.BundleExecution, error) {
	out := []publication.BundleExecution{}
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc.state IN @states AND doc.updatedAt < @before SORT doc.updatedAt ASC RETURN doc`, r.batchSize, map[string]interface{}{"@collection": BundleExecutionsCollection, "states": executionStates(state), "before": before}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var execution publication.BundleExecution
		if err := json.Unmarshal(data, &execution); err != nil {
			return err
		}
		out = append(out, execution.CanonicalizeLegacy())
		return nil
	})
	return out, err
}

func (r *Registry) VisitExecutionPages(ctx context.Context, state publication.BundleState, before time.Time, pageSize int, visit publication.BundleExecutionPageFunc) error {
	if pageSize <= 0 {
		return fmt.Errorf("execution page size must be positive")
	}
	if visit == nil {
		return fmt.Errorf("execution page visitor is required")
	}
	var afterUpdatedAt time.Time
	afterKey := ""
	for {
		page := make([]publication.BundleExecution, 0, pageSize)
		lastKey := ""
		err := r.client.QueryRows(ctx, `FOR doc IN @@collection
FILTER doc.state IN @states
  AND doc.updatedAt < @before
  AND (doc.updatedAt > @afterUpdatedAt OR (doc.updatedAt == @afterUpdatedAt AND doc._key > @afterKey))
SORT doc.updatedAt ASC, doc._key ASC
LIMIT @limit
RETURN doc`, r.batchSize, map[string]interface{}{
			"@collection":    BundleExecutionsCollection,
			"states":         executionStates(state),
			"before":         before,
			"afterUpdatedAt": afterUpdatedAt,
			"afterKey":       afterKey,
			"limit":          pageSize,
		}, func(row map[string]any) error {
			data, err := json.Marshal(row)
			if err != nil {
				return err
			}
			var execution publication.BundleExecution
			if err := json.Unmarshal(data, &execution); err != nil {
				return err
			}
			page = append(page, execution.CanonicalizeLegacy())
			if key, ok := row["_key"].(string); ok {
				lastKey = key
			} else {
				lastKey = execution.ID
			}
			return nil
		})
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		if err := visit(page); err != nil {
			return err
		}
		if len(page) < pageSize {
			return nil
		}
		if lastKey == "" || page[len(page)-1].UpdatedAt.IsZero() {
			return fmt.Errorf("execution page has no stable continuation key")
		}
		afterUpdatedAt = page[len(page)-1].UpdatedAt
		afterKey = lastKey
	}
}

func executionStates(state publication.BundleState) []publication.BundleState {
	states := []publication.BundleState{state.Canonical()}
	switch state.Canonical() {
	case publication.BundleQueued:
		states = append(states, publication.BundlePending)
	case publication.BundleRunning:
		states = append(states, publication.BundlePreflight, publication.BundleLoading)
	case publication.BundlePublished:
		states = append(states, publication.BundleReady)
	}
	return states
}

func (r *Registry) FindExecutionBySelector(ctx context.Context, project, generation string, selector publication.DataframeSelector) (publication.BundleExecution, publication.BundleOutputRecord, error) {
	if err := selector.Validate(); err != nil {
		return publication.BundleExecution{}, publication.BundleOutputRecord{}, err
	}
	execution, err := r.loadExecution(ctx, `FOR doc IN @@collection
FILTER (doc.project == @project OR doc.Project == @project) AND (doc.datasetGeneration == @generation OR doc.DatasetGeneration == @generation)
  AND (doc.name == @recipe OR doc.Name == @recipe) AND (doc.translationVersion == @translationVersion OR doc.TranslationVersion == @translationVersion)
  AND doc.state IN ["PUBLISHED", "READY"]
  AND LENGTH(FOR output IN doc.outputs FILTER output.name == @output OR output.Name == @output RETURN 1) > 0
SORT doc.publishedAt DESC, doc.readyAt DESC, doc.updatedAt DESC LIMIT 1 RETURN doc`, map[string]interface{}{
		"@collection": BundleExecutionsCollection, "project": project, "generation": generation,
		"recipe": selector.Recipe, "translationVersion": selector.TranslationVersion, "output": selector.Output,
	})
	if err != nil {
		return publication.BundleExecution{}, publication.BundleOutputRecord{}, err
	}
	execution = execution.CanonicalizeLegacy()
	for _, output := range execution.Outputs {
		if output.Name == selector.Output {
			return execution, output, nil
		}
	}
	return publication.BundleExecution{}, publication.BundleOutputRecord{}, publication.ErrBundleNotFound
}

func (r *Registry) loadExecution(ctx context.Context, query string, vars map[string]interface{}) (publication.BundleExecution, error) {
	var found *publication.BundleExecution
	err := r.client.QueryRows(ctx, query, r.batchSize, vars, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var execution publication.BundleExecution
		if err := json.Unmarshal(data, &execution); err != nil {
			return err
		}
		found = &execution
		return nil
	})
	if err != nil {
		return publication.BundleExecution{}, err
	}
	if found == nil {
		return publication.BundleExecution{}, publication.ErrBundleNotFound
	}
	return *found, nil
}

func (r *Registry) GetPointer(ctx context.Context, name string) (publication.BundlePointer, error) {
	var found *publication.BundlePointer
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc._key == @key RETURN doc`, r.batchSize, map[string]interface{}{"@collection": BundlePointersCollection, "key": pointerDocumentKey(name)}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var pointer publication.BundlePointer
		if err := json.Unmarshal(data, &pointer); err != nil {
			return err
		}
		found = &pointer
		return nil
	})
	if err != nil {
		return publication.BundlePointer{}, err
	}
	if found == nil {
		return publication.BundlePointer{}, publication.ErrBundleNotFound
	}
	return *found, nil
}

// CompareAndSwapPointer performs the visibility update in Arango. The query
// returns a small object so the generic QueryRows client can inspect whether
// the expected version won the race.
func (r *Registry) CompareAndSwapPointer(ctx context.Context, name, expected, next string) error {
	var result struct {
		Updated bool `json:"updated"`
	}
	updated := false
	err := r.client.QueryRows(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing == null OR existing.executionId == @expected
UPSERT {_key: @key}
INSERT {_key: @key, name: @name, executionId: @next, updatedAt: @updatedAt}
UPDATE {executionId: @next, updatedAt: @updatedAt}
IN @@collection
RETURN {updated: true}`, r.batchSize, map[string]interface{}{"@collection": BundlePointersCollection, "key": pointerDocumentKey(name), "name": name, "expected": expected, "next": next, "updatedAt": time.Now().UTC()}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &result)
	})
	if err != nil {
		return err
	}
	updated = result.Updated
	if !updated {
		return publication.ErrBundlePointerConflict
	}
	return nil
}

// PublishExecution atomically commits both the visibility pointer and the
// PUBLISHED metadata. A failed compare-and-swap leaves both documents intact.
func (r *Registry) PublishExecution(ctx context.Context, name, expected string, execution publication.BundleExecution) error {
	data, err := json.Marshal(execution)
	if err != nil {
		return err
	}
	var executionDoc map[string]any
	if err := json.Unmarshal(data, &executionDoc); err != nil {
		return err
	}
	updated := false
	err = r.client.QueryRows(ctx, `LET pointer = DOCUMENT(@@pointers, @pointerKey)
LET execution = DOCUMENT(@@executions, @executionKey)
LET lease = DOCUMENT(@@leases, @leaseKey)
FILTER execution != null
  AND lease != null
  AND lease.ownerId == @owner
  AND lease.expiresAt >= @now
  AND (pointer == null OR pointer.executionId == @expected)
UPDATE execution WITH @execution IN @@executions
UPSERT {_key: @pointerKey}
INSERT {_key: @pointerKey, name: @name, executionId: @executionKey, updatedAt: @updatedAt}
UPDATE {executionId: @executionKey, updatedAt: @updatedAt}
IN @@pointers
RETURN {updated: true}`, r.batchSize, map[string]interface{}{
		"@pointers": BundlePointersCollection, "@executions": BundleExecutionsCollection, "@leases": BundleLeasesCollection,
		"pointerKey": pointerDocumentKey(name), "executionKey": execution.ID,
		"leaseKey": execution.Key, "owner": execution.OwnerID, "now": time.Now().UTC(),
		"name": name, "expected": expected, "updatedAt": execution.UpdatedAt, "execution": executionDoc,
	}, func(map[string]any) error { updated = true; return nil })
	if err != nil {
		return err
	}
	if !updated {
		return publication.ErrBundlePointerConflict
	}
	return nil
}

func (r *Registry) AcquireBundleLease(ctx context.Context, key, owner string, expires time.Time) (bool, error) {
	claimed := false
	err := r.client.QueryRows(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing == null OR existing.expiresAt < @now OR existing.ownerId == @owner
UPSERT {_key: @key}
INSERT {_key: @key, ownerId: @owner, expiresAt: @expiresAt}
UPDATE {ownerId: @owner, expiresAt: @expiresAt}
IN @@collection
RETURN {claimed: true}`, r.batchSize, map[string]interface{}{"@collection": BundleLeasesCollection, "key": key, "owner": owner, "expiresAt": expires, "now": time.Now().UTC()}, func(row map[string]any) error { claimed = true; return nil })
	return claimed, err
}

func (r *Registry) RenewBundleLease(ctx context.Context, key, owner string, expires time.Time) (bool, error) {
	claimed := false
	err := r.client.QueryRows(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing != null AND existing.ownerId == @owner
UPDATE existing WITH {expiresAt: @expiresAt} IN @@collection
RETURN {claimed: true}`, r.batchSize, map[string]interface{}{"@collection": BundleLeasesCollection, "key": key, "owner": owner, "expiresAt": expires}, func(row map[string]any) error { claimed = true; return nil })
	return claimed, err
}

func (r *Registry) ReleaseBundleLease(ctx context.Context, key, owner string) error {
	executor, ok := r.client.(arangostore.AQLExecutor)
	if !ok {
		return nil
	}
	return executor.ExecuteAQL(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing != null AND existing.ownerId == @owner
REMOVE existing IN @@collection`, map[string]interface{}{"@collection": BundleLeasesCollection, "key": key, "owner": owner})
}

func (r *Registry) AcquireExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return false, publication.ErrExecutionReadPinLost
	}
	now := time.Now().UTC()
	claimed := false
	err := r.client.QueryRows(ctx, `LET state = DOCUMENT(@@collection, @stateKey)
LET pins = state == null ? [] : NOT_NULL(state.pins, [])
LET retained = (FOR pin IN pins FILTER pin.expiresAt >= @now AND pin.ownerId != @owner RETURN pin)
LET cleanup = state == null ? null : state.cleanup
FILTER cleanup == null OR cleanup.expiresAt < @now
UPSERT {_key: @stateKey}
INSERT {_key: @stateKey, executionId: @executionId, pins: APPEND(retained, [{ownerId: @owner, expiresAt: @expiresAt}]), cleanup: null, updatedAt: @now}
UPDATE {pins: APPEND(retained, [{ownerId: @owner, expiresAt: @expiresAt}]), updatedAt: @now} IN @@collection
	RETURN {claimed: true}`, r.batchSize, map[string]interface{}{"@collection": ExecutionReadPinsCollection, "stateKey": executionStateKey(executionID), "executionId": executionID, "owner": owner, "expiresAt": expires, "now": now}, func(row map[string]any) error {
		claimed, _ = row["claimed"].(bool)
		return nil
	})
	return claimed, err
}

func (r *Registry) RenewExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	now := time.Now().UTC()
	claimed := false
	err := r.client.QueryRows(ctx, `FOR state IN @@collection
FILTER state._key == @stateKey
LET pins = NOT_NULL(state.pins, [])
LET cleanup = state.cleanup
LET owned = FIRST(FOR pin IN pins FILTER pin.ownerId == @owner RETURN pin)
LET retained = (FOR pin IN pins FILTER pin.ownerId != @owner AND pin.expiresAt >= @now RETURN pin)
FILTER (cleanup == null OR cleanup.expiresAt < @now) AND owned != null AND owned.expiresAt >= @now
UPDATE state WITH {pins: APPEND(retained, [{ownerId: @owner, expiresAt: @expiresAt}]), updatedAt: @now} IN @@collection
	RETURN {claimed: true}`, r.batchSize, map[string]interface{}{"@collection": ExecutionReadPinsCollection, "stateKey": executionStateKey(executionID), "owner": owner, "expiresAt": expires, "now": now}, func(row map[string]any) error {
		claimed, _ = row["claimed"].(bool)
		return nil
	})
	return claimed, err
}

func (r *Registry) ReleaseExecutionReadPin(ctx context.Context, executionID, owner string) error {
	return r.removeReadPin(ctx, executionID, owner, "PIN")
}

func (r *Registry) ClaimExecutionCleanup(ctx context.Context, executionID, owner string) (bool, error) {
	now := time.Now().UTC()
	claimed := false
	err := r.client.QueryRows(ctx, `LET state = DOCUMENT(@@collection, @stateKey)
LET pins = state == null ? [] : NOT_NULL(state.pins, [])
LET active = (FOR pin IN pins FILTER pin.expiresAt >= @now RETURN pin)
LET cleanup = state == null ? null : state.cleanup
FILTER LENGTH(active) == 0 AND (cleanup == null OR cleanup.expiresAt < @now OR cleanup.ownerId == @owner)
UPSERT {_key: @stateKey}
INSERT {_key: @stateKey, executionId: @executionId, pins: [], cleanup: {ownerId: @owner, expiresAt: @expiresAt}, updatedAt: @now}
UPDATE {pins: [], cleanup: {ownerId: @owner, expiresAt: @expiresAt}, updatedAt: @now} IN @@collection
	RETURN {claimed: true}`, r.batchSize, map[string]interface{}{"@collection": ExecutionReadPinsCollection, "stateKey": executionStateKey(executionID), "executionId": executionID, "owner": owner, "expiresAt": now.Add(executionCleanupTTL), "now": now}, func(row map[string]any) error {
		claimed, _ = row["claimed"].(bool)
		return nil
	})
	return claimed, err
}

func (r *Registry) RenewExecutionCleanup(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	now := time.Now().UTC()
	claimed := false
	err := r.client.QueryRows(ctx, `FOR state IN @@collection
FILTER state._key == @stateKey
LET pins = NOT_NULL(state.pins, [])
LET cleanup = state.cleanup
LET active = (FOR pin IN pins FILTER pin.expiresAt >= @now RETURN pin)
FILTER cleanup != null AND cleanup.ownerId == @owner AND cleanup.expiresAt >= @now AND LENGTH(active) == 0
UPDATE state WITH {cleanup: {ownerId: @owner, expiresAt: @expiresAt}, updatedAt: @now} IN @@collection
	RETURN {claimed: true}`, r.batchSize, map[string]interface{}{"@collection": ExecutionReadPinsCollection, "stateKey": executionStateKey(executionID), "owner": owner, "expiresAt": expires, "now": now}, func(row map[string]any) error {
		claimed, _ = row["claimed"].(bool)
		return nil
	})
	return claimed, err
}

func (r *Registry) ReleaseExecutionCleanup(ctx context.Context, executionID, owner string) error {
	return r.removeReadPin(ctx, executionID, owner, "CLEANUP")
}

func (r *Registry) removeReadPin(ctx context.Context, executionID, owner, kind string) error {
	now := time.Now().UTC()
	if kind == "PIN" {
		return r.client.QueryRows(ctx, `FOR state IN @@collection
FILTER state._key == @stateKey
LET pins = NOT_NULL(state.pins, [])
LET retained = (FOR pin IN pins FILTER pin.ownerId != @owner RETURN pin)
UPDATE state WITH {pins: retained, updatedAt: @now} IN @@collection
		RETURN {released: true}`, r.batchSize, map[string]interface{}{"@collection": ExecutionReadPinsCollection, "stateKey": executionStateKey(executionID), "owner": owner, "now": now}, func(map[string]any) error { return nil })
	}
	return r.client.QueryRows(ctx, `FOR state IN @@collection
FILTER state._key == @stateKey
LET cleanup = state.cleanup
FILTER cleanup != null AND cleanup.ownerId == @owner
UPDATE state WITH {cleanup: null, updatedAt: @now} IN @@collection
RETURN {released: true}`, r.batchSize, map[string]interface{}{"@collection": ExecutionReadPinsCollection, "stateKey": executionStateKey(executionID), "owner": owner, "now": now}, func(map[string]any) error { return nil })
}

func executionStateKey(executionID string) string {
	sum := sha256.Sum256([]byte("state\x00" + executionID))
	return "state_" + hex.EncodeToString(sum[:])
}

func pointerDocumentKey(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

var _ publication.BundleCatalog = (*Registry)(nil)
var _ publication.ExactExecutionCatalog = (*Registry)(nil)
var _ publication.PagedBundleCatalog = (*Registry)(nil)
var _ publication.ExecutionReadPinCatalog = (*Registry)(nil)
