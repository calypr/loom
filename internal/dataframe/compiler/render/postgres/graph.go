package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const graphOutputColumn = "paths"

func RenderGraphPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	return renderGraphPhysicalPlan(plan, storage)
}

func graphPlanMentionsPathOperations(plan ir.PhysicalPlan) bool {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalPathSeedOp || operation.Kind == ir.PhysicalPathExtendOp || operation.Kind == ir.PhysicalGraphReturnOp {
			return true
		}
	}
	return false
}

func renderGraphPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateGraphPlanSurface(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := plan.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}
	if plan.Engine != "" && plan.Engine != ir.PhysicalEngineAQL {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: engine %q has no PostgreSQL source; the source graph IR is backend-neutral and ClickHouse plans are not PostgreSQL-rendered",
			ErrUnsupportedPostgresPlan, string(plan.Engine))
	}
	if plan.ClickHouseCombine != nil || plan.ClickHousePrefix != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: ClickHouse combine/prefix payloads have no PostgreSQL rendering", ErrUnsupportedPostgresPlan)
	}
	if plan.StageSequence != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: staged row-operator sequences are not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: preview source windowing is not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if len(plan.DeferredExpressionLets) != 0 {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: completed plans must not carry deferred expression lets", ErrUnsupportedPostgresPlan)
	}
	renderer := &planRenderer{
		storage:           storage,
		rows:              map[string]rowScope{},
		parameters:        map[string]int{},
		canonicalAuthLets: map[string]string{},
	}
	if err := renderer.loadScope(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	sql, outputColumns, err := renderer.renderGraphStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, outputColumns)
}

