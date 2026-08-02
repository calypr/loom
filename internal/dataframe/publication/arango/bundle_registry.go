package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
)

const (
	BundleExecutionsCollection = "loom_dataframe_bundle_executions"
	BundlePointersCollection   = "loom_dataframe_bundle_pointers"
	BundleLeasesCollection     = "loom_dataframe_bundle_leases"
)

// AQLExecutor is implemented by Loom's concrete Arango client. Keeping it
// optional preserves the small fake Client used by existing registry tests.
type AQLExecutor interface {
	ExecuteAQL(context.Context, string, map[string]interface{}) error
}

func (r *Registry) SaveExecution(ctx context.Context, execution publication.BundleExecution) error {
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
	return r.client.InsertBatchRaw(ctx, BundleExecutionsCollection, []json.RawMessage{data}, true, "document")
}

func (r *Registry) GetExecution(ctx context.Context, id string) (publication.BundleExecution, error) {
	return r.loadExecution(ctx, `FOR doc IN @@collection FILTER doc._key == @key RETURN doc`, map[string]interface{}{"@collection": BundleExecutionsCollection, "key": id})
}

func (r *Registry) FindExecutionByKey(ctx context.Context, key string) (publication.BundleExecution, error) {
	return r.loadExecution(ctx, `FOR doc IN @@collection FILTER doc.key == @key SORT doc.createdAt DESC LIMIT 1 RETURN doc`, map[string]interface{}{"@collection": BundleExecutionsCollection, "key": key})
}

func (r *Registry) ListExecutions(ctx context.Context, state publication.BundleState, before time.Time) ([]publication.BundleExecution, error) {
	out := []publication.BundleExecution{}
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc.state == @state AND doc.updatedAt < @before SORT doc.updatedAt ASC RETURN doc`, r.batchSize, map[string]interface{}{"@collection": BundleExecutionsCollection, "state": state, "before": before}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var execution publication.BundleExecution
		if err := json.Unmarshal(data, &execution); err != nil {
			return err
		}
		out = append(out, execution)
		return nil
	})
	return out, err
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
	executor, ok := r.client.(AQLExecutor)
	if !ok {
		return nil
	}
	return executor.ExecuteAQL(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing != null AND existing.ownerId == @owner
REMOVE existing IN @@collection`, map[string]interface{}{"@collection": BundleLeasesCollection, "key": key, "owner": owner})
}

func pointerDocumentKey(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

var _ publication.BundleCatalog = (*Registry)(nil)
var _ publication.StaleBundleCatalog = (*Registry)(nil)
