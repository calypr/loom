// Package execution is Loom's production recipe execution seam. It owns
// recipe resolution, scoped discovery, canonical compiler orchestration, and
// streaming row execution; transport adapters do not interpret recipes or
// construct AQL.
package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type QueryRows func(context.Context, string, int, map[string]any, func(map[string]any) error) error

const (
	// DefaultPreviewLimit is used when a preview request omits its limit.
	DefaultPreviewLimit = 25
	// MaxPreviewLimit bounds rows accumulated by one output preview.
	MaxPreviewLimit    = 1000
	previewPlanMode    = "physical"
	previewPlanProfile = "generic_fhir_graph_recipe"
)

// PreviewRequest selects one compiled output and bounds its preview rows.
type PreviewRequest struct {
	Output string
	Limit  int
}

// PreviewSummary is safe execution metadata for one preview output. It does
// not contain AQL, bind variables, or physical plan contents.
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
}

type Config struct {
	Registry      exec.Reader
	Revisions     recipe.RevisionStore
	ResolveBundle func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error)
	QueryRows     QueryRows
	ScopeDigest   func(recipe.RuntimeBindings) string
	BatchSize     int
}

type Engine struct {
	registry      exec.Reader
	revisions     recipe.RevisionStore
	resolveBundle func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error)
	queryRows     QueryRows
	scopeDigest   func(recipe.RuntimeBindings) string
	batchSize     int
}

// Resolved contains the immutable recipe after schema discovery, semantic
// discovery provenance, and canonical physical plans per output. Bundle is the
// fully resolved recipe that may be passed back through CompileResolvedBundle
// after a receipt or other durable boundary. Compiled remains request-scoped
// runtime state; callers must not persist it.
type Resolved struct {
	Bundle               recipe.Bundle
	Semantic             semantic.ResolvedRecipePlan
	Compiled             lower.CompiledRecipe
	StoredRecipeDigest   string
	ResolvedSchemaDigest string
}

// ResolutionError marks failures while compiling a recipe before publication
// starts. Transport adapters can expose these as actionable recipe errors;
// publication and backend failures remain opaque internal errors.
type ResolutionError struct{ Err error }

func (e *ResolutionError) Error() string {
	if e == nil || e.Err == nil {
		return "recipe resolution failed"
	}
	return e.Err.Error()
}

func (e *ResolutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type OutputStream struct {
	Name          string
	Columns       []string
	RowIdentity   *spec.RowIdentity
	DynamicChecks map[string]map[string]DynamicColumnCheck
	query         string
	bindVars      map[string]any
	stream        QueryRows
	batchSize     int
}

type DynamicColumnCheck struct {
	ColumnName       string
	ValueType        string
	AllowUnknownKeys bool
}

type StreamResult struct {
	Output   string
	Columns  []string
	RowCount int
}

func New(cfg Config) (*Engine, error) {
	if cfg.Registry == nil {
		return nil, fmt.Errorf("recipe registry is required")
	}
	if cfg.QueryRows == nil {
		return nil, fmt.Errorf("recipe query executor is required")
	}
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = 1000
	}
	return &Engine{registry: cfg.Registry, revisions: cfg.Revisions, resolveBundle: cfg.ResolveBundle, queryRows: cfg.QueryRows, scopeDigest: cfg.ScopeDigest, batchSize: batch}, nil
}

func (e *Engine) Resolve(ctx context.Context, name string, bindings recipe.RuntimeBindings) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, fmt.Errorf("recipe project is required")
	}
	var entry exec.Entry
	var err error
	if bindings.RecipeDigest != "" && e.revisions != nil {
		revision, revisionErr := e.revisions.Get(ctx, bindings.Project, name, bindings.RecipeDigest)
		if revisionErr != nil {
			return Resolved{}, revisionErr
		}
		entry = exec.Entry{Bundle: revision.Bundle, Digest: revision.Digest}
	} else {
		entry, err = e.registry.LoadRecipe(ctx, name)
		if err != nil {
			return Resolved{}, err
		}
	}
	return e.resolveEntry(ctx, entry, bindings)
}

