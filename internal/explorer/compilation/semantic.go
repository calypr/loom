package compilation

import (
	"context"
	"fmt"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"

	"slices"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type semanticOccurrence struct {
	node  ws.RouteNode
	graph capability.Node
	edge  *capability.Edge
}

type semanticRecipeNode struct {
	fields       []recipe.Field
	dynamics     []recipe.DynamicColumn
	pivots       []recipe.Pivot
	ownerRecords []recipe.OwnerRecordProjection
	aggregates   []recipe.Aggregate
}

func compileSemanticDocument(ctx context.Context, project, explorerID string, document ws.Document, snapshot capability.Snapshot, workspaceOutputIDs []string) (Result, error) {
	if err := contextErr(ctx); err != nil {
		return Result{}, err
	}
	occurrences, err := resolveSemanticRoute(document, snapshot)
	if err != nil {
		return Result{}, err
	}
	root := occurrences[ws.RootOccurrenceID]
	rowGrain, ok := spec.InferRowGrain(root.graph.ResourceType)
	if !ok || !root.graph.RowRootEligible {
		return Result{}, fail("lower", "UNSUPPORTED_ROW_ROOT", "$.rootResourceType", "root resource type is not an eligible recipe row root", map[string]any{"resourceType": root.graph.ResourceType}, nil)
	}
	var expansion *recipe.Expansion
	var groupRows *recipe.GroupRows
	if document.Rows.Kind == ws.RowDefinitionExpanded {
		compiled, err := compileExpandedRows(document.Rows.Expanded, document.Route, occurrences, snapshot)
		if err != nil {
			return Result{}, err
		}
		expansion = &compiled
		rowGrain = spec.RowGrainExpanded
	} else if document.Rows.Kind == ws.RowDefinitionGroups {
		if document.Rows.Groups == nil || document.Rows.Groups.Source.Kind != ws.GroupSourceExplicit || document.Rows.Groups.Source.Explicit == nil {
			return Result{}, fail("lower", "UNSUPPORTED_GROUP_SOURCE", "$.rows.groups.source", "only pinned explicit group revisions can produce rows", nil, nil)
		}
		explicit := document.Rows.Groups.Source.Explicit
		groupRows = &recipe.GroupRows{
			RevisionID: explicit.RevisionID, UnassignedMemberPolicy: string(explicit.UnassignedMemberPolicy),
			AfterStepID: document.Rows.Groups.AfterStepID,
			RowValues:   make([]recipe.GroupRowValuePolicy, 0, len(document.Rows.Groups.RowValues)),
		}
		for _, value := range document.Rows.Groups.RowValues {
			groupRows.RowValues = append(groupRows.RowValues, recipe.GroupRowValuePolicy{
				ColumnID: value.ColumnID, Policy: recipe.ConstructionRowValuePolicy(value.Policy),
			})
		}
		rowGrain = spec.RowGrainGroups
	}

	nodes := make(map[string]*semanticRecipeNode, len(occurrences))
	for id := range occurrences {
		nodes[id] = &semanticRecipeNode{}
	}
	columnTransformations := make([]recipe.ColumnTransformation, 0, len(document.Columns))
	emitted := make([]viewer.EmittedColumn, 0, len(document.Columns))
	mappings := make([]receiptmodel.IdentityMapping, 0, len(document.Columns))
	presentation := PresentationConfig{OutputID: document.Output.ID, Title: document.Output.Title, Columns: make([]PresentationColumn, 0, len(document.Columns))}
	rowMultiplication := "none"
	if expansion != nil {
		rowMultiplication = "expand"
	}
	structuralSuitability := "scalar"
	if len(document.Columns) == 0 {
		structuralSuitability = ""
	}
	contract := receiptmodel.PublicOutputContract{OutputID: document.Output.ID, RootResourceType: root.graph.ResourceType, RowGrain: string(rowGrain), RowMultiplication: rowMultiplication, Lossless: true, MLReady: true, StructuralSuitability: structuralSuitability, Columns: make([]receiptmodel.PublicOutputColumn, 0, len(document.Columns))}
	columns := semanticColumnCompiler{
		project: project, explorerID: explorerID, document: document, snapshot: snapshot,
		occurrences: occurrences, nodes: nodes, expansion: expansion,
		columnTransformations: columnTransformations, emitted: emitted, mappings: mappings,
		presentation: presentation, contract: contract, countEmissions: make(map[string]int),
	}
	for index, column := range document.Columns {
		if err := columns.compile(index, column); err != nil {
			return Result{}, err
		}
	}
	columnTransformations, emitted, mappings = columns.columnTransformations, columns.emitted, columns.mappings
	presentation, contract = columns.presentation, columns.contract

	if document.Construction != nil {
		for index, projection := range document.Construction.SourceProjections {
			path := strings.TrimPrefix(strings.TrimSpace(projection.FieldPath), "root.")
			expanded := constructionExpandedFieldOwner(document.Construction, projection, path)
			candidate, found := semanticFieldCandidate(snapshot, root.graph.ID, path)
			if projection.OccurrenceID != ws.RootOccurrenceID || !found ||
				strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.") != path || candidate.LogicalType != projection.LogicalType {
				return Result{}, fail("capability", "STALE_CONSTRUCTION_SOURCE_FIELD", fmt.Sprintf("$.construction.sourceProjections[%d]", index), "source projection no longer matches an authorized field on the root resource", map[string]any{"fieldPath": path}, nil)
			}
			if expanded != nil {

				if !capability.IsRepeatedCardinality(candidate.Cardinality) ||
					!slices.Contains(candidate.ProjectionModes, capability.ProjectionArray) ||
					!semanticSelfRepeated(candidate, path) {
					return Result{}, fail("capability", "STALE_CONSTRUCTION_SOURCE_FIELD", fmt.Sprintf("$.construction.sourceProjections[%d]", index), "signed expanded field no longer matches a direct-root repeated array candidate on the root resource", map[string]any{"fieldPath": path}, nil)
				}
			} else if len(candidate.RepeatedBoundaries) != 0 ||
				!slices.Contains(candidate.ProjectionModes, capability.ProjectionScalar) {
				return Result{}, fail("capability", "STALE_CONSTRUCTION_SOURCE_FIELD", fmt.Sprintf("$.construction.sourceProjections[%d]", index), "source projection no longer matches an authorized scalar VALUE field or authorized direct root array on the root resource", map[string]any{"fieldPath": path}, nil)
			}
			schemaIndex, err := fhirschema.GeneratedIndex()
			if err != nil {
				return Result{}, fail("capability", "FHIR_SCHEMA_UNAVAILABLE", "$.construction.sourceProjections", "generated FHIR field metadata is unavailable", nil, err)
			}
			facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(root.graph.ResourceType), path)
			if err != nil || facts.CanonicalPath != path || facts.FHIRType != projection.FHIRType || facts.Reference {
				return Result{}, fail("capability", "STALE_CONSTRUCTION_SOURCE_SCHEMA", fmt.Sprintf("$.construction.sourceProjections[%d]", index), "source projection no longer matches generated FHIR metadata", map[string]any{"fieldPath": path}, err)
			}
			repeated := capability.IsRepeatedCardinality(candidate.Cardinality)
			if expanded != nil {

				if !repeated || facts.Shape != fhirschema.RowPathArray || facts.Cardinality != fhirschema.RowCardinalityMany ||
					expanded.Path != path || expanded.Cardinality != ws.ConstructionExpandedFieldCardinalityMany ||
					expanded.LogicalType != projection.LogicalType || expanded.FHIRType != projection.FHIRType {
					return Result{}, fail("capability", "STALE_EXPANDED_SOURCE_SCHEMA", fmt.Sprintf("$.construction.sourceProjections[%d]", index), "signed expanded field no longer names a direct-root many-cardinality non-reference array", map[string]any{"fieldPath": path}, err)
				}
				if _, ok := facade.TableReshapeScalarKind(candidate.LogicalType); !ok {
					return Result{}, fail("capability", "UNSUPPORTED_EXPANDED_SOURCE_KIND", fmt.Sprintf("$.construction.sourceProjections[%d]", index), "expanded array items are not a compiler-accepted scalar kind", map[string]any{"fieldPath": path, "logicalType": candidate.LogicalType}, nil)
				}
			} else if repeated || facts.Shape != fhirschema.RowPathScalar || facts.Cardinality != fhirschema.RowCardinalityOne {
				return Result{}, fail("capability", "STALE_CONSTRUCTION_SOURCE_SCHEMA", fmt.Sprintf("$.construction.sourceProjections[%d]", index), "source projection no longer matches generated FHIR scalar metadata", map[string]any{"fieldPath": path}, err)
			}
			laneField := recipe.Field{
				Name: ws.ConstructionSourceProjectionName(projection.ColumnID), ColumnID: projection.ColumnID,
				Label: projection.Label, FieldRef: projection.FieldPath,
				Expr: recipe.Expression{Select: semanticAlias(ws.RootOccurrenceID) + "." + path},
			}
			if expanded != nil {

				laneField.ValueMode = recipe.ValueModeAll
			}
			nodes[ws.RootOccurrenceID].fields = append(nodes[ws.RootOccurrenceID].fields, laneField)
		}
	}

	construction, err := recipeConstruction(document.Construction, document.Columns, emitted)
	if err != nil {
		return Result{}, fail("capability", "UNSUPPORTED_CONSTRUCTION_SOURCE_SHAPE", "$.construction.sourceColumns", err.Error(), nil, err)
	}
	output := recipe.Output{Name: document.Output.ID, RootResourceType: root.graph.ResourceType, RootOccurrenceID: ws.RootOccurrenceID, RowGrain: string(rowGrain), RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact, Fields: nodes[ws.RootOccurrenceID].fields, Pivots: nodes[ws.RootOccurrenceID].pivots, OwnerRecords: nodes[ws.RootOccurrenceID].ownerRecords, Aggregates: nodes[ws.RootOccurrenceID].aggregates, DynamicColumns: nodes[ws.RootOccurrenceID].dynamics, ColumnTransformations: columnTransformations, Construction: construction, Expand: expansion, GroupRows: groupRows, CollisionPolicy: "error"}
	if expansion != nil {
		output.Identity = &recipe.Identity{Name: "__loom_row_id", Expansion: &recipe.ExpansionIdentity{}}
	}
	output.Traversals = semanticTraversals(document.Route, occurrences, nodes)
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "explorer_" + safeName(project) + "_" + safeName(explorerID), TranslationVersion: TranslationVersion, Outputs: []recipe.Output{output}}
	if err := bundle.ValidateWithWorkspaceOutputs(workspaceOutputIDs); err != nil {
		return Result{}, fail("lower", "INVALID_RECIPE", "$.recipe", err.Error(), nil, err)
	}
	digest, err := bundle.DigestWithWorkspaceOutputs(workspaceOutputIDs)
	if err != nil {
		return Result{}, fail("lower", "RECIPE_DIGEST_FAILED", "$.recipe", "recipe digest could not be calculated", nil, err)
	}
	return Result{Bundle: bundle, RecipeDigest: digest, EmittedColumns: emitted, IdentityMappings: mappings, Presentation: presentation, OutputContract: contract}, nil
}

