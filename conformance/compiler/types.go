// Package compilerfixture loads the canonical recipe compiler oracle corpus.
package compilerfixture

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

const SchemaVersion = "loom.compiler-oracle/v1"

type Expected struct {
	Supported             bool           `json:"supported"`
	PlanProfile           string         `json:"planProfile,omitempty"`
	OptimizerRules        []string       `json:"optimizerRules,omitempty"`
	ErrorContains         string         `json:"errorContains,omitempty"`
	QueryContains         []string       `json:"queryContains,omitempty"`
	QueryNotContains      []string       `json:"queryNotContains,omitempty"`
	BindVars              map[string]any `json:"bindVars,omitempty"`
	OutputColumns         []string       `json:"outputColumns,omitempty"`
	ExpectedTraversalSets *int           `json:"expectedTraversalSets,omitempty"`
}

type Fixture struct {
	Schema      string        `json:"schema"`
	ID          string        `json:"id"`
	Description string        `json:"description"`
	Tags        []string      `json:"tags,omitempty"`
	Limit       int           `json:"limit"`
	Project     string        `json:"project"`
	Recipe      recipe.Bundle `json:"recipe"`
	Expected    Expected      `json:"expected"`
	SourceFile  string        `json:"-"`
}

func LoadDir(dir string) ([]Fixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fixtures := make([]Fixture, 0)
	seen := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var fixture Fixture
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&fixture); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		fixture.SourceFile = path
		if err := fixture.Validate(); err != nil {
			return nil, fmt.Errorf("validate %s: %w", path, err)
		}
		if prior, ok := seen[fixture.ID]; ok {
			return nil, fmt.Errorf("duplicate fixture id %q in %s and %s", fixture.ID, prior, path)
		}
		seen[fixture.ID] = path
		fixtures = append(fixtures, fixture)
	}
	if len(fixtures) == 0 {
		return nil, errors.New("compiler fixture directory contains no JSON fixtures")
	}
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].ID < fixtures[j].ID })
	return fixtures, nil
}

func (f Fixture) Validate() error {
	if f.Schema != SchemaVersion {
		return fmt.Errorf("schema must be %q", SchemaVersion)
	}
	if strings.TrimSpace(f.ID) == "" || strings.ContainsAny(f.ID, " /\\") {
		return errors.New("id must be a non-empty path-safe identifier")
	}
	if strings.TrimSpace(f.Description) == "" {
		return errors.New("description is required")
	}
	if f.Limit <= 0 {
		return errors.New("limit must be positive")
	}
	if strings.TrimSpace(f.Project) == "" {
		return errors.New("project is required")
	}
	if err := f.Recipe.Validate(); err != nil {
		return fmt.Errorf("recipe: %w", err)
	}
	if len(f.Recipe.Outputs) != 1 {
		return errors.New("recipe must contain exactly one output")
	}
	if f.Expected.Supported {
		if f.Expected.ErrorContains != "" {
			return errors.New("supported fixture cannot declare errorContains")
		}
	} else if strings.TrimSpace(f.Expected.ErrorContains) == "" {
		return errors.New("unsupported fixture must declare errorContains")
	}
	return nil
}
