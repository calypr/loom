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
	schemas, err := validateOutputs(normalizedOutputs)
	if err != nil {
		return Result{}, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidData, "")
	}
	limits = limits.normalized()
	tx, err := target.Begin(ctx, identity, schemas)
	if err != nil {
		return Result{}, err
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
	for _, output := range normalizedOutputs {
		stat := PublishedOutput{Name: output.Name}
		batch := make([]map[string]any, 0, limits.BatchRows)
		batchBytes := 0
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
			if err := validateRow(output.Columns, row); err != nil {
				return dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidData, "")
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
			{Name: "auth_resource_path", Kind: "string", Nullable: true},
			{Name: "project_id", Kind: "string"},
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

func validateOutputs(outputs []OutputStream) ([]OutputSchema, error) {
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
			if _, ok := columnSeen[column.Name]; ok {
				return nil, fmt.Errorf("output %q column %q is duplicated", name, column.Name)
			}
			columnSeen[column.Name] = struct{}{}
		}
		schemas = append(schemas, OutputSchema{Name: name, Columns: columns})
	}
	return schemas, nil
}

func validateRow(columns []LogicalColumn, row map[string]any) error {
	if row == nil {
		return fmt.Errorf("row is nil")
	}
	known := make(map[string]LogicalColumn, len(columns))
	for _, column := range columns {
		known[column.Name] = column
		value, ok := row[column.Name]
		if !ok || value == nil {
			if !column.Nullable {
				return fmt.Errorf("required column %q is missing", column.Name)
			}
			continue
		}
		if err := validateValue(column, value); err != nil {
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

func validateValue(column LogicalColumn, value any) error {
	if column.Repeated {
		v := reflect.ValueOf(value)
		if v.Kind() != reflect.Array && v.Kind() != reflect.Slice {
			return fmt.Errorf("column %q must be repeated", column.Name)
		}
		for i := 0; i < v.Len(); i++ {
			if err := validateScalar(column, v.Index(i).Interface()); err != nil {
				return err
			}
		}
		return nil
	}
	return validateScalar(column, value)
}

func validateScalar(column LogicalColumn, value any) error {
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
		return fmt.Errorf("object-valued column %q is not supported by the flat publication contract", column.Name)
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
