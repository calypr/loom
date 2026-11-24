package ir

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	"github.com/calypr/loom/internal/projectid"
)

func validateCohortRootSourceProof(plan PhysicalPlan) error {
	var source *PhysicalCohortRootSource
	for _, operation := range plan.Operations {
		if operation.RootScan != nil && operation.RootScan.CohortSource != nil {
			source = operation.RootScan.CohortSource
			break
		}
	}
	if source == nil {
		return nil
	}
	if source.CohortStageID != "group_rows" || source.CohortInputStageID == "" || source.RootIdentityColumn != "_key" || source.RootResourceType == "" || source.RootResourceType != plan.Source.ResourceType {
		return fmt.Errorf("cohort root source has invalid typed identity proof")
	}
	resourceProject, ok := plan.BindVars[source.ResourceProjectBindKey].(string)
	if !ok || strings.TrimSpace(resourceProject) == "" {
		return fmt.Errorf("cohort root source requires its resource-storage project binding")
	}
	authorizedProject, ok := plan.BindVars[PhysicalScopeProjectBind].(string)
	if !ok || strings.TrimSpace(authorizedProject) == "" || resourceProject != authorizedProject {
		return fmt.Errorf("cohort root source resource-storage project must match the authorized source project")
	}
	if plan.StageSequence == nil {
		return nil
	}
	for _, stage := range plan.StageSequence.Stages {
		if stage.ID != source.CohortStageID || stage.Kind != PhysicalStageCohortGroupOp || stage.CohortGroup == nil {
			continue
		}
		if stage.InputStageID != source.CohortInputStageID {
			return fmt.Errorf("cohort root source input stage differs from typed cohort stage")
		}
		rows := stage.CohortGroup.Rows
		if source.RevisionCollectionBindKey != rows.RevisionCollectionBindKey ||
			source.SelectionCollectionBindKey != rows.SelectionCollectionBindKey ||
			source.SelectionMembersCollectionBindKey != rows.SelectionMembersCollectionBindKey ||
			source.MembershipsCollectionBindKey != rows.MembershipsCollectionBindKey ||
			source.RevisionIDBindKey != rows.RevisionIDBindKey ||
			source.ProjectBindKey != rows.ProjectBindKey ||
			source.ResourceProjectBindKey != rows.ResourceProjectBindKey ||
			source.DatasetGenerationBindKey != rows.DatasetGenerationBindKey ||
			source.ResourceTypeBindKey != rows.ResourceTypeBindKey ||
			source.PolicyBindKey != rows.PolicyBindKey {
			return fmt.Errorf("cohort root source bindings differ from the typed cohort stage")
		}
		for _, column := range stage.InputColumns {
			if column.Internal && column.Name == source.RootIdentityColumn && column.RootContributorResourceType == source.RootResourceType &&
				column.Kind == "string" && column.Cardinality == "required_one" {
				return nil
			}
		}
		return fmt.Errorf("cohort root source requires one exact root identity per cohort input row")
	}
	return fmt.Errorf("cohort root source requires a typed COHORT_GROUP stage")
}

func validatePhysicalCohortRootSource(source PhysicalCohortRootSource, bindVars map[string]any) error {
	for _, key := range []string{
		source.RevisionCollectionBindKey,
		source.SelectionCollectionBindKey,
		source.SelectionMembersCollectionBindKey,
		source.MembershipsCollectionBindKey,
	} {
		if err := requireCollectionBind(bindVars, key); err != nil {
			return err
		}
	}
	for _, key := range []string{
		source.RevisionIDBindKey,
		source.ProjectBindKey,
		source.ResourceProjectBindKey,
		source.DatasetGenerationBindKey,
		source.ResourceTypeBindKey,
		source.PolicyBindKey,
	} {
		if err := requireBind(bindVars, key); err != nil {
			return err
		}
	}
	selectionProject, ok := bindVars[source.ProjectBindKey].(string)
	if !ok || strings.TrimSpace(selectionProject) == "" {
		return fmt.Errorf("cohort selection project bind must be a non-empty string")
	}
	resourceProject, ok := bindVars[source.ResourceProjectBindKey].(string)
	if !ok || strings.TrimSpace(resourceProject) == "" || projectid.Canonical(resourceProject) != projectid.Canonical(selectionProject) {
		return fmt.Errorf("cohort resource-storage project must match the canonical selection project")
	}
	return nil
}

func validatePhysicalGroupRows(rows PhysicalGroupRows, bindVars map[string]any) error {
	outputs := map[string]bool{"group_revision_id": true, "group_id": true, "group_label": true, "group_ordinal": true, "members": true, "__loom_row_id": true}
	for index, value := range rows.MemberValues {
		if !physicalVariablePattern.MatchString(value.Output) || outputs[value.Output] {
			return fmt.Errorf("cohort member value %d has invalid or colliding output %q", index, value.Output)
		}
		outputs[value.Output] = true
		if !validTableScalarKind(value.Kind) && value.Kind != "OBJECT" {
			return fmt.Errorf("cohort member value %q has invalid kind %q", value.Output, value.Kind)
		}
		if value.Policy != "ALL" && value.Policy != "ONE" {
			return fmt.Errorf("cohort member value %q has invalid policy %q", value.Output, value.Policy)
		}
		if !validCohortMemberValueExpression(value.Expression, bindVars) {
			return fmt.Errorf("cohort member value %q must select the exact member payload", value.Output)
		}
		if err := validatePhysicalExpression(value.Expression, map[string]bool{"cohort_member": true}, bindVars); err != nil {
			return fmt.Errorf("cohort member value %q: %w", value.Output, err)
		}
	}
	if rows.CellTrace != nil {
		trace := rows.CellTrace
		if !physicalPathPartPattern.MatchString(trace.OutputColumn) || !outputs[trace.OutputColumn] || strings.HasPrefix(trace.OutputColumn, "__loom_") {
			return fmt.Errorf("group-row cell trace output column %q is not a public group field", trace.OutputColumn)
		}
		wantCardinality := PhysicalScalarCardinality
		if trace.OutputColumn == "members" {
			wantCardinality = PhysicalArrayCardinality
		} else {
			for _, value := range rows.MemberValues {
				if value.Output == trace.OutputColumn && value.Policy == "ALL" {
					wantCardinality = PhysicalArrayCardinality
					break
				}
			}
		}
		if trace.Cardinality != wantCardinality {
			return fmt.Errorf("group-row cell trace output %q cardinality %q does not match its projected shape %q", trace.OutputColumn, trace.Cardinality, wantCardinality)
		}
		if strings.TrimSpace(trace.OffsetBindKey) == "" || strings.TrimSpace(trace.LimitBindKey) == "" || strings.TrimSpace(trace.FetchLimitBindKey) == "" {
			return fmt.Errorf("group-row cell trace requires offset, limit, and fetch-limit binds")
		}
		offset, ok := bindVars[trace.OffsetBindKey].(int)
		if !ok || offset < 0 {
			return fmt.Errorf("group-row cell trace offset bind %q must be a non-negative int", trace.OffsetBindKey)
		}
		limit, ok := bindVars[trace.LimitBindKey].(int)
		if !ok || limit <= 0 {
			return fmt.Errorf("group-row cell trace limit bind %q must be a positive int", trace.LimitBindKey)
		}
		fetchLimit, ok := bindVars[trace.FetchLimitBindKey].(int)
		if !ok || fetchLimit != limit+1 {
			return fmt.Errorf("group-row cell trace fetch-limit bind %q must be limit plus one", trace.FetchLimitBindKey)
		}
	}
	for _, key := range []string{
		rows.RevisionCollectionBindKey, rows.SelectionCollectionBindKey,
		rows.DefinitionsCollectionBindKey, rows.MembershipsCollectionBindKey,
		rows.SelectionMembersCollectionBindKey, rows.ResourceCollectionBindKey,
	} {
		if err := requireCollectionBind(bindVars, key); err != nil {
			return err
		}
	}
	for _, key := range []string{
		rows.RevisionIDBindKey, rows.ProjectBindKey, rows.ResourceProjectBindKey, rows.DatasetGenerationBindKey,
		rows.ResourceTypeBindKey, rows.PolicyBindKey, rows.AuthResourcePathsBindKey,
		rows.AuthUnrestrictedBindKey,
	} {
		if err := requireBind(bindVars, key); err != nil {
			return err
		}
	}
	selectionProject, ok := bindVars[rows.ProjectBindKey].(string)
	if !ok || strings.TrimSpace(selectionProject) == "" {
		return fmt.Errorf("group selection project bind must be a non-empty string")
	}
	resourceProject, ok := bindVars[rows.ResourceProjectBindKey].(string)
	if !ok || strings.TrimSpace(resourceProject) == "" || projectid.Canonical(resourceProject) != projectid.Canonical(selectionProject) {
		return fmt.Errorf("group resource-storage project must match the canonical selection project")
	}
	if id, ok := bindVars[rows.RevisionIDBindKey].(string); !ok || strings.TrimSpace(id) == "" {
		return fmt.Errorf("group revision ID bind must be a non-empty string")
	}
	if policy, ok := bindVars[rows.PolicyBindKey].(string); !ok || (policy != "ERROR" && policy != "EXCLUDE" && policy != "GROUP_AS_UNASSIGNED") {
		return fmt.Errorf("group unassigned-member policy bind is invalid")
	}
	if rows.LimitBindKey != "" {
		if err := requireBind(bindVars, rows.LimitBindKey); err != nil {
			return err
		}
		limit, ok := bindVars[rows.LimitBindKey].(int)
		if !ok || limit <= 0 {
			return fmt.Errorf("group rows limit bind must be a positive int")
		}
	}
	return nil
}

