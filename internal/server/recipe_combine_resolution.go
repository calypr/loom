package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type exactMaterializationReader interface {
	ExactExecutionMaterialization(context.Context, string, string) (published.Materialization, error)
}

type clickHouseCombineInputResolver struct {
	reader exactMaterializationReader
	scopes *authscope.ScopeResolver
}

func (r clickHouseCombineInputResolver) resolve(ctx context.Context, combine ir.PhysicalClickHouseCombine, bindings recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error) {
	if r.reader == nil {
		return nil, fmt.Errorf("published dataframe reader is required for ClickHouse combine")
	}
	if len(combine.Inputs) < 2 {
		return nil, fmt.Errorf("ClickHouse combine requires at least two exact inputs")
	}
	if strings.TrimSpace(bindings.Project) == "" {
		return nil, fmt.Errorf("ClickHouse combine project is required")
	}
	requestedScope, err := scopeFromRecipeBindings(bindings)
	if err != nil {
		return nil, err
	}

	resolved := make([]ir.ResolvedClickHouseTable, 0, len(combine.Inputs))
	for index, reference := range combine.Inputs {
		if strings.TrimSpace(reference.TableID) == "" || strings.TrimSpace(reference.RevisionID) == "" || strings.TrimSpace(reference.OutputID) == "" {
			return nil, fmt.Errorf("ClickHouse combine input %d requires exact table, revision, and output IDs", index)
		}
		materialization, err := r.reader.ExactExecutionMaterialization(ctx, reference.RevisionID, reference.OutputID)
		if err != nil {
			return nil, fmt.Errorf("resolve exact ClickHouse combine input %d: %w", index, err)
		}
		input, err := r.resolveMaterialization(ctx, index, reference, materialization, bindings, requestedScope)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, input)
	}
	return resolved, nil
}

func (r clickHouseCombineInputResolver) resolveMaterialization(ctx context.Context, index int, reference ir.PhysicalCombineInputRef, materialization published.Materialization, bindings recipe.RuntimeBindings, requestedScope authscope.ReadScope) (ir.ResolvedClickHouseTable, error) {
	if materialization.State != published.StateReady {
		return ir.ResolvedClickHouseTable{}, fmt.Errorf("ClickHouse combine input %d is not a published queryable materialization", index)
	}
	if materialization.Revision != reference.RevisionID || !materialization.Selector.Valid() || materialization.Selector.Key() != reference.TableID || materialization.Selector.Output != reference.OutputID {
		return ir.ResolvedClickHouseTable{}, fmt.Errorf("ClickHouse combine input %d does not match its exact table, revision, and output identity", index)
	}
	if materialization.Project != bindings.Project || materialization.DatasetGeneration != bindings.DatasetGeneration {
		return ir.ResolvedClickHouseTable{}, fmt.Errorf("ClickHouse combine input %d belongs to a different project or dataset generation", index)
	}
	if strings.TrimSpace(materialization.ReceiptID) == "" || strings.TrimSpace(materialization.SchemaDigest) == "" || strings.TrimSpace(materialization.ScopeDigest) == "" || strings.TrimSpace(materialization.PhysicalTable) == "" {
		return ir.ResolvedClickHouseTable{}, fmt.Errorf("ClickHouse combine input %d is missing immutable publication metadata", index)
	}

	persistedScope, err := persistedMaterializationScope(materialization)
	if err != nil {
		return ir.ResolvedClickHouseTable{}, fmt.Errorf("ClickHouse combine input %d has invalid persisted authorization metadata: %w", index, err)
	}
	persistedBindings := recipe.RuntimeBindings{
		Project: materialization.Project, DatasetGeneration: materialization.DatasetGeneration,
		AuthScopeMode: persistedScope.Mode, AuthResourcePaths: append([]string(nil), persistedScope.AuthResourcePaths...),
	}
	if materialization.ScopeDigest != recipeScopeDigest(persistedBindings) {
		return ir.ResolvedClickHouseTable{}, fmt.Errorf("ClickHouse combine input %d authorization metadata does not match its persisted scope digest", index)
	}

	effectiveScope := persistedScope
	if r.scopes != nil {
		principal, _ := authscope.PrincipalFromContext(ctx)
		effectiveScope, err = r.scopes.ResolveReadScopeForGeneration(ctx, principal, materialization.Project, materialization.DatasetGeneration, persistedScope.AuthResourcePaths)
		if err != nil {
			return ir.ResolvedClickHouseTable{}, recipeAuthorizationError(err)
		}
	}
	if effectiveScope.Mode == authscope.ReadScopeRestricted && len(effectiveScope.AuthResourcePaths) == 0 {
		return ir.ResolvedClickHouseTable{}, fmt.Errorf("ClickHouse combine input %d has an empty restricted authorization scope", index)
	}
	if !sameClickHouseReadScope(effectiveScope, requestedScope) {
		return ir.ResolvedClickHouseTable{}, fmt.Errorf("ClickHouse combine input %d authorization scope differs from the requested output scope", index)
	}

	columns, err := resolvedClickHouseColumns(materialization.Columns)
	if err != nil {
		return ir.ResolvedClickHouseTable{}, fmt.Errorf("ClickHouse combine input %d has an invalid published schema: %w", index, err)
	}
	return ir.ResolvedClickHouseTable{
		TableID: reference.TableID, RevisionID: reference.RevisionID, OutputID: reference.OutputID,
		Recipe: materialization.Selector.Recipe, TranslationVersion: materialization.Selector.TranslationVersion,
		Project: materialization.Project, DatasetGeneration: materialization.DatasetGeneration,
		ReceiptID: materialization.ReceiptID, SchemaDigest: materialization.SchemaDigest, ScopeDigest: materialization.ScopeDigest,
		PhysicalTable: materialization.PhysicalTable, Unrestricted: effectiveScope.Unrestricted(),
		AuthResourcePaths: append([]string(nil), effectiveScope.AuthResourcePaths...), Columns: columns,
	}, nil
}

