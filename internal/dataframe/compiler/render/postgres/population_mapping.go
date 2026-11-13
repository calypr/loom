package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const populationMappingMembersColumnAlias = "__loom_population_members"

const populationMappingContributorColumn = "__loom_root_contributor_keys"

type populationMappingRender struct {
	witness          bool
	construction     bool
	terminal         *ir.PhysicalStagePopulationMappingReturn
	membersColumnRef string
	memberOrderTerm  string
}

func ValidatePopulationMappingSource(plan ir.PhysicalPlan, binding PostgresPopulationRootBinding) error {
	if err := validatePopulationMappingPlanSurface(plan, binding); err != nil {
		return err
	}
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("validate physical plan: %w", err)
	}
	return nil
}

func RenderPopulationMappingPlan(
	plan ir.PhysicalPlan,
	storage ir.PostgresStorageBinding,
	binding PostgresPopulationRootBinding,
) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validatePopulationMappingPlanSurface(plan, binding); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if plan.StageSequence != nil {
		return renderPopulationMappingConstruction(plan, storage, binding)
	}
	return renderPopulationMappingFlat(plan, storage, binding)
}

func validatePopulationMappingPlanSurface(plan ir.PhysicalPlan, binding PostgresPopulationRootBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: population mapping plan has no operations", ErrUnsupportedPostgresPlan)
	}
	if plan.Engine != "" && plan.Engine != ir.PhysicalEngineAQL {
		return fmt.Errorf(
			"%w: engine %q has no PostgreSQL source; the source graph IR is backend-neutral and ClickHouse plans are not PostgreSQL-rendered",
			ErrUnsupportedPostgresPlan, string(plan.Engine))
	}
	if plan.ClickHouseCombine != nil || plan.ClickHousePrefix != nil {
		return fmt.Errorf("%w: ClickHouse combine/prefix payloads have no PostgreSQL rendering", ErrUnsupportedPostgresPlan)
	}
	if len(plan.DeferredExpressionLets) != 0 {
		return fmt.Errorf("%w: completed plans must not carry deferred expression lets", ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID {
		return fmt.Errorf("%w: preview source windowing is not implemented for the PostgreSQL population mapping terminals",
			ErrUnsupportedPostgresPlan)
	}
	root := plan.Operations[0]
	if root.Kind != ir.PhysicalRootScanOp || root.RootScan == nil {
		return fmt.Errorf("%w: the leading operation must be ROOT_SCAN with its payload set", ErrUnsupportedPostgresPlan)
	}
	scan := *root.RootScan
	if scan.Population == nil {
		return fmt.Errorf("%w: population mapping requires a membership-driven population root source", ErrUnsupportedPostgresPlan)
	}
	if scan.CohortSource != nil {
		return fmt.Errorf("%w: population mapping cannot combine cohort and population sources", ErrUnsupportedPostgresPlan)
	}
	if scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population mapping cannot combine with the candidate seed, which stays unregistered",
			ErrUnsupportedPostgresPlan)
	}
	if err := requireMembershipRootBinds(scan); err != nil {
		return err
	}
	if _, err := validatePopulationPayload(plan, scan, true); err != nil {
		return err
	}
	if plan.StageSequence != nil {
		if err := validateStageSequencePlanSurface(plan, true); err != nil {
			return err
		}
		return validatePopulationMappingConstructionSurface(plan)
	}
	return validatePopulationMappingFlatSurface(plan)
}

func validatePopulationMappingConstructionSurface(plan ir.PhysicalPlan) error {
	found := false
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name != ir.PhysicalPopulationMappingMembersColumn {
				continue
			}
			if !projection.Hidden || projection.Expression != nil ||
				projection.Value.Variable != ir.PopulationMappingMembersVariable || len(projection.Value.Path) != 0 {
				return fmt.Errorf("%w: source projection %q is not the collected selected member IDs",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w: the composition mapping source RETURN omits the compiler-owned member witness projection %q (compiler/population_mapping.go adds it)",
			ErrUnsupportedPostgresPlan, ir.PhysicalPopulationMappingMembersColumn)
	}
	if key := plan.StageSequence.PreviewLimitBindKey; key != "" {
		if limit, ok := plan.BindVars[key].(int); !ok || limit <= 0 {
			return fmt.Errorf("%w: composition preview limit bind %q must be a positive int (render/aql/stage_sequence.go:452-453 twin)",
				ErrUnsupportedPostgresPlan, key)
		}
	}
	return nil
}