func validCohortMemberValueExpression(expression PhysicalExpression, bindVars map[string]any) bool {
	if isCohortMemberPayloadSelector(expression) {
		return true
	}
	if expression.Kind != PhysicalCallExpression || expression.Call == nil || expression.Call.Name != "case" {
		return false
	}
	args := expression.Call.Args
	if len(args) < 5 || (len(args)-3)%2 != 0 {
		return false
	}
	selector, nullInput, ok := cohortMemberValueEquality(args[0], nil, bindVars)
	if !ok || nullInput != nil || !isCohortMemberLiteral(args[1], nil, bindVars) {
		return false
	}
	for index := 2; index < len(args)-1; index += 2 {
		_, value, ok := cohortMemberValueEquality(args[index], &selector, bindVars)
		if !ok {
			return false
		}
		if _, ok := value.(string); !ok {
			return false
		}
		output, ok := cohortMemberLiteral(args[index+1], bindVars)
		if !ok {
			return false
		}
		if _, ok := output.(string); !ok {
			return false
		}
	}
	defaultValue := args[len(args)-1]
	if isCohortMemberPayloadSelector(defaultValue) {
		return true
	}
	return isCohortMemberUnknownValueAssertion(defaultValue, bindVars)
}

func isCohortMemberPayloadSelector(expression PhysicalExpression) bool {
	return expression.Kind == PhysicalExtractExpression && expression.Extract != nil &&
		expression.Extract.Source.Variable == "cohort_member" &&
		len(expression.Extract.Source.Path) == 1 && expression.Extract.Source.Path[0] == "payload"
}

func cohortMemberValueEquality(expression PhysicalExpression, expectedSelector *PhysicalExpression, bindVars map[string]any) (PhysicalExpression, any, bool) {
	if expression.Kind != PhysicalCallExpression || expression.Call == nil || expression.Call.Name != "eq" || len(expression.Call.Args) != 2 {
		return PhysicalExpression{}, nil, false
	}
	left := expression.Call.Args[0]
	if !isCohortMemberPayloadSelector(left) {
		return PhysicalExpression{}, nil, false
	}
	if expectedSelector != nil && (left.Extract.Source.Variable != expectedSelector.Extract.Source.Variable ||
		left.Extract.Source.Path[0] != expectedSelector.Extract.Source.Path[0]) {
		return PhysicalExpression{}, nil, false
	}
	value, ok := cohortMemberLiteral(expression.Call.Args[1], bindVars)
	return left, value, ok
}

func isCohortMemberLiteral(expression PhysicalExpression, want any, bindVars map[string]any) bool {
	value, ok := cohortMemberLiteral(expression, bindVars)
	return ok && reflect.DeepEqual(value, want)
}

func cohortMemberLiteral(expression PhysicalExpression, bindVars map[string]any) (any, bool) {
	if expression.Kind != PhysicalLiteralExpression || expression.Literal == nil {
		return nil, false
	}
	value, ok := bindVars[expression.Literal.BindKey]
	return value, ok
}

func isCohortMemberUnknownValueAssertion(expression PhysicalExpression, bindVars map[string]any) bool {
	if expression.Kind != PhysicalCallExpression || expression.Call == nil || expression.Call.Name != "assert" || len(expression.Call.Args) != 2 {
		return false
	}
	return isCohortMemberLiteral(expression.Call.Args[0], false, bindVars) &&
		isCohortMemberLiteral(expression.Call.Args[1], "CATEGORY_RECODE_UNKNOWN_VALUE", bindVars)
}

func validatePhysicalPopulationRootSource(source PhysicalPopulationRootSource, bindVars map[string]any) error {
	if err := requireCollectionBind(bindVars, source.MemberScan.CollectionBindKey); err != nil {
		return fmt.Errorf("member scan: %w", err)
	}
	defined := map[string]bool{}
	if err := definePhysicalVariable(defined, source.MemberScan.Variable); err != nil {
		return fmt.Errorf("member scan: %w", err)
	}
	for index, filter := range source.MemberFilters {
		if err := validatePhysicalFilter(filter, defined, bindVars); err != nil {
			return fmt.Errorf("member filter %d: %w", index, err)
		}
	}
	if len(source.ResourceOperations) == 0 || source.ResourceOperations[0].Kind != PhysicalCollectionScanOp {
		return fmt.Errorf("resource operations must begin with COLLECTION_SCAN")
	}
	for index, operation := range source.ResourceOperations {
		if err := operation.validatePayload(); err != nil {
			return fmt.Errorf("resource operation %d (%s): %w", index, operation.Kind, err)
		}
		switch operation.Kind {
		case PhysicalCollectionScanOp:
			if index != 0 {
				return fmt.Errorf("resource operation %d: population root source permits exactly one leading collection scan", index)
			}
			if err := requireCollectionBind(bindVars, operation.CollectionScan.CollectionBindKey); err != nil {
				return fmt.Errorf("resource operation %d: %w", index, err)
			}
			if err := definePhysicalVariable(defined, operation.CollectionScan.Variable); err != nil {
				return fmt.Errorf("resource operation %d: %w", index, err)
			}
		case PhysicalTraversalOp:
			traversal := operation.Traversal
			if !defined[traversal.SourceVariable] {
				return fmt.Errorf("resource operation %d: traversal source variable %q is out of scope", index, traversal.SourceVariable)
			}
			if traversal.Direction != PhysicalOutbound && traversal.Direction != PhysicalInbound && traversal.Direction != PhysicalAny {
				return fmt.Errorf("resource operation %d: invalid traversal direction %q", index, traversal.Direction)
			}
			if err := validatePhysicalTraversalStrategy(*traversal); err != nil {
				return fmt.Errorf("resource operation %d: %w", index, err)
			}
			if err := requireCollectionBind(bindVars, traversal.EdgeCollectionBindKey); err != nil {
				return fmt.Errorf("resource operation %d: %w", index, err)
			}
			for _, key := range []string{traversal.EdgeLabelBindKey, traversal.TargetTypeBindKey} {
				if key != "" {
					if err := requireBind(bindVars, key); err != nil {
						return fmt.Errorf("resource operation %d: %w", index, err)
					}
				}
			}
			if err := definePhysicalVariable(defined, traversal.TargetVariable); err != nil {
				return fmt.Errorf("resource operation %d: %w", index, err)
			}
			if err := definePhysicalVariable(defined, traversal.EdgeVariable); err != nil {
				return fmt.Errorf("resource operation %d: %w", index, err)
			}
		case PhysicalFilterOp:
			if err := validatePhysicalFilter(*operation.Filter, defined, bindVars); err != nil {
				return fmt.Errorf("resource operation %d: %w", index, err)
			}
		case PhysicalDerivedLetOp:
			if err := validatePhysicalDerivedLet(*operation.DerivedLet, defined, bindVars); err != nil {
				return fmt.Errorf("resource operation %d: %w", index, err)
			}
			if err := definePhysicalVariable(defined, operation.DerivedLet.Variable); err != nil {
				return fmt.Errorf("resource operation %d: %w", index, err)
			}
		default:
			return fmt.Errorf("resource operation %d has unsupported kind %q", index, operation.Kind)
		}
	}
	if err := validatePhysicalValue(source.RootKey, defined, bindVars); err != nil {
		return fmt.Errorf("root key: %w", err)
	}
	if err := validatePhysicalValue(source.MemberID, defined, bindVars); err != nil {
		return fmt.Errorf("member id: %w", err)
	}
	if source.CollectMembersVariable != "" && !physicalVariablePattern.MatchString(source.CollectMembersVariable) {
		return fmt.Errorf("unsafe collected member variable %q", source.CollectMembersVariable)
	}
	return nil
}

func validatePhysicalPathNode(node PhysicalPathNode, defined map[string]bool, binds map[string]any) error {
	if !physicalVariablePattern.MatchString(node.Alias) {
		return fmt.Errorf("path node alias %q is unsafe", node.Alias)
	}
	if strings.TrimSpace(node.ResourceType) == "" {
		return fmt.Errorf("path node %q resource type is required", node.Alias)
	}
	return validatePhysicalValue(node.Value, defined, binds)
}

