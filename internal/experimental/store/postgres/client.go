package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"arangodb-proto/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	edgeCollection              = "fhir_edge"
	fieldCatalogCollection      = "fhir_field_catalog"
	patientFileRollupCollection = "patient_file_rollup"
	scalarCollection            = "fhir_scalar_index"
)

type ScalarIndexRow struct {
	Project      string
	ResourceKey  string
	ResourceType string
	Path         string
	ValueText    string
	ValueNum     any
	System       string
	Code         string
	Display      string
	Ordinal      int
}

type Client struct {
	pool *pgxpool.Pool
}

var keyCleaner = regexp.MustCompile(`[^A-Za-z0-9_\-:.@()+,=;$!*'%]`)

func Open(ctx context.Context, rawURL, database, username, password string) (*Client, error) {
	dsn := postgresDSN(rawURL, database, username, password)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 16
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 10 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Client{pool: pool}, nil
}

func postgresDSN(rawURL, database, username, password string) string {
	rawURL = strings.TrimSpace(rawURL)
	if strings.HasPrefix(rawURL, "postgres://") || strings.HasPrefix(rawURL, "postgresql://") {
		return rawURL
	}
	if rawURL == "" || strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		rawURL = "127.0.0.1:5432"
	}
	if database == "" {
		database = "fhir_proto"
	}
	if username == "" {
		username = "postgres"
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(username, password),
		Host:   rawURL,
		Path:   "/" + database,
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *Client) Bootstrap(ctx context.Context, spec store.BootstrapSpec) error {
	if err := c.ensureSchema(ctx); err != nil {
		return err
	}
	if collectionsToTruncate := truncateCollections(spec.Collections); len(collectionsToTruncate) > 0 {
		reportBootstrap(spec, "go_bootstrap_postgres_truncate_start", map[string]any{"backend": "postgres"})
		if err := c.truncateCollections(ctx, collectionsToTruncate); err != nil {
			return err
		}
		reportBootstrap(spec, "go_bootstrap_postgres_truncate_complete", map[string]any{"backend": "postgres"})
	}

	total := len(spec.Collections)
	for i, collection := range spec.Collections {
		reportBootstrap(spec, "go_bootstrap_collection_start", map[string]any{
			"backend":    "postgres",
			"collection": collection.Name,
			"edge":       collection.Edge,
			"truncate":   collection.Truncate,
			"position":   i + 1,
			"total":      total,
		})
		reportBootstrap(spec, "go_bootstrap_collection_ready", map[string]any{
			"backend":    "postgres",
			"collection": collection.Name,
			"position":   i + 1,
			"total":      total,
		})
		reportBootstrap(spec, "go_bootstrap_collection_indexes_start", map[string]any{
			"backend":    "postgres",
			"collection": collection.Name,
			"indexes":    len(collection.Indexes),
			"position":   i + 1,
			"total":      total,
		})
		reportBootstrap(spec, "go_bootstrap_collection_complete", map[string]any{
			"backend":    "postgres",
			"collection": collection.Name,
			"indexes":    len(collection.Indexes),
			"position":   i + 1,
			"total":      total,
		})
	}
	return nil
}

