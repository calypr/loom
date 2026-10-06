package clickhouse

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// WorkspaceArtifact is one exact compiler-resolved same-bundle output capture.
// ExpectedIdentity is derived from the source OutputStream and compiler schema,
// not from manifest values supplied by the caller.
type WorkspaceArtifact struct {
	OutputID         string
	ExpectedIdentity ir.ClickHouseArtifactIdentity
	Artifact         ir.ResolvedClickHousePrivateArtifact
}

// RenderWorkspaceClickHouseCombine resolves a terminal workspace Combine in
// authored input order. Private captures are keyed by stable output ID;
// published tables are supplied only for the exact published refs in plan.
func RenderWorkspaceClickHouseCombine(
	plan ir.PhysicalClickHouseCombine,
	workspace []WorkspaceArtifact,
	published []ir.ResolvedClickHouseTable,
	project, generation string,
	limit int,
) (RenderedCombine, error) {
	if limit < 0 {
		return RenderedCombine{}, fmt.Errorf("ClickHouse combine limit cannot be negative")
	}
	if err := plan.ValidateWithWorkspaceArtifacts(); err != nil {
		return RenderedCombine{}, err
	}
	if strings.TrimSpace(project) == "" || strings.TrimSpace(generation) == "" {
		return RenderedCombine{}, fmt.Errorf("workspace ClickHouse combine requires exact project and dataset generation")
	}
	artifacts := make(map[string]WorkspaceArtifact, len(workspace))
	for index, source := range workspace {
		if strings.TrimSpace(source.OutputID) == "" || source.OutputID != source.ExpectedIdentity.OutputID || source.OutputID != source.Artifact.Identity.OutputID {
			return RenderedCombine{}, fmt.Errorf("workspace artifact %d does not carry its exact output ID", index)
		}
		if _, exists := artifacts[source.OutputID]; exists {
			return RenderedCombine{}, fmt.Errorf("workspace artifact output ID %q is duplicated", source.OutputID)
		}
		if err := validateArtifactIdentity(source.ExpectedIdentity, source.Artifact.Identity); err != nil {
			return RenderedCombine{}, fmt.Errorf("workspace artifact %q: %w", source.OutputID, err)
		}
		if source.ExpectedIdentity.ScopeMode != source.Artifact.Identity.ScopeMode || source.ExpectedIdentity.ScopeEvidenceDigest != source.Artifact.Identity.ScopeEvidenceDigest {
			return RenderedCombine{}, fmt.Errorf("workspace artifact %q scope evidence differs from its compiler identity", source.OutputID)
		}
		if source.ExpectedIdentity.Project != project || source.ExpectedIdentity.DatasetGeneration != generation {
			return RenderedCombine{}, fmt.Errorf("workspace artifact %q belongs to a different project or dataset generation", source.OutputID)
		}
		if !identifierPattern.MatchString(source.Artifact.PhysicalTable) || !strings.HasPrefix(source.Artifact.PhysicalTable, "loom_private_") || strings.TrimSpace(source.Artifact.ArtifactID) == "" {
			return RenderedCombine{}, fmt.Errorf("workspace artifact %q has an invalid private table identity", source.OutputID)
		}
		if source.ExpectedIdentity.ScopeMode == ir.ClickHouseArtifactScopeWhole {
			if strings.TrimSpace(source.ExpectedIdentity.ScopeEvidenceDigest) == "" {
				return RenderedCombine{}, fmt.Errorf("workspace artifact %q is missing whole-scope compiler evidence", source.OutputID)
			}
		} else if source.ExpectedIdentity.ScopeMode == ir.ClickHouseArtifactScopeRows {
			if source.ExpectedIdentity.ScopeEvidenceDigest != "" {
				return RenderedCombine{}, fmt.Errorf("row-scoped workspace artifact %q cannot carry whole-scope evidence", source.OutputID)
			}
		} else {
			return RenderedCombine{}, fmt.Errorf("workspace artifact %q has an unsupported scope mode", source.OutputID)
		}
		artifacts[source.OutputID] = source
	}

	inputs := make([]ir.ResolvedClickHouseTable, 0, len(plan.Inputs))
	publishedIndex := 0
	seenWorkspace := make(map[string]bool, len(plan.Inputs))
	for index, ref := range plan.Inputs {
		if ref.WorkspaceOutputID != "" {
			source, ok := artifacts[ref.WorkspaceOutputID]
			if !ok {
				return RenderedCombine{}, fmt.Errorf("workspace Combine input %d has no captured output %q", index, ref.WorkspaceOutputID)
			}
			seenWorkspace[ref.WorkspaceOutputID] = true
			identity := source.Artifact.Identity
			inputs = append(inputs, ir.ResolvedClickHouseTable{
				Project: identity.Project, DatasetGeneration: identity.DatasetGeneration,
				SchemaDigest: identity.SchemaDigest, ScopeDigest: identity.ScopeDigest,
				PhysicalTable:     source.Artifact.PhysicalTable,
				Unrestricted:      identity.AuthScopeMode == "unrestricted",
				AuthResourcePaths: append([]string(nil), identity.AuthResourcePaths...),
				Columns:           append([]ir.ResolvedClickHouseColumn(nil), source.Artifact.Columns...),
				PrivateArtifact:   &source.Artifact,
				ScopeMode:         identity.ScopeMode, ScopeEvidenceDigest: identity.ScopeEvidenceDigest,
			})
			continue
		}
		if publishedIndex >= len(published) {
			return RenderedCombine{}, fmt.Errorf("resolved published ClickHouse inputs are incomplete")
		}
		inputs = append(inputs, published[publishedIndex])
		publishedIndex++
	}
	if publishedIndex != len(published) || len(seenWorkspace) != len(artifacts) {
		return RenderedCombine{}, fmt.Errorf("workspace Combine received unused or incomplete resolved inputs")
	}
	if err := validateWorkspaceResolvedInputs(plan, inputs, project, generation); err != nil {
		return RenderedCombine{}, err
	}
	if err := validateResolvedSchema(plan, inputs); err != nil {
		return RenderedCombine{}, err
	}
	if plan.Kind == ir.PhysicalCombineAppend {
		if err := validateAppendProjectionTypes(plan, inputs); err != nil {
			return RenderedCombine{}, err
		}
	}
	var query string
	var args []any
	switch plan.Kind {
	case ir.PhysicalCombineKeyJoin:
		query, args = renderWorkspaceKeyJoin(plan, inputs)
	case ir.PhysicalCombineAppend:
		query, args = renderWorkspaceAppend(plan, inputs)
	case ir.PhysicalCombineMembership:
		query, args = renderWorkspaceMembership(plan, inputs)
	default:
		return RenderedCombine{}, fmt.Errorf("unsupported ClickHouse combine kind %q", plan.Kind)
	}
	if limit > 0 {
		query = applyQueryLimit(query, limit)
	}
	return RenderedCombine{Query: query, Args: args, Columns: combineQueryColumns(plan)}, nil
}

