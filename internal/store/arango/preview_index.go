package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// EnsurePreviewCoveringIndexWithStoredValues provisions a compiler-owned
// projection index. Stored values are validated and verified as part of the
// exact index definition because Arango's ensure operation does not distinguish
// otherwise identical field tuples by stored-value configuration.
func (c *Client) EnsurePreviewCoveringIndexWithStoredValues(ctx context.Context, collection, name string, fields, storedValues []string) error {
	if c == nil || !validPreviewCoveringIndexWithStoredValues(collection, name, fields, storedValues) {
		return fmt.Errorf("invalid preview covering index specification")
	}
	return c.ensurePreviewCoveringIndexDefinition(ctx, collection, name, fields, storedValues, nil)
}

// EnsurePreviewCoveringIndexWithStoredValuesReplacing provisions a
// compiler-owned stored-values index and replaces only the exact older
// compiler-owned field-only definition supplied by the same plan.
func (c *Client) EnsurePreviewCoveringIndexWithStoredValuesReplacing(ctx context.Context, collection, name string, fields, storedValues []string, supersededName string, supersededFields []string) error {
	replacement := &previewIndexReplacement{name: supersededName, fields: append([]string(nil), supersededFields...)}
	if c == nil || !validPreviewCoveringIndexStoredValuesReplacement(collection, name, fields, storedValues, replacement) {
		return fmt.Errorf("invalid preview covering index replacement specification")
	}
	return c.ensurePreviewCoveringIndexDefinition(ctx, collection, name, fields, storedValues, replacement)
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
	return c.ensurePreviewCoveringIndexDefinition(ctx, collection, name, fields, nil, replacement)
}

func (c *Client) ensurePreviewCoveringIndexDefinition(ctx context.Context, collection, name string, fields, storedValues []string, replacement *previewIndexReplacement) error {
	valid := validPreviewCoveringIndex(collection, name, fields)
	if len(storedValues) != 0 {
		valid = validPreviewCoveringIndexWithStoredValues(collection, name, fields, storedValues)
	}
	if c == nil || !valid {
		return fmt.Errorf("invalid preview covering index specification")
	}
	c.previewIndexMu.Lock()
	defer c.previewIndexMu.Unlock()

	col, err := c.db.GetCollection(ctx, collection, nil)
	if err != nil {
		return fmt.Errorf("open preview covering collection: %w", err)
	}
	return ensurePreviewCoveringIndexDefinition(ctx, col, name, fields, storedValues, replacement)
}

func ensurePreviewCoveringIndex(ctx context.Context, col previewIndexCollection, name string, fields []string, replacement *previewIndexReplacement) error {
	return ensurePreviewCoveringIndexDefinition(ctx, col, name, fields, nil, replacement)
}

func ensurePreviewCoveringIndexWithStoredValues(ctx context.Context, col previewIndexCollection, name string, fields, storedValues []string) error {
	return ensurePreviewCoveringIndexDefinition(ctx, col, name, fields, storedValues, nil)
}

func ensurePreviewCoveringIndexDefinition(ctx context.Context, col previewIndexCollection, name string, fields, storedValues []string, replacement *previewIndexReplacement) error {
	if len(storedValues) != 0 && !validPreviewCoveringIndexStoredValuesDefinition(name, fields, storedValues) {
		return fmt.Errorf("invalid preview covering index specification")
	}
	indexes, err := col.Indexes(ctx)
	if err != nil {
		return fmt.Errorf("list preview covering indexes: %w", err)
	}
	count := 0
	var existing *driver.IndexResponse
	var superseded *driver.IndexResponse
	for _, index := range indexes {
		if index.Name == name {
			if !previewCoveringIndexMatchesDefinition(index, fields, storedValues) {
				return fmt.Errorf("preview covering index name already has different fields or stored values")
			}
			copy := index
			existing = &copy
		}
		if strings.HasPrefix(index.Name, previewCoveringIndexPrefix) {
			count++
		}
		if replacement != nil && index.Name == replacement.name && previewCoveringIndexMatchesDefinition(index, replacement.fields, nil) {
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
		Name: name, StoredValues: append([]string(nil), storedValues...),
		Sparse: utils.NewType(false), Unique: utils.NewType(false),
	})
	if err != nil {
		return fmt.Errorf("create preview covering index: %w", err)
	}
	if index.Name != name || !previewCoveringIndexMatchesDefinition(index, fields, storedValues) {
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
	return previewCoveringIndexMatchesDefinition(index, fields, nil)
}

func previewCoveringIndexMatchesDefinition(index driver.IndexResponse, fields, storedValues []string) bool {
	return index.Type == driver.IndexType("persistent") && index.RegularIndex != nil &&
		index.Unique != nil && !*index.Unique && index.Sparse != nil && !*index.Sparse &&
		slices.Equal(index.RegularIndex.Fields, fields) && slices.Equal(index.RegularIndex.StoredValues, storedValues)
}

func validPreviewCoveringIndexWithStoredValues(collection, name string, fields, storedValues []string) bool {
	return validPreviewCoveringIndex(collection, name, fields) &&
		validPreviewCoveringIndexStoredValuesDefinition(name, fields, storedValues)
}

func validPreviewCoveringIndexStoredValuesReplacement(collection, name string, fields, storedValues []string, replacement *previewIndexReplacement) bool {
	return validPreviewCoveringIndexWithStoredValues(collection, name, fields, storedValues) &&
		replacement != nil && replacement.name != name &&
		validPreviewCoveringIndex(collection, replacement.name, replacement.fields) &&
		previewCoveringIndexName(collection, replacement.fields) == replacement.name
}

// previewCoveringIndexName is kept in lockstep with the compiler's stable
// field-only name derivation so replacement is limited to an exact owned spec.
func previewCoveringIndexName(collection string, fields []string) string {
	digest := sha256.Sum256([]byte(collection + "\x00" + strings.Join(fields, "\x00")))
	return previewCoveringIndexPrefix + hex.EncodeToString(digest[:8])
}

func validPreviewCoveringIndexStoredValuesDefinition(name string, fields, storedValues []string) bool {
	if !strings.HasPrefix(name, previewCoveringIndexPrefix) || !validIndexPath(name) ||
		len(fields) < 5 || len(fields) > 32 ||
		!slices.Equal(fields[:4], []string{"project", "dataset_generation", "auth_resource_path", "_key"}) ||
		len(storedValues) == 0 || len(storedValues) > 32 {
		return false
	}
	fieldSet := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if !validIndexPath(field) {
			return false
		}
		fieldSet[field] = struct{}{}
	}
	for index, field := range fields[4:] {
		if !strings.HasPrefix(field, "payload.") || (index > 0 && fields[index+3] >= field) {
			return false
		}
		for previous := 4; previous < index+4; previous++ {
			if previewIndexPathsOverlap(fields[previous], field) {
				return false
			}
		}
	}
	seen := make(map[string]struct{}, len(storedValues))
	for _, path := range storedValues {
		if !strings.HasPrefix(path, "payload.") || !validIndexPath(path) {
			return false
		}
		for existing := range seen {
			if previewIndexPathsOverlap(existing, path) {
				return false
			}
		}
		for field := range fieldSet {
			if previewIndexPathsOverlap(field, path) {
				return false
			}
		}
		seen[path] = struct{}{}
	}
	return true
}

func previewIndexPathsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+".") || strings.HasPrefix(right, left+".")
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
