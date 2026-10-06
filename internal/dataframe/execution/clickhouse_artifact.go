package execution

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
)

// MaterializeClickHouseArtifact streams a complete AQL output into a private,
// typed ClickHouse artifact. The artifact remains leased after this call and
// must be released after the ClickHouse stage finishes reading it.
func (s OutputStream) MaterializeClickHouseArtifact(ctx context.Context, manager *chartifact.Manager, executionID, stageID string, columns []chartifact.Column) (*chartifact.Artifact, error) {
	capture, err := s.materializeClickHouseArtifact(ctx, manager, executionID, stageID, columns)
	if err != nil {
		return nil, err
	}
	return capture.Artifact, nil
}

func (s OutputStream) materializeClickHouseArtifact(ctx context.Context, manager *chartifact.Manager, executionID, stageID string, columns []chartifact.Column) (*materializedClickHouseCapture, error) {
	if ctx == nil || manager == nil {
		return nil, fmt.Errorf("private artifact context and manager are required")
	}
	if s.captureExecutionID != "" && executionID != s.captureExecutionID {
		return nil, fmt.Errorf("private artifact execution ID differs from its owning workspace capture")
	}
	aqlSource := s.physicalEngine != ir.PhysicalEngineClickHouse && s.clickHouseCombine == nil && s.stream != nil
	clickHouseSource := s.physicalEngine == ir.PhysicalEngineClickHouse && s.clickHouseCombine != nil && s.clickHouseQueryRows != nil && s.compiledOutput
	if !aqlSource && !clickHouseSource {
		return nil, fmt.Errorf("private artifact requires a compiler-resolved streamable AQL or ClickHouse output")
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
	authMode, err := streamExecutionAuthScopeMode(s)
	if err != nil {
		return nil, fmt.Errorf("private artifact authorization scope: %w", err)
	}
	if aqlSource && (s.scopeEvidence == nil || s.scopeEvidenceIdentity == nil || !s.scopeEvidence.Matches(s.physicalPlan, *s.scopeEvidenceIdentity)) {
		return nil, fmt.Errorf("private AQL artifact requires opaque evidence for its exact complete source plan")
	}
	if aqlSource && s.artifactScopeMode == "" {
		s.artifactScopeMode = chartifact.ScopeModeRows
	}
	if s.artifactScopeMode != "" && s.artifactScopeMode != chartifact.ScopeModeRows && s.artifactScopeMode != chartifact.ScopeModeWhole {
		return nil, fmt.Errorf("private artifact requires a supported compiler output scope mode")
	}
	if aqlSource && s.artifactScopeMode == chartifact.ScopeModeWhole && (!s.compiledOutput || strings.TrimSpace(s.artifactScopeEvidence) == "") {
		return nil, fmt.Errorf("whole-scope private artifact requires compiler-issued scope evidence")
	}
	if aqlSource && s.artifactScopeMode == chartifact.ScopeModeRows && s.artifactScopeEvidence != "" {
		return nil, fmt.Errorf("row-scoped private artifact cannot carry whole-scope evidence")
	}
	if aqlSource && authMode == authscope.ReadScopeRestricted && s.artifactScopeMode == chartifact.ScopeModeRows && !s.bindings.IncludeAuthResourcePath {
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
	if clickHouseSource {
		return s.materializePreparedClickHouse(ctx, manager, executionID, stageID, normalized)
	}
	identity := chartifact.Identity{
		ExecutionID: executionID, OutputID: s.Name, StageID: stageID,
		Project: s.bindings.Project, DatasetGeneration: s.bindings.DatasetGeneration,
		RecipeDigest: s.recipeDigest, PlanDigest: s.planFingerprint,
		AuthScopeMode: authMode, AuthResourcePaths: append([]string(nil), s.bindings.AuthResourcePaths...),
		ScopeMode: s.artifactScopeMode, ScopeEvidenceDigest: s.artifactScopeEvidence,
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
	return &materializedClickHouseCapture{
		Artifact: artifact, Identity: clickHouseArtifactIdentityFromChartifact(artifact.Manifest().Identity),
		PhysicalPlan:  s.physicalPlan,
		ScopeEvidence: *s.scopeEvidence, ScopeIdentity: *s.scopeEvidenceIdentity,
	}, nil
}

// materializePreparedClickHouse resolves every exact input and composes the
// query's scope evidence before opening a writer. Dependency leases remain
// owned by withPreparedClickHouse until the complete source query is consumed.
func (s OutputStream) materializePreparedClickHouse(ctx context.Context, manager *chartifact.Manager, executionID, stageID string, columns []chartifact.Column) (*materializedClickHouseCapture, error) {
	var capture *materializedClickHouseCapture
	captureStream := s
	captureStream.captureExecutionID = executionID
	err := captureStream.withPreparedClickHouse(ctx, func(preparedCtx context.Context, prepared preparedClickHouseQuery) error {
		if prepared.Identity.ExecutionID != executionID || prepared.Identity.OutputID != s.Name || prepared.Identity.StageID != stageID {
			return fmt.Errorf("prepared ClickHouse artifact identity differs from its exact execution, output, or stage")
		}
		if prepared.ScopeIdentity.OutputID != s.Name || prepared.ScopeIdentity.Project != s.bindings.Project ||
			prepared.ScopeIdentity.DatasetGeneration != s.bindings.DatasetGeneration ||
			(s.bindings.AuthScopeMode != "" && prepared.ScopeIdentity.AuthScopeMode != string(s.bindings.AuthScopeMode)) ||
			!sameExecutionStrings(prepared.ScopeIdentity.AuthResourcePaths, s.bindings.AuthResourcePaths) ||
			!prepared.ScopeEvidence.Matches(prepared.PhysicalPlan, prepared.ScopeIdentity) {
			return fmt.Errorf("prepared ClickHouse artifact lacks evidence for its exact output plan and scope")
		}
		if prepared.Identity.Project != s.bindings.Project || prepared.Identity.DatasetGeneration != s.bindings.DatasetGeneration ||
			prepared.Identity.AuthScopeMode != authscope.ReadScopeMode(prepared.ScopeIdentity.AuthScopeMode) ||
			(s.bindings.AuthScopeMode != "" && prepared.Identity.AuthScopeMode != s.bindings.AuthScopeMode) ||
			!sameExecutionStrings(prepared.Identity.AuthResourcePaths, s.bindings.AuthResourcePaths) {
			return fmt.Errorf("prepared ClickHouse artifact identity differs from its exact runtime scope")
		}
		schemaDigest, err := chartifact.SchemaDigest(stageID, columns)
		if err != nil {
			return fmt.Errorf("digest prepared ClickHouse artifact schema: %w", err)
		}
		if prepared.Identity.SchemaDigest != schemaDigest {
			return fmt.Errorf("prepared ClickHouse artifact schema differs from the compiler-final schema")
		}
		writer, err := manager.Begin(preparedCtx, prepared.Identity, columns)
		if err != nil {
			return fmt.Errorf("begin private ClickHouse artifact: %w", err)
		}
		streamResult, err := s.streamPreparedClickHouse(preparedCtx, prepared, func(row map[string]any) error {
			return writer.Write(preparedCtx, row)
		})
		if err != nil {
			abortErr := writer.Abort(context.WithoutCancel(preparedCtx))
			return errors.Join(fmt.Errorf("stream workspace Combine into private ClickHouse artifact: %w", err), abortErr)
		}
		artifact, err := writer.Finalize(preparedCtx)
		if err != nil {
			abortErr := writer.Abort(context.WithoutCancel(preparedCtx))
			return errors.Join(fmt.Errorf("finalize private ClickHouse artifact: %w", err), abortErr)
		}
		if !reflect.DeepEqual(artifact.Manifest().Identity, prepared.Identity) {
			cleanupErr := artifact.Release(context.WithoutCancel(preparedCtx))
			return errors.Join(fmt.Errorf("private ClickHouse artifact manifest differs from its prepared identity"), cleanupErr)
		}
		if artifact.Manifest().RowCount != int64(streamResult.RowCount) {
			cleanupErr := artifact.Release(context.WithoutCancel(preparedCtx))
			return errors.Join(fmt.Errorf("private ClickHouse artifact row count differs from its source stream"), cleanupErr)
		}
		capture = &materializedClickHouseCapture{
			Artifact: artifact, Identity: clickHouseArtifactIdentityFromChartifact(prepared.Identity),
			PhysicalPlan: prepared.PhysicalPlan, ScopeIdentity: prepared.ScopeIdentity, ScopeEvidence: prepared.ScopeEvidence,
		}
		return nil
	})
	if err != nil {
		if capture != nil && capture.Artifact != nil {
			err = errors.Join(err, capture.Artifact.Release(context.WithoutCancel(ctx)))
		}
		return nil, err
	}
	if capture == nil {
		return nil, fmt.Errorf("prepared ClickHouse artifact completed without a capture")
	}
	return capture, nil
}

// PrivateClickHouseArtifactSource identifies one exact resolved workspace
// output to capture before a terminal ClickHouse operation.
type PrivateClickHouseArtifactSource struct {
	OutputID         string
	Stream           OutputStream
	Columns          []chartifact.Column
	ExpectedIdentity ir.ClickHouseArtifactIdentity
}

// PrivateClickHouseArtifactInput preserves the caller's source order when the
// completed private captures are handed to the ClickHouse consumer.
type PrivateClickHouseArtifactInput struct {
	OutputID         string
	Artifact         *chartifact.Artifact
	ExpectedIdentity ir.ClickHouseArtifactIdentity
	SourcePlan       ir.PhysicalPlan
	ScopeIdentity    ir.WholeRelationScopeIdentity
	ScopeEvidence    ir.WholeRelationScopeEvidence
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
			sourceStream := source.Stream
			sourceStream.captureExecutionID = executionID
			capture, err := sourceStream.materializeClickHouseArtifact(artifactCtx, manager, executionID, sourceStream.stageID, source.Columns)
			if err != nil {
				if cause := context.Cause(artifactCtx); cause != nil {
					return cause
				}
				return fmt.Errorf("materialize workspace output %q: %w", source.OutputID, err)
			}
			artifacts = append(artifacts, PrivateClickHouseArtifactInput{
				OutputID: source.OutputID, Artifact: capture.Artifact, ExpectedIdentity: capture.Identity,
				SourcePlan: capture.PhysicalPlan, ScopeIdentity: capture.ScopeIdentity, ScopeEvidence: capture.ScopeEvidence,
			})
			expected := source.ExpectedIdentity
			if expected.OutputID != "" {
				expected.ExecutionID = executionID
				if !reflect.DeepEqual(expected, capture.Identity) {
					return fmt.Errorf("materialized workspace output %q differs from its exact expected identity", source.OutputID)
				}
			}
			manifestIdentity := clickHouseArtifactIdentityFromChartifact(capture.Artifact.Manifest().Identity)
			if !reflect.DeepEqual(manifestIdentity, capture.Identity) {
				return fmt.Errorf("materialized workspace output %q manifest differs from its complete prepared identity", source.OutputID)
			}

			leaseCtx, cancelLeaseCtx := capture.Artifact.LeaseContext(artifactCtx)
			cancelLeaseContexts = append(cancelLeaseContexts, cancelLeaseCtx)
			capturedArtifact := capture.Artifact
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
			if err := capture.Artifact.CheckLease(); err != nil {
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
