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
	if strings.TrimSpace(s.Name) == "" || s.Name != strings.TrimSpace(s.Name) {
		return nil, fmt.Errorf("private AQL artifact requires an exact output ID")
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
		ExecutionID: executionID, OutputID: s.Name, StageID: stageID,
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

// PrivateClickHouseArtifactSource identifies one exact resolved workspace
// output to capture before a terminal ClickHouse operation.
type PrivateClickHouseArtifactSource struct {
	OutputID string
	Stream   OutputStream
	Columns  []chartifact.Column
}

// PrivateClickHouseArtifactInput preserves the caller's source order when the
// completed private captures are handed to the ClickHouse consumer.
type PrivateClickHouseArtifactInput struct {
	OutputID string
	Artifact *chartifact.Artifact
}

// WithPrivateClickHouseArtifacts pins any published inputs, captures each
// workspace output in order, invokes consume while every capture lease and
// published pin is live, then releases every capture before returning. A
// zero-length publishedRevisions list is valid for an all-workspace Combine.
func WithPrivateClickHouseArtifacts(
	ctx context.Context,
	manager *chartifact.Manager,
	executionID string,
	sources []PrivateClickHouseArtifactSource,
	publishedRevisions []string,
	withPins WithExecutionReadPins,
	consume func(context.Context, []PrivateClickHouseArtifactInput) error,
) error {
	if ctx == nil || manager == nil || consume == nil {
		return fmt.Errorf("private artifact execution requires context, manager, and consumer")
	}
	if len(sources) == 0 {
		return fmt.Errorf("private artifact execution requires at least one workspace output source")
	}
	seenOutputs := make(map[string]bool, len(sources))
	var firstSource *PrivateClickHouseArtifactSource
	for index, source := range sources {
		if strings.TrimSpace(source.OutputID) == "" || source.OutputID != strings.TrimSpace(source.OutputID) || seenOutputs[source.OutputID] {
			return fmt.Errorf("private artifact source %d requires a unique exact output ID", index)
		}
		if source.Stream.Name != source.OutputID {
			return fmt.Errorf("private artifact source %d output ID does not match its resolved stream", index)
		}
		if firstSource == nil {
			copy := source
			firstSource = &copy
		} else if !samePrivateArtifactSourceContext(firstSource.Stream, source.Stream) {
			return fmt.Errorf("private artifact source %q differs from the first source's recipe, project, generation, or authorization scope", source.OutputID)
		}
		seenOutputs[source.OutputID] = true
	}
	if len(publishedRevisions) > 0 && withPins == nil {
		return fmt.Errorf("published Combine inputs require exact execution read pins")
	}

	visit := func(pinnedCtx context.Context) (visitErr error) {
		if pinnedCtx == nil {
			return fmt.Errorf("private artifact pin callback returned a nil context")
		}
		artifactCtx, cancelArtifacts := context.WithCancelCause(pinnedCtx)
		artifacts := make([]PrivateClickHouseArtifactInput, 0, len(sources))
		stopLeaseCallbacks := make([]func() bool, 0, len(sources))
		cancelLeaseContexts := make([]context.CancelFunc, 0, len(sources))
		defer func() {
			cancelArtifacts(nil)
			for index := len(stopLeaseCallbacks) - 1; index >= 0; index-- {
				stopLeaseCallbacks[index]()
			}
			for index := len(cancelLeaseContexts) - 1; index >= 0; index-- {
				cancelLeaseContexts[index]()
			}
			for index := len(artifacts) - 1; index >= 0; index-- {
				releaseErr := artifacts[index].Artifact.Release(context.WithoutCancel(pinnedCtx))
				if releaseErr != nil {
					visitErr = errors.Join(visitErr, fmt.Errorf("release private artifact for workspace output %q: %w", artifacts[index].OutputID, releaseErr))
				}
			}
		}()

		for _, source := range sources {
			if err := artifactCtx.Err(); err != nil {
				return context.Cause(artifactCtx)
			}
			artifact, err := source.Stream.MaterializeClickHouseArtifact(artifactCtx, manager, executionID, source.Stream.stageID, source.Columns)
			if err != nil {
				if cause := context.Cause(artifactCtx); cause != nil {
					return cause
				}
				return fmt.Errorf("materialize workspace output %q: %w", source.OutputID, err)
			}
			artifacts = append(artifacts, PrivateClickHouseArtifactInput{OutputID: source.OutputID, Artifact: artifact})

			leaseCtx, cancelLeaseCtx := artifact.LeaseContext(artifactCtx)
			cancelLeaseContexts = append(cancelLeaseContexts, cancelLeaseCtx)
			capturedArtifact := artifact
			capturedOutputID := source.OutputID
			stopLeaseCallbacks = append(stopLeaseCallbacks, context.AfterFunc(leaseCtx, func() {
				if artifactCtx.Err() != nil {
					return
				}
				if leaseErr := capturedArtifact.CheckLease(); leaseErr != nil {
					cancelArtifacts(fmt.Errorf("private artifact lease for workspace output %q: %w", capturedOutputID, leaseErr))
					return
				}
				cancelArtifacts(leaseCtx.Err())
			}))
			if err := artifact.CheckLease(); err != nil {
				leaseErr := fmt.Errorf("private artifact lease for workspace output %q: %w", source.OutputID, err)
				cancelArtifacts(leaseErr)
				return leaseErr
			}
		}

		for _, input := range artifacts {
			if err := artifactCtx.Err(); err != nil {
				return context.Cause(artifactCtx)
			}
			if err := input.Artifact.CheckLease(); err != nil {
				return fmt.Errorf("private artifact lease for workspace output %q: %w", input.OutputID, err)
			}
		}
		consumeErr := consume(artifactCtx, artifacts)
		if cause := context.Cause(artifactCtx); cause != nil {
			return cause
		}
		if consumeErr != nil {
			return consumeErr
		}
		for _, input := range artifacts {
			if err := input.Artifact.CheckLease(); err != nil {
				return fmt.Errorf("private artifact lease for workspace output %q: %w", input.OutputID, err)
			}
		}
		if cause := context.Cause(artifactCtx); cause != nil {
			return cause
		}
		return nil
	}

	if len(publishedRevisions) == 0 {
		return visit(ctx)
	}
	return withPins(ctx, append([]string(nil), publishedRevisions...), visit)
}

func samePrivateArtifactSourceContext(left, right OutputStream) bool {
	if left.recipeDigest != right.recipeDigest || left.bindings.Project != right.bindings.Project ||
		left.bindings.DatasetGeneration != right.bindings.DatasetGeneration || left.bindings.AuthScopeMode != right.bindings.AuthScopeMode ||
		len(left.bindings.AuthResourcePaths) != len(right.bindings.AuthResourcePaths) {
		return false
	}
	for index, path := range left.bindings.AuthResourcePaths {
		if path != right.bindings.AuthResourcePaths[index] {
			return false
		}
	}
	return true
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
