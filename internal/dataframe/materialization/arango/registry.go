package arango

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/materialization"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const Collection = "loom_dataframe_materializations"

type Client interface {
	QueryRows(context.Context, string, int, map[string]interface{}, arangostore.RowVisitor) error
	InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error
}

type Registry struct {
	client    Client
	batchSize int
}

func New(client Client) (*Registry, error) {
	if client == nil {
		return nil, fmt.Errorf("Arango materialization registry client is required")
	}
	return &Registry{client: client, batchSize: 32}, nil
}

func BootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{
		Name:    Collection,
		Indexes: [][]string{{"project", "state"}, {"project", "datasetGeneration"}, {"state"}},
	}, {
		Name:    BundleExecutionsCollection,
		Indexes: [][]string{{"key"}, {"state"}, {"project", "datasetGeneration", "name", "state"}},
	}, {
		Name:    BundlePointersCollection,
		Indexes: [][]string{{"executionId"}},
	}, {
		Name:    BundleLeasesCollection,
		Indexes: [][]string{{"expiresAt"}},
	}}}
}

func (r *Registry) Save(ctx context.Context, m materialization.Materialization) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	doc["_key"] = m.ID
	data, err = json.Marshal(doc)
	if err != nil {
		return err
	}
	return r.client.InsertBatchRaw(ctx, Collection, []json.RawMessage{data}, true, "document")
}

func (r *Registry) Get(ctx context.Context, id string) (materialization.Materialization, error) {
	var found *materialization.Materialization
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc._key == @key RETURN doc`, r.batchSize, map[string]interface{}{"@collection": Collection, "key": id}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var value materialization.Materialization
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		found = &value
		return nil
	})
	if err != nil {
		return materialization.Materialization{}, err
	}
	if found == nil {
		return materialization.Materialization{}, fmt.Errorf("materialization %q not found", id)
	}
	return *found, nil
}

func (r *Registry) ListReady(ctx context.Context, project string) ([]materialization.Materialization, error) {
	out := []materialization.Materialization{}
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc.project == @project AND doc.state == @state SORT doc.createdAt ASC RETURN doc`, r.batchSize, map[string]interface{}{"@collection": Collection, "project": project, "state": string(materialization.StateReady)}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var value materialization.Materialization
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		out = append(out, value)
		return nil
	})
	return out, err
}