func validateGraphPlanSurface(plan ir.PhysicalPlan) error {
	if len(plan.Operations) < 3 {
		return fmt.Errorf("%w: graph plans require at least ROOT_SCAN, PATH_SEED, and a final GRAPH_RETURN", ErrUnsupportedPostgresPlan)
	}
	first := plan.Operations[0]
	if first.Kind != ir.PhysicalRootScanOp || first.RootScan == nil {
		return fmt.Errorf("%w: graph plans require one leading ROOT_SCAN payload (lower/graph_plan.go:51-55)", ErrUnsupportedPostgresPlan)
	}
	last := plan.Operations[len(plan.Operations)-1]
	if last.Kind != ir.PhysicalGraphReturnOp || last.GraphReturn == nil {
		return fmt.Errorf("%w: graph plans require GRAPH_RETURN as the final operation (lower/graph_plan.go:139)", ErrUnsupportedPostgresPlan)
	}
	seedIndex := -1
	definedSets := map[string]bool{}
	var lastRouteOrder int
	var haveRouteOrder bool
	for index := 1; index < len(plan.Operations)-1; index++ {
		operation := plan.Operations[index]
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			if seedIndex != -1 {
				return fmt.Errorf("%w: graph plan operation %d: a FILTER after PATH_SEED must live inside a PATH_EXTEND Scope (the graph plan is flat; lower/graph_plan.go:113-119)",
					ErrUnsupportedPostgresPlan, index)
			}
			if operation.Filter == nil {
				return fmt.Errorf("%w: graph plan operation %d: filter payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalDerivedLetOp:
			if seedIndex != -1 {
				return fmt.Errorf("%w: graph plan operation %d: a DERIVED_LET after PATH_SEED must live inside a PATH_EXTEND Scope (the graph plan is flat)",
					ErrUnsupportedPostgresPlan, index)
			}
			if operation.DerivedLet == nil {
				return fmt.Errorf("%w: graph plan operation %d: derived let payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalPathSeedOp:
			if seedIndex != -1 {
				return fmt.Errorf("%w: graph plan operation %d: a second PATH_SEED is outside the closed graph slice", ErrUnsupportedPostgresPlan, index)
			}
			seed := operation.PathSeed
			if seed == nil {
				return fmt.Errorf("%w: graph plan operation %d: path seed payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if seed.RouteOrder != 0 {
				return fmt.Errorf("%w: graph plan operation %d: seed route order %d is not 0 (lower/graph_plan.go:70 always emits 0; the PathSets ascending check treats the seed set as its zero anchor, so a divergence must fail closed rather than let the check and the bound value drift)",
					ErrUnsupportedPostgresPlan, index, seed.RouteOrder)
			}
			if seed.Variable == "" {
				return fmt.Errorf("%w: graph plan operation %d: path seed must name its set variable", ErrUnsupportedPostgresPlan, index)
			}
			if value := seed.Node.Value; value.Variable != first.RootScan.Variable || len(value.Path) != 0 || value.BindKey != "" {
				return fmt.Errorf("%w: graph plan operation %d: the seed node must read the root row itself (lower/graph_plan.go:70-72; the AQL seed renders the one root value, render/aql/graph_render.go:80-88)",
					ErrUnsupportedPostgresPlan, index)
			}
			seedIndex = index
			definedSets[seed.Variable] = true
		case ir.PhysicalPathExtendOp:
			if seedIndex == -1 {
				return fmt.Errorf("%w: graph plan operation %d: PATH_EXTEND before PATH_SEED is outside the closed graph slice", ErrUnsupportedPostgresPlan, index)
			}
			extend := operation.PathExtend
			if extend == nil {
				return fmt.Errorf("%w: graph plan operation %d: path extend payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if extend.Variable == "" {
				return fmt.Errorf("%w: graph plan operation %d: path extend must name its set variable", ErrUnsupportedPostgresPlan, index)
			}
			if !definedSets[extend.SourceVariable] {
				return fmt.Errorf("%w: graph plan operation %d: source path set %q is not a set defined by this plan",
					ErrUnsupportedPostgresPlan, index, extend.SourceVariable)
			}
			if extend.Traversal.Direction != ir.PhysicalInbound &&
				extend.Traversal.Direction != ir.PhysicalOutbound {
				return fmt.Errorf("%w: graph plan operation %d: %s-direction traversal has no proven PostgreSQL contract (the spine registers INBOUND/OUTBOUND only, lower/traversal.go; ANY is a registered capability gap)",
					ErrUnsupportedPostgresPlan, index, string(extend.Traversal.Direction))
			}
			if extend.Relationship.LabelBindKey != extend.Traversal.EdgeLabelBindKey {
				return fmt.Errorf("%w: graph plan operation %d: relationship label bind %q does not equal the traversal edge label bind %q (lower/graph_plan.go:126 always sets one key for both; the AQL twin filters edges on EdgeLabelBindKey, render/aql/graph_render.go:119/:128, while rendering the public label and dedupe identity from Relationship.LabelBindKey, :144-145 — the renderer reads one bind, so a divergence must fail closed, mirroring the node-type/target-type pin)",
					ErrUnsupportedPostgresPlan, index, extend.Relationship.LabelBindKey, extend.Traversal.EdgeLabelBindKey)
			}
			if len(extend.SourcePath) != 0 {
				return fmt.Errorf("%w: graph plan operation %d: a non-empty SourcePath has no registered PostgreSQL spelling (the planner always emits nil, lower/graph_plan.go:124; the AQL nested-terminal read render/aql/graph_render.go:101-107 stays a typed gap)",
					ErrUnsupportedPostgresPlan, index)
			}
			if extend.Traversal.Strategy == ir.PhysicalTraversalEndpointLookup &&
				(strings.TrimSpace(extend.Traversal.EndpointField) == "" || strings.TrimSpace(extend.Traversal.EndpointJoinField) == "") {
				return fmt.Errorf("%w: graph plan operation %d: endpoint-lookup traversal payload is incomplete (the planner always names EndpointField and EndpointJoinField, lower/traversal.go:69-70)",
					ErrUnsupportedPostgresPlan, index)
			}
			if value := extend.Node.Value; value.Variable != extend.Traversal.TargetVariable || len(value.Path) != 0 || value.BindKey != "" {
				return fmt.Errorf("%w: graph plan operation %d: the extend node must read the reached target row itself (lower/graph_plan.go:125)",
					ErrUnsupportedPostgresPlan, index)
			}
			blockEnd := 0
			if err := matchCanonicalScopeBlock(extend.Scope, &blockEnd, []string{extend.Traversal.EdgeVariable, extend.Traversal.TargetVariable}); err != nil {
				return fmt.Errorf("%w: graph plan operation %d extend scope %d: canonical hop scope block: %s",
					ErrUnsupportedPostgresPlan, index, blockEnd, err.Error())
			}
			for scopeIndex := blockEnd; scopeIndex < len(extend.Scope); scopeIndex++ {
				scopeOperation := extend.Scope[scopeIndex]
				switch scopeOperation.Kind {
				case ir.PhysicalFilterOp:
					if scopeOperation.Filter == nil {
						return fmt.Errorf("%w: graph plan operation %d extend scope %d: filter payload is missing", ErrUnsupportedPostgresPlan, index, scopeIndex)
					}
					if err := validateGraphExtendFilter(index, scopeIndex, extend, scopeOperation.Filter); err != nil {
						return err
					}
				case ir.PhysicalDerivedLetOp:
					if scopeOperation.DerivedLet == nil || scopeOperation.DerivedLet.Operator != canonicalAuthLetOperator {
						return fmt.Errorf("%w: graph plan operation %d extend scope %d: only the canonical AUTH_RESOURCE_PATH_ALLOWED LET is registered",
							ErrUnsupportedPostgresPlan, index, scopeIndex)
					}
				default:
					return fmt.Errorf("%w: graph plan operation %d extend scope %d: operation %q is outside the closed graph slice",
						ErrUnsupportedPostgresPlan, index, scopeIndex, string(scopeOperation.Kind))
				}
			}
			definedSets[extend.Variable] = true
			if haveRouteOrder && extend.RouteOrder <= lastRouteOrder {
				return fmt.Errorf("%w: graph plan operation %d: route order %d does not ascend after %d (walk/graph_plan.go:85-90 always emits DFS-ascending orders; the PathSets flatten order depends on it)",
					ErrUnsupportedPostgresPlan, index, extend.RouteOrder, lastRouteOrder)
			}
			lastRouteOrder, haveRouteOrder = extend.RouteOrder, true
		default:
			return fmt.Errorf("%w: graph plan operation %d (%q) is not part of the closed graph slice",
				ErrUnsupportedPostgresPlan, index, string(operation.Kind))
		}
	}
	if seedIndex == -1 {
		return fmt.Errorf("%w: graph plans require exactly one PATH_SEED (lower/graph_plan.go:67-74)", ErrUnsupportedPostgresPlan)
	}
	seenSets := map[string]bool{}
	previousRouteOrder := 0
	for setIndex, setName := range last.GraphReturn.PathSets {
		if !definedSets[setName] {
			return fmt.Errorf("%w: graph return path set %q is not a set defined by this plan", ErrUnsupportedPostgresPlan, setName)
		}
		if seenSets[setName] {
			return fmt.Errorf("%w: graph return path set %q is listed twice", ErrUnsupportedPostgresPlan, setName)
		}
		seenSets[setName] = true
		routeOrder := 0
		if setName != plan.Operations[seedIndex].PathSeed.Variable {
			routeOrder = -1
			for index := seedIndex + 1; index < len(plan.Operations)-1; index++ {
				if extend := plan.Operations[index].PathExtend; extend != nil && extend.Variable == setName {
					routeOrder = extend.RouteOrder
					break
				}
			}
			if routeOrder < 0 {
				return fmt.Errorf("%w: graph return path set %q is not route-ordered", ErrUnsupportedPostgresPlan, setName)
			}
		}
		if setIndex > 0 && routeOrder <= previousRouteOrder {
			return fmt.Errorf("%w: graph return path sets are not route-order ascending at %q (%d after %d); the planner's flatten order is part of the dedupe contract",
				ErrUnsupportedPostgresPlan, setName, routeOrder, previousRouteOrder)
		}
		previousRouteOrder = routeOrder
	}
	return nil
}

func validateGraphExtendFilter(index, scopeIndex int, extend *ir.PhysicalPathExtend, filter *ir.PhysicalFilter) error {
	refuse := func(reason string) error {
		return fmt.Errorf("%w: graph plan operation %d extend scope %d: %s", ErrUnsupportedPostgresPlan, index, scopeIndex, reason)
	}
	if filter.Expression == nil || filter.Expression.Kind != ir.PhysicalComparisonPredicate || filter.Expression.Comparison == nil {
		return refuse("extend-scope FILTERs must be the planner's comparison-expression form (lower/graph_plan.go:277/:313)")
	}
	comparison := filter.Expression.Comparison
	if comparison.Correlation != nil {
		return refuse("correlated CODE path filters are a registered capability gap (lower/graph_plan.go:259-278; correlated predicates have no registered PostgreSQL contract)")
	}
	if comparison.LeftExpression == nil || comparison.LeftExpression.Extract == nil {
		return refuse("the compared value must be the planner's selector extraction (lower/graph_plan.go:284-286)")
	}
	extract := comparison.LeftExpression.Extract
	if extract.Source.Variable != extend.Traversal.TargetVariable || len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return refuse("extend-scope FILTERs may only read the reached target's payload (lower/graph_plan.go:285-286; the edge row is never readable)")
	}
	return nil
}

type graphPathSet struct {
	cteAlias     string
	terminalType string
}

func (r *planRenderer) renderGraphStatement(plan ir.PhysicalPlan) (string, []string, error) {
	rootScan := plan.Operations[0].RootScan
	if err := r.renderRootScan(plan, *rootScan); err != nil {
		return "", nil, err
	}
	seedConjuncts := r.where
	r.where = nil

	seedIndex := 1
	for index := 1; index < len(plan.Operations)-1; index++ {
		operation := plan.Operations[index]
		if operation.Kind == ir.PhysicalPathSeedOp {
			seedIndex = index
			break
		}
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			canonical, err := r.acceptCanonicalScopeFilter(operation.Filter)
			if err != nil {
				return "", nil, err
			}
			if canonical {
				continue
			}
			renderPlan := graph20NormalizeRequiredMatchSubplan(plan, index)
			clause, err := r.renderFilter(renderPlan, *renderPlan.Operations[index].Filter)
			if err != nil {
				return "", nil, fmt.Errorf("graph root filter %d: %w", index, err)
			}
			seedConjuncts = append(seedConjuncts, clause)
		case ir.PhysicalDerivedLetOp:
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", nil, err
			}
			if canonical {
				continue
			}
			return "", nil, fmt.Errorf("%w: graph root operation %d is not a canonical scope operation", ErrUnsupportedPostgresPlan, index)
		}
	}

	resourceTable := r.storage.Resource.Identifier().SQLText()
	edgeTable := r.storage.Edge.Identifier().SQLText()
	resourceTypeColumn := r.storage.Resource.MustColumn("resource_type").SQLText()
	logicalIDColumn := r.storage.Resource.MustColumn("logical_id").SQLText()
	payloadColumn := r.storage.Resource.MustColumn("payload").SQLText()
	edgeSeqColumn := r.storage.Edge.MustColumn("edge_seq").SQLText()

	rootRow := r.rows[rootScan.Variable]
	seed := plan.Operations[seedIndex].PathSeed

	seedNode := "jsonb_build_object('alias', $" + strconv.Itoa(r.textParameter(seed.Node.Alias, "text:"+seed.Node.Alias)) + "::text" +
		", 'resourceType', $" + strconv.Itoa(r.textParameter(seed.Node.ResourceType, "text:"+seed.Node.ResourceType)) + "::text" +
		", 'id', " + rootRow.alias + "." + logicalIDColumn +
		", 'key', " + rootRow.alias + "." + logicalIDColumn +
		", 'payload', " + rootRow.alias + "." + payloadColumn + ")"
	seedSelect := "SELECT jsonb_build_array(" + rootRow.alias + "." + resourceTypeColumn + ", " + rootRow.alias + "." + logicalIDColumn + ") AS \"path_identity\"" +
		", $" + strconv.Itoa(r.graphBigintParameter(seed.RouteOrder)) + "::bigint AS \"route_order\"" +
		", $" + strconv.Itoa(r.textParameter(seed.Node.Alias, "text:"+seed.Node.Alias)) + "::text AS \"terminal_alias\"" +
		", jsonb_build_array(" + seedNode + ") AS \"nodes\"" +
		", '[]'::jsonb AS \"relationships\", '[]'::jsonb AS \"occurrence_trail\"" +
		", " + rootRow.alias + "." + resourceTypeColumn + " AS \"resource_type\"" +
		", " + rootRow.alias + "." + logicalIDColumn + " AS \"logical_id\"" +
		" FROM " + resourceTable + " AS " + rootRow.alias
	if len(seedConjuncts) > 0 {
		seedSelect += " WHERE " + strings.Join(seedConjuncts, " AND ")
	}

	sets := map[string]graphPathSet{seed.Variable: {cteAlias: "path_0", terminalType: seed.Node.ResourceType}}
	ctes := []string{"path_0 AS (" + seedSelect + ")"}
	terminalTypeMismatch := func(index int, setName, want, got string) error {
		return fmt.Errorf("%w: graph plan operation %d: set %q terminal type %q does not continue the authored chain from %q",
			ErrUnsupportedPostgresPlan, index, setName, got, want)
	}

	for index := seedIndex + 1; index < len(plan.Operations)-1; index++ {
		extend := plan.Operations[index].PathExtend
		parent, ok := sets[extend.SourceVariable]
		if !ok {
			return "", nil, fmt.Errorf("%w: graph plan operation %d: source path set %q is not a set defined by this plan",
				ErrUnsupportedPostgresPlan, index, extend.SourceVariable)
		}
		if parent.terminalType != extend.Relationship.FromResourceType {
			return "", nil, terminalTypeMismatch(index, extend.SourceVariable, parent.terminalType, extend.Relationship.FromResourceType)
		}
		const parentAlias = "p"
		verified, err := r.validateTraversalContract(plan, graph20NativeStrategyTraversal(extend.Traversal), rowScope{alias: parentAlias, resourceType: extend.Relationship.FromResourceType})
		if err != nil {
			return "", nil, fmt.Errorf("graph plan operation %d traversal: %w", index, err)
		}
		if extend.Node.ResourceType != verified.targetType {
			return "", nil, fmt.Errorf("%w: graph plan operation %d: node resource type %q does not match the traversal target type bind %q (lower/graph_plan.go:125-126 always agrees; a divergence must fail closed)",
				ErrUnsupportedPostgresPlan, index, extend.Node.ResourceType, verified.targetType)
		}
		seq := r.nextTraversalSequence()
		edgeAlias := "e" + strconv.Itoa(seq)
		targetAlias := "x" + strconv.Itoa(seq)
		edgeWhere, targetOn := r.edgeOccurrencePredicates(&verified, edgeAlias, targetAlias, parentAlias)

		localRows := map[string]rowScope{extend.Traversal.TargetVariable: {alias: targetAlias, resourceType: verified.targetType}}
		var extendConjuncts []string
		scopeIndex := 0
		if err := matchCanonicalScopeBlock(extend.Scope, &scopeIndex, []string{extend.Traversal.EdgeVariable, extend.Traversal.TargetVariable}); err != nil {
			return "", nil, fmt.Errorf("%w: graph plan operation %d extend scope %d: canonical hop scope block: %s",
				ErrUnsupportedPostgresPlan, index, scopeIndex, err.Error())
		}
		for ; scopeIndex < len(extend.Scope); scopeIndex++ {
			operation := extend.Scope[scopeIndex]
			if operation.Kind != ir.PhysicalFilterOp || operation.Filter == nil {
				return "", nil, fmt.Errorf("%w: graph plan operation %d extend scope %d: only FILTER operations follow the canonical scope block",
					ErrUnsupportedPostgresPlan, index, scopeIndex)
			}
			fragment, err := r.renderPredicateFragmentScoped(plan, *operation.Filter.Expression, localRows, true)
			if err != nil {
				return "", nil, fmt.Errorf("%w: graph plan operation %d extend scope %d: %w", ErrUnsupportedPostgresPlan, index, scopeIndex, err)
			}
			extendConjuncts = append(extendConjuncts, fragment.sql)
		}

		labelRef := "$" + strconv.Itoa(verified.labelIndex)
		nodeObject := "jsonb_build_object('alias', $" + strconv.Itoa(r.textParameter(extend.Node.Alias, "text:"+extend.Node.Alias)) + "::text" +
			", 'resourceType', $" + strconv.Itoa(r.textParameter(extend.Node.ResourceType, "text:"+extend.Node.ResourceType)) + "::text" +
			", 'id', " + targetAlias + "." + logicalIDColumn +
			", 'key', " + targetAlias + "." + logicalIDColumn +
			", 'payload', " + targetAlias + "." + payloadColumn + ")"
		relationshipObject := "jsonb_build_object('alias', $" + strconv.Itoa(r.textParameter(extend.Relationship.Alias, "text:"+extend.Relationship.Alias)) + "::text" +
			", 'label', " + labelRef + "::text" +
			", 'fromResourceType', $" + strconv.Itoa(r.textParameter(extend.Relationship.FromResourceType, "text:"+extend.Relationship.FromResourceType)) + "::text" +
			", 'toResourceType', $" + strconv.Itoa(r.textParameter(extend.Relationship.ToResourceType, "text:"+extend.Relationship.ToResourceType)) + "::text" + ")"

		cteAlias := "path_" + strconv.Itoa(len(ctes))
		selectText := "SELECT p.\"path_identity\" || jsonb_build_array(" + targetAlias + "." + resourceTypeColumn + ", " + targetAlias + "." + logicalIDColumn + ", " + labelRef + "::text) AS \"path_identity\"" +
			", $" + strconv.Itoa(r.graphBigintParameter(extend.RouteOrder)) + "::bigint AS \"route_order\"" +
			", $" + strconv.Itoa(r.textParameter(extend.Node.Alias, "text:"+extend.Node.Alias)) + "::text AS \"terminal_alias\"" +
			", p.\"nodes\" || jsonb_build_array(" + nodeObject + ") AS \"nodes\"" +
			", p.\"relationships\" || jsonb_build_array(" + relationshipObject + ") AS \"relationships\"" +
			", p.\"occurrence_trail\" || jsonb_build_array(" + edgeAlias + "." + edgeSeqColumn + ") AS \"occurrence_trail\"" +
			", " + targetAlias + "." + resourceTypeColumn + " AS \"resource_type\"" +
			", " + targetAlias + "." + logicalIDColumn + " AS \"logical_id\"" +
			" FROM " + parent.cteAlias + " AS p" +
			" INNER JOIN " + edgeTable + " AS " + edgeAlias + " ON " + edgeWhere +
			" INNER JOIN " + resourceTable + " AS " + targetAlias + " ON " + targetOn
		if len(extendConjuncts) > 0 {
			selectText += " WHERE " + strings.Join(extendConjuncts, " AND ")
		}
		ctes = append(ctes, cteAlias+" AS ("+selectText+")")
		sets[extend.Variable] = graphPathSet{cteAlias: cteAlias, terminalType: extend.Node.ResourceType}
	}

	graphReturn := plan.Operations[len(plan.Operations)-1].GraphReturn
	arms := make([]string, 0, len(graphReturn.PathSets))
	for _, setName := range graphReturn.PathSets {
		pathSet, ok := sets[setName]
		if !ok {
			return "", nil, fmt.Errorf("%w: graph return path set %q is not a set defined by this plan", ErrUnsupportedPostgresPlan, setName)
		}
		arms = append(arms, "SELECT \"path_identity\", \"route_order\", \"terminal_alias\", \"nodes\", \"relationships\", \"occurrence_trail\" FROM "+pathSet.cteAlias)
	}

	limit, ok := plan.BindVars[graphReturn.LimitBindKey].(int)
	if !ok || limit <= 0 {
		return "", nil, fmt.Errorf("%w: graph limit bind %q must be a positive int (lower/graph_plan.go:21-23/:49)",
			ErrUnsupportedPostgresPlan, graphReturn.LimitBindKey)
	}
	limitIndex := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limit)}, "")

	sql := "WITH " + strings.Join(ctes, ", ") +
		", graph_paths AS (SELECT *, ROW_NUMBER() OVER (PARTITION BY path_identity ORDER BY route_order ASC, occurrence_trail ASC) AS rn" +
		" FROM (" + strings.Join(arms, " UNION ALL ") + ") AS g_union)" +
		" SELECT jsonb_build_object('terminalAlias', terminal_alias, 'nodes', nodes, 'relationships', relationships) AS \"c0\"" +
		" FROM graph_paths AS g_row WHERE g_row.rn = 1" +
		" ORDER BY g_row.route_order ASC, g_row.path_identity::text COLLATE \"C\" ASC" +
		" LIMIT $" + strconv.Itoa(limitIndex)
	return sql, []string{graphOutputColumn}, nil
}

