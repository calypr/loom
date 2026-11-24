package execution

import (
	"context"

	population "github.com/calypr/loom/internal/dataframe/compiler/population"

	"fmt"

	"sort"

	"strings"
)

func (e *Engine) PopulationMapping(ctx context.Context, resolved Resolved, request PopulationMappingRequest, visitMembers func(context.Context, func(string) error) error) (PopulationMappingResult, error) {
	if strings.TrimSpace(request.Output) == "" {
		return PopulationMappingResult{}, fmt.Errorf("population mapping output is required")
	}
	compiled, err := compilePopulationMappingOutput(resolved, request.Output)
	if err != nil {
		return PopulationMappingResult{}, err
	}
	return e.populationMappingCompiled(ctx, compiled, request, visitMembers)
}

func (e *Engine) populationMappingCompiled(ctx context.Context, compiled population.CompiledPopulationMappingQuery, request PopulationMappingRequest, visitMembers func(context.Context, func(string) error) error) (PopulationMappingResult, error) {
	if e == nil || e.queryRows == nil {
		return PopulationMappingResult{}, fmt.Errorf("population mapping query executor is required")
	}
	if visitMembers == nil {
		return PopulationMappingResult{}, fmt.Errorf("population mapping member reader is required")
	}
	limit := request.MaxUnmapped
	if limit <= 0 {
		limit = defaultPopulationMappingUnmapped
	}
	if limit > maxPopulationMappingUnmapped {
		return PopulationMappingResult{}, fmt.Errorf("population mapping unmapped limit exceeds %d", maxPopulationMappingUnmapped)
	}
	maxMembers := request.MaxSelectedMembers
	if maxMembers <= 0 {
		maxMembers = defaultPopulationMappingMembers
	}
	maxWitness := request.MaxWitnessRows
	if maxWitness <= 0 {
		maxWitness = defaultPopulationMappingWitness
	}

	selected := make(map[string]struct{})
	if err := visitMembers(ctx, func(id string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("population mapping member ID is empty")
		}
		selected[id] = struct{}{}
		if int64(len(selected)) > maxMembers {
			return errPopulationMappingLimit
		}
		return nil
	}); err != nil {
		if populationMappingIncomplete(err) {
			return incompletePopulationMappingResult(), nil
		}
		return PopulationMappingResult{}, fmt.Errorf("read population members: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return incompletePopulationMappingResult(), nil
	}

	if compiled.MemberColumn == "" || (compiled.IdentityPartsColumn == "" && compiled.ExplicitIdentityColumn == "") {
		return PopulationMappingResult{}, fmt.Errorf("population mapping query is missing typed witness columns")
	}

	mapped := make(map[string]struct{})
	emitted := make(map[string]struct{})
	witnessRows := int64(0)
	err := e.queryRows(ctx, compiled.Query, e.batchSize, compiled.BindVars, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		witnessRows++
		if witnessRows > maxWitness {
			return errPopulationMappingLimit
		}
		memberID, ok := row[compiled.MemberColumn].(string)
		memberID = strings.TrimSpace(memberID)
		if !ok || memberID == "" {
			return fmt.Errorf("population mapping witness has an invalid member ID")
		}
		if _, ok := selected[memberID]; !ok {
			return fmt.Errorf("population mapping witness member is not selected")
		}
		identity, identityErr := populationWitnessIdentity(compiled, row)
		if identityErr != nil {
			return identityErr
		}
		mapped[memberID] = struct{}{}
		emitted[identity] = struct{}{}
		return nil
	})
	if err != nil {
		if populationMappingIncomplete(err) {
			return incompletePopulationMappingResult(), nil
		}
		return PopulationMappingResult{}, fmt.Errorf("execute population mapping: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return incompletePopulationMappingResult(), nil
	}

	selectedIDs := make([]string, 0, len(selected))
	for id := range selected {
		selectedIDs = append(selectedIDs, id)
	}
	sort.Strings(selectedIDs)
	after := strings.TrimSpace(request.AfterMemberID)
	unmapped := make([]string, 0, min(limit, len(selectedIDs)))
	hasMore := false
	for _, id := range selectedIDs {
		if _, ok := mapped[id]; ok || (after != "" && id <= after) {
			continue
		}
		if len(unmapped) == limit {
			hasMore = true
			break
		}
		unmapped = append(unmapped, id)
	}
	return PopulationMappingResult{
		Status:            PopulationMappingComplete,
		SelectedCount:     int64(len(selected)),
		MappedCount:       int64(len(mapped)),
		UnmappedCount:     int64(len(selected) - len(mapped)),
		EmittedRows:       int64(len(emitted)),
		UnmappedMemberIDs: unmapped,
		HasMoreUnmapped:   hasMore,
	}, nil
}
