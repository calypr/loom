// Package lineage defines stable identities and provenance for columns that a
// single authored source slot expands into at compile time.
package lineage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const sourceChildPrefix = "source_child_v1_"

type SourceChildKind string

const (
	IndexedValueChild  SourceChildKind = "INDEXED_VALUE"
	RepeatedCountChild SourceChildKind = "REPEATED_COUNT"
)

// Coordinate identifies one concrete item at a repeated boundary. Width is
// intentionally absent: discovering more items must not change old IDs.
type Coordinate struct {
	BoundaryPath string `json:"boundaryPath"`
	Index        int    `json:"index"`
}

// SourceChild records the source slot and structural evidence that produced
// one public child column. ParentColumnIDs is sorted and may contain several
// owners when indexed projections share a repeated-count emission.
type SourceChild struct {
	Kind            SourceChildKind `json:"kind"`
	ParentColumnIDs []string        `json:"parentColumnIds"`
	OccurrenceID    string          `json:"occurrenceId"`
	SourcePath      string          `json:"sourcePath"`
	BoundaryPath    string          `json:"boundaryPath,omitempty"`
	Coordinates     []Coordinate    `json:"coordinates,omitempty"`
}

// StableSourceChildID returns the deterministic public identity for a source
// child. Names, labels, and observed widths are presentation or catalog facts
// and are deliberately excluded.
func StableSourceChildID(child SourceChild) (string, SourceChild, error) {
	child = cloneSourceChild(child)
	if child.Kind != IndexedValueChild && child.Kind != RepeatedCountChild {
		return "", SourceChild{}, fmt.Errorf("unsupported source child kind %q", child.Kind)
	}
	if strings.TrimSpace(child.OccurrenceID) == "" || child.OccurrenceID != strings.TrimSpace(child.OccurrenceID) {
		return "", SourceChild{}, fmt.Errorf("source child occurrenceId is required")
	}
	if strings.TrimSpace(child.SourcePath) == "" || child.SourcePath != strings.TrimSpace(child.SourcePath) {
		return "", SourceChild{}, fmt.Errorf("source child sourcePath is required")
	}
	if len(child.ParentColumnIDs) == 0 {
		return "", SourceChild{}, fmt.Errorf("source child requires at least one parent column ID")
	}
	sort.Strings(child.ParentColumnIDs)
	for index, parentID := range child.ParentColumnIDs {
		if strings.TrimSpace(parentID) == "" || parentID != strings.TrimSpace(parentID) {
			return "", SourceChild{}, fmt.Errorf("source child parent column IDs must be non-empty and trimmed")
		}
		if index > 0 && child.ParentColumnIDs[index-1] == parentID {
			return "", SourceChild{}, fmt.Errorf("source child parent column ID %q is duplicated", parentID)
		}
	}
	if child.Kind == IndexedValueChild {
		if len(child.ParentColumnIDs) != 1 || child.BoundaryPath != "" || len(child.Coordinates) == 0 {
			return "", SourceChild{}, fmt.Errorf("indexed value child requires one parent and at least one coordinate")
		}
	} else if strings.TrimSpace(child.BoundaryPath) == "" {
		return "", SourceChild{}, fmt.Errorf("repeated count child requires a boundary path")
	}
	for index, coordinate := range child.Coordinates {
		if strings.TrimSpace(coordinate.BoundaryPath) == "" || coordinate.BoundaryPath != strings.TrimSpace(coordinate.BoundaryPath) || coordinate.Index < 0 {
			return "", SourceChild{}, fmt.Errorf("source child coordinate %d is invalid", index)
		}
	}
	if child.Kind == RepeatedCountChild {
		for index, coordinate := range child.Coordinates {
			if coordinate.BoundaryPath == child.BoundaryPath {
				return "", SourceChild{}, fmt.Errorf("repeated count boundary duplicates parent coordinate %d", index)
			}
		}
	}

	identity := sourceChildIdentity{
		Kind: child.Kind, ParentColumnID: child.ParentColumnIDs[0], OccurrenceID: child.OccurrenceID,
		SourcePath: child.SourcePath, BoundaryPath: child.BoundaryPath,
		Coordinates: append([]Coordinate(nil), child.Coordinates...),
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", SourceChild{}, fmt.Errorf("encode source child identity: %w", err)
	}
	childID := sourceChildPrefix + base64.RawURLEncoding.EncodeToString(encoded)
	return childID, child, nil
}

// ParseSourceChildID verifies the canonical wire form and returns the
// structural lineage encoded in a generated child ID.
func ParseSourceChildID(value string) (SourceChild, bool) {
	if !strings.HasPrefix(value, sourceChildPrefix) {
		return SourceChild{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, sourceChildPrefix))
	if err != nil {
		return SourceChild{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var identity sourceChildIdentity
	if err := decoder.Decode(&identity); err != nil {
		return SourceChild{}, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return SourceChild{}, false
	}
	child := SourceChild{
		Kind: identity.Kind, ParentColumnIDs: []string{identity.ParentColumnID}, OccurrenceID: identity.OccurrenceID,
		SourcePath: identity.SourcePath, BoundaryPath: identity.BoundaryPath,
		Coordinates: append([]Coordinate(nil), identity.Coordinates...),
	}
	canonicalID, child, err := StableSourceChildID(child)
	if err != nil || canonicalID != value {
		return SourceChild{}, false
	}
	return child, true
}

// IsSourceChildID reports whether value uses the reserved generated-child ID
// namespace, including malformed values that ParseSourceChildID rejects.
func IsSourceChildID(value string) bool {
	return strings.HasPrefix(value, sourceChildPrefix)
}

// CloneSourceChild returns a deep copy suitable for retaining in compiler
// schemas that are copied between construction stages.
func CloneSourceChild(child *SourceChild) *SourceChild {
	if child == nil {
		return nil
	}
	cloned := cloneSourceChild(*child)
	return &cloned
}

type sourceChildIdentity struct {
	Kind           SourceChildKind `json:"kind"`
	ParentColumnID string          `json:"parentColumnId"`
	OccurrenceID   string          `json:"occurrenceId"`
	SourcePath     string          `json:"sourcePath"`
	BoundaryPath   string          `json:"boundaryPath,omitempty"`
	Coordinates    []Coordinate    `json:"coordinates,omitempty"`
}

func cloneSourceChild(child SourceChild) SourceChild {
	child.ParentColumnIDs = append([]string(nil), child.ParentColumnIDs...)
	child.Coordinates = append([]Coordinate(nil), child.Coordinates...)
	return child
}
