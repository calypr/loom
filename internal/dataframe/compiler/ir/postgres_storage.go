package ir

// Trusted PostgreSQL storage bindings for the compiler (pg-ir-02).
//
// This file describes the physical tables and columns of the PostgreSQL 18
// source store separately from runtime values. A PostgresStorageBinding is
// the renderer's only source of SQL identifiers: table and column names reach
// a rendered statement exclusively through the types below, every one of
// which is constructed by an allowlisted lookup and is therefore trusted.
// Scope values (project, dataset generation, auth resource paths), filter
// values, payload paths, and output projection names are runtime data; they
// never enter a storage binding and the renderer must pass them to the
// driver as ordered parameters instead.
//
// The allowlist mirrors exactly the tables created by the pg-store embedded
// migrations (internal/backends/postgres/internal/store/migrations):
//
//	000002_fhir_resource.sql     -> PostgresResourceLogicalTable
//	000004_edge_occurrence.sql   -> PostgresEdgeOccurrenceLogicalTable
//
// If a store migration adds or renames a column, this allowlist must be
// updated deliberately in the same wave; an identifier that is not listed
// here cannot become SQL text, and the renderer fails with
// ErrUnregisteredPostgresIdentifier rather than falling back.

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
)

// ErrUnregisteredPostgresIdentifier reports that a table or column name is
// not part of the compiler-owned allowlist for the logical resource or edge
// tables. Callers must surface it; there is no identifier fallback.
var ErrUnregisteredPostgresIdentifier = errors.New("unregistered postgres identifier")

// postgresSchemaNamePattern is the same schema-name contract the pg-store
// repositories enforce (internal/backends/postgres/internal/store schemaNamePattern): a
// lowercase owner-chosen schema name. Validation here means a schema name
// that survived construction can be emitted quoted without quoting rules
// having to defend against embedded quotes.
var postgresSchemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// PostgresLogicalTableKind names one logical table of the PostgreSQL source
// store. The set is closed: exactly the FHIR resource table and the FHIR
// edge-occurrence table. Control-plane tables (ledger, manifests, leases) are
// owned by their store packages and are not compiler storage surfaces.
type PostgresLogicalTableKind string

const (
	// PostgresResourceLogicalTable is the scoped FHIR resource table
	// (fhir_resource, migration 000002_fhir_resource.sql): one row per
	// (project, dataset_generation, resource_type, logical_id) with the
	// original resource object stored as JSONB payload.
	PostgresResourceLogicalTable PostgresLogicalTableKind = "resource"
	// PostgresEdgeOccurrenceLogicalTable is the scoped typed edge
	// occurrence table (fhir_edge_occurrence, migration
	// 000004_edge_occurrence.sql): endpoints are stored as resource type +
	// logical id identities inside the same scope.
	PostgresEdgeOccurrenceLogicalTable PostgresLogicalTableKind = "edge_occurrence"
)

// postgresLogicalTableSpec is the closed registry entry for one logical
// table: its physical table name and its allowlisted column set, taken
// literally from the owning store migration.
type postgresLogicalTableSpec struct {
	physicalTable string
	columns       map[string]struct{}
}

var postgresLogicalTables = map[PostgresLogicalTableKind]postgresLogicalTableSpec{
	PostgresResourceLogicalTable: {
		physicalTable: "fhir_resource",
		columns: columnSet(
			"resource_seq", "project", "dataset_generation", "resource_type",
			"logical_id", "auth_resource_path", "payload", "recorded_at",
		),
	},
	PostgresEdgeOccurrenceLogicalTable: {
		physicalTable: "fhir_edge_occurrence",
		columns: columnSet(
			"edge_seq", "project", "dataset_generation", "auth_resource_path",
			"label", "source_resource_type", "source_logical_id",
			"target_resource_type", "target_logical_id", "source_path",
			"source_ordinal", "logical_edge_key", "recorded_at",
		),
	},
}