func (c *Client) ensureSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS fhir_resource (
			project text NOT NULL,
			resource_key text NOT NULL,
			resource_type text NOT NULL,
			logical_id text NOT NULL,
			auth_resource_path text,
			source_collection text NOT NULL,
			body jsonb NOT NULL,
			updated_at timestamptz DEFAULT now(),
			PRIMARY KEY (project, resource_key)
		);`,
		`CREATE INDEX IF NOT EXISTS fhir_resource_type_key_idx ON fhir_resource (project, resource_type, resource_key);`,
		`CREATE INDEX IF NOT EXISTS fhir_resource_logical_idx ON fhir_resource (project, resource_type, logical_id);`,
		`CREATE INDEX IF NOT EXISTS fhir_resource_auth_type_key_idx ON fhir_resource (project, auth_resource_path, resource_type, resource_key);`,
		`CREATE TABLE IF NOT EXISTS fhir_edge (
			project text NOT NULL,
			edge_key text NOT NULL,
			src_key text NOT NULL,
			src_type text NOT NULL,
			edge_type text NOT NULL,
			path text NOT NULL,
			dst_key text NOT NULL,
			dst_type text NOT NULL,
			ordinal int,
			created_at timestamptz DEFAULT now(),
			PRIMARY KEY (project, edge_key)
		);`,
		`CREATE INDEX IF NOT EXISTS fhir_edge_forward_idx ON fhir_edge (project, src_key, edge_type, dst_type, dst_key);`,
		`CREATE INDEX IF NOT EXISTS fhir_edge_forward_type_idx ON fhir_edge (project, src_type, edge_type, dst_type, src_key, dst_key);`,
		`CREATE INDEX IF NOT EXISTS fhir_edge_reverse_idx ON fhir_edge (project, dst_key, edge_type, src_type, src_key);`,
		`CREATE INDEX IF NOT EXISTS fhir_edge_reverse_type_idx ON fhir_edge (project, dst_type, edge_type, src_type, dst_key, src_key);`,
		`CREATE INDEX IF NOT EXISTS fhir_edge_edge_type_idx ON fhir_edge (project, edge_type, src_type, dst_type);`,
		`CREATE TABLE IF NOT EXISTS fhir_scalar_index (
			project text NOT NULL,
			resource_key text NOT NULL,
			resource_type text NOT NULL,
			path text NOT NULL,
			value_text text,
			value_num double precision,
			system text,
			code text,
			display text,
			ordinal int,
			PRIMARY KEY (project, resource_key, path, ordinal)
		);`,
		`CREATE INDEX IF NOT EXISTS fhir_scalar_path_text_idx ON fhir_scalar_index (project, resource_type, path, value_text);`,
		`CREATE INDEX IF NOT EXISTS fhir_scalar_code_idx ON fhir_scalar_index (project, resource_type, path, system, code, display);`,
		`CREATE TABLE IF NOT EXISTS fhir_field_catalog (
			key text PRIMARY KEY,
			project text NOT NULL,
			resource_type text NOT NULL,
			path text NOT NULL,
			kind text NOT NULL,
			doc_count bigint NOT NULL,
			sample_count bigint NOT NULL,
			distinct_values text[],
			distinct_truncated boolean NOT NULL,
			pivot_candidate boolean NOT NULL,
			pivot_kind text,
			pivot_columns text[]
		);`,
		`CREATE INDEX IF NOT EXISTS fhir_field_catalog_project_resource_idx ON fhir_field_catalog (project, resource_type);`,
		`CREATE INDEX IF NOT EXISTS fhir_field_catalog_project_resource_path_idx ON fhir_field_catalog (project, resource_type, path);`,
		`CREATE INDEX IF NOT EXISTS fhir_field_catalog_pivot_idx ON fhir_field_catalog (project, resource_type, pivot_candidate);`,
		`CREATE TABLE IF NOT EXISTS patient_file_rollup (
			key text PRIMARY KEY,
			project text NOT NULL,
			patient_key text NOT NULL,
			auth_resource_path text,
			specimen_count int NOT NULL,
			group_count int NOT NULL,
			file_count int NOT NULL,
			specimen_types text[],
			preservation_methods text[],
			file_keys text[]
		);`,
		`CREATE INDEX IF NOT EXISTS patient_file_rollup_project_patient_idx ON patient_file_rollup (project, patient_key);`,
		`CREATE INDEX IF NOT EXISTS patient_file_rollup_auth_patient_idx ON patient_file_rollup (project, auth_resource_path, patient_key);`,
	}
	for _, stmt := range stmts {
		if _, err := c.pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) InsertBatchRaw(ctx context.Context, collection string, docs []json.RawMessage, overwrite bool, writeAPI string) error {
	if len(docs) == 0 {
		return nil
	}
	switch collection {
	case edgeCollection:
		return c.insertEdges(ctx, docs, overwrite)
	case scalarCollection:
		return c.insertScalarIndexDocuments(ctx, docs, overwrite)
	case fieldCatalogCollection:
		return c.insertFieldCatalog(ctx, docs, overwrite)
	case patientFileRollupCollection:
		return c.insertPatientFileRollups(ctx, docs, overwrite)
	default:
		return c.insertResources(ctx, collection, docs, overwrite)
	}
}

func (c *Client) insertResources(ctx context.Context, collection string, docs []json.RawMessage, overwrite bool) error {
	rows := make([][]any, 0, len(docs))
	for i, raw := range docs {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("decode resource %s document %d: %w", collection, i, err)
		}
		key := stringValue(doc["_key"])
		logicalID := stringValue(doc["id"])
		resourceType := stringValue(doc["resourceType"])
		project := stringValue(doc["project"])
		authResourcePath := stringValue(doc["auth_resource_path"])
		payload := doc["payload"]
		if key == "" || resourceType == "" || project == "" || payload == nil {
			return fmt.Errorf("resource %s document %d missing _key/resourceType/project/payload", collection, i)
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal resource payload %s/%s: %w", resourceType, key, err)
		}
		resourceKey := collectionID(resourceType, key)
		rows = append(rows, []any{project, resourceKey, resourceType, logicalID, nullableText(authResourcePath), collection, string(body)})
	}
	if !overwrite {
		if _, err := c.pool.CopyFrom(ctx, pgx.Identifier{"fhir_resource"}, []string{
			"project", "resource_key", "resource_type", "logical_id", "auth_resource_path", "source_collection", "body",
		}, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
		return nil
	}
	if err := c.batchExec(ctx, `INSERT INTO fhir_resource (
		project, resource_key, resource_type, logical_id, auth_resource_path, source_collection, body
	) VALUES ($1, $2, $3, $4, $5, $6, $7)
	ON CONFLICT (project, resource_key) DO UPDATE SET
		resource_type = EXCLUDED.resource_type,
		logical_id = EXCLUDED.logical_id,
		auth_resource_path = EXCLUDED.auth_resource_path,
		source_collection = EXCLUDED.source_collection,
		body = EXCLUDED.body,
		updated_at = now()`, rows); err != nil {
		return err
	}
	return nil
}

func (c *Client) insertScalarRows(ctx context.Context, rows []ScalarIndexRow) error {
	if len(rows) == 0 {
		return nil
	}
	copyRows := make([][]any, 0, len(rows))
	for _, row := range rows {
		copyRows = append(copyRows, []any{
			row.Project,
			row.ResourceKey,
			row.ResourceType,
			row.Path,
			nullableText(row.ValueText),
			row.ValueNum,
			nullableText(row.System),
			nullableText(row.Code),
			nullableText(row.Display),
			row.Ordinal,
		})
	}
	_, err := c.pool.CopyFrom(ctx, pgx.Identifier{"fhir_scalar_index"}, []string{
		"project", "resource_key", "resource_type", "path", "value_text", "value_num", "system", "code", "display", "ordinal",
	}, pgx.CopyFromRows(copyRows))
	return err
}

func (c *Client) insertScalarIndexDocuments(ctx context.Context, docs []json.RawMessage, overwrite bool) error {
	rows := make([]ScalarIndexRow, 0, len(docs))
	for i, raw := range docs {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("decode scalar index document %d: %w", i, err)
		}
		rows = append(rows, ScalarIndexRow{
			Project:      stringValue(doc["project"]),
			ResourceKey:  stringValue(doc["resource_key"]),
			ResourceType: stringValue(doc["resource_type"]),
			Path:         stringValue(doc["path"]),
			ValueText:    stringValue(doc["value_text"]),
			ValueNum:     numericValue(doc["value_num"]),
			System:       stringValue(doc["system"]),
			Code:         stringValue(doc["code"]),
			Display:      stringValue(doc["display"]),
			Ordinal:      int(int64Value(doc["ordinal"])),
		})
	}
	if overwrite {
		return fmt.Errorf("overwrite mode is not supported for %s inserts", scalarCollection)
	}
	return c.insertScalarRows(ctx, rows)
}

func (c *Client) insertEdges(ctx context.Context, docs []json.RawMessage, overwrite bool) error {
	rows := make([][]any, 0, len(docs))
	for i, raw := range docs {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("decode edge document %d: %w", i, err)
		}
		key := stringValue(doc["_key"])
		from := stringValue(doc["_from"])
		to := stringValue(doc["_to"])
		label := stringValue(doc["label"])
		project := stringValue(doc["project"])
		fromType := stringValue(doc["from_type"])
		toType := stringValue(doc["to_type"])
		if key == "" || from == "" || to == "" || label == "" || project == "" {
			return fmt.Errorf("edge document %d missing _key/_from/_to/label/project", i)
		}
		rows = append(rows, []any{project, key, from, fromType, label, label, to, toType, nil})
	}
	if !overwrite {
		_, err := c.pool.CopyFrom(ctx, pgx.Identifier{"fhir_edge"}, []string{
			"project", "edge_key", "src_key", "src_type", "edge_type", "path", "dst_key", "dst_type", "ordinal",
		}, pgx.CopyFromRows(rows))
		return err
	}
	return c.batchExec(ctx, `INSERT INTO fhir_edge (
		project, edge_key, src_key, src_type, edge_type, path, dst_key, dst_type, ordinal
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	ON CONFLICT (project, edge_key) DO UPDATE SET
		src_key = EXCLUDED.src_key,
		src_type = EXCLUDED.src_type,
		edge_type = EXCLUDED.edge_type,
		path = EXCLUDED.path,
		dst_key = EXCLUDED.dst_key,
		dst_type = EXCLUDED.dst_type,
		ordinal = EXCLUDED.ordinal`, rows)
}

func (c *Client) insertFieldCatalog(ctx context.Context, docs []json.RawMessage, overwrite bool) error {
	rows := make([][]any, 0, len(docs))
	for i, raw := range docs {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("decode field catalog document %d: %w", i, err)
		}
		rows = append(rows, []any{
			stringValue(doc["_key"]),
			stringValue(doc["project"]),
			stringValue(doc["resource_type"]),
			stringValue(doc["path"]),
			stringValue(doc["kind"]),
			int64Value(doc["doc_count"]),
			int64Value(doc["sample_count"]),
			stringSlice(doc["distinct_values"]),
			boolValue(doc["distinct_truncated"]),
			boolValue(doc["pivot_candidate"]),
			nullableText(stringValue(doc["pivot_kind"])),
			stringSlice(doc["pivot_columns"]),
		})
	}
	if !overwrite {
		_, err := c.pool.CopyFrom(ctx, pgx.Identifier{"fhir_field_catalog"}, []string{
			"key", "project", "resource_type", "path", "kind", "doc_count", "sample_count", "distinct_values", "distinct_truncated", "pivot_candidate", "pivot_kind", "pivot_columns",
		}, pgx.CopyFromRows(rows))
		return err
	}
	return c.batchExec(ctx, `INSERT INTO fhir_field_catalog (
		key, project, resource_type, path, kind, doc_count, sample_count, distinct_values,
		distinct_truncated, pivot_candidate, pivot_kind, pivot_columns
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	ON CONFLICT (key) DO UPDATE SET
		project = EXCLUDED.project,
		resource_type = EXCLUDED.resource_type,
		path = EXCLUDED.path,
		kind = EXCLUDED.kind,
		doc_count = EXCLUDED.doc_count,
		sample_count = EXCLUDED.sample_count,
		distinct_values = EXCLUDED.distinct_values,
		distinct_truncated = EXCLUDED.distinct_truncated,
		pivot_candidate = EXCLUDED.pivot_candidate,
		pivot_kind = EXCLUDED.pivot_kind,
		pivot_columns = EXCLUDED.pivot_columns`, rows)
}

func (c *Client) insertPatientFileRollups(ctx context.Context, docs []json.RawMessage, overwrite bool) error {
	rows := make([][]any, 0, len(docs))
	for i, raw := range docs {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("decode patient file rollup document %d: %w", i, err)
		}
		rows = append(rows, []any{
			stringValue(doc["_key"]),
			stringValue(doc["project"]),
			stringValue(doc["patient_key"]),
			nullableText(stringValue(doc["auth_resource_path"])),
			int64Value(doc["specimen_count"]),
			int64Value(doc["group_count"]),
			int64Value(doc["file_count"]),
			stringSlice(doc["specimen_types"]),
			stringSlice(doc["preservation_methods"]),
			stringSlice(doc["file_keys"]),
		})
	}
	if !overwrite {
		_, err := c.pool.CopyFrom(ctx, pgx.Identifier{"patient_file_rollup"}, []string{
			"key", "project", "patient_key", "auth_resource_path", "specimen_count", "group_count", "file_count", "specimen_types", "preservation_methods", "file_keys",
		}, pgx.CopyFromRows(rows))
		return err
	}
	return c.batchExec(ctx, `INSERT INTO patient_file_rollup (
		key, project, patient_key, auth_resource_path, specimen_count, group_count, file_count,
		specimen_types, preservation_methods, file_keys
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (key) DO UPDATE SET
		project = EXCLUDED.project,
		patient_key = EXCLUDED.patient_key,
		auth_resource_path = EXCLUDED.auth_resource_path,
		specimen_count = EXCLUDED.specimen_count,
		group_count = EXCLUDED.group_count,
		file_count = EXCLUDED.file_count,
		specimen_types = EXCLUDED.specimen_types,
		preservation_methods = EXCLUDED.preservation_methods,
		file_keys = EXCLUDED.file_keys`, rows)
}

func (c *Client) batchExec(ctx context.Context, sql string, rows [][]any) error {
	batch := &pgx.Batch{}
	for _, row := range rows {
		batch.Queue(sql, row...)
	}
	br := c.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rows {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit store.RowVisitor) error {
	args := pgx.NamedArgs{}
	for key, value := range bindVars {
		args[key] = value
	}
	rows, err := c.pool.Query(ctx, query, args)
	if err != nil {
		return err
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return err
		}
		row := make(map[string]any, len(values))
		for i, value := range values {
			row[string(fields[i].Name)] = normalizeValue(value)
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (c *Client) Close(ctx context.Context) error {
	if c != nil && c.pool != nil {
		c.pool.Close()
	}
	return nil
}

func (c *Client) ResetScalarIndex(ctx context.Context, project, resourceType string, truncate bool) error {
	if truncate && strings.TrimSpace(project) == "" && strings.TrimSpace(resourceType) == "" {
		_, err := c.pool.Exec(ctx, `TRUNCATE fhir_scalar_index`)
		return err
	}
	clauses := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if strings.TrimSpace(project) != "" {
		args = append(args, project)
		clauses = append(clauses, fmt.Sprintf("project = $%d", len(args)))
	}
	if strings.TrimSpace(resourceType) != "" {
		args = append(args, resourceType)
		clauses = append(clauses, fmt.Sprintf("resource_type = $%d", len(args)))
	}
	if len(clauses) == 0 {
		_, err := c.pool.Exec(ctx, `DELETE FROM fhir_scalar_index`)
		return err
	}
	_, err := c.pool.Exec(ctx, `DELETE FROM fhir_scalar_index WHERE `+strings.Join(clauses, ` AND `), args...)
	return err
}

func (c *Client) InsertScalarIndexRows(ctx context.Context, rows []ScalarIndexRow) error {
	return c.insertScalarRows(ctx, rows)
}

func reportBootstrap(spec store.BootstrapSpec, event string, fields map[string]any) {
	if spec.Reporter != nil {
		spec.Reporter(event, fields)
	}
}

func hasTruncate(collections []store.CollectionSpec) bool {
	for _, collection := range collections {
		if collection.Truncate {
			return true
		}
	}
	return false
}

func truncateCollections(collections []store.CollectionSpec) []string {
	physical := make(map[string]struct{})
	hasResources := false
	for _, collection := range collections {
		if !collection.Truncate {
			continue
		}
		switch collection.Name {
		case edgeCollection, fieldCatalogCollection, patientFileRollupCollection, scalarCollection:
			physical[collection.Name] = struct{}{}
		default:
			hasResources = true
		}
	}
	if hasResources {
		physical["fhir_resource"] = struct{}{}
		physical[edgeCollection] = struct{}{}
		physical[fieldCatalogCollection] = struct{}{}
		physical[scalarCollection] = struct{}{}
		physical[patientFileRollupCollection] = struct{}{}
	}
	if len(physical) == 0 {
		return nil
	}
	out := make([]string, 0, len(physical))
	for name := range physical {
		out = append(out, name)
	}
	return out
}

func (c *Client) truncateCollections(ctx context.Context, collections []string) error {
	if len(collections) == 0 {
		return nil
	}
	quoted := make([]string, 0, len(collections))
	for _, name := range collections {
		quoted = append(quoted, pgx.Identifier{name}.Sanitize())
	}
	_, err := c.pool.Exec(ctx, `TRUNCATE `+strings.Join(quoted, `, `))
	return err
}

func collectionID(resourceType, key string) string {
	return cleanKey(resourceType) + "/" + cleanKey(key)
}

func cleanKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	return keyCleaner.ReplaceAllString(value, "_")
}

func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func boolValue(value any) bool {
	v, _ := value.(bool)
	return v
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if items, ok := value.([]string); ok {
			return items
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func ScalarIndexRows(project, resourceKey, resourceType string, payload map[string]any) []ScalarIndexRow {
	rows := make([]ScalarIndexRow, 0, 32)
	ordinals := make(map[string]int)
	var walk func(path string, value any)
	walk = func(path string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			if system, code, display, ok := codingTriple(typed); ok && path != "" {
				ordinal := ordinals[path]
				ordinals[path]++
				rows = append(rows, ScalarIndexRow{
					Project:      project,
					ResourceKey:  resourceKey,
					ResourceType: resourceType,
					Path:         path,
					ValueText:    firstNonEmpty(display, code),
					System:       system,
					Code:         code,
					Display:      display,
					Ordinal:      ordinal,
				})
			}
			for key, child := range typed {
				if child == nil {
					continue
				}
				childPath := key
				if path != "" {
					childPath = path + "." + key
				}
				walk(childPath, child)
			}
		case []any:
			arrayPath := path + "[]"
			for _, item := range typed {
				if item == nil {
					continue
				}
				walk(arrayPath, item)
			}
		default:
			if path == "" {
				return
			}
			text, num, ok := scalarIndexValue(typed)
			if !ok {
				return
			}
			ordinal := ordinals[path]
			ordinals[path]++
			rows = append(rows, ScalarIndexRow{
				Project:      project,
				ResourceKey:  resourceKey,
				ResourceType: resourceType,
				Path:         path,
				ValueText:    text,
				ValueNum:     num,
				Ordinal:      ordinal,
			})
		}
	}
	walk("", payload)
	return rows
}

func numericValue(value any) any {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		if num, err := typed.Float64(); err == nil {
			return num
		}
	}
	return nil
}

func codingTriple(value map[string]any) (string, string, string, bool) {
	system := stringValue(value["system"])
	code := stringValue(value["code"])
	display := stringValue(value["display"])
	return system, code, display, system != "" || code != "" || display != ""
}

func scalarIndexValue(value any) (string, any, bool) {
	switch typed := value.(type) {
	case string:
		return typed, nil, typed != ""
	case bool:
		if typed {
			return "true", nil, true
		}
		return "false", nil, true
	case float64:
		return fmt.Sprintf("%v", typed), typed, true
	case float32:
		num := float64(typed)
		return fmt.Sprintf("%v", typed), num, true
	case int:
		num := float64(typed)
		return fmt.Sprintf("%d", typed), num, true
	case int64:
		num := float64(typed)
		return fmt.Sprintf("%d", typed), num, true
	case int32:
		num := float64(typed)
		return fmt.Sprintf("%d", typed), num, true
	case json.Number:
		if num, err := typed.Float64(); err == nil {
			return typed.String(), num, true
		}
		return typed.String(), nil, true
	default:
		return "", nil, false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeValue(value any) any {
	switch v := value.(type) {
	case []byte:
		var decoded any
		if json.Unmarshal(v, &decoded) == nil {
			return decoded
		}
		return string(v)
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return out
	default:
		return value
	}
}