func validateWorkspaceResolvedInputs(plan ir.PhysicalClickHouseCombine, inputs []ir.ResolvedClickHouseTable, project, generation string) error {
	if len(inputs) != len(plan.Inputs) {
		return fmt.Errorf("resolved ClickHouse input count %d does not match plan count %d", len(inputs), len(plan.Inputs))
	}
	var unrestricted *bool
	var paths []string
	for index, input := range inputs {
		ref := plan.Inputs[index]
		if input.Project != project || input.DatasetGeneration != generation || !identifierPattern.MatchString(input.PhysicalTable) {
			return fmt.Errorf("resolved ClickHouse input %d has an invalid project, generation, or physical table", index)
		}
		if input.Unrestricted && len(input.AuthResourcePaths) != 0 {
			return fmt.Errorf("resolved ClickHouse input %d has an unrestricted scope with path filters", index)
		}
		if !input.Unrestricted && len(input.AuthResourcePaths) == 0 {
			return fmt.Errorf("resolved ClickHouse input %d has an empty restricted authorization scope", index)
		}
		scopeMode := input.ScopeMode
		if scopeMode == "" && ref.WorkspaceOutputID == "" {
			// Older pinned TABLE_REVISION records predate explicit artifact
			// scope modes and always retained row-level authorization paths.
			scopeMode = ir.ClickHouseArtifactScopeRows
		}
		switch scopeMode {
		case ir.ClickHouseArtifactScopeRows:
			if input.ScopeEvidenceDigest != "" {
				return fmt.Errorf("resolved ClickHouse input %d row scope cannot carry whole-scope evidence", index)
			}
		case ir.ClickHouseArtifactScopeWhole:
			if !validScopeEvidenceDigest(input.ScopeEvidenceDigest) {
				return fmt.Errorf("resolved ClickHouse input %d whole scope lacks valid compiler evidence", index)
			}
		default:
			return fmt.Errorf("resolved ClickHouse input %d has an unsupported artifact scope mode", index)
		}
		if unrestricted == nil {
			value := input.Unrestricted
			unrestricted = &value
			paths = append([]string(nil), input.AuthResourcePaths...)
		} else if input.Unrestricted != *unrestricted || !sameStrings(input.AuthResourcePaths, paths) {
			return fmt.Errorf("workspace and published ClickHouse inputs must share one complete authorization scope")
		}
		if ref.WorkspaceOutputID != "" {
			artifact := input.PrivateArtifact
			if artifact == nil || artifact.Identity.OutputID != ref.WorkspaceOutputID || input.TableID != "" || input.RevisionID != "" || input.OutputID != "" {
				return fmt.Errorf("resolved ClickHouse input %d does not match its exact workspace output reference", index)
			}
			if artifact.Identity.Project != project || artifact.Identity.DatasetGeneration != generation || artifact.Identity.AuthScopeMode != map[bool]string{true: "unrestricted", false: "restricted"}[input.Unrestricted] || !sameStrings(artifact.Identity.AuthResourcePaths, paths) ||
				artifact.Identity.ScopeMode != input.ScopeMode || artifact.Identity.ScopeEvidenceDigest != input.ScopeEvidenceDigest {
				return fmt.Errorf("resolved workspace artifact %q differs from the complete request scope", ref.WorkspaceOutputID)
			}
			if input.ScopeMode == ir.ClickHouseArtifactScopeWhole {
				if input.ScopeEvidenceDigest == "" || artifact.Identity.ScopeEvidenceDigest != input.ScopeEvidenceDigest {
					return fmt.Errorf("resolved workspace artifact %q has invalid whole-scope evidence", ref.WorkspaceOutputID)
				}
			} else if input.ScopeMode != "" && input.ScopeMode != ir.ClickHouseArtifactScopeRows {
				return fmt.Errorf("resolved workspace artifact %q has an unsupported scope mode", ref.WorkspaceOutputID)
			}
			continue
		}
		if input.PrivateArtifact != nil || input.PrivateStageID != "" || input.TableID != ref.TableID || input.RevisionID != ref.RevisionID || input.OutputID != ref.OutputID {
			return fmt.Errorf("resolved ClickHouse input %d does not match its exact published reference", index)
		}
		if input.RevisionID == "" || input.OutputID == "" || input.SchemaDigest == "" || input.ReceiptID == "" || input.ScopeDigest == "" {
			return fmt.Errorf("resolved published ClickHouse input %d is missing immutable publication identity", index)
		}
	}
	return nil
}