// ResolveVersion loads an exact immutable recipe version. New publication
// workflows must use this method; Resolve is the deprecated default alias.
func (e *Engine) ResolveVersion(ctx context.Context, name, translationVersion string, bindings recipe.RuntimeBindings) (Resolved, error) {
	entry, err := e.registry.LoadRecipeVersion(ctx, name, translationVersion)
	if err != nil {
		return Resolved{}, err
	}
	return e.resolveEntry(ctx, entry, bindings)
}

// ResolveBundle runs an unregistered immutable bundle through the identical
// schema, semantic, optimization, and lowering pipeline as a registry entry.
// Explorer uses this to avoid creating recipe drafts/revisions for authored UI
// intent.
func (e *Engine) ResolveBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, fmt.Errorf("recipe project is required")
	}
	digest, err := bundle.Digest()
	if err != nil {
		return Resolved{}, fmt.Errorf("digest bundle: %w", err)
	}
	return e.resolveEntry(ctx, exec.Entry{Bundle: bundle, Digest: digest}, bindings)
}

// CompileResolvedBundle validates and compiles a bundle that has already been
// resolved by schema discovery. It never invokes Config.ResolveBundle and
// therefore performs no catalog or schema discovery. Dynamic declarations
// with nil Columns are rejected by the semantic boundary; a non-nil empty
// Columns slice is an explicitly resolved, zero-column family and is valid.
func (e *Engine) CompileResolvedBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, fmt.Errorf("recipe project is required")
	}
	digest, err := bundle.Digest()
	if err != nil {
		return Resolved{}, fmt.Errorf("digest resolved bundle: %w", err)
	}
	return e.compileResolvedBundle(ctx, bundle, bindings, digest)
}

func (e *Engine) PreviewBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (map[string][]map[string]any, error) {
	resolved, err := e.ResolveBundle(ctx, bundle, bindings)
	if err != nil {
		return nil, &ResolutionError{Err: err}
	}
	return e.Preview(ctx, resolved, bindings.PreviewLimit)
}

// PreviewResolvedBundle compiles an already schema-resolved bundle without
// catalog discovery, then executes its request-scoped preview.
func (e *Engine) PreviewResolvedBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (map[string][]map[string]any, error) {
	resolved, err := e.CompileResolvedBundle(ctx, bundle, bindings)
	if err != nil {
		return nil, &ResolutionError{Err: err}
	}
	return e.Preview(ctx, resolved, bindings.PreviewLimit)
}

func (e *Engine) MaterializeBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings, publish func(context.Context, Resolved) error) (Resolved, error) {
	resolved, err := e.ResolveBundle(ctx, bundle, bindings)
	if err != nil {
		return Resolved{}, &ResolutionError{Err: err}
	}
	if publish != nil {
		if err := publish(ctx, resolved); err != nil {
			return Resolved{}, err
		}
	}
	return resolved, nil
}

// MaterializeResolvedBundle compiles an already schema-resolved bundle
// without catalog discovery and hands the resulting request-scoped plan to
// the publisher.
func (e *Engine) MaterializeResolvedBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings, publish func(context.Context, Resolved) error) (Resolved, error) {
	resolved, err := e.CompileResolvedBundle(ctx, bundle, bindings)
	if err != nil {
		return Resolved{}, &ResolutionError{Err: err}
	}
	if publish != nil {
		if err := publish(ctx, resolved); err != nil {
			return Resolved{}, err
		}
	}
	return resolved, nil
}

func (e *Engine) resolveEntry(ctx context.Context, entry exec.Entry, bindings recipe.RuntimeBindings) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, fmt.Errorf("recipe project is required")
	}
	bundle := entry.Bundle
	storedRecipeDigest, err := bundle.Digest()
	if err != nil {
		return Resolved{}, fmt.Errorf("digest stored recipe: %w", err)
	}
	if e.resolveBundle != nil {
		bundle, err = e.resolveBundle(ctx, bundle, bindings)
		if err != nil {
			return Resolved{}, fmt.Errorf("resolve recipe schema: %w", err)
		}
	}
	return e.compileResolvedBundle(ctx, bundle, bindings, storedRecipeDigest)
}