func mergeContractQuality(contract *receiptmodel.PublicOutputContract, emission viewer.EmittedColumn) {
	contract.Lossless = contract.Lossless && emission.Lossless
	contract.MLReady = contract.MLReady && emission.MLReady
	if emission.StructuralSuitability == "requires-review" {
		contract.StructuralSuitability = "requires-review"
	} else if contract.StructuralSuitability == "scalar" && emission.StructuralSuitability == "array" {
		contract.StructuralSuitability = "array"
	}
	for _, reason := range emission.LossReasons {
		if !containsSemanticString(contract.LossReasons, reason) {
			contract.LossReasons = append(contract.LossReasons, reason)
		}
	}
}

func containsSemanticString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func choiceArmForPath(path string) string {
	for _, raw := range strings.Split(strings.TrimPrefix(path, "root."), ".") {
		part := strings.TrimSuffix(raw, "[]")
		for _, family := range []string{"value", "effective", "deceased", "onset", "performed", "occurrence", "timing", "asNeeded", "medication"} {
			if len(part) > len(family) && strings.HasPrefix(part, family) {
				next := part[len(family)]
				if next >= 'A' && next <= 'Z' {
					return part
				}
			}
		}
	}
	return ""
}

func indexedLabel(label string, coordinates []capability.RepeatedCoordinate) string {
	parts := []string{label}
	for _, coordinate := range coordinates {
		parts = append(parts, fmt.Sprintf("[%d]", coordinate.Index))
	}
	return strings.Join(parts, " ")
}

