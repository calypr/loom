package execution

// Sanitized live PostgreSQL Explain (pg-exec-10).
//
// This file turns one parent-tagged PostgreSQL compiled command into a
// transport-safe plan assessment. The only path to a PostgreSQL EXPLAIN is
// through the typed CompiledCommand boundary established by arch-backend-01:
// statement SQL and ordered arguments are read exclusively via
// CompiledCommand.PostgreSQLParameters, the EXPLAIN wrapper is re-tagged
// through compiler.NewPostgreSQLCommand, and the injected runner receives
// nothing but that tagged command. Raw EXPLAIN JSON stays inside this file:
// the parser lifts only the agreed backend/node/estimate fields into
// PostgreSQLPlanAssessment and drops every other key, so statement text,
// bound values (which PostgreSQL deparses into Filter/Index Cond strings),
// relation and index names, aliases, and schema internals never reach a
// transport adapter. There is no AQL fallback and no AQL-to-SQL conversion
// anywhere in this path.

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
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// postgreSQLExplainPrefix is the fixed EXPLAIN wrapper. Only this constant
// is prepended to the typed statement; nothing in a compiled command can
// change the EXPLAIN context itself.
const postgreSQLExplainPrefix = "EXPLAIN (FORMAT JSON)\n"

// postgresqlPlanJSONExecutor bounds keep a hostile or malfunctioning backend
// response from turning the parser into an unbounded resource sink.
const (
	postgresExplainMaxRawBytes = 4 << 20 // 4 MiB of plan JSON
	postgresExplainMaxNodes    = 512
	postgresExplainMaxDepth    = 64
)

