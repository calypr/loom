package explorer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
)

// PublicOutputContracts is the current, transport-neutral public output
// contract persisted inside a compilation receipt. Outputs retain workspace
// order; executable selectors and physical names are deliberately absent.
type PublicOutputContracts struct {
	Outputs []PublicOutputContract `json:"outputs"`
}

// PublicOutputContract describes one compiled workspace output.
type PublicOutputContract struct {
	OutputID          string               `json:"outputId"`
	RootResourceType  string               `json:"rootResourceType,omitempty"`
	RowGrain          string               `json:"rowGrain,omitempty"`
	RowMultiplication string               `json:"rowMultiplication,omitempty"`
	Lossless          bool                 `json:"lossless"`
	MLReady           bool                 `json:"mlReady"`
	Columns           []PublicOutputColumn `json:"columns"`
}

type PublicOutputColumn struct {
	Column             string                          `json:"column"`
	AuthoredColumns    []string                        `json:"authoredColumns,omitempty"`
	Label              string                          `json:"label"`
	LogicalType        string                          `json:"logicalType"`
	Nullable           bool                            `json:"nullable"`
	Shape              string                          `json:"shape,omitempty"`
	SourceResourceType string                          `json:"sourceResourceType,omitempty"`
	SourcePath         string                          `json:"sourcePath,omitempty"`
	ChoiceArm          string                          `json:"choiceArm,omitempty"`
	Coordinates        []capability.RepeatedCoordinate `json:"coordinates,omitempty"`
	Lossless           bool                            `json:"lossless"`
	MLReady            bool                            `json:"mlReady"`
	Filterable         bool                            `json:"filterable"`
	Chartable          bool                            `json:"chartable"`

	// Compiler identities remain available to internal legacy tests only. They
	// are deliberately absent from the V2 public contract.
	EmissionID     string `json:"-"`
	PublicColumn   string `json:"-"`
	CandidateID    string `json:"-"`
	OccurrenceID   string `json:"-"`
	ProjectionMode string `json:"-"`
}

// DecodePublicOutputContracts strictly decodes the current contract shape.
// Null columns are accepted because an empty V2 selection is a valid mutable
// editor state and the current compiler serializes its nil slice as null.
// Missing fields, unknown fields, duplicate keys, and trailing JSON are not accepted.
func DecodePublicOutputContracts(raw json.RawMessage) (PublicOutputContracts, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return PublicOutputContracts{}, invalidOutputContract("contract is required")
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return PublicOutputContracts{}, invalidOutputContract("duplicate or malformed JSON key: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		if err == nil {
			err = errors.New("contract must be a JSON object")
		}
		return PublicOutputContracts{}, invalidOutputContract("malformed JSON object: %v", err)
	}
	if _, ok := fields["outputs"]; !ok {
		return PublicOutputContracts{}, invalidOutputContract("outputs is required")
	}
	if bytes.Equal(bytes.TrimSpace(fields["outputs"]), []byte("null")) {
		return PublicOutputContracts{}, invalidOutputContract("outputs must be an array")
	}
	var outputFields []map[string]json.RawMessage
	if err := json.Unmarshal(fields["outputs"], &outputFields); err != nil {
		return PublicOutputContracts{}, invalidOutputContract("outputs must be an array: %v", err)
	}
	for i, output := range outputFields {
		if _, ok := output["outputId"]; !ok {
			return PublicOutputContracts{}, invalidOutputContract("outputs[%d].outputId is required", i)
		}
		if _, ok := output["columns"]; !ok {
			return PublicOutputContracts{}, invalidOutputContract("outputs[%d].columns is required", i)
		}
	}
	var contracts PublicOutputContracts
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contracts); err != nil {
		return PublicOutputContracts{}, invalidOutputContract("decode failed: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return PublicOutputContracts{}, invalidOutputContract("trailing JSON: %v", err)
	}
	seen := make(map[string]struct{}, len(contracts.Outputs))
	for outputIndex, contract := range contracts.Outputs {
		if strings.TrimSpace(contract.OutputID) == "" {
			return PublicOutputContracts{}, invalidOutputContract("outputs[%d].outputId is required", outputIndex)
		}
		if _, exists := seen[contract.OutputID]; exists {
			return PublicOutputContracts{}, invalidOutputContract("duplicate outputId %q", contract.OutputID)
		}
		seen[contract.OutputID] = struct{}{}
		for columnIndex, column := range contract.Columns {
			path := fmt.Sprintf("outputs[%d].columns[%d]", outputIndex, columnIndex)
			for _, field := range []struct{ name, value string }{
				{"column", column.Column}, {"label", column.Label},
				{"logicalType", column.LogicalType},
			} {
				if strings.TrimSpace(field.value) == "" {
					return PublicOutputContracts{}, invalidOutputContract("%s.%s is required", path, field.name)
				}
			}
		}
	}
	return contracts, nil
}

func (c PublicOutputContracts) Output(outputID string) (PublicOutputContract, bool) {
	for _, output := range c.Outputs {
		if output.OutputID == outputID {
			return output, true
		}
	}
	return PublicOutputContract{}, false
}