func publicColumnContract(column viewer.EmittedColumn) receiptmodel.PublicOutputColumn {
	return receiptmodel.PublicOutputColumn{
		Column: column.PublicColumn, AuthoredColumns: append([]string(nil), column.AuthoredColumns...), InputColumns: append([]string(nil), column.InputColumns...), Label: firstNonEmpty(column.Label, column.PublicColumn), LogicalType: column.LogicalType,
		Nullable: column.Nullable, Shape: column.Shape, SourceResourceType: column.SourceResourceType, SourcePath: column.SourcePath,
		ChoiceArm: column.ChoiceArm, Coordinates: append([]capability.RepeatedCoordinate(nil), column.Coordinates...), UnitNormalization: clonePublicUnitNormalization(column.UnitNormalization),
		Lossless: column.Lossless, MLReady: column.MLReady, StructuralSuitability: column.StructuralSuitability, LossReasons: append([]string(nil), column.LossReasons...), Filterable: column.Filterable, Chartable: column.Chartable,
	}
}

func publicUnitNormalization(policy *ws.UnitNormalizationPolicy) *viewer.PublicUnitNormalization {
	if policy == nil {
		return nil
	}
	approved, err := unit.ResolveApprovedUnitPolicy(policy.PolicyID, policy.Version)
	if err != nil {
		return nil
	}
	rules := make([]viewer.PublicUnitRuleIdentity, 0, len(approved.Rules))
	for _, rule := range approved.Rules {
		rules = append(rules, viewer.PublicUnitRuleIdentity{ID: rule.ID, Version: rule.Version})
	}
	return &viewer.PublicUnitNormalization{Target: approved.Target, Rules: rules}
}

func clonePublicUnitNormalization(input *viewer.PublicUnitNormalization) *viewer.PublicUnitNormalization {
	if input == nil {
		return nil
	}
	copy := *input
	copy.Rules = append([]viewer.PublicUnitRuleIdentity(nil), input.Rules...)
	return &copy
}

func presentationColumn(source ws.Column, sourceOrder, order int, emission viewer.EmittedColumn) PresentationColumn {
	visible := true
	pinned := false
	if source.Table != nil {
		if source.Table.Visible != nil {
			visible = *source.Table.Visible
		}
		pinned = source.Table.Pinned
	} else {
		visible = false
	}
	authoredOrder := sourceOrder
	if source.Table != nil && source.Table.Order != nil {
		authoredOrder = *source.Table.Order
	}
	result := PresentationColumn{EmissionID: emission.EmissionID, PublicColumn: emission.PublicColumn, Label: emission.Label, Visible: visible, Order: authoredOrder, PhysicalOrder: order, Pinned: pinned}
	if source.Filter != nil && emission.Filterable {
		result.FilterLabel = firstNonEmpty(source.Filter.Label, emission.Label)
		result.FilterOrder = sourceOrder
	}
	if source.Chart != nil && emission.Chartable {
		result.ChartType = source.Chart.Type
		result.ChartTitle = source.Chart.Title
		result.ChartOrder = sourceOrder
	}
	return result
}

