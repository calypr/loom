package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

const postgreSQLExplainPrefix = "EXPLAIN (FORMAT JSON)\n"

const (
	postgresExplainMaxRawBytes = 4 << 20
	postgresExplainMaxNodes    = 512
	postgresExplainMaxDepth    = 64
)

var postgresExplainNodePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 ]{0,63}$`)

var (
	postgresExplainSequentialNodeTypes = map[string]bool{
		"Seq Scan":    true,
		"Sample Scan": true,
	}
	postgresExplainIndexNodeTypes = map[string]bool{
		"Index Scan":        true,
		"Index Only Scan":   true,
		"Bitmap Index Scan": true,
	}
)

type PostgreSQLPlanJSONExecutor func(ctx context.Context, command command.CompiledCommand) (json.RawMessage, error)

func BuildPostgreSQLExplainCommand(compiled compiler.CompiledQuery) (command.CompiledCommand, error) {
	sqlText, args, err := compiled.Command.PostgreSQLParameters()
	if err != nil {
		return command.CompiledCommand{}, err
	}
	explainCommand, err := command.NewPostgreSQLCommand(postgreSQLExplainPrefix+sqlText, args)
	if err != nil {
		return command.CompiledCommand{}, fmt.Errorf("tag EXPLAIN command: %w", err)
	}
	return explainCommand, nil
}

func PostgreSQLExplainPlan(ctx context.Context, executor PostgreSQLPlanJSONExecutor, compiled compiler.CompiledQuery) (PostgreSQLPlanAssessment, error) {
	if executor == nil {
		return PostgreSQLPlanAssessment{}, fmt.Errorf("live PostgreSQL Explain requires a plan JSON executor; PostgreSQL requests never fall back to Arango")
	}
	explainCommand, err := BuildPostgreSQLExplainCommand(compiled)
	if err != nil {
		return PostgreSQLPlanAssessment{}, err
	}
	raw, err := executor(ctx, explainCommand)
	if err != nil {
		return PostgreSQLPlanAssessment{}, postgreSQLExplainExecutorError(ctx, err)
	}
	assessment, err := sanitizePostgreSQLPlanJSON(raw)
	if err != nil {
		return PostgreSQLPlanAssessment{}, postgreSQLExplainShapeError(err)
	}
	return assessment, nil
}

func postgreSQLExplainExecutorError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeClientCanceled, "live PostgreSQL recipe Explain canceled")
	}
	if _, classified := dataframeerrors.AsUserError(err); classified {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "live PostgreSQL recipe Explain timed out", dataframeerrors.WithRetryable(true))
	}
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "live PostgreSQL recipe Explain failed", dataframeerrors.WithRetryable(true))
}

func postgreSQLExplainShapeError(err error) error {
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "PostgreSQL EXPLAIN returned an unexpected plan shape",
		dataframeerrors.WithRetryable(false),
		dataframeerrors.WithDetails(map[string]any{"backend": "postgresql", "stage": "plan-parse"}),
	)
}

type postgresExplainRawNode struct {
	NodeType    *string                  `json:"Node Type"`
	StartupCost *float64                 `json:"Startup Cost"`
	TotalCost   *float64                 `json:"Total Cost"`
	PlanRows    *float64                 `json:"Plan Rows"`
	PlanWidth   *float64                 `json:"Plan Width"`
	Plans       []postgresExplainRawNode `json:"Plans"`
}

func sanitizePostgreSQLPlanJSON(raw json.RawMessage) (PostgreSQLPlanAssessment, error) {
	if len(raw) > postgresExplainMaxRawBytes {
		return PostgreSQLPlanAssessment{}, fmt.Errorf("plan JSON exceeds %d bytes", postgresExplainMaxRawBytes)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return PostgreSQLPlanAssessment{}, errors.New("plan JSON is empty")
	}
	var entries []struct {
		Plan *postgresExplainRawNode `json:"Plan"`
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&entries); err != nil {
		return PostgreSQLPlanAssessment{}, fmt.Errorf("plan JSON is not a PostgreSQL EXPLAIN array: %v", err)
	}
	if err := decoder.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return PostgreSQLPlanAssessment{}, errors.New("plan JSON carries trailing documents")
	}
	if len(entries) != 1 {
		return PostgreSQLPlanAssessment{}, fmt.Errorf("plan JSON must hold exactly one statement plan, got %d", len(entries))
	}
	if entries[0].Plan == nil {
		return PostgreSQLPlanAssessment{}, errors.New("plan JSON entry has no Plan object")
	}
	builder := &postgresExplainTreeBuilder{}
	root, err := builder.build(*entries[0].Plan, 0, -1)
	if err != nil {
		return PostgreSQLPlanAssessment{}, err
	}
	assessment := PostgreSQLPlanAssessment{
		Backend:      string(command.CompiledBackendPostgreSQL),
		Nodes:        builder.nodes,
		FullScans:    builder.fullScans,
		IndexScans:   builder.indexScans,
		RootEstimate: root.Estimate,
	}
	return assessment, nil
}

type postgresExplainTreeBuilder struct {
	nodes      []PostgreSQLPlanNode
	fullScans  []PostgreSQLScanEvidence
	indexScans []PostgreSQLScanEvidence
}

func (b *postgresExplainTreeBuilder) build(raw postgresExplainRawNode, depth, parentOrdinal int) (PostgreSQLPlanNode, error) {
	if depth > postgresExplainMaxDepth {
		return PostgreSQLPlanNode{}, fmt.Errorf("plan tree exceeds %d levels of nesting", postgresExplainMaxDepth)
	}
	if len(b.nodes) >= postgresExplainMaxNodes {
		return PostgreSQLPlanNode{}, fmt.Errorf("plan tree exceeds %d nodes", postgresExplainMaxNodes)
	}
	if raw.NodeType == nil {
		return PostgreSQLPlanNode{}, errors.New("plan node has no Node Type")
	}
	nodeType := *raw.NodeType
	if !postgresExplainNodePattern.MatchString(nodeType) {
		return PostgreSQLPlanNode{}, errors.New("plan node reports a Node Type outside the PostgreSQL operator vocabulary")
	}
	estimate, err := rawEstimate(raw)
	if err != nil {
		return PostgreSQLPlanNode{}, fmt.Errorf("plan node %q: %v", nodeType, err)
	}
	ordinal := len(b.nodes)
	node := PostgreSQLPlanNode{Ordinal: ordinal, ParentOrdinal: parentOrdinal, Depth: depth, NodeType: nodeType, Estimate: estimate}
	b.nodes = append(b.nodes, node)
	switch {
	case postgresExplainSequentialNodeTypes[nodeType]:
		b.fullScans = append(b.fullScans, PostgreSQLScanEvidence{Ordinal: ordinal, NodeType: nodeType, ScanKind: PostgreSQLScanSequential})
	case postgresExplainIndexNodeTypes[nodeType]:
		b.indexScans = append(b.indexScans, PostgreSQLScanEvidence{Ordinal: ordinal, NodeType: nodeType, ScanKind: PostgreSQLScanIndex})
	}
	for _, child := range raw.Plans {
		if _, err := b.build(child, depth+1, ordinal); err != nil {
			return PostgreSQLPlanNode{}, err
		}
	}
	return node, nil
}

func rawEstimate(raw postgresExplainRawNode) (PostgreSQLPlanEstimate, error) {
	startup, err := requiredEstimate(raw.StartupCost, "Startup Cost")
	if err != nil {
		return PostgreSQLPlanEstimate{}, err
	}
	total, err := requiredEstimate(raw.TotalCost, "Total Cost")
	if err != nil {
		return PostgreSQLPlanEstimate{}, err
	}
	rows, err := requiredEstimate(raw.PlanRows, "Plan Rows")
	if err != nil {
		return PostgreSQLPlanEstimate{}, err
	}
	width, err := requiredEstimate(raw.PlanWidth, "Plan Width")
	if err != nil {
		return PostgreSQLPlanEstimate{}, err
	}
	if math.IsNaN(startup) || math.IsInf(startup, 0) || math.IsNaN(total) || math.IsInf(total, 0) ||
		math.IsNaN(rows) || math.IsInf(rows, 0) || math.IsNaN(width) || math.IsInf(width, 0) {
		return PostgreSQLPlanEstimate{}, errors.New("plan estimates must be finite")
	}
	if startup < 0 || total < 0 || rows < 0 || width < 0 {
		return PostgreSQLPlanEstimate{}, errors.New("plan estimates must be non-negative")
	}
	if width != math.Trunc(width) {
		return PostgreSQLPlanEstimate{}, errors.New("Plan Width must be a whole number")
	}
	return PostgreSQLPlanEstimate{StartupCost: startup, TotalCost: total, PlanRows: rows, PlanWidth: int(width)}, nil
}

func requiredEstimate(value *float64, field string) (float64, error) {
	if value == nil {
		return 0, fmt.Errorf("plan node is missing %s", field)
	}
	return *value, nil
}

func (c Control) explainCompiledForOutput(ctx context.Context, resolved Resolved, output lower.CompiledRecipeOutput, limit int) (compiler.CompiledQuery, error) {
	if output.Plan.Engine != "" && output.Plan.Engine != ir.PhysicalEngineAQL && output.Plan.Engine != ir.PhysicalEngineClickHouse {
		return compiler.CompiledQuery{}, fmt.Errorf("output %q has unsupported physical engine %q", output.Name, output.Plan.Engine)
	}
	bindings := resolved.Semantic.SemanticPlan.Bindings.Clone()
	return compiler.CompileRecipeOutput(c.Engine.compileTarget, output, bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
}
