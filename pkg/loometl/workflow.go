package loometl

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const LegacyMutableUploadEnv = "LOOM_ETL_LEGACY_MUTABLE_UPLOAD"

type Diagnostic struct {
	Project                  string
	Generation               string
	Selector                 *DataframeSelector
	ExecutionID              string
	Phase                    string
	Output                   string
	LoomRequestID            string
	ErrorCode                string
	Details                  map[string]any
	Retryable                bool
	PreviousReleasePreserved bool
	Message                  string
	Error                    string
}

type DiagnosticSink interface {
	Log(context.Context, Diagnostic)
}

type SlogSink struct{ Logger *slog.Logger }

func (s SlogSink) Log(ctx context.Context, d Diagnostic) {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	args := []any{
		"project", d.Project, "generation", d.Generation, "execution", d.ExecutionID,
		"phase", d.Phase, "output", d.Output, "loom_request_id", d.LoomRequestID,
		"error_code", d.ErrorCode, "retryable", d.Retryable,
		"previous_release_preserved", d.PreviousReleasePreserved,
	}
	if d.Error != "" {
		args = append(args, "error", d.Error)
	}
	if d.Selector != nil {
		args = append(args, "recipe", d.Selector.Recipe, "translation_version", d.Selector.TranslationVersion, "selector_output", d.Selector.Output)
	}
	if len(d.Details) != 0 {
		args = append(args, "details", d.Details)
	}
	if d.ErrorCode != "" {
		logger.ErrorContext(ctx, d.Message, args...)
	} else {
		logger.InfoContext(ctx, d.Message, args...)
	}
}

type LegacyRunner interface {
	RunLegacy(context.Context, RunRequest) (RunResult, error)
}

type WorkflowConfig struct {
	API                 LoomAPI
	Diagnostics         DiagnosticSink
	PollInterval        time.Duration
	Sleep               func(context.Context, time.Duration) error
	LegacyMutableUpload bool
	Legacy              LegacyRunner
}

type Workflow struct {
	api         LoomAPI
	diagnostics DiagnosticSink
	poll        time.Duration
	sleep       func(context.Context, time.Duration) error
	legacyMode  bool
	legacy      LegacyRunner
}

