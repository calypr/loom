// Package plan contains immutable, backend-neutral plans produced after
// recipe validation and scoped schema discovery.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type DynamicSpec struct {
	Name        string
	AllowedKeys []string
	MaxColumns  int
	Collision   string
}

type Candidate struct {
	Key       string
	ValueType string
}

type Column struct {
	Name      string `json:"name"`
	ValueType string `json:"valueType"`
	SourceKey string `json:"sourceKey,omitempty"`
}

type FrozenSchema struct {
	Columns []Column `json:"columns"`
	Digest  string   `json:"digest"`
}

// Freeze converts discovery candidates into a deterministic bounded schema.
// It does not perform discovery itself; callers provide candidates obtained
// through the same scoped physical plan used for execution.
func Freeze(spec DynamicSpec, candidates []Candidate) (FrozenSchema, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return FrozenSchema{}, fmt.Errorf("dynamic column name is required")
	}
	if spec.MaxColumns < 0 {
		return FrozenSchema{}, fmt.Errorf("dynamic column max must not be negative")
	}
	policy := spec.Collision
	if policy == "" {
		policy = "error"
	}
	if policy != "error" && policy != "overwrite" && policy != "coalesce" {
		return FrozenSchema{}, fmt.Errorf("unsupported dynamic collision policy %q", policy)
	}
	allowed := map[string]struct{}{}
	for _, key := range spec.AllowedKeys {
		allowed[key] = struct{}{}
	}
	columns := map[string]Column{}
	for _, candidate := range candidates {
		if len(allowed) > 0 {
			if _, ok := allowed[candidate.Key]; !ok {
				continue
			}
		}
		key := sanitize(candidate.Key)
		if key == "" {
			continue
		}
		name := spec.Name + "_" + key
		column := Column{Name: name, ValueType: candidate.ValueType, SourceKey: candidate.Key}
		if column.ValueType == "" {
			column.ValueType = "unknown"
		}
		if existing, ok := columns[name]; ok {
			if policy == "error" {
				return FrozenSchema{}, fmt.Errorf("dynamic key collision at %q", name)
			}
			if existing.ValueType != column.ValueType && policy != "coalesce" {
				return FrozenSchema{}, fmt.Errorf("dynamic column %q has incompatible types %q and %q", name, existing.ValueType, column.ValueType)
			}
			if policy == "coalesce" && existing.ValueType == "unknown" {
				columns[name] = column
			}
			continue
		}
		if spec.MaxColumns > 0 && len(columns) >= spec.MaxColumns {
			return FrozenSchema{}, fmt.Errorf("dynamic column limit %d exceeded", spec.MaxColumns)
		}
		columns[name] = column
	}
	result := FrozenSchema{Columns: make([]Column, 0, len(columns))}
	for _, column := range columns {
		result.Columns = append(result.Columns, column)
	}
	sort.Slice(result.Columns, func(i, j int) bool { return result.Columns[i].Name < result.Columns[j].Name })
	canonical, err := json.Marshal(result.Columns)
	if err != nil {
		return FrozenSchema{}, err
	}
	sum := sha256.Sum256(canonical)
	result.Digest = hex.EncodeToString(sum[:])
	return result, nil
}

var invalidColumn = regexp.MustCompile(`[^A-Za-z0-9_]`)

func sanitize(value string) string {
	value = invalidColumn.ReplaceAllString(value, "_")
	if value == "" {
		return "_"
	}
	if value[0] >= '0' && value[0] <= '9' {
		value = "_" + value
	}
	if strings.HasPrefix(value, "__") {
		value = "_" + strings.TrimPrefix(value, "__")
	}
	return value
}
