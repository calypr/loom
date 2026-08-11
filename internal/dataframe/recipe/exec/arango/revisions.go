package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

const RevisionCollection = "loom_dataframe_recipe_revisions"
const ProjectDraftCollection = "loom_dataframe_project_recipe_drafts"
const ProjectRevisionCollection = "loom_dataframe_project_recipe_revisions"

// RevisionRegistry persists immutable project recipe revisions in the same
// Arango database/client as the legacy name-addressed recipe registry.
type RevisionRegistry struct {
	client    Client
	batchSize int
}

func NewRevisionRegistry(client Client) (*RevisionRegistry, error) {
	if client == nil {
		return nil, fmt.Errorf("Arango recipe revision registry client is required")
	}
	return &RevisionRegistry{client: client, batchSize: 32}, nil
}

func RevisionBootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{
		{Name: RevisionCollection, Indexes: [][]string{{"project", "name"}, {"project", "name", "digest"}}},
		{Name: ProjectDraftCollection, Indexes: [][]string{{"project"}}},
		{Name: ProjectRevisionCollection, Indexes: [][]string{{"project", "recipeName"}, {"project", "revisionNumber"}, {"project", "id"}}},
	}}
}

func revisionKey(project, name, digest string) string {
	sum := sha256.Sum256([]byte(project + "\x00" + name + "\x00" + digest))
	return hex.EncodeToString(sum[:])
}

func (r *RevisionRegistry) Register(ctx context.Context, project string, bundle recipe.Bundle, parent string) (recipe.RecipeRevision, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(bundle.Name) == "" {
		return recipe.RecipeRevision{}, fmt.Errorf("project and recipe name are required")
	}
	if err := bundle.Validate(); err != nil {
		return recipe.RecipeRevision{}, err
	}
	digest, err := bundle.Digest()
	if err != nil {
		return recipe.RecipeRevision{}, err
	}
	if parent != "" {
		current, currentErr := r.currentDigest(ctx, project, bundle.Name)
		if currentErr != nil && currentErr != exec.ErrRecipeNotFound {
			return recipe.RecipeRevision{}, currentErr
		}
		if current != "" && current != parent {
			return recipe.RecipeRevision{}, fmt.Errorf("recipe revision parent %q is not current (current %q)", parent, current)
		}
	}
	id, idErr := uuid.NewV7()
	if idErr != nil {
		return recipe.RecipeRevision{}, idErr
	}
	value := recipe.RecipeRevision{ID: id.String(), Project: project, Name: bundle.Name, Digest: digest, Parent: parent, Bundle: bundle, CreatedAt: time.Now().UTC()}
	data, err := json.Marshal(value)
	if err != nil {
		return recipe.RecipeRevision{}, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return recipe.RecipeRevision{}, err
	}
	doc["project"], doc["name"], doc["digest"], doc["parentDigest"], doc["_key"] = project, bundle.Name, digest, parent, revisionKey(project, bundle.Name, digest)
	doc["createdAt"] = value.CreatedAt
	data, err = json.Marshal(doc)
	if err != nil {
		return recipe.RecipeRevision{}, err
	}
	if err := r.client.InsertBatchRaw(ctx, RevisionCollection, []json.RawMessage{data}, true, "document"); err != nil {
		return recipe.RecipeRevision{}, err
	}
	return value, nil
}

