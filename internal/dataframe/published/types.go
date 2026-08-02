package published

import (
	"context"
	"fmt"
	"time"

	publication "github.com/calypr/loom/internal/dataframe/publication"
)

type State string

const authResourcePathColumn = "auth_resource_path"

const StateReady State = "READY"

type Column struct {
	Name       string `json:"name"`
	ClickHouse string `json:"clickhouseType"`
}

type Materialization struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Revision          string     `json:"revision,omitempty"`
	Project           string     `json:"project"`
	DatasetGeneration string     `json:"datasetGeneration"`
	State             State      `json:"state"`
	ScopeUnrestricted bool       `json:"scopeUnrestricted"`
	AuthResourcePaths []string   `json:"authResourcePaths,omitempty"`
	Columns           []Column   `json:"columns"`
	PhysicalTable     string     `json:"physicalTable"`
	RowCount          int64      `json:"rowCount"`
	RowCountKnown     bool       `json:"-"`
	CreatedAt         time.Time  `json:"createdAt"`
	ReadyAt           *time.Time `json:"readyAt,omitempty"`
	Error             string     `json:"error,omitempty"`
	FailureCode       string     `json:"failureCode,omitempty"`
	FailureRetryable  bool       `json:"failureRetryable,omitempty"`
}

func ResolvePublishedOutput(ctx context.Context, catalog publication.BundleCatalog, project, generation, alias string) (Materialization, error) {
	if catalog == nil {
		return Materialization{}, fmt.Errorf("bundle catalog is required")
	}
	listed, ok := catalog.(publication.StaleBundleCatalog)
	if !ok {
		return Materialization{}, fmt.Errorf("bundle catalog does not support dataset resolution")
	}
	executions, err := listed.ListExecutions(ctx, publication.BundleReady, time.Now().UTC().Add(time.Second))
	if err != nil {
		return Materialization{}, err
	}
	var newest *publication.BundleExecution
	for index := range executions {
		execution := executions[index]
		if execution.Project != project || execution.DatasetGeneration != generation || execution.State != publication.BundleReady {
			continue
		}
		pointer, pointerErr := catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr != nil {
			return Materialization{}, fmt.Errorf("resolve dataframe pointer: %w", pointerErr)
		}
		if pointer.ExecutionID != execution.ID {
			continue
		}
		for _, output := range execution.Outputs {
			outputAlias := output.Alias
			if outputAlias == "" {
				outputAlias = output.Name
			}
			if outputAlias == alias && (newest == nil || execution.UpdatedAt.After(newest.UpdatedAt)) {
				copy := execution
				newest = &copy
				break
			}
		}
	}
	if newest != nil {
		for _, output := range newest.Outputs {
			outputAlias := output.Alias
			if outputAlias == "" {
				outputAlias = output.Name
			}
			if outputAlias == alias {
				return publishedMaterialization(*newest, output, outputAlias), nil
			}
		}
	}
	return Materialization{}, fmt.Errorf("published dataset %q was not found for project %q and generation %q", alias, project, generation)
}

func ListPublishedOutputs(ctx context.Context, catalog publication.BundleCatalog, project, generation string) ([]Materialization, error) {
	listed, ok := catalog.(publication.StaleBundleCatalog)
	if !ok {
		return nil, fmt.Errorf("bundle catalog does not support dataset listing")
	}
	executions, err := listed.ListExecutions(ctx, publication.BundleReady, time.Now().UTC().Add(time.Second))
	if err != nil {
		return nil, err
	}
	result := make([]Materialization, 0)
	for _, execution := range executions {
		if execution.Project != project || execution.DatasetGeneration != generation || execution.State != publication.BundleReady {
			continue
		}
		pointer, pointerErr := catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr != nil {
			return nil, fmt.Errorf("resolve dataframe pointer: %w", pointerErr)
		}
		if pointer.ExecutionID != execution.ID {
			continue
		}
		for _, output := range execution.Outputs {
			alias := output.Alias
			if alias == "" {
				alias = output.Name
			}
			result = append(result, publishedMaterialization(execution, output, alias))
		}
	}
	return result, nil
}

func publishedMaterialization(execution publication.BundleExecution, output publication.BundleOutputRecord, alias string) Materialization {
	return Materialization{
		ID:                execution.ID + ":" + output.Name,
		Name:              alias,
		Revision:          execution.ID,
		Project:           execution.Project,
		DatasetGeneration: execution.DatasetGeneration,
		State:             StateReady,
		ScopeUnrestricted: len(execution.AuthResourcePaths) == 0,
		AuthResourcePaths: append([]string(nil), execution.AuthResourcePaths...),
		Columns:           publishedColumns(output.Columns),
		PhysicalTable:     output.PhysicalTable,
		RowCount:          output.RowCount,
		CreatedAt:         execution.CreatedAt,
		ReadyAt:           execution.ReadyAt,
	}
}

func publishedColumns(columns []publication.PhysicalColumn) []Column {
	result := make([]Column, len(columns))
	for index, column := range columns {
		result[index] = Column{Name: column.Name, ClickHouse: column.ClickHouse}
	}
	return result
}