func columnSet(names ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

// PostgresTableIdentifier is a validated schema-qualified table identifier.
// Its fields are unexported so the only way to obtain one is
// NewPostgresTableIdentifier, which resolves the physical table name from
// the closed registry above; an arbitrary string can never become a table
// identifier.
type PostgresTableIdentifier struct {
	schema string
	table  string
}

// Schema reports the validated schema name.
func (i PostgresTableIdentifier) Schema() string { return i.schema }

// Table reports the registry-provided physical table name.
func (i PostgresTableIdentifier) Table() string { return i.table }

// SQLText returns the identifier as a double-quoted qualified name. Only
// registry-validated lowercase names can reach this method, so the quoted
// form is safe to splice into statement text.
func (i PostgresTableIdentifier) SQLText() string {
	return quotePostgresIdentifier(i.schema) + "." + quotePostgresIdentifier(i.table)
}

// IsRegistered reports whether i names a registry table in a validated
// schema. The zero value is not registered.
func (i PostgresTableIdentifier) IsRegistered() bool {
	if i.schema == "" || i.table == "" {
		return false
	}
	if !postgresSchemaNamePattern.MatchString(i.schema) {
		return false
	}
	for _, spec := range postgresLogicalTables {
		if spec.physicalTable == i.table {
			return true
		}
	}
	return false
}

// NewPostgresTableIdentifier resolves one logical table kind into a trusted
// schema-qualified identifier. An unknown kind, or a schema name outside the
// owner-chosen lowercase contract, fails with
// ErrUnregisteredPostgresIdentifier-wrapped errors.
func NewPostgresTableIdentifier(schema string, kind PostgresLogicalTableKind) (PostgresTableIdentifier, error) {
	spec, ok := postgresLogicalTables[kind]
	if !ok {
		return PostgresTableIdentifier{}, fmt.Errorf("%w: logical table kind %q is not registered",
			ErrUnregisteredPostgresIdentifier, string(kind))
	}
	if !postgresSchemaNamePattern.MatchString(schema) {
		return PostgresTableIdentifier{}, fmt.Errorf("%w: schema name %q must match [a-z_][a-z0-9_]{0,62}",
			ErrUnregisteredPostgresIdentifier, schema)
	}
	return PostgresTableIdentifier{schema: schema, table: spec.physicalTable}, nil
}

// PostgresColumnIdentifier is a validated column of one logical table. The
// only constructor is PostgresLogicalTable.Column, so an unallowlisted name
// can never become SQL text.
type PostgresColumnIdentifier struct {
	tableKind PostgresLogicalTableKind
	column    string
}

// TableKind reports the logical table this column belongs to.
func (c PostgresColumnIdentifier) TableKind() PostgresLogicalTableKind { return c.tableKind }

// Name reports the allowlisted column name.
func (c PostgresColumnIdentifier) Name() string { return c.column }

// SQLText returns the column as a double-quoted identifier.
func (c PostgresColumnIdentifier) SQLText() string { return quotePostgresIdentifier(c.column) }

// PostgresLogicalTable binds one logical table kind to a validated physical
// table identifier. It is the lookup surface for that table's column
// allowlist.
type PostgresLogicalTable struct {
	kind  PostgresLogicalTableKind
	table PostgresTableIdentifier
}

// Kind reports which logical table this describes.
func (t PostgresLogicalTable) Kind() PostgresLogicalTableKind { return t.kind }

// Identifier returns the trusted table identifier.
func (t PostgresLogicalTable) Identifier() PostgresTableIdentifier { return t.table }

// Column resolves one column name against this logical table's allowlist.
// Unknown columns fail with ErrUnregisteredPostgresIdentifier; a zero
// PostgresLogicalTable rejects every name.
func (t PostgresLogicalTable) Column(name string) (PostgresColumnIdentifier, error) {
	spec, ok := postgresLogicalTables[t.kind]
	if !ok {
		return PostgresColumnIdentifier{}, fmt.Errorf("%w: logical table %q has no registry entry",
			ErrUnregisteredPostgresIdentifier, string(t.kind))
	}
	if _, ok := spec.columns[name]; !ok {
		return PostgresColumnIdentifier{}, fmt.Errorf("%w: column %q is not a column of logical table %q",
			ErrUnregisteredPostgresIdentifier, name, string(t.kind))
	}
	return PostgresColumnIdentifier{tableKind: t.kind, column: name}, nil
}

// MustColumn resolves a compiler-owned column name that is static in this
// repository. It panics on an allowlist miss, which is a programming bug in
// the compiler, not user input: every call site names a literal column that
// the migration defines.
func (t PostgresLogicalTable) MustColumn(name string) PostgresColumnIdentifier {
	column, err := t.Column(name)
	if err != nil {
		panic(err)
	}
	return column
}

// PostgresStorageBinding is the renderer's complete trusted storage surface:
// the physical placement of the logical resource and edge tables inside one
// validated schema. It carries identifiers only. It never carries scope
// values, filter values, payload paths, or projection names, and its zero
// value is not a valid renderer input.
type PostgresStorageBinding struct {
	// Resource is the fhir_resource placement.
	Resource PostgresLogicalTable
	// Edge is the fhir_edge_occurrence placement.
	Edge PostgresLogicalTable
}

// NewPostgresStorageBinding binds both logical tables to one owner-chosen
// schema. Both identifiers are resolved through the closed registry, so the
// returned binding is validated by construction.
func NewPostgresStorageBinding(schema string) (PostgresStorageBinding, error) {
	resource, err := NewPostgresTableIdentifier(schema, PostgresResourceLogicalTable)
	if err != nil {
		return PostgresStorageBinding{}, fmt.Errorf("resource table: %w", err)
	}
	edge, err := NewPostgresTableIdentifier(schema, PostgresEdgeOccurrenceLogicalTable)
	if err != nil {
		return PostgresStorageBinding{}, fmt.Errorf("edge table: %w", err)
	}
	return PostgresStorageBinding{
		Resource: PostgresLogicalTable{kind: PostgresResourceLogicalTable, table: resource},
		Edge:     PostgresLogicalTable{kind: PostgresEdgeOccurrenceLogicalTable, table: edge},
	}, nil
}

// Validate reports whether the binding is a fully constructed, registered
// binding. The zero value and any hand-assembled partial fail.
func (b PostgresStorageBinding) Validate() error {
	if b.Resource.kind != PostgresResourceLogicalTable || !b.Resource.table.IsRegistered() {
		return fmt.Errorf("%w: resource logical table is not registered", ErrUnregisteredPostgresIdentifier)
	}
	if b.Edge.kind != PostgresEdgeOccurrenceLogicalTable || !b.Edge.table.IsRegistered() {
		return fmt.Errorf("%w: edge logical table is not registered", ErrUnregisteredPostgresIdentifier)
	}
	if b.Resource.table.schema != b.Edge.table.schema {
		return fmt.Errorf("%w: resource table schema %q and edge table schema %q must match",
			ErrUnregisteredPostgresIdentifier, b.Resource.table.schema, b.Edge.table.schema)
	}
	return nil
}

// RegisteredPostgresColumnNames returns the allowlisted column names of one
// logical table in sorted order. It is a read-only view for validation and
// diagnostics, not a construction surface.
func RegisteredPostgresColumnNames(kind PostgresLogicalTableKind) ([]string, error) {
	spec, ok := postgresLogicalTables[kind]
	if !ok {
		return nil, fmt.Errorf("%w: logical table kind %q is not registered",
			ErrUnregisteredPostgresIdentifier, string(kind))
	}
	names := make([]string, 0, len(spec.columns))
	for name := range spec.columns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// quotePostgresIdentifier doubles embedded quotes. Callers only pass names
// that already passed the registry or schema pattern, so no embedded quote
// can exist; the doubling is defense in depth, not the trust mechanism.
func quotePostgresIdentifier(name string) string {
	out := make([]byte, 0, len(name)+2)
	out = append(out, '"')
	for index := 0; index < len(name); index++ {
		if name[index] == '"' {
			out = append(out, '"')
		}
		out = append(out, name[index])
	}
	out = append(out, '"')
	return string(out)
}
