package arango

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/utils"
)

const pivotPreviewIndexPrefix = "loom_pivot_preview_"
const maxPivotPreviewIndexesPerCollection = 4

var ErrPivotPreviewIndexLimit = errors.New("pivot preview index limit reached")

// EnsurePivotPreviewIndex adds one bounded, compiler-selected covering index.
// The caller's AQL hint is non-forcing, so previews continue on the canonical
// plan if index preparation is unavailable.
func (c *Client) EnsurePivotPreviewIndex(ctx context.Context, collection, name string, fields []string) error {
	if c == nil || !validPivotPreviewIndex(collection, name, fields) {
		return fmt.Errorf("invalid pivot preview index specification")
	}
	c.previewIndexMu.Lock()
	defer c.previewIndexMu.Unlock()

	col, err := c.db.GetCollection(ctx, collection, nil)
	if err != nil {
		return fmt.Errorf("open pivot preview collection: %w", err)
	}
	indexes, err := col.Indexes(ctx)
	if err != nil {
		return fmt.Errorf("list pivot preview indexes: %w", err)
	}
	count := 0
	for _, index := range indexes {
		if index.Name == name {
			if index.Type != driver.IndexType("persistent") || index.RegularIndex == nil || !reflect.DeepEqual(index.RegularIndex.Fields, fields) {
				return fmt.Errorf("pivot preview index name already has different fields")
			}
			return nil
		}
		if strings.HasPrefix(index.Name, pivotPreviewIndexPrefix) {
			count++
		}
	}
	if count >= maxPivotPreviewIndexesPerCollection {
		return ErrPivotPreviewIndexLimit
	}
	index, _, err := col.EnsurePersistentIndex(ctx, fields, &driver.CreatePersistentIndexOptions{
		Name: name, Sparse: utils.NewType(false), Unique: utils.NewType(false),
	})
	if err != nil {
		return fmt.Errorf("create pivot preview index: %w", err)
	}
	if index.Name != name || index.RegularIndex == nil || !reflect.DeepEqual(index.RegularIndex.Fields, fields) {
		return fmt.Errorf("pivot preview covering index was not selected by Arango")
	}
	return nil
}

func validPivotPreviewIndex(collection, name string, fields []string) bool {
	if collection == "" || !validIndexPath(collection) || !strings.HasPrefix(name, pivotPreviewIndexPrefix) || !validIndexPath(name) {
		return false
	}
	if len(fields) < 4 || len(fields) > 32 || fields[0] != "project" || fields[1] != "dataset_generation" || fields[2] != "auth_resource_path" {
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
