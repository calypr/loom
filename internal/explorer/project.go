package explorer

import (
	"github.com/calypr/loom/internal/dataset"
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
