package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

// Publish consumes each stream once and keeps at most one bounded batch in
// memory. A target is not committed until every stream has passed validation.
func Publish(ctx context.Context, target Target, identity PublicationIdentity, outputs []OutputStream, limits Limits) (Result, error) {
	if target == nil {
		return Result{}, fmt.Errorf("publication target is required")
	}
	if len(outputs) == 0 {
		return Result{}, fmt.Errorf("publication requires at least one output")
	}
	normalizedOutputs, err := injectPublicationMetadata(identity, outputs)
	if err != nil {
		return Result{}, err
	}
	supportsObjects := false
	if objectTarget, ok := target.(ObjectValueTarget); ok {
		supportsObjects = objectTarget.SupportsObjectValues()
	}
	schemas, err := validateOutputs(normalizedOutputs, supportsObjects)
	if err != nil {
		return Result{}, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidData, "")
	}
	limits = limits.normalized()
	tx, err := target.Begin(ctx, identity, schemas)
	if err != nil {
		return Result{}, err
	}
	if noop, ok := tx.(interface {
		Idempotent() bool
		ExistingPublishedOutputs() []PublishedOutput
	}); ok && noop.Idempotent() {
		return Result{Outputs: noop.ExistingPublishedOutputs()}, nil
	}
	fail := func(cause error) (Result, error) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		var abortErr error
		if aborter, ok := tx.(interface {
			Abort(context.Context, error) error
		}); ok {
			abortErr = aborter.Abort(cleanupCtx, cause)
		} else {
			abortErr = tx.Rollback(cleanupCtx)
		}
		return Result{}, errors.Join(cause, abortErr)
	}
	stats := make(map[string]PublishedOutput, len(normalizedOutputs))
	populated := make(map[string]map[string]bool, len(normalizedOutputs))
	for _, output := range normalizedOutputs {
		stat := PublishedOutput{Name: output.Name}
		batch := make([]map[string]any, 0, limits.BatchRows)
		batchBytes := 0
		populated[output.Name] = make(map[string]bool)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			if err := tx.WriteBatch(ctx, output.Name, batch); err != nil {
				return err
			}
			batch = batch[:0]
			batchBytes = 0
			return nil
		}
		err := output.Stream(ctx, func(row map[string]any) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := validateRow(output.Columns, row, supportsObjects); err != nil {
				return dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidData, "")
			}
			for _, column := range output.Columns {
				if column.Provenance != ColumnDiscovered || column.LoomOwned || column.IsIdentity {
					continue
				}
				value, ok := row[column.Name]
				if ok && populatedValue(column, value) {
					populated[output.Name][column.Name] = true
				}
			}
			encoded, err := json.Marshal(row)
			if err != nil {
				return dataframeerrors.Wrap(err, dataframeerrors.CodeOutputEncodingFailed, "")
			}
			if len(encoded) > limits.BatchBytes {
				return fmt.Errorf("output %q row exceeds batch byte limit %d", output.Name, limits.BatchBytes)
			}
			batch = append(batch, row)
			batchBytes += len(encoded)
			stat.RowCount++
			stat.ByteCount += int64(len(encoded))
			if len(batch) >= limits.BatchRows || batchBytes >= limits.BatchBytes {
				return flush()
			}
			return nil
		})
		if err != nil {
			return fail(fmt.Errorf("output %q stream: %w", output.Name, err))
		}
		if err := flush(); err != nil {
			return fail(fmt.Errorf("output %q final batch: %w", output.Name, err))
		}
		stats[output.Name] = stat
	}
	retained := make([]OutputSchema, 0, len(normalizedOutputs))
	for _, output := range normalizedOutputs {
		schema := OutputSchema{Name: output.Name}
		for _, column := range output.Columns {
			if column.Provenance == ColumnDiscovered && !column.LoomOwned && !column.IsIdentity && !populated[output.Name][column.Name] {
				continue
			}
			schema.Columns = append(schema.Columns, column)
		}
		retained = append(retained, schema)
	}
	finalDigest := FinalSchemaDigest(identity, retained)
	if finalizer, ok := tx.(interface {
		FinalizeSchema(context.Context, []OutputSchema) error
	}); ok {
		if err := finalizer.FinalizeSchema(ctx, retained); err != nil {
			return fail(fmt.Errorf("publication schema finalization: %w", err))
		}
	} else {
		for index := range retained {
			if len(retained[index].Columns) != len(normalizedOutputs[index].Columns) {
				return fail(fmt.Errorf("publication target cannot finalize pruned schema for output %q", retained[index].Name))
			}
		}
	}
	if setter, ok := tx.(interface{ SetFinalSchemaDigest(string) error }); ok {
		if err := setter.SetFinalSchemaDigest(finalDigest); err != nil {
			return fail(fmt.Errorf("publication schema digest: %w", err))
		}
	}
	published, err := tx.Commit(ctx)
	if err != nil {
		return fail(fmt.Errorf("publication commit: %w", err))
	}
	if len(published) == 0 {
		published = make([]PublishedOutput, 0, len(outputs))
		for _, output := range normalizedOutputs {
			published = append(published, stats[output.Name])
		}
	} else {
		for i := range published {
			if stat, ok := stats[published[i].Name]; ok {
				published[i].RowCount = stat.RowCount
				published[i].ByteCount = stat.ByteCount
			}
		}
	}
	return Result{Outputs: published}, nil
}

