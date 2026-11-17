package explorer

import (
	"context"
	"errors"
	"strings"
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
	Config      ProjectConfig `json:"config"`
	RevisionID  string        `json:"revisionId"`
	Diagnostics []Diagnostic  `json:"diagnostics,omitempty"`
	ExecutionID string        `json:"-"`
}

type ViewBuilderCatalog struct {
	RecipeName  string             `json:"recipeName"`
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
	ExpectedRevisionID string        `json:"expectedRevisionId"`
	CatalogRevisionID  string        `json:"catalogRevisionId"`
	Config             ProjectConfig `json:"config"`
}

type ViewBuilderDefaultsRequest struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	DefaultConfigID string `json:"defaultConfigId"`
	DefaultFrameID  string `json:"defaultFrameId"`
}

type ViewBuilderMutation struct {
	Project            string
	ExpectedRevisionID string
	CatalogRevisionID  string
	Publication        ProjectConfigPublication
	Actor              string
}

type ViewBuilderRepository interface {
	GetViewBuilderSettings(context.Context, string) (ViewBuilderSettings, error)
	SaveViewBuilderConfig(context.Context, ViewBuilderMutation) error
	DeleteViewBuilderConfig(context.Context, string, string, string) error
	SaveViewBuilderDefaults(context.Context, string, ViewBuilderDefaultsRequest) error
	AdoptViewBuilderCatalogs(context.Context, string) error
}

// ViewBuilderFrameID binds a logical output ID to its recipe namespace.
func ViewBuilderFrameID(recipeName, outputName string) string {
	return strings.TrimSpace(recipeName) + "/" + strings.TrimSpace(outputName)
}

func parseViewBuilderFrameID(id string) (recipeName, outputName string, ok bool) {
	parts := strings.Split(id, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	if parts[0] != strings.TrimSpace(parts[0]) || parts[1] != strings.TrimSpace(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}
