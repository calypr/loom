package arango

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/utils"
)

const previewCoveringIndexPrefix = "loom_pivot_preview_"
const maxPreviewCoveringIndexesPerCollection = 4
const previewCoveringIndexCleanupTimeout = 3 * time.Second

var ErrPreviewCoveringIndexLimit = errors.New("preview covering index limit reached")

type previewIndexCollection interface {
	Indexes(context.Context) ([]driver.IndexResponse, error)
	EnsurePersistentIndex(context.Context, []string, *driver.CreatePersistentIndexOptions) (driver.IndexResponse, bool, error)
	DeleteIndexByID(context.Context, string) error
}

type previewIndexReplacement struct {
	name   string
	fields []string
}

// EnsurePreviewCoveringIndex adds one bounded, compiler-selected covering index.
// The caller's AQL hint is non-forcing, so previews continue on the canonical
// plan if index preparation is unavailable.
func (c *Client) EnsurePreviewCoveringIndex(ctx context.Context, collection, name string, fields []string) error {
	return c.ensurePreviewCoveringIndex(ctx, collection, name, fields, nil)
}

// EnsurePreviewCoveringIndexReplacing provisions a related-category index and,
// only at the per-collection cap, may replace the exact compiler-named legacy
// category index supplied by the same plan.
func (c *Client) EnsurePreviewCoveringIndexReplacing(ctx context.Context, collection, name string, fields []string, supersededName string, supersededFields []string) error {
	replacement := &previewIndexReplacement{name: supersededName, fields: append([]string(nil), supersededFields...)}
	if c == nil || !validPreviewCoveringIndexReplacement(collection, name, fields, replacement) {
		return fmt.Errorf("invalid preview covering index replacement specification")
	}
	return c.ensurePreviewCoveringIndex(ctx, collection, name, fields, replacement)
}

func (c *Client) ensurePreviewCoveringIndex(ctx context.Context, collection, name string, fields []string, replacement *previewIndexReplacement) error {
	if c == nil || !validPreviewCoveringIndex(collection, name, fields) {
		return fmt.Errorf("invalid preview covering index specification")
	}
	c.previewIndexMu.Lock()
	defer c.previewIndexMu.Unlock()

	col, err := c.db.GetCollection(ctx, collection, nil)
	if err != nil {
		return fmt.Errorf("open preview covering collection: %w", err)
	}
	return ensurePreviewCoveringIndex(ctx, col, name, fields, replacement)
}

func ensurePreviewCoveringIndex(ctx context.Context, col previewIndexCollection, name string, fields []string, replacement *previewIndexReplacement) error {
	indexes, err := col.Indexes(ctx)
	if err != nil {
		return fmt.Errorf("list preview covering indexes: %w", err)
	}
	count := 0
	var existing *driver.IndexResponse
	var superseded *driver.IndexResponse
	for _, index := range indexes {
		if index.Name == name {
			if !previewCoveringIndexMatches(index, fields) {
				return fmt.Errorf("preview covering index name already has different fields")
			}
			copy := index
			existing = &copy
		}
		if strings.HasPrefix(index.Name, previewCoveringIndexPrefix) {
			count++
		}
		if replacement != nil && index.Name == replacement.name && previewCoveringIndexMatches(index, replacement.fields) {
			copy := index
			superseded = &copy
		}
	}
	if existing != nil {
		if count >= maxPreviewCoveringIndexesPerCollection && superseded != nil {
			return deleteSupersededPreviewIndex(ctx, col, *superseded)
		}
		return nil
	}
	if count >= maxPreviewCoveringIndexesPerCollection && superseded == nil {
		return ErrPreviewCoveringIndexLimit
	}
	index, created, err := col.EnsurePersistentIndex(ctx, fields, &driver.CreatePersistentIndexOptions{
		Name:   name,
		Sparse: utils.NewType(false), Unique: utils.NewType(false),
	})
	if err != nil {
		return fmt.Errorf("create preview covering index: %w", err)
	}
	if index.Name != name || !previewCoveringIndexMatches(index, fields) {
		verificationErr := fmt.Errorf("preview covering index was not selected by Arango")
		if created {
			if index.ID == "" {
				return errors.Join(verificationErr, fmt.Errorf("cannot roll back unexpected preview index without a stable ID"))
			}
			if rollbackErr := rollbackCreatedPreviewIndex(ctx, col, index.ID); rollbackErr != nil {
				return errors.Join(verificationErr, fmt.Errorf("roll back unexpected preview index: %w", rollbackErr))
			}
		}
		return verificationErr
	}
	if count >= maxPreviewCoveringIndexesPerCollection && superseded != nil {
		if err := deleteSupersededPreviewIndex(ctx, col, *superseded); err != nil {
			if created && index.ID != "" {
				if rollbackErr := rollbackCreatedPreviewIndex(ctx, col, index.ID); rollbackErr != nil {
					return errors.Join(err, fmt.Errorf("roll back replacement preview index: %w", rollbackErr))
				}
			}
			return err
		}
	}
	return nil
}

