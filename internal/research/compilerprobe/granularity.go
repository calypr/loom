package compilerprobe

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type TraversalStep struct {
	ID     int
	Parent int
	Source recipe.Traversal
}

func SplitTraversals(roots []recipe.Traversal) []TraversalStep {
	var steps []TraversalStep
	var walk func([]recipe.Traversal, int)
	walk = func(siblings []recipe.Traversal, parent int) {
		for _, node := range siblings {
			id := len(steps) + 1
			children := node.Traversals
			node.Traversals = nil
			steps = append(steps, TraversalStep{ID: id, Parent: parent, Source: node})
			walk(children, id)
		}
	}
	walk(roots, 0)
	return steps
}

func JoinTraversals(steps []TraversalStep) ([]recipe.Traversal, error) {
	children := make([][]recipe.Traversal, len(steps)+1)
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step.ID != i+1 || step.Parent < 0 || step.Parent >= step.ID {
			return nil, fmt.Errorf("invalid traversal order at %d", i)
		}
		node := step.Source
		node.Traversals = children[step.ID]
		children[step.Parent] = append(children[step.Parent], node)
	}
	for _, siblings := range children {
		for i, j := 0, len(siblings)-1; i < j; i, j = i+1, j-1 {
			siblings[i], siblings[j] = siblings[j], siblings[i]
		}
	}
	return children[0], nil
}
