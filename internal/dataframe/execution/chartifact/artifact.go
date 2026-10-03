// Package chartifact manages private ClickHouse tables used to cross an
// execution boundary. These artifacts are never registered as published
// outputs and never advance a visibility pointer.
package chartifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

type State string

const (
	StateCreating       State = "CREATING"
	StateWriting        State = "WRITING"
	StateReady          State = "READY"
	StateCleanupPending State = "CLEANUP_PENDING"

	rowIDColumn        = "__loom_row_id"
	authPathColumn     = "auth_resource_path"
	projectIDColumn    = "project_id"
	defaultTablePrefix = "loom_private_stage"
)

var (
	ErrLeaseLost = errors.New("private ClickHouse artifact lease lost")
	identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Identity binds an intermediate table to the exact source execution and
// compiler result that created it. SchemaDigest and ScopeDigest are computed
// by Begin; supplied values, when present, must match.
type Identity struct {
	ExecutionID       string
	StageID           string
	Project           string
	DatasetGeneration string
	RecipeDigest      string
	PlanDigest        string
	SchemaDigest      string
	ScopeDigest       string
	AuthScopeMode     authscope.ReadScopeMode
	AuthResourcePaths []string
}

// Column records a stable compiler ID alongside logical and physical types.
// The physical type is restricted to the supported scalar storage types.
type Column struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name"`
	SemanticPath   string `json:"semanticPath,omitempty"`
	LogicalType    string `json:"logicalType"`
	ClickHouseType string `json:"clickhouseType"`
	Nullable       bool   `json:"nullable,omitempty"`
	Repeated       bool   `json:"repeated,omitempty"`
	Internal       bool   `json:"internal,omitempty"`
	NormalizedUnit string `json:"normalizedUnit,omitempty"`
}

