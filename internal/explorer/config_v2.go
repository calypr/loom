package explorer

import (
	"encoding/json"
)

const ConfigV2APIVersion = "loom.calypr.org/explorer-config/v2"

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
	Column       string `json:"column"`
	Label        string `json:"label,omitempty"`
	Visible      bool   `json:"visible"`
	Pinned       bool   `json:"pinned,omitempty"`
	CellRenderer string `json:"cellRenderer,omitempty"`
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
	Type          string            `json:"type"`
	Title         string            `json:"title"`
	FileName      string            `json:"fileName,omitempty"`
	Output        string            `json:"output,omitempty"`
	Columns       []string          `json:"columns,omitempty"`
	ExportHeaders map[string]string `json:"exportHeaders,omitempty"`
}
type SharedFilter struct {
	Output string `json:"output"`
	Column string `json:"column"`
}
type FileActions struct {
	Extensions map[string][]string `json:"extensions,omitempty"`
	Actions    map[string]string   `json:"actions,omitempty"`
}