func graph20NormalizeRequiredMatchSubplan(plan ir.PhysicalPlan, index int) ir.PhysicalPlan {
	operation := plan.Operations[index]
	if operation.Filter == nil || operation.Filter.Expression == nil ||
		operation.Filter.Expression.Kind != ir.PhysicalExistsPredicate || operation.Filter.Expression.Exists == nil ||
		validateRequiredMatchReturn(plan, *operation.Filter.Expression.Exists) != nil {
		return plan
	}
	operations := operation.Filter.Expression.Exists.Operations
	kept := make([]ir.PhysicalOperation, 0, len(operations))
	consumed := false
	var blockVariables []string
	for step := 0; step < len(operations); step++ {
		candidate := operations[step]
		if candidate.Kind == ir.PhysicalTraversalOp && candidate.Traversal != nil {
			blockVariables = []string{candidate.Traversal.EdgeVariable, candidate.Traversal.TargetVariable}
			kept = append(kept, candidate)
			continue
		}
		if blockVariables != nil {
			cursor := step
			if err := matchCanonicalScopeBlock(operations, &cursor, blockVariables); err == nil && cursor > step {
				step = cursor - 1
				blockVariables = nil
				consumed = true
				continue
			}
		}
		kept = append(kept, candidate)
	}
	if !consumed {
		return plan
	}
	normalized := plan
	normalizedOperations := append([]ir.PhysicalOperation(nil), plan.Operations...)
	operationCopy := normalizedOperations[index]
	filterCopy := *operationCopy.Filter
	expressionCopy := *filterCopy.Expression
	existsCopy := *expressionCopy.Exists
	existsCopy.Operations = kept
	expressionCopy.Exists = &existsCopy
	filterCopy.Expression = &expressionCopy
	operationCopy.Filter = &filterCopy
	normalizedOperations[index] = operationCopy
	normalized.Operations = normalizedOperations
	return normalized
}

func (r *planRenderer) graphBigintParameter(value int) int {
	return r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(value)}, "")
}

func graph20NativeStrategyTraversal(traversal ir.PhysicalTraversal) ir.PhysicalTraversal {
	if traversal.Strategy == ir.PhysicalTraversalEndpointLookup {
		traversal.Strategy = ir.PhysicalTraversalNative
	}
	return traversal
}