func resolveSemanticRoute(document ws.Document, snapshot capability.Snapshot) (map[string]semanticOccurrence, error) {
	byType := map[string][]capability.Node{}
	for _, node := range snapshot.Nodes {
		byType[node.ResourceType] = append(byType[node.ResourceType], node)
	}
	result := map[string]semanticOccurrence{}
	var walk func(ws.RouteNode, *semanticOccurrence, string, int, map[string]bool) error
	walk = func(route ws.RouteNode, parent *semanticOccurrence, path string, depth int, usedEdges map[string]bool) error {
		if snapshot.Policy.Route.MaxHops > 0 && depth > snapshot.Policy.Route.MaxHops {
			return fail("route", "ROUTE_TOO_LONG", path, "route exceeds capability route policy", map[string]any{"maxHops": snapshot.Policy.Route.MaxHops, "hops": depth}, nil)
		}
		var graph capability.Node
		var edge *capability.Edge
		if parent == nil {
			matches := byType[route.ResourceType]
			eligible := matches[:0]
			for _, match := range matches {
				if match.RowRootEligible {
					eligible = append(eligible, match)
				}
			}
			if len(eligible) != 1 {
				return fail("route", "AMBIGUOUS_ROOT_RESOURCE", path+".resourceType", "root resource type must resolve to exactly one eligible capability node", map[string]any{"resourceType": route.ResourceType, "matches": len(eligible)}, nil)
			}
			graph = eligible[0]
		} else {
			var selected capability.Edge
			if route.CatalogEdgeID != "" {
				candidate, found := snapshot.Edge(route.CatalogEdgeID)
				if !found || !semanticRouteEdgeMatches(snapshot, parent.graph, route, candidate) {
					return fail("route", "STALE_ROUTE_EDGE", path+".catalogEdgeId", "pinned catalog edge does not identify the authored route step", map[string]any{"catalogEdgeId": route.CatalogEdgeID}, nil)
				}
				selected = candidate
			} else {
				matches := []capability.Edge{}
				for _, candidate := range snapshot.Edges {
					if semanticRouteEdgeMatches(snapshot, parent.graph, route, candidate) {
						matches = append(matches, candidate)
					}
				}
				if len(matches) != 1 {
					return fail("route", "AMBIGUOUS_RELATIONSHIP", path+".relationship", "relationship must resolve to exactly one capability edge", map[string]any{"fromResourceType": parent.graph.ResourceType, "relationship": route.Relationship, "toResourceType": route.ResourceType, "matches": len(matches)}, nil)
				}
				selected = matches[0]
			}
			if usedEdges[selected.ID] && !snapshot.Policy.Route.AllowsRepeatedEdges {
				return fail("route", "REPEATED_EDGE_NOT_ALLOWED", path+".relationship", "route policy does not allow repeated edges", nil, nil)
			}
			if selected.FromNodeID == selected.ToNodeID && !snapshot.Policy.Route.AllowsSelfLoops {
				return fail("route", "SELF_LOOP_NOT_ALLOWED", path+".relationship", "route policy does not allow self loops", nil, nil)
			}
			nextUsedEdges := make(map[string]bool, len(usedEdges)+1)
			for id, used := range usedEdges {
				nextUsedEdges[id] = used
			}
			nextUsedEdges[selected.ID] = true
			edge = &selected
			graph, _ = snapshot.Node(selected.ToNodeID)
			usedEdges = nextUsedEdges
		}
		current := semanticOccurrence{node: route, graph: graph, edge: edge}
		result[route.OccurrenceID] = current
		children := append([]ws.RouteNode(nil), route.Children...)
		sort.SliceStable(children, func(i, j int) bool { return children[i].OccurrenceID < children[j].OccurrenceID })
		for i := range children {
			if err := walk(children[i], &current, fmt.Sprintf("%s.children[%d]", path, i), depth+1, usedEdges); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(document.Route, nil, "$.route", 0, map[string]bool{}); err != nil {
		return nil, err
	}
	return result, nil
}

func semanticRouteEdgeMatches(snapshot capability.Snapshot, parent capability.Node, child ws.RouteNode, edge capability.Edge) bool {
	from, fromFound := snapshot.Node(edge.FromNodeID)
	to, toFound := snapshot.Node(edge.ToNodeID)
	direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
	return edge.ID != "" && edge.BlockedReason == "" && fromFound && toFound &&
		edge.FromNodeID == parent.ID && from.ResourceType == parent.ResourceType &&
		(edge.SourceResourceType == "" || edge.SourceResourceType == parent.ResourceType) &&
		to.ResourceType == child.ResourceType && (edge.TargetResourceType == "" || edge.TargetResourceType == child.ResourceType) && edge.Label == child.Relationship &&
		(direction == "" || direction == "INBOUND" || direction == "OUTBOUND")
}

func semanticTraversals(route ws.RouteNode, occurrences map[string]semanticOccurrence, nodes map[string]*semanticRecipeNode) []recipe.Traversal {
	children := append([]ws.RouteNode(nil), route.Children...)
	sort.SliceStable(children, func(i, j int) bool { return children[i].OccurrenceID < children[j].OccurrenceID })
	result := make([]recipe.Traversal, 0, len(children))
	for _, child := range children {
		occurrence := occurrences[child.OccurrenceID]
		node := nodes[child.OccurrenceID]
		matchMode := recipe.MatchOptional
		if child.MatchMode.Normalized() == ws.RouteMatchRequired {
			matchMode = recipe.MatchRequired
		}
		result = append(result, recipe.Traversal{Name: recipeName(occurrence.edge.Label, occurrence.edge.ID), OccurrenceID: child.OccurrenceID, Alias: semanticAlias(child.OccurrenceID), ToResourceType: occurrence.graph.ResourceType, MatchMode: matchMode, Fields: node.fields, Pivots: node.pivots, OwnerRecords: node.ownerRecords, Aggregates: node.aggregates, DynamicColumns: node.dynamics, Traversals: semanticTraversals(child, occurrences, nodes)})
	}
	return result
}

func semanticAlias(occurrenceID string) string {
	if occurrenceID == ws.RootOccurrenceID {
		return "root"
	}

	return safeName(occurrenceID)
}

func semanticSelfRepeated(candidate capability.Candidate, path string) bool {
	for _, boundary := range candidate.RepeatedBoundaries {
		if strings.TrimPrefix(strings.TrimSpace(boundary.Path), "root.") != path {
			return false
		}
	}
	return true
}

func semanticFieldCandidate(snapshot capability.Snapshot, nodeID, fieldPath string) (capability.Candidate, bool) {
	want := strings.TrimPrefix(strings.TrimSpace(fieldPath), "root.")
	for _, candidate := range snapshot.Candidates {
		actual := strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.")
		if candidate.NodeID == nodeID && actual == want {
			return candidate, true
		}
	}
	return capability.Candidate{}, false
}

func capabilityProjectionMode(mode string) (capability.ProjectionMode, bool) {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "VALUE":
		return capability.ProjectionScalar, true
	case "INDEXED":
		return capability.ProjectionIndexed, true
	case "FIRST":
		return capability.ProjectionFirst, true
	case "ALL":
		return capability.ProjectionArray, true
	case "DISTINCT":
		return capability.ProjectionDistinctArray, true
	default:
		return "", false
	}
}

func semanticFixedLookup(column ws.Column, resourceType, alias, leaf, logicalType string) (recipe.DynamicColumn, error) {
	empty := ""
	fieldPath := strings.Trim(strings.TrimSpace(column.Source.FieldPath()), ".")
	sourcePath, keyPath := "", ""
	var value recipe.Expression
	sourceKey := column.Source.LookupMatch()
	switch column.Source.Kind {
	case ws.SourceIdentifierBySystem:
		if column.Source.Lookup != nil && column.Source.Lookup.Identifier != nil {
			binding := *column.Source.Lookup.Identifier
			checked, err := fhirbinding.ValidateIdentifierBinding(resourceType, binding)
			if err != nil {
				return recipe.DynamicColumn{}, fmt.Errorf("identifier binding: %w", err)
			}
			sourcePath = checked.OwnerSelector.CanonicalPath()
			keyPath = "item." + checked.SystemSelector.CanonicalPath()
			value = recipe.Expression{Select: "item." + checked.ValueSelector.CanonicalPath()}
			sourceKey = checked.SystemURI
		} else {
			sourcePath, keyPath = firstNonEmpty(fieldPath, "identifier[]"), "item.system"
			value = recipe.Expression{Select: "item.value"}
		}
	case ws.SourceExtensionByURL:
		sourcePath, keyPath = firstNonEmpty(fieldPath, "extension[]"), "item.url"
		value = coalesceString("item.valueString", "item.valueCode", "item.valueInteger", "item.valueDecimal", "item.valueBoolean", "item.valueDate", "item.valueDateTime", "item.valueUri")
	default:
		return recipe.DynamicColumn{}, fmt.Errorf("unsupported source kind %q", column.Source.Kind)
	}
	key := recipe.Expression{Select: keyPath}
	return recipe.DynamicColumn{Name: "fixed_" + shortHash(column.Column), ValueMode: projectionValueMode(column.Source.ProjectionMode()), ColumnPrefix: &empty, Source: recipe.Expression{Select: alias + "." + sourcePath}, Key: &key, Value: &value, Columns: []string{leaf}, MaxColumns: 1, ColumnTypes: map[string]string{leaf: logicalType}, ColumnSourceKeys: map[string]string{leaf: sourceKey}}, nil
}

func semanticAggregate(column ws.Column, alias, resourceType string, contributorWhere *recipe.Filter) (recipe.Aggregate, string, error) {
	if column.Source.Aggregate == nil {
		return recipe.Aggregate{}, "", fmt.Errorf("aggregate payload is required")
	}
	source := column.Source.Aggregate
	op := strings.ToUpper(strings.TrimSpace(source.Operation))
	operation := recipe.AggregateOperation(op)
	aggregate := recipe.Aggregate{
		Name: "aggregate_" + shortHash(column.OccurrenceID+"\x00"+column.Column+"\x00"+op), OutputName: column.Column,
		Operation: operation, FieldRef: column.Column, ValueMode: recipe.ValueModeAuto,
		RequiredValues: append([]string(nil), source.RequiredValues...),
	}
	if window := source.ContributorWindow; window != nil {
		timestampPath := strings.TrimPrefix(strings.Trim(strings.TrimSpace(window.TimestampPath), "."), "root.")
		anchorPath := strings.TrimPrefix(strings.Trim(strings.TrimSpace(window.AnchorPath), "."), "root.")
		aggregate.ContributorWindow = &recipe.ContributorWindow{
			Timestamp:   recipe.Expression{Select: alias + "." + timestampPath},
			Anchor:      recipe.Expression{Select: "root." + anchorPath},
			LowerOffset: window.LowerOffset, UpperOffset: window.UpperOffset,
			LowerInclusive: window.LowerInclusive, UpperInclusive: window.UpperInclusive,
			Precision: recipe.TemporalPrecision(window.Precision),
		}
	}
	if ordering := source.Ordering; ordering != nil {
		timestampPath := strings.TrimPrefix(strings.Trim(strings.TrimSpace(ordering.TimestampPath), "."), "root.")
		aggregate.Ordering = &recipe.TemporalOrdering{
			Timestamp: recipe.Expression{Select: alias + "." + timestampPath},
			Direction: recipe.TemporalDirection(ordering.Direction), TiePolicy: recipe.TemporalTiePolicy(ordering.TiePolicy),
		}
	}
	if strings.TrimSpace(source.Path) != "" {
		path := strings.Trim(strings.TrimSpace(source.Path), ".")
		aggregate.Expr = &recipe.Expression{Select: alias + "." + path}
	}
	if source.UnitNormalization != nil {
		if strings.TrimSpace(source.Path) == "" {
			return recipe.Aggregate{}, "", fmt.Errorf("unit normalization requires a Quantity value path")
		}
		valuePath := strings.Trim(strings.TrimSpace(source.Path), ".")
		systemPath, codePath, pathErr := quantityIdentityPaths(resourceType, valuePath)
		if pathErr != nil {
			return recipe.Aggregate{}, "", pathErr
		}
		approved, policyErr := unit.ResolveApprovedUnitPolicy(source.UnitNormalization.PolicyID, source.UnitNormalization.Version)
		if policyErr != nil {
			return recipe.Aggregate{}, "", policyErr
		}
		aggregate.UnitNormalization = &recipe.UnitNormalizationPolicy{
			SystemPath: systemPath, CodePath: codePath, Target: approved.Target,
			Rules: append([]unit.UnitRuleReference(nil), approved.Rules...),
		}
	}
	if contributorWhere != nil {
		aggregate.Where = contributorWhere
	}

	logicalType := "string"
	switch operation {
	case recipe.AggregateCount, recipe.AggregateCountDistinct:
		logicalType = "integer"
	case recipe.AggregateSum, recipe.AggregateMean:
		metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, strings.Trim(strings.TrimSpace(source.Path), "."))
		if !ok || (metadata.Primitive != fhirschema.PrimitiveInteger && metadata.Primitive != fhirschema.PrimitiveDecimal) {
			return recipe.Aggregate{}, "", fmt.Errorf("aggregate operation %s requires an integer or decimal selector", operation)
		}
		logicalType = "decimal"
	case recipe.AggregateExists, recipe.AggregateContainsAll:
		logicalType = "boolean"
	case recipe.AggregateDistinctValues, recipe.AggregateMin, recipe.AggregateMax, recipe.AggregateRequireOne, recipe.AggregateCollect, recipe.AggregateFirstOrdered:
		metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, strings.Trim(strings.TrimSpace(source.Path), "."))
		if !ok || metadata.Primitive == fhirschema.PrimitiveUnknown {
			return recipe.Aggregate{}, "", fmt.Errorf("aggregate selector %q is not represented by generated resource type %q", source.Path, resourceType)
		}
		switch metadata.Primitive {
		case fhirschema.PrimitiveInteger:
			logicalType = "integer"
		case fhirschema.PrimitiveDecimal:
			logicalType = "decimal"
		case fhirschema.PrimitiveBoolean:
			logicalType = "boolean"
		case fhirschema.PrimitiveDate:
			logicalType = "date"
		case fhirschema.PrimitiveDateTime:
			logicalType = "date_time"
		default:
			logicalType = "string"
		}
	default:
		return recipe.Aggregate{}, "", fmt.Errorf("unsupported aggregate operation %q", source.Operation)
	}
	return aggregate, logicalType, nil
}