func validatePhysicalPathExtend(extend PhysicalPathExtend, defined map[string]bool, binds map[string]any) error {
	if !defined[extend.SourceVariable] {
		return fmt.Errorf("source variable %q is out of scope", extend.SourceVariable)
	}
	for _, part := range extend.SourcePath {
		if !physicalPathPartPattern.MatchString(part) {
			return fmt.Errorf("source path segment %q is unsafe", part)
		}
	}
	if extend.MatchMode != "" && extend.MatchMode != "REQUIRED" && extend.MatchMode != "OPTIONAL" {
		return fmt.Errorf("unsupported path match mode %q", extend.MatchMode)
	}
	if err := validatePhysicalPathNode(extend.Node, map[string]bool{extend.Traversal.TargetVariable: true}, binds); err != nil {
		return err
	}
	if !physicalVariablePattern.MatchString(extend.Relationship.Alias) {
		return fmt.Errorf("path relationship alias %q is unsafe", extend.Relationship.Alias)
	}
	if !physicalVariablePattern.MatchString(extend.Traversal.TargetVariable) || !physicalVariablePattern.MatchString(extend.Traversal.EdgeVariable) {
		return fmt.Errorf("path traversal target and edge variables must be safe")
	}
	if extend.Traversal.EdgeCollectionBindKey == "" || extend.Traversal.EdgeLabelBindKey == "" || extend.Traversal.TargetTypeBindKey == "" {
		return fmt.Errorf("path traversal requires edge collection, label, and target type binds")
	}
	for _, key := range []string{extend.Traversal.EdgeCollectionBindKey, extend.Traversal.EdgeLabelBindKey, extend.Traversal.TargetTypeBindKey, extend.Relationship.LabelBindKey} {
		if key != "" {
			if err := requireBind(binds, key); err != nil {
				return err
			}
		}
	}
	definedScope := map[string]bool{extend.Traversal.TargetVariable: true, extend.Traversal.EdgeVariable: true}
	for index, operation := range extend.Scope {
		if operation.Kind != PhysicalFilterOp && operation.Kind != PhysicalDerivedLetOp && operation.Kind != PhysicalExpressionLetOp {
			return fmt.Errorf("path scope operation %d has unsupported kind %q", index, operation.Kind)
		}
		if err := operation.validatePayload(); err != nil {
			return err
		}
		switch operation.Kind {
		case PhysicalFilterOp:
			if err := validatePhysicalFilter(*operation.Filter, definedScope, binds); err != nil {
				return err
			}
		case PhysicalDerivedLetOp:
			if err := validatePhysicalDerivedLet(*operation.DerivedLet, definedScope, binds); err != nil {
				return err
			}
			if err := definePhysicalVariable(definedScope, operation.DerivedLet.Variable); err != nil {
				return err
			}
		case PhysicalExpressionLetOp:
			if err := validatePhysicalExpression(operation.ExpressionLet.Expression, definedScope, binds); err != nil {
				return err
			}
			if err := definePhysicalVariable(definedScope, operation.ExpressionLet.Variable); err != nil {
				return err
			}
		}
	}
	return validatePhysicalTraversalStrategy(extend.Traversal)
}

func validatePhysicalGraphReturn(graph PhysicalGraphReturn, defined map[string]bool, binds map[string]any) error {
	if len(graph.PathSets) == 0 {
		return fmt.Errorf("graph return requires at least one path set")
	}
	seen := map[string]bool{}
	for _, set := range graph.PathSets {
		if !physicalVariablePattern.MatchString(set) || seen[set] {
			return fmt.Errorf("invalid or duplicate path set %q", set)
		}
		seen[set] = true
		if !defined[set] {
			return fmt.Errorf("path set %q is out of scope", set)
		}
	}
	if err := requireBind(binds, graph.LimitBindKey); err != nil {
		return err
	}
	limit, ok := binds[graph.LimitBindKey].(int)
	if !ok || limit <= 0 {
		return fmt.Errorf("graph limit bind %q must be a positive int", graph.LimitBindKey)
	}
	return nil
}

func validatePhysicalTraversalStrategy(traversal PhysicalTraversal) error {
	strategy := traversal.Strategy
	if strategy == "" || strategy == PhysicalTraversalNative {
		return nil
	}
	if strategy != PhysicalTraversalEndpointLookup {
		return fmt.Errorf("unsupported traversal strategy %q", strategy)
	}
	if traversal.Direction != PhysicalInbound && traversal.Direction != PhysicalOutbound {
		return fmt.Errorf("endpoint lookup requires INBOUND or OUTBOUND direction")
	}
	if !physicalPathPartPattern.MatchString(traversal.EndpointField) || !physicalPathPartPattern.MatchString(traversal.EndpointJoinField) {
		return fmt.Errorf("endpoint lookup requires safe endpoint and join fields")
	}
	if len(traversal.EndpointIndexFields) == 0 {
		return fmt.Errorf("endpoint lookup requires declared compound index fields")
	}
	for _, field := range traversal.EndpointIndexFields {
		if !physicalPathPartPattern.MatchString(field) {
			return fmt.Errorf("endpoint lookup has unsafe index field %q", field)
		}
	}
	return nil
}

func validatePhysicalSet(set PhysicalSet, parent map[string]bool, bindVars map[string]any) error {
	if set.Projection != nil {
		if len(set.Projection.Fields) == 0 {
			return fmt.Errorf("set %q projection requires at least one field", set.Variable)
		}
		seenProjectionFields := map[string]bool{}
		for _, field := range set.Projection.Fields {
			if !physicalVariablePattern.MatchString(field.Name) || seenProjectionFields[field.Name] {
				return fmt.Errorf("set %q projection field %q is unsafe or duplicated", set.Variable, field.Name)
			}
			seenProjectionFields[field.Name] = true
			if !schemaDefinitionExists(field.ResourceType) {
				return fmt.Errorf("set %q projection field %q has invalid resource type %q", set.Variable, field.Name, field.ResourceType)
			}
			if err := validatePhysicalSelector(field.ResourceType, field.Selector); err != nil {
				return fmt.Errorf("set %q projection field %q selector: %w", set.Variable, field.Name, err)
			}
			switch field.Demand {
			case PhysicalSelectorAllValues, PhysicalSelectorFirstValue:
			default:
				return fmt.Errorf("set %q projection field %q has unsupported value demand %q", set.Variable, field.Name, field.Demand)
			}
		}
	}
	if set.Output != nil {
		if len(set.Output.Fields) == 0 {
			return fmt.Errorf("set %q compact output requires at least one retained field", set.Variable)
		}
		seenOutputFields := map[PhysicalSetOutputField]bool{}
		for _, field := range set.Output.Fields {
			switch field {
			case PhysicalSetGraphIDField, PhysicalSetKeyField, PhysicalSetLogicalKeyField, PhysicalSetIDField, PhysicalSetResourceTypeField, PhysicalSetPayloadField:
			default:
				return fmt.Errorf("set %q compact output field %q is unsupported", set.Variable, field)
			}
			if seenOutputFields[field] {
				return fmt.Errorf("set %q compact output field %q is duplicated", set.Variable, field)
			}
			seenOutputFields[field] = true
		}
		if !seenOutputFields[PhysicalSetGraphIDField] || !seenOutputFields[PhysicalSetKeyField] {
			return fmt.Errorf("set %q compact output must retain _id and _key", set.Variable)
		}
	}
	if set.Reduction != nil {
		if !physicalVariablePattern.MatchString(set.Reduction.Variable) || set.Reduction.Variable == set.Variable {
			return fmt.Errorf("set %q reduction variable %q is unsafe or shadows the set", set.Variable, set.Reduction.Variable)
		}
		if !physicalVariablePattern.MatchString(set.Reduction.SourceSetVariable) || set.Reduction.SourceSetVariable != set.Variable {
			return fmt.Errorf("set %q reduction source %q must equal the owning set", set.Variable, set.Reduction.SourceSetVariable)
		}
		if set.Projection == nil {
			return fmt.Errorf("set %q reduction requires a selector projection", set.Variable)
		}
		if len(set.Reduction.Fields) == 0 {
			return fmt.Errorf("set %q reduction requires at least one field", set.Variable)
		}
		projectedFields := make(map[string]bool, len(set.Projection.Fields))
		for _, field := range set.Projection.Fields {
			projectedFields[field.Name] = true
		}
		seen := make(map[string]bool, len(set.Reduction.Fields))
		for _, field := range set.Reduction.Fields {
			if !physicalVariablePattern.MatchString(field.Name) || seen[field.Name] {
				return fmt.Errorf("set %q reduction field %q is unsafe or duplicated", set.Variable, field.Name)
			}
			seen[field.Name] = true
			if !physicalVariablePattern.MatchString(field.SourceField) || !projectedFields[field.SourceField] {
				return fmt.Errorf("set %q reduction source field %q is not a projected selector slot", set.Variable, field.SourceField)
			}
			switch field.Mode {
			case PhysicalSetReductionFirst, PhysicalSetReductionAll, PhysicalSetReductionDistinct:
			default:
				return fmt.Errorf("set %q reduction field %q has unsupported mode %q", set.Variable, field.Name, field.Mode)
			}
		}
	}
	if set.Prepared != nil {
		prepared := set.Prepared
		if !physicalVariablePattern.MatchString(prepared.Variable) || !physicalVariablePattern.MatchString(prepared.SourceSetVariable) {
			return fmt.Errorf("prepared set variables must be safe")
		}
		if prepared.SourceSetVariable != set.Variable {
			return fmt.Errorf("prepared set source %q must equal owning set %q", prepared.SourceSetVariable, set.Variable)
		}
		if len(prepared.Fields) == 0 {
			return fmt.Errorf("prepared set %q requires at least one field", prepared.Variable)
		}
		seen := map[string]bool{}
		for _, field := range prepared.Fields {
			if !physicalVariablePattern.MatchString(field.Name) || seen[field.Name] {
				return fmt.Errorf("prepared set field %q is unsafe or duplicated", field.Name)
			}
			seen[field.Name] = true
			if !schemaDefinitionExists(field.ResourceType) {
				return fmt.Errorf("prepared set field %q has invalid resource type %q", field.Name, field.ResourceType)
			}
			if err := validatePhysicalSelector(field.ResourceType, field.Selector); err != nil {
				return fmt.Errorf("prepared set field %q selector: %w", field.Name, err)
			}
		}
	}
	if set.SourceSetVariable == "" {
		return validatePhysicalSubplan(set.Subplan, parent, bindVars)
	}
	if !physicalVariablePattern.MatchString(set.ItemVariable) {
		return fmt.Errorf("shared subset %q has unsafe item variable", set.ItemVariable)
	}
	if !parent[set.SourceSetVariable] {
		return fmt.Errorf("shared subset source %q is out of scope", set.SourceSetVariable)
	}
	if len(set.Subplan.Captures) != 1 || set.Subplan.Captures[0] != set.SourceSetVariable {
		return fmt.Errorf("shared subset %q must capture exactly its source set", set.Variable)
	}
	defined := map[string]bool{set.SourceSetVariable: true, set.ItemVariable: true}
	for index, operation := range set.Subplan.Operations {
		if operation.Kind != PhysicalFilterOp && operation.Kind != PhysicalDerivedLetOp {
			return fmt.Errorf("shared subset operation %d has unsupported kind %q", index, operation.Kind)
		}
		if operation.Kind == PhysicalFilterOp {
			if err := validatePhysicalFilter(*operation.Filter, defined, bindVars); err != nil {
				return err
			}
		} else {
			if err := validatePhysicalDerivedLet(*operation.DerivedLet, defined, bindVars); err != nil {
				return err
			}
			if err := definePhysicalVariable(defined, operation.DerivedLet.Variable); err != nil {
				return err
			}
		}
	}
	return validatePhysicalExpression(set.Subplan.Return, defined, bindVars)
}

