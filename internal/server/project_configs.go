package server

import (
	"fmt"
	"os"
	"strings"

	"github.com/calypr/loom/internal/explorer"
)

func loadProjectConfigs(config ServerConfig, recipeName string) (explorer.ProjectConfigCatalog, error) {
	if strings.TrimSpace(config.ProjectConfigs) == "" {
		return explorer.ProjectConfigCatalog{}, nil
	}
	if !config.ClickHouse.Enabled || recipeName == "" {
		return explorer.ProjectConfigCatalog{}, fmt.Errorf("server.project_configs requires ClickHouse and a configured dataframer recipe")
	}
	data, err := os.ReadFile(config.ProjectConfigs)
	if err != nil {
		return explorer.ProjectConfigCatalog{}, fmt.Errorf("read project configs %q: %w", config.ProjectConfigs, err)
	}
	catalog, err := explorer.DecodeProjectConfigs(data)
	if err != nil {
		return explorer.ProjectConfigCatalog{}, fmt.Errorf("parse project configs %q: %w", config.ProjectConfigs, err)
	}
	if len(catalog.Configs) != 0 {
		found := false
		for _, definition := range catalog.Configs {
			if definition.RecipeRef.Name == recipeName {
				found = true
				break
			}
		}
		if !found {
			return explorer.ProjectConfigCatalog{}, fmt.Errorf("project configs have no definition for configured recipe %q", recipeName)
		}
	}
	return catalog, nil
}
