package surreal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"arangodb-proto/internal/store"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	surrealhttp "github.com/surrealdb/surrealdb.go/pkg/connection/http"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

type Client struct {
	db *surrealdb.DB
}

var identSanitizer = regexp.MustCompile(`[^A-Za-z0-9_]`)

func Open(ctx context.Context, url, namespace, database, username, password, authToken string) (*Client, error) {
	parsedURL, err := urlpkgParse(url)
	if err != nil {
		return nil, err
	}
	conf := connection.NewConfig(parsedURL)

	var conn connection.Connection
	switch parsedURL.Scheme {
	case "http", "https":
		conn = surrealhttp.New(conf).SetTimeout(5 * time.Minute)
	case "ws", "wss":
		conn = gorillaws.New(conf).SetTimeOut(0)
	default:
		db, err := surrealdb.FromEndpointURLString(ctx, url)
		if err != nil {
			return nil, err
		}
		return openWithDB(ctx, db, namespace, database, username, password, authToken)
	}

	db, err := surrealdb.FromConnection(ctx, conn)
	if err != nil {
		return nil, err
	}
	return openWithDB(ctx, db, namespace, database, username, password, authToken)
}

func openWithDB(ctx context.Context, db *surrealdb.DB, namespace, database, username, password, authToken string) (*Client, error) {
	if strings.TrimSpace(namespace) == "" {
		namespace = "test"
	}
	if strings.TrimSpace(database) == "" {
		database = "test"
	}

	if strings.TrimSpace(username) != "" {
		_, err := db.SignIn(ctx, surrealdb.Auth{
			Username: username,
			Password: password,
		})
		if err != nil {
			_ = db.Close(ctx)
			return nil, err
		}
	}

	if strings.TrimSpace(authToken) != "" {
		if err := db.Authenticate(ctx, authToken); err != nil {
			_ = db.Close(ctx)
			return nil, err
		}
	}
	nsIdent := sanitizeIdentifier(namespace)
	dbIdent := sanitizeIdentifier(database)
	setupQuery := fmt.Sprintf(`
DEFINE NAMESPACE IF NOT EXISTS %s;
USE NS %s;
DEFINE DATABASE IF NOT EXISTS %s;
USE DB %s;
`, nsIdent, nsIdent, dbIdent, dbIdent)
	if _, err := surrealdb.Query[any](ctx, db, setupQuery, nil); err != nil {
		_ = db.Close(ctx)
		return nil, fmt.Errorf("setup namespace %s database %s: %w", namespace, database, err)
	}
	if err := db.Use(ctx, namespace, database); err != nil {
		_ = db.Close(ctx)
		return nil, fmt.Errorf("use namespace %s database %s: %w", namespace, database, err)
	}

	return &Client{db: db}, nil
}

func urlpkgParse(raw string) (*url.URL, error) {
	return url.ParseRequestURI(raw)
}

func (c *Client) Bootstrap(ctx context.Context, spec store.BootstrapSpec) error {
	total := len(spec.Collections)
	for i, collection := range spec.Collections {
		tableName := sanitizeIdentifier(collection.Name)
		tableKind := "SCHEMALESS"
		if collection.Edge {
			tableKind = "TYPE RELATION SCHEMALESS"
		}
		indexes := surrealBootstrapIndexes(collection)
		reportBootstrap(spec, "go_bootstrap_collection_start", map[string]any{
			"backend":    "surreal",
			"collection": collection.Name,
			"edge":       collection.Edge,
			"truncate":   collection.Truncate,
			"position":   i + 1,
			"total":      total,
		})

		if _, err := surrealdb.Query[[]map[string]any](ctx, c.db, fmt.Sprintf("DEFINE TABLE IF NOT EXISTS %s %s;", tableName, tableKind), nil); err != nil {
			return fmt.Errorf("define table %s: %w", collection.Name, err)
		}
		reportBootstrap(spec, "go_bootstrap_collection_ready", map[string]any{
			"backend":    "surreal",
			"collection": collection.Name,
			"position":   i + 1,
			"total":      total,
		})
		if collection.Truncate {
			reportBootstrap(spec, "go_bootstrap_collection_truncate_start", map[string]any{
				"backend":    "surreal",
				"collection": collection.Name,
				"position":   i + 1,
				"total":      total,
			})
			if _, err := surrealdb.Query[[]map[string]any](ctx, c.db, fmt.Sprintf("DELETE %s RETURN NONE;", tableName), nil); err != nil {
				return fmt.Errorf("truncate table %s: %w", collection.Name, err)
			}
			reportBootstrap(spec, "go_bootstrap_collection_truncate_complete", map[string]any{
				"backend":    "surreal",
				"collection": collection.Name,
				"position":   i + 1,
				"total":      total,
			})
		}
		reportBootstrap(spec, "go_bootstrap_collection_indexes_start", map[string]any{
			"backend":    "surreal",
			"collection": collection.Name,
			"indexes":    len(indexes),
			"position":   i + 1,
			"total":      total,
		})
		for _, fields := range indexes {
			if len(fields) == 0 {
				continue
			}
			indexName := surrealIndexName(collection.Name, fields)
			if _, err := surrealdb.Query[[]map[string]any](ctx, c.db, fmt.Sprintf(
				"DEFINE INDEX IF NOT EXISTS %s ON TABLE %s FIELDS %s;",
				indexName,
				tableName,
				strings.Join(fields, ", "),
			), nil); err != nil {
				return fmt.Errorf("define index %s on %s: %w", indexName, collection.Name, err)
			}
		}
		reportBootstrap(spec, "go_bootstrap_collection_complete", map[string]any{
			"backend":    "surreal",
			"collection": collection.Name,
			"indexes":    len(indexes),
			"position":   i + 1,
			"total":      total,
		})
	}
	return nil
}

