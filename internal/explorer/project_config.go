package explorer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/calypr/loom/internal/dataset"
)

const ProjectConfigsAPIVersion = "loom.calypr.org/project-configs/v1"

var projectConfigIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type ProjectConfigRecipeRef struct {
	Name string `json:"name"`
}

// ProjectConfig is one project-owned Viewer presentation of a registered
// dataframe recipe. It intentionally contains no recipe definition.
type ProjectConfig struct {
	ID            string                    `json:"id"`
	Title         string                    `json:"title"`
	RecipeRef     ProjectConfigRecipeRef    `json:"recipeRef"`
	Views         []ConfigView              `json:"views"`
	SharedFilters map[string][]SharedFilter `json:"sharedFilters,omitempty"`
	FileActions   FileActions               `json:"fileActions,omitempty"`
}

type ProjectConfigCatalog struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Configs    []ProjectConfig `json:"configs"`
}

type ProjectConfigPublication struct {
	Definition ProjectConfig
	Revision   Revision
}

type ProjectConfigPublishRequest struct {
	RecipeName              string
	Configs                 []ProjectConfigPublication
	Release                 dataset.ProjectRelease
	ExpectedReleaseRevision int64
}

type ProjectConfigPublisher interface {
	PublishProjectConfigs(context.Context, ProjectConfigPublishRequest) (dataset.ActiveRelease, error)
}

