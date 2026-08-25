package explorer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
)

// ConfigV2 is the portable Explorer artifact. Both repository and interactive
// packets may carry the complete human-facing presentation layer. A repository
// packet may also be baseline-only, in which case the presentation fields are
// omitted and the frontend derives a layout from live dataset metadata. It
// contains no catalog, selector, join, or other Builder implementation ID.
const ConfigV2APIVersion = "loom.calypr.org/explorer-config/v2"

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// ConfigManagementForID returns the management value required inside a V2
// packet for the durable Explorer identity. The repository default keeps its
// repository identity even though Builder users may edit and publish it.
func ConfigManagementForID(explorerID string) string {
	if explorerID == "default" {
		return "repository"
	}
	return "interactive"
}

type ConfigV2 struct {
	APIVersion    string                    `json:"apiVersion"`
	Kind          string                    `json:"kind"`
	Project       string                    `json:"project"`
	Explorer      ConfigExplorer            `json:"explorer"`
	Recipe        json.RawMessage           `json:"recipe"`
	Views         []ConfigView              `json:"views,omitempty"`
	SharedFilters map[string][]SharedFilter `json:"sharedFilters,omitempty"`
	FileActions   FileActions               `json:"fileActions,omitempty"`
}

type ConfigExplorer struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Management  string `json:"management"`
}
type ConfigView struct {
	ID           string              `json:"id"`
	Title        string              `json:"title"`
	Output       string              `json:"output"`
	RowLabel     string              `json:"rowLabel,omitempty"`
	Table        ConfigTable         `json:"table"`
	Filters      []ConfigFilter      `json:"filters,omitempty"`
	Charts       []ConfigChart       `json:"charts,omitempty"`
	FixedFilters map[string][]string `json:"fixedFilters,omitempty"`
	Actions      []ConfigAction      `json:"actions,omitempty"`
}
type ConfigTable struct {
	Columns []ConfigColumn `json:"columns"`
}
type ConfigColumn struct {
	Column  string `json:"column"`
	Label   string `json:"label,omitempty"`
	Visible bool   `json:"visible"`
}
type ConfigFilter struct {
	Column string `json:"column"`
	Label  string `json:"label,omitempty"`
}
type ConfigChart struct {
	Column string `json:"column"`
	Type   string `json:"type"`
	Title  string `json:"title,omitempty"`
}
type ConfigAction struct {
	Type     string   `json:"type"`
	Title    string   `json:"title"`
	FileName string   `json:"fileName,omitempty"`
	Output   string   `json:"output,omitempty"`
	Columns  []string `json:"columns,omitempty"`
}
type SharedFilter struct {
	Output string `json:"output"`
	Column string `json:"column"`
}
type FileActions struct {
	Extensions map[string][]string `json:"extensions,omitempty"`
	Actions    map[string]string   `json:"actions,omitempty"`
}

// PresentationRepairError reports a presentation reference that cannot be
// repaired without making a view unusable. Ordinary stale references are
// removed by RepairConfigV2Presentation and returned as WARN diagnostics.
type PresentationRepairError struct {
	Diagnostic Diagnostic
}

func (e *PresentationRepairError) Error() string {
	if e == nil {
		return "invalid Explorer presentation"
	}
	return e.Diagnostic.Message
}