func (c *Client) InsertBatchRaw(ctx context.Context, collection string, docs []json.RawMessage, overwrite bool, writeAPI string) error {
	if len(docs) == 0 {
		return nil
	}
	if collection == "fhir_edge" {
		return c.insertBatchRelationRaw(ctx, collection, docs, overwrite)
	}
	content := make([]map[string]any, 0, len(docs))
	for i, raw := range docs {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("decode %s batch document %d: %w", collection, i, err)
		}
		key, _ := doc["_key"].(string)
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s batch document %d missing _key", collection, i)
		}
		content = append(content, doc)
	}

	query := fmt.Sprintf(`
FOR $doc IN $content {
  %s type::record(%q, $doc._key) CONTENT $doc RETURN NONE;
};
`, surrealWriteVerb(overwrite), sanitizeIdentifier(collection))
	_, err := surrealdb.Query[any](ctx, c.db, query, map[string]any{"content": content})
	if err != nil {
		return fmt.Errorf("insert %s batch: %w", collection, err)
	}
	return nil
}

func (c *Client) insertBatchRelationRaw(ctx context.Context, collection string, docs []json.RawMessage, overwrite bool) error {
	content := make([]map[string]any, 0, len(docs))
	ids := make([]any, 0, len(docs))
	for i, raw := range docs {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("decode %s batch relation %d: %w", collection, i, err)
		}
		key, _ := doc["_key"].(string)
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s batch relation %d missing _key", collection, i)
		}
		inTable, inID, err := splitCollectionID(stringValue(doc["_from"]))
		if err != nil {
			return fmt.Errorf("decode relation _from for %s/%s: %w", collection, key, err)
		}
		outTable, outID, err := splitCollectionID(stringValue(doc["_to"]))
		if err != nil {
			return fmt.Errorf("decode relation _to for %s/%s: %w", collection, key, err)
		}
		content = append(content, map[string]any{
			"id":        models.NewRecordID(collection, key),
			"in":        models.NewRecordID(inTable, inID),
			"out":       models.NewRecordID(outTable, outID),
			"_from":     stringValue(doc["_from"]),
			"_to":       stringValue(doc["_to"]),
			"_key":      key,
			"label":     stringValue(doc["label"]),
			"project":   stringValue(doc["project"]),
			"from_type": stringValue(doc["from_type"]),
			"to_type":   stringValue(doc["to_type"]),
		})
		ids = append(ids, models.NewRecordID(collection, key))
	}
	query := fmt.Sprintf("INSERT RELATION INTO %s $content RETURN NONE", sanitizeIdentifier(collection))
	params := map[string]any{"content": content}
	if overwrite {
		query = fmt.Sprintf("DELETE $ids RETURN NONE; INSERT RELATION INTO %s $content RETURN NONE", sanitizeIdentifier(collection))
		params["ids"] = ids
	}
	_, err := surrealdb.Query[any](ctx, c.db, query, params)
	if err != nil {
		return fmt.Errorf("insert relation batch %s: %w", collection, err)
	}
	return nil
}

func (c *Client) QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit store.RowVisitor) error {
	results, err := surrealdb.Query[any](ctx, c.db, query, bindVars)
	if err != nil {
		return err
	}
	if results == nil {
		return nil
	}
	for _, result := range *results {
		if result.Error != nil {
			return result.Error
		}
		switch rows := result.Result.(type) {
		case []any:
			for _, item := range rows {
				row, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if err := visit(row); err != nil {
					return err
				}
			}
		case map[string]any:
			if err := visit(rows); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close(ctx)
}

func sanitizeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "x"
	}
	return identSanitizer.ReplaceAllString(value, "_")
}

func surrealIndexName(collection string, fields []string) string {
	parts := make([]string, 0, len(fields)+1)
	parts = append(parts, collection)
	parts = append(parts, fields...)
	return sanitizeIdentifier(strings.Join(parts, "_"))
}

func splitCollectionID(value string) (string, string, error) {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("malformed collection id %q", value)
	}
	return parts[0], parts[1], nil
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func surrealWriteVerb(overwrite bool) string {
	if overwrite {
		return "UPSERT"
	}
	return "CREATE"
}

func surrealBootstrapIndexes(collection store.CollectionSpec) [][]string {
	indexes := append([][]string(nil), collection.Indexes...)
	if !collection.Edge {
		return indexes
	}
	extra := [][][]string{
		{{"project", "label", "_to"}},
		{{"project", "label", "_from"}},
		{{"project", "from_type", "label", "_to"}},
		{{"project", "to_type", "label", "_from"}},
	}
	for _, group := range extra {
		for _, fields := range group {
			if !hasIndexFields(indexes, fields) {
				indexes = append(indexes, fields)
			}
		}
	}
	return indexes
}

func hasIndexFields(indexes [][]string, fields []string) bool {
	for _, existing := range indexes {
		if len(existing) != len(fields) {
			continue
		}
		match := true
		for i := range fields {
			if existing[i] != fields[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func reportBootstrap(spec store.BootstrapSpec, event string, fields map[string]any) {
	if spec.Reporter != nil {
		spec.Reporter(event, fields)
	}
}