// compileResolvedBundle is the shared post-discovery compiler. Keeping this
// helper separate from resolveEntry makes it impossible for receipt-backed
// callers to accidentally rediscover catalog fields during compilation.
func (e *Engine) compileResolvedBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings, storedRecipeDigest string) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, fmt.Errorf("recipe project is required")
	}
	if strings.TrimSpace(storedRecipeDigest) == "" {
		var err error
		storedRecipeDigest, err = bundle.Digest()
		if err != nil {
			return Resolved{}, fmt.Errorf("digest resolved bundle: %w", err)
		}
	}
	semanticPlan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return Resolved{}, err
	}
	// The public recipe identity is the registered document digest. The
	// resolved schema digest below captures catalog-derived fields and scope so
	// materializations cannot collide when one recipe resolves differently.
	semanticPlan.RecipeDigest = storedRecipeDigest
	scope := ""
	if e.scopeDigest != nil {
		scope = e.scopeDigest(bindings)
	}
	resolved, err := semantic.ResolveRecipePlan(semanticPlan, scope, bindings.DatasetGeneration)
	if err != nil {
		return Resolved{}, err
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return Resolved{}, err
	}
	// Lowering and optimization are request-scoped work. Cache the optimized
	// physical plan on the resolved result so preview/materialization streams
	// only clone, window, and render it; they must not rebuild the recipe.
	policy := ir.DefaultPhysicalOptimizationPolicy()
	for index := range compiled.Outputs {
		optimized, optimizeErr := optimize.OptimizePhysicalPlanWithPolicy(compiled.Outputs[index].Plan, policy)
		if optimizeErr != nil {
			return Resolved{}, fmt.Errorf("optimize output %q: %w", compiled.Outputs[index].Name, optimizeErr)
		}
		compiled.Outputs[index].OptimizedPlan = &optimized
	}
	resolvedSchemaDigest, err := resolvedBundleSchemaDigest(storedRecipeDigest, bundle, bindings)
	if err != nil {
		return Resolved{}, err
	}
	resolved.ResolvedSchemaDigest = resolvedSchemaDigest
	return Resolved{Bundle: bundle, Semantic: resolved, Compiled: compiled, StoredRecipeDigest: storedRecipeDigest, ResolvedSchemaDigest: resolvedSchemaDigest}, nil
}