// RepairConfigV2Presentation removes presentation references that are absent
// from the authoritative emitted output schema. The schema is deliberately
// supplied by the caller because recipe parsing alone cannot know dynamic or
// snapshot-dependent output columns.
func RepairConfigV2Presentation(cfg ConfigV2, available map[string]map[string]bool) (ConfigV2, []Diagnostic, error) {
	if len(available) == 0 {
		return cfg, nil, nil
	}
	diagnostics := make([]Diagnostic, 0)
	stale := func(path, output, column, reference string) {
		diagnostics = append(diagnostics, Diagnostic{
			Severity:  "WARN",
			Stage:     "presentation",
			Code:      "STALE_PRESENTATION_REFERENCE",
			FieldPath: path,
			Message:   fmt.Sprintf("removed stale %s presentation reference to output column %q", reference, column),
			Details: map[string]any{
				"outputId":      output,
				"column":        column,
				"referenceKind": reference,
			},
		})
	}
	hasColumn := func(output, column string) bool {
		columns, ok := available[output]
		return ok && columns[column]
	}
	allColumns := func(column string) bool {
		for _, columns := range available {
			if columns[column] {
				return true
			}
		}
		return false
	}

	for viewIndex := range cfg.Views {
		view := &cfg.Views[viewIndex]
		if _, ok := available[view.Output]; !ok {
			return cfg, diagnostics, &PresentationRepairError{Diagnostic: Diagnostic{
				Severity:  "ERROR",
				Stage:     "presentation",
				Code:      "STALE_PRESENTATION_OUTPUT",
				FieldPath: fmt.Sprintf("$.views[%d].output", viewIndex),
				Message:   fmt.Sprintf("view %q references an output absent from the compiled schema", view.ID),
				Details:   map[string]any{"viewId": view.ID, "outputId": view.Output},
			}}
		}
		columns := view.Table.Columns[:0]
		for columnIndex, column := range view.Table.Columns {
			if hasColumn(view.Output, column.Column) {
				columns = append(columns, column)
				continue
			}
			stale(fmt.Sprintf("$.views[%d].table.columns[%d].column", viewIndex, columnIndex), view.Output, column.Column, "table")
		}
		view.Table.Columns = columns
		if len(view.Table.Columns) == 0 {
			return cfg, diagnostics, &PresentationRepairError{Diagnostic: Diagnostic{
				Severity:  "ERROR",
				Stage:     "presentation",
				Code:      "STALE_PRESENTATION_EMPTY_VIEW",
				FieldPath: fmt.Sprintf("$.views[%d].table.columns", viewIndex),
				Message:   fmt.Sprintf("view %q has no table columns after stale presentation repair", view.ID),
				Details:   map[string]any{"viewId": view.ID, "outputId": view.Output},
			}}
		}
		if view.RowLabel != "" && !hasColumn(view.Output, view.RowLabel) {
			stale(fmt.Sprintf("$.views[%d].rowLabel", viewIndex), view.Output, view.RowLabel, "rowLabel")
			view.RowLabel = ""
		}
		filters := view.Filters[:0]
		for filterIndex, filter := range view.Filters {
			if hasColumn(view.Output, filter.Column) {
				filters = append(filters, filter)
				continue
			}
			stale(fmt.Sprintf("$.views[%d].filters[%d].column", viewIndex, filterIndex), view.Output, filter.Column, "filter")
		}
		view.Filters = filters
		charts := view.Charts[:0]
		for chartIndex, chart := range view.Charts {
			if hasColumn(view.Output, chart.Column) {
				charts = append(charts, chart)
				continue
			}
			stale(fmt.Sprintf("$.views[%d].charts[%d].column", viewIndex, chartIndex), view.Output, chart.Column, "chart")
		}
		view.Charts = charts
		for column := range view.FixedFilters {
			if hasColumn(view.Output, column) {
				continue
			}
			stale(fmt.Sprintf("$.views[%d].fixedFilters.%s", viewIndex, column), view.Output, column, "fixedFilter")
			delete(view.FixedFilters, column)
		}
		for actionIndex := range view.Actions {
			action := &view.Actions[actionIndex]
			kept := action.Columns[:0]
			for columnIndex, column := range action.Columns {
				if hasColumn(view.Output, column) {
					kept = append(kept, column)
					continue
				}
				stale(fmt.Sprintf("$.views[%d].actions[%d].columns[%d]", viewIndex, actionIndex, columnIndex), view.Output, column, "action")
			}
			action.Columns = kept
		}
	}

	for output, filters := range cfg.SharedFilters {
		kept := filters[:0]
		for index, filter := range filters {
			if hasColumn(filter.Output, filter.Column) {
				kept = append(kept, filter)
				continue
			}
			stale(fmt.Sprintf("$.sharedFilters.%s[%d].column", output, index), filter.Output, filter.Column, "sharedFilter")
		}
		if len(kept) == 0 {
			delete(cfg.SharedFilters, output)
		} else {
			cfg.SharedFilters[output] = kept
		}
	}
	for extension, columns := range cfg.FileActions.Extensions {
		kept := columns[:0]
		for index, column := range columns {
			if allColumns(column) {
				kept = append(kept, column)
				continue
			}
			stale(fmt.Sprintf("$.fileActions.extensions.%s[%d]", extension, index), "", column, "fileAction")
		}
		if len(kept) == 0 {
			delete(cfg.FileActions.Extensions, extension)
		} else {
			cfg.FileActions.Extensions[extension] = kept
		}
	}
	return cfg, diagnostics, nil
}

// DecodeConfigV2 rejects the legacy explorerConfig/tabs envelope and every
// unknown field. The recipe gets its own strict decoder and validator.
func DecodeConfigV2(raw []byte, project string) (ConfigV2, recipe.Bundle, error) {
	return DecodeDefaultConfigV2(raw, project)
}

