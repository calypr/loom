// Package elasticsearch is the direct Elasticsearch/OpenSearch publication
// target for the backend-neutral dataframe publication runner.
package elasticsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	es "github.com/calypr/loom/internal/store/elasticsearch"
	"github.com/google/uuid"
)

type Options struct {
	Client         *es.Client
	TargetName     string
	IndexPrefix    string
	AliasTemplate  string
	BatchRows      int
	BatchBytes     int
	RequestTimeout time.Duration
	MaxRetries     int
	Shards         int
	Replicas       int
	Profile        string
}

type Target struct {
	client        *es.Client
	targetName    string
	indexPrefix   string
	aliasTemplate string
	shards        int
	replicas      int
	maxRetries    int
}

func New(options Options) (*Target, error) {
	if options.Client == nil {
		return nil, fmt.Errorf("elasticsearch client is required")
	}
	prefix := strings.Trim(strings.TrimSpace(options.IndexPrefix), "_")
	if prefix == "" {
		prefix = "loom"
	}
	targetName := strings.TrimSpace(options.TargetName)
	if targetName == "" {
		targetName = "elasticsearch"
	}
	if options.Replicas < 0 {
		options.Replicas = 0
	}
	profile := strings.ToLower(strings.TrimSpace(options.Profile))
	if profile != "" && profile != "generic" && profile != "gen3-guppy" {
		return nil, fmt.Errorf("unsupported Elasticsearch publication profile %q", options.Profile)
	}
	return &Target{client: options.Client, targetName: targetName, indexPrefix: prefix, aliasTemplate: options.AliasTemplate, shards: options.Shards, replicas: options.Replicas, maxRetries: options.MaxRetries}, nil
}

func (t *Target) Begin(ctx context.Context, identity publication.PublicationIdentity, schemas []publication.OutputSchema) (publication.Transaction, error) {
	if strings.TrimSpace(identity.Project) == "" || strings.TrimSpace(identity.Name) == "" {
		return nil, fmt.Errorf("publication project and name are required")
	}
	executionID := uuid.NewString()
	tx := &transaction{target: t, identity: identity, executionID: executionID, outputs: make(map[string]outputState, len(schemas))}
	for _, schema := range schemas {
		index := t.indexName(schema.Name, identity.Project, executionID)
		alias := t.aliasName(schema.Name, identity.Project)
		properties, err := mapping(schema.Columns)
		if err != nil {
			return nil, fmt.Errorf("output %q mapping: %w", schema.Name, err)
		}
		if err := t.client.CreateIndex(ctx, index, properties, t.shards, t.replicas); err != nil {
			tx.cleanup(context.Background())
			return nil, fmt.Errorf("create staged index %q: %w", index, err)
		}
		tx.outputs[schema.Name] = outputState{schema: schema, index: index, alias: alias}
	}
	return tx, nil
}

type outputState struct {
	schema publication.OutputSchema
	index  string
	alias  string
	rows   int64
}

type transaction struct {
	target      *Target
	identity    publication.PublicationIdentity
	executionID string
	outputs     map[string]outputState
	closed      bool
}

func (t *transaction) WriteBatch(ctx context.Context, output string, rows []map[string]any) error {
	if t.closed {
		return fmt.Errorf("elasticsearch publication transaction is closed")
	}
	state, ok := t.outputs[output]
	if !ok {
		return fmt.Errorf("output %q was not declared", output)
	}
	documents := make([]es.BulkDocument, 0, len(rows))
	for _, row := range rows {
		id, ok := row["__loom_row_id"]
		if !ok || strings.TrimSpace(fmt.Sprint(id)) == "" {
			return fmt.Errorf("output %q row is missing __loom_row_id", output)
		}
		source := cloneRow(row)
		source["project_id"] = t.identity.Project
		if len(t.identity.AuthResourcePaths) == 1 {
			source["auth_resource_path"] = t.identity.AuthResourcePaths[0]
		} else if len(t.identity.AuthResourcePaths) > 1 {
			source["auth_resource_paths"] = append([]string(nil), t.identity.AuthResourcePaths...)
		}
		documents = append(documents, es.BulkDocument{ID: fmt.Sprint(id), Source: source})
	}
	remaining := documents
	for attempt := 0; len(remaining) > 0; attempt++ {
		result, err := t.target.client.Bulk(ctx, state.index, remaining)
		if err != nil {
			return err
		}
		if len(result.Items) != len(remaining) {
			return fmt.Errorf("bulk response item count %d does not match request count %d", len(result.Items), len(remaining))
		}
		failed := make([]es.BulkDocument, 0)
		for i, item := range result.Items {
			if item.Status >= 200 && item.Status < 300 && len(item.Error) == 0 {
				continue
			}
			if i >= len(remaining) {
				return fmt.Errorf("bulk response item count exceeds request")
			}
			if !retryableItem(item.Status) || attempt >= t.target.maxRetries {
				return fmt.Errorf("bulk item %q failed with status %d", item.ID, item.Status)
			}
			failed = append(failed, remaining[i])
		}
		if len(failed) == 0 {
			state.rows += int64(len(remaining))
			t.outputs[output] = state
			return nil
		}
		remaining = failed
	}
	return nil
}