func resolvedBundleSchemaDigest(storedDigest string, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (string, error) {
	resolvedDigest, err := bundle.Digest()
	if err != nil {
		return "", err
	}
	paths := append([]string(nil), bindings.AuthResourcePaths...)
	sort.Strings(paths)
	payload, err := json.Marshal(struct {
		Stored, Resolved, Project, Generation, ScopeMode string
		Paths                                            []string
	}{storedDigest, resolvedDigest, bindings.Project, bindings.DatasetGeneration, string(bindings.AuthScopeMode), paths})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// Materialize resolves a recipe once and hands the complete resolved plan to
// the publisher. Resolve and compile therefore share one discovery snapshot.
func (e *Engine) Materialize(ctx context.Context, name string, bindings recipe.RuntimeBindings, publish func(context.Context, Resolved) error) (Resolved, error) {
	resolved, err := e.Resolve(ctx, name, bindings)
	if err != nil {
		return Resolved{}, &ResolutionError{Err: err}
	}
	if publish != nil {
		if err := publish(ctx, resolved); err != nil {
			return Resolved{}, err
		}
	}
	return resolved, nil
}

func (e *Engine) MaterializeVersion(ctx context.Context, name, translationVersion string, bindings recipe.RuntimeBindings, publish func(context.Context, Resolved) error) (Resolved, error) {
	resolved, err := e.ResolveVersion(ctx, name, translationVersion, bindings)
	if err != nil {
		return Resolved{}, &ResolutionError{Err: err}
	}
	if publish != nil {
		if err := publish(ctx, resolved); err != nil {
			return Resolved{}, err
		}
	}
	return resolved, nil
}

func (e *Engine) Streams(ctx context.Context, resolved Resolved) ([]OutputStream, error) {
	return e.streamsWithLimit(ctx, resolved, 0)
}

// Run consumes every row from every selected output. Unlike Preview, this
// deliberately compiles without an execution LIMIT and is intended for
// callers that explicitly requested the complete dataframe in memory.
func (e *Engine) Run(ctx context.Context, resolved Resolved) (map[string][]map[string]any, error) {
	streams, err := e.Streams(ctx, resolved)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]map[string]any, len(streams))
	for _, stream := range streams {
		rows := make([]map[string]any, 0)
		_, err := stream.Stream(ctx, func(row map[string]any) error {
			rows = append(rows, row)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", stream.Name, err)
		}
		result[stream.Name] = rows
	}
	return result, nil
}

func (e *Engine) streamsWithLimit(_ context.Context, resolved Resolved, limit int) ([]OutputStream, error) {
	streams := make([]OutputStream, 0, len(resolved.Compiled.Outputs))
	selected := selectedOutputNames(resolved.Semantic.SemanticPlan.Bindings.OutputNames, resolved.Compiled.Outputs)
	for _, name := range resolved.Semantic.SemanticPlan.Bindings.OutputNames {
		if !selected[name] {
			return nil, fmt.Errorf("requested recipe output %q was not found", name)
		}
	}
	for _, output := range resolved.Compiled.Outputs {
		if selected != nil && !selected[output.Name] {
			continue
		}
		stream, _, err := e.streamForOutput(resolved, output.Name, limit)
		if err != nil {
			return nil, err
		}
		streams = append(streams, stream)
	}
	return streams, nil
}

func (e *Engine) streamForOutput(resolved Resolved, name string, limit int) (OutputStream, compiler.CompiledQuery, error) {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name != name {
			continue
		}
		query, err := compiler.CompileRecipeOutputWithPolicy(output, resolved.Semantic.SemanticPlan.Bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			return OutputStream{}, compiler.CompiledQuery{}, fmt.Errorf("output %q: %w", output.Name, err)
		}
		return OutputStream{
			Name: output.Name, Columns: append([]string(nil), query.PublicColumns...), RowIdentity: query.RowIdentity.Clone(),
			DynamicChecks: dynamicChecks(output.DynamicColumns), query: query.Query, bindVars: query.BindVars,
			stream: e.queryRows, batchSize: e.batchSize,
		}, query, nil
	}
	return OutputStream{}, compiler.CompiledQuery{}, previewAdmissionError(dataframeerrors.CodeInvalidRequest, "requested preview output is not available", map[string]any{"output": name})
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

func (e *Engine) Preview(ctx context.Context, resolved Resolved, limit int) (map[string][]map[string]any, error) {
	limit, err := normalizePreviewLimit(limit)
	if err != nil {
		return nil, err
	}
	selected := resolved.Semantic.SemanticPlan.Bindings.OutputNames
	if len(selected) == 0 {
		selected = make([]string, 0, len(resolved.Compiled.Outputs))
		for _, output := range resolved.Compiled.Outputs {
			selected = append(selected, output.Name)
		}
	}
	for _, name := range selected {
		if !hasCompiledOutput(resolved, name) {
			return nil, previewAdmissionError(dataframeerrors.CodeInvalidRequest, "requested preview output is not available", map[string]any{"output": name})
		}
	}
	result := make(map[string][]map[string]any, len(selected))
	for _, name := range selected {
		rows := make([]map[string]any, 0, limit)
		_, err := e.PreviewOutput(ctx, resolved, PreviewRequest{Output: name, Limit: limit}, func(row map[string]any) error {
			rows = append(rows, row)
			return nil
		})
		if err != nil {
			return nil, err
		}
		result[name] = rows
	}
	return result, nil
}

func hasCompiledOutput(resolved Resolved, name string) bool {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name == name {
			return true
		}
	}
	return false
}

