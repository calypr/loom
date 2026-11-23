package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/utils"
	"slices"
	"strings"
	"time"
	"unicode"
)

const previewCoveringIndexPrefix = "loom_pivot_preview_"

const maxPreviewCoveringIndexesPerCollection = 4

const previewCoveringIndexCleanupTimeout = 3 * time.Second

var ErrPreviewCoveringIndexLimit = errors.New("preview covering index limit reached")

type PreviewCoveringIndex struct {
	Collection   string
	Name         string
	Fields       []string
	StoredValues []string
}

type PreviewIndexes struct {
	client *Client
}

func (preview PreviewIndexes) ListPreviewCoveringIndexes(ctx context.Context, collection string) ([]PreviewCoveringIndex, error) {
	c := preview.client
	if c == nil || c.db == nil || collection == "" || !validIndexPath(collection) {
		return nil, fmt.Errorf("invalid preview covering collection")
	}
	col, err := c.db.GetCollection(ctx, collection, nil)
	if err != nil {
		return nil, fmt.Errorf("open preview covering collection: %w", err)
	}
	indexes, err := col.Indexes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list preview covering indexes: %w", err)
	}
	return previewCoveringIndexDefinitions(collection, indexes), nil
}

func (preview PreviewIndexes) EnsurePreviewCoveringIndex(ctx context.Context, collection, name string, fields []string) error {
	return preview.ensurePreviewCoveringIndex(ctx, collection, name, fields)
}

// Essential: EnsurePreviewCoveringIndexWithStoredValues provisions a compiler-owned
// projection index. Stored values are validated and verified as part of the
// exact index definition because Arango's ensure operation does not distinguish
// otherwise identical field tuples by stored-value configuration.
func (preview PreviewIndexes) EnsurePreviewCoveringIndexWithStoredValues(ctx context.Context, collection, name string, fields, storedValues []string) error {
	c := preview.client
	if c == nil || !validPreviewCoveringIndexWithStoredValues(collection, name, fields, storedValues) {
		return fmt.Errorf("invalid preview covering index specification")
	}
	return preview.ensurePreviewCoveringIndexDefinition(ctx, collection, name, fields, storedValues)
}

func (preview PreviewIndexes) ensurePreviewCoveringIndex(ctx context.Context, collection, name string, fields []string) error {
	c := preview.client
	if c == nil || !validPreviewCoveringIndex(collection, name, fields) {
		return fmt.Errorf("invalid preview covering index specification")
	}
	return preview.ensurePreviewCoveringIndexDefinition(ctx, collection, name, fields, nil)
}

func (preview PreviewIndexes) ensurePreviewCoveringIndexDefinition(ctx context.Context, collection, name string, fields, storedValues []string) error {
	c := preview.client
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
	return ensurePreviewCoveringIndexDefinition(ctx, col, name, fields, storedValues)
}

type previewIndexCollection interface {
	Indexes(context.Context) ([]driver.IndexResponse, error)
	EnsurePersistentIndex(context.Context, []string, *driver.CreatePersistentIndexOptions) (driver.IndexResponse, bool, error)
	DeleteIndexByID(context.Context, string) error
}

func previewCoveringIndexDefinitions(collection string, indexes []driver.IndexResponse) []PreviewCoveringIndex {
	definitions := make([]PreviewCoveringIndex, 0, len(indexes))
	for _, index := range indexes {
		if index.Type != driver.IndexType("persistent") || index.RegularIndex == nil ||
			index.Unique == nil || *index.Unique || index.Sparse == nil || *index.Sparse ||
			!validPreviewCoveringIndexDefinition(collection, index.Name, index.RegularIndex.Fields, index.RegularIndex.StoredValues) {
			continue
		}
		definitions = append(definitions, PreviewCoveringIndex{
			Collection:   collection,
			Name:         index.Name,
			Fields:       append([]string(nil), index.RegularIndex.Fields...),
			StoredValues: append([]string(nil), index.RegularIndex.StoredValues...),
		})
	}
	return definitions
}

func ensurePreviewCoveringIndex(ctx context.Context, col previewIndexCollection, name string, fields []string) error {
	return ensurePreviewCoveringIndexDefinition(ctx, col, name, fields, nil)
}

func ensurePreviewCoveringIndexWithStoredValues(ctx context.Context, col previewIndexCollection, name string, fields, storedValues []string) error {
	return ensurePreviewCoveringIndexDefinition(ctx, col, name, fields, storedValues)
}