func injectPublicationMetadata(identity PublicationIdentity, outputs []OutputStream) ([]OutputStream, error) {
	result := make([]OutputStream, 0, len(outputs))
	for _, output := range outputs {
		columns := make([]LogicalColumn, 0, len(output.Columns))
		for _, column := range output.Columns {
			if column.Name == "auth_resource_path" {
				return nil, fmt.Errorf("output %q column %q is reserved by Loom", output.Name, column.Name)
			}
			if column.Name != "project_id" {
				columns = append(columns, column)
			}
		}
		copyOutput := output
		copyOutput.Columns = append([]LogicalColumn{
			{Name: "auth_resource_path", Kind: "string", Nullable: true, Provenance: ColumnExplicit, LoomOwned: true},
			{Name: "project_id", Kind: "string", Provenance: ColumnExplicit, LoomOwned: true},
		}, columns...)
		originalStream := output.Stream
		copyOutput.Stream = func(ctx context.Context, visit func(map[string]any) error) error {
			return originalStream(ctx, func(row map[string]any) error {
				if row == nil {
					return visit(nil)
				}
				row = cloneRow(row)
				if _, ok := row["auth_resource_path"]; !ok {
					if len(identity.AuthResourcePaths) == 1 {
						row["auth_resource_path"] = identity.AuthResourcePaths[0]
					} else {
						row["auth_resource_path"] = ""
					}
				}
				// project_id is Loom-owned metadata; never trust a source row value.
				row["project_id"] = identity.Project
				return visit(row)
			})
		}
		result = append(result, copyOutput)
	}
	return result, nil
}

func cloneRow(row map[string]any) map[string]any {
	copy := make(map[string]any, len(row)+1)
	for key, value := range row {
		copy[key] = value
	}
	return copy
}