func (r *RevisionRegistry) currentDigest(ctx context.Context, project, name string) (string, error) {
	var digest string
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc.project == @project AND doc.name == @name SORT doc.createdAt DESC LIMIT 1 RETURN doc.digest`, r.batchSize, map[string]any{"@collection": RevisionCollection, "project": project, "name": name}, func(row map[string]any) error {
		digest = stringValue(row["digest"])
		return nil
	})
	if err != nil {
		return "", err
	}
	if digest == "" {
		return "", exec.ErrRecipeNotFound
	}
	return digest, nil
}

func (r *RevisionRegistry) Get(ctx context.Context, project, name, digest string) (recipe.RecipeRevision, error) {
	var found *recipe.RecipeRevision
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc.project == @project AND doc.name == @name AND doc.digest == @digest LIMIT 1 RETURN doc`, r.batchSize, map[string]any{"@collection": RevisionCollection, "project": project, "name": name, "digest": digest}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var value recipe.RecipeRevision
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		found = &value
		return nil
	})
	if err != nil {
		return recipe.RecipeRevision{}, err
	}
	if found == nil {
		return recipe.RecipeRevision{}, recipe.ErrRecipeRevisionNotFound
	}
	return *found, nil
}

func (r *RevisionRegistry) List(ctx context.Context, project, name string) ([]recipe.RecipeRevision, error) {
	result := make([]recipe.RecipeRevision, 0)
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc.project == @project AND doc.name == @name SORT doc.createdAt DESC RETURN doc`, r.batchSize, map[string]any{"@collection": RevisionCollection, "project": project, "name": name}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var value recipe.RecipeRevision
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		result = append(result, value)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

var _ recipe.RevisionStore = (*RevisionRegistry)(nil)
var _ recipe.DraftStore = (*RevisionRegistry)(nil)

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func (r *RevisionRegistry) GetDraft(ctx context.Context, project string) (recipe.RecipeDraft, error) {
	var found *recipe.RecipeDraft
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc.project == @project LIMIT 1 RETURN doc`, r.batchSize, map[string]any{"@collection": ProjectDraftCollection, "project": project}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var value recipe.RecipeDraft
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		found = &value
		return nil
	})
	if err != nil {
		return recipe.RecipeDraft{}, err
	}
	if found == nil {
		return recipe.RecipeDraft{}, recipe.ErrDraftNotFound
	}
	return *found, nil
}

