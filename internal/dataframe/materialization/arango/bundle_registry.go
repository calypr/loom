package arango

import (
	"context"
	"encoding/json"
	"time"

	"github.com/calypr/loom/internal/dataframe/materialization"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const (
	BundleExecutionsCollection = "loom_dataframe_bundle_executions"
	BundlePointersCollection   = "loom_dataframe_bundle_pointers"
)

// AQLExecutor is implemented by Loom's concrete Arango client. Keeping it
// optional preserves the small fake Client used by existing registry tests.
type AQLExecutor interface {
	ExecuteAQL(context.Context, string, map[string]interface{}) error
}

func BundleBootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{
		{Name: BundleExecutionsCollection, Indexes: [][]string{{"key"}, {"state"}, {"name", "state"}}},
		{Name: BundlePointersCollection, Indexes: [][]string{{"executionId"}}},
	}}
}

func (r *Registry) SaveExecution(ctx context.Context, execution materialization.BundleExecution) error {
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

func (r *Registry) GetExecution(ctx context.Context, id string) (materialization.BundleExecution, error) {
	return r.loadExecution(ctx, `FOR doc IN @@collection FILTER doc._key == @key RETURN doc`, map[string]interface{}{"@collection": BundleExecutionsCollection, "key": id})
}

func (r *Registry) FindExecutionByKey(ctx context.Context, key string) (materialization.BundleExecution, error) {
	return r.loadExecution(ctx, `FOR doc IN @@collection FILTER doc.key == @key SORT doc.createdAt DESC LIMIT 1 RETURN doc`, map[string]interface{}{"@collection": BundleExecutionsCollection, "key": key})
}

func (r *Registry) ListExecutions(ctx context.Context, state materialization.BundleState, before time.Time) ([]materialization.BundleExecution, error) {
	out := []materialization.BundleExecution{}
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc.state == @state AND doc.updatedAt < @before SORT doc.updatedAt ASC RETURN doc`, r.batchSize, map[string]interface{}{"@collection": BundleExecutionsCollection, "state": state, "before": before}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var execution materialization.BundleExecution
		if err := json.Unmarshal(data, &execution); err != nil {
			return err
		}
		out = append(out, execution)
		return nil
	})
	return out, err
}

func (r *Registry) loadExecution(ctx context.Context, query string, vars map[string]interface{}) (materialization.BundleExecution, error) {
	var found *materialization.BundleExecution
	err := r.client.QueryRows(ctx, query, r.batchSize, vars, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var execution materialization.BundleExecution
		if err := json.Unmarshal(data, &execution); err != nil {
			return err
		}
		found = &execution
		return nil
	})
	if err != nil {
		return materialization.BundleExecution{}, err
	}
	if found == nil {
		return materialization.BundleExecution{}, materialization.ErrBundleNotFound
	}
	return *found, nil
}

func (r *Registry) GetPointer(ctx context.Context, name string) (materialization.BundlePointer, error) {
	var found *materialization.BundlePointer
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc._key == @key RETURN doc`, r.batchSize, map[string]interface{}{"@collection": BundlePointersCollection, "key": name}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var pointer materialization.BundlePointer
		if err := json.Unmarshal(data, &pointer); err != nil {
			return err
		}
		found = &pointer
		return nil
	})
	if err != nil {
		return materialization.BundlePointer{}, err
	}
	if found == nil {
		return materialization.BundlePointer{}, materialization.ErrBundleNotFound
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
	err := r.client.QueryRows(ctx, `LET existing = FIRST(FOR doc IN @@collection FILTER doc._key == @key RETURN doc)
LET changed = existing == null ? (INSERT {_key: @key, name: @key, executionId: @next, updatedAt: @updatedAt} IN @@collection RETURN true) : (existing.executionId == @expected ? (UPDATE existing WITH {executionId: @next, updatedAt: @updatedAt} IN @@collection RETURN true) : [false])
RETURN {updated: FIRST(changed)}`, r.batchSize, map[string]interface{}{"@collection": BundlePointersCollection, "key": name, "expected": expected, "next": next, "updatedAt": time.Now().UTC()}, func(row map[string]any) error {
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
		return materialization.ErrBundlePointerConflict
	}
	return nil
}

var _ materialization.BundleCatalog = (*Registry)(nil)
var _ materialization.StaleBundleCatalog = (*Registry)(nil)
