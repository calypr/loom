// Package arango provides the durable exec.Store adapter used by Loom
// deployments that already persist dataset metadata in ArangoDB.
package arango

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const Collection = "loom_dataframe_recipes"

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
		return nil, fmt.Errorf("Arango recipe registry client is required")
	}
	return &Registry{client: client, batchSize: 32}, nil
}

func BootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{Name: Collection, Indexes: [][]string{{"name"}, {"digest"}}}}}
}

func (r *Registry) SaveRecipe(ctx context.Context, entry exec.Entry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	doc["name"] = entry.Bundle.Name
	doc["digest"] = entry.Digest
	doc["_key"] = entry.Digest
	data, err = json.Marshal(doc)
	if err != nil {
		return err
	}
	return r.client.InsertBatchRaw(ctx, Collection, []json.RawMessage{data}, true, "document")
}

func (r *Registry) LoadRecipe(ctx context.Context, name string) (exec.Entry, error) {
	var found *exec.Entry
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc.name == @name RETURN doc`, r.batchSize, map[string]interface{}{"@collection": Collection, "name": name}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var entry exec.Entry
		if err := json.Unmarshal(data, &entry); err != nil {
			return err
		}
		found = &entry
		return nil
	})
	if err != nil {
		return exec.Entry{}, err
	}
	if found == nil {
		return exec.Entry{}, fmt.Errorf("%w: %s", exec.ErrRecipeNotFound, name)
	}
	return *found, nil
}
