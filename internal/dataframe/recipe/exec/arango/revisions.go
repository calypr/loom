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
)

const RevisionCollection = "loom_dataframe_recipe_revisions"

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
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{Name: RevisionCollection, Indexes: [][]string{{"project", "name"}, {"project", "name", "digest"}}}}}
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
	value := recipe.RecipeRevision{Project: project, Name: bundle.Name, Digest: digest, Parent: parent, Bundle: bundle, CreatedAt: time.Now().UTC()}
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

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