func validatePopulationMappingFlatSurface(plan ir.PhysicalPlan) error {
	sawTerminal := false
	for index, operation := range plan.Operations {
		last := index == len(plan.Operations)-1
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return fmt.Errorf("%w: exactly one leading ROOT_SCAN is required", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalPopulationMappingReturnOp:
			if !last || sawTerminal {
				return fmt.Errorf("%w: POPULATION_MAPPING_RETURN must be the single final operation", ErrUnsupportedPostgresPlan)
			}
			sawTerminal = true
			if operation.PopulationMappingReturn == nil {
				return fmt.Errorf("%w: operation %d: population-mapping payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := validatePopulationMappingReturnShape(*operation.PopulationMappingReturn); err != nil {
				return fmt.Errorf("operation %d: %w", index, err)
			}
		case ir.PhysicalUnnestOp, ir.PhysicalSetOp:
			return fmt.Errorf("%w: operation %q on a member-collecting mapping root is a named capability gap (unnest.go/sets.go bind their own root contracts); the shape is refused, never partially rendered",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		case ir.PhysicalReturnOp:
			return fmt.Errorf("%w: the population mapping terminal replaces the RETURN projection list; a plan carrying both is not the registered shape",
				ErrUnsupportedPostgresPlan)
		}
	}
	if !sawTerminal {
		return fmt.Errorf("%w: the flat population mapping requires a terminal POPULATION_MAPPING_RETURN", ErrUnsupportedPostgresPlan)
	}
	return nil
}

func validatePopulationMappingReturnShape(terminal ir.PhysicalPopulationMappingReturn) error {
	if terminal.Members.Kind != ir.PhysicalValueExpression ||
		terminal.Members.Value == nil || terminal.Members.Value.Variable != ir.PopulationMappingMembersVariable ||
		len(terminal.Members.Value.Path) != 0 || terminal.Members.Value.BindKey != "" {
		return fmt.Errorf("%w: mapping Members must be the collected-member value over %q",
			ErrUnsupportedPostgresPlan, ir.PopulationMappingMembersVariable)
	}
	if terminal.ExplicitIdentity == nil && len(terminal.IdentityParts) == 0 {
		return fmt.Errorf("%w: mapping identity has no renderable value", ErrUnsupportedPostgresPlan)
	}
	for index, part := range terminal.IdentityParts {
		if strings.TrimSpace(part.Name) == "" {
			return fmt.Errorf("%w: mapping identity part %d has no name", ErrUnsupportedPostgresPlan, index)
		}
	}
	return nil
}

func renderPopulationMappingFlat(
	plan ir.PhysicalPlan,
	storage ir.PostgresStorageBinding,
	binding PostgresPopulationRootBinding,
) (command.RenderedPostgresPhysicalPlan, error) {
	bound := binding
	renderer := &planRenderer{
		storage:           storage,
		rows:              map[string]rowScope{},
		parameters:        map[string]int{},
		canonicalAuthLets: map[string]string{},
		membership:        &bound,
		mapping:           &populationMappingRender{witness: true},
	}
	if err := renderer.loadScope(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := plan.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}
	if err := renderer.renderOperations(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(
		renderer.assemblePopulationMapping(), renderer.args, renderer.outputColumns)
}

func renderPopulationMappingConstruction(
	plan ir.PhysicalPlan,
	storage ir.PostgresStorageBinding,
	binding PostgresPopulationRootBinding,
) (command.RenderedPostgresPhysicalPlan, error) {
	bound := binding
	renderer := &planRenderer{
		storage:           storage,
		rows:              map[string]rowScope{},
		parameters:        map[string]int{},
		canonicalAuthLets: map[string]string{},
		stageRows:         map[string]stageRowScope{},
		membership:        &bound,
		mapping: &populationMappingRender{
			witness:      true,
			construction: true,
			terminal:     plan.StageSequence.PopulationMappingReturn,
		},
	}
	if err := renderer.loadScope(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := plan.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}
	sql, err := renderer.renderStageSequenceMappingStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

func (r *planRenderer) renderPopulationMappingTerminal(plan ir.PhysicalPlan, terminal ir.PhysicalPopulationMappingReturn) error {
	if r.mapping == nil || !r.mapping.witness {
		return fmt.Errorf("%w: operation %q is not part of the v1 PostgreSQL render surface",
			ErrUnsupportedPostgresPlan, string(ir.PhysicalPopulationMappingReturnOp))
	}
	if r.mapping.membersColumnRef == "" {
		return fmt.Errorf("%w: the member witness never rendered — the population root did not take the witness posture",
			ErrUnsupportedPostgresPlan)
	}
	alias := "w0"
	guard := "CASE WHEN " + r.mapping.membersColumnRef + " IS NULL OR jsonb_typeof(" +
		r.mapping.membersColumnRef + ") <> 'array' THEN '[]'::jsonb ELSE " + r.mapping.membersColumnRef + " END"
	lateral := "CROSS JOIN LATERAL jsonb_array_elements_text(" + guard + ") AS " + alias + "(member)"
	r.laterals = append(r.laterals, lateral)
	memberSQL := alias + ".member"
	identitySQL, identityColumn, err := r.renderMappingIdentityValue(plan, terminal)
	if err != nil {
		return err
	}
	r.selects = append(r.selects, memberSQL+` AS "c0"`, identitySQL+` AS "c1"`)
	r.outputColumns = []string{ir.PhysicalPopulationMappingMemberField, identityColumn}
	r.mapping.memberOrderTerm = memberSQL + ` COLLATE "C" ASC`
	return nil
}

func (r *planRenderer) assemblePopulationMapping() string {
	var builder strings.Builder
	builder.WriteString("SELECT ")
	builder.WriteString(strings.Join(r.selects, ", "))
	builder.WriteString(" FROM " + r.storage.Resource.Identifier().SQLText() + " AS " + r.rootAlias)
	for _, join := range r.joins {
		builder.WriteString(" " + join)
	}
	for _, lateral := range r.laterals {
		builder.WriteString(" " + lateral)
	}
	if len(r.where) > 0 {
		builder.WriteString(" WHERE " + strings.Join(r.where, " AND "))
	}
	order := append(r.windowOrder(), r.mapping.memberOrderTerm)
	builder.WriteString(" ORDER BY " + strings.Join(order, ", "))
	builder.WriteString(r.window.limitClause)
	return builder.String()
}

func (r *planRenderer) renderMappingIdentityValue(plan ir.PhysicalPlan, terminal ir.PhysicalPopulationMappingReturn) (string, string, error) {
	if terminal.ExplicitIdentity != nil {
		value, err := r.renderMappingIdentityExpression(plan, *terminal.ExplicitIdentity)
		if err != nil {
			return "", "", fmt.Errorf("mapping explicit identity: %w", err)
		}
		return value, ir.PhysicalPopulationMappingExplicitIdentityField, nil
	}
	if len(terminal.IdentityParts) == 0 {
		return "", "", fmt.Errorf("%w: mapping identity has no renderable value", ErrUnsupportedPostgresPlan)
	}
	parts := make([]string, 0, len(terminal.IdentityParts))
	for index, part := range terminal.IdentityParts {
		value, err := r.renderMappingIdentityExpression(plan, part.Expression)
		if err != nil {
			return "", "", fmt.Errorf("mapping identity part %d (%s): %w", index, part.Name, err)
		}
		parts = append(parts, value)
	}
	return "jsonb_build_array(" + strings.Join(parts, ", ") + ")", ir.PhysicalPopulationMappingIdentityPartsField, nil
}

func (r *planRenderer) renderMappingIdentityExpression(plan ir.PhysicalPlan, expression ir.PhysicalExpression) (string, error) {
	switch expression.Kind {
	case ir.PhysicalValueExpression:
		if expression.Value == nil {
			return "", fmt.Errorf("%w: mapping identity VALUE expression has no value payload", ErrUnsupportedPostgresPlan)
		}
		return r.renderMappingValue(plan, *expression.Value)
	case ir.PhysicalExtractExpression:
		extract := expression
		classified, err := classifyExtractionProjections([]ir.PhysicalProjection{
			{Name: "__loom_mapping_identity", Expression: &extract},
		}, func(variable string) (string, bool) {
			scope, ok := r.rows[variable]
			return scope.resourceType, ok
		})
		if err != nil {
			return "", err
		}
		if len(classified) != 1 || classified[0].kind != extractionSelector {
			return "", fmt.Errorf("%w: mapping identity EXTRACT expression is not a registered scalar selector reading",
				ErrUnsupportedPostgresPlan)
		}
		scope, ok := r.rows[classified[0].expression.Extract.Source.Variable]
		if !ok || scope.derivedValue != "" {
			return "", fmt.Errorf("%w: mapping identity selector reads a variable that binds no stored row",
				ErrUnsupportedPostgresPlan)
		}
		return r.scalarExtractValueExpression(scope.alias, classified[0].path, expression.NullBehavior)
	default:
		return "", fmt.Errorf("%w: mapping identity expression kind %q is a named capability gap and is refused, never partially rendered",
			ErrUnsupportedPostgresPlan, string(expression.Kind))
	}
}

func (r *planRenderer) renderMappingValue(plan ir.PhysicalPlan, value ir.PhysicalValue) (string, error) {
	if value.BindKey != "" {
		raw, ok := plan.BindVars[value.BindKey]
		if !ok {
			return "", fmt.Errorf("%w: mapping identity bind %q is not in the plan bind vars", ErrUnsupportedPostgresPlan, value.BindKey)
		}
		return r.mappingBindParameter(raw, value.BindKey)
	}
	return r.resolveValue(value)
}

func (r *planRenderer) mappingBindParameter(raw any, bindKey string) (string, error) {
	var parameter command.PostgreSQLParameter
	var cast string
	switch value := raw.(type) {
	case nil:
		parameter = command.PostgreSQLParameter{Type: command.PostgreSQLParameterNull, Value: nil}
		cast = "::text"
	case string:
		parameter = command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: value}
		cast = "::text"
	case int:
		parameter = command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(value)}
		cast = "::bigint"
	case int64:
		parameter = command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: value}
		cast = "::bigint"
	case float64:
		parameter = command.PostgreSQLParameter{Type: command.PostgreSQLParameterDoublePrecision, Value: value}
		cast = "::double precision"
	case bool:
		parameter = command.PostgreSQLParameter{Type: command.PostgreSQLParameterBoolean, Value: value}
		cast = "::boolean"
	default:
		return "", fmt.Errorf("%w: mapping identity bind %q carries %T; the typed band is string, int, int64, float64, bool, or nil",
			ErrUnsupportedPostgresPlan, bindKey, raw)
	}
	index := r.addParameter(parameter, "mapping-bind:"+bindKey)
	return "$" + strconv.Itoa(index) + cast, nil
}

func (r *planRenderer) renderStageSequenceMappingStatement(plan ir.PhysicalPlan) (string, error) {
	sequence := plan.StageSequence
	terminal := sequence.PopulationMappingReturn
	if terminal == nil {
		return "", fmt.Errorf("%w: the composition mapping terminal requires the PopulationMappingReturn payload",
			ErrUnsupportedPostgresPlan)
	}
	if terminal.SourceMemberIDsColumn != ir.PhysicalPopulationMappingMembersColumn ||
		terminal.SourceRootKeyColumn != "_key" || terminal.FinalRootContributorColumn == "" ||
		terminal.RowIdentityColumn == "" {
		return "", fmt.Errorf("%w: composition mapping terminal columns are not the compiler-owned mapping fields",
			ErrUnsupportedPostgresPlan)
	}
	chain, err := r.renderStageSequenceCTEChain(plan)
	if err != nil {
		return "", err
	}
	sourceKey, ok := chain.sourceFacts[terminal.SourceRootKeyColumn]
	if !ok || sourceKey.jsonb {
		return "", fmt.Errorf("%w: the composition source relation does not carry the materialized text %q root-key fact",
			ErrUnsupportedPostgresPlan, terminal.SourceRootKeyColumn)
	}
	sourceMembers, ok := chain.sourceFacts[terminal.SourceMemberIDsColumn]
	if !ok || !sourceMembers.jsonb {
		return "", fmt.Errorf("%w: the composition source relation does not carry the member witness fact",
			ErrUnsupportedPostgresPlan)
	}
	contributors, ok := chain.facts[terminal.FinalRootContributorColumn]
	if !ok {
		return "", fmt.Errorf("%w: final contributor column %q is not a column of the last stage",
			ErrUnsupportedPostgresPlan, terminal.FinalRootContributorColumn)
	}
	identity, ok := chain.facts[terminal.RowIdentityColumn]
	if !ok {
		return "", fmt.Errorf("%w: row identity column %q is not a column of the last stage",
			ErrUnsupportedPostgresPlan, terminal.RowIdentityColumn)
	}

	contribRef := stageTerminalAlias + `."` + contributors.alias + `"`
	var contributorGuard string
	if terminal.FinalRootContributorsMany {
		if !contributors.jsonb {
			return "", fmt.Errorf("%w: many-cardinality contributors column %q is a SQL scalar and carries no array",
				ErrUnsupportedPostgresPlan, terminal.FinalRootContributorColumn)
		}
		contributorGuard = "CASE WHEN " + contribRef + " IS NULL OR jsonb_typeof(" + contribRef +
			") <> 'array' THEN '[]'::jsonb ELSE " + contribRef + " END"
	} else {
		if contributors.jsonb {
			return "", fmt.Errorf("%w: scalar contributors column %q is jsonb and has no registered singleton reading",
				ErrUnsupportedPostgresPlan, terminal.FinalRootContributorColumn)
		}
		contributorGuard = "CASE WHEN " + contribRef + " IS NULL THEN '[]'::jsonb ELSE jsonb_build_array(" + contribRef + ") END"
	}
	memberRef := "ps." + `"` + sourceMembers.alias + `"`
	memberGuard := "CASE WHEN " + memberRef + " IS NULL OR jsonb_typeof(" + memberRef + ") <> 'array' THEN '[]'::jsonb ELSE " + memberRef + " END"
	identityRef := stageTerminalAlias + `."` + identity.alias + `"`
	identityOrder := identityRef + ` COLLATE "C" ASC`
	if identity.jsonb {
		identityOrder = identityRef + `::text COLLATE "C" ASC`
	}

	sql := "WITH " + strings.Join(chain.ctes, ", ") +
		` SELECT mn0.member AS "c0", jsonb_build_array(` + identityRef + `) AS "c1"` +
		" FROM " + chain.relation + " AS " + stageTerminalAlias +
		" CROSS JOIN LATERAL jsonb_array_elements_text(" + contributorGuard + `) AS cn0(contributor)` +
		` INNER JOIN ` + stageSourceRelation + ` AS ps ON ps."` + sourceKey.alias + `" = cn0.contributor` +
		" CROSS JOIN LATERAL jsonb_array_elements_text(" + memberGuard + `) AS mn0(member)` +
		" GROUP BY " + identityRef + ", mn0.member" +
		" ORDER BY " + identityOrder + `, mn0.member COLLATE "C" ASC`
	if sequence.PreviewLimitBindKey != "" {
		limit, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int)
		if !ok || limit <= 0 {
			return "", fmt.Errorf("%w: composition preview limit bind %q must be a positive int (render/aql/stage_sequence.go:452-453 twin)",
				ErrUnsupportedPostgresPlan, sequence.PreviewLimitBindKey)
		}
		index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limit)}, "")
		sql += " LIMIT $" + strconv.Itoa(index)
	}
	identityColumn := ir.PhysicalPopulationMappingIdentityPartsField
	r.outputColumns = []string{ir.PhysicalPopulationMappingMemberField, identityColumn}
	return sql, nil
}
