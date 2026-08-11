// Package publication owns the backend-neutral publication contract for
// resolved dataframe streams. Targets stage rows and decide how a publication
// becomes visible; this package owns validation, bounded batching, and the
// lifecycle around that target.
package publication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type ColumnProvenance string

const (
	ColumnExplicit   ColumnProvenance = "EXPLICIT"
	ColumnDiscovered ColumnProvenance = "DISCOVERED"
)

// LogicalColumn is the backend-independent schema emitted by the compiler.
// Kind is one of string, code, uuid, date, date-time, integer, decimal,
// boolean, or object. Object values are rejected by the generic MVP runner
// unless a target explicitly opts into a serialization policy.
type LogicalColumn struct {
	Name string
	// SemanticPath is the stable FHIR/provenance identity. It is persisted
	// alongside the physical schema but never used to name ClickHouse columns.
	SemanticPath string
	Kind         string
	Repeated     bool
	Nullable     bool
	IsIdentity   bool
	Provenance   ColumnProvenance
	LoomOwned    bool
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
	ScopeProject       string
	ProjectRevisionID  string
	SelectedOutputs    []string
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

// SchemaFinalizer is implemented by publication targets that can remove
// unpopulated discovered columns from their private staging tables. The
// runner uses the optional interface so older non-ClickHouse test targets
// remain source-compatible while production publication requires it when a
// column must be dropped.
type SchemaFinalizer interface {
	FinalizeSchema(context.Context, []OutputSchema) error
}

// FinalSchemaDigest computes the versioned digest for the schema actually
// staged. Physical names and discovery provenance are deliberately excluded.
func FinalSchemaDigest(identity PublicationIdentity, schemas []OutputSchema) string {
	type contract struct {
		Name         string `json:"name"`
		Kind         string `json:"kind"`
		SemanticPath string `json:"semanticPath,omitempty"`
		Repeated     bool   `json:"repeated,omitempty"`
		Nullable     bool   `json:"nullable,omitempty"`
		Identity     bool   `json:"identity,omitempty"`
	}
	type output struct {
		Name    string     `json:"name"`
		Columns []contract `json:"columns"`
	}
	ordered := make([]output, 0, len(schemas))
	for _, schema := range schemas {
		item := output{Name: schema.Name, Columns: make([]contract, 0, len(schema.Columns))}
		for _, column := range schema.Columns {
			item.Columns = append(item.Columns, contract{Name: column.Name, Kind: column.Kind, SemanticPath: column.SemanticPath, Repeated: column.Repeated, Nullable: column.Nullable, Identity: column.IsIdentity})
		}
		ordered = append(ordered, item)
	}
	payload := struct {
		Version           int      `json:"version"`
		RecipeDigest      string   `json:"recipeDigest"`
		ScopeDigest       string   `json:"scopeDigest"`
		DatasetGeneration string   `json:"datasetGeneration"`
		Outputs           []output `json:"outputs"`
	}{2, identity.RecipeDigest, identity.ScopeDigest, identity.DatasetGeneration, ordered}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
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
