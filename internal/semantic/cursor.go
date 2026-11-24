package semantic

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/digest"
)

type semanticInventoryCursor struct {
	Version       int    `json:"v"`
	RequestDigest string `json:"r"`
	BindingID     string `json:"b"`
	ConceptID     string `json:"c"`
}

func PageDigest(opts PageOptions, buildID string, limit int) string {
	paths := append([]string(nil), opts.AuthResourcePaths...)
	sort.Strings(paths)
	paths = slices.Compact(paths)
	unrestricted := len(paths) == 0
	if opts.AuthResourcePathsUnrestricted != nil {
		unrestricted = *opts.AuthResourcePathsUnrestricted
	}
	parts := []string{
		"semantic-inventory-page/v1",
		opts.Project,
		strings.TrimSpace(opts.DatasetGeneration),
		buildID,
		strconv.FormatBool(unrestricted),
		opts.ResourceType,
		opts.BindingID,
		opts.Query,
		strconv.Itoa(limit),
	}

	if opts.SelectionMode != "" && opts.SelectionMode != ExactConcept {
		parts[0] = "semantic-inventory-page/" + string(opts.SelectionMode) + "/v1"
	}
	parts = append(parts, paths...)
	return digest.Digest(parts...)
}

func EncodeCursor(opts PageOptions, buildID, bindingID, conceptID string) string {
	limit := semanticInventoryLimit(opts.Limit)
	cursor := semanticInventoryCursor{
		Version:       1,
		RequestDigest: PageDigest(opts, buildID, limit),
		BindingID:     bindingID,
		ConceptID:     conceptID,
	}
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func DecodeCursor(raw string, opts PageOptions, buildID string) (string, string, error) {
	if raw == "" {
		return "", "", nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", "", ErrSemanticInventoryCursorMismatch
	}
	var cursor semanticInventoryCursor
	if err := json.Unmarshal(encoded, &cursor); err != nil || cursor.Version != 1 {
		return "", "", ErrSemanticInventoryCursorMismatch
	}
	limit := semanticInventoryLimit(opts.Limit)
	if cursor.RequestDigest != PageDigest(opts, buildID, limit) || cursor.BindingID == "" || cursor.ConceptID == "" {
		return "", "", ErrSemanticInventoryCursorMismatch
	}
	return cursor.BindingID, cursor.ConceptID, nil
}

func semanticInventoryLimit(limit int) int {
	if limit <= 0 || limit > PageLimit {
		return PageLimit
	}
	return limit
}
