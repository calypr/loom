package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
)

const maxConstructionColumnPopulationIDs = compiler.MaxColumnPopulationBatch

type ConstructionColumnPopulationRequest struct {
	Project              string   `json:"-"`
	ExplorerID           string   `json:"-"`
	SnapshotToken        string   `json:"snapshotToken"`
	ExpectedDraftVersion int64    `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string   `json:"expectedDraftDigest"`
	OutputID             string   `json:"outputId"`
	StageID              string   `json:"stageId"`
	ColumnIDs            []string `json:"columnIds"`
}

type ConstructionColumnPopulationSummary struct {
	ColumnID string `json:"columnId"`
	Status   string `json:"status"`
}

type ConstructionColumnPopulationResponse struct {
	SnapshotToken string                                `json:"snapshotToken"`
	DraftVersion  int64                                 `json:"draftVersion"`
	DraftDigest   string                                `json:"draftDigest"`
	OutputID      string                                `json:"outputId"`
	StageID       string                                `json:"stageId"`
	Columns       []ConstructionColumnPopulationSummary `json:"columns"`
}

func (r ConstructionColumnPopulationRequest) Validate() error {
	for name, value := range map[string]string{
		"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID, "stageId": r.StageID,
	} {
		if err := requireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	if len(r.ColumnIDs) == 0 || len(r.ColumnIDs) > maxConstructionColumnPopulationIDs {
		return fmt.Errorf("columnIds must contain between 1 and %d ids", maxConstructionColumnPopulationIDs)
	}
	seen := make(map[string]struct{}, len(r.ColumnIDs))
	for _, id := range r.ColumnIDs {
		if err := requireExactIdentity(id, "columnId"); err != nil {
			return err
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("columnIds must be unique")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (s *Service) GetConstructionColumnPopulation(ctx context.Context, request ConstructionColumnPopulationRequest) (ConstructionColumnPopulationResponse, error) {
	return s.constructionScans().getConstructionColumnPopulation(ctx, request)
}

func (s constructionScanFlow) getConstructionColumnPopulation(ctx context.Context, request ConstructionColumnPopulationRequest) (ConstructionColumnPopulationResponse, error) {
	if err := request.Validate(); err != nil {
		return ConstructionColumnPopulationResponse{}, malformed("construction-column-population", err.Error(), err)
	}
	base, err := s.loadBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken, request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return ConstructionColumnPopulationResponse{}, err
	}
	if base.receipt == nil {
		return ConstructionColumnPopulationResponse{}, conflict("construction-column-population", "STAGE_SCAN_UNAVAILABLE", "the exact compiled output is not available for column population", nil, nil)
	}
	var stage *explorer.ReceiptConstructionStage
	for index := range base.stages {
		if base.stages[index].ID == request.StageID {
			stage = &base.stages[index]
			break
		}
	}
	if stage == nil {
		return ConstructionColumnPopulationResponse{}, conflict("construction-column-population", "STALE_STAGE_REFERENCE", "the requested stage is not present in the current compiled output", nil, nil)
	}

	keys := make([]columnPopulationCacheKey, len(request.ColumnIDs))
	response := ConstructionColumnPopulationResponse{
		SnapshotToken: request.SnapshotToken, DraftVersion: base.owner.DraftVersion,
		DraftDigest: base.owner.DraftDigest, OutputID: request.OutputID, StageID: request.StageID,
		Columns: make([]ConstructionColumnPopulationSummary, len(request.ColumnIDs)),
	}
	misses := make([]string, 0, len(request.ColumnIDs))
	missIndices := make(map[string]int, len(request.ColumnIDs))
	for index, id := range request.ColumnIDs {
		if _, ok := constructionCategoryColumn(stage.Columns, id); !ok {
			return ConstructionColumnPopulationResponse{}, unprocessable("construction-column-population", "INVALID_COLUMN_ID", "each requested column must be a public scalar in the selected stage", nil)
		}
		keys[index] = columnPopulationCacheKey{
			Project: projectid.Canonical(request.Project), Generation: base.snapshot.Identity.Generation,
			ReceiptID: base.receipt.ID, OutputID: request.OutputID, StageID: request.StageID,
			ColumnID: id, Scope: base.snapshot.Identity.AuthorizationScopeDigest,
		}
		if status, ok := s.populationCache.Get(keys[index]); ok {
			response.Columns[index] = ConstructionColumnPopulationSummary{ColumnID: id, Status: status}
			continue
		}
		missIndices[id] = index
		misses = append(misses, id)
	}
	if len(misses) == 0 {
		return response, nil
	}
	if s.scanPopulation == nil {
		return ConstructionColumnPopulationResponse{}, unavailable("construction-column-population", "COLUMN_POPULATION_UNAVAILABLE", "compiler-owned column population scanning is not configured", nil)
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(base.catalog.Project), SelectionProject: base.catalog.Project,
		DatasetGeneration: base.snapshot.Identity.Generation, SelectionMembersCollection: s.selectionMembersCollection,
		OutputNames: []string{request.OutputID},
	}
	applyAuthorizedScope(&bindings, base.authorized, false)
	result, err := s.scanPopulation(ctx, base.receipt, bindings, dataframeexecution.ColumnPopulationRequest{
		Output: request.OutputID, StageID: request.StageID, ColumnIDs: misses,
	})
	if err != nil {
		return ConstructionColumnPopulationResponse{}, unavailable("construction-column-population", "COLUMN_POPULATION_SCAN_FAILED", "the current stage could not be checked for populated columns; retry the request", err)
	}
	if len(result.Columns) != len(misses) {
		return ConstructionColumnPopulationResponse{}, unavailable("construction-column-population", "COLUMN_POPULATION_INCOMPLETE", "the current stage scan did not return every requested column", nil)
	}
	seen := make(map[string]struct{}, len(result.Columns))
	cacheStatuses := make(map[string]string, len(result.Columns))
	for _, column := range result.Columns {
		index, requested := missIndices[column.ColumnID]
		if !requested {
			return ConstructionColumnPopulationResponse{}, unavailable("construction-column-population", "COLUMN_POPULATION_MISMATCH", "the current stage scan returned an unrequested column", nil)
		}
		if _, duplicate := seen[column.ColumnID]; duplicate {
			return ConstructionColumnPopulationResponse{}, unavailable("construction-column-population", "COLUMN_POPULATION_MISMATCH", "the current stage scan returned a column more than once", nil)
		}
		seen[column.ColumnID] = struct{}{}
		status := strings.ToUpper(strings.TrimSpace(column.Status))
		if status != "POPULATED" && status != "EMPTY" && status != "UNKNOWN" {
			return ConstructionColumnPopulationResponse{}, unavailable("construction-column-population", "COLUMN_POPULATION_MISMATCH", "the current stage scan returned an invalid population status", nil)
		}
		response.Columns[index] = ConstructionColumnPopulationSummary{ColumnID: column.ColumnID, Status: status}
		if status != "UNKNOWN" {
			cacheStatuses[column.ColumnID] = status
		}
	}
	if len(seen) != len(misses) {
		return ConstructionColumnPopulationResponse{}, unavailable("construction-column-population", "COLUMN_POPULATION_INCOMPLETE", "the current stage scan did not return every requested column", nil)
	}
	if ctx.Err() == nil {
		for columnID, status := range cacheStatuses {
			s.populationCache.Put(keys[missIndices[columnID]], status)
		}
	}
	return response, nil
}