func NewWorkflow(cfg WorkflowConfig) (*Workflow, error) {
	if cfg.API == nil {
		return nil, fmt.Errorf("Loom API is required")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.Sleep == nil {
		cfg.Sleep = func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	if cfg.LegacyMutableUpload && cfg.Legacy == nil {
		return nil, fmt.Errorf("legacy mutable upload is enabled but no legacy runner is configured")
	}
	return &Workflow{api: cfg.API, diagnostics: cfg.Diagnostics, poll: cfg.PollInterval, sleep: cfg.Sleep, legacyMode: cfg.LegacyMutableUpload, legacy: cfg.Legacy}, nil
}

type RunRequest struct {
	Project           string
	GitCommit         string
	AuthResourcePath  string
	Resources         []ResourceSource
	RequiredSelectors []DataframeSelector
	OptionalSelectors []DataframeSelector
	ExpectedRevision  *int64
}

type RunResult struct {
	Generation SnapshotGeneration
	Executions []MaterializationExecution
	Active     ActiveRelease
	Legacy     bool
}

func (w *Workflow) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	if w.legacyMode {
		result, err := w.legacy.RunLegacy(ctx, request)
		result.Legacy = true
		return result, err
	}
	if err := validateRunRequest(request); err != nil {
		return RunResult{}, err
	}
	result := RunResult{}
	expectedRevision, observedActive, err := w.releaseRevision(ctx, request)
	if err != nil {
		return result, w.fail(ctx, request, "READ_ACTIVE_RELEASE", nil, "", err)
	}

	expectedTypes := make([]string, len(request.Resources))
	for i, resource := range request.Resources {
		expectedTypes[i] = resource.ResourceType
	}
	sort.Strings(expectedTypes)
	generation, err := w.api.CreateOrResumeGeneration(ctx, request.Project, request.GitCommit, CreateGenerationRequest{
		GitCommit: request.GitCommit, ExpectedResourceTypes: expectedTypes, AuthResourcePath: request.AuthResourcePath,
	})
	if err != nil {
		return result, w.fail(ctx, request, "CREATE_GENERATION", nil, "", err)
	}
	if err := validateGeneration(generation, request.Project, request.GitCommit); err != nil {
		return result, w.fail(ctx, request, "CREATE_GENERATION", nil, "", err)
	}
	if !sameStrings(generation.ExpectedResourceTypes, expectedTypes) {
		return result, w.fail(ctx, request, "CREATE_GENERATION", nil, "", &APIError{Code: "CHECKSUM_CONFLICT", Message: "generation expected resource types differ", RequestID: generation.RequestID})
	}
	result.Generation = generation
	w.log(ctx, Diagnostic{Project: request.Project, Generation: request.GitCommit, Phase: "CREATE_GENERATION", LoomRequestID: generation.RequestID, PreviousReleasePreserved: true, Message: "snapshot generation created or resumed"})

	for _, resource := range request.Resources {
		if uploaded, ok := findUpload(generation.Uploads, resource.ResourceType); ok {
			if uploaded.SHA256 != resource.SHA256 || uploaded.Size != resource.Size {
				conflict := &APIError{Code: "CHECKSUM_CONFLICT", Message: "existing immutable upload differs", Details: map[string]any{"resourceType": resource.ResourceType}, CanRetry: false}
				return result, w.fail(ctx, request, "UPLOAD_RESOURCE", nil, resource.ResourceType, conflict)
			}
			continue
		}
		if generation.State == "STAGED" {
			return result, w.fail(ctx, request, "UPLOAD_RESOURCE", nil, resource.ResourceType, &APIError{Code: "CHECKSUM_CONFLICT", Message: "staged generation is missing the expected immutable upload", Details: map[string]any{"resourceType": resource.ResourceType}})
		}
		generation, err = w.api.UploadResource(ctx, request.Project, request.GitCommit, resource)
		if err != nil {
			return result, w.fail(ctx, request, "UPLOAD_RESOURCE", nil, resource.ResourceType, err)
		}
		if err := validateGeneration(generation, request.Project, request.GitCommit); err != nil {
			return result, w.fail(ctx, request, "UPLOAD_RESOURCE", nil, resource.ResourceType, err)
		}
		result.Generation = generation
		w.log(ctx, Diagnostic{Project: request.Project, Generation: request.GitCommit, Phase: "UPLOAD_RESOURCE", Output: resource.ResourceType, LoomRequestID: generation.RequestID, PreviousReleasePreserved: true, Message: "snapshot resource uploaded"})
	}
	if generation.State != "STAGED" {
		finalized, finalizeErr := w.api.FinalizeGeneration(ctx, request.Project, request.GitCommit)
		if finalizeErr != nil {
			return result, w.fail(ctx, request, "FINALIZE_GENERATION", nil, "", finalizeErr)
		}
		generation = finalized.Generation
		result.Generation = generation
		if err := validateGeneration(generation, request.Project, request.GitCommit); err != nil {
			return result, w.fail(ctx, request, "FINALIZE_GENERATION", nil, "", err)
		}
		if generation.State != "STAGED" {
			return result, w.fail(ctx, request, "FINALIZE_GENERATION", nil, "", &APIError{Code: "GENERATION_INCOMPLETE", Message: "finalize did not return STAGED", RequestID: finalized.RequestID})
		}
		w.log(ctx, Diagnostic{Project: request.Project, Generation: request.GitCommit, Phase: "FINALIZE_GENERATION", LoomRequestID: finalized.RequestID, PreviousReleasePreserved: true, Message: "snapshot generation staged"})
	}

	groups := selectorGroups(request.RequiredSelectors)
	for _, group := range groups {
		execution, startErr := w.api.StartMaterialization(ctx, MaterializationRequest{Project: request.Project, Generation: request.GitCommit, Selector: group[0]})
		if startErr != nil {
			return result, w.fail(ctx, request, "START_MATERIALIZATION", &group[0], group[0].Output, startErr)
		}
		if execution.ID == "" || execution.Name != group[0].Recipe || execution.TranslationVersion != group[0].TranslationVersion || execution.SourceGeneration != request.GitCommit {
			return result, w.fail(ctx, request, "START_MATERIALIZATION", &group[0], group[0].Output, &APIError{Code: "PUBLICATION_FAILED", Message: "Loom returned the wrong durable execution identity", RequestID: execution.TransportRequestID})
		}
		w.log(ctx, Diagnostic{Project: request.Project, Generation: request.GitCommit, Selector: &group[0], ExecutionID: execution.ID, Phase: "START_MATERIALIZATION", LoomRequestID: execution.TransportRequestID, PreviousReleasePreserved: true, Message: "exact dataframe materialization started"})
		execution, pollErr := w.pollExecution(ctx, execution)
		result.Executions = append(result.Executions, execution)
		if pollErr != nil {
			return result, w.fail(ctx, request, "POLL_MATERIALIZATION", &group[0], group[0].Output, pollErr)
		}
		if err := verifyRequiredOutputs(execution, request.GitCommit, group); err != nil {
			return result, w.fail(ctx, request, "VERIFY_MATERIALIZATION", &group[0], group[0].Output, err)
		}
		w.log(ctx, Diagnostic{Project: request.Project, Generation: request.GitCommit, Selector: &group[0], ExecutionID: execution.ID, Phase: "VERIFY_MATERIALIZATION", LoomRequestID: firstNonempty(execution.LoomRequestID, execution.TransportRequestID), PreviousReleasePreserved: true, Message: "required dataframe outputs published"})
	}

	release, err := w.api.CreateRelease(ctx, request.Project, CreateReleaseRequest{Generation: request.GitCommit, GitCommit: request.GitCommit, OptionalSelectors: request.OptionalSelectors})
	if err != nil {
		return result, w.fail(ctx, request, "CREATE_RELEASE", nil, "", err)
	}
	if release.ID == "" || release.Project != request.Project || release.Generation != request.GitCommit {
		return result, w.fail(ctx, request, "CREATE_RELEASE", nil, "", fmt.Errorf("Loom returned an invalid project release"))
	}
	w.log(ctx, Diagnostic{Project: request.Project, Generation: request.GitCommit, Phase: "CREATE_RELEASE", LoomRequestID: release.RequestID, PreviousReleasePreserved: true, Message: "immutable project release created"})
	if observedActive != nil && observedActive.Release.ID == release.ID {
		result.Active = *observedActive
		w.log(ctx, Diagnostic{Project: request.Project, Generation: request.GitCommit, Phase: "ACTIVATE_RELEASE", LoomRequestID: observedActive.RequestID, Message: "project release was already active; activation confirmed"})
		return result, nil
	}

	active, activateErr := w.api.ActivateRelease(ctx, request.Project, release.ID, ActivateReleaseRequest{ExpectedRevision: expectedRevision})
	if activateErr != nil {
		confirmed, confirmErr := w.api.ActiveRelease(ctx, request.Project)
		if confirmErr == nil && confirmed.Release.ID == release.ID {
			active = confirmed
			activateErr = nil
		}
	}
	if activateErr != nil {
		return result, w.fail(ctx, request, "ACTIVATE_RELEASE", nil, "", activateErr)
	}
	if active.Release.ID != release.ID || active.Release.Generation != request.GitCommit || active.Revision != expectedRevision+1 {
		return result, w.fail(ctx, request, "CONFIRM_RELEASE", nil, "", fmt.Errorf("release activation was not confirmed"))
	}
	result.Active = active
	w.log(ctx, Diagnostic{Project: request.Project, Generation: request.GitCommit, Phase: "ACTIVATE_RELEASE", LoomRequestID: active.RequestID, Message: "project release activation confirmed"})
	return result, nil
}

func (w *Workflow) releaseRevision(ctx context.Context, request RunRequest) (int64, *ActiveRelease, error) {
	if request.ExpectedRevision != nil {
		return *request.ExpectedRevision, nil, nil
	}
	active, err := w.api.ActiveRelease(ctx, request.Project)
	if err == nil {
		return active.Revision, &active, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.Code == "NO_ACTIVE_RELEASE" || apiErr.Code == "RELEASE_NOT_FOUND") {
		return 0, nil, nil
	}
	if errors.As(err, &apiErr) && apiErr.Status == 404 {
		return 0, nil, nil
	}
	return 0, nil, err
}

func (w *Workflow) pollExecution(ctx context.Context, execution MaterializationExecution) (MaterializationExecution, error) {
	if execution.ID == "" {
		return execution, fmt.Errorf("durable execution ID is required")
	}
	for !execution.Terminal() {
		if err := w.sleep(ctx, w.poll); err != nil {
			return execution, err
		}
		current, err := w.api.MaterializationStatus(ctx, execution.ID)
		if err != nil {
			return execution, err
		}
		execution = current
	}
	if execution.State == "FAILED" {
		return execution, executionFailure(execution)
	}
	return execution, nil
}

func executionFailure(execution MaterializationExecution) error {
	result := &APIError{Code: execution.ErrorCode, Message: execution.Error, Phase: execution.Phase, RequestID: execution.LoomRequestID}
	if result.Code == "" {
		result.Code = "PUBLICATION_FAILED"
	}
	if execution.ErrorRetryable != nil {
		result.CanRetry = *execution.ErrorRetryable
	}
	for _, output := range execution.Outputs {
		if output.State != "FAILED" {
			continue
		}
		if output.ErrorCode != "" {
			result.Code = output.ErrorCode
		}
		if output.Error != "" {
			result.Message = output.Error
		}
		if output.Phase != "" {
			result.Phase = output.Phase
		}
		result.Output = output.Name
		if output.ErrorRetryable != nil {
			result.CanRetry = *output.ErrorRetryable
		}
		break
	}
	return result
}

func verifyRequiredOutputs(execution MaterializationExecution, generation string, required []DataframeSelector) error {
	if !execution.Successful() || execution.SourceGeneration != generation {
		return &APIError{Code: "PUBLICATION_FAILED", Message: "execution is not published for the staged generation", RequestID: execution.LoomRequestID}
	}
	byKey := make(map[string]ExecutionOutput, len(execution.Outputs))
	for _, output := range execution.Outputs {
		if output.Selector != nil {
			byKey[output.Selector.Key()] = output
		}
	}
	for _, selector := range required {
		output, ok := byKey[selector.Key()]
		if !ok || (output.State != "PUBLISHED" && output.State != "READY") {
			return &APIError{Code: "PUBLICATION_FAILED", Message: "required exact output is missing or unpublished", Output: selector.Output, RequestID: execution.LoomRequestID}
		}
	}
	return nil
}

func validateRunRequest(request RunRequest) error {
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.GitCommit) == "" {
		return fmt.Errorf("project and Git commit are required")
	}
	if len(request.Resources) == 0 {
		return fmt.Errorf("at least one FHIR resource upload is required")
	}
	resourceTypes := map[string]struct{}{}
	for _, resource := range request.Resources {
		if err := validateResourceType(resource.ResourceType); err != nil || resource.Open == nil || !validChecksum(resource.SHA256) || resource.Size < 0 {
			return fmt.Errorf("invalid resource source %q", resource.ResourceType)
		}
		if _, duplicate := resourceTypes[resource.ResourceType]; duplicate {
			return fmt.Errorf("duplicate resource type %q", resource.ResourceType)
		}
		resourceTypes[resource.ResourceType] = struct{}{}
	}
	if request.ExpectedRevision != nil && *request.ExpectedRevision < 0 {
		return fmt.Errorf("expected release revision must not be negative")
	}
	if len(request.RequiredSelectors) == 0 {
		return fmt.Errorf("at least one required dataframe selector is required")
	}
	for _, selector := range append(append([]DataframeSelector(nil), request.RequiredSelectors...), request.OptionalSelectors...) {
		if err := selector.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateGeneration(generation SnapshotGeneration, project, gitCommit string) error {
	if generation.Dataset.Project != project || generation.Dataset.Generation != gitCommit || generation.GitCommit != gitCommit {
		return fmt.Errorf("Loom returned a generation with the wrong immutable identity")
	}
	if generation.State != "LOADING" && generation.State != "STAGED" {
		return fmt.Errorf("generation %s is %s", gitCommit, generation.State)
	}
	return nil
}

func findUpload(uploads []ResourceUpload, resourceType string) (ResourceUpload, bool) {
	for _, upload := range uploads {
		if upload.ResourceType == resourceType {
			return upload, true
		}
	}
	return ResourceUpload{}, false
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for i := range leftCopy {
		if leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
}

func selectorGroups(selectors []DataframeSelector) [][]DataframeSelector {
	unique := make(map[string]DataframeSelector, len(selectors))
	for _, selector := range selectors {
		unique[selector.Key()] = selector
	}
	values := make([]DataframeSelector, 0, len(unique))
	for _, selector := range unique {
		values = append(values, selector)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Key() < values[j].Key() })
	groups := make([][]DataframeSelector, 0)
	for _, selector := range values {
		if len(groups) == 0 || groups[len(groups)-1][0].Recipe != selector.Recipe || groups[len(groups)-1][0].TranslationVersion != selector.TranslationVersion {
			groups = append(groups, []DataframeSelector{selector})
		} else {
			groups[len(groups)-1] = append(groups[len(groups)-1], selector)
		}
	}
	return groups
}

func (w *Workflow) fail(ctx context.Context, request RunRequest, stage string, selector *DataframeSelector, output string, err error) error {
	diagnostic := Diagnostic{Project: request.Project, Generation: request.GitCommit, Selector: selector, Phase: stage, Output: output, PreviousReleasePreserved: true, Message: "snapshot ETL failed; prior release remains active", Error: err.Error()}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		diagnostic.ErrorCode, diagnostic.Retryable, diagnostic.LoomRequestID, diagnostic.Details = apiErr.Code, apiErr.CanRetry, apiErr.RequestID, apiErr.Details
		if apiErr.Phase != "" {
			diagnostic.Phase = apiErr.Phase
		}
		if apiErr.Output != "" {
			diagnostic.Output = apiErr.Output
		}
	}
	w.log(ctx, diagnostic)
	return &WorkflowError{Stage: stage, Project: request.Project, Generation: request.GitCommit, PreviousReleasePreserved: true, Cause: err}
}

func (w *Workflow) log(ctx context.Context, diagnostic Diagnostic) {
	if w.diagnostics != nil {
		w.diagnostics.Log(ctx, diagnostic)
	}
}

func LegacyMutableUploadEnabled() (bool, error) {
	value := strings.TrimSpace(os.Getenv(LegacyMutableUploadEnv))
	if value == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", LegacyMutableUploadEnv, err)
	}
	return enabled, nil
}

func NewWorkflowFromEnvironment(cfg WorkflowConfig) (*Workflow, error) {
	enabled, err := LegacyMutableUploadEnabled()
	if err != nil {
		return nil, err
	}
	cfg.LegacyMutableUpload = enabled
	return NewWorkflow(cfg)
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
