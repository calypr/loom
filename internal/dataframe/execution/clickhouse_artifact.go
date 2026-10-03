package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
)

// MaterializeClickHouseArtifact streams a complete AQL output into a private,
// typed ClickHouse artifact. The artifact remains leased after this call and
// must be released after the ClickHouse stage finishes reading it.
func (s OutputStream) MaterializeClickHouseArtifact(ctx context.Context, manager *chartifact.Manager, executionID, stageID string, columns []chartifact.Column) (*chartifact.Artifact, error) {
	if ctx == nil || manager == nil {
		return nil, fmt.Errorf("private artifact context and manager are required")
	}
	if s.physicalEngine == "CLICKHOUSE" || s.clickHouseCombine != nil || s.stream == nil {
		return nil, fmt.Errorf("private AQL artifact requires a streamable AQL output")
	}
	if s.queryLimit != 0 {
		return nil, fmt.Errorf("private AQL artifact cannot be built from a bounded preview")
	}
	if strings.TrimSpace(s.planFingerprint) == "" || strings.TrimSpace(s.recipeDigest) == "" {
		return nil, fmt.Errorf("private AQL artifact requires exact recipe and physical plan digests")
	}
	if strings.TrimSpace(s.stageID) == "" || stageID != s.stageID {
		return nil, fmt.Errorf("private AQL artifact stage ID does not match the compiled final stage")
	}
	if len(s.outputSchema) == 0 {
		return nil, fmt.Errorf("private AQL artifact requires a compiler-resolved output schema")
	}
	if s.bindings.Project == "" || s.bindings.DatasetGeneration == "" {
		return nil, fmt.Errorf("private AQL artifact requires exact project and dataset generation")
	}
	if s.bindings.AuthScopeMode == authscope.ReadScopeRestricted && !s.bindings.IncludeAuthResourcePath {
		return nil, fmt.Errorf("restricted AQL artifact requires a row-level authorization path projection")
	}

	normalized, err := chartifact.NormalizeSchema(columns)
	if err != nil {
		return nil, fmt.Errorf("validate private AQL artifact schema: %w", err)
	}
	compiled, err := chartifact.ColumnsFromCompiledOutput(s.outputSchema)
	if err != nil {
		return nil, fmt.Errorf("resolve compiled private AQL artifact schema: %w", err)
	}
	suppliedDigest, err := chartifact.SchemaDigest(stageID, normalized)
	if err != nil {
		return nil, fmt.Errorf("digest supplied private AQL artifact schema: %w", err)
	}
	compiledDigest, err := chartifact.SchemaDigest(stageID, compiled)
	if err != nil {
		return nil, fmt.Errorf("digest compiled private AQL artifact schema: %w", err)
	}
	if suppliedDigest != compiledDigest {
		return nil, fmt.Errorf("private AQL artifact schema does not match the compiled output schema")
	}
	if err := validateStreamSchema(s.Columns, normalized); err != nil {
		return nil, err
	}
	identity := chartifact.Identity{
		ExecutionID: executionID, StageID: stageID,
		Project: s.bindings.Project, DatasetGeneration: s.bindings.DatasetGeneration,
		RecipeDigest: s.recipeDigest, PlanDigest: s.planFingerprint,
		AuthScopeMode: s.bindings.AuthScopeMode, AuthResourcePaths: append([]string(nil), s.bindings.AuthResourcePaths...),
	}
	writer, err := manager.Begin(ctx, identity, normalized)
	if err != nil {
		return nil, fmt.Errorf("begin private AQL artifact: %w", err)
	}
	streamResult, err := s.Stream(ctx, func(row map[string]any) error {
		return writer.Write(ctx, row)
	})
	if err != nil {
		abortErr := writer.Abort(context.WithoutCancel(ctx))
		return nil, errors.Join(fmt.Errorf("stream AQL prefix into private ClickHouse artifact: %w", err), abortErr)
	}
	artifact, err := writer.Finalize(ctx)
	if err != nil {
		abortErr := writer.Abort(context.WithoutCancel(ctx))
		return nil, errors.Join(fmt.Errorf("finalize private AQL artifact: %w", err), abortErr)
	}
	if artifact.Manifest().RowCount != int64(streamResult.RowCount) {
		cleanupErr := artifact.Release(context.WithoutCancel(ctx))
		return nil, errors.Join(fmt.Errorf("private AQL artifact row count differs from its source stream"), cleanupErr)
	}
	return artifact, nil
}

// WithPrivateClickHouseArtifact materializes an AQL prefix while exact
// published inputs are pinned, invokes consume with both protections active,
// then releases the private table before releasing the published pins.
// consume must resolve exact published inputs inside its callback before it
// renders or executes the terminal ClickHouse operation.
func (s OutputStream) WithPrivateClickHouseArtifact(ctx context.Context, manager *chartifact.Manager, executionID, stageID string, columns []chartifact.Column, publishedRevisions []string, withPins WithExecutionReadPins, consume func(context.Context, *chartifact.Artifact) error) error {
	if ctx == nil || manager == nil || withPins == nil || consume == nil {
		return fmt.Errorf("private artifact execution requires context, manager, read pins, and consumer")
	}
	if len(publishedRevisions) == 0 {
		return fmt.Errorf("private artifact combine requires exact published input revisions")
	}
	return withPins(ctx, append([]string(nil), publishedRevisions...), func(pinnedCtx context.Context) (consumeErr error) {
		artifact, err := s.MaterializeClickHouseArtifact(pinnedCtx, manager, executionID, stageID, columns)
		if err != nil {
			return err
		}
		defer func() {
			releaseErr := artifact.Release(context.WithoutCancel(pinnedCtx))
			consumeErr = errors.Join(consumeErr, releaseErr)
		}()
		artifactCtx, cancel := artifact.LeaseContext(pinnedCtx)
		defer cancel()
		return consume(artifactCtx, artifact)
	})
}

func validateStreamSchema(streamColumns []string, artifactColumns []chartifact.Column) error {
	stream := make(map[string]bool, len(streamColumns))
	for _, name := range streamColumns {
		if strings.TrimSpace(name) == "" || stream[name] {
			return fmt.Errorf("AQL stream has an empty or duplicate output column")
		}
		stream[name] = true
	}
	physical := make(map[string]bool, len(artifactColumns))
	for _, column := range artifactColumns {
		if column.Internal || column.Name == "__loom_row_id" || column.Name == "auth_resource_path" || column.Name == "project_id" {
			continue
		}
		physical[column.Name] = true
	}
	if len(stream) != len(physical) {
		return fmt.Errorf("AQL stream columns do not match the private artifact schema")
	}
	for name := range stream {
		if !physical[name] {
			return fmt.Errorf("AQL stream column %q is missing from the private artifact schema", name)
		}
	}
	return nil
}
