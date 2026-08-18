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
)

// ConfigV2 is the portable Explorer artifact. Both repository and interactive
// packets may carry the complete human-facing presentation layer. A repository
// packet may also be baseline-only, in which case the presentation fields are
// omitted and the frontend derives a layout from live dataset metadata. It
// contains no catalog, selector, join, or other Builder implementation ID.
const ConfigV2APIVersion = "loom.calypr.org/explorer-config/v2"

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

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
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return cfg, bundle, nil, "", fmt.Errorf("canonicalize ExplorerConfigV2: %w", err)
	}
	canonical, err := json.Marshal(value)
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
	if cfg.Project != project {
		return cfg, recipe.Bundle{}, fmt.Errorf("config project must match deployment project")
	}
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
