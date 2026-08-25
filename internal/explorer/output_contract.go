package explorer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

// PublicOutputContract is the current, transport-neutral public output
// contract persisted inside a compilation receipt. It describes the one
// output selected by the V2 Builder; executable selectors and physical names
// are deliberately absent.
type PublicOutputContract struct {
	OutputID string               `json:"outputId"`
	Columns  []PublicOutputColumn `json:"columns"`
}

type PublicOutputColumn struct {
	EmissionID   string `json:"emissionId"`
	PublicColumn string `json:"publicColumn"`
	CandidateID  string `json:"candidateId,omitempty"`
	OccurrenceID string `json:"occurrenceId,omitempty"`
	LogicalType  string `json:"logicalType"`
	Filterable   bool   `json:"filterable"`
	Chartable    bool   `json:"chartable"`
}

// DecodePublicOutputContract strictly decodes the current contract shape.
// Null columns are accepted because an empty V2 selection is a valid mutable
// editor state and the current compiler serializes its nil slice as null.
// Missing columns, unknown fields, duplicate keys, and trailing JSON are not
// accepted.
func DecodePublicOutputContract(raw json.RawMessage) (PublicOutputContract, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return PublicOutputContract{}, invalidOutputContract("contract is required")
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return PublicOutputContract{}, invalidOutputContract("duplicate or malformed JSON key: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		if err == nil {
			err = errors.New("contract must be a JSON object")
		}
		return PublicOutputContract{}, invalidOutputContract("malformed JSON object: %v", err)
	}
	if _, ok := fields["outputId"]; !ok {
		return PublicOutputContract{}, invalidOutputContract("outputId is required")
	}
	if _, ok := fields["columns"]; !ok {
		return PublicOutputContract{}, invalidOutputContract("columns is required")
	}
	var contract PublicOutputContract
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		return PublicOutputContract{}, invalidOutputContract("decode failed: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return PublicOutputContract{}, invalidOutputContract("trailing JSON: %v", err)
	}
	if strings.TrimSpace(contract.OutputID) == "" {
		return PublicOutputContract{}, invalidOutputContract("outputId is required")
	}
	for i, column := range contract.Columns {
		if strings.TrimSpace(column.EmissionID) == "" {
			return PublicOutputContract{}, invalidOutputContract("columns[%d].emissionId is required", i)
		}
		if strings.TrimSpace(column.PublicColumn) == "" {
			return PublicOutputContract{}, invalidOutputContract("columns[%d].publicColumn is required", i)
		}
		if strings.TrimSpace(column.LogicalType) == "" {
			return PublicOutputContract{}, invalidOutputContract("columns[%d].logicalType is required", i)
		}
	}
	return contract, nil
}

// ValidateAgainst proves that the typed public contract is exactly the
// compiler-owned emitted-column sequence for the receipt's bundle. It checks
// output identity, order, IDs, and every public metadata bit. A mismatch is a
// receipt integrity failure and therefore requires explicit recompilation.
func (c PublicOutputContract) ValidateAgainst(bundle recipe.Bundle, emitted []EmittedColumn) error {
	if strings.TrimSpace(c.OutputID) == "" {
		return invalidOutputContract("outputId is required")
	}
	outputFound := false
	for _, output := range bundle.Outputs {
		if output.Name == c.OutputID {
			outputFound = true
			break
		}
	}
	if !outputFound {
		return invalidOutputContract("outputId %q is absent from the compiled recipe", c.OutputID)
	}
	if len(c.Columns) != len(emitted) {
		return invalidOutputContract("column count %d does not match emitted column count %d", len(c.Columns), len(emitted))
	}
	seenEmission := make(map[string]struct{}, len(emitted))
	seenPublic := make(map[string]struct{}, len(emitted))
	for i, column := range emitted {
		if strings.TrimSpace(column.OutputID) != c.OutputID {
			return invalidOutputContract("emittedColumns[%d] belongs to output %q, want %q", i, column.OutputID, c.OutputID)
		}
		if strings.TrimSpace(column.EmissionID) == "" || strings.TrimSpace(column.PublicColumn) == "" {
			return invalidOutputContract("emittedColumns[%d] has an empty identity", i)
		}
		if _, exists := seenEmission[column.EmissionID]; exists {
			return invalidOutputContract("duplicate emissionId %q", column.EmissionID)
		}
		if _, exists := seenPublic[column.PublicColumn]; exists {
			return invalidOutputContract("duplicate publicColumn %q", column.PublicColumn)
		}
		seenEmission[column.EmissionID] = struct{}{}
		seenPublic[column.PublicColumn] = struct{}{}
		actual := c.Columns[i]
		if actual.EmissionID != column.EmissionID || actual.PublicColumn != column.PublicColumn || actual.CandidateID != column.CandidateID || actual.OccurrenceID != column.OccurrenceID || actual.LogicalType != column.LogicalType || actual.Filterable != column.Filterable || actual.Chartable != column.Chartable {
			return invalidOutputContract("columns[%d] does not match emittedColumns[%d]", i, i)
		}
	}
	return nil
}

func invalidOutputContract(format string, args ...any) error {
	return fmt.Errorf("%w: public output contract %s", ErrReceiptRecompileRequired, fmt.Sprintf(format, args...))
}