func (t *transaction) Validate(ctx context.Context) error {
	if t.closed {
		return fmt.Errorf("elasticsearch publication transaction is closed")
	}
	for name, state := range t.outputs {
		if err := t.target.client.Refresh(ctx, state.index); err != nil {
			return fmt.Errorf("refresh output %q: %w", name, err)
		}
		count, err := t.target.client.Count(ctx, state.index)
		if err != nil {
			return fmt.Errorf("count output %q: %w", name, err)
		}
		if count != state.rows {
			return fmt.Errorf("output %q count %d does not match streamed rows %d", name, count, state.rows)
		}
	}
	return nil
}

func (t *transaction) Commit(ctx context.Context) ([]publication.PublishedOutput, error) {
	if t.closed {
		return nil, fmt.Errorf("elasticsearch publication transaction is closed")
	}
	actions := make([]es.AliasAction, 0, len(t.outputs)*2)
	for _, state := range t.outputs {
		previous, err := t.target.client.AliasIndices(ctx, state.alias)
		if err != nil {
			return nil, err
		}
		for _, index := range previous {
			actions = append(actions, es.AliasAction{Action: "remove", Index: index, Alias: state.alias})
		}
		actions = append(actions, es.AliasAction{Action: "add", Index: state.index, Alias: state.alias})
	}
	if err := t.target.client.SwapAliases(ctx, actions); err != nil {
		return nil, err
	}
	t.closed = true
	published := make([]publication.PublishedOutput, 0, len(t.outputs))
	for name, state := range t.outputs {
		published = append(published, publication.PublishedOutput{Name: name, PhysicalName: state.index, RowCount: state.rows})
	}
	return published, nil
}

func (t *transaction) Rollback(ctx context.Context) error {
	if t.closed {
		return nil
	}
	t.closed = true
	return t.cleanup(ctx)
}

func (t *transaction) cleanup(ctx context.Context) error {
	var first error
	for _, state := range t.outputs {
		if err := t.target.client.DeleteIndex(ctx, state.index); err != nil {
			if first == nil {
				first = err
			}
		}
	}
	return first
}

func mapping(columns []publication.LogicalColumn) (map[string]map[string]any, error) {
	properties := make(map[string]map[string]any, len(columns)+2)
	for _, column := range columns {
		fieldType, err := elasticType(column.Kind)
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", column.Name, err)
		}
		properties[column.Name] = map[string]any{"type": fieldType}
	}
	properties["project_id"] = map[string]any{"type": "keyword"}
	properties["auth_resource_path"] = map[string]any{"type": "keyword"}
	properties["auth_resource_paths"] = map[string]any{"type": "keyword"}
	return properties, nil
}

func elasticType(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "string", "code", "uuid", "date", "date-time", "datetime":
		return "keyword", nil
	case "integer":
		return "long", nil
	case "decimal":
		return "double", nil
	case "boolean":
		return "boolean", nil
	case "object":
		return "", fmt.Errorf("object-valued columns are not supported by the flat profile")
	default:
		return "", fmt.Errorf("unsupported logical kind %q", kind)
	}
}

func (t *Target) indexName(output, project, execution string) string {
	return fmt.Sprintf("%s_%s_%s_%s", safeName(t.indexPrefix), safeName(output), shortHash(project), safeName(execution))
}

func (t *Target) aliasName(output, project string) string {
	if strings.TrimSpace(t.aliasTemplate) != "" {
		return strings.ReplaceAll(strings.ReplaceAll(t.aliasTemplate, "{output}", safeName(output)), "{project_hash}", shortHash(project))
	}
	return fmt.Sprintf("%s_%s_%s", safeName(t.indexPrefix), safeName(output), shortHash(project))
}

func safeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func retryableItem(status int) bool {
	return status == 429 || status == 502 || status == 503 || status == 504
}

func cloneRow(row map[string]any) map[string]any {
	copy := make(map[string]any, len(row))
	for key, value := range row {
		copy[key] = value
	}
	return copy
}
