package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
)

type query struct {
	Query    string         `json:"query"`
	BindVars map[string]any `json:"bindVars,omitempty"`
}

type workload struct {
	Name                string           `json:"name"`
	Plan                *ir.PhysicalPlan `json:"plan,omitempty"`
	Mode                string           `json:"mode,omitempty"`
	RootIndex           string           `json:"rootIndex,omitempty"`
	GroupKeySourcePaths [][]string       `json:"groupKeySourcePaths,omitempty"`
	Fast                query            `json:"fast,omitempty"`
	Generic             query            `json:"generic,omitempty"`
	FastContains        []string         `json:"fastContains"`
	GenericExcludes     []string         `json:"genericExcludes"`
	Ordered             bool             `json:"ordered"`
}

func loadWorkload(path string) (workload, error) {
	file, err := os.Open(path)
	if err != nil {
		return workload{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var input workload
	if err := decoder.Decode(&input); err != nil {
		return workload{}, err
	}
	if input.Plan != nil {
		if input.Fast.Query != "" || input.Generic.Query != "" {
			return workload{}, fmt.Errorf("supply a physical plan or rendered queries")
		}
		if err := input.render(); err != nil {
			return workload{}, err
		}
	}
	return input, input.verifyQueries()
}

func (input *workload) render() error {
	var options aql.Options
	switch input.RootIndex {
	case "":
	case "off":
		options.RootIndex = aql.RootIndexDisabled()
	default:
		options.RootIndex = aql.RootIndexHint(input.RootIndex)
	}
	switch input.Mode {
	case "streaming-pivot":
		options.Preview = aql.StreamingMissingCategoryPivotPreview{}
	case "two-scan-pivot":
		options.Preview = aql.TwoScanPivotPreview{GroupKeySourcePaths: input.GroupKeySourcePaths}
	case "dynamic-pivot":
		options.Preview = aql.DynamicCategoryPivotPreview{}
	case "semantic-group":
		options.Preview = aql.SemanticGroupProjectionFactorizationPreview{}
	case "root-index":
		if input.RootIndex == "" {
			return fmt.Errorf("root-index mode requires a hint or off")
		}
	default:
		return fmt.Errorf("unknown preview mode %q", input.Mode)
	}
	fast, err := aql.Render(*input.Plan, options)
	if err != nil {
		return fmt.Errorf("render fast: %w", err)
	}
	generic, err := aql.Render(*input.Plan, aql.Options{})
	if err != nil {
		return fmt.Errorf("render generic: %w", err)
	}
	input.Fast = query{fast.Query, fast.BindVars}
	input.Generic = query{generic.Query, generic.BindVars}
	return nil
}

func (input workload) verifyQueries() error {
	if strings.TrimSpace(input.Fast.Query) == "" || strings.TrimSpace(input.Generic.Query) == "" {
		return fmt.Errorf("both rendered queries are required")
	}
	if input.Fast.Query == input.Generic.Query {
		return fmt.Errorf("fast and generic renderings are identical")
	}
	if len(input.FastContains) == 0 || len(input.GenericExcludes) == 0 {
		return fmt.Errorf("fastContains and genericExcludes are required to prove the render path")
	}
	var distinctMarker bool
	for _, marker := range input.FastContains {
		if marker == "" || !strings.Contains(input.Fast.Query, marker) {
			return fmt.Errorf("fast query missing marker %q", marker)
		}
		distinctMarker = distinctMarker || !strings.Contains(input.Generic.Query, marker)
	}
	if !distinctMarker {
		return fmt.Errorf("at least one fast marker must distinguish the two queries")
	}
	for _, marker := range input.GenericExcludes {
		if marker == "" || strings.Contains(input.Generic.Query, marker) {
			return fmt.Errorf("generic query includes excluded marker %q", marker)
		}
	}
	return nil
}
