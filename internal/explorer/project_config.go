package explorer

import (
	"context"

	"github.com/calypr/loom/internal/dataset"
	explorerconfig "github.com/calypr/loom/internal/explorer/config"
)

type ProjectConfigPublication struct {
	Definition ProjectConfig
	Revision   Revision
}

type ProjectConfigPublishRequest struct {
	RecipeName                string
	Configs                   []ProjectConfigPublication
	CatalogSnapshot           *Revision
	ExpectedConfigRevisionIDs map[string]string
	Release                   dataset.ProjectRelease
	ExpectedReleaseRevision   int64
}

type ProjectConfigPublisher interface {
	PublishProjectConfigs(context.Context, ProjectConfigPublishRequest) (dataset.ActiveRelease, error)
}

const ProjectConfigsAPIVersion = explorerconfig.ProjectConfigsAPIVersion

type ProjectConfigRecipeRef = explorerconfig.ProjectConfigRecipeRef
type ProjectConfig = explorerconfig.ProjectConfig

var CloneProjectConfigDefinition = explorerconfig.CloneProjectConfigDefinition

func validateProjectConfigs(configs []ProjectConfig) error {
	return explorerconfig.ValidateProjectConfigs(configs)
}
