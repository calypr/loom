// Package publication owns the backend-neutral publication contract for
// resolved dataframe streams. Targets stage rows and decide how a publication
// becomes visible; this package owns validation, bounded batching, and the
// lifecycle around that target.
package publication

import "context"

// LogicalColumn is the backend-independent schema emitted by the compiler.
// Kind is one of string, code, uuid, date, date-time, integer, decimal,
// boolean, or object. Object values are rejected by the generic MVP runner
// unless a target explicitly opts into a serialization policy.
type LogicalColumn struct {
	Name       string
	Kind       string
	Repeated   bool
	Nullable   bool
	IsIdentity bool
}

type OutputSchema struct {
	Name    string
	Columns []LogicalColumn
}

// OutputStream is consumed exactly once. The callback must invoke visit for
// each row and must stop when visit returns an error.
type OutputStream struct {
	Name    string
	Columns []LogicalColumn
	Stream  func(context.Context, func(map[string]any) error) error
}

type PublicationIdentity struct {
	Name               string
	TranslationVersion string
	Project            string
	DatasetGeneration  string
	RecipeDigest       string
	SchemaDigest       string
	ScopeDigest        string
	EngineVersion      string
	TargetConfigDigest string
	AuthScopeMode      string
	AuthResourcePaths  []string
}

type PublishedOutput struct {
	Name         string
	PhysicalName string
	RowCount     int64
	ByteCount    int64
}

type Target interface {
	Begin(context.Context, PublicationIdentity, []OutputSchema) (Transaction, error)
}

type Transaction interface {
	WriteBatch(context.Context, string, []map[string]any) error
	Commit(context.Context) ([]PublishedOutput, error)
	Rollback(context.Context) error
}

type Limits struct {
	BatchRows  int
	BatchBytes int
}

func (l Limits) normalized() Limits {
	if l.BatchRows <= 0 {
		l.BatchRows = 1000
	}
	if l.BatchBytes <= 0 {
		l.BatchBytes = 4 << 20
	}
	return l
}

type Result struct {
	Outputs []PublishedOutput
}
