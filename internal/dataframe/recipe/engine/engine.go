// Package engine is Loom's production recipe execution seam. It owns
// recipe resolution, scoped discovery, canonical compiler orchestration, and
// streaming row execution; transport adapters do not interpret recipes or
// construct AQL.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

type Registry interface {
	LoadRecipe(context.Context, string) (exec.Entry, error)
}

type VersionedRegistry interface {
	Registry
	LoadRecipeVersion(context.Context, string, string) (exec.Entry, error)
}

type ScopedVersionedRegistry interface {
	VersionedRegistry
	LoadRecipeVersionForProject(context.Context, string, string, string) (exec.Entry, error)
}

type QueryRows func(context.Context, string, int, map[string]any, func(map[string]any) error) error

type Config struct {
	Registry      Registry
	Revisions     recipe.RevisionStore
	ResolveBundle func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error)
	QueryRows     QueryRows
	ScopeDigest   func(recipe.RuntimeBindings) string
	BatchSize     int
}

type Engine struct {
	registry      Registry
	revisions     recipe.RevisionStore
	resolveBundle func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error)
	queryRows     QueryRows
	scopeDigest   func(recipe.RuntimeBindings) string
	batchSize     int
}

const (
	DefaultPreviewLimit     = 25
	MaxPreviewLimit         = 100
	MaxPreviewColumns       = 512
	MaxRecipeRequestBytes   = 1 << 20
	MaxPreviewResponseBytes = 5 << 20
)

// Resolved contains semantic discovery provenance and canonical physical plans
// per output. It deliberately contains no recipe-specific physical model.
type Resolved struct {
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
	Query         string
	BindVars      map[string]any
	DynamicChecks map[string]map[string]DynamicColumnCheck
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
	entry, err := e.loadEntry(ctx, name, bindings)
	if err != nil {
		return Resolved{}, err
	}
	return e.resolveEntry(ctx, entry, bindings)
}

func (e *Engine) loadEntry(ctx context.Context, name string, bindings recipe.RuntimeBindings) (exec.Entry, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return exec.Entry{}, fmt.Errorf("recipe project is required")
	}
	if bindings.RecipeDigest != "" && e.revisions != nil {
		revision, err := e.revisions.Get(ctx, bindings.Project, name, bindings.RecipeDigest)
		if err != nil {
			return exec.Entry{}, err
		}
		return exec.Entry{Bundle: revision.Bundle, Digest: revision.Digest}, nil
	}
	return e.registry.LoadRecipe(ctx, name)
}

// ResolveBundle validates, schema-resolves, and compiles an inline bundle.
// It is intentionally independent of the registry so unsaved authoring
// previews never create or mutate a durable recipe record.
func (e *Engine) ResolveBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	entry, err := canonicalInlineEntry(bundle)
	if err != nil {
		return Resolved{}, err
	}
	return e.resolveEntry(ctx, entry, bindings)
}

func (e *Engine) ValidateBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (semantic.RecipePlan, error) {
	resolved, err := e.ResolveBundle(ctx, bundle, bindings)
	if err != nil {
		return semantic.RecipePlan{}, err
	}
	return resolved.Semantic.SemanticPlan, nil
}

func (e *Engine) ExplainBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error) {
	plan, err := e.ValidateBundle(ctx, bundle, bindings)
	if err != nil {
		return semantic.RecipePlanExplanation{}, err
	}
	return plan.Explain(), nil
}

func (e *Engine) PreviewBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (Preview, error) {
	if len(bindings.OutputNames) > 1 {
		return Preview{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "preview accepts exactly one selected output")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resolved, err := e.ResolveBundle(ctx, bundle, bindings)
	if err != nil {
		return Preview{}, err
	}
	rows, err := e.Preview(ctx, resolved, bindings.PreviewLimit)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Plan: resolved.Semantic, Outputs: outputRows(resolved, rows)}, nil
}

// ResolveVersion loads an exact immutable recipe version. New publication
// workflows must use this method; Resolve is the deprecated default alias.
func (e *Engine) ResolveVersion(ctx context.Context, name, translationVersion string, bindings recipe.RuntimeBindings) (Resolved, error) {
	if strings.TrimSpace(bindings.ScopeProject) != "" {
		return e.ResolveVersionForProject(ctx, bindings.ScopeProject, name, translationVersion, bindings)
	}
	registry, ok := e.registry.(VersionedRegistry)
	if !ok {
		return Resolved{}, fmt.Errorf("recipe registry does not support exact versions")
	}
	entry, err := registry.LoadRecipeVersion(ctx, name, translationVersion)
	if err != nil {
		return Resolved{}, err
	}
	return e.resolveEntry(ctx, entry, bindings)
}

// ResolveVersionForProject loads only the immutable version registered under
// the requested project. It never falls back to the global registry.
func (e *Engine) ResolveVersionForProject(ctx context.Context, project, name, translationVersion string, bindings recipe.RuntimeBindings) (Resolved, error) {
	registry, ok := e.registry.(ScopedVersionedRegistry)
	if !ok {
		return Resolved{}, fmt.Errorf("recipe registry does not support scoped exact versions")
	}
	entry, err := registry.LoadRecipeVersionForProject(ctx, project, name, translationVersion)
	if err != nil {
		return Resolved{}, err
	}
	bindings.ScopeProject = project
	return e.resolveEntry(ctx, entry, bindings)
}