func deleteSupersededPreviewIndex(ctx context.Context, col previewIndexCollection, index driver.IndexResponse) error {
	if index.ID == "" {
		return fmt.Errorf("superseded preview index has no stable ID")
	}
	if err := col.DeleteIndexByID(ctx, index.ID); err != nil {
		return fmt.Errorf("delete superseded preview covering index %q: %w", index.Name, err)
	}
	return nil
}

func rollbackCreatedPreviewIndex(ctx context.Context, col previewIndexCollection, id string) error {
	if id == "" {
		return fmt.Errorf("created preview index has no stable ID")
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), previewCoveringIndexCleanupTimeout)
	defer cancel()
	return col.DeleteIndexByID(cleanupCtx, id)
}

func validPreviewCoveringIndexReplacement(collection, name string, fields []string, replacement *previewIndexReplacement) bool {
	if !validPreviewCoveringIndex(collection, name, fields) || replacement == nil ||
		!validPreviewCoveringIndex(collection, replacement.name, replacement.fields) || name == replacement.name {
		return false
	}
	if len(fields) != 5 || fields[2] != "resourceType" || !strings.HasPrefix(fields[3], "payload.") || fields[4] != "auth_resource_path" {
		return false
	}
	expectedLegacy := []string{"project", "dataset_generation", fields[3], "auth_resource_path"}
	return slices.Equal(replacement.fields, expectedLegacy)
}

func previewCoveringIndexMatches(index driver.IndexResponse, fields []string) bool {
	return index.Type == driver.IndexType("persistent") && index.RegularIndex != nil &&
		index.Unique != nil && !*index.Unique && index.Sparse != nil && !*index.Sparse &&
		slices.Equal(index.RegularIndex.Fields, fields)
}

func validPreviewCoveringIndex(collection, name string, fields []string) bool {
	if collection == "" || !validIndexPath(collection) || !strings.HasPrefix(name, previewCoveringIndexPrefix) || !validIndexPath(name) {
		return false
	}
	if len(fields) < 4 || len(fields) > 32 || fields[0] != "project" || fields[1] != "dataset_generation" {
		return false
	}
	categoryOrdered := len(fields) == 4 && strings.HasPrefix(fields[2], "payload.") && fields[3] == "auth_resource_path"
	relatedCategoryTypeOrdered := len(fields) == 5 && fields[2] == "resourceType" &&
		strings.HasPrefix(fields[3], "payload.") && fields[4] == "auth_resource_path"
	if fields[2] != "auth_resource_path" && !categoryOrdered && !relatedCategoryTypeOrdered {
		return false
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if !validIndexPath(field) {
			return false
		}
		if _, exists := seen[field]; exists {
			return false
		}
		seen[field] = struct{}{}
	}
	return true
}

func validIndexPath(path string) bool {
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			return false
		}
		for _, char := range part {
			if char != '_' && !unicode.IsLetter(char) && !unicode.IsDigit(char) {
				return false
			}
		}
	}
	return true
}