func scopeFromRecipeBindings(bindings recipe.RuntimeBindings) (authscope.ReadScope, error) {
	scope := authscope.ReadScope{Mode: bindings.AuthScopeMode, AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...)}
	switch scope.Mode {
	case authscope.ReadScopeUnrestricted:
		if len(scope.AuthResourcePaths) != 0 {
			return authscope.ReadScope{}, fmt.Errorf("unrestricted ClickHouse combine scope cannot carry path filters")
		}
	case authscope.ReadScopeRestricted:
		if len(scope.AuthResourcePaths) == 0 {
			return authscope.ReadScope{}, fmt.Errorf("restricted ClickHouse combine scope cannot be empty")
		}
	default:
		return authscope.ReadScope{}, fmt.Errorf("ClickHouse combine requires an explicit authorization scope mode")
	}
	return scope, nil
}

func persistedMaterializationScope(materialization published.Materialization) (authscope.ReadScope, error) {
	scope := authscope.ReadScope{
		Mode:              authscope.ReadScopeMode(materialization.AuthScopeMode),
		AuthResourcePaths: append([]string(nil), materialization.AuthResourcePaths...),
	}
	switch scope.Mode {
	case authscope.ReadScopeUnrestricted:
		if !materialization.ScopeUnrestricted || len(scope.AuthResourcePaths) != 0 {
			return authscope.ReadScope{}, fmt.Errorf("unrestricted scope metadata is inconsistent")
		}
	case authscope.ReadScopeRestricted:
		if materialization.ScopeUnrestricted || len(scope.AuthResourcePaths) == 0 {
			return authscope.ReadScope{}, fmt.Errorf("restricted scope metadata is empty or inconsistent")
		}
	default:
		return authscope.ReadScope{}, fmt.Errorf("scope mode %q is unsupported", scope.Mode)
	}
	return scope, nil
}

func sameClickHouseReadScope(left, right authscope.ReadScope) bool {
	if left.Mode != right.Mode {
		return false
	}
	leftPaths := canonicalClickHouseScopePaths(left.AuthResourcePaths)
	rightPaths := canonicalClickHouseScopePaths(right.AuthResourcePaths)
	if len(leftPaths) != len(rightPaths) {
		return false
	}
	for index := range leftPaths {
		if leftPaths[index] != rightPaths[index] {
			return false
		}
	}
	return true
}

func canonicalClickHouseScopePaths(paths []string) []string {
	result := append([]string(nil), paths...)
	sort.Strings(result)
	unique := result[:0]
	for _, path := range result {
		if len(unique) == 0 || unique[len(unique)-1] != path {
			unique = append(unique, path)
		}
	}
	return unique
}

func resolvedClickHouseColumns(columns []published.Column) ([]ir.ResolvedClickHouseColumn, error) {
	resolved := make([]ir.ResolvedClickHouseColumn, 0, len(columns))
	seenIDs := make(map[string]bool, len(columns))
	for index, column := range columns {
		if column.ID == "" && column.Name != "__loom_row_id" && column.Name != "auth_resource_path" && column.Name != "project_id" {
			return nil, fmt.Errorf("column %d %q has no persisted stable ID", index, column.Name)
		}
		if column.ID != "" && (column.ID != strings.TrimSpace(column.ID) || seenIDs[column.ID]) {
			return nil, fmt.Errorf("column %d has a duplicate or untrimmed stable ID", index)
		}
		if column.ID != "" {
			seenIDs[column.ID] = true
		}
		resolved = append(resolved, ir.ResolvedClickHouseColumn{
			ID: column.ID, Name: column.Name, SemanticPath: column.SemanticPath,
			LogicalType: column.LogicalType, ClickHouseType: column.ClickHouse,
			Nullable: column.Nullable, Repeated: column.Repeated,
		})
	}
	return resolved, nil
}
