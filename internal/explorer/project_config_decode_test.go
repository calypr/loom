package explorer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
)

type ProjectConfigCatalog struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Configs    []ProjectConfig `json:"configs"`
}

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

func TestDecodeProjectConfigsIsStrictAndDigestIsStable(t *testing.T) {
	valid := []byte(`{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[{"id":"documents","title":"Documents","recipeRef":{"name":"metadata"},"views":[{"id":"documents","output":"Documents","table":{"columns":[]}}]}]}`)
	catalog, err := DecodeProjectConfigs(valid)
	if err != nil {
		t.Fatalf("DecodeProjectConfigs() error = %v", err)
	}
	first, err := catalog.Configs[0].Digest()
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	catalog.Configs[0].Views[0].Table.Columns = nil
	second, err := catalog.Configs[0].Digest()
	if err != nil {
		t.Fatalf("Digest() for nil columns error = %v", err)
	}
	if first != second {
		t.Fatalf("nil and empty columns changed digest: %q != %q", first, second)
	}

	for name, raw := range map[string]string{
		"unknown field":          `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[],"extra":true}`,
		"duplicate key":          `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[],"kind":"ProjectConfigs"}`,
		"trailing value":         `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[]} {}`,
		"null configs":           `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":null}`,
		"inline recipe rejected": `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[{"id":"documents","title":"Documents","recipe":{"name":"metadata"},"views":[{"id":"documents","output":"Documents"}]}]}`,
		"unsupported chart":      `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[{"id":"documents","title":"Documents","recipeRef":{"name":"metadata"},"views":[{"id":"documents","output":"Documents","charts":[{"column":"status","type":"scatter"}]}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeProjectConfigs([]byte(raw)); err == nil {
				t.Fatal("DecodeProjectConfigs() succeeded, want error")
			}
		})
	}
}
