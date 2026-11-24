package workspace

import (
	"fmt"
	"strings"
)

func (w Workspace) validateSemanticBindings() error {
	columns := map[string]map[string]bool{}
	for _, document := range w.Documents {
		columns[document.Output.ID] = map[string]bool{}
		for _, column := range document.Columns {
			columns[document.Output.ID][column.Column] = true
		}
	}
	for name, bindings := range w.SharedFilters {
		if strings.TrimSpace(name) == "" || len(bindings) == 0 {
			return fmt.Errorf("sharedFilters names and bindings must be non-empty")
		}
		for i, binding := range bindings {
			if !columns[binding.OutputID][binding.Column] {
				return fmt.Errorf("sharedFilters[%q][%d] references unknown output column", name, i)
			}
		}
	}
	if w.FileActions != nil {
		for extension, actions := range w.FileActions.Extensions {
			if strings.TrimSpace(extension) == "" || len(actions) == 0 {
				return fmt.Errorf("fileActions.extensions entries must be non-empty")
			}
			for _, action := range actions {
				if _, ok := w.FileActions.Actions[action]; !ok {
					return fmt.Errorf("fileActions extension %q references unknown action %q", extension, action)
				}
			}
		}
	}
	return nil
}
