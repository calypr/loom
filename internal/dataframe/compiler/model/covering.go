package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const PreviewCoveringIndexNamePrefix = "loom_pivot_preview_"

func PreviewCoveringIndexPrefixFields() []string {
	return []string{"project", "dataset_generation"}
}

func PreviewQuantityCategoryFields() []string {
	return []string{"project", "dataset_generation", "_key", "payload.id", "payload.status"}
}

func PreviewQuantityCategoryStoredValues() []string {
	return []string{"auth_resource_path", "payload.valueQuantity", "resourceType"}
}

func PreviewCoveringIndexSpecForSourcePaths(collection string, projectionPaths []string) *PreviewCoveringIndexSpec {
	if strings.TrimSpace(collection) == "" {
		return nil
	}
	pathSet := make(map[string]struct{}, len(projectionPaths)+len(PreviewCoveringIndexPrefixFields()))
	for _, field := range PreviewCoveringIndexPrefixFields() {
		pathSet[field] = struct{}{}
	}
	for _, path := range projectionPaths {
		if path == "auth_resource_path" {
			continue
		}
		pathSet[path] = struct{}{}
	}
	pathSet["_key"] = struct{}{}
	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	fields := PreviewCoveringIndexPrefixFields()
	for _, path := range paths {
		if slices.Contains(fields, path) {
			continue
		}
		fields = append(fields, path)
	}
	if len(fields) < 3 || len(fields) > 32 {
		return nil
	}
	storedValues := []string{"auth_resource_path"}
	name := PreviewCoveringIndexNameWithStoredValues(collection, fields, storedValues)
	return &PreviewCoveringIndexSpec{Collection: collection, Name: name, Fields: fields, StoredValues: storedValues}
}

func PreviewCoveringProjectionPath(projection ir.PhysicalProjection, rootVariable string) (string, bool) {
	if projection.Expression == nil {
		if projection.Value.Variable != rootVariable || !ValidPreviewIndexPath(projection.Value.Path) {
			return "", false
		}
		return strings.Join(projection.Value.Path, "."), true
	}
	expression := projection.Expression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Cardinality != ir.PhysicalScalarCardinality || expression.Extract == nil {
		return "", false
	}
	extract := expression.Extract
	if extract.ExecutionMode != ir.PhysicalSelectorDirectScalar || extract.Source.Variable != rootVariable ||
		len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" || len(extract.Fallbacks) != 0 ||
		extract.Distinct || extract.Prepared != nil || extract.UnitNormalization != nil || extract.Selector.Filter != nil ||
		len(extract.Selector.Steps) == 0 {
		return "", false
	}
	path := append([]string(nil), extract.Source.Path...)
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return "", false
		}
		path = append(path, step.Field)
	}
	if !ValidPreviewIndexPath(path) {
		return "", false
	}
	return strings.Join(path, "."), true
}

func PreviewCoveringProjectionPaths(projections []ir.PhysicalProjection, rootVariable string) ([]string, bool) {
	if len(projections) == 0 {
		return nil, false
	}
	paths := make([]string, 0, len(projections))
	seenNames := make(map[string]struct{}, len(projections))
	for _, projection := range projections {
		if projection.Name == "" {
			return nil, false
		}
		if _, exists := seenNames[projection.Name]; exists {
			return nil, false
		}
		seenNames[projection.Name] = struct{}{}
		path, ok := PreviewCoveringProjectionPath(projection, rootVariable)
		if !ok {
			return nil, false
		}
		paths = append(paths, path)
	}
	return paths, true
}

func ValidPreviewIndexPath(path []string) bool {
	if len(path) == 0 {
		return false
	}
	for _, part := range path {
		if part == "" {
			return false
		}
		for index, char := range part {
			valid := char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || index > 0 && char >= '0' && char <= '9'
			if !valid {
				return false
			}
		}
	}
	return true
}

func PreviewCoveringIndexName(collection string, fields []string) string {
	canonical := collection + "\x00" + strings.Join(fields, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%s%s", PreviewCoveringIndexNamePrefix, hex.EncodeToString(digest[:8]))
}

func PreviewCoveringIndexNameWithStoredValues(collection string, fields, storedValues []string) string {
	if len(storedValues) == 0 {
		return PreviewCoveringIndexName(collection, fields)
	}
	canonicalStoredValues := append([]string(nil), storedValues...)
	sort.Strings(canonicalStoredValues)
	canonical := collection + "\x00fields\x00" + strings.Join(fields, "\x00") +
		"\x00storedValues\x00" + strings.Join(canonicalStoredValues, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%s%s", PreviewCoveringIndexNamePrefix, hex.EncodeToString(digest[:8]))
}
