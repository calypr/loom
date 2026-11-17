//go:generate go run generate_children.go . children.gen.go
package ir

import "fmt"

func Walk(root Node, visit func(Node) error) error {
	active := make(map[Node]bool)
	var walk func(Node) error
	walk = func(node Node) error {
		if node == nil || node.isNil() {
			return nil
		}
		if active[node] {
			return fmt.Errorf("physical IR child cycle at %T", node)
		}
		active[node] = true
		defer delete(active, node)
		if err := visit(node); err != nil {
			return err
		}
		for _, child := range Children(node) {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}
