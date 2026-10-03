package arango

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/utils"
)

const previewCoveringIndexPrefix = "loom_pivot_preview_"
const maxPreviewCoveringIndexesPerCollection = 4

var ErrPreviewCoveringIndexLimit = errors.New("preview covering index limit reached")

// EnsurePreviewCoveringIndex adds one bounded, compiler-selected covering index.
// The caller's AQL hint is non-forcing, so previews continue on the canonical
// plan if index preparation is unavailable.
func (c *Client) EnsurePreviewCoveringIndex(ctx context.Context, collection, name string, fields []string) error {
	if c == nil || !validPreviewCoveringIndex(collection, name, fields) {
		return fmt.Errorf("invalid preview covering index specification")
	}
	c.previewIndexMu.Lock()
	defer c.previewIndexMu.Unlock()

	col, err := c.db.GetCollection(ctx, collection, nil)
	if err != nil {
		return fmt.Errorf("open preview covering collection: %w", err)
	}
	indexes, err := col.Indexes(ctx)
	if err != nil {
		return fmt.Errorf("list preview covering indexes: %w", err)
	}
	count := 0
	for _, index := range indexes {
		if index.Name == name {
			if !previewCoveringIndexMatches(index, fields) {
				return fmt.Errorf("preview covering index name already has different fields")
			}
			return nil
		}
		if strings.HasPrefix(index.Name, previewCoveringIndexPrefix) {
			count++
		}
	}
	if count >= maxPreviewCoveringIndexesPerCollection {
		return ErrPreviewCoveringIndexLimit
	}
	index, _, err := col.EnsurePersistentIndex(ctx, fields, &driver.CreatePersistentIndexOptions{
		Name:   name,
		Sparse: utils.NewType(false), Unique: utils.NewType(false),
	})
	if err != nil {
		return fmt.Errorf("create preview covering index: %w", err)
	}
	if index.Name != name || !previewCoveringIndexMatches(index, fields) {
		return fmt.Errorf("preview covering index was not selected by Arango")
	}
	return nil
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
	if fields[2] != "auth_resource_path" && !categoryOrdered {
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