func validateOutputs(outputs []OutputStream, supportsObjects bool) ([]OutputSchema, error) {
	seen := map[string]struct{}{}
	schemas := make([]OutputSchema, 0, len(outputs))
	for _, output := range outputs {
		name := strings.TrimSpace(output.Name)
		if name == "" || output.Stream == nil {
			return nil, fmt.Errorf("output name and stream are required")
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("output %q is duplicated", name)
		}
		seen[name] = struct{}{}
		if len(output.Columns) == 0 {
			return nil, fmt.Errorf("output %q has no columns", name)
		}
		columns := append([]LogicalColumn(nil), output.Columns...)
		columnSeen := map[string]struct{}{}
		for _, column := range columns {
			if strings.TrimSpace(column.Name) == "" || strings.TrimSpace(column.Kind) == "" {
				return nil, fmt.Errorf("output %q has an invalid column", name)
			}
			if strings.EqualFold(strings.TrimSpace(column.Kind), "object") && !supportsObjects {
				return nil, fmt.Errorf("output %q object-valued column %q is not supported by the publication target", name, column.Name)
			}
			if _, ok := columnSeen[column.Name]; ok {
				return nil, fmt.Errorf("output %q column %q is duplicated", name, column.Name)
			}
			columnSeen[column.Name] = struct{}{}
		}
		schemas = append(schemas, OutputSchema{Name: name, Columns: columns})
	}
	return schemas, nil
}

func validateRow(columns []LogicalColumn, row map[string]any, supportsObjects bool) error {
	if row == nil {
		return fmt.Errorf("row is nil")
	}
	known := make(map[string]LogicalColumn, len(columns))
	for _, column := range columns {
		known[column.Name] = column
		value, ok := row[column.Name]
		if !ok || value == nil {
			if column.Provenance == ColumnDiscovered {
				continue
			}
			if !column.Nullable {
				return fmt.Errorf("required column %q is missing", column.Name)
			}
			continue
		}
		if err := validateValue(column, value, supportsObjects); err != nil {
			return err
		}
	}
	for name := range row {
		if _, ok := known[name]; !ok {
			return fmt.Errorf("row contains undeclared column %q", name)
		}
	}
	return nil
}

func populatedValue(column LogicalColumn, value any) bool {
	if value == nil {
		return false
	}
	if column.Repeated {
		v := reflect.ValueOf(value)
		if v.Kind() == reflect.Array || v.Kind() == reflect.Slice {
			return v.Len() > 0
		}
	}
	return true
}

func validateValue(column LogicalColumn, value any, supportsObjects bool) error {
	if column.Repeated {
		v := reflect.ValueOf(value)
		if v.Kind() != reflect.Array && v.Kind() != reflect.Slice {
			return fmt.Errorf("column %q must be repeated", column.Name)
		}
		for i := 0; i < v.Len(); i++ {
			if err := validateScalar(column, v.Index(i).Interface(), supportsObjects); err != nil {
				return err
			}
		}
		return nil
	}
	return validateScalar(column, value, supportsObjects)
}

func validateScalar(column LogicalColumn, value any, supportsObjects bool) error {
	if value == nil {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(column.Kind))
	valid := false
	switch kind {
	case "string", "code", "uuid", "date", "date-time", "datetime":
		_, valid = value.(string)
	case "integer":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float64, json.Number:
			valid = true
		}
	case "decimal":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
			valid = true
		}
	case "boolean":
		_, valid = value.(bool)
	case "object":
		if !supportsObjects {
			return fmt.Errorf("object-valued column %q is not supported by the flat publication contract", column.Name)
		}
		if err := validateObjectValue(value); err != nil {
			return fmt.Errorf("object-valued column %q is invalid: %w", column.Name, err)
		}
		valid = true
	default:
		return fmt.Errorf("column %q has unsupported logical kind %q", column.Name, column.Kind)
	}
	if !valid {
		return fmt.Errorf("column %q has value of incompatible type %T", column.Name, value)
	}
	if kind == "integer" {
		if f, ok := value.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f) {
			return fmt.Errorf("column %q has non-integral value", column.Name)
		}
	}
	return nil
}

func validateObjectValue(value any) error {
	rv := reflect.ValueOf(value)
	for rv.IsValid() && (rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer) {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return nil
	}
	if rv.Kind() != reflect.Map && rv.Kind() != reflect.Struct {
		return fmt.Errorf("expected an object, got %T", value)
	}
	if _, err := json.Marshal(value); err != nil {
		return fmt.Errorf("cannot encode as JSON: %w", err)
	}
	return nil
}