func quantityIdentityPaths(resourceType, valuePath string) (string, string, error) {
	return fhirbinding.QuantityIdentityPaths(resourceType, valuePath)
}

func capabilityCandidate(snapshot capability.Snapshot, nodeID, candidateID string) (capability.Candidate, bool) {
	for _, candidate := range snapshot.Candidates {
		if candidate.ID == candidateID && candidate.NodeID == nodeID {
			return candidate, true
		}
	}
	return capability.Candidate{}, false
}

func contributorRecipeFilter(resourceType, alias string, candidate capability.Candidate, predicate ws.ContributorPredicate) (recipe.Filter, error) {
	path := strings.TrimPrefix(strings.Trim(strings.TrimSpace(candidate.FieldPath), "."), "root.")
	selector, err := spec.ParseSelector(path)
	if err != nil {
		return recipe.Filter{}, fmt.Errorf("contributor candidate %q selector: %w", candidate.ID, err)
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, selector.CanonicalPath())
	if !ok || metadata.Primitive == fhirschema.PrimitiveUnknown {
		return recipe.Filter{}, fmt.Errorf("contributor candidate %q selector %q is not a supported scalar", candidate.ID, candidate.FieldPath)
	}
	where := recipe.Filter{Select: alias + "." + selector.CanonicalPath(), FieldRef: candidate.ID}
	where.Operator = recipe.FilterOperator(predicate.Operator)
	where.Quantifier = recipe.ArrayQuantifier(predicate.Quantifier)
	if predicate.Value != nil {
		value := recipe.FilterValue{Kind: recipe.FilterValueKind(predicate.Value.Kind)}
		switch predicate.Value.Kind {
		case ws.ContributorString:
			stringValue := *predicate.Value.String
			value.String = &stringValue
		case ws.ContributorValueCode:
			value.Code = &recipe.CodeValue{Code: predicate.Value.Code.Code}
		default:
			return recipe.Filter{}, fmt.Errorf("unsupported contributor value kind %q", predicate.Value.Kind)
		}
		where.Values = []recipe.FilterValue{value}
	}
	if err := where.Validate(); err != nil {
		return recipe.Filter{}, fmt.Errorf("contributor candidate %q: %w", candidate.ID, err)
	}
	return where, nil
}