// ValidateAgainst proves that the ordered contract set exactly matches the
// recipe outputs and each output's ordered emitted columns.
func (c PublicOutputContracts) ValidateAgainst(bundle recipe.Bundle, emitted []EmittedColumn) error {
	if len(c.Outputs) != len(bundle.Outputs) {
		return invalidOutputContract("output count %d does not match recipe output count %d", len(c.Outputs), len(bundle.Outputs))
	}
	emittedByOutput := make(map[string][]EmittedColumn, len(c.Outputs))
	for i, column := range emitted {
		if strings.TrimSpace(column.OutputID) == "" {
			return invalidOutputContract("emittedColumns[%d].outputId is required", i)
		}
		emittedByOutput[column.OutputID] = append(emittedByOutput[column.OutputID], column)
	}
	for i, contract := range c.Outputs {
		if contract.OutputID != bundle.Outputs[i].Name {
			return invalidOutputContract("outputs[%d].outputId %q does not match recipe output %q", i, contract.OutputID, bundle.Outputs[i].Name)
		}
		if err := contract.ValidateAgainst(bundle, emittedByOutput[contract.OutputID]); err != nil {
			return err
		}
		delete(emittedByOutput, contract.OutputID)
	}
	for outputID := range emittedByOutput {
		return invalidOutputContract("emitted columns reference unknown output %q", outputID)
	}
	return nil
}

// ValidateAgainst proves that the typed public contract is exactly the
// compiler-owned emitted-column sequence for the receipt's bundle. It checks
// output identity, order, IDs, and every public metadata bit. A mismatch is a
// receipt integrity failure and therefore requires explicit recompilation.
func (c PublicOutputContract) ValidateAgainst(bundle recipe.Bundle, emitted []EmittedColumn) error {
	if strings.TrimSpace(c.OutputID) == "" {
		return invalidOutputContract("outputId is required")
	}
	var compiledOutput *recipe.Output
	for _, output := range bundle.Outputs {
		if output.Name == c.OutputID {
			value := output
			compiledOutput = &value
			break
		}
	}
	if compiledOutput == nil {
		return invalidOutputContract("outputId %q is absent from the compiled recipe", c.OutputID)
	}
	if c.RootResourceType != "" && c.RootResourceType != compiledOutput.RootResourceType {
		return invalidOutputContract("rootResourceType %q does not match compiled recipe %q", c.RootResourceType, compiledOutput.RootResourceType)
	}
	if c.RowGrain != "" && c.RowGrain != compiledOutput.RowGrain {
		return invalidOutputContract("rowGrain %q does not match compiled recipe %q", c.RowGrain, compiledOutput.RowGrain)
	}
	if c.RowMultiplication != "" && c.RowMultiplication != "none" {
		return invalidOutputContract("unsupported rowMultiplication %q", c.RowMultiplication)
	}
	if len(c.Columns) != len(emitted) {
		return invalidOutputContract("column count %d does not match emitted column count %d", len(c.Columns), len(emitted))
	}
	seenPublic := make(map[string]struct{}, len(emitted))
	expectedLossless, expectedMLReady := true, true
	for i, column := range emitted {
		if strings.TrimSpace(column.OutputID) != c.OutputID {
			return invalidOutputContract("emittedColumns[%d] belongs to output %q, want %q", i, column.OutputID, c.OutputID)
		}
		if strings.TrimSpace(column.PublicColumn) == "" {
			return invalidOutputContract("emittedColumns[%d] has an empty identity", i)
		}
		if _, exists := seenPublic[column.PublicColumn]; exists {
			return invalidOutputContract("duplicate publicColumn %q", column.PublicColumn)
		}
		seenPublic[column.PublicColumn] = struct{}{}
		expectedLossless = expectedLossless && column.Lossless
		expectedMLReady = expectedMLReady && column.MLReady
		actual := c.Columns[i]
		if actual.Column != column.PublicColumn || !reflect.DeepEqual(actual.AuthoredColumns, column.AuthoredColumns) || actual.Label != column.Label || actual.LogicalType != column.LogicalType || actual.Nullable != column.Nullable || actual.Shape != column.Shape || actual.SourceResourceType != column.SourceResourceType || actual.SourcePath != column.SourcePath || actual.ChoiceArm != column.ChoiceArm || !reflect.DeepEqual(actual.Coordinates, column.Coordinates) || actual.Lossless != column.Lossless || actual.MLReady != column.MLReady || actual.Filterable != column.Filterable || actual.Chartable != column.Chartable {
			return invalidOutputContract("columns[%d] does not match emittedColumns[%d]", i, i)
		}
	}
	if c.Lossless != expectedLossless || c.MLReady != expectedMLReady {
		return invalidOutputContract("aggregate lossless/mlReady flags do not match emitted columns: got (%t, %t), want (%t, %t)", c.Lossless, c.MLReady, expectedLossless, expectedMLReady)
	}
	return nil
}

func invalidOutputContract(format string, args ...any) error {
	return fmt.Errorf("%w: public output contract %s", ErrReceiptRecompileRequired, fmt.Sprintf(format, args...))
}
