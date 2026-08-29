// Package clickhouse is Loom's narrow typed boundary around the official
// ClickHouse Go driver. The materialization service never receives a raw SQL
// connection or a table name from the browser.
package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type Options struct {
	// URL accepts clickhouse://host:9000 or http://host:8123 URLs. The
	// official driver supports both protocols; native TCP is the default
	// recommended production transport.
	URL      string
	Database string
	Username string
	Password string
	Timeout  time.Duration
}

type Client struct {
	opts Options
	conn driver.Conn
}

type Column struct {
	Name string
	Type string
}

var identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return nil, fmt.Errorf("clickhouse URL is required")
	}
	if opts.Database == "" {
		opts.Database = "default"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	parsed, err := parseOptions(opts)
	if err != nil {
		return nil, err
	}
	conn, err := ch.Open(parsed)
	if err != nil {
		return nil, fmt.Errorf("open ClickHouse connection: %w", err)
	}
	return &Client{opts: opts, conn: conn}, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Ping verifies that the configured ClickHouse connection is reachable.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("ClickHouse connection is not configured")
	}
	return c.conn.Ping(ctx)
}

func (c *Client) CreateTable(ctx context.Context, table string, columns []Column) error {
	if err := validateIdentifier(table); err != nil {
		return err
	}
	if len(columns) == 0 {
		return fmt.Errorf("at least one ClickHouse column is required")
	}
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		if err := validateIdentifier(column.Name); err != nil {
			return err
		}
		if strings.TrimSpace(column.Type) == "" {
			return fmt.Errorf("ClickHouse type for %q is required", column.Name)
		}
		parts = append(parts, fmt.Sprintf("`%s` %s", column.Name, column.Type))
	}
	query := fmt.Sprintf("CREATE TABLE `%s` (%s) ENGINE = MergeTree ORDER BY (`__loom_row_id`)", table, strings.Join(parts, ", "))
	return c.conn.Exec(ctx, query)
}