func semanticExtensionPivot(column ws.Column, leaf string) (recipe.Pivot, error) {
	lookup := column.Source.Lookup
	if lookup == nil || lookup.Extension == nil {
		return recipe.Pivot{}, fmt.Errorf("ancestor-aware extension lookup requires extension binding")
	}
	if strings.TrimSpace(column.Column) == "" {
		return recipe.Pivot{}, fmt.Errorf("extension output column is required")
	}
	return recipe.Pivot{
		Name:    "extension_correlated_" + shortHash(column.Column+"\x00"+strings.Join(lookup.Extension.URLPath, "\x00")),
		Columns: []string{leaf}, ColumnAliases: map[string]string{leaf: leaf},
		ProjectionMode:       recipe.NormalizedPivotProjectionMode(lookup.ProjectionMode),
		ExtensionCorrelation: lookup.Extension,
	}, nil
}

func semanticCodedValuePivot(column ws.Column, leaf string) (recipe.Pivot, error) {
	lookup := column.Source.Lookup
	if lookup == nil || lookup.Binding == nil || lookup.Key == nil {
		return recipe.Pivot{}, fmt.Errorf("correlated coding lookup requires binding and key")
	}
	if strings.TrimSpace(column.Column) == "" {
		return recipe.Pivot{}, fmt.Errorf("correlated coding output column is required")
	}
	pivotName := "correlated_" + shortHash(column.Column+"\x00"+lookup.Key.System+"\x00"+lookup.Key.Code)
	columns := []string{lookup.Key.Code}
	columnAliases := map[string]string{lookup.Key.Code: leaf}
	if lookup.Binding.SelectionScope == fhirbinding.CorrelatedSelectionScopeSystemVersion {

		pivotName = "correlated_category_" + shortHash(column.Column+"\x00"+lookup.Key.System+"\x00"+lookup.Key.Version+"\x00"+lookup.Binding.KeyPath)
		columns = []string{leaf}
		columnAliases = map[string]string{leaf: leaf}
	}
	pivot := recipe.Pivot{
		Name: pivotName, Columns: columns, ColumnAliases: columnAliases,
		ProjectionMode: recipe.NormalizedPivotProjectionMode(lookup.ProjectionMode), Correlation: lookup.Binding,
		CorrelationSystem: lookup.Key.System, CorrelationCode: lookup.Key.Code, CorrelationVersion: lookup.Key.Version,
	}
	return pivot, nil
}

