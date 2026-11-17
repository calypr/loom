package ir

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
)

var ErrUnregisteredPostgresIdentifier = errors.New("unregistered postgres identifier")

var postgresSchemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

type PostgresLogicalTableKind string

const (
	PostgresResourceLogicalTable       PostgresLogicalTableKind = "resource"
	PostgresEdgeOccurrenceLogicalTable PostgresLogicalTableKind = "edge_occurrence"
)

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

type PostgresTableIdentifier struct {
	schema string
	table  string
}

func (i PostgresTableIdentifier) Schema() string { return i.schema }

func (i PostgresTableIdentifier) Table() string { return i.table }

func (i PostgresTableIdentifier) SQLText() string {
	return quotePostgresIdentifier(i.schema) + "." + quotePostgresIdentifier(i.table)
}

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

type PostgresColumnIdentifier struct {
	tableKind PostgresLogicalTableKind
	column    string
}

func (c PostgresColumnIdentifier) TableKind() PostgresLogicalTableKind { return c.tableKind }

func (c PostgresColumnIdentifier) Name() string { return c.column }

func (c PostgresColumnIdentifier) SQLText() string { return quotePostgresIdentifier(c.column) }

type PostgresLogicalTable struct {
	kind  PostgresLogicalTableKind
	table PostgresTableIdentifier
}

func (t PostgresLogicalTable) Kind() PostgresLogicalTableKind { return t.kind }

func (t PostgresLogicalTable) Identifier() PostgresTableIdentifier { return t.table }

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

func (t PostgresLogicalTable) MustColumn(name string) PostgresColumnIdentifier {
	column, err := t.Column(name)
	if err != nil {
		panic(err)
	}
	return column
}

type PostgresStorageBinding struct {
	Resource PostgresLogicalTable
	Edge     PostgresLogicalTable
}

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