// DecodeProjectConfigs accepts the chart catalog format and rejects unknown,
// duplicate, and trailing JSON so a typo cannot silently remove presentation.
func DecodeProjectConfigs(raw []byte) (ProjectConfigCatalog, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ProjectConfigCatalog{}, errors.New("project config catalog is required")
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return ProjectConfigCatalog{}, fmt.Errorf("decode project config catalog: %w", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		if err == nil {
			err = errors.New("catalog must be a JSON object")
		}
		return ProjectConfigCatalog{}, fmt.Errorf("decode project config catalog: %w", err)
	}
	for _, required := range []string{"apiVersion", "kind", "configs"} {
		if _, ok := fields[required]; !ok {
			return ProjectConfigCatalog{}, fmt.Errorf("decode project config catalog: %s is required", required)
		}
	}
	if bytes.Equal(bytes.TrimSpace(fields["configs"]), []byte("null")) {
		return ProjectConfigCatalog{}, errors.New("decode project config catalog: configs must be an array")
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var catalog ProjectConfigCatalog
	if err := decoder.Decode(&catalog); err != nil {
		return ProjectConfigCatalog{}, fmt.Errorf("decode project config catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return ProjectConfigCatalog{}, fmt.Errorf("decode project config catalog: trailing JSON: %w", err)
	}
	if catalog.APIVersion != ProjectConfigsAPIVersion {
		return ProjectConfigCatalog{}, fmt.Errorf("unsupported project config apiVersion %q", catalog.APIVersion)
	}
	if catalog.Kind != "ProjectConfigs" {
		return ProjectConfigCatalog{}, fmt.Errorf("unsupported project config kind %q", catalog.Kind)
	}
	if catalog.Configs == nil {
		return ProjectConfigCatalog{}, errors.New("project config catalog configs must be an array")
	}
	if err := validateProjectConfigs(catalog.Configs); err != nil {
		return ProjectConfigCatalog{}, err
	}
	return catalog, nil
}

// Digest returns the content identity of one config definition. Map keys are
// sorted by encoding/json; authored slices retain their meaningful order.
func (config ProjectConfig) Digest() (string, error) {
	if err := config.validate(); err != nil {
		return "", err
	}
	canonical := normalizeProjectConfig(config)
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode project config %q: %w", config.ID, err)
	}
	canonicalJSON, err := canonicalJSONBytes(raw)
	if err != nil {
		return "", fmt.Errorf("canonicalize project config %q: %w", config.ID, err)
	}
	sum := sha256.Sum256(canonicalJSON)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateProjectConfigs(configs []ProjectConfig) error {
	seen := make(map[string]struct{}, len(configs))
	for index, config := range configs {
		if err := config.validate(); err != nil {
			return fmt.Errorf("configs[%d]: %w", index, err)
		}
		if _, ok := seen[config.ID]; ok {
			return fmt.Errorf("duplicate project config id %q", config.ID)
		}
		seen[config.ID] = struct{}{}
	}
	return nil
}

func (config ProjectConfig) validate() error {
	if !projectConfigIDPattern.MatchString(config.ID) {
		return fmt.Errorf("id must contain only letters, digits, '.', '_' or '-' and start with a letter or digit")
	}
	if strings.TrimSpace(config.Title) == "" {
		return errors.New("title is required")
	}
	if strings.TrimSpace(config.RecipeRef.Name) == "" || config.RecipeRef.Name != strings.TrimSpace(config.RecipeRef.Name) {
		return errors.New("recipeRef.name is required")
	}
	if len(config.Views) == 0 {
		return errors.New("at least one view is required")
	}

	seenViews := make(map[string]struct{}, len(config.Views))
	for index, view := range config.Views {
		path := fmt.Sprintf("views[%d]", index)
		if strings.TrimSpace(view.ID) == "" || view.ID != strings.TrimSpace(view.ID) {
			return fmt.Errorf("%s.id is required", path)
		}
		if strings.TrimSpace(view.Output) == "" || view.Output != strings.TrimSpace(view.Output) {
			return fmt.Errorf("%s.output is required", path)
		}
		if _, ok := seenViews[view.ID]; ok {
			return fmt.Errorf("duplicate view id %q", view.ID)
		}
		seenViews[view.ID] = struct{}{}
		if err := validateProjectConfigView(view, path); err != nil {
			return err
		}
	}
	for name, filters := range config.SharedFilters {
		if strings.TrimSpace(name) == "" {
			return errors.New("sharedFilters names must not be empty")
		}
		seen := make(map[string]struct{}, len(filters))
		for index, filter := range filters {
			if strings.TrimSpace(filter.Output) == "" || strings.TrimSpace(filter.Column) == "" {
				return fmt.Errorf("sharedFilters[%q][%d] requires output and column", name, index)
			}
			key := filter.Output + "\x00" + filter.Column
			if _, ok := seen[key]; ok {
				return fmt.Errorf("sharedFilters[%q] duplicates %s.%s", name, filter.Output, filter.Column)
			}
			seen[key] = struct{}{}
		}
	}
	for action, target := range config.FileActions.Actions {
		if strings.TrimSpace(action) == "" || strings.TrimSpace(target) == "" {
			return errors.New("fileActions.actions entries require names and targets")
		}
	}
	for extension, actions := range config.FileActions.Extensions {
		if strings.TrimSpace(extension) == "" || len(actions) == 0 {
			return errors.New("fileActions.extensions entries require an extension and actions")
		}
		for _, action := range actions {
			if _, ok := config.FileActions.Actions[action]; !ok {
				return fmt.Errorf("fileActions extension %q references unknown action %q", extension, action)
			}
		}
	}
	return nil
}

func validateProjectConfigView(view ConfigView, path string) error {
	seenTable := make(map[string]struct{}, len(view.Table.Columns))
	for index, column := range view.Table.Columns {
		if strings.TrimSpace(column.Column) == "" {
			return fmt.Errorf("%s.table.columns[%d].column is required", path, index)
		}
		if _, ok := seenTable[column.Column]; ok {
			return fmt.Errorf("%s.table.columns duplicates %q", path, column.Column)
		}
		seenTable[column.Column] = struct{}{}
	}
	seenFilters := make(map[string]struct{}, len(view.Filters))
	for index, filter := range view.Filters {
		if strings.TrimSpace(filter.Column) == "" {
			return fmt.Errorf("%s.filters[%d].column is required", path, index)
		}
		if _, ok := seenFilters[filter.Column]; ok {
			return fmt.Errorf("%s.filters duplicates %q", path, filter.Column)
		}
		seenFilters[filter.Column] = struct{}{}
	}
	seenCharts := make(map[string]struct{}, len(view.Charts))
	for index, chart := range view.Charts {
		if strings.TrimSpace(chart.Column) == "" {
			return fmt.Errorf("%s.charts[%d].column is required", path, index)
		}
		if !supportedProjectConfigChartType(chart.Type) {
			return fmt.Errorf("%s.charts[%d].type %q is unsupported", path, index, chart.Type)
		}
		if _, ok := seenCharts[chart.Column]; ok {
			return fmt.Errorf("%s.charts duplicates %q", path, chart.Column)
		}
		seenCharts[chart.Column] = struct{}{}
	}
	for column, values := range view.FixedFilters {
		if strings.TrimSpace(column) == "" || len(values) == 0 {
			return fmt.Errorf("%s.fixedFilters entries require a column and at least one value", path)
		}
	}
	for index, action := range view.Actions {
		if strings.TrimSpace(action.Type) == "" || strings.TrimSpace(action.Title) == "" {
			return fmt.Errorf("%s.actions[%d] requires type and title", path, index)
		}
		if action.Output != "" && action.Output != strings.TrimSpace(action.Output) {
			return fmt.Errorf("%s.actions[%d].output must not contain surrounding whitespace", path, index)
		}
		seenColumns := make(map[string]struct{}, len(action.Columns))
		for columnIndex, column := range action.Columns {
			if strings.TrimSpace(column) == "" {
				return fmt.Errorf("%s.actions[%d].columns[%d] is required", path, index, columnIndex)
			}
			if _, ok := seenColumns[column]; ok {
				return fmt.Errorf("%s.actions[%d].columns duplicates %q", path, index, column)
			}
			seenColumns[column] = struct{}{}
		}
	}
	return nil
}

func supportedProjectConfigChartType(chartType string) bool {
	switch chartType {
	case "bar", "horizontalStacked", "pie", "fullPie", "donut":
		return true
	default:
		return false
	}
}

func normalizeProjectConfig(config ProjectConfig) ProjectConfig {
	if config.Views == nil {
		config.Views = []ConfigView{}
	}
	config.Views = copyProjectConfigSlice(config.Views)
	for index := range config.Views {
		view := &config.Views[index]
		if view.Table.Columns == nil {
			view.Table.Columns = []ConfigColumn{}
		} else {
			view.Table.Columns = copyProjectConfigSlice(view.Table.Columns)
		}
		if view.Filters == nil {
			view.Filters = []ConfigFilter{}
		} else {
			view.Filters = copyProjectConfigSlice(view.Filters)
		}
		if view.Charts == nil {
			view.Charts = []ConfigChart{}
		} else {
			view.Charts = copyProjectConfigSlice(view.Charts)
		}
		if view.Actions == nil {
			view.Actions = []ConfigAction{}
		} else {
			view.Actions = copyProjectConfigSlice(view.Actions)
		}
	}
	if config.SharedFilters != nil {
		filtersByName := make(map[string][]SharedFilter, len(config.SharedFilters))
		for name, filters := range config.SharedFilters {
			if filters == nil {
				filtersByName[name] = []SharedFilter{}
			} else {
				filtersByName[name] = copyProjectConfigSlice(filters)
			}
		}
		config.SharedFilters = filtersByName
	}
	return config
}

func cloneProjectConfigDefinition(config ProjectConfig) (ProjectConfig, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return ProjectConfig{}, fmt.Errorf("encode config: %w", err)
	}
	var cloned ProjectConfig
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return ProjectConfig{}, fmt.Errorf("decode config: %w", err)
	}
	return normalizeProjectConfig(cloned), nil
}

func copyProjectConfigSlice[T any](values []T) []T {
	cloned := make([]T, len(values))
	copy(cloned, values)
	return cloned
}