func containsWholeScope(inputs []ir.ResolvedClickHouseTable) bool {
	for _, input := range inputs {
		if input.ScopeMode == ir.ClickHouseArtifactScopeWhole {
			return true
		}
	}
	return false
}

func workspaceInputScopePredicate(input ir.ResolvedClickHouseTable, alias string) ([]string, []any) {
	if input.ScopeMode == ir.ClickHouseArtifactScopeWhole {
		return nil, nil
	}
	return inputScopePredicate(input, alias)
}

func renderWorkspaceKeyJoin(plan ir.PhysicalClickHouseCombine, inputs []ir.ResolvedClickHouseTable) (string, []any) {
	if !containsWholeScope(inputs) {
		return renderKeyJoin(plan, inputs)
	}
	left, right := inputs[0], inputs[1]
	leftAlias, rightAlias := "__loom_left", "__loom_right"
	selects := []string{keyJoinIdentity(leftAlias, rightAlias, plan.JoinType)}
	for _, projection := range plan.Projections {
		alias := leftAlias
		if projection.InputIndex == 1 {
			alias = rightAlias
		}
		column := resolvedColumnByID(inputs[projection.InputIndex], projection.InputColumnID)
		selects = append(selects, fmt.Sprintf("%s.`%s` AS `%s`", alias, column.Name, outputColumn(plan, projection.OutputColumnID).Name))
	}
	selects = append(selects, "CAST(NULL, 'Nullable(String)') AS `auth_resource_path`")
	joinType := "INNER"
	if plan.JoinType == "LEFT" {
		joinType = "LEFT"
	}
	conditions := make([]string, 0, len(plan.Keys)+1)
	for _, key := range plan.Keys {
		leftColumn := resolvedColumnByID(left, key.LeftColumnID)
		rightColumn := resolvedColumnByID(right, key.RightColumnID)
		conditions = append(conditions, fmt.Sprintf("%s.`%s` = %s.`%s`", leftAlias, leftColumn.Name, rightAlias, rightColumn.Name))
	}
	if rightWhere, _ := workspaceInputScopePredicate(right, rightAlias); len(rightWhere) > 0 {
		conditions = append(conditions, rightWhere...)
	}
	query := "SELECT " + strings.Join(selects, ", ") + " FROM " + quoteIdentifier(left.PhysicalTable) + " AS " + leftAlias + " ALL " + joinType + " JOIN " + quoteIdentifier(right.PhysicalTable) + " AS " + rightAlias + " ON " + strings.Join(conditions, " AND ")
	where, leftArgs := workspaceInputScopePredicate(left, leftAlias)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	_, rightArgs := workspaceInputScopePredicate(right, rightAlias)
	args := append([]any(nil), rightArgs...)
	args = append(args, leftArgs...)
	query += " ORDER BY `__loom_row_id` ASC SETTINGS join_use_nulls = 1"
	return query, args
}