func validateAndDefinePhysicalTraversal(traversal PhysicalTraversal, defined map[string]bool, bindVars map[string]any) error {
	if !defined[traversal.SourceVariable] {
		return fmt.Errorf("traversal source variable %q is out of scope", traversal.SourceVariable)
	}
	if traversal.Direction != PhysicalOutbound && traversal.Direction != PhysicalInbound && traversal.Direction != PhysicalAny {
		return fmt.Errorf("invalid traversal direction %q", traversal.Direction)
	}
	if traversal.EdgeTargetTypeField != "" && !physicalPathPartPattern.MatchString(traversal.EdgeTargetTypeField) {
		return fmt.Errorf("unsafe traversal edge type field %q", traversal.EdgeTargetTypeField)
	}
	if err := validatePhysicalTraversalStrategy(traversal); err != nil {
		return err
	}
	for _, key := range []string{traversal.EdgeCollectionBindKey, traversal.EdgeLabelBindKey, traversal.TargetTypeBindKey} {
		if key != "" {
			if err := requireBind(bindVars, key); err != nil {
				return err
			}
		}
	}
	if traversal.EdgeCollectionBindKey == "" {
		return fmt.Errorf("traversal edge collection bind key is required")
	}
	if err := requireCollectionBind(bindVars, traversal.EdgeCollectionBindKey); err != nil {
		return err
	}
	if err := definePhysicalVariable(defined, traversal.TargetVariable); err != nil {
		return err
	}
	if traversal.EdgeVariable != "" {
		if err := definePhysicalVariable(defined, traversal.EdgeVariable); err != nil {
			return err
		}
	}
	return nil
}

func validatePhysicalUnnest(unnest PhysicalUnnest, defined map[string]bool, bindVars map[string]any) error {
	owner := unnest.Owner
	if !physicalVariablePattern.MatchString(owner.RootVariable) || !defined[owner.RootVariable] {
		return fmt.Errorf("unnest root variable %q is unsafe or out of scope", owner.RootVariable)
	}
	if !schemaDefinitionExists(owner.ResourceType) {
		return fmt.Errorf("unnest owner has invalid resource type %q", owner.ResourceType)
	}
	routeScope := cloneDefinedPhysicalVariables(defined)
	previousVariable := owner.RootVariable
	for index, step := range owner.Route {
		if strings.TrimSpace(step.OccurrenceID) == "" {
			return fmt.Errorf("unnest owner route step %d is missing an occurrence ID", index)
		}
		if step.Traversal.SourceVariable != previousVariable {
			return fmt.Errorf("unnest owner route step %d starts at %q, want %q", index, step.Traversal.SourceVariable, previousVariable)
		}
		if err := validateAndDefinePhysicalTraversal(step.Traversal, routeScope, bindVars); err != nil {
			return fmt.Errorf("unnest owner route step %d: %w", index, err)
		}
		for scopeIndex, operation := range step.Scope {
			if err := validatePhysicalUnnestRouteScopeOperation(operation, routeScope, bindVars); err != nil {
				return fmt.Errorf("unnest owner route step %d scope operation %d: %w", index, scopeIndex, err)
			}
		}
		previousVariable = step.Traversal.TargetVariable
	}
	if !physicalVariablePattern.MatchString(owner.OwnerVariable) {
		return fmt.Errorf("unnest owner variable %q is unsafe", owner.OwnerVariable)
	}
	if owner.OwnerVariable != previousVariable {
		return fmt.Errorf("unnest owner variable %q does not match route terminal %q", owner.OwnerVariable, previousVariable)
	}
	if len(owner.Route) > 0 {
		last := owner.Route[len(owner.Route)-1]
		if owner.OccurrenceID != last.OccurrenceID {
			return fmt.Errorf("unnest owner occurrence %q does not match route terminal %q", owner.OccurrenceID, last.OccurrenceID)
		}
		if targetType, ok := bindVars[last.Traversal.TargetTypeBindKey].(string); !ok || targetType != owner.ResourceType {
			return fmt.Errorf("unnest owner resource type %q does not match route terminal type", owner.ResourceType)
		}
	}
	if !physicalVariablePattern.MatchString(unnest.OutputVariable) || !physicalVariablePattern.MatchString(unnest.HasItemVariable) {
		return fmt.Errorf("unnest output and item discriminator variables must be safe")
	}
	if unnest.Ordinality != "" && !physicalVariablePattern.MatchString(unnest.Ordinality) {
		return fmt.Errorf("unnest ordinality variable %q is unsafe", unnest.Ordinality)
	}
	switch unnest.EmptyPolicy {
	case PhysicalUnnestError, PhysicalUnnestExclude, PhysicalUnnestPreserveParent:
	default:
		return fmt.Errorf("unsupported unnest empty policy %q", unnest.EmptyPolicy)
	}
	if unnest.Expression.Cardinality != PhysicalArrayCardinality {
		return fmt.Errorf("unnest source expression must be array-valued, got %q", unnest.Expression.Cardinality)
	}
	if len(unnest.Ancestors) > 0 {
		if unnest.Expression.Extract == nil {
			return fmt.Errorf("unnest ancestry requires an extract source")
		}
		seenAncestorSteps := map[int]bool{}
		for index, ancestor := range unnest.Ancestors {
			steps := unnest.Expression.Extract.Selector.Steps
			if !physicalVariablePattern.MatchString(ancestor.Variable) {
				return fmt.Errorf("unnest ancestor %d variable %q is unsafe", index, ancestor.Variable)
			}
			if ancestor.StepIndex < 0 || ancestor.StepIndex >= len(steps)-1 || !steps[ancestor.StepIndex].Iterate {
				return fmt.Errorf("unnest ancestor %d does not identify a repeated prefix selector step", index)
			}
			if seenAncestorSteps[ancestor.StepIndex] {
				return fmt.Errorf("unnest repeats ancestor selector step %d", ancestor.StepIndex)
			}
			seenAncestorSteps[ancestor.StepIndex] = true
		}
	}
	if unnest.Expression.Extract != nil && len(owner.Route) > 0 {
		if unnest.Expression.Extract.Source.Variable != owner.OwnerVariable || unnest.Expression.Extract.ResourceType != owner.ResourceType {
			return fmt.Errorf("unnest source expression is not bound to its exact owner occurrence")
		}
	}
	if err := validatePhysicalExpression(unnest.Expression, routeScope, bindVars); err != nil {
		return fmt.Errorf("unnest source expression: %w", err)
	}
	if err := definePhysicalUnnestOutputs(unnest, routeScope); err != nil {
		return fmt.Errorf("unnest output bindings: %w", err)
	}
	return nil
}

