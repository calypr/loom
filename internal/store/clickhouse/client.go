// Package clickhouse is Loom's narrow typed boundary around the official
// ClickHouse Go driver. The materialization service never receives a raw SQL
// connection or a table name from the browser.
package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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

// InsertJSONEachRow is retained as a compatibility name for callers, but the
// implementation is now a native typed PrepareBatch. Values are appended in
// deterministic map-key order only for this low-level compatibility method;
// materialization uses InsertRows with the persisted schema order.
func (c *Client) InsertJSONEachRow(ctx context.Context, table string, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	columns := make([]string, 0, len(rows[0]))
	for name := range rows[0] {
		columns = append(columns, name)
	}
	sortStrings(columns)
	values := make([]Column, 0, len(columns))
	for _, name := range columns {
		values = append(values, Column{Name: name, Type: inferType(rows[0][name])})
	}
	return c.InsertRows(ctx, table, values, rows)
}

func inferType(value any) string {
	switch value.(type) {
	case bool:
		return "Bool"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "Int64"
	case float32, float64:
		return "Float64"
	case []string:
		return "Array(String)"
	case []int, []int8, []int16, []int32, []int64, []uint, []uint8, []uint16, []uint32, []uint64:
		return "Array(Int64)"
	case []float32, []float64:
		return "Array(Float64)"
	case []bool:
		return "Array(Bool)"
	default:
		return "String"
	}
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
			values[i] = row[column.Name]
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

// QueryRowsArgs executes a SELECT with driver-bound positional arguments and
// decodes each row through ClickHouse's native driver. Query-controlled values
// must be supplied as args rather than interpolated into query text.
func (c *Client) QueryRowsArgs(ctx context.Context, query string, columns []string, args ...any) ([]map[string]any, error) {
	if len(columns) == 0 {
		return nil, fmt.Errorf("ClickHouse query columns are required")
	}
	base := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), "FORMAT JSONEachRow"))
	quoted := make([]string, len(columns))
	for i, column := range columns {
		if err := validateIdentifier(column); err != nil {
			return nil, err
		}
		quoted[i] = fmt.Sprintf("`%s`", column)
	}
	wrapped := fmt.Sprintf("SELECT toJSONString(tuple(%s)) AS __loom_json FROM (%s) AS __loom_rows", strings.Join(quoted, ", "), base)
	rows, err := c.conn.Query(ctx, wrapped, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, fmt.Errorf("scan ClickHouse dataframe row: %w", err)
		}
		var values []any
		if err := json.Unmarshal([]byte(encoded), &values); err != nil {
			return nil, fmt.Errorf("decode ClickHouse dataframe row: %w", err)
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			if i < len(values) {
				row[column] = values[i]
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
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
	return parsed, nil
}

func validateIdentifier(value string) error {
	if !identifierRE.MatchString(value) {
		return fmt.Errorf("invalid ClickHouse identifier %q", value)
	}
	return nil
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