func (c *Client) EnsureDatabase(ctx context.Context) error {
	if err := validateIdentifier(c.opts.Database); err != nil {
		return err
	}
	// CREATE DATABASE must execute through a connection whose default database
	// is known to exist; the main connection intentionally targets the new DB.
	options, err := parseOptions(c.opts)
	if err != nil {
		return err
	}
	options.Auth.Database = "default"
	bootstrap, err := ch.Open(options)
	if err != nil {
		return err
	}
	defer bootstrap.Close()
	return bootstrap.Exec(ctx, fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`", c.opts.Database))
}

func (c *Client) AddColumn(ctx context.Context, table string, column Column) error {
	if err := validateIdentifier(table); err != nil {
		return err
	}
	if err := validateIdentifier(column.Name); err != nil {
		return err
	}
	return c.conn.Exec(ctx, fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN IF NOT EXISTS `%s` %s", table, column.Name, column.Type))
}

func (c *Client) DropTable(ctx context.Context, table string) error {
	if err := validateIdentifier(table); err != nil {
		return err
	}
	return c.conn.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", table))
}

// DropColumns removes validated staging columns in one ALTER TABLE operation.
func (c *Client) DropColumns(ctx context.Context, table string, columns []string) error {
	if err := validateIdentifier(table); err != nil {
		return err
	}
	if len(columns) == 0 {
		return nil
	}
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		if err := validateIdentifier(column); err != nil {
			return err
		}
		parts = append(parts, fmt.Sprintf("DROP COLUMN IF EXISTS `%s`", column))
	}
	return c.conn.Exec(ctx, fmt.Sprintf("ALTER TABLE `%s` %s", table, strings.Join(parts, ", ")))
}

// VerifyOutput confirms that a staged publication table is readable, has the
// expected physical columns, and contains exactly the rows acknowledged by
// the publisher before any visibility pointer is advanced.
func (c *Client) VerifyOutput(ctx context.Context, table string, columns []Column, expectedRows int64) error {
	if err := validateIdentifier(table); err != nil {
		return err
	}
	rows, err := c.QueryRowsArgs(ctx, fmt.Sprintf("SELECT count() AS row_count FROM `%s`", table), []string{"row_count"})
	if err != nil {
		return fmt.Errorf("read staged output %q: %w", table, err)
	}
	if len(rows) != 1 {
		return fmt.Errorf("staged output %q returned %d count rows", table, len(rows))
	}
	count, ok := integerValue(rows[0]["row_count"])
	if !ok || count != expectedRows {
		return fmt.Errorf("staged output %q row count = %v, want %d", table, rows[0]["row_count"], expectedRows)
	}
	columnRows, err := c.QueryRowsArgs(ctx, "SELECT name, type FROM system.columns WHERE database = currentDatabase() AND table = ? ORDER BY position", []string{"name", "type"}, table)
	if err != nil {
		return fmt.Errorf("inspect staged output %q: %w", table, err)
	}
	if len(columnRows) != len(columns) {
		return fmt.Errorf("staged output %q has %d columns, want %d", table, len(columnRows), len(columns))
	}
	for i, column := range columns {
		if columnRows[i]["name"] != column.Name || columnRows[i]["type"] != column.Type {
			return fmt.Errorf("staged output %q column %d = %v %v, want %s %s", table, i, columnRows[i]["name"], columnRows[i]["type"], column.Name, column.Type)
		}
	}
	return nil
}

func integerValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case uint64:
		if typed <= uint64(^uint64(0)>>1) {
			return int64(typed), true
		}
	case float64:
		if typed == math.Trunc(typed) && typed >= math.MinInt64 && typed <= math.MaxInt64 {
			return int64(typed), true
		}
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return parsed, true
		}
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func (c *Client) InsertRows(ctx context.Context, table string, columns []Column, rows []map[string]any) error {
	if err := validateIdentifier(table); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	names := make([]string, len(columns))
	for i, column := range columns {
		if err := validateIdentifier(column.Name); err != nil {
			return err
		}
		names[i] = fmt.Sprintf("`%s`", column.Name)
	}
	batch, err := c.conn.PrepareBatch(ctx, fmt.Sprintf("INSERT INTO `%s` (%s)", table, strings.Join(names, ", ")))
	if err != nil {
		return fmt.Errorf("prepare ClickHouse batch: %w", err)
	}
	defer batch.Close()
	for _, row := range rows {
		values := make([]any, len(columns))
		for i, column := range columns {
			value, err := normalizeInsertValue(column, row[column.Name])
			if err != nil {
				return err
			}
			values[i] = value
		}
		if err := batch.Append(values...); err != nil {
			return fmt.Errorf("append ClickHouse batch row: %w", err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("send ClickHouse batch: %w", err)
	}
	return nil
}

func normalizeInsertValue(column Column, value any) (any, error) {
	columnType := strings.TrimSpace(column.Type)
	baseType := unwrapClickHouseType(columnType, "Nullable")
	if value == nil {
		if strings.HasPrefix(baseType, "Array(") {
			return []any{}, nil
		}
		return nil, nil
	}
	if itemType, ok := wrappedClickHouseType(baseType, "Array"); ok {
		items := reflect.ValueOf(value)
		if items.Kind() != reflect.Array && items.Kind() != reflect.Slice {
			return nil, fmt.Errorf("ClickHouse column %q expects %s, got %T", column.Name, columnType, value)
		}
		if unwrapClickHouseType(itemType, "Nullable") == "JSON" {
			normalized := make([]*ch.JSON, items.Len())
			for index := 0; index < items.Len(); index++ {
				item, err := normalizeInsertValue(Column{Name: column.Name, Type: itemType}, items.Index(index).Interface())
				if err != nil {
					return nil, err
				}
				if item == nil {
					continue
				}
				jsonValue, ok := item.(*ch.JSON)
				if !ok {
					return nil, fmt.Errorf("ClickHouse column %q JSON array item normalized to %T", column.Name, item)
				}
				normalized[index] = jsonValue
			}
			return normalized, nil
		}
		normalized := make([]any, items.Len())
		for index := 0; index < items.Len(); index++ {
			item, err := normalizeInsertValue(Column{Name: column.Name, Type: itemType}, items.Index(index).Interface())
			if err != nil {
				return nil, err
			}
			normalized[index] = item
		}
		return normalized, nil
	}
	if baseType == "JSON" {
		return normalizeJSONInsertValue(column.Name, value)
	}
	text, isString := value.(string)
	if !isString {
		return value, nil
	}
	switch {
	case baseType == "Date":
		parsed, err := time.Parse("2006-01-02", text)
		if err != nil {
			return nil, fmt.Errorf("ClickHouse column %q parse Date %q: %w", column.Name, text, err)
		}
		return parsed, nil
	case strings.HasPrefix(baseType, "DateTime"):
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, text); err == nil {
				return parsed, nil
			}
		}
		return nil, fmt.Errorf("ClickHouse column %q parse DateTime %q: unsupported timestamp format", column.Name, text)
	}
	return value, nil
}

func normalizeJSONInsertValue(column string, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	if _, ok := value.(*ch.JSON); ok {
		return value, nil
	}
	if _, ok := value.(ch.JSON); ok {
		return value, nil
	}
	object, ok, err := jsonObjectMap(value)
	if err != nil {
		return nil, fmt.Errorf("ClickHouse column %q JSON object: %w", column, err)
	}
	if !ok {
		return nil, fmt.Errorf("ClickHouse column %q expects a JSON object, got %T", column, value)
	}
	return buildNativeJSONObject(object)
}

func buildNativeJSONObject(object map[string]any) (*ch.JSON, error) {
	result := ch.NewJSON()
	if err := addNativeJSONFields(result, "", object); err != nil {
		return nil, err
	}
	return result, nil
}

func addNativeJSONFields(result *ch.JSON, prefix string, object map[string]any) error {
	for key, value := range object {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if nested, ok, err := jsonObjectMap(value); err != nil {
			return err
		} else if ok {
			if err := addNativeJSONFields(result, path, nested); err != nil {
				return err
			}
			continue
		}
		normalized, err := normalizeNativeJSONField(value)
		if err != nil {
			return err
		}
		result.SetValueAtPath(path, normalized)
	}
	return nil
}

func normalizeNativeJSONField(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(value)
	for rv.IsValid() && (rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer) {
		if rv.IsNil() {
			return nil, nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) || rv.Type().Elem().Kind() == reflect.Uint8 {
		return value, nil
	}
	items := make([]*ch.JSON, rv.Len())
	for index := 0; index < rv.Len(); index++ {
		item := rv.Index(index).Interface()
		if item == nil || (reflect.ValueOf(item).Kind() == reflect.Pointer && reflect.ValueOf(item).IsNil()) {
			continue
		}
		object, ok, err := jsonObjectMap(item)
		if err != nil {
			return nil, err
		}
		if !ok {
			return value, nil
		}
		converted, err := buildNativeJSONObject(object)
		if err != nil {
			return nil, err
		}
		items[index] = converted
	}
	return ch.NewDynamicWithType(items, "Array(JSON)"), nil
}

func jsonObjectMap(value any) (map[string]any, bool, error) {
	if value == nil {
		return nil, false, nil
	}
	switch typed := value.(type) {
	case map[string]any:
		return typed, true, nil
	case ch.JSON:
		return typed.NestedMap(), true, nil
	case *ch.JSON:
		if typed == nil {
			return nil, false, nil
		}
		return typed.NestedMap(), true, nil
	}
	rv := reflect.ValueOf(value)
	for rv.IsValid() && (rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer) {
		if rv.IsNil() {
			return nil, false, nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return nil, false, nil
	}
	if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
		result := make(map[string]any, rv.Len())
		for _, key := range rv.MapKeys() {
			result[key.String()] = rv.MapIndex(key).Interface()
		}
		return result, true, nil
	}
	if rv.Kind() != reflect.Struct {
		return nil, false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false, err
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, false, nil
	}
	return result, true, nil
}

func unwrapClickHouseType(value, wrapper string) string {
	if inner, ok := wrappedClickHouseType(value, wrapper); ok {
		return inner
	}
	return value
}

func wrappedClickHouseType(value, wrapper string) (string, bool) {
	prefix := wrapper + "("
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, ")") {
		return "", false
	}
	return strings.TrimSpace(value[len(prefix) : len(value)-1]), true
}

// QueryRowsArgs executes a SELECT with driver-bound positional arguments and
// decodes each row through ClickHouse's native driver. Query-controlled values
// must be supplied as args rather than interpolated into query text.
func (c *Client) QueryRowsArgs(ctx context.Context, query string, columns []string, args ...any) ([]map[string]any, error) {
	result := make([]map[string]any, 0)
	if err := c.QueryRowsArgsVisit(ctx, query, columns, func(row map[string]any) error {
		result = append(result, row)
		return nil
	}, args...); err != nil {
		return nil, err
	}
	return result, nil
}

// QueryRowsArgsVisit executes a SELECT and visits decoded rows as they arrive.
// The callback must not retain the map after returning unless it makes a copy.
// Query-controlled values remain driver-bound positional arguments.
func (c *Client) QueryRowsArgsVisit(ctx context.Context, query string, columns []string, visit func(map[string]any) error, args ...any) error {
	if len(columns) == 0 {
		return fmt.Errorf("ClickHouse query columns are required")
	}
	if visit == nil {
		return fmt.Errorf("ClickHouse row visitor is required")
	}
	base := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), "FORMAT JSONEachRow"))
	quoted := make([]string, len(columns))
	for i, column := range columns {
		if err := validateIdentifier(column); err != nil {
			return err
		}
		quoted[i] = fmt.Sprintf("`%s`", column)
	}
	wrapped := fmt.Sprintf("SELECT toJSONString(tuple(%s)) AS __loom_json FROM (%s) AS __loom_rows", strings.Join(quoted, ", "), base)
	rows, err := c.conn.Query(ctx, wrapped, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return fmt.Errorf("scan ClickHouse dataframe row: %w", err)
		}
		var values []any
		if err := json.Unmarshal([]byte(encoded), &values); err != nil {
			return fmt.Errorf("decode ClickHouse dataframe row: %w", err)
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			if i < len(values) {
				row[column] = values[i]
			}
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func parseOptions(opts Options) (*ch.Options, error) {
	parsed, err := ch.ParseDSN(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("parse ClickHouse URL: %w", err)
	}
	parsed.Auth.Database = opts.Database
	parsed.Auth.Username = opts.Username
	parsed.Auth.Password = opts.Password
	parsed.DialTimeout = opts.Timeout
	parsed.ReadTimeout = opts.Timeout
	// Native JSON columns are used for logical object-valued dataframe fields.
	// Keep the setting on every connection so DDL and native batch inserts work
	// consistently against ClickHouse 25.3 deployments.
	if parsed.Settings == nil {
		parsed.Settings = ch.Settings{}
	}
	parsed.Settings["allow_experimental_json_type"] = true
	return parsed, nil
}

func validateIdentifier(value string) error {
	if !identifierRE.MatchString(value) {
		return fmt.Errorf("invalid ClickHouse identifier %q", value)
	}
	return nil
}