func ensurePreviewCoveringIndexDefinition(ctx context.Context, col previewIndexCollection, name string, fields, storedValues []string) error {
	if len(storedValues) != 0 && !validPreviewCoveringIndexStoredValuesDefinition(name, fields, storedValues) {
		return fmt.Errorf("invalid preview covering index specification")
	}
	indexes, err := col.Indexes(ctx)
	if err != nil {
		return fmt.Errorf("list preview covering indexes: %w", err)
	}
	count := 0
	var existing *driver.IndexResponse
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
	}
	if existing != nil {
		return nil
	}
	if count >= maxPreviewCoveringIndexesPerCollection {
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

func previewCoveringIndexMatches(index driver.IndexResponse, fields []string) bool {
	return previewCoveringIndexMatchesDefinition(index, fields, nil)
}

func previewCoveringIndexMatchesDefinition(index driver.IndexResponse, fields, storedValues []string) bool {
	return index.Type == driver.IndexType("persistent") && index.RegularIndex != nil &&
		index.Unique != nil && !*index.Unique && index.Sparse != nil && !*index.Sparse &&
		slices.Equal(index.RegularIndex.Fields, fields) && slices.Equal(index.RegularIndex.StoredValues, storedValues)
}

func validPreviewCoveringIndexWithStoredValues(collection, name string, fields, storedValues []string) bool {
	return validPreviewCoveringIndexFieldSet(collection, name, fields) &&
		validPreviewCoveringIndexStoredValuesDefinition(name, fields, storedValues)
}

func previewCoveringIndexName(collection string, fields []string) string {
	digest := sha256.Sum256([]byte(collection + "\x00" + strings.Join(fields, "\x00")))
	return previewCoveringIndexPrefix + hex.EncodeToString(digest[:8])
}

func previewCoveringIndexNameWithStoredValues(collection string, fields, storedValues []string) string {
	canonicalStoredValues := append([]string(nil), storedValues...)
	slices.Sort(canonicalStoredValues)
	canonical := collection + "\x00fields\x00" + strings.Join(fields, "\x00") +
		"\x00storedValues\x00" + strings.Join(canonicalStoredValues, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return previewCoveringIndexPrefix + hex.EncodeToString(digest[:8])
}

func validPreviewCoveringIndexStoredValuesDefinition(name string, fields, storedValues []string) bool {
	if !strings.HasPrefix(name, previewCoveringIndexPrefix) || !validIndexPath(name) ||
		len(fields) > 32 || len(storedValues) == 0 || len(storedValues) > 32 {
		return false
	}
	legacyFields := len(fields) >= 5 &&
		slices.Equal(fields[:4], []string{"project", "dataset_generation", "auth_resource_path", "_key"})
	newFields := len(fields) >= 2 && slices.Equal(fields[:2], []string{"project", "dataset_generation"})
	if !legacyFields && !newFields {
		return false
	}
	fieldSet := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if !validIndexPath(field) {
			return false
		}
		fieldSet[field] = struct{}{}
	}
	if newFields && !legacyFields {
		for _, field := range fields[2:] {
			if field != "_key" && !strings.HasPrefix(field, "payload.") {
				return false
			}
		}
	}
	lastPayloadField := ""
	fieldOffset := 2
	if legacyFields {
		fieldOffset = 4
	}
	for _, field := range fields[fieldOffset:] {
		if field == "_key" {
			continue
		}
		if legacyFields && field == "resourceType" && field == fields[len(fields)-1] {
			continue
		}
		if strings.HasPrefix(field, "payload.") && lastPayloadField != "" && lastPayloadField >= field {
			return false
		}
		if strings.HasPrefix(field, "payload.") {
			for previous := fieldOffset; previous < len(fields); previous++ {
				if fields[previous] == field {
					break
				}
				if previewIndexPathsOverlap(fields[previous], field) {
					return false
				}
			}
			lastPayloadField = field
		} else if field != "_key" {
			return false
		}
	}
	seen := make(map[string]struct{}, len(storedValues))
	lastStoredValue := ""
	storedAuth := false
	for _, path := range storedValues {
		if !validIndexPath(path) || (path != "auth_resource_path" && path != "resourceType" && !strings.HasPrefix(path, "payload.")) ||
			(lastStoredValue != "" && lastStoredValue >= path) {
			return false
		}
		storedAuth = storedAuth || path == "auth_resource_path"
		lastStoredValue = path
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
	return legacyFields || storedAuth
}

func previewIndexPathsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+".") || strings.HasPrefix(right, left+".")
}

func validPreviewCoveringIndex(collection, name string, fields []string) bool {
	return validPreviewCoveringIndexFieldSet(collection, name, fields) && !newStoredPreviewIndexFields(fields)
}

func validPreviewCoveringIndexDefinition(collection, name string, fields, storedValues []string) bool {
	if len(storedValues) != 0 {
		return validPreviewCoveringIndexWithStoredValues(collection, name, fields, storedValues)
	}
	return validPreviewCoveringIndex(collection, name, fields)
}

func validPreviewCoveringIndexFieldSet(collection, name string, fields []string) bool {
	if collection == "" || !validIndexPath(collection) || !strings.HasPrefix(name, previewCoveringIndexPrefix) || !validIndexPath(name) {
		return false
	}
	if len(fields) < 3 || len(fields) > 32 || fields[0] != "project" || fields[1] != "dataset_generation" {
		return false
	}
	newStoredLayout := newStoredPreviewIndexFields(fields)
	categoryOrdered := len(fields) == 4 && strings.HasPrefix(fields[2], "payload.") && fields[3] == "auth_resource_path"
	relatedCategoryTypeOrdered := len(fields) == 5 && fields[2] == "resourceType" &&
		strings.HasPrefix(fields[3], "payload.") && fields[4] == "auth_resource_path"
	if fields[2] != "auth_resource_path" && !categoryOrdered && !relatedCategoryTypeOrdered && !newStoredLayout {
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

func newStoredPreviewIndexFields(fields []string) bool {
	if len(fields) < 3 || fields[0] != "project" || fields[1] != "dataset_generation" {
		return false
	}
	if fields[2] != "_key" && !strings.HasPrefix(fields[2], "payload.") {
		return false
	}
	lastPayload := ""
	seenPayload := make(map[string]struct{}, len(fields)-2)
	for _, field := range fields[2:] {
		if field != "_key" && !strings.HasPrefix(field, "payload.") {
			return false
		}
		if strings.HasPrefix(field, "payload.") {
			if lastPayload != "" && lastPayload >= field {
				return false
			}
			for previous := range seenPayload {
				if previewIndexPathsOverlap(previous, field) {
					return false
				}
			}
			seenPayload[field] = struct{}{}
			lastPayload = field
		}
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
