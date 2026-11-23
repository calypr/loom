package explorer

import (
	explorerconfig "github.com/calypr/loom/internal/explorer/config"
)

const ProjectConfigsAPIVersion = explorerconfig.ProjectConfigsAPIVersion

type ProjectConfigRecipeRef = explorerconfig.ProjectConfigRecipeRef
type ProjectConfig = explorerconfig.ProjectConfig

var CloneProjectConfigDefinition = explorerconfig.CloneProjectConfigDefinition

func validateProjectConfigs(configs []ProjectConfig) error {
	return explorerconfig.ValidateProjectConfigs(configs)
}