func validatePhysicalUnnestRouteScopeOperation(operation PhysicalOperation, defined map[string]bool, bindVars map[string]any) error {
	if err := operation.validatePayload(); err != nil {
		return err
	}
	switch operation.Kind {
	case PhysicalFilterOp:
		return validatePhysicalFilter(*operation.Filter, defined, bindVars)
	case PhysicalDerivedLetOp:
		if err := validatePhysicalDerivedLet(*operation.DerivedLet, defined, bindVars); err != nil {
			return err
		}
		return definePhysicalVariable(defined, operation.DerivedLet.Variable)
	case PhysicalExpressionLetOp:
		if err := validatePhysicalExpression(operation.ExpressionLet.Expression, defined, bindVars); err != nil {
			return err
		}
		return definePhysicalVariable(defined, operation.ExpressionLet.Variable)
	default:
		return fmt.Errorf("unsupported route scope operation %q", operation.Kind)
	}
}

func definePhysicalUnnestVariables(unnest PhysicalUnnest, defined map[string]bool) error {
	for _, step := range unnest.Owner.Route {
		if err := definePhysicalVariable(defined, step.Traversal.TargetVariable); err != nil {
			return err
		}
		if step.Traversal.EdgeVariable != "" {
			if err := definePhysicalVariable(defined, step.Traversal.EdgeVariable); err != nil {
				return err
			}
		}
		for _, operation := range step.Scope {
			switch operation.Kind {
			case PhysicalDerivedLetOp:
				if err := definePhysicalVariable(defined, operation.DerivedLet.Variable); err != nil {
					return err
				}
			case PhysicalExpressionLetOp:
				if err := definePhysicalVariable(defined, operation.ExpressionLet.Variable); err != nil {
					return err
				}
			}
		}
	}
	return definePhysicalUnnestOutputs(unnest, defined)
}

func definePhysicalUnnestOutputs(unnest PhysicalUnnest, defined map[string]bool) error {
	for _, variable := range []string{unnest.OutputVariable, unnest.HasItemVariable, unnest.Ordinality} {
		if variable == "" {
			continue
		}
		if err := definePhysicalVariable(defined, variable); err != nil {
			return err
		}
	}
	for _, ancestor := range unnest.Ancestors {
		if err := definePhysicalVariable(defined, ancestor.Variable); err != nil {
			return err
		}
	}
	return nil
}

func cloneDefinedPhysicalVariables(defined map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(defined))
	for variable, present := range defined {
		copy[variable] = present
	}
	return copy
}

func definePhysicalVariable(defined map[string]bool, variable string) error {
	if !physicalVariablePattern.MatchString(variable) {
		return fmt.Errorf("unsafe variable name %q", variable)
	}
	if defined[variable] {
		return fmt.Errorf("variable %q is already defined", variable)
	}
	defined[variable] = true
	return nil
}

func requireBind(bindVars map[string]any, key string) error {
	if !physicalBindKeyPattern.MatchString(key) {
		return fmt.Errorf("unsafe bind key %q", key)
	}
	if _, ok := bindVars[key]; !ok {
		return fmt.Errorf("bind key %q is not defined", key)
	}
	return nil
}

func requireCollectionBind(bindVars map[string]any, key string) error {
	value, ok := bindVars[key]
	if !ok {
		return fmt.Errorf("bind key %q is not defined", key)
	}
	collection, ok := value.(string)
	if !ok || strings.TrimSpace(collection) == "" {
		return fmt.Errorf("collection bind key %q must have a non-empty string value", key)
	}
	return nil
}

func validatePhysicalPredicate(predicate PhysicalPredicate, defined map[string]bool, bindVars map[string]any) error {
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	switch operator {
	case "EQUALS", "NOT_EQUALS", "IN", "EXISTS", "MISSING", "CONTAINS_TEXT", "GT", "GTE", "LT", "LTE":
	default:
		return fmt.Errorf("unknown physical filter operator %q", predicate.Operator)
	}
	if predicate.Correlation != nil {
		if predicate.Left.Variable != "" || predicate.Left.BindKey != "" || len(predicate.Left.Path) != 0 || predicate.LeftExpression != nil || predicate.Right != nil {
			return fmt.Errorf("correlated predicate cannot also declare ordinary left/right values")
		}
		if operator != "EQUALS" && operator != "EXISTS" {
			return fmt.Errorf("correlated predicate operator %q is unsupported", predicate.Operator)
		}
		return validatePhysicalCorrelation(*predicate.Correlation, defined, bindVars)
	}
	hasLeftValue := predicate.Left.Variable != "" || predicate.Left.BindKey != "" || len(predicate.Left.Path) != 0
	hasLeftExpression := predicate.LeftExpression != nil
	if hasLeftValue == hasLeftExpression {
		return fmt.Errorf("physical filter predicate requires exactly one left value or expression")
	}
	if hasLeftExpression {
		if err := validatePhysicalExpression(*predicate.LeftExpression, defined, bindVars); err != nil {
			return fmt.Errorf("physical filter predicate left expression: %w", err)
		}
		if predicate.LeftExpression.Cardinality != PhysicalArrayCardinality {
			return fmt.Errorf("physical filter predicate left expression must be array-valued")
		}
		if operator != "EXISTS" && operator != "MISSING" && !predicate.ValueKind.Valid() {
			return fmt.Errorf("physical filter predicate value kind %q is invalid", predicate.ValueKind)
		}
		if predicate.Quantifier != "" && !predicate.Quantifier.Valid() {
			return fmt.Errorf("physical filter predicate quantifier %q is invalid", predicate.Quantifier)
		}
	} else if err := validatePhysicalValue(predicate.Left, defined, bindVars); err != nil {
		return err
	}
	requiresRight := operator != "EXISTS" && operator != "MISSING"
	if requiresRight != (predicate.Right != nil) {
		return fmt.Errorf("physical filter operator %s right value presence is invalid", operator)
	}
	if predicate.Right != nil {
		if err := validatePhysicalValue(*predicate.Right, defined, bindVars); err != nil {
			return err
		}
	}
	return nil
}

func validatePhysicalFilter(filter PhysicalFilter, defined map[string]bool, bindVars map[string]any) error {
	legacy := strings.TrimSpace(filter.Predicate.Operator) != ""
	rich := filter.Expression != nil
	if legacy == rich {
		return fmt.Errorf("filter requires exactly one legacy predicate or predicate expression")
	}
	if legacy {
		if filter.Predicate.LeftExpression == nil {
			switch strings.ToUpper(strings.TrimSpace(filter.Predicate.Operator)) {
			case "EQUALS", "NOT_EQUALS", "IN", "EXISTS", "MISSING", "CONTAINS_TEXT", "GT", "GTE", "LT", "LTE":
			default:
				return fmt.Errorf("unsupported physical filter operator %q in legacy predicate", filter.Predicate.Operator)
			}
		}
		return validatePhysicalPredicate(filter.Predicate, defined, bindVars)
	}
	return validatePhysicalPredicateExpression(*filter.Expression, defined, bindVars)
}

func validatePhysicalDerivedLet(derived PhysicalDerivedLet, defined map[string]bool, bindVars map[string]any) error {
	if strings.TrimSpace(derived.Operator) == "" {
		return fmt.Errorf("derived LET operator is required")
	}
	for _, input := range derived.Inputs {
		if err := validatePhysicalValue(input, defined, bindVars); err != nil {
			return err
		}
	}
	return nil
}

func validatePhysicalProjection(projection PhysicalProjection, defined map[string]bool, bindVars map[string]any) error {
	hasValue := projection.Value.Variable != "" || projection.Value.BindKey != "" || len(projection.Value.Path) != 0
	hasExpression := projection.Expression != nil
	if projection.PresenceOutput {
		if projection.Presence == nil || hasValue || hasExpression {
			return fmt.Errorf("presence output requires only a typed presence proof")
		}
	} else if hasValue == hasExpression {
		return fmt.Errorf("projection requires exactly one value or expression")
	}
	if projection.Presence != nil {
		if err := validatePhysicalProjectionPresence(*projection.Presence, defined, bindVars); err != nil {
			return fmt.Errorf("projection presence: %w", err)
		}
	}
	if projection.PresenceOutput {
		return nil
	}
	if hasExpression {
		return validatePhysicalExpression(*projection.Expression, defined, bindVars)
	}
	return validatePhysicalValue(projection.Value, defined, bindVars)
}