// DecodeDefaultConfigV2 validates the repository contract. Repository packets
// may be baseline-only or may include a complete presentation layer authored
// by ETL. In the latter case the packet is preserved losslessly and exposed as
// the default's activeConfig after publication.
func DecodeDefaultConfigV2(raw []byte, project string) (ConfigV2, recipe.Bundle, error) {
	return decodeConfigV2(raw, project, "default", "repository")
}

// DecodeInteractiveConfigV2 validates a complete interactive V2 document. It
// shares the exact recipe/presentation validator used by repository deployment
// while keeping ownership mode explicit.
func DecodeInteractiveConfigV2(raw []byte, project, explorerID string) (ConfigV2, recipe.Bundle, error) {
	return decodeConfigV2(raw, project, explorerID, "interactive")
}

// CanonicalConfigV2 validates raw and returns deterministic JSON plus its
// content digest. Generic JSON canonicalization preserves every V2 field,
// including presentation data that is not represented in recipe.Bundle.
func CanonicalConfigV2(raw []byte, project, explorerID, management string) (ConfigV2, recipe.Bundle, []byte, string, error) {
	var cfg ConfigV2
	var bundle recipe.Bundle
	var err error
	if management == "interactive" {
		cfg, bundle, err = DecodeInteractiveConfigV2(raw, project, explorerID)
	} else {
		cfg, bundle, err = decodeConfigV2(raw, project, explorerID, management)
	}
	if err != nil {
		return cfg, bundle, nil, "", err
	}
	canonical, err := canonicalJSONBytes(raw)
	if err != nil {
		return cfg, bundle, nil, "", fmt.Errorf("canonicalize ExplorerConfigV2: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return cfg, bundle, canonical, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeConfigV2(raw []byte, project, explorerID, management string) (ConfigV2, recipe.Bundle, error) {
	var cfg ConfigV2
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, recipe.Bundle{}, fmt.Errorf("decode ExplorerConfigV2: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return cfg, recipe.Bundle{}, fmt.Errorf("decode ExplorerConfigV2: multiple JSON values")
	}
	if cfg.APIVersion != ConfigV2APIVersion || cfg.Kind != "ExplorerConfig" {
		return cfg, recipe.Bundle{}, fmt.Errorf("apiVersion must be %q and kind must be ExplorerConfig", ConfigV2APIVersion)
	}
	if projectid.Canonical(cfg.Project) != projectid.Canonical(project) {
		return cfg, recipe.Bundle{}, fmt.Errorf("config project must match deployment project")
	}
	cfg.Project = projectid.Canonical(project)
	if cfg.Explorer.ID != explorerID || !strings.EqualFold(cfg.Explorer.Management, management) {
		return cfg, recipe.Bundle{}, fmt.Errorf("config explorer identity or management mode does not match deployment")
	}
	if strings.TrimSpace(cfg.Explorer.Title) == "" {
		return cfg, recipe.Bundle{}, fmt.Errorf("ExplorerConfigV2 requires a title")
	}
	hasPresentation := len(cfg.Views) > 0 || len(cfg.SharedFilters) > 0 || len(cfg.FileActions.Extensions) > 0 || len(cfg.FileActions.Actions) > 0
	if strings.EqualFold(management, "interactive") && len(cfg.Views) == 0 {
		return cfg, recipe.Bundle{}, fmt.Errorf("interactive ExplorerConfigV2 requires at least one view")
	}
	if strings.EqualFold(management, "repository") && hasPresentation && len(cfg.Views) == 0 {
		return cfg, recipe.Bundle{}, fmt.Errorf("repository ExplorerConfigV2 presentation requires at least one view")
	}
	bundle, err := recipe.Parse(cfg.Recipe)
	if err != nil {
		return cfg, recipe.Bundle{}, fmt.Errorf("invalid recipe: %w", err)
	}
	outputs := map[string]bool{}
	for _, output := range bundle.Outputs {
		outputs[output.Name] = true
	}
	views := map[string]bool{}
	for i, view := range cfg.Views {
		if !idPattern.MatchString(view.ID) || views[view.ID] || strings.TrimSpace(view.Title) == "" || !outputs[view.Output] {
			return cfg, recipe.Bundle{}, fmt.Errorf("views[%d] must have a unique id, title, and recipe output", i)
		}
		views[view.ID] = true
		if len(view.Table.Columns) == 0 {
			return cfg, recipe.Bundle{}, fmt.Errorf("views[%d].table.columns is required", i)
		}
		for _, column := range view.Table.Columns {
			if strings.TrimSpace(column.Column) == "" {
				return cfg, recipe.Bundle{}, fmt.Errorf("views[%d] has an empty table column", i)
			}
		}
	}
	return cfg, bundle, nil
}