// Manifest is the durable identity and cleanup record for one private table.
// Catalog implementations must treat Identity, Columns, and PhysicalTable as
// immutable after Create.
type Manifest struct {
	ArtifactID    string    `json:"artifactId"`
	Identity      Identity  `json:"identity"`
	Columns       []Column  `json:"columns"`
	PhysicalTable string    `json:"physicalTable"`
	State         State     `json:"state"`
	RowCount      int64     `json:"rowCount"`
	ByteCount     int64     `json:"byteCount"`
	LeaseOwner    string    `json:"leaseOwner"`
	LeaseUntil    time.Time `json:"leaseUntil"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Progress is the only mutable portion of a manifest. Catalog.Update must
// compare the current lease owner with owner and must not replace immutable
// identity or schema fields. For active states it may extend LeaseUntil but
// must not shorten it; CLEANUP_PENDING may make the lease immediately
// reclaimable after the owner has stopped using the table.
type Progress struct {
	State      State
	RowCount   int64
	ByteCount  int64
	LeaseUntil time.Time
	UpdatedAt  time.Time
}

// Catalog is the durable manifest and lease boundary. Create must be
// conditional on ArtifactID being absent. Update and Renew must compare the
// owner and reject expired leases; Renew must not resurrect an expired lease.
// Update must preserve immutable identity, schema, and table fields. ClaimCleanup
// must atomically compare LeaseUntil with expiredBefore, change the owner, and
// set the cleanup lease; only one caller may win. Delete and ReleaseCleanup
// must verify the current owner.
type Catalog interface {
	Create(context.Context, Manifest) error
	Update(context.Context, string, string, Progress) error
	Renew(context.Context, string, string, time.Time) (bool, error)
	ListExpired(context.Context, time.Time, int) ([]Manifest, error)
	ClaimCleanup(context.Context, string, string, time.Time, time.Time) (Manifest, bool, error)
	Delete(context.Context, string, string) error
	ReleaseCleanup(context.Context, string, string, time.Time) error
}

// ClickHouse is the narrow private-table contract. The production adapter can
// be the existing typed client; tests use a fake and do not need a server.
// InsertRows must consume its bounded batch before returning, and DropTable
// must be idempotent.
type ClickHouse interface {
	CreateTable(context.Context, string, []clickhouse.Column) error
	InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error
	VerifyOutput(context.Context, string, []clickhouse.Column, int64) error
	DropTable(context.Context, string) error
}

type Config struct {
	Catalog     Catalog
	ClickHouse  ClickHouse
	TablePrefix string
	BatchRows   int
	BatchBytes  int
	LeaseTTL    time.Duration
}

type Manager struct {
	catalog    Catalog
	clickHouse ClickHouse
	prefix     string
	batchRows  int
	batchBytes int
	leaseTTL   time.Duration
}

func New(cfg Config) (*Manager, error) {
	if cfg.Catalog == nil || cfg.ClickHouse == nil {
		return nil, fmt.Errorf("private artifact catalog and ClickHouse store are required")
	}
	prefix := strings.TrimSpace(cfg.TablePrefix)
	if prefix == "" {
		prefix = defaultTablePrefix
	}
	if !identifierRE.MatchString(prefix) || !strings.HasPrefix(prefix, "loom_private_") {
		return nil, fmt.Errorf("private artifact table prefix must be a safe loom_private_ identifier")
	}
	if cfg.BatchRows <= 0 {
		cfg.BatchRows = 1000
	}
	if cfg.BatchBytes <= 0 {
		cfg.BatchBytes = 4 << 20
	}
	if cfg.LeaseTTL == 0 {
		cfg.LeaseTTL = 2 * time.Minute
	}
	if cfg.LeaseTTL < 3*time.Second {
		return nil, fmt.Errorf("private artifact lease TTL must be at least three seconds")
	}
	return &Manager{
		catalog: cfg.Catalog, clickHouse: cfg.ClickHouse, prefix: prefix,
		batchRows: cfg.BatchRows, batchBytes: cfg.BatchBytes, leaseTTL: cfg.LeaseTTL,
	}, nil
}

// NormalizeSchema appends the Loom-owned identity and scope columns required
// by a private intermediate artifact. Conflicting uses of reserved names fail.
func NormalizeSchema(columns []Column) ([]Column, error) {
	var rowID *Column
	var authPath *Column
	var projectID *Column
	public := make([]Column, 0, len(columns))
	seenNames, seenIDs := map[string]bool{}, map[string]bool{}
	for _, input := range columns {
		column := input
		if strings.TrimSpace(column.Name) == "" || column.Name != strings.TrimSpace(column.Name) || !identifierRE.MatchString(column.Name) {
			return nil, fmt.Errorf("artifact column name %q is empty, untrimmed, or unsafe", column.Name)
		}
		if column.Name == rowIDColumn {
			if rowID != nil || !column.Internal || column.Repeated || column.Nullable || !logicalTypeIsString(column.LogicalType) {
				return nil, fmt.Errorf("artifact %s must be one non-null internal string identity column", rowIDColumn)
			}
			column.ClickHouseType = "String"
			column.SemanticPath = "loom:row_id"
			if column.ID == "" {
				column.ID = "loom:row_id"
			}
			rowID = &column
			continue
		}
		if column.Name == authPathColumn || column.Name == projectIDColumn {
			if !column.Internal || !logicalTypeIsString(column.LogicalType) || column.Repeated {
				return nil, fmt.Errorf("artifact column %q is Loom-owned", column.Name)
			}
			switch column.Name {
			case authPathColumn:
				if authPath != nil || !column.Nullable || (column.ClickHouseType != "" && column.ClickHouseType != "Nullable(String)") {
					return nil, fmt.Errorf("artifact %s must be one nullable internal string column", authPathColumn)
				}
				column.ID, column.ClickHouseType = "loom:auth_resource_path", "Nullable(String)"
				authPath = &column
			case projectIDColumn:
				if projectID != nil || column.Nullable || (column.ClickHouseType != "" && column.ClickHouseType != "String") {
					return nil, fmt.Errorf("artifact %s must be one non-null internal string column", projectIDColumn)
				}
				column.ID, column.ClickHouseType = "loom:project_id", "String"
				projectID = &column
			}
			continue
		}
		if column.Internal {
			return nil, fmt.Errorf("unsupported internal artifact column %q", column.Name)
		}
		if strings.TrimSpace(column.ID) == "" || column.ID != strings.TrimSpace(column.ID) {
			return nil, fmt.Errorf("artifact column %q requires a trimmed stable ID", column.Name)
		}
		if seenNames[column.Name] || seenIDs[column.ID] {
			return nil, fmt.Errorf("artifact column %q has a duplicate name or stable ID", column.Name)
		}
		seenNames[column.Name], seenIDs[column.ID] = true, true
		physical, err := physicalType(column.LogicalType, column.Nullable, column.Repeated)
		if err != nil {
			return nil, fmt.Errorf("artifact column %q: %w", column.Name, err)
		}
		if column.ClickHouseType != "" && column.ClickHouseType != physical {
			return nil, fmt.Errorf("artifact column %q ClickHouse type %q does not match compiler type %q", column.Name, column.ClickHouseType, physical)
		}
		column.ClickHouseType = physical
		public = append(public, column)
	}
	if rowID == nil {
		column := Column{ID: "loom:row_id", Name: rowIDColumn, SemanticPath: "loom:row_id", LogicalType: "string", ClickHouseType: "String", Internal: true}
		rowID = &column
	}
	result := make([]Column, 0, len(public)+3)
	result = append(result, *rowID)
	result = append(result, public...)
	if authPath == nil {
		column := Column{ID: "loom:auth_resource_path", Name: authPathColumn, LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true, Internal: true}
		authPath = &column
	}
	if projectID == nil {
		column := Column{ID: "loom:project_id", Name: projectIDColumn, LogicalType: "string", ClickHouseType: "String", Internal: true}
		projectID = &column
	}
	result = append(result, *authPath, *projectID)
	allNames, allIDs := map[string]bool{}, map[string]bool{}
	for _, column := range result {
		if allNames[column.Name] || allIDs[column.ID] {
			return nil, fmt.Errorf("artifact schema has a duplicate column name or stable ID")
		}
		allNames[column.Name], allIDs[column.ID] = true, true
	}
	return result, nil
}

// ColumnsFromCompiledOutput converts the compiler's finalized output schema
// into the physical scalar schema used at the private engine boundary. Hidden
// compiler helpers are omitted; the stable row identity is always retained.
func ColumnsFromCompiledOutput(schema []lower.CompiledOutputColumn) ([]Column, error) {
	columns := make([]Column, 0, len(schema))
	rowIDSeen := false
	for _, compiled := range schema {
		if compiled.Identity || compiled.Name == rowIDColumn {
			if rowIDSeen || compiled.Name != rowIDColumn {
				return nil, fmt.Errorf("compiled artifact schema has an invalid row identity column")
			}
			if (compiled.Kind != "" && !strings.EqualFold(compiled.Kind, "string")) || compiled.Nullable || (compiled.Cardinality != "" && compiled.Cardinality != string(expression.RequiredOne)) {
				return nil, fmt.Errorf("compiled artifact row identity must be a required scalar string")
			}
			rowIDSeen = true
			columns = append(columns, Column{ID: compiled.ID, Name: rowIDColumn, SemanticPath: "loom:row_id", LogicalType: "string", Internal: true})
			continue
		}
		if compiled.Internal {
			continue
		}
		switch compiled.Cardinality {
		case string(expression.RequiredOne), string(expression.OptionalOne), string(expression.Many):
		default:
			return nil, fmt.Errorf("compiled artifact column %q has unsupported cardinality %q", compiled.Name, compiled.Cardinality)
		}
		column := Column{
			ID: compiled.ID, Name: compiled.Name, SemanticPath: compiled.SemanticPath,
			LogicalType: compiled.Kind, Nullable: compiled.Nullable || compiled.Cardinality == string(expression.OptionalOne),
			Repeated: compiled.Cardinality == string(expression.Many),
		}
		if compiled.NormalizedUnit != nil {
			encoded, err := json.Marshal(compiled.NormalizedUnit)
			if err != nil {
				return nil, fmt.Errorf("encode normalized unit for column %q: %w", compiled.Name, err)
			}
			column.NormalizedUnit = string(encoded)
		}
		columns = append(columns, column)
	}
	if !rowIDSeen {
		columns = append(columns, Column{ID: "loom:row_id", Name: rowIDColumn, SemanticPath: "loom:row_id", LogicalType: "string", Internal: true})
	}
	return NormalizeSchema(columns)
}

func physicalType(logical string, nullable, repeated bool) (string, error) {
	base := ""
	switch strings.ToLower(strings.TrimSpace(logical)) {
	case "string", "code":
		base = "String"
	case "uuid":
		base = "UUID"
	case "date":
		base = "Date"
	case "date-time", "date_time", "datetime":
		base = "DateTime64(3)"
	case "integer":
		base = "Int64"
	case "decimal":
		base = "Float64"
	case "boolean":
		base = "Bool"
	default:
		return "", fmt.Errorf("unsupported logical type %q", logical)
	}
	if repeated {
		base = "Array(" + base + ")"
	} else if nullable {
		base = "Nullable(" + base + ")"
	}
	return base, nil
}

func logicalTypeIsString(logical string) bool {
	return strings.EqualFold(strings.TrimSpace(logical), "string")
}

func scopeDigest(identity Identity) string {
	paths := append([]string(nil), identity.AuthResourcePaths...)
	sort.Strings(paths)
	data := identity.Project + "\x00" + identity.DatasetGeneration + "\x00" + string(identity.AuthScopeMode) + "\x00" + strings.Join(paths, "\x00")
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func schemaDigest(stageID string, columns []Column) (string, error) {
	data, err := json.Marshal(struct {
		Version int      `json:"version"`
		StageID string   `json:"stageId"`
		Columns []Column `json:"columns"`
	}{1, stageID, columns})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// SchemaDigest returns the digest of a normalized compiler stage schema.
func SchemaDigest(stageID string, columns []Column) (string, error) {
	normalized, err := NormalizeSchema(columns)
	if err != nil {
		return "", err
	}
	return schemaDigest(stageID, normalized)
}

func (m *Manager) Begin(ctx context.Context, identity Identity, columns []Column) (*Writer, error) {
	if ctx == nil {
		return nil, fmt.Errorf("private artifact context is required")
	}
	identity = cloneIdentity(identity)
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	normalized, err := NormalizeSchema(columns)
	if err != nil {
		return nil, err
	}
	computedSchemaDigest, err := schemaDigest(identity.StageID, normalized)
	if err != nil {
		return nil, fmt.Errorf("digest private artifact schema: %w", err)
	}
	if identity.SchemaDigest != "" && identity.SchemaDigest != computedSchemaDigest {
		return nil, fmt.Errorf("private artifact schema digest does not match compiler schema")
	}
	identity.SchemaDigest = computedSchemaDigest
	computedScopeDigest := scopeDigest(identity)
	if identity.ScopeDigest != "" && identity.ScopeDigest != computedScopeDigest {
		return nil, fmt.Errorf("private artifact scope digest does not match exact scope")
	}
	identity.ScopeDigest = computedScopeDigest
	artifactID := uuid.NewString()
	tableSuffix := strings.ReplaceAll(artifactID, "-", "")
	physicalTable := m.prefix + "_" + tableSuffix
	owner := "stage-" + uuid.NewString()
	now := time.Now().UTC()
	manifest := Manifest{
		ArtifactID: artifactID, Identity: identity, Columns: cloneColumns(normalized), PhysicalTable: physicalTable,
		State: StateCreating, LeaseOwner: owner, LeaseUntil: now.Add(m.leaseTTL), CreatedAt: now, UpdatedAt: now,
	}
	if err := m.catalog.Create(ctx, cloneManifest(manifest)); err != nil {
		return nil, fmt.Errorf("record private artifact manifest: %w", err)
	}
	lease := startLease(m.catalog, artifactID, owner, m.leaseTTL)
	writer := &Writer{
		manager: m, manifest: manifest, owner: owner, lease: lease,
		columns: toClickHouseColumns(normalized), batch: make([]map[string]any, 0, m.batchRows),
	}
	if err := m.clickHouse.CreateTable(ctx, physicalTable, writer.columns); err != nil {
		cleanupErr := writer.cleanupOwned(ctx)
		return nil, errors.Join(fmt.Errorf("create private ClickHouse artifact table: %w", err), cleanupErr)
	}
	manifest.State = StateWriting
	manifest.UpdatedAt = time.Now().UTC()
	manifest.LeaseUntil = manifest.UpdatedAt.Add(m.leaseTTL)
	if err := m.catalog.Update(ctx, artifactID, owner, Progress{State: manifest.State, LeaseUntil: manifest.LeaseUntil, UpdatedAt: manifest.UpdatedAt}); err != nil {
		cleanupErr := writer.cleanupOwned(ctx)
		return nil, errors.Join(fmt.Errorf("record private artifact table: %w", err), cleanupErr)
	}
	writer.manifest = manifest
	return writer, nil
}

func validateIdentity(identity Identity) error {
	for name, value := range map[string]string{
		"execution ID": identity.ExecutionID, "stage ID": identity.StageID, "project": identity.Project,
		"dataset generation": identity.DatasetGeneration, "recipe digest": identity.RecipeDigest, "plan digest": identity.PlanDigest,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("private artifact %s is required and must be trimmed", name)
		}
	}
	switch identity.AuthScopeMode {
	case authscope.ReadScopeUnrestricted:
		if len(identity.AuthResourcePaths) != 0 {
			return fmt.Errorf("unrestricted private artifact scope cannot carry resource paths")
		}
	case authscope.ReadScopeRestricted:
		if len(identity.AuthResourcePaths) == 0 {
			return fmt.Errorf("restricted private artifact scope cannot be empty")
		}
		seen := make(map[string]bool, len(identity.AuthResourcePaths))
		for _, path := range identity.AuthResourcePaths {
			if strings.TrimSpace(path) == "" || path != strings.TrimSpace(path) || seen[path] {
				return fmt.Errorf("restricted private artifact scope contains an invalid or duplicate resource path")
			}
			seen[path] = true
		}
	default:
		return fmt.Errorf("private artifact requires an explicit authorization scope mode")
	}
	return nil
}

type Writer struct {
	manager    *Manager
	manifest   Manifest
	owner      string
	lease      *leaseKeeper
	columns    []clickhouse.Column
	batch      []map[string]any
	batchBytes int
	rowCount   int64
	byteCount  int64
	closed     bool
	mu         sync.Mutex
}

// Write validates and buffers one row. The writer retains a defensive copy;
// the caller may reuse the supplied map as soon as Write returns.
func (w *Writer) Write(ctx context.Context, input map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("private artifact writer is closed")
	}
	if err := w.lease.check(); err != nil {
		return err
	}
	if ctx == nil {
		return fmt.Errorf("private artifact write context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	row, size, err := w.validateRow(input)
	if err != nil {
		return err
	}
	if size > w.manager.batchBytes {
		return fmt.Errorf("private artifact row exceeds batch byte limit %d", w.manager.batchBytes)
	}
	if len(w.batch) > 0 && (len(w.batch) >= w.manager.batchRows || w.batchBytes+size > w.manager.batchBytes) {
		if err := w.flush(ctx); err != nil {
			return err
		}
	}
	w.batch = append(w.batch, row)
	w.batchBytes += size
	w.rowCount++
	w.byteCount += int64(size)
	if len(w.batch) >= w.manager.batchRows || w.batchBytes >= w.manager.batchBytes {
		return w.flush(ctx)
	}
	return nil
}

func (w *Writer) validateRow(input map[string]any) (map[string]any, int, error) {
	if input == nil {
		return nil, 0, fmt.Errorf("private artifact row is nil")
	}
	row := make(map[string]any, len(w.manifest.Columns))
	known := make(map[string]Column, len(w.manifest.Columns))
	for _, column := range w.manifest.Columns {
		known[column.Name] = column
	}
	for name, value := range input {
		if name == projectIDColumn {
			continue
		}
		if _, ok := known[name]; !ok {
			return nil, 0, fmt.Errorf("private artifact row has unknown column %q", name)
		}
		row[name] = cloneValue(value)
	}
	rowID, ok := row[rowIDColumn].(string)
	if !ok || strings.TrimSpace(rowID) == "" {
		return nil, 0, fmt.Errorf("private artifact row is missing its stable string row identity")
	}
	if _, ok := row[authPathColumn]; !ok {
		if w.manifest.Identity.AuthScopeMode == authscope.ReadScopeRestricted {
			return nil, 0, fmt.Errorf("restricted private artifact row is missing auth_resource_path")
		}
		row[authPathColumn] = ""
	}
	path, ok := row[authPathColumn].(string)
	if !ok {
		return nil, 0, fmt.Errorf("private artifact auth_resource_path must be a string")
	}
	if w.manifest.Identity.AuthScopeMode == authscope.ReadScopeRestricted {
		if path == "" || !contains(w.manifest.Identity.AuthResourcePaths, path) {
			return nil, 0, fmt.Errorf("private artifact row authorization path is outside the exact restricted scope")
		}
	}
	// project_id is Loom-owned metadata; never trust a source row value.
	row[projectIDColumn] = w.manifest.Identity.Project
	for _, column := range w.manifest.Columns {
		value, exists := row[column.Name]
		if !exists {
			if !column.Nullable {
				return nil, 0, fmt.Errorf("private artifact row is missing required column %q", column.Name)
			}
			row[column.Name] = nil
			continue
		}
		if value == nil && !column.Nullable {
			return nil, 0, fmt.Errorf("private artifact row column %q is unexpectedly null", column.Name)
		}
		if value != nil {
			if err := validateColumnValue(column, value); err != nil {
				return nil, 0, fmt.Errorf("private artifact row column %q: %w", column.Name, err)
			}
		}
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return nil, 0, fmt.Errorf("encode private artifact row size: %w", err)
	}
	return row, len(encoded), nil
}

func validateColumnValue(column Column, value any) error {
	physical := column.ClickHouseType
	if strings.HasPrefix(physical, "Nullable(") && strings.HasSuffix(physical, ")") {
		physical = strings.TrimSuffix(strings.TrimPrefix(physical, "Nullable("), ")")
	}
	if strings.HasPrefix(physical, "Array(") && strings.HasSuffix(physical, ")") {
		items := reflect.ValueOf(value)
		if items.Kind() != reflect.Array && items.Kind() != reflect.Slice {
			return fmt.Errorf("expects an array, got %T", value)
		}
		itemType := strings.TrimSuffix(strings.TrimPrefix(physical, "Array("), ")")
		itemColumn := column
		itemColumn.ClickHouseType = itemType
		itemColumn.Nullable = false
		for index := 0; index < items.Len(); index++ {
			item := items.Index(index).Interface()
			if item == nil {
				return fmt.Errorf("array item %d is null", index)
			}
			if err := validateColumnValue(itemColumn, item); err != nil {
				return fmt.Errorf("array item %d: %w", index, err)
			}
		}
		return nil
	}
	switch physical {
	case "String", "UUID", "Date", "DateTime64(3)":
		switch value.(type) {
		case string, time.Time:
			return nil
		default:
			return fmt.Errorf("expects %s, got %T", physical, value)
		}
	case "Bool":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expects Bool, got %T", value)
		}
	case "Int64":
		if !isInt64Value(value) {
			return fmt.Errorf("expects Int64, got %T", value)
		}
	case "Float64":
		if !isFiniteNumberValue(value) {
			return fmt.Errorf("expects Float64, got %T", value)
		}
	default:
		return fmt.Errorf("has unsupported physical type %q", physical)
	}
	return nil
}

func isInt64Value(value any) bool {
	switch typed := value.(type) {
	case int, int8, int16, int32, int64, uint8, uint16, uint32:
		return true
	case uint:
		return uint64(typed) <= math.MaxInt64
	case uint64:
		return typed <= math.MaxInt64
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0) && math.Trunc(typed) == typed && typed >= math.MinInt64 && typed < math.MaxInt64
	default:
		return false
	}
}

func isFiniteNumberValue(value any) bool {
	switch typed := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return !math.IsNaN(float64(typed)) && !math.IsInf(float64(typed), 0)
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0)
	default:
		return false
	}
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(typed))
		for key, item := range typed {
			copy[key] = cloneValue(item)
		}
		return copy
	case []any:
		copy := make([]any, len(typed))
		for index, item := range typed {
			copy[index] = cloneValue(item)
		}
		return copy
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

func (w *Writer) flush(ctx context.Context) error {
	if len(w.batch) == 0 {
		return nil
	}
	if err := w.lease.check(); err != nil {
		return err
	}
	if err := w.manager.clickHouse.InsertRows(ctx, w.manifest.PhysicalTable, w.columns, w.batch); err != nil {
		return fmt.Errorf("write private ClickHouse artifact batch: %w", err)
	}
	progress := Progress{
		State: StateWriting, RowCount: w.rowCount, ByteCount: w.byteCount,
		LeaseUntil: time.Now().UTC().Add(w.manager.leaseTTL), UpdatedAt: time.Now().UTC(),
	}
	if err := w.manager.catalog.Update(ctx, w.manifest.ArtifactID, w.owner, progress); err != nil {
		return fmt.Errorf("record private artifact batch progress: %w", err)
	}
	w.manifest.State, w.manifest.RowCount, w.manifest.ByteCount = progress.State, progress.RowCount, progress.ByteCount
	w.manifest.LeaseUntil, w.manifest.UpdatedAt = progress.LeaseUntil, progress.UpdatedAt
	w.batch = w.batch[:0]
	w.batchBytes = 0
	return nil
}

// Finalize flushes and verifies the table before making it available to a
// later typed ClickHouse stage. Its lease remains active until Release.
func (w *Writer) Finalize(ctx context.Context) (*Artifact, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, fmt.Errorf("private artifact writer is closed")
	}
	if err := w.lease.check(); err != nil {
		return nil, err
	}
	if err := w.flush(ctx); err != nil {
		return nil, err
	}
	if err := w.manager.clickHouse.VerifyOutput(ctx, w.manifest.PhysicalTable, w.columns, w.rowCount); err != nil {
		return nil, fmt.Errorf("verify private ClickHouse artifact: %w", err)
	}
	progress := Progress{
		State: StateReady, RowCount: w.rowCount, ByteCount: w.byteCount,
		LeaseUntil: time.Now().UTC().Add(w.manager.leaseTTL), UpdatedAt: time.Now().UTC(),
	}
	if err := w.manager.catalog.Update(ctx, w.manifest.ArtifactID, w.owner, progress); err != nil {
		return nil, fmt.Errorf("mark private ClickHouse artifact ready: %w", err)
	}
	w.manifest.State, w.manifest.RowCount, w.manifest.ByteCount = progress.State, progress.RowCount, progress.ByteCount
	w.manifest.LeaseUntil, w.manifest.UpdatedAt = progress.LeaseUntil, progress.UpdatedAt
	w.closed = true
	return &Artifact{manager: w.manager, manifest: cloneManifest(w.manifest), owner: w.owner, lease: w.lease}, nil
}

// Abort removes a partially written artifact. If ClickHouse cannot drop the
// table, the cleanup-pending manifest remains for Reconcile.
func (w *Writer) Abort(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.cleanupOwned(ctx)
}

func (w *Writer) cleanupOwned(ctx context.Context) error {
	w.lease.stop()
	cleanupCtx, cancel := boundedCleanupContext(ctx)
	defer cancel()
	progress := Progress{State: StateCleanupPending, RowCount: w.rowCount, ByteCount: w.byteCount, LeaseUntil: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := w.manager.catalog.Update(cleanupCtx, w.manifest.ArtifactID, w.owner, progress); err != nil {
		return fmt.Errorf("mark private artifact for cleanup: %w", err)
	}
	if err := w.manager.clickHouse.DropTable(cleanupCtx, w.manifest.PhysicalTable); err != nil {
		updateErr := w.manager.catalog.Update(cleanupCtx, w.manifest.ArtifactID, w.owner, progress)
		return errors.Join(fmt.Errorf("drop private ClickHouse artifact: %w", err), updateErr)
	}
	if err := w.manager.catalog.Delete(cleanupCtx, w.manifest.ArtifactID, w.owner); err != nil {
		return fmt.Errorf("delete private artifact manifest after table drop: %w", err)
	}
	return nil
}

type Artifact struct {
	manager    *Manager
	manifest   Manifest
	owner      string
	lease      *leaseKeeper
	release    sync.Once
	releaseErr error
}

func (a *Artifact) Manifest() Manifest {
	if a == nil {
		return Manifest{}
	}
	return cloneManifest(a.manifest)
}

func (a *Artifact) CheckLease() error {
	if a == nil || a.lease == nil {
		return fmt.Errorf("private ClickHouse artifact lease is unavailable")
	}
	return a.lease.check()
}

// LeaseContext cancels ctx if the durable cleanup lease is lost. A streaming
// ClickHouse query should use this context until it stops reading the table.
func (a *Artifact) LeaseContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	if a == nil || a.lease == nil {
		cancel()
		return leaseCtx, cancel
	}
	go func() {
		select {
		case <-a.lease.lost:
			cancel()
		case <-leaseCtx.Done():
		}
	}()
	return leaseCtx, cancel
}

// Release drops the table and removes its manifest. It is safe to call more
// than once. A failed drop leaves an expired cleanup-pending manifest.
func (a *Artifact) Release(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.release.Do(func() {
		a.lease.stop()
		cleanupCtx, cancel := boundedCleanupContext(ctx)
		defer cancel()
		manifest := a.manifest
		progress := Progress{State: StateCleanupPending, RowCount: manifest.RowCount, ByteCount: manifest.ByteCount, LeaseUntil: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := a.manager.catalog.Update(cleanupCtx, manifest.ArtifactID, a.owner, progress); err != nil {
			a.releaseErr = fmt.Errorf("mark private artifact for cleanup: %w", err)
			return
		}
		if err := a.manager.clickHouse.DropTable(cleanupCtx, manifest.PhysicalTable); err != nil {
			updateErr := a.manager.catalog.Update(cleanupCtx, manifest.ArtifactID, a.owner, progress)
			a.releaseErr = errors.Join(fmt.Errorf("drop private ClickHouse artifact: %w", err), updateErr)
			return
		}
		if err := a.manager.catalog.Delete(cleanupCtx, manifest.ArtifactID, a.owner); err != nil {
			a.releaseErr = fmt.Errorf("delete private artifact manifest after table drop: %w", err)
		}
	})
	return a.releaseErr
}

// Reconcile claims and removes artifacts whose execution leases expired. It
// can be run repeatedly and is safe when multiple service replicas race.
func (m *Manager) Reconcile(ctx context.Context, now time.Time, limit int) error {
	if ctx == nil {
		return fmt.Errorf("private artifact reconciliation context is required")
	}
	if limit <= 0 {
		limit = 100
	}
	manifests, err := m.catalog.ListExpired(ctx, now, limit)
	if err != nil {
		return fmt.Errorf("list expired private artifacts: %w", err)
	}
	var first error
	for _, candidate := range manifests {
		owner := "cleanup-" + uuid.NewString()
		claimed, ok, err := m.catalog.ClaimCleanup(ctx, candidate.ArtifactID, owner, now, now.Add(m.leaseTTL))
		if err != nil {
			first = errors.Join(first, fmt.Errorf("claim private artifact %q cleanup: %w", candidate.ArtifactID, err))
			continue
		}
		if !ok {
			continue
		}
		cleanupCtx, cancel := boundedCleanupContext(ctx)
		if !m.validTableName(claimed.PhysicalTable) {
			_ = m.catalog.ReleaseCleanup(cleanupCtx, claimed.ArtifactID, owner, now)
			first = errors.Join(first, fmt.Errorf("private artifact %q has an unsafe table name", claimed.ArtifactID))
			cancel()
			continue
		}
		if err := m.clickHouse.DropTable(cleanupCtx, claimed.PhysicalTable); err != nil {
			releaseErr := m.catalog.ReleaseCleanup(cleanupCtx, claimed.ArtifactID, owner, now)
			first = errors.Join(first, fmt.Errorf("reconcile private artifact %q table: %w", claimed.ArtifactID, err), releaseErr)
			cancel()
			continue
		}
		if err := m.catalog.Delete(cleanupCtx, claimed.ArtifactID, owner); err != nil {
			first = errors.Join(first, fmt.Errorf("reconcile private artifact %q manifest: %w", claimed.ArtifactID, err))
		}
		cancel()
	}
	return first
}

func (m *Manager) validTableName(table string) bool {
	return identifierRE.MatchString(table) && strings.HasPrefix(table, m.prefix+"_")
}

type leaseKeeper struct {
	catalog  Catalog
	id       string
	owner    string
	ttl      time.Duration
	stopCh   chan struct{}
	done     chan struct{}
	lost     chan struct{}
	stopOnce sync.Once
	lostOnce sync.Once
	mu       sync.Mutex
	err      error
}

func startLease(catalog Catalog, id, owner string, ttl time.Duration) *leaseKeeper {
	lease := &leaseKeeper{catalog: catalog, id: id, owner: owner, ttl: ttl, stopCh: make(chan struct{}), done: make(chan struct{}), lost: make(chan struct{})}
	go lease.run()
	return lease
}

func (l *leaseKeeper) run() {
	defer close(l.done)
	ticker := time.NewTicker(l.ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			owned, err := l.catalog.Renew(ctx, l.id, l.owner, time.Now().UTC().Add(l.ttl))
			cancel()
			if err != nil || !owned {
				if err == nil {
					err = ErrLeaseLost
				}
				l.mu.Lock()
				l.err = errors.Join(ErrLeaseLost, err)
				l.mu.Unlock()
				l.lostOnce.Do(func() { close(l.lost) })
				return
			}
		}
	}
}

func (l *leaseKeeper) check() error {
	if l == nil {
		return ErrLeaseLost
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *leaseKeeper) stop() {
	if l == nil {
		return
	}
	l.stopOnce.Do(func() { close(l.stopCh) })
	<-l.done
}

func toClickHouseColumns(columns []Column) []clickhouse.Column {
	result := make([]clickhouse.Column, len(columns))
	for index, column := range columns {
		result[index] = clickhouse.Column{Name: column.Name, Type: column.ClickHouseType}
	}
	return result
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func boundedCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

func cloneIdentity(identity Identity) Identity {
	identity.AuthResourcePaths = append([]string(nil), identity.AuthResourcePaths...)
	return identity
}

func cloneColumns(columns []Column) []Column { return append([]Column(nil), columns...) }

func cloneManifest(manifest Manifest) Manifest {
	manifest.Identity = cloneIdentity(manifest.Identity)
	manifest.Columns = cloneColumns(manifest.Columns)
	return manifest
}
