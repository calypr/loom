// Package clickhouse adapts Loom's existing atomic ClickHouse bundle store to
// the backend-neutral publication runner.
package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	store "github.com/calypr/loom/internal/store/clickhouse"
)

type Target struct {
	Store IdentityBundleStore
}

func (t *Target) Reconcile(ctx context.Context, olderThan time.Time) error {
	if store, ok := t.Store.(interface {
		Reconcile(context.Context, time.Time) error
	}); ok {
		return store.Reconcile(ctx, olderThan)
	}
	return fmt.Errorf("ClickHouse publication store does not support reconciliation")
}

func New(args ...any) (*Target, error) {
	var bundleStore IdentityBundleStore
	switch len(args) {
	case 1:
		bundleStore, _ = args[0].(IdentityBundleStore)
	case 2:
		client, clientOK := args[0].(BundleClickHouseStore)
		catalog, catalogOK := args[1].(publication.BundleCatalog)
		if clientOK && catalogOK {
			var err error
			bundleStore, err = NewBundleStore(client, catalog)
			if err != nil {
				return nil, err
			}
		}
	}
	if bundleStore == nil {
		return nil, fmt.Errorf("ClickHouse bundle store is required")
	}
	return &Target{Store: bundleStore}, nil
}

func (t *Target) Begin(ctx context.Context, identity publication.PublicationIdentity, schemas []publication.OutputSchema) (publication.Transaction, error) {
	bundleIdentity := publication.BundleIdentity{Name: identity.Name, Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest, ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion, AuthResourcePaths: append([]string(nil), identity.AuthResourcePaths...)}
	tx, err := t.Store.BeginBundleFor(ctx, bundleIdentity)
	if err != nil {
		return nil, err
	}
	result := &transaction{tx: tx, columns: make(map[string][]store.Column, len(schemas))}
	for _, schema := range schemas {
		columns, err := toColumns(schema.Columns)
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			_ = tx.Abort(cleanupCtx, fmt.Errorf("output %q schema: %w", schema.Name, err))
			cancel()
			return nil, fmt.Errorf("output %q schema: %w", schema.Name, err)
		}
		if err := tx.CreateOutput(ctx, schema.Name, columns); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			_ = tx.Abort(cleanupCtx, fmt.Errorf("output %q create: %w", schema.Name, err))
			cancel()
			return nil, fmt.Errorf("output %q create: %w", schema.Name, err)
		}
		result.columns[schema.Name] = columns
	}
	return result, nil
}

type transaction struct {
	tx      publication.AtomicBundleTx
	columns map[string][]store.Column
	closed  bool
}

func (t *transaction) WriteBatch(ctx context.Context, output string, rows []map[string]any) error {
	if t.closed {
		return fmt.Errorf("ClickHouse publication transaction is closed")
	}
	columns, ok := t.columns[output]
	if !ok {
		return fmt.Errorf("output %q was not declared", output)
	}
	return t.tx.InsertRows(ctx, output, columns, rows)
}

func (t *transaction) Validate(context.Context) error {
	if t.closed {
		return fmt.Errorf("ClickHouse publication transaction is closed")
	}
	return nil
}

func (t *transaction) Commit(ctx context.Context) ([]publication.PublishedOutput, error) {
	if t.closed {
		return nil, fmt.Errorf("ClickHouse publication transaction is closed")
	}
	if err := t.tx.Commit(ctx); err != nil {
		return nil, err
	}
	t.closed = true
	return nil, nil
}

func (t *transaction) Rollback(ctx context.Context) error {
	return t.Abort(ctx, fmt.Errorf("publication rolled back"))
}

func (t *transaction) Abort(ctx context.Context, cause error) error {
	if t.closed {
		return nil
	}
	t.closed = true
	return t.tx.Abort(ctx, cause)
}

func toColumns(columns []publication.LogicalColumn) ([]store.Column, error) {
	result := make([]store.Column, 0, len(columns))
	for _, column := range columns {
		kind := strings.ToLower(strings.TrimSpace(column.Kind))
		columnType := "String"
		switch kind {
		case "boolean":
			columnType = "Bool"
		case "integer":
			columnType = "Int64"
		case "decimal":
			columnType = "Float64"
		case "date":
			columnType = "Date"
		case "date-time":
			columnType = "DateTime64(3)"
		case "uuid":
			columnType = "UUID"
		case "code":
			columnType = "String"
		case "object":
			return nil, fmt.Errorf("object-valued column %q is not supported", column.Name)
		}
		if column.Repeated {
			columnType = "Array(" + columnType + ")"
		} else if column.Nullable {
			columnType = "Nullable(" + columnType + ")"
		}
		result = append(result, store.Column{Name: column.Name, Type: columnType})
	}
	return result, nil
}