func appendSemanticPivot(pivots []recipe.Pivot, pivot recipe.Pivot) []recipe.Pivot {
	for i := range pivots {
		if pivots[i].Name == pivot.Name {
			pivots[i].Columns = append(pivots[i].Columns, pivot.Columns...)
			return pivots
		}
	}
	return append(pivots, pivot)
}

func coalesceString(paths ...string) recipe.Expression {
	args := make([]recipe.Expression, 0, len(paths))
	for _, path := range paths {
		args = append(args, recipe.Expression{Select: path})
	}
	return recipe.Expression{Call: "coalesce_string", Args: args}
}

func constructionExpandedFieldOwner(construction *ws.Construction, projection ws.ConstructionSourceProjection, path string) *ws.ConstructionExpandedFieldSource {
	if projection.OwnerStepID == "" {
		return nil
	}
	for _, step := range construction.Steps {
		if step.ID != projection.OwnerStepID || step.Operation.Kind != ws.ConstructionOperationExpand || step.Operation.Expand == nil {
			continue
		}
		expanded := step.Operation.Expand
		if expanded.ExpandedField == nil || expanded.InputColumnID != projection.ColumnID {
			return nil
		}
		if err := ws.ValidateConstructionExpandedFieldSource(expanded.ExpandedField); err != nil || expanded.ExpandedField.Path != path {
			return nil
		}
		return expanded.ExpandedField
	}
	return nil
}