func (r *RevisionRegistry) SaveDraft(ctx context.Context, draft recipe.RecipeDraft, expectedVersion int64) (recipe.RecipeDraft, error) {
	if strings.TrimSpace(draft.Project) == "" {
		return recipe.RecipeDraft{}, fmt.Errorf("project is required")
	}
	if draft.Document.Name == "" {
		draft.Document.Name = recipe.ProjectRecipeName(draft.Project)
	}
	if draft.Document.TranslationVersion == "" {
		draft.Document.TranslationVersion = "draft"
	}
	if err := draft.Document.Validate(); err != nil {
		return recipe.RecipeDraft{}, err
	}
	digest, err := draft.Document.Digest()
	if err != nil {
		return recipe.RecipeDraft{}, err
	}
	draft.AuthoringDigest = "sha256:" + digest
	draft.DraftVersion = expectedVersion + 1
	draft.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(draft)
	if err != nil {
		return recipe.RecipeDraft{}, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return recipe.RecipeDraft{}, err
	}
	doc["_key"] = draftKey(draft.Project)
	var saved *recipe.RecipeDraft
	err = r.client.QueryRows(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing == null OR existing.draftVersion == @expectedVersion
UPSERT {_key: @key}
INSERT MERGE(@document, {_key: @key})
UPDATE MERGE(@document, {_key: @key})
IN @@collection
RETURN NEW`, r.batchSize, map[string]any{"@collection": ProjectDraftCollection, "key": draftKey(draft.Project), "expectedVersion": expectedVersion, "document": doc}, func(row map[string]any) error {
		bytes, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var value recipe.RecipeDraft
		if err := json.Unmarshal(bytes, &value); err != nil {
			return err
		}
		saved = &value
		return nil
	})
	if err != nil {
		return recipe.RecipeDraft{}, err
	}
	if saved == nil {
		current, currentErr := r.GetDraft(ctx, draft.Project)
		if currentErr != nil {
			return recipe.RecipeDraft{}, currentErr
		}
		return recipe.RecipeDraft{}, &recipe.DraftConflictError{Current: current}
	}
	return *saved, nil
}

func (r *RevisionRegistry) DeleteProject(ctx context.Context, project string) error {
	executor, ok := r.client.(interface {
		ExecuteAQL(context.Context, string, map[string]interface{}) error
	})
	if !ok {
		return nil
	}
	return executor.ExecuteAQL(ctx, `FOR doc IN @@drafts FILTER doc.project == @project REMOVE doc IN @@drafts
FOR doc IN @@revisions FILTER doc.project == @project REMOVE doc IN @@revisions`, map[string]interface{}{"@drafts": ProjectDraftCollection, "@revisions": ProjectRevisionCollection, "project": project})
}

func draftKey(project string) string {
	sum := sha256.Sum256([]byte(project))
	return "draft_" + hex.EncodeToString(sum[:])
}

// RegisterProjectRevision persists the rich authoring revision record used by
// the project lifecycle while leaving the digest-addressed compatibility
// registry untouched.
func (r *RevisionRegistry) RegisterProjectRevision(ctx context.Context, project string, bundle recipe.Bundle, parent string, revisionNumber int64) (recipe.RecipeRevision, error) {
	if strings.TrimSpace(project) == "" {
		return recipe.RecipeRevision{}, fmt.Errorf("project is required")
	}
	if bundle.Name == "" {
		bundle.Name = recipe.ProjectRecipeName(project)
	}
	if bundle.TranslationVersion == "" {
		bundle.TranslationVersion = "draft"
	}
	if err := bundle.Validate(); err != nil {
		return recipe.RecipeRevision{}, err
	}
	digest, err := bundle.Digest()
	if err != nil {
		return recipe.RecipeRevision{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return recipe.RecipeRevision{}, err
	}
	value := recipe.RecipeRevision{ID: id.String(), Project: project, Name: bundle.Name, RecipeName: recipe.ProjectRecipeName(project), Digest: digest, Parent: parent, Bundle: bundle, RevisionNumber: revisionNumber, Status: recipe.RecipeRevisionValidating, TranslationVersion: recipe.ProjectRecipeTranslationVersion(revisionNumber, id.String()), CreatedAt: time.Now().UTC()}
	data, err := json.Marshal(value)
	if err != nil {
		return recipe.RecipeRevision{}, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return recipe.RecipeRevision{}, err
	}
	doc["_key"] = value.ID
	doc["project"] = project
	doc["recipeName"] = value.RecipeName
	if err := r.client.InsertBatchRaw(ctx, ProjectRevisionCollection, []json.RawMessage{mustJSON(doc)}, true, "document"); err != nil {
		return recipe.RecipeRevision{}, err
	}
	return value, nil
}

func (r *RevisionRegistry) GetProjectRevision(ctx context.Context, project, id string) (recipe.RecipeRevision, error) {
	var found *recipe.RecipeRevision
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc.project == @project AND doc.id == @id LIMIT 1 RETURN doc`, r.batchSize, map[string]any{"@collection": ProjectRevisionCollection, "project": project, "id": id}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var value recipe.RecipeRevision
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		found = &value
		return nil
	})
	if err != nil {
		return recipe.RecipeRevision{}, err
	}
	if found == nil {
		return recipe.RecipeRevision{}, recipe.ErrRecipeRevisionNotFound
	}
	return *found, nil
}

func (r *RevisionRegistry) ListProjectRevisions(ctx context.Context, project string) ([]recipe.RecipeRevision, error) {
	result := make([]recipe.RecipeRevision, 0)
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER doc.project == @project SORT doc.revisionNumber DESC, doc.createdAt DESC RETURN doc`, r.batchSize, map[string]any{"@collection": ProjectRevisionCollection, "project": project}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var value recipe.RecipeRevision
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		result = append(result, value)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].RevisionNumber > result[j].RevisionNumber })
	return result, nil
}

func mustJSON(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