// PreviewOutput executes exactly one selected output and exposes only the
// public output schema. Physical query text, bind variables, and internal
// projections never cross this boundary.
func (e *Engine) PreviewOutput(ctx context.Context, resolved Resolved, request PreviewRequest, visit func(map[string]any) error) (PreviewSummary, error) {
	if err := contextError(ctx); err != nil {
		return PreviewSummary{}, err
	}
	limit, err := normalizePreviewLimit(request.Limit)
	if err != nil {
		return PreviewSummary{}, err
	}
	if strings.TrimSpace(request.Output) == "" {
		return PreviewSummary{}, previewAdmissionError(dataframeerrors.CodeInvalidRequest, "preview output is required", nil)
	}
	if visit == nil {
		return PreviewSummary{}, previewAdmissionError(dataframeerrors.CodeInvalidRequest, "preview visitor is required", nil)
	}
	loweringStarted := time.Now()
	stream, query, err := e.streamForOutput(resolved, request.Output, limit)
	if err != nil {
		if _, ok := dataframeerrors.AsUserError(err); ok {
			return PreviewSummary{}, err
		}
		return PreviewSummary{}, dataframeerrors.Wrap(err, dataframeerrors.CodeRecipeContractViolation, "preview plan compilation failed")
	}
	if err := contextError(ctx); err != nil {
		return PreviewSummary{}, err
	}
	if err := validatePreviewPlan(query, limit); err != nil {
		return PreviewSummary{}, err
	}
	summary := PreviewSummary{Output: stream.Name, Columns: append([]string(nil), stream.Columns...), PlanMode: query.PlanMode, PlanProfile: query.PlanProfile, PlanFingerprint: query.PlanDiagnostics.Fingerprint, TraversalCount: query.TraversalCount, LoweringDuration: time.Since(loweringStarted)}
	count := 0
	var visitorErr error
	queryStarted := time.Now()
	queryErr := stream.stream(ctx, stream.query, stream.batchSize, stream.bindVars, func(row map[string]any) error {
		if err := contextError(ctx); err != nil {
			return err
		}
		if count >= limit {
			return errPreviewLimit
		}
		resolvedRow, err := materializePostQueryRowWithChecks(row, stream.DynamicChecks)
		if err != nil {
			return err
		}
		public := publicPreviewRow(resolvedRow, stream.Columns)
		if err := visit(public); err != nil {
			visitorErr = err
			return err
		}
		count++
		return nil
	})
	summary.RowCount = count
	summary.QueryDuration = time.Since(queryStarted)
	if visitorErr != nil {
		return summary, normalizePreviewError(visitorErr, false)
	}
	if queryErr != nil && !errors.Is(queryErr, errPreviewLimit) {
		return summary, normalizePreviewError(queryErr, true)
	}
	if err := contextError(ctx); err != nil {
		return summary, err
	}
	return summary, nil
}

func normalizePreviewLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultPreviewLimit, nil
	}
	if limit < 1 || limit > MaxPreviewLimit {
		return 0, previewAdmissionError(dataframeerrors.CodeInvalidLimit, "preview limit is outside the supported range", map[string]any{"maximum": MaxPreviewLimit})
	}
	return limit, nil
}

func validatePreviewPlan(query compiler.CompiledQuery, limit int) error {
	if query.PlanMode != previewPlanMode || query.PlanProfile != previewPlanProfile || strings.TrimSpace(query.PlanDiagnostics.Fingerprint) == "" || query.Limit != limit {
		return previewAdmissionError(dataframeerrors.CodePlanTooExpensive, "compiled preview plan is not in the approved preview plan class", nil)
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return dataframeerrors.Normalize(err)
	}
	return nil
}

func previewAdmissionError(code dataframeerrors.ErrorCode, message string, details map[string]any) error {
	options := []dataframeerrors.ErrorOption{dataframeerrors.WithRetryable(false)}
	if details != nil {
		options = append(options, dataframeerrors.WithDetails(details))
	}
	return dataframeerrors.NewError(code, message, options...)
}

func normalizePreviewError(err error, backend bool) error {
	if err == nil {
		return nil
	}
	var drift *DynamicDriftError
	if errors.As(err, &drift) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeDynamicSchemaDrift, "dynamic schema drift", dataframeerrors.WithDetails(map[string]any{"dynamic_map": drift.DynamicName, "frozen_key_count": drift.FrozenKeyCount}))
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		return userErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return dataframeerrors.Normalize(err)
	}
	if backend {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return dataframeerrors.Normalize(err)
}

func publicPreviewRow(row map[string]any, columns []string) map[string]any {
	public := make(map[string]any, len(columns))
	for _, column := range columns {
		if value, ok := row[column]; ok {
			public[column] = value
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
	err := s.stream(ctx, s.query, s.batchSize, s.bindVars, func(row map[string]any) error {
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

// publicStreamRow removes compiler-only projections after they have served
// post-query validation and row-identity derivation. auth_resource_path is the
// one hidden projection retained for publication because it preserves the
// source row's authorization scope when a materialization spans multiple
// authorized paths.
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
		checks[column.DynamicName][column.SourceKey] = DynamicColumnCheck{ColumnName: column.Name, ValueType: column.ValueType, AllowUnknownKeys: column.AllowUnknownKeys}
	}
	return checks
}