func validatePhysicalPopulationMappingReturn(terminal PhysicalPopulationMappingReturn, defined map[string]bool, bindVars map[string]any) error {
	if terminal.Members.Cardinality != PhysicalArrayCardinality || terminal.Members.NullBehavior != PhysicalEmptyOnNull {
		return fmt.Errorf("mapping members must be an EMPTY_ON_NULL array expression")
	}
	if err := validatePhysicalExpression(terminal.Members, defined, bindVars); err != nil {
		return fmt.Errorf("mapping members: %w", err)
	}
	if len(terminal.IdentityParts) == 0 {
		return fmt.Errorf("mapping identity requires at least one ordered identity part")
	}
	seen := make(map[string]struct{}, len(terminal.IdentityParts))
	for index, part := range terminal.IdentityParts {
		if strings.TrimSpace(part.Name) == "" {
			return fmt.Errorf("mapping identity part %d has an empty name", index)
		}
		if _, exists := seen[part.Name]; exists {
			return fmt.Errorf("mapping identity part %q is duplicated", part.Name)
		}
		seen[part.Name] = struct{}{}
		if part.Expression.Cardinality != PhysicalScalarCardinality {
			return fmt.Errorf("mapping identity part %q must be scalar", part.Name)
		}
		if err := validatePhysicalExpression(part.Expression, defined, bindVars); err != nil {
			return fmt.Errorf("mapping identity part %q: %w", part.Name, err)
		}
	}
	if terminal.ExplicitIdentity != nil {
		if terminal.ExplicitIdentity.Cardinality != PhysicalScalarCardinality {
			return fmt.Errorf("mapping explicit identity must be scalar")
		}
		if err := validatePhysicalExpression(*terminal.ExplicitIdentity, defined, bindVars); err != nil {
			return fmt.Errorf("mapping explicit identity: %w", err)
		}
	}
	return nil
}

func validatePhysicalCellTraceReturn(terminal PhysicalCellTraceReturn, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalExpression(terminal.Value, defined, bindVars); err != nil {
		return fmt.Errorf("trace value: %w", err)
	}
	if terminal.Contribution != nil {
		if !defined[terminal.Contribution.SetVariable] {
			return fmt.Errorf("trace contribution set %q is not defined", terminal.Contribution.SetVariable)
		}
		if !physicalPathPartPattern.MatchString(terminal.Contribution.ValueField) {
			return fmt.Errorf("trace contribution field %q is invalid", terminal.Contribution.ValueField)
		}
	}
	if terminal.Reshape != nil {
		if !defined[terminal.Reshape.OutputVariable] {
			return fmt.Errorf("trace reshape output %q is not defined", terminal.Reshape.OutputVariable)
		}
		for index, source := range terminal.Reshape.Sources {
			switch source.Kind {
			case PhysicalCellTracePivotGroupKey, PhysicalCellTracePivotCell:
				if source.OneInputRowPerGroup {
					if !defined[source.InputRowVariable] {
						return fmt.Errorf("trace reshape source %d input row %q is not defined", index, source.InputRowVariable)
					}
				} else if !defined[source.GroupRowsVariable] {
					return fmt.Errorf("trace reshape source %d group rows %q are not defined", index, source.GroupRowsVariable)
				}
				if strings.TrimSpace(source.SourceColumn) == "" || source.OmissionCode == "" && !physicalPathPartPattern.MatchString(source.SourcePresenceField) {
					return fmt.Errorf("trace reshape source %d requires a source column and compiler-owned presence field", index)
				}
				if source.Kind == PhysicalCellTracePivotCell && source.Category == nil {
					return fmt.Errorf("trace pivot-cell source %d requires its frozen category", index)
				}
			case PhysicalCellTraceUnpivotValue, PhysicalCellTraceUnpivotField:
				if strings.TrimSpace(source.SourceColumn) == "" {
					return fmt.Errorf("trace unpivot source %d requires a source column", index)
				}
			default:
				return fmt.Errorf("trace reshape source %d has unsupported kind %q", index, source.Kind)
			}
		}
	}
	if strings.TrimSpace(terminal.OffsetBindKey) == "" || strings.TrimSpace(terminal.LimitBindKey) == "" || strings.TrimSpace(terminal.FetchLimitBindKey) == "" {
		return fmt.Errorf("trace offset, limit, and fetch-limit binds are required")
	}
	offset, ok := bindVars[terminal.OffsetBindKey].(int)
	if !ok || offset < 0 {
		return fmt.Errorf("trace offset bind %q must be a non-negative int", terminal.OffsetBindKey)
	}
	limit, ok := bindVars[terminal.LimitBindKey].(int)
	if !ok || limit <= 0 {
		return fmt.Errorf("trace limit bind %q must be a positive int", terminal.LimitBindKey)
	}
	fetchLimit, ok := bindVars[terminal.FetchLimitBindKey].(int)
	if !ok || fetchLimit != limit+1 {
		return fmt.Errorf("trace fetch-limit bind %q must be limit plus one", terminal.FetchLimitBindKey)
	}
	if len(terminal.IdentityParts) == 0 {
		return fmt.Errorf("trace identity requires at least one ordered identity part")
	}
	for index, part := range terminal.IdentityParts {
		if strings.TrimSpace(part.Name) == "" || part.Expression.Cardinality != PhysicalScalarCardinality {
			return fmt.Errorf("trace identity part %d must have a name and scalar expression", index)
		}
		if err := validatePhysicalExpression(part.Expression, defined, bindVars); err != nil {
			return fmt.Errorf("trace identity part %q: %w", part.Name, err)
		}
	}
	if terminal.ExplicitIdentity != nil {
		if terminal.ExplicitIdentity.Cardinality != PhysicalScalarCardinality {
			return fmt.Errorf("trace explicit identity must be scalar")
		}
		if err := validatePhysicalExpression(*terminal.ExplicitIdentity, defined, bindVars); err != nil {
			return fmt.Errorf("trace explicit identity: %w", err)
		}
	}
	return nil
}