func (columns *semanticColumnCompiler) compileAggregate(index int, column ws.Column, occurrence semanticOccurrence, alias string) (string, error) {
	document, snapshot := columns.document, columns.snapshot
	root := columns.occurrences[ws.RootOccurrenceID]
	nodes, explorerID := columns.nodes, columns.explorerID
	if source := column.Source.Aggregate; source != nil {
		if source.ContributorWindow != nil && column.OccurrenceID == ws.RootOccurrenceID {
			return "", fail("intent", "CONTRIBUTOR_WINDOW_REQUIRES_RELATED_RESOURCE", fmt.Sprintf("$.columns[%d].source.aggregate.contributorWindow", index), "contributor windows require a related resource occurrence", nil, nil)
		}
		operation := capability.AggregateOperation(strings.ToUpper(strings.TrimSpace(source.Operation)))
		path := strings.TrimPrefix(strings.TrimSpace(source.Path), "root.")
		candidate, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, path)
		if found {
			input := capability.AggregateInput{
				LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
				HasField: path != "", RelatedResource: column.OccurrenceID != ws.RootOccurrenceID,
				ContributorWindowConfigured: source.ContributorWindow != nil,
				OrderingConfigured:          source.Ordering != nil,
				RequiredValuesConfigured:    len(source.RequiredValues) > 0,
			}
			choices := capability.DeriveAggregateOperationCapabilities(input, capability.AggregateRowContext(document.Rows.Kind))
			for _, choice := range choices {
				if choice.Operation == operation && !choice.Supported && (operation == capability.AggregateSum || operation == capability.AggregateMean) {
					return "", fail("capability", "AGGREGATE_OPERATION_UNAVAILABLE", fmt.Sprintf("$.columns[%d].source.aggregate.operation", index), choice.Reason, map[string]any{"operation": operation, "reasonCode": choice.ReasonCode, "rowContext": choice.RowContext}, nil)
				}
			}
		}
	}
	var contributorWhere *recipe.Filter
	if column.Contributor != nil {
		catalog := ws.CatalogFromCapability(snapshot, explorerID)
		if err := authoringv2.ValidateContributorForCatalog(document, catalog, column.OccurrenceID, column.Source, *column.Contributor); err != nil {
			return "", fail("intent", "INVALID_CONTRIBUTOR_PREDICATE", fmt.Sprintf("$.columns[%d].contributor", index), err.Error(), map[string]any{"candidateId": column.Contributor.CandidateID}, err)
		}
		candidate, found := capabilityCandidate(snapshot, occurrence.graph.ID, column.Contributor.CandidateID)
		if !found {
			return "", fail("intent", "STALE_CONTRIBUTOR_CANDIDATE", fmt.Sprintf("$.columns[%d].contributor.candidateId", index), "contributor candidate is not present on the resolved capability node", map[string]any{"candidateId": column.Contributor.CandidateID}, nil)
		}
		where, whereErr := contributorRecipeFilter(occurrence.graph.ResourceType, alias, candidate, *column.Contributor)
		if whereErr != nil {
			return "", fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].contributor", index), whereErr.Error(), nil, whereErr)
		}
		contributorWhere = &where
	}
	if column.Source.Aggregate != nil {
		if path := strings.TrimPrefix(strings.TrimSpace(column.Source.Aggregate.Path), "root."); path != "" {
			if _, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, path); !found {
				return "", fail("intent", "STALE_FIELD", fmt.Sprintf("$.columns[%d].source.aggregate.path", index), "aggregate path is not present on the resolved capability node", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": path}, nil)
			}
		}
		if window := column.Source.Aggregate.ContributorWindow; window != nil {
			timestampPath := strings.TrimPrefix(strings.TrimSpace(window.TimestampPath), "root.")
			timestampCandidate, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, timestampPath)
			if !found || !strings.EqualFold(timestampCandidate.LogicalType, "date_time") {
				return "", fail("intent", "INVALID_CONTRIBUTOR_WINDOW_TIMESTAMP", fmt.Sprintf("$.columns[%d].source.aggregate.contributorWindow.timestampPath", index), "contributor window timestamp must be a date_time field on the contributing resource", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": timestampPath}, nil)
			}
			transformCandidate := timestampCandidate
			aggregatePath := strings.TrimPrefix(strings.TrimSpace(column.Source.Aggregate.Path), "root.")
			if aggregatePath != "" {
				if aggregateCandidate, hasAggregate := semanticFieldCandidate(snapshot, occurrence.graph.ID, aggregatePath); hasAggregate {
					transformCandidate = aggregateCandidate
				}
			} else {
				operation := strings.ToUpper(strings.TrimSpace(column.Source.Aggregate.Operation))
				if operation != "COUNT" && operation != "EXISTS" {
					return "", fail("intent", "STALE_FIELD", fmt.Sprintf("$.columns[%d].source.aggregate.path", index), "aggregate path is required for this operation", map[string]any{"operation": operation}, nil)
				}
			}
			transformations := ws.AggregateTransformationCapabilitiesForCapability(snapshot, transformCandidate.ID)
			if !transformations.Temporal.SupportsTimestamp(occurrence.graph.ResourceType, timestampPath) {
				return "", fail("capability", "UNADVERTISED_CONTRIBUTOR_WINDOW_TIMESTAMP", fmt.Sprintf("$.columns[%d].source.aggregate.contributorWindow.timestampPath", index), "contributor window timestamp is not an advertised scalar date_time choice for the contributing resource", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": timestampPath}, nil)
			}
			anchorPath := strings.TrimPrefix(strings.TrimSpace(window.AnchorPath), "root.")
			anchorCandidate, found := semanticFieldCandidate(snapshot, root.graph.ID, anchorPath)
			if !found || !strings.EqualFold(anchorCandidate.LogicalType, "date_time") {
				return "", fail("intent", "INVALID_CONTRIBUTOR_WINDOW_ANCHOR", fmt.Sprintf("$.columns[%d].source.aggregate.contributorWindow.anchorPath", index), "contributor window anchor must be a date_time field on the root row", map[string]any{"resourceType": root.graph.ResourceType, "fieldPath": anchorPath}, nil)
			}
			if !transformations.Temporal.SupportsAnchor(root.graph.ResourceType, anchorPath) {
				return "", fail("capability", "UNADVERTISED_CONTRIBUTOR_WINDOW_ANCHOR", fmt.Sprintf("$.columns[%d].source.aggregate.contributorWindow.anchorPath", index), "contributor window anchor is not an advertised scalar date_time choice for the root row", map[string]any{"resourceType": root.graph.ResourceType, "fieldPath": anchorPath}, nil)
			}
			if ordering := column.Source.Aggregate.Ordering; ordering != nil {
				orderingPath := strings.TrimPrefix(strings.TrimSpace(ordering.TimestampPath), "root.")
				orderingCandidate, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, orderingPath)
				if !found || !strings.EqualFold(orderingCandidate.LogicalType, "date_time") {
					return "", fail("intent", "INVALID_ORDERING_TIMESTAMP", fmt.Sprintf("$.columns[%d].source.aggregate.ordering.timestampPath", index), "ordering timestamp must be a date_time field on the contributing resource", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": orderingPath}, nil)
				}
				if !transformations.Temporal.SupportsTimestamp(occurrence.graph.ResourceType, orderingPath) {
					return "", fail("capability", "UNADVERTISED_ORDERING_TIMESTAMP", fmt.Sprintf("$.columns[%d].source.aggregate.ordering.timestampPath", index), "ordering timestamp is not an advertised scalar date_time choice for the contributing resource", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": orderingPath}, nil)
				}
			}
		}
		if policy := column.Source.Aggregate.UnitNormalization; policy != nil {
			aggregatePath := strings.TrimPrefix(strings.TrimSpace(column.Source.Aggregate.Path), "root.")
			aggregateCandidate, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, aggregatePath)
			if !found {
				return "", fail("intent", "STALE_FIELD", fmt.Sprintf("$.columns[%d].source.aggregate.path", index), "aggregate path is not present on the resolved capability node", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": aggregatePath}, nil)
			}
			transformations := ws.AggregateTransformationCapabilitiesForCapability(snapshot, aggregateCandidate.ID)
			advertised := false
			for _, preset := range transformations.UnitNormalization.Presets {
				if preset.PolicyID != policy.PolicyID || preset.Version != policy.Version {
					continue
				}
				if !preset.Available {
					return "", fail("capability", "UNIT_NORMALIZATION_UNAVAILABLE", fmt.Sprintf("$.columns[%d].source.aggregate.unitNormalization", index), preset.Reason, map[string]any{"policyId": policy.PolicyID, "version": policy.Version, "reasonCode": preset.ReasonCode}, nil)
				}
				advertised = true
				break
			}
			if !advertised {
				return "", fail("capability", "UNADVERTISED_UNIT_NORMALIZATION", fmt.Sprintf("$.columns[%d].source.aggregate.unitNormalization", index), "unit normalization policy is not advertised for the aggregate candidate", map[string]any{"policyId": policy.PolicyID, "version": policy.Version}, nil)
			}
		}
	}
	aggregate, aggregateType, aggregateErr := semanticAggregate(column, alias, occurrence.graph.ResourceType, contributorWhere)
	if aggregateErr != nil {
		return "", fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), aggregateErr.Error(), nil, aggregateErr)
	}
	nodes[column.OccurrenceID].aggregates = append(nodes[column.OccurrenceID].aggregates, aggregate)
	return aggregateType, nil
}