// postgresExplainNodePattern gates kept "Node Type" strings. PostgreSQL node
// types come from its fixed operator vocabulary (letters, digits, and
// spaces). Anything outside this shape — quotes, parentheses, dollar signs,
// SQL punctuation — cannot be a node type and is rejected fail-closed.
var postgresExplainNodePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 ]{0,63}$`)

// postgresExplainSequentialNodeTypes and postgresExplainIndexNodeTypes are
// the closed scan classifications. A stored-relation full read is a
// sequential scan; index access is any index scan shape, including bitmap
// index scans. Every other node type is structural only.
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

// PostgreSQLPlanJSONExecutor runs one parent-tagged EXPLAIN command against
// PostgreSQL and returns the raw FORMAT JSON plan text. It is the transport
// seam the server owns: driver calls, connection pooling, and parameter
// binding belong to the adapter (internal/dataframe/postgres.(*Executor)
// satisfies the dispatch requirement). The raw JSON it returns is internal
// to PostgreSQLExplainPlan and never escapes the sanitizer.
type PostgreSQLPlanJSONExecutor func(ctx context.Context, command compiler.CompiledCommand) (json.RawMessage, error)

// BuildPostgreSQLExplainCommand derives the EXPLAIN-tagged command for one
// compiled query through closed dispatch. The statement SQL and ordered
// typed arguments come only from PostgreSQLParameters; the result is a new
// PostgreSQL-tagged command with the fixed EXPLAIN prefix and the same
// argument list. An AQL-tagged command is rejected with
// ErrCompiledCommandBackendMismatch and an untagged or unknown-tag command
// with ErrCompiledCommandBackendNotRegistered, so no command can reach the
// PostgreSQL EXPLAIN context without naming PostgreSQL.
func BuildPostgreSQLExplainCommand(compiled CompiledQuery) (compiler.CompiledCommand, error) {
	sqlText, args, err := compiled.Command.PostgreSQLParameters()
	if err != nil {
		return compiler.CompiledCommand{}, err
	}
	explainCommand, err := compiler.NewPostgreSQLCommand(postgreSQLExplainPrefix+sqlText, args)
	if err != nil {
		return compiler.CompiledCommand{}, fmt.Errorf("tag EXPLAIN command: %w", err)
	}
	return explainCommand, nil
}

// PostgreSQLExplainPlan executes one parent-tagged EXPLAIN for the compiled
// query and returns the sanitized assessment. The executor receives only the
// re-tagged EXPLAIN command; the raw plan JSON it returns is parsed and
// reduced here and is never returned or embedded in an error message.
// Failures are typed: executor failures become backend-unavailable user
// errors with the cause chain preserved for server-side classification,
// context aborts keep their context identity, and plan-shape violations
// become non-retryable backend-unavailable user errors reporting only the
// violated invariant.
//
// Error-boundary seam (pg-store-01 #101 merged; arch-backend-03 wired): the
// PostgreSQL rows adapter classifies driver and server failures at the
// store/postgres boundary, so failures arriving here already carry their
// authoritative Loom identity (stable condition, SQLSTATE, retryability).
// Such errors pass through unchanged — execution never re-types a
// boundary classification. Only failures the boundary did not classify
// (custom executors, unclassified causes) get the coarse
// retryable-backend-unavailable fallback, with the cause chain preserved
// through QueryError for server-side logs.
func PostgreSQLExplainPlan(ctx context.Context, executor PostgreSQLPlanJSONExecutor, compiled CompiledQuery) (PostgreSQLPlanAssessment, error) {
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

// postgreSQLExplainExecutorError types one executor failure without echoing
// the backend message: the cause is retained for errors.Is/errors.As and
// server logs, while the user-facing message stays sentinel-free.
// Failures already carrying a Loom user-error identity — including every
// store/postgres boundary classification surfaced by the rows adapter —
// pass through unchanged so the boundary's condition, SQLSTATE, and
// retryability stay authoritative; only unclassified failures receive the
// coarse fallback.
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

// postgreSQLExplainShapeError types one plan-shape violation. The reported
// message names the violated invariant only; the raw plan JSON never
// appears. Shape violations are not retryable: the same statement will
// produce the same malformed assessment.
func postgreSQLExplainShapeError(err error) error {
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "PostgreSQL EXPLAIN returned an unexpected plan shape",
		dataframeerrors.WithRetryable(false),
		dataframeerrors.WithDetails(map[string]any{"backend": "postgresql", "stage": "plan-parse"}),
	)
}

// postgresExplainRawNode decodes only the recognized EXPLAIN JSON keys.
// Every other key (Relation Name, Index Name, Alias, Filter, Index Cond,
// Output, Sort Key, Planning Time, and every future vendor field) is
// dropped by construction, not by blocklist.
type postgresExplainRawNode struct {
	NodeType    *string                  `json:"Node Type"`
	StartupCost *float64                 `json:"Startup Cost"`
	TotalCost   *float64                 `json:"Total Cost"`
	PlanRows    *float64                 `json:"Plan Rows"`
	PlanWidth   *float64                 `json:"Plan Width"`
	Plans       []postgresExplainRawNode `json:"Plans"`
}

// sanitizePostgreSQLPlanJSON parses one EXPLAIN (FORMAT JSON) document into
// the agreed sanitized assessment. It accepts exactly the shape PostgreSQL
// emits for a single statement: a JSON array with one element carrying a
// "Plan" object whose recursive "Plans" children form the plan tree.
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
		Backend:      string(compiler.CompiledBackendPostgreSQL),
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
		// The reported node type is not a PostgreSQL operator name; refuse to
		// render it rather than smuggle arbitrary text into the response.
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

// explainCompiledForOutput compiles one resolved output for ExplainPhysical.
// With no PostgreSQL selection seam installed — every existing deployment
// and test — this is exactly the pre-existing AQL policy compile, unchanged.
// With the seam installed it mirrors the stream path's backend-tagged
// decision (buildCompiledOutputStream): ClickHouse Combine outputs keep the
// AQL compile surface, unsupported physical engines fail before any compile,
// and a seam selection yields the same PostgreSQL-tagged CompiledQuery the
// stream would run, built field-for-field like postgreSQLStreamForOutput so
// Explain assesses the exact command execution will use.
func (c Control) explainCompiledForOutput(ctx context.Context, resolved Resolved, output lower.CompiledRecipeOutput, limit int) (compiler.CompiledQuery, error) {
	if c.Engine.resolvePostgreSQLCommands == nil || output.Plan.Engine == ir.PhysicalEngineClickHouse {
		return compiler.CompileRecipeOutputWithPolicy(output, resolved.Semantic.SemanticPlan.Bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
	}
	switch output.Plan.Engine {
	case "", ir.PhysicalEngineAQL:
	default:
		return compiler.CompiledQuery{}, fmt.Errorf("output %q has unsupported physical engine %q", output.Name, output.Plan.Engine)
	}
	bindings := resolved.Semantic.SemanticPlan.Bindings.Clone()
	selection, err := c.Engine.resolvePostgreSQLCommands(ctx, output, output.Plan, bindings, limit)
	if err != nil {
		return compiler.CompiledQuery{}, fmt.Errorf("output %q: postgres command resolution: %w", output.Name, err)
	}
	if selection == nil {
		return compiler.CompileRecipeOutputWithPolicy(output, resolved.Semantic.SemanticPlan.Bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
	}
	return postgreSQLExplainCompiledQuery(resolved, output, bindings, limit, selection)
}

// postgreSQLExplainCompiledQuery builds the Explain-facing twin of
// postgreSQLStreamForOutput's CompiledQuery construction. Field semantics
// stay identical to that authority; if it changes, this mirror must change
// with it or Explain would assess a different command than execution runs.
func postgreSQLExplainCompiledQuery(resolved Resolved, output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, limit int, selection *ResolvedPostgreSQLCommand) (compiler.CompiledQuery, error) {
	if output.Plan.ClickHouseCombine != nil {
		return compiler.CompiledQuery{}, fmt.Errorf("output %q: PostgreSQL-served output cannot carry a ClickHouse combine payload", output.Name)
	}
	sqlText, args, err := selection.Command.PostgreSQLParameters()
	if err != nil {
		return compiler.CompiledQuery{}, fmt.Errorf("output %q: %w", output.Name, err)
	}
	if len(selection.Columns) == 0 {
		return compiler.CompiledQuery{}, fmt.Errorf("output %q: PostgreSQL command selection requires a non-empty declared result column contract", output.Name)
	}
	outputSchema := lower.CloneCompiledOutputSchema(output.OutputSchema)
	publicColumns := publicPostgreSQLColumns(outputSchema, selection.Columns)
	if len(outputSchema) > 0 && len(publicColumns) == 0 {
		return compiler.CompiledQuery{}, fmt.Errorf("output %q: every declared PostgreSQL result column is an internal projection; refusing to publish internal columns in the public row", output.Name)
	}
	return compiler.CompiledQuery{
		Command:            selection.Command,
		Project:            bindings.Project,
		DatasetGeneration:  bindings.DatasetGeneration,
		RootResourceType:   output.RootResourceType,
		TranslationVersion: output.TranslationVersion,
		AuthResourcePaths:  append([]string(nil), bindings.AuthResourcePaths...),
		PlanMode:           previewPlanMode,
		PlanProfile:        previewPlanProfile,
		RowIdentity:        output.RowIdentity.Clone(),
		Columns:            append([]string(nil), selection.Columns...),
		PublicColumns:      append([]string(nil), publicColumns...),
		OutputSchema:       outputSchema,
		Limit:              limit,
		PlanDiagnostics:    ir.CompilerPlanDiagnostics{Fingerprint: postgreSQLCommandFingerprint(sqlText, args, selection.Columns)},
	}, nil
}
