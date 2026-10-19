package compilation

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/capability"
)

const (
	maxIndexedBoundaryItems = 1000
	maxIndexedOutputColumns = 100000
)

type indexedProjection struct {
	Leaf        string
	Selector    string
	Coordinates []capability.RepeatedCoordinate
}

type indexedCount struct {
	Leaf        string
	Selector    string
	Coordinates []capability.RepeatedCoordinate
}

func expandIndexedProjection(baseLeaf, fieldPath string, boundaries []capability.RepeatedBoundary) ([]indexedProjection, []indexedCount, error) {
	if len(boundaries) == 0 {
		return nil, nil, fmt.Errorf("indexed projection requires complete repeated-boundary evidence")
	}
	for _, boundary := range boundaries {
		if boundary.MaxItems > maxIndexedBoundaryItems {
			return nil, nil, fmt.Errorf("repeated boundary %q observed %d items; maximum is %d", boundary.Path, boundary.MaxItems, maxIndexedBoundaryItems)
		}
		if boundary.MaxItems < 0 {
			return nil, nil, fmt.Errorf("repeated boundary %q has invalid width %d", boundary.Path, boundary.MaxItems)
		}
	}
	columnCount := 1
	for _, boundary := range boundaries {
		if boundary.MaxItems == 0 {
			columnCount = 0
			break
		}
		if columnCount > maxIndexedOutputColumns/boundary.MaxItems {
			return nil, nil, fmt.Errorf("indexed projection requires more than %d physical columns; no columns were truncated", maxIndexedOutputColumns)
		}
		columnCount *= boundary.MaxItems
	}

	coordinates := coordinateProduct(boundaries)
	projections := make([]indexedProjection, 0, len(coordinates))
	for _, coordinate := range coordinates {
		projections = append(projections, indexedProjection{
			Leaf:        indexedLeaf(baseLeaf, coordinate),
			Selector:    indexedSelector(fieldPath, coordinate),
			Coordinates: append([]capability.RepeatedCoordinate(nil), coordinate...),
		})
	}

	counts := make([]indexedCount, 0)
	for boundaryIndex, boundary := range boundaries {
		parents := coordinateProduct(boundaries[:boundaryIndex])
		if len(parents) == 0 {
			parents = [][]capability.RepeatedCoordinate{{}}
		}
		for _, parent := range parents {
			counts = append(counts, indexedCount{
				Leaf:        boundaryCountLeaf(boundary.Path, parent),
				Selector:    indexedSelector(boundary.Path, parent),
				Coordinates: append([]capability.RepeatedCoordinate(nil), parent...),
			})
		}
	}
	return projections, counts, nil
}

func coordinateProduct(boundaries []capability.RepeatedBoundary) [][]capability.RepeatedCoordinate {
	if len(boundaries) == 0 {
		return nil
	}
	product := [][]capability.RepeatedCoordinate{{}}
	for _, boundary := range boundaries {
		next := make([][]capability.RepeatedCoordinate, 0, len(product)*boundary.MaxItems)
		for _, prefix := range product {
			for index := 0; index < boundary.MaxItems; index++ {
				coordinate := append([]capability.RepeatedCoordinate(nil), prefix...)
				coordinate = append(coordinate, capability.RepeatedCoordinate{BoundaryPath: boundary.Path, Index: index, Width: boundary.MaxItems})
				next = append(next, coordinate)
			}
		}
		product = next
	}
	return product
}

func indexedSelector(path string, coordinates []capability.RepeatedCoordinate) string {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(path), "root."), ".")
	coordinateIndex := 0
	for index, part := range parts {
		if !strings.HasSuffix(part, "[]") || coordinateIndex >= len(coordinates) {
			continue
		}
		parts[index] = strings.TrimSuffix(part, "[]") + fmt.Sprintf("[%d]", coordinates[coordinateIndex].Index)
		coordinateIndex++
	}
	return strings.Join(parts, ".")
}

func indexedLeaf(base string, coordinates []capability.RepeatedCoordinate) string {
	parts := []string{base}
	for _, coordinate := range coordinates {
		parts = append(parts, fmt.Sprint(coordinate.Index))
	}
	return strings.Join(parts, "__")
}

func boundaryCountLeaf(path string, parents []capability.RepeatedCoordinate) string {
	parts := strings.Split(strings.TrimPrefix(path, "root."), ".")
	parentIndex := 0
	for index, part := range parts {
		parts[index] = strings.TrimSuffix(part, "[]")
		if strings.HasSuffix(part, "[]") && parentIndex < len(parents) {
			parts[index] += fmt.Sprintf("__%d", parents[parentIndex].Index)
			parentIndex++
		}
	}
	return strings.Join(parts, "__") + "__count"
}
