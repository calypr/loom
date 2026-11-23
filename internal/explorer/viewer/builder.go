package viewer

import (
	"errors"
	"strings"

	explorerconfig "github.com/calypr/loom/internal/explorer/config"
)

var ErrInvalidViewBuilder = errors.New("invalid View Builder request")

type ViewBuilderSettings struct {
	Project         string            `json:"project"`
	Version         int64             `json:"version"`
	DefaultConfigID string            `json:"defaultConfigId,omitempty"`
	DefaultFrameID  string            `json:"defaultFrameId,omitempty"`
	Catalogs        map[string]string `json:"catalogs"`
}

type ViewBuilderConfig struct {
	Config      explorerconfig.ProjectConfig `json:"config"`
	RevisionID  string                       `json:"revisionId"`
	Diagnostics []Diagnostic                 `json:"diagnostics,omitempty"`
	ExecutionID string                       `json:"-"`
}

type ViewBuilderCatalog struct {
	RecipeName  string             `json:"recipeName"`
	Title       string             `json:"title,omitempty"`
	RevisionID  string             `json:"revisionId"`
	ExecutionID string             `json:"executionId"`
	Generation  string             `json:"generation"`
	Frames      []ViewBuilderFrame `json:"frames"`
}

type ViewBuilderFrame struct {
	ID      string              `json:"id"`
	Title   string              `json:"title"`
	Columns []ViewBuilderColumn `json:"columns"`
}

type ViewBuilderColumn struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	Cardinality string `json:"cardinality"`
	Filterable  bool   `json:"filterable"`
	Chartable   bool   `json:"chartable"`
}

type ViewBuilderState struct {
	Settings ViewBuilderSettings  `json:"settings"`
	Configs  []ViewBuilderConfig  `json:"configs"`
	Catalogs []ViewBuilderCatalog `json:"catalogs"`
	CanEdit  bool                 `json:"canEdit"`
}

type ViewBuilderSaveRequest struct {
	ExpectedRevisionID string                       `json:"expectedRevisionId"`
	CatalogRevisionID  string                       `json:"catalogRevisionId"`
	Config             explorerconfig.ProjectConfig `json:"config"`
}

type ViewBuilderDefaultsRequest struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	DefaultConfigID string `json:"defaultConfigId"`
	DefaultFrameID  string `json:"defaultFrameId"`
}

func ViewBuilderFrameID(recipeName, outputName string) string {
	return strings.TrimSpace(recipeName) + "/" + strings.TrimSpace(outputName)
}
