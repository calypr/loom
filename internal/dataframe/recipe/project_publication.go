package recipe

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/publication"
)

type ProjectRecipeEnqueuer interface {
	Enqueue(context.Context, publication.BundleIdentity) (publication.BundleExecution, error)
}

type ProjectRecipeRegistry interface {
	RegisterVersionForProject(context.Context, string, Bundle) (string, error)
}

type ProjectRecipeRegistryFunc func(context.Context, string, Bundle) (string, error)

func (f ProjectRecipeRegistryFunc) RegisterVersionForProject(ctx context.Context, project string, bundle Bundle) (string, error) {
	return f(ctx, project, bundle)
}

// ProjectRecipePublisher is the control-plane boundary for project recipe
// publication. It registers one immutable scoped version, enqueues exactly
// the requested outputs, and never advances a mutable/global pointer.
type ProjectRecipePublisher struct {
	Drafts    DraftStore
	Revisions ProjectRevisionStore
	Registry  ProjectRecipeRegistry
	Enqueuer  ProjectRecipeEnqueuer
	Default   Bundle
}

func (p ProjectRecipePublisher) Publish(ctx context.Context, project string, expectedDraftVersion int64, sourceGeneration string, selectedOutputs []string) (RecipeRevision, error) {
	if p.Drafts == nil || p.Revisions == nil || p.Registry == nil || p.Enqueuer == nil {
		return RecipeRevision{}, fmt.Errorf("project recipe publisher is not configured")
	}
	draft, err := p.Drafts.GetDraft(ctx, project)
	if err != nil {
		if err != ErrDraftNotFound {
			return RecipeRevision{}, err
		}
		draft, err = NormalizeProjectBundle(project, p.Default)
		if err != nil {
			return RecipeRevision{}, err
		}
	}
	if expectedDraftVersion >= 0 && draft.DraftVersion != expectedDraftVersion {
		return RecipeRevision{}, &DraftConflictError{Current: draft}
	}
	selectedOutputs, err = normalizeSelectedOutputs(draft.Document, selectedOutputs)
	if err != nil {
		return RecipeRevision{}, err
	}
	revisions, err := p.Revisions.ListProjectRevisions(ctx, project)
	if err != nil {
		return RecipeRevision{}, err
	}
	for _, existing := range revisions {
		if strings.TrimPrefix(existing.AuthoringDigest, "sha256:") == strings.TrimPrefix(draft.AuthoringDigest, "sha256:") && sameStrings(revisionOutputs(existing), selectedOutputs) {
			return existing, nil
		}
	}
	next := int64(1)
	parent := draft.BaseRevisionID
	if len(revisions) != 0 {
		next = revisions[0].RevisionNumber + 1
		if parent == "" {
			parent = revisions[0].ID
		}
	}
	// RegisterProjectRevision assigns managed name, UUIDv7, and exact r######
	// translation version before the registry receives the canonical document.
	revision, err := p.Revisions.RegisterProjectRevision(ctx, project, draft.Document, parent, next)
	if err != nil {
		return RecipeRevision{}, err
	}
	digest, err := p.Registry.RegisterVersionForProject(ctx, project, revision.Bundle)
	if err != nil {
		revision.Status = RecipeRevisionFailed
		revision.Diagnostics = []BuilderDiagnostic{{Severity: "error", Code: "REGISTRY_UNAVAILABLE", Message: "recipe version could not be registered", Retryable: true}}
		_ = p.Revisions.UpdateProjectRevision(ctx, revision)
		return RecipeRevision{}, err
	}
	identity := publication.BundleIdentity{
		Name: revision.RecipeName, TranslationVersion: revision.TranslationVersion,
		Project: project, DatasetGeneration: sourceGeneration, RecipeDigest: digest,
		EngineVersion: "loom-recipe-v1", ScopeProject: project,
		ProjectRevisionID: revision.ID, SelectedOutputs: append([]string(nil), selectedOutputs...),
	}
	execution, err := p.Enqueuer.Enqueue(ctx, identity)
	if err != nil {
		revision.Status = RecipeRevisionFailed
		revision.Diagnostics = []BuilderDiagnostic{{Severity: "error", Code: "MATERIALIZATION_ENQUEUE_FAILED", Message: "selected materializations could not be queued", Retryable: true}}
		_ = p.Revisions.UpdateProjectRevision(ctx, revision)
		return RecipeRevision{}, err
	}
	revision.Status = RecipeRevisionMaterializing
	revision.AuthoringDigest = draft.AuthoringDigest
	revision.SourceGeneration = sourceGeneration
	revision.ExecutionID = execution.ID
	revision.Outputs = make([]RecipeRevisionOutput, 0, len(selectedOutputs))
	for _, output := range selectedOutputs {
		revision.Outputs = append(revision.Outputs, RecipeRevisionOutput{Output: output})
	}
	if err := p.Revisions.UpdateProjectRevision(ctx, revision); err != nil {
		return RecipeRevision{}, err
	}
	return revision, nil
}

func (p ProjectRecipePublisher) ObserveExecution(ctx context.Context, execution publication.BundleExecution) error {
	if execution.ScopeProject == "" || execution.ProjectRevisionID == "" {
		return nil
	}
	revision, err := p.Revisions.GetProjectRevision(ctx, execution.ScopeProject, execution.ProjectRevisionID)
	if err != nil {
		return err
	}
	revision.ExecutionID = execution.ID
	for i := range revision.Outputs {
		for _, output := range execution.Outputs {
			if output.Name == revision.Outputs[i].Output {
				revision.Outputs[i].MaterializationID = output.MaterializationID
			}
		}
	}
	if execution.State.Successful() && selectedOutputsQueryable(execution, revision.Outputs) {
		revision.Status = RecipeRevisionReady
	} else if execution.State == publication.BundleFailed {
		revision.Status = RecipeRevisionFailed
		revision.Diagnostics = []BuilderDiagnostic{{Severity: "error", Code: execution.FailureCode, Message: "recipe materialization failed", Retryable: execution.FailureRetryable}}
	} else {
		revision.Status = RecipeRevisionMaterializing
	}
	return p.Revisions.UpdateProjectRevision(ctx, revision)
}

func selectedOutputsQueryable(execution publication.BundleExecution, selected []RecipeRevisionOutput) bool {
	if len(selected) == 0 || len(execution.Outputs) < len(selected) {
		return false
	}
	for _, want := range selected {
		found := false
		for _, output := range execution.Outputs {
			if output.Name == want.Output && output.Queryable() {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func normalizeSelectedOutputs(bundle Bundle, selected []string) ([]string, error) {
	seen := make(map[string]bool, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		seen[output.Name] = true
	}
	if len(selected) == 0 {
		for _, output := range bundle.Outputs {
			selected = append(selected, output.Name)
		}
	}
	result := append([]string(nil), selected...)
	for _, name := range result {
		if strings.TrimSpace(name) == "" || !seen[name] {
			return nil, fmt.Errorf("selected recipe output %q does not exist", name)
		}
	}
	sort.Strings(result)
	for i := 1; i < len(result); i++ {
		if result[i] == result[i-1] {
			return nil, fmt.Errorf("selected recipe output %q is duplicated", result[i])
		}
	}
	return result, nil
}

func revisionOutputs(revision RecipeRevision) []string {
	result := make([]string, 0, len(revision.Outputs))
	for _, output := range revision.Outputs {
		result = append(result, output.Output)
	}
	return result
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