func (e *Engine) resolveEntry(ctx context.Context, entry exec.Entry, bindings recipe.RuntimeBindings) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, fmt.Errorf("recipe project is required")
	}
	bundle := entry.Bundle
	var err error
	storedRecipeDigest := entry.Digest
	if strings.TrimSpace(storedRecipeDigest) == "" {
		storedRecipeDigest, err = bundle.Digest()
		if err != nil {
			return Resolved{}, fmt.Errorf("digest stored recipe: %w", err)
		}
	}
	bundle, err = selectedBundle(bundle, bindings.OutputNames)
	if err != nil {
		return Resolved{}, err
	}
	if e.resolveBundle != nil {
		bundle, err = e.resolveBundle(ctx, bundle, bindings)
		if err != nil {
			if _, typed := dataframeerrors.AsUserError(err); typed {
				return Resolved{}, err
			}
			return Resolved{}, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
		}
	}
	semanticPlan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		if _, typed := dataframeerrors.AsUserError(err); typed {
			return Resolved{}, err
		}
		return Resolved{}, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
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
	return Resolved{Semantic: resolved, Compiled: compiled, StoredRecipeDigest: storedRecipeDigest, ResolvedSchemaDigest: resolvedSchemaDigest}, nil
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

func (e *Engine) MaterializeVersionForProject(ctx context.Context, project, name, translationVersion string, bindings recipe.RuntimeBindings, publish func(context.Context, Resolved) error) (Resolved, error) {
	resolved, err := e.ResolveVersionForProject(ctx, project, name, translationVersion, bindings)
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
		query, err := compiler.CompileRecipeOutputWithPolicy(output, resolved.Semantic.SemanticPlan.Bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", output.Name, err)
		}
		if len(query.PublicColumns) > MaxPreviewColumns {
			return nil, dataframeerrors.NewError(dataframeerrors.CodePlanTooExpensive, "")
		}
		streams = append(streams, OutputStream{
			Name: output.Name, Columns: append([]string(nil), query.PublicColumns...), RowIdentity: query.RowIdentity.Clone(), Query: query.Query,
			BindVars: query.BindVars, DynamicChecks: dynamicChecks(output.DynamicColumns), stream: e.queryRows, batchSize: e.batchSize,
		})
	}
	return streams, nil
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
	if limit == 0 {
		limit = DefaultPreviewLimit
	}
	if !validPreviewLimit(limit) {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidLimit, "")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	streams, err := e.streamsWithLimit(ctx, resolved, limit)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]map[string]any, len(streams))
	for _, stream := range streams {
		rows := make([]map[string]any, 0, limit)
		count := 0
		err := stream.stream(ctx, stream.Query, stream.batchSize, stream.BindVars, func(row map[string]any) error {
			if count >= limit {
				return errPreviewLimit
			}
			resolvedRow, err := materializePostQueryRowWithChecks(row, stream.DynamicChecks)
			if err != nil {
				return err
			}
			rows = append(rows, resolvedRow)
			count++
			return nil
		})
		if err != nil && err != errPreviewLimit {
			return nil, fmt.Errorf("output %q: %w", stream.Name, err)
		}
		result[stream.Name] = rows
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeOutputEncodingFailed, "")
	}
	if len(encoded) > MaxPreviewResponseBytes {
		return nil, dataframeerrors.NewError(dataframeerrors.CodePlanTooExpensive, "")
	}
	return result, nil
}

func validPreviewLimit(limit int) bool {
	return limit == 10 || limit == 25 || limit == 50 || limit == 100
}

func selectedBundle(bundle recipe.Bundle, names []string) (recipe.Bundle, error) {
	if len(names) == 0 {
		return bundle, nil
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return recipe.Bundle{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
		wanted[name] = struct{}{}
	}
	selected := make([]recipe.Output, 0, len(wanted))
	for _, output := range bundle.Outputs {
		if _, ok := wanted[output.Name]; ok {
			selected = append(selected, output)
			delete(wanted, output.Name)
		}
	}
	if len(wanted) != 0 {
		return recipe.Bundle{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	bundle.Outputs = selected
	return bundle, nil
}

func canonicalInlineEntry(bundle recipe.Bundle) (exec.Entry, error) {
	if bundle.Fragments != nil {
		expanded, err := bundle.ExpandFragments()
		if err != nil {
			return exec.Entry{}, err
		}
		bundle = expanded
	}
	if err := bundle.Validate(); err != nil {
		return exec.Entry{}, err
	}
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		return exec.Entry{}, err
	}
	if len(canonical) > MaxRecipeRequestBytes {
		return exec.Entry{}, dataframeerrors.NewError(dataframeerrors.CodePlanTooExpensive, "")
	}
	var immutable recipe.Bundle
	if err := json.Unmarshal(canonical, &immutable); err != nil {
		return exec.Entry{}, err
	}
	digest, err := immutable.Digest()
	if err != nil {
		return exec.Entry{}, err
	}
	return exec.Entry{Bundle: immutable, Digest: digest}, nil
}

var errPreviewLimit = fmt.Errorf("preview limit reached")

func (s OutputStream) Stream(ctx context.Context, visit func(map[string]any) error) (StreamResult, error) {
	if visit == nil {
		return StreamResult{}, fmt.Errorf("row visitor is required")
	}
	count := 0
	err := s.stream(ctx, s.Query, s.batchSize, s.BindVars, func(row map[string]any) error {
		resolved, err := materializePostQueryRowWithChecks(row, s.DynamicChecks)
		if err != nil {
			return err
		}
		if err := ensureStableRowIdentity(resolved, s.RowIdentity, s.BindVars); err != nil {
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
