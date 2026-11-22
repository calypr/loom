package publication

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type ColumnProvenance string

const (
	ColumnExplicit   ColumnProvenance = "EXPLICIT"
	ColumnDiscovered ColumnProvenance = "DISCOVERED"
)

type LogicalColumn struct {
	ID   string
	Name string

	SemanticPath string
	Kind         string
	Repeated     bool
	Nullable     bool
	IsIdentity   bool
	Provenance   ColumnProvenance
	LoomOwned    bool
}

type OutputSchema struct {
	Name      string
	Columns   []LogicalColumn
	SourceRow *SourceRowMetadata
}

func FinalSchemaDigest(identity PublicationIdentity, schemas []OutputSchema) string {
	type contract struct {
		ID           string `json:"id,omitempty"`
		Name         string `json:"name"`
		Kind         string `json:"kind"`
		SemanticPath string `json:"semanticPath,omitempty"`
		Repeated     bool   `json:"repeated,omitempty"`
		Nullable     bool   `json:"nullable,omitempty"`
		Identity     bool   `json:"identity,omitempty"`
	}
	type output struct {
		Name      string             `json:"name"`
		Columns   []contract         `json:"columns"`
		SourceRow *SourceRowMetadata `json:"sourceRow,omitempty"`
	}
	ordered := make([]output, 0, len(schemas))
	for _, schema := range schemas {
		item := output{Name: schema.Name, Columns: make([]contract, 0, len(schema.Columns))}
		for _, column := range schema.Columns {
			item.Columns = append(item.Columns, contract{ID: column.ID, Name: column.Name, Kind: column.Kind, SemanticPath: column.SemanticPath, Repeated: column.Repeated, Nullable: column.Nullable, Identity: column.IsIdentity})
		}
		item.SourceRow = schema.SourceRow
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
