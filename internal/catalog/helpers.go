package catalog

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

func emit(event string, fields map[string]any) {
	payload := map[string]any{"event": event}
	for key, value := range fields {
		payload[key] = value
	}
	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Fprintln(os.Stdout, string(data))
}

func secondsSince(start time.Time) float64 {
	return time.Since(start).Seconds()
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func int64Value(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case float32:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("unsupported numeric type %T", value)
	}
}

func int64Must(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int32:
		return int64(typed)
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	default:
		return 0
	}
}

func boolValue(value any) bool {
	v, _ := value.(bool)
	return v
}

func stringSliceValue(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func fieldCatalogKey(project, authResourcePath, resourceType, path string) string {
	return sanitizeCollectionKey(project + "::" + authResourcePath + "::" + resourceType + "::" + path)
}

const generationFieldCatalogKeyPrefix = "gfc_"

// fieldCatalogKeyForGeneration returns the persistent catalog document key for
// one profiler identity. The empty-generation branch deliberately calls the
// pre-generation key function unchanged: existing catalogs and callers that
// have not selected a generation must retain their exact legacy key layout.
//
// A non-empty generation uses a SHA-256 digest of every identity component.
// In particular, it must not append a sanitized generation string to the
// legacy key: sanitization is many-to-one (for example, a slash and a space
// both become an underscore) and would allow one immutable generation to
// overwrite another catalog row.
func fieldCatalogKeyForGeneration(project, datasetGeneration, authResourcePath, resourceType, path string) string {
	datasetGeneration = NormalizeDatasetGeneration(datasetGeneration)
	if datasetGeneration == "" {
		return fieldCatalogKey(project, authResourcePath, resourceType, path)
	}
	return generationFieldCatalogKeyPrefix + catalogIdentityDigest(
		"field-catalog/v1",
		project,
		datasetGeneration,
		authResourcePath,
		resourceType,
		path,
	)
}

// catalogIdentityDigest hashes a length-prefixed sequence rather than a
// delimiter-joined string. That keeps the input encoding injective even if a
// project, generation, or path itself contains a delimiter or NUL byte.
func catalogIdentityDigest(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func sanitizeCollectionKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			strings.ContainsRune("_-:.@()+,=;$!*'", r):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func appendPath(prefix, key string, array bool) string {
	if prefix == "" {
		if array {
			return key + "[]"
		}
		return key
	}
	if array {
		return prefix + "." + key + "[]"
	}
	return prefix + "." + key
}

func appendAccessor(accessor []pathStep, step pathStep) []pathStep {
	out := append([]pathStep(nil), accessor...)
	out = append(out, step)
	return out
}

func extractAccessorValues(root any, accessor []pathStep) ([]any, bool) {
	nodes := []any{root}
	for _, step := range accessor {
		next := make([]any, 0, len(nodes))
		for _, node := range nodes {
			obj, ok := node.(map[string]any)
			if !ok {
				continue
			}
			value, ok := obj[step.field]
			if !ok || value == nil {
				continue
			}
			if step.iterateArray {
				items, ok := value.([]any)
				if !ok {
					continue
				}
				next = append(next, items...)
				continue
			}
			next = append(next, value)
		}
		if len(next) == 0 {
			return nil, false
		}
		nodes = next
	}
	if len(nodes) == 0 {
		return nil, false
	}
	return nodes, true
}

func scalarStringValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		if typed {
			return "true", true
		}
		return "false", true
	case float64:
		return fmt.Sprintf("%v", typed), true
	case float32:
		return fmt.Sprintf("%v", typed), true
	case int:
		return fmt.Sprintf("%d", typed), true
	case int64:
		return fmt.Sprintf("%d", typed), true
	case int32:
		return fmt.Sprintf("%d", typed), true
	default:
		return "", false
	}
}

func codeableConceptColumns(value map[string]any) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	appendValue := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if _, ok := seen[text]; ok {
			return
		}
		seen[text] = struct{}{}
		out = append(out, text)
	}
	if text, ok := value["text"].(string); ok {
		appendValue(text)
	}
	if codingValues, ok := value["coding"].([]any); ok {
		for _, raw := range codingValues {
			coding, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if display, ok := coding["display"].(string); ok {
				appendValue(display)
				continue
			}
			if code, ok := coding["code"].(string); ok {
				appendValue(code)
			}
		}
	}
	return out
}

func sortedKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func shapeFingerprintForValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := sortedKeys(typed)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, key+":"+shapeFingerprintForValue(typed[key]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		childPrints := make([]string, 0, len(typed))
		seen := make(map[string]struct{})
		for _, item := range typed {
			fingerprint := shapeFingerprintForValue(item)
			if _, ok := seen[fingerprint]; ok {
				continue
			}
			seen[fingerprint] = struct{}{}
			childPrints = append(childPrints, fingerprint)
		}
		sort.Strings(childPrints)
		return "[" + strings.Join(childPrints, "|") + "]"
	case string:
		return "s"
	case bool:
		return "b"
	case float64, float32, int, int32, int64:
		return "n"
	case nil:
		return "0"
	default:
		return fmt.Sprintf("%T", value)
	}
}
