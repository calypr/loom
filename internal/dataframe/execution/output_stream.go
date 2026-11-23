package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	irscope "github.com/calypr/loom/internal/dataframe/compiler/ir/scope"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	rowlineage "github.com/calypr/loom/internal/dataframe/compiler/rowlineage"

	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/store/arango"
)

const (
	DefaultPreviewLimit = 25

	MaxPreviewLimit    = 1000
	previewPlanMode    = "physical"
	previewPlanProfile = "generic_fhir_graph_recipe"
)

type PreviewRequest struct {
	Output             string
	Limit              int
	IncludeRowIdentity bool
}

type PreviewSummary struct {
	Output           string
	Columns          []string
	RowCount         int
	PlanMode         string
	PlanProfile      string
	PlanFingerprint  string
	TraversalCount   int
	LoweringDuration time.Duration
	QueryDuration    time.Duration

	Complete  bool
	Truncated bool

	PartialValidation    bool
	RowLineageCapability rowlineage.RowLineageCapability
}

type OutputStream struct {
	Name                     string
	Columns                  []string
	sourceIdentityMode       string
	RowIdentity              *spec.RowIdentity
	DynamicChecks            map[string]map[string]DynamicColumnCheck
	query                    string
	bindVars                 map[string]any
	stream                   QueryRows
	batchSize                int
	rootPageRows             int
	page                     *compiler.CompiledOutputPage
	withPreviewReadSnapshot  WithPreviewReadSnapshot
	physicalEngine           ir.PhysicalEngine
	clickHouseCombine        *ir.PhysicalClickHouseCombine
	project                  string
	bindings                 recipe.RuntimeBindings
	clickHouseQueryRows      ClickHouseQueryRows
	resolveClickHouseInputs  ResolveClickHouseInputs
	withExecutionReadPins    WithExecutionReadPins
	queryLimit               int
	queryMemoryLimit         int64
	sampledAggregate         bool
	projectionDecisionKey    *groupPreviewRowsKey
	projectionDecision       *groupPreviewProjectionDecision
	recipeDigest             string
	planFingerprint          string
	stageID                  string
	outputSchema             []lower.CompiledOutputColumn
	compiledOutput           bool
	artifactScopeMode        string
	artifactScopeEvidence    string
	scopeEvidence            *irscope.WholeRelationScopeEvidence
	scopeEvidenceIdentity    *irscope.WholeRelationScopeIdentity
	workspaceArtifactManager *chartifact.Manager
	workspaceArtifactSources []PrivateClickHouseArtifactSource
	captureExecutionID       string
	physicalPlan             ir.PhysicalPlan
}

type DynamicColumnCheck struct {
	ColumnName       string
	ValueType        string
	Many             bool
	AllowUnknownKeys bool
}

type StreamResult struct {
	Output   string
	Columns  []string
	RowCount int
}

func selectedOutputNames(names []string, outputs []lower.CompiledRecipeOutput) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	known := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		known[output.Name] = false
	}
	for _, name := range names {
		if _, ok := known[name]; ok {
			known[name] = true
		}
	}
	return known
}

func publicPreviewRow(row map[string]any, columns []string, includeRowIdentity bool) map[string]any {
	public := make(map[string]any, len(columns))
	for _, column := range columns {
		if value, ok := row[column]; ok {
			public[column] = value
		}
	}
	if includeRowIdentity {
		if value, ok := row["__loom_row_id"]; ok {
			public["__loom_row_id"] = value
		}
	}
	return public
}

var errPreviewLimit = fmt.Errorf("preview limit reached")

func (s OutputStream) Stream(ctx context.Context, visit func(map[string]any) error) (StreamResult, error) {
	if visit == nil {
		return StreamResult{}, fmt.Errorf("row visitor is required")
	}
	count := 0
	err := s.streamRaw(ctx, func(row map[string]any) error {
		resolved, err := materializePostQueryRowWithChecks(row, s.DynamicChecks)
		if err != nil {
			return err
		}
		if err := ensureStableRowIdentity(resolved, s.RowIdentity, s.bindVars); err != nil {
			return err
		}
		resolved = publicStreamRow(resolved, s.Columns)
		count++
		return visit(resolved)
	})
	return StreamResult{Output: s.Name, Columns: append([]string(nil), s.Columns...), RowCount: count}, err
}

func (s OutputStream) streamRaw(ctx context.Context, visit func(map[string]any) error) error {
	if s.queryMemoryLimit > 0 {
		ctx = arango.WithQueryMemoryLimit(ctx, s.queryMemoryLimit)
	}
	if s.physicalEngine == ir.PhysicalEngineClickHouse {
		return s.withPreparedClickHouse(ctx, func(preparedCtx context.Context, prepared preparedClickHouseQuery) error {
			return s.clickHouseQueryRows(preparedCtx, prepared.Query, prepared.Columns, visit, prepared.Args...)
		})
	}
	if s.page == nil || s.rootPageRows == 0 {
		return s.stream(ctx, s.query, s.batchSize, s.bindVars, visit)
	}
	if len(s.page.RowsPartitions) > 0 && s.withPreviewReadSnapshot != nil {
		return s.withPreviewReadSnapshot(ctx, func(snapshotCtx context.Context, query QueryRows) error {
			return s.streamRootPages(snapshotCtx, query, true, visit)
		})
	}
	return s.streamRootPages(ctx, s.stream, false, visit)
}

func cloneBindVars(input map[string]any) map[string]any {
	clone := make(map[string]any, len(input))
	for key, value := range input {
		clone[key] = value
	}
	return clone
}

func publicStreamRow(row map[string]any, columns []string) map[string]any {
	public := make(map[string]any, len(columns)+2)
	for _, column := range columns {
		if value, ok := row[column]; ok {
			public[column] = value
		}
	}
	for _, column := range []string{"__loom_row_id", "auth_resource_path"} {
		if value, ok := row[column]; ok {
			public[column] = value
		}
	}
	return public
}

func ensureStableRowIdentity(row map[string]any, identity *spec.RowIdentity, bindVars map[string]any) error {
	if row == nil {
		return fmt.Errorf("row is nil")
	}
	if value, ok := row["__loom_row_id"]; ok && value != nil && fmt.Sprint(value) != "" {
		return nil
	}
	if identity == nil || len(identity.Fields) == 0 {
		return fmt.Errorf("compiled output is missing a stable row identity")
	}
	parts := make([]any, 0, len(identity.Fields))
	for _, field := range identity.Fields {
		value, ok := row[field]
		if !ok && bindVars != nil {
			value, ok = bindVars[field]
		}
		if !ok || value == nil {
			return fmt.Errorf("stable row identity field %q is missing", field)
		}
		parts = append(parts, value)
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return fmt.Errorf("encode stable row identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	row["__loom_row_id"] = hex.EncodeToString(digest[:])
	return nil
}

func dynamicChecks(metadata []lower.DynamicColumnMetadata) map[string]map[string]DynamicColumnCheck {
	checks := make(map[string]map[string]DynamicColumnCheck)
	for _, column := range metadata {
		if checks[column.DynamicName] == nil {
			checks[column.DynamicName] = map[string]DynamicColumnCheck{}
		}
		checks[column.DynamicName][column.SourceKey] = DynamicColumnCheck{ColumnName: column.Name, ValueType: column.ValueType, Many: column.Many, AllowUnknownKeys: column.AllowUnknownKeys}
	}
	return checks
}