func validatePhysicalExpression(expression PhysicalExpression, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalExpressionObjectCycles(expression); err != nil {
		return err
	}
	if !expression.Cardinality.valid() {
		return fmt.Errorf("expression has invalid cardinality %q", expression.Cardinality)
	}
	if !expression.NullBehavior.valid() {
		return fmt.Errorf("expression has invalid null behavior %q", expression.NullBehavior)
	}
	payloads := 0
	if expression.Value != nil {
		payloads++
	}
	if expression.Literal != nil {
		payloads++
	}
	if expression.Extract != nil {
		payloads++
	}
	if expression.Aggregate != nil {
		payloads++
	}
	if expression.Pivot != nil {
		payloads++
	}
	if expression.OwnerRecords != nil {
		payloads++
	}
	if expression.Slice != nil {
		payloads++
	}
	if expression.ObjectLookup != nil {
		payloads++
	}
	if expression.KeyedMap != nil {
		payloads++
	}
	if expression.ObjectKeys != nil {
		payloads++
	}
	if expression.KeySet != nil {
		payloads++
	}
	if expression.Object != nil {
		payloads++
	}
	if expression.Subplan != nil {
		payloads++
	}
	if expression.Call != nil {
		payloads++
	}
	if expression.RelatedField != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("expression must contain exactly one payload")
	}
	switch expression.Kind {
	case PhysicalValueExpression:
		if expression.Value == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalValue(*expression.Value, defined, bindVars)
	case PhysicalLiteralExpression:
		if expression.Literal == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		if err := requireBind(bindVars, expression.Literal.BindKey); err != nil {
			return err
		}
		return nil
	case PhysicalExtractExpression:
		if expression.Extract == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalExtract(*expression.Extract, defined, bindVars)
	case PhysicalAggregateExpression:
		if expression.Aggregate == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalAggregate(*expression.Aggregate, defined, bindVars)
	case PhysicalPivotExpression:
		if expression.Pivot == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalPivot(*expression.Pivot, defined, bindVars)
	case PhysicalOwnerRecordsExpression:
		if expression.OwnerRecords == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		if expression.OwnerRecords.Correlation.ValueScope == fhirbinding.CorrelatedValueScopeCoding {
			return fmt.Errorf("owner records does not support Coding value scope")
		}
		if err := validatePhysicalCorrelation(expression.OwnerRecords.Correlation, defined, bindVars); err != nil {
			return fmt.Errorf("owner records correlation: %w", err)
		}
		for _, key := range []string{expression.OwnerRecords.OwnerPathBindKey, expression.OwnerRecords.ChoiceArmBindKey, expression.OwnerRecords.LogicalTypeBindKey} {
			if err := requireBind(bindVars, key); err != nil {
				return err
			}
			if _, ok := bindVars[key].(string); !ok {
				return fmt.Errorf("owner records bind %q must be a string", key)
			}
		}
		return nil
	case PhysicalSliceExpression:
		if expression.Slice == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalSlice(*expression.Slice, defined, bindVars)
	case PhysicalObjectLookupExpression:
		if expression.ObjectLookup == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		if !defined[expression.ObjectLookup.ObjectVariable] {
			return fmt.Errorf("object lookup references undefined variable %q", expression.ObjectLookup.ObjectVariable)
		}
		return requireBind(bindVars, expression.ObjectLookup.KeyBindKey)
	case PhysicalKeyedMapExpression:
		if expression.KeyedMap == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalKeyedMap(*expression.KeyedMap, defined, bindVars)
	case PhysicalObjectKeysExpression:
		if expression.ObjectKeys == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		if !defined[expression.ObjectKeys.ObjectVariable] {
			return fmt.Errorf("object keys references undefined variable %q", expression.ObjectKeys.ObjectVariable)
		}
		return nil
	case PhysicalKeySetExpression:
		if expression.KeySet == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalKeySet(*expression.KeySet, defined, bindVars)
	case PhysicalObjectExpression:
		if expression.Object == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalObject(*expression.Object, defined, bindVars)
	case PhysicalSubplanExpression:
		if expression.Subplan == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		if expression.Cardinality != PhysicalArrayCardinality {
			return fmt.Errorf("SUBPLAN expression must be array-valued, got %q", expression.Cardinality)
		}
		if expression.NullBehavior != PhysicalEmptyOnNull {
			return fmt.Errorf("SUBPLAN expression must use EMPTY_ON_NULL, got %q", expression.NullBehavior)
		}
		return validatePhysicalSubplan(*expression.Subplan, defined, bindVars)
	case PhysicalCallExpression:
		if expression.Call == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		if strings.EqualFold(strings.TrimSpace(expression.Call.Name), "distinct_present_one") &&
			(expression.Cardinality != PhysicalScalarCardinality || expression.NullBehavior != PhysicalPreserveNull) {
			return fmt.Errorf("distinct_present_one must produce a nullable scalar")
		}
		return validatePhysicalCall(*expression.Call, defined, bindVars)
	case PhysicalRelatedFieldExpression:
		if expression.RelatedField == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		if expression.Cardinality != PhysicalScalarCardinality || expression.NullBehavior != PhysicalPreserveNull {
			return fmt.Errorf("related field expression must be a nullable scalar")
		}
		if err := validatePhysicalValue(expression.RelatedField.DocumentID, defined, bindVars); err != nil {
			return fmt.Errorf("related field document ID: %w", err)
		}
		if expression.RelatedField.ResourceType == "" || len(expression.RelatedField.Path) == 0 || expression.RelatedField.Path[0] != "payload" {
			return fmt.Errorf("related field path is empty")
		}
		for index, segment := range expression.RelatedField.Path {
			if !spec.ValidFieldPathSegment(segment) {
				return fmt.Errorf("related field path segment %d is invalid", index)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown expression kind %q", expression.Kind)
	}
}

func validatePhysicalKeyedMap(keyed PhysicalKeyedMap, defined map[string]bool, bindVars map[string]any) error {
	if keyed.Source.Cardinality != PhysicalArrayCardinality {
		return fmt.Errorf("keyed map source expression must be array-valued, got %q", keyed.Source.Cardinality)
	}
	if !physicalVariablePattern.MatchString(keyed.ItemVariable) {
		return fmt.Errorf("keyed map item variable %q is unsafe", keyed.ItemVariable)
	}
	if keyed.Reduction != PhysicalMapFirst && keyed.Reduction != PhysicalMapFirstSorted && keyed.Reduction != PhysicalMapAll && keyed.Reduction != PhysicalMapDistinct {
		return fmt.Errorf("unsupported keyed map reduction %q", keyed.Reduction)
	}
	if err := validatePhysicalExpression(keyed.Source, defined, bindVars); err != nil {
		return fmt.Errorf("keyed map source: %w", err)
	}
	itemDefined := make(map[string]bool, len(defined)+1)
	for name, value := range defined {
		itemDefined[name] = value
	}
	itemDefined[keyed.ItemVariable] = true
	if err := validatePhysicalExpression(keyed.ItemKey, itemDefined, bindVars); err != nil {
		return fmt.Errorf("keyed map item key: %w", err)
	}
	if err := validatePhysicalExpression(keyed.ItemValue, itemDefined, bindVars); err != nil {
		return fmt.Errorf("keyed map item value: %w", err)
	}
	for index, fallback := range keyed.ValueFallbacks {
		if err := validatePhysicalExpression(fallback, itemDefined, bindVars); err != nil {
			return fmt.Errorf("keyed map fallback %d: %w", index, err)
		}
	}
	return nil
}

func validatePhysicalKeySet(keySet PhysicalKeySet, defined map[string]bool, bindVars map[string]any) error {
	if !physicalVariablePattern.MatchString(keySet.ItemVariable) {
		return fmt.Errorf("key set item variable %q is unsafe", keySet.ItemVariable)
	}
	if keySet.Source.Cardinality != PhysicalArrayCardinality {
		return fmt.Errorf("key set source expression must be array-valued, got %q", keySet.Source.Cardinality)
	}
	if err := validatePhysicalExpression(keySet.Source, defined, bindVars); err != nil {
		return fmt.Errorf("key set source: %w", err)
	}
	itemDefined := make(map[string]bool, len(defined)+1)
	for name, value := range defined {
		itemDefined[name] = value
	}
	itemDefined[keySet.ItemVariable] = true
	if err := validatePhysicalExpression(keySet.ItemKey, itemDefined, bindVars); err != nil {
		return fmt.Errorf("key set item key: %w", err)
	}
	return nil
}

func validatePhysicalCall(call PhysicalCall, defined map[string]bool, bindVars map[string]any) error {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name == "" {
		return fmt.Errorf("call name is required")
	}
	known := map[string]bool{
		"coalesce": true, "coalesce_string": true, "fallback": true, "first": true, "all": true, "distinct": true, "distinct_present_one": true, "length": true,
		"canonical_json": true,
		"concat":         true, "join": true, "cast": true, "reference_id": true,
		"path_segment": true, "basename": true, "last_segment": true,
		"sanitize_name": true, "sanitize_graphql_name": true, "uuid3": true, "uuid5": true,
		"if": true, "case": true, "not": true, "and": true, "or": true,
		"eq": true, "neq": true, "gt": true, "gte": true, "lt": true, "lte": true,
		"contains": true, "assert": true,
		"add": true, "subtract": true, "multiply": true, "divide": true,
	}
	if !known[name] {
		return fmt.Errorf("unsupported call %q", call.Name)
	}
	if name == "cast" {
		if len(call.Args) != 1 || strings.TrimSpace(call.TargetKind) == "" {
			return fmt.Errorf("cast requires one argument and a target kind")
		}
		switch strings.ToLower(strings.TrimSpace(call.TargetKind)) {
		case "string", "integer", "decimal", "boolean", "date", "date_time", "code", "uuid":
		default:
			return fmt.Errorf("cast target kind %q is unsupported", call.TargetKind)
		}
	} else if name == "distinct_present_one" {
		if len(call.Args) != 1 || call.Args[0].Cardinality != PhysicalArrayCardinality ||
			(call.TargetKind != "STRING" && call.TargetKind != "INTEGER" && call.TargetKind != "DECIMAL" && call.TargetKind != "BOOLEAN") {
			return fmt.Errorf("distinct_present_one requires one array argument and a table scalar target kind")
		}
	} else if name == "assert" && len(call.Args) != 2 {
		return fmt.Errorf("assert requires a condition and message")
	} else if (name == "add" || name == "subtract" || name == "multiply" || name == "divide") && len(call.Args) != 2 {
		return fmt.Errorf("%s requires two arguments", name)
	} else if name != "distinct_present_one" && call.TargetKind != "" {
		return fmt.Errorf("target kind is only valid for cast")
	}
	for index, arg := range call.Args {
		if err := validatePhysicalExpression(arg, defined, bindVars); err != nil {
			return fmt.Errorf("call %q argument %d: %w", name, index, err)
		}
	}
	return nil
}

func validatePhysicalExpressionObjectCycles(expression PhysicalExpression) error {
	active := map[*PhysicalObject]bool{}
	visited := map[*PhysicalObject]bool{}
	activeSubplans := map[*PhysicalSubplan]bool{}
	visitedSubplans := map[*PhysicalSubplan]bool{}
	var visitExpression func(PhysicalExpression) error
	var visitObject func(*PhysicalObject) error
	var visitPredicate func(*PhysicalPredicateExpression) error
	var visitSubplan func(*PhysicalSubplan) error
	visitExpression = func(current PhysicalExpression) error {
		if current.Object != nil {
			if err := visitObject(current.Object); err != nil {
				return err
			}
		}
		if current.Aggregate != nil && current.Aggregate.Value != nil {
			if err := visitExpression(*current.Aggregate.Value); err != nil {
				return err
			}
		}
		if current.Call != nil {
			for index := range current.Call.Args {
				if err := visitExpression(current.Call.Args[index]); err != nil {
					return err
				}
			}
		}
		if current.Aggregate != nil && current.Aggregate.Predicate != nil {
			if err := visitPredicate(current.Aggregate.Predicate); err != nil {
				return err
			}
		}
		if current.Slice != nil {
			if current.Slice.Predicate != nil {
				if err := visitPredicate(current.Slice.Predicate); err != nil {
					return err
				}
			}
			if current.Slice.Sort != nil {
				if err := visitExpression(*current.Slice.Sort); err != nil {
					return err
				}
			}
			for _, projection := range current.Slice.Projections {
				if err := visitExpression(projection.Expression); err != nil {
					return err
				}
			}
		}
		if current.KeyedMap != nil {
			if err := visitExpression(current.KeyedMap.Source); err != nil {
				return err
			}
			if err := visitExpression(current.KeyedMap.ItemKey); err != nil {
				return err
			}
			if err := visitExpression(current.KeyedMap.ItemValue); err != nil {
				return err
			}
			for _, fallback := range current.KeyedMap.ValueFallbacks {
				if err := visitExpression(fallback); err != nil {
					return err
				}
			}
		}
		if current.Subplan != nil {
			if err := visitSubplan(current.Subplan); err != nil {
				return err
			}
		}
		return nil
	}
	visitPredicate = func(predicate *PhysicalPredicateExpression) error {
		if predicate == nil {
			return nil
		}
		if predicate.Comparison != nil && predicate.Comparison.LeftExpression != nil {
			if err := visitExpression(*predicate.Comparison.LeftExpression); err != nil {
				return err
			}
		}
		for index := range predicate.Children {
			if err := visitPredicate(&predicate.Children[index]); err != nil {
				return err
			}
		}
		if predicate.Exists != nil {
			return visitSubplan(predicate.Exists)
		}
		return nil
	}
	visitSubplan = func(subplan *PhysicalSubplan) error {
		if subplan == nil {
			return nil
		}
		if activeSubplans[subplan] {
			return fmt.Errorf("physical subplan expression contains a recursive cycle")
		}
		if visitedSubplans[subplan] {
			return nil
		}
		activeSubplans[subplan] = true
		defer delete(activeSubplans, subplan)
		for index := range subplan.Operations {
			operation := &subplan.Operations[index]
			switch operation.Kind {
			case PhysicalFilterOp:
				if operation.Filter != nil && operation.Filter.Expression != nil {
					if err := visitPredicate(operation.Filter.Expression); err != nil {
						return err
					}
				}
			case PhysicalExpressionLetOp:
				if operation.ExpressionLet != nil {
					if err := visitExpression(operation.ExpressionLet.Expression); err != nil {
						return err
					}
				}
			case PhysicalSetOp:
				if operation.Set != nil {
					if err := visitSubplan(&operation.Set.Subplan); err != nil {
						return err
					}
				}
			case PhysicalUnnestOp:
				if operation.Unnest != nil {
					if err := visitExpression(operation.Unnest.Expression); err != nil {
						return err
					}
					for _, step := range operation.Unnest.Owner.Route {
						routeScope := PhysicalSubplan{Operations: step.Scope}
						if err := visitSubplan(&routeScope); err != nil {
							return err
						}
					}
				}
			case PhysicalPathExtendOp:
				if operation.PathExtend != nil {
					routeScope := PhysicalSubplan{Operations: operation.PathExtend.Scope}
					if err := visitSubplan(&routeScope); err != nil {
						return err
					}
				}
			}
		}
		if err := visitExpression(subplan.Return); err != nil {
			return err
		}
		visitedSubplans[subplan] = true
		return nil
	}
	visitObject = func(object *PhysicalObject) error {
		if active[object] {
			return fmt.Errorf("physical object expression contains a recursive cycle")
		}
		if visited[object] {
			return nil
		}
		active[object] = true
		for _, field := range object.Fields {
			if err := visitExpression(field.Expression); err != nil {
				return err
			}
		}
		delete(active, object)
		visited[object] = true
		return nil
	}
	return visitExpression(expression)
}

func validatePhysicalSlice(slice PhysicalSlice, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalValue(slice.Source, defined, bindVars); err != nil {
		return err
	}
	if slice.Predicate != nil {
		if err := validatePhysicalPredicateExpression(*slice.Predicate, defined, bindVars); err != nil {
			return fmt.Errorf("slice predicate: %w", err)
		}
	}
	if slice.Sort == nil {
		return fmt.Errorf("slice requires a stable sort expression")
	}
	if err := validatePhysicalExpression(*slice.Sort, defined, bindVars); err != nil {
		return fmt.Errorf("slice sort: %w", err)
	}
	if err := requireBind(bindVars, slice.LimitBindKey); err != nil {
		return err
	}
	limit, ok := bindVars[slice.LimitBindKey].(int)
	if !ok || limit <= 0 {
		return fmt.Errorf("slice limit bind %q must be a positive int", slice.LimitBindKey)
	}
	return validatePhysicalExpressionProjections(slice.Projections, defined, bindVars, "slice")
}

func validatePhysicalObject(object PhysicalObject, defined map[string]bool, bindVars map[string]any) error {
	return validatePhysicalExpressionProjections(object.Fields, defined, bindVars, "object")
}

func validatePhysicalExpressionProjections(projections []PhysicalExpressionProjection, defined map[string]bool, bindVars map[string]any, owner string) error {
	if len(projections) == 0 {
		return fmt.Errorf("%s requires at least one projection", owner)
	}
	seen := map[string]bool{}
	for _, projection := range projections {
		if strings.TrimSpace(projection.Name) == "" || seen[projection.Name] {
			return fmt.Errorf("%s projection name %q is empty or duplicated", owner, projection.Name)
		}
		seen[projection.Name] = true
		if err := validatePhysicalExpression(projection.Expression, defined, bindVars); err != nil {
			return fmt.Errorf("%s projection %q: %w", owner, projection.Name, err)
		}
	}
	return nil
}

func validatePhysicalPredicateExpression(predicate PhysicalPredicateExpression, defined map[string]bool, bindVars map[string]any) error {

	if err := validatePhysicalExpressionObjectCycles(PhysicalExpression{
		Aggregate: &PhysicalAggregate{Predicate: &predicate},
	}); err != nil {
		return err
	}
	return validatePhysicalPredicateExpressionValidated(predicate, defined, bindVars)
}

func validatePhysicalPredicateExpressionValidated(predicate PhysicalPredicateExpression, defined map[string]bool, bindVars map[string]any) error {
	switch predicate.Kind {
	case PhysicalComparisonPredicate:
		if predicate.Comparison == nil || len(predicate.Children) != 0 || predicate.Exists != nil {
			return fmt.Errorf("comparison predicate requires exactly one comparison")
		}
		return validatePhysicalPredicate(*predicate.Comparison, defined, bindVars)
	case PhysicalAllPredicate, PhysicalAnyPredicate:
		if predicate.Comparison != nil || predicate.Exists != nil || len(predicate.Children) == 0 {
			return fmt.Errorf("%s predicate requires one or more child predicates", predicate.Kind)
		}
		for index, child := range predicate.Children {
			if err := validatePhysicalPredicateExpressionValidated(child, defined, bindVars); err != nil {
				return fmt.Errorf("predicate child %d: %w", index, err)
			}
		}
		return nil
	case PhysicalNotPredicate:
		if predicate.Comparison != nil || predicate.Exists != nil || len(predicate.Children) != 1 {
			return fmt.Errorf("NOT predicate requires exactly one child predicate")
		}
		return validatePhysicalPredicateExpressionValidated(predicate.Children[0], defined, bindVars)
	case PhysicalExistsPredicate:
		if predicate.Comparison != nil || len(predicate.Children) != 0 || predicate.Exists == nil {
			return fmt.Errorf("EXISTS predicate requires exactly one subplan")
		}
		if predicate.Exists.Sort != nil || predicate.Exists.Unique {
			return fmt.Errorf("EXISTS subplan cannot use projection-only sort or unique modifiers")
		}
		return validatePhysicalSubplan(*predicate.Exists, defined, bindVars)
	default:
		return fmt.Errorf("unknown predicate kind %q", predicate.Kind)
	}
}

func validatePhysicalValue(value PhysicalValue, defined map[string]bool, bindVars map[string]any) error {
	hasVariable := value.Variable != ""
	hasBind := value.BindKey != ""
	if hasVariable == hasBind {
		return fmt.Errorf("physical value must reference exactly one variable or bind key")
	}
	if hasBind {
		if len(value.Path) != 0 {
			return fmt.Errorf("bind value %q cannot have a path", value.BindKey)
		}
		return requireBind(bindVars, value.BindKey)
	}
	if !defined[value.Variable] {
		return fmt.Errorf("variable %q is out of scope", value.Variable)
	}
	for _, part := range value.Path {
		if !physicalPathPartPattern.MatchString(part) {
			return fmt.Errorf("unsafe path segment %q", part)
		}
	}
	return nil
}
