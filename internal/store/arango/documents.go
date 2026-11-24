package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/arangodb/shared"
)

func (c *Client) CollectionNamesReadOnly(ctx context.Context) ([]string, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("collection audit requires an open database")
	}
	collections, err := c.db.Collections(ctx)
	if err != nil {
		return nil, fmt.Errorf("list existing collections: %w", err)
	}
	names := make([]string, 0, len(collections))
	for _, collection := range collections {
		names = append(names, collection.Name())
	}
	sort.Strings(names)
	return names, nil
}

func StringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func (c *Client) VisitCollectionDocumentsRaw(ctx context.Context, collection string, visit func(json.RawMessage) error) (resultErr error) {
	if c == nil || c.db == nil || collection == "" || visit == nil {
		return errors.New("raw collection audit requires an open database, collection, and visitor")
	}
	if exists, err := c.db.CollectionExists(ctx, collection); err != nil {
		return fmt.Errorf("check existing collection %q: %w", collection, err)
	} else if !exists {
		return fmt.Errorf("collection %q does not exist; audit will not create it", collection)
	}
	cursor, err := c.db.Query(ctx, `FOR document IN @@collection RETURN document`, &driver.QueryOptions{
		BatchSize: 1000, BindVars: map[string]any{"@collection": collection},
	})
	if err != nil {
		return fmt.Errorf("read documents in %q: %w", collection, err)
	}
	defer func() { resultErr = errors.Join(resultErr, cursor.Close()) }()
	for cursor.HasMore() {
		var raw json.RawMessage
		if _, err := cursor.ReadDocument(ctx, &raw); err != nil {
			if shared.IsNoMoreDocuments(err) {
				break
			}
			return fmt.Errorf("read document in %q: %w", collection, err)
		}
		if err := visit(raw); err != nil {
			return fmt.Errorf("visit document in %q: %w", collection, err)
		}
	}
	return ctx.Err()
}