func renderWorkspaceAppend(plan ir.PhysicalClickHouseCombine, inputs []ir.ResolvedClickHouseTable) (string, []any) {
	if !containsWholeScope(inputs) {
		return renderAppend(plan, inputs)
	}
	queries := make([]string, 0, len(inputs))
	var args []any
	for inputIndex, input := range inputs {
		alias := fmt.Sprintf("__loom_input_%d", inputIndex)
		selects := []string{appendIdentity(alias, inputIndex)}
		for _, output := range plan.Outputs {
			projection := projectionFor(plan, inputIndex, output.ID)
			if projection.InputColumnID == "" {
				selects = append(selects, fmt.Sprintf("CAST(NULL, '%s') AS `%s`", output.ClickHouseType, output.Name))
				continue
			}
			column := resolvedColumnByID(input, projection.InputColumnID)
			selects = append(selects, fmt.Sprintf("%s.`%s` AS `%s`", alias, column.Name, output.Name))
		}
		selects = append(selects, "CAST(NULL, 'Nullable(String)') AS `auth_resource_path`")
		query := "SELECT " + strings.Join(selects, ", ") + " FROM " + quoteIdentifier(input.PhysicalTable) + " AS " + alias
		if where, scopeArgs := workspaceInputScopePredicate(input, alias); len(where) != 0 {
			query += " WHERE " + strings.Join(where, " AND ")
			args = append(args, scopeArgs...)
		}
		queries = append(queries, query)
	}
	return strings.Join(queries, " UNION ALL ") + " ORDER BY `__loom_row_id` ASC", args
}

func renderWorkspaceMembership(plan ir.PhysicalClickHouseCombine, inputs []ir.ResolvedClickHouseTable) (string, []any) {
	if !containsWholeScope(inputs) {
		return renderMembership(plan, inputs)
	}
	left, right := inputs[0], inputs[1]
	leftAlias, rightAlias, rightSourceAlias := "__loom_left", "__loom_members", "__loom_member_source"
	selects := []string{leftAlias + ".`__loom_row_id` AS `__loom_row_id`"}
	for _, output := range plan.Outputs {
		projection := projectionFor(plan, 0, output.ID)
		column := resolvedColumnByID(left, projection.InputColumnID)
		selects = append(selects, fmt.Sprintf("%s.`%s` AS `%s`", leftAlias, column.Name, output.Name))
	}
	selects = append(selects, "CAST(NULL, 'Nullable(String)') AS `auth_resource_path`")
	rightKeys := make([]string, 0, len(plan.Keys)+1)
	conditions := make([]string, 0, len(plan.Keys))
	for index, key := range plan.Keys {
		leftColumn := resolvedColumnByID(left, key.LeftColumnID)
		rightColumn := resolvedColumnByID(right, key.RightColumnID)
		rightKeys = append(rightKeys, fmt.Sprintf("%s.`%s` AS `__loom_key_%d`", rightSourceAlias, rightColumn.Name, index))
		conditions = append(conditions, fmt.Sprintf("%s.`%s` = %s.`__loom_key_%d`", leftAlias, leftColumn.Name, rightAlias, index))
	}
	rightKeys = append(rightKeys, "1 AS `__loom_match`")
	mode := "IS NOT NULL"
	if plan.MembershipMode == "EXCLUDE" {
		mode = "IS NULL"
	}
	rightQuery := "SELECT DISTINCT " + strings.Join(rightKeys, ", ") + " FROM " + quoteIdentifier(right.PhysicalTable) + " AS " + rightSourceAlias
	rightWhere, args := workspaceInputScopePredicate(right, rightSourceAlias)
	if len(rightWhere) > 0 {
		rightQuery += " WHERE " + strings.Join(rightWhere, " AND ")
	}
	query := "SELECT " + strings.Join(selects, ", ") + " FROM " + quoteIdentifier(left.PhysicalTable) + " AS " + leftAlias + " LEFT ANY JOIN (" + rightQuery + ") AS " + rightAlias + " ON " + strings.Join(conditions, " AND ")
	leftWhere, leftArgs := workspaceInputScopePredicate(left, leftAlias)
	where := append(leftWhere, rightAlias+".`__loom_match` "+mode)
	query += " WHERE " + strings.Join(where, " AND ")
	args = append(args, leftArgs...)
	query += " ORDER BY `__loom_row_id` ASC SETTINGS join_use_nulls = 1"
	return query, args
}
