package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/store/clickhouse"
)

func (t *clickHouseBundleTx) CreateOutput(ctx context.Context, name string, columns []clickhouse.Column) error {
	if t.idempotent {
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	if t.checkpointUncertain {
		return ErrBundleCheckpointUncertain
	}
	if err := t.ensureLease(); err != nil {
		return err
	}
	if !validBundleOutput(name) {
		return fmt.Errorf("invalid bundle output name %q", name)
	}
	for _, output := range t.execution.Outputs {
		if output.Name == name {
			return fmt.Errorf("bundle output %q is duplicated", name)
		}
	}
	if len(columns) == 0 {
		return fmt.Errorf("bundle output %q has no columns", name)
	}
	logicalColumns := append([]clickhouse.Column(nil), columns...)
	columns = withRowIdentityColumn(columns)
	for _, c := range columns {
		if c.Name == "__loom_row_id" {
			continue
		}
		if err := validateBundleColumn(publication.PhysicalColumn{Name: c.Name, ClickHouse: c.Type}); err != nil {
			return err
		}
	}
	table := t.tableName(name)
	if err := t.store.clickHouse.CreateTable(ctx, table, columns); err != nil {
		return err
	}
	converted := make([]publication.PhysicalColumn, len(columns))
	for i, c := range columns {
		converted[i] = publication.PhysicalColumn{Name: c.Name, ClickHouse: c.Type}
	}
	t.execution.Outputs = append(t.execution.Outputs, publication.BundleOutputRecord{Name: name, PhysicalTable: table, Selector: t.execution.Selector(name), Columns: converted, State: publication.BundleRunning})
	if t.columns == nil {
		t.columns = make(map[string][]clickhouse.Column)
	}

	t.columns[name] = logicalColumns
	return t.save(ctx)
}

func (t *clickHouseBundleTx) SetOutputMetadata(ctx context.Context, name string, columns []publication.LogicalColumn) error {
	if t.idempotent {
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	if t.checkpointUncertain {
		return ErrBundleCheckpointUncertain
	}
	if err := t.ensureLease(); err != nil {
		return err
	}
	idx := t.outputIndex(name)
	if idx < 0 {
		return fmt.Errorf("bundle output %q was not created", name)
	}
	record := &t.execution.Outputs[idx]
	if len(columns) == 0 {
		return fmt.Errorf("bundle output %q has no metadata columns", name)
	}
	physical := make(map[string]publication.LogicalColumn, len(columns))
	for _, column := range columns {
		physical[column.Name] = column
	}
	for index := range record.Columns {
		column := &record.Columns[index]
		if logical, ok := physical[column.Name]; ok {
			column.ID = logical.ID
			column.SemanticPath = logical.SemanticPath
			column.LogicalType = logical.Kind
			column.Nullable = logical.Nullable
			column.Repeated = logical.Repeated
			column.Provenance = logical.Provenance
			column.LoomOwned = logical.LoomOwned || logical.IsIdentity || column.Name == "__loom_row_id" || column.Name == "auth_resource_path" || column.Name == "project_id"
		}
	}
	return t.save(ctx)
}

func (t *clickHouseBundleTx) SetSourceRowMetadata(ctx context.Context, name string, metadata publication.SourceRowMetadata) error {
	if t.idempotent {
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	if err := t.ensureLease(); err != nil {
		return err
	}
	metadata.ResourceType = strings.TrimSpace(metadata.ResourceType)
	metadata.IDColumn = strings.TrimSpace(metadata.IDColumn)
	if metadata.ResourceType == "" || metadata.IDColumn == "" {
		return fmt.Errorf("source row metadata requires resource type and id column")
	}
	idx := t.outputIndex(name)
	if idx < 0 {
		return fmt.Errorf("bundle output %q was not created", name)
	}
	record := &t.execution.Outputs[idx]
	found := false
	for _, column := range record.Columns {
		if column.Name == metadata.IDColumn {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("source id column %q is not present in output %q", metadata.IDColumn, name)
	}
	record.SourceRow = &metadata
	return t.save(ctx)
}

func (t *clickHouseBundleTx) FinalizeSchema(ctx context.Context, schemas []publication.OutputSchema) error {
	if t.idempotent {
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	if t.checkpointUncertain {
		return ErrBundleCheckpointUncertain
	}
	if err := t.ensureLease(); err != nil {
		return err
	}
	for _, schema := range schemas {
		idx := t.outputIndex(schema.Name)
		if idx < 0 {
			return fmt.Errorf("bundle output %q was not created", schema.Name)
		}
		record := &t.execution.Outputs[idx]
		if schema.SourceRow != nil {
			metadata := *schema.SourceRow
			metadata.ResourceType = strings.TrimSpace(metadata.ResourceType)
			metadata.IDColumn = strings.TrimSpace(metadata.IDColumn)
			if record.SourceRow != nil && *record.SourceRow != metadata {
				return fmt.Errorf("output %q source identity changed during schema finalization", schema.Name)
			}
			if record.SourceRow == nil {
				record.SourceRow = &metadata
			}
		}
		retained := make(map[string]publication.LogicalColumn, len(schema.Columns))
		for _, column := range schema.Columns {
			retained[column.Name] = column
		}
		var dropped []string
		seen := make(map[string]bool, len(record.Columns))
		kept := make([]publication.PhysicalColumn, 0, len(record.Columns))
		for _, column := range record.Columns {
			logical, ok := retained[column.Name]
			if !ok {
				if column.Name == "__loom_row_id" || column.Name == "auth_resource_path" || column.Name == "project_id" || strings.HasPrefix(column.Name, "__loom_") {
					seen[column.Name] = true
					kept = append(kept, column)
				} else if column.Provenance != publication.ColumnDiscovered {
					return fmt.Errorf("output %q attempted to remove explicit column %q", schema.Name, column.Name)
				} else {
					dropped = append(dropped, column.Name)
				}
				continue
			}
			seen[column.Name] = true
			column.SemanticPath, column.LogicalType = logical.SemanticPath, logical.Kind
			column.Nullable, column.Repeated = logical.Nullable, logical.Repeated
			column.Provenance, column.LoomOwned = logical.Provenance, logical.LoomOwned || logical.IsIdentity || strings.HasPrefix(column.Name, "__loom_") || column.Name == "auth_resource_path" || column.Name == "project_id"
			kept = append(kept, column)
		}
		if len(dropped) > 0 {
			if err := t.store.clickHouse.DropColumns(ctx, record.PhysicalTable, dropped); err != nil {
				return err
			}
		}
		record.Columns = kept
		for name := range retained {
			if !seen[name] {
				return fmt.Errorf("output %q retained unknown schema column %q", schema.Name, name)
			}
		}
	}
	identity := publication.PublicationIdentity{Name: t.execution.Name, TranslationVersion: t.execution.TranslationVersion, Project: t.execution.Project, DatasetGeneration: t.execution.DatasetGeneration, RecipeDigest: t.execution.RecipeDigest, SchemaDigest: t.execution.SchemaDigest, ReceiptID: t.execution.ReceiptID, ScopeDigest: t.execution.ScopeDigest, EngineVersion: t.execution.EngineVersion, AuthScopeMode: t.execution.AuthScopeMode, AuthResourcePaths: append([]string(nil), t.execution.AuthResourcePaths...)}
	t.execution.SchemaDigest = publication.FinalSchemaDigest(identity, schemas)
	return t.save(ctx)
}

func (t *clickHouseBundleTx) SetFinalSchemaDigest(digest string) error {
	if t.idempotent {
		return nil
	}
	if strings.TrimSpace(digest) == "" {
		return fmt.Errorf("final schema digest is required")
	}
	t.execution.SchemaDigest = digest
	return nil
}

func (t *clickHouseBundleTx) InsertRows(ctx context.Context, name string, columns []clickhouse.Column, rows []map[string]any) error {
	if t.idempotent || len(rows) == 0 {
		return nil
	}
	if t.checkpointUncertain {
		return ErrBundleCheckpointUncertain
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	if err := t.ensureLease(); err != nil {
		return err
	}
	idx := t.outputIndex(name)
	if idx < 0 {
		return fmt.Errorf("bundle output %q was not created", name)
	}
	record := &t.execution.Outputs[idx]
	effectiveColumns := columns
	if len(columns)+1 == len(record.Columns) && record.Columns[0].Name == "__loom_row_id" {
		effectiveColumns = make([]clickhouse.Column, 0, len(columns)+1)
		effectiveColumns = append(effectiveColumns, clickhouse.Column{Name: "__loom_row_id", Type: record.Columns[0].ClickHouse})
		effectiveColumns = append(effectiveColumns, columns...)
		rows = addGeneratedRowIdentities(rows, record.RowCount)
	}
	if len(effectiveColumns) != len(record.Columns) {
		return fmt.Errorf("output %q schema changed after preflight", name)
	}
	for i, c := range effectiveColumns {
		if record.Columns[i].Name != c.Name || record.Columns[i].ClickHouse != c.Type {
			return fmt.Errorf("output %q schema changed after preflight", name)
		}
	}
	if err := t.store.clickHouse.InsertRows(ctx, record.PhysicalTable, effectiveColumns, rows); err != nil {
		return err
	}
	record.RowCount += int64(len(rows))
	for _, row := range rows {
		encoded, _ := json.Marshal(row)
		record.ByteCount += int64(len(encoded))
	}
	if err := t.save(ctx); err != nil {
		t.checkpointUncertain = true
		return errors.Join(ErrBundleCheckpointUncertain, err)
	}
	return nil
}

func withRowIdentityColumn(columns []clickhouse.Column) []clickhouse.Column {
	for _, column := range columns {
		if column.Name == "__loom_row_id" {
			return append([]clickhouse.Column(nil), columns...)
		}
	}
	result := make([]clickhouse.Column, 0, len(columns)+1)
	result = append(result, clickhouse.Column{Name: "__loom_row_id", Type: "String"})
	return append(result, columns...)
}

func addGeneratedRowIdentities(rows []map[string]any, start int64) []map[string]any {
	result := make([]map[string]any, len(rows))
	for index, row := range rows {
		copy := cloneBundleRow(row)
		if _, ok := copy["__loom_row_id"]; !ok {
			copy["__loom_row_id"] = fmt.Sprintf("%d", start+int64(index))
		}
		result[index] = copy
	}
	return result
}

func cloneBundleRow(row map[string]any) map[string]any {
	copy := make(map[string]any, len(row)+1)
	for key, value := range row {
		copy[key] = value
	}
	return copy
}

var bundleOutputRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var schemaIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var supportedSchemaScalarRE = regexp.MustCompile(`^(String|JSON|UUID|Bool|Int8|Int16|Int32|Int64|Int128|Int256|UInt8|UInt16|UInt32|UInt64|UInt128|UInt256|Float32|Float64|Date|Date32|DateTime|DateTime64(\([^)]*\))?)$`)

func validateBundleColumn(column publication.PhysicalColumn) error {
	if column.Name == "" || !schemaIdentifierRE.MatchString(column.Name) || column.Name == "__loom_row_id" {
		return fmt.Errorf("invalid dataframe schema column %q", column.Name)
	}
	typ := column.ClickHouse
	for strings.HasPrefix(typ, "Nullable(") || strings.HasPrefix(typ, "Array(") {
		typ = strings.TrimSuffix(strings.TrimPrefix(typ, strings.SplitN(typ, "(", 2)[0]+"("), ")")
	}
	if typ == "" || !supportedSchemaScalarRE.MatchString(typ) {
		return fmt.Errorf("unsupported ClickHouse type %q for schema column %q", column.ClickHouse, column.Name)
	}
	return nil
}

func validBundleOutput(value string) bool { return bundleOutputRE.MatchString(value) }

func toColumns(columns []publication.LogicalColumn) ([]clickhouse.Column, error) {
	result := make([]clickhouse.Column, 0, len(columns))
	for _, column := range columns {
		kind := strings.ToLower(strings.TrimSpace(column.Kind))
		columnType := "String"
		logicalType := ""
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

			columnType = "String"
			logicalType = "object"
		}
		if column.Repeated {
			if logicalType == "object" {
				columnType = "Array(Nullable(String))"
			} else {
				columnType = "Array(" + columnType + ")"
			}
		} else if column.Nullable {
			columnType = "Nullable(" + columnType + ")"
		}
		result = append(result, clickhouse.Column{Name: column.Name, Type: columnType, LogicalType: logicalType})
	}
	return result, nil
}

func allOutputsQueryable(outputs []publication.BundleOutputRecord) bool {
	if len(outputs) == 0 {
		return false
	}
	for _, output := range outputs {
		if !output.Queryable() {
			return false
		}
	}
	return true
}
