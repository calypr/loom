package ir

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func validatePhysicalStageCohortGroup(stage PhysicalConstructionStage, cohort PhysicalStageCohortGroup, bindVars map[string]any) error {
	if err := validatePhysicalGroupRows(cohort.Rows, bindVars); err != nil {
		return err
	}
	if !physicalVariablePattern.MatchString(cohort.RootContributorVariable) {
		return fmt.Errorf("root contributor variable is unsafe")
	}
	if cohort.RootContributorOutputColumn != "__loom_root_contributor_keys" {
		return fmt.Errorf("root contributor output must use the compiler-owned contributor-set column")
	}
	resourceType, ok := bindVars[cohort.Rows.ResourceTypeBindKey].(string)
	if !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("root resource type is missing")
	}
	input, found := physicalStageColumnMap(stage.InputColumns)[cohort.ContributorInputColumn]
	if !found || !input.Internal || input.RootContributorResourceType != resourceType || input.Kind != "string" {
		return fmt.Errorf("contributor input %q is not a compiler-proven root identity", cohort.ContributorInputColumn)
	}
	if cohort.ContributorInputMany {
		if input.Name != cohort.RootContributorOutputColumn || input.Cardinality != "many" {
			return fmt.Errorf("contributor input %q is not a root contributor set", cohort.ContributorInputColumn)
		}
	} else if input.Name != "_key" || input.Cardinality != "required_one" {
		return fmt.Errorf("contributor input %q is not the direct root storage identity", cohort.ContributorInputColumn)
	}
	output, found := physicalStageColumnMap(stage.OutputColumns)[cohort.RootContributorOutputColumn]
	if !found || !output.Internal || output.Kind != "string" || output.Cardinality != "many" || output.RootContributorResourceType != resourceType {
		return fmt.Errorf("root contributor output %q has an invalid schema", cohort.RootContributorOutputColumn)
	}
	return nil
}

func validatePhysicalStageGroup(stage PhysicalConstructionStage, group PhysicalStageGroup, bindVars map[string]any, sourceIdentity string, sourceOperations []PhysicalOperation) error {
	if len(group.Keys) == 0 && group.SemanticKey == nil && len(group.Aggregates) == 0 && len(group.RowValues) == 0 {
		return fmt.Errorf("requires at least one key or aggregate")
	}
	if group.SemanticKey != nil && group.CodingKey != nil {
		return fmt.Errorf("Group cannot combine a semantic family key with CODING_IDENTITY")
	}
	if group.SemanticKey != nil {
		key := group.SemanticKey
		if stage.InputStageID != "source_projection" || key.OutputColumn == "" ||
			key.TupleVariable == "" || key.LabelVariable == "" || key.RootCollectionBindKey == "" ||
			key.ResourceType == "" || key.CodingPath == "" || len(key.PathSegments) == 0 ||
			key.BindingID == "" || key.RuleVersion == "" || key.FamilyID == "" || key.System == "" {
			return fmt.Errorf("semantic key requires a direct-root stage and exact resolved family identity")
		}
		if bindVars[key.RootCollectionBindKey] != key.ResourceType {
			return fmt.Errorf("semantic key root collection does not match resource type %q", key.ResourceType)
		}
	}
	switch group.MissingKeyPolicy {
	case PhysicalStageGroupMissingKeyGroup, PhysicalStageGroupMissingKeyExclude, PhysicalStageGroupMissingKeyError:
	default:
		return fmt.Errorf("missing-key policy %q is unsupported", group.MissingKeyPolicy)
	}
	if err := requireNonEmptyStringBind(bindVars, group.ConstructionIDBindKey); err != nil {
		return fmt.Errorf("construction ID: %w", err)
	}
	if !physicalVariablePattern.MatchString(group.GroupRowsVariable) || !physicalVariablePattern.MatchString(group.IdentityVariable) {
		return fmt.Errorf("group row and identity variables must be safe")
	}
	if err := validateStageProjectionNames(stage.InputProjections, stage.InputColumns); err != nil {
		return fmt.Errorf("input projections: %w", err)
	}
	defined := map[string]bool{stage.InputRowVariable: true}
	if group.RelatedOccurrence != nil {
		for _, variable := range []string{
			group.RelatedOccurrence.RelatedRecords.RelatedRecordsVariable,
			group.RelatedOccurrence.RelatedRecords.IndexVariable,
			group.RelatedOccurrence.RelatedRecords.ItemVariable,
			group.RelatedOccurrence.RouteHasItemVariable,
		} {
			if variable != "" {
				if err := definePhysicalVariable(defined, variable); err != nil {
					return fmt.Errorf("related route variable: %w", err)
				}
			}
		}
		if unnest := group.RelatedOccurrence.CodingUnnest; unnest != nil {
			for _, variable := range []string{unnest.OutputVariable, unnest.HasItemVariable, unnest.Ordinality} {
				if variable != "" {
					if err := definePhysicalVariable(defined, variable); err != nil {
						return fmt.Errorf("related Coding unnest variable: %w", err)
					}
				}
			}
		}
	}
	for index, projection := range stage.InputProjections {
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("input projection %d (%q): %w", index, projection.Name, err)
		}
	}
	if err := definePhysicalVariable(defined, group.GroupRowsVariable); err != nil {
		return err
	}
	inputByName, outputByName := physicalStageColumnMap(stage.InputColumns), physicalStageColumnMap(stage.OutputColumns)
	if err := validateRootContributorReduction(
		group.RootContributorInputColumn,
		group.RootContributorInputMany,
		group.RootContributorOutputColumn,
		group.RootContributorVariable,
		stage.InputColumns, stage.OutputColumns, defined,
	); err != nil {
		return err
	}
	if group.DynamicRuntimeKeysVariable != "" {
		input, inOK := inputByName["__loom_dynamic_runtime_keys"]
		output, outOK := outputByName["__loom_dynamic_runtime_keys"]
		if !inOK || !outOK || !input.Internal || !output.Internal || input.Kind != "object" || output.Kind != "object" || output.Identity || output.Cardinality != "many" {
			return fmt.Errorf("dynamic runtime inventories require hidden input and repeated hidden output")
		}
		found := false
		for _, projection := range stage.OutputProjections {
			if projection.Name == "__loom_dynamic_runtime_keys" {
				found = projection.Hidden && projection.Expression == nil && projection.Value.Variable == group.DynamicRuntimeKeysVariable && projection.Value.BindKey == "" && len(projection.Value.Path) == 0
			}
		}
		if !found {
			return fmt.Errorf("dynamic runtime inventories require the exact hidden Group projection")
		}
		if err := definePhysicalVariable(defined, group.DynamicRuntimeKeysVariable); err != nil {
			return fmt.Errorf("dynamic runtime inventories: %w", err)
		}
	}
	for index, key := range group.Keys {
		input, ok := inputByName[key.InputColumn]
		if !ok || input.Cardinality == "many" {
			return fmt.Errorf("key %d column %q is not a public scalar input", index, key.InputColumn)
		}
		if key.FieldSource != nil {
			if !key.InputInternal || !input.Internal || !validPhysicalGroupFieldSource(key.FieldSource, stage.InputStageID) {
				return fmt.Errorf("key %d hidden input column %q has no exact source-stage authority", index, key.InputColumn)
			}
		} else if key.InputInternal || input.Internal {
			return fmt.Errorf("key %d column %q is not a public scalar input", index, key.InputColumn)
		}
		kind, ok := physicalStageScalarKind(input.Kind)
		if !ok || kind != key.Kind {
			return fmt.Errorf("key %d column %q has unsupported or mismatched scalar kind %q", index, key.InputColumn, key.Kind)
		}
		output, ok := outputByName[key.OutputColumn]
		if !ok || output.Internal || output.Kind != input.Kind || output.Cardinality != input.Cardinality {
			return fmt.Errorf("key %d output column %q does not preserve its input type", index, key.OutputColumn)
		}
		if err := definePhysicalVariable(defined, key.Variable); err != nil {
			return fmt.Errorf("key %d: %w", index, err)
		}
	}
	if group.CodingKey != nil {
		if err := validatePhysicalStageCodingKey(stage, *group.CodingKey, outputByName, inputByName, defined, bindVars, sourceIdentity, sourceOperations); err != nil {
			return fmt.Errorf("CODING_IDENTITY key: %w", err)
		}
	}
	if group.SemanticKey != nil {
		semantic := group.SemanticKey
		if _, exists := outputByName[semantic.OutputColumn]; !exists {
			return fmt.Errorf("semantic key output column %q is missing", semantic.OutputColumn)
		}
		if err := definePhysicalVariable(defined, semantic.TupleVariable); err != nil {
			return fmt.Errorf("semantic key tuple: %w", err)
		}
		if err := definePhysicalVariable(defined, semantic.LabelVariable); err != nil {
			return fmt.Errorf("semantic key label: %w", err)
		}
	}
	for index, aggregate := range group.Aggregates {
		output, ok := outputByName[aggregate.Output]
		if !ok || output.Internal || output.Cardinality == "many" || output.Kind != aggregate.OutputKind {
			return fmt.Errorf("aggregate %d output column %q does not match its type", index, aggregate.Output)
		}
		needInput := aggregate.Operation != "COUNT_ROWS"
		if !needInput {
			if aggregate.InputColumn != "" || aggregate.InputKind != "" {
				return fmt.Errorf("aggregate %d COUNT_ROWS must not have an input column", index)
			}
		} else {
			input, ok := inputByName[aggregate.InputColumn]
			if !ok || input.Internal || input.Cardinality == "many" {
				return fmt.Errorf("aggregate %d input column %q is not a public scalar input", index, aggregate.InputColumn)
			}
			kind, ok := physicalStageScalarKind(input.Kind)
			if !ok || kind != aggregate.InputKind {
				return fmt.Errorf("aggregate %d input column %q has unsupported or mismatched scalar kind %q", index, aggregate.InputColumn, aggregate.InputKind)
			}
		}
		switch aggregate.Operation {
		case "COUNT_ROWS", "COUNT_NON_NULL", "COUNT_DISTINCT":
			if output.Kind != "integer" {
				return fmt.Errorf("aggregate %d count output must be integer", index)
			}
		case "MIN", "MAX":
			if output.Kind != inputByName[aggregate.InputColumn].Kind {
				return fmt.Errorf("aggregate %d %s output kind must match its input", index, aggregate.Operation)
			}
		case "SUM":
			if aggregate.InputKind != "INTEGER" && aggregate.InputKind != "DECIMAL" || output.Kind != inputByName[aggregate.InputColumn].Kind {
				return fmt.Errorf("aggregate %d SUM requires a matching numeric output", index)
			}
		case "MEAN":
			if aggregate.InputKind != "INTEGER" && aggregate.InputKind != "DECIMAL" || output.Kind != "decimal" {
				return fmt.Errorf("aggregate %d MEAN requires numeric input and decimal output", index)
			}
		default:
			return fmt.Errorf("aggregate %d has unsupported operation %q", index, aggregate.Operation)
		}
		if err := definePhysicalVariable(defined, aggregate.Variable); err != nil {
			return fmt.Errorf("aggregate %d: %w", index, err)
		}
	}
	reservedOutputs := make(map[string]bool, len(group.Keys)+len(group.Aggregates))
	for _, key := range group.Keys {
		reservedOutputs[key.OutputColumn] = true
	}
	for _, aggregate := range group.Aggregates {
		reservedOutputs[aggregate.Output] = true
	}
	if err := validatePhysicalStageRowValues(
		group.RowValues, stage.InputColumns, stage.OutputColumns,
		physicalProjectionNameSet(stage.InputProjections), reservedOutputs, defined, stage.InputStageID,
	); err != nil {
		return fmt.Errorf("row values: %w", err)
	}
	if err := validatePhysicalGroupRelatedOccurrenceAdapter(group, stage); err != nil {
		return fmt.Errorf("related occurrence adapter: %w", err)
	}
	if err := definePhysicalVariable(defined, group.IdentityVariable); err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	for index, projection := range stage.OutputProjections {
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("output projection %d (%q): %w", index, projection.Name, err)
		}
	}
	if err := definePhysicalVariable(defined, stage.OutputRowVariable); err != nil {
		return err
	}
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return fmt.Errorf("output projections: %w", err)
	}
	return nil
}

func validatePhysicalStageCodingKey(
	stage PhysicalConstructionStage,
	coding PhysicalStageGroupCodingKey,
	outputs, inputs map[string]PhysicalStageColumn,
	defined map[string]bool,
	bindVars map[string]any,
	sourceIdentity string,
	sourceOperations []PhysicalOperation,
) error {
	if coding.RelatedSource != nil {
		return validatePhysicalStageRelatedCodingKey(stage, coding, outputs, inputs, defined, sourceIdentity)
	}
	if stage.InputStageID != "source_projection" || sourceIdentity != "__loom_expansion_identity" ||
		coding.SourceStageID != stage.InputStageID || coding.SourceShapeID == "" || coding.SourceOccurrenceKeyColumn == "" ||
		coding.OwnerOccurrenceID == "" || coding.OwnerResourceType == "" || coding.CodingPath == "" ||
		coding.OutputColumn == "" || coding.TupleVariable == "" || coding.LabelVariable == "" || coding.MembershipColumn == "" {
		return fmt.Errorf("requires exact source_projection expansion identity and compiler source-shape binding")
	}
	if sourceIdentity != "__loom_expansion_identity" {
		return fmt.Errorf("source identity is not the exact expansion occurrence")
	}
	var unnest *PhysicalUnnest
	for _, operation := range sourceOperations {
		if operation.Kind != PhysicalUnnestOp {
			continue
		}
		if operation.Unnest == nil || unnest != nil {
			return fmt.Errorf("source must contain exactly one typed PhysicalUnnest")
		}
		unnest = operation.Unnest
	}
	if unnest == nil || coding.SourceUnnest == nil {
		return fmt.Errorf("does not carry both the source and compiled exact PhysicalUnnest")
	}
	if !PhysicalUnnestExactGroupShapeEqual(*unnest, *coding.SourceUnnest) {
		return fmt.Errorf("compiled Group unnest differs from the exact source operation")
	}
	if unnest.Ordinality == "" || unnest.Expression.Extract == nil ||
		unnest.Owner.OccurrenceID != coding.OwnerOccurrenceID || unnest.Owner.ResourceType != coding.OwnerResourceType ||
		unnest.Expression.Extract.Selector.CanonicalPath() != coding.CodingPath {
		return fmt.Errorf("does not match owner %q/%q, Coding selector %q, and ordinal %q", coding.OwnerOccurrenceID, coding.OwnerResourceType, coding.CodingPath, unnest.Ordinality)
	}
	output, ok := outputs[coding.OutputColumn]
	if !ok || output.Internal || output.Kind != "string" || output.Cardinality != "required_one" {
		return fmt.Errorf("output column %q must be a public scalar string", coding.OutputColumn)
	}
	membership, ok := inputs[coding.MembershipColumn]
	if !ok || !membership.Internal || membership.Kind != "object" || membership.Cardinality != "optional_one" || !membership.Nullable {
		return fmt.Errorf("membership column %q must be a hidden nullable Coding tuple object", coding.MembershipColumn)
	}
	occurrence, ok := inputs[coding.SourceOccurrenceKeyColumn]
	if !ok || !occurrence.Internal || occurrence.Kind != "object" || occurrence.Cardinality != "required_one" {
		return fmt.Errorf("occurrence column %q must be the hidden exact expansion identity object", coding.SourceOccurrenceKeyColumn)
	}
	if !stageInputRowProjection(stage, coding.MembershipColumn) || !stageInputRowProjection(stage, coding.SourceOccurrenceKeyColumn) {
		return fmt.Errorf("membership and occurrence columns must be projected from the exact source row")
	}
	membershipProjection, ok := sourceReturnProjection(sourceOperations, coding.MembershipColumn)
	if !ok || !isCodingMembershipProjection(membershipProjection, *unnest) {
		return fmt.Errorf("membership projection does not contain only the selected Coding system/version/code/display fields")
	}
	occurrenceProjection, ok := sourceReturnProjection(sourceOperations, coding.SourceOccurrenceKeyColumn)
	if !ok || !isExactExpansionOccurrenceProjection(occurrenceProjection, *unnest, bindVars) {
		return fmt.Errorf("occurrence projection is not the exact root/route/owner/item/ordinal identity")
	}
	if err := definePhysicalVariable(defined, coding.InputTupleVariable); err != nil {
		return fmt.Errorf("input tuple: %w", err)
	}
	if err := definePhysicalVariable(defined, coding.TupleVariable); err != nil {
		return fmt.Errorf("tuple: %w", err)
	}
	if err := definePhysicalVariable(defined, coding.LabelVariable); err != nil {
		return fmt.Errorf("label: %w", err)
	}
	return nil
}

func validatePhysicalStageRelatedCodingKey(
	stage PhysicalConstructionStage,
	coding PhysicalStageGroupCodingKey,
	outputs, inputs map[string]PhysicalStageColumn,
	defined map[string]bool,
	sourceIdentity string,
) error {
	related := coding.RelatedSource
	if stage.Group == nil {
		return fmt.Errorf("Group payload is missing")
	}
	adapter := stage.Group.RelatedOccurrence
	if related == nil || adapter == nil || stage.InputStageID != "source_projection" || sourceIdentity != "_key" ||
		coding.SourceStageID != stage.InputStageID || coding.SourceShapeID != related.SourceShapeID ||
		coding.SourceOccurrenceKeyColumn == "" || coding.SourceOccurrenceKeyColumn != adapter.OccurrenceKeyColumn ||
		coding.SourceUnnest != nil || coding.OwnerOccurrenceID != related.TargetOccurrenceID ||
		coding.OwnerResourceType != related.TargetResourceType || coding.CodingPath != related.CodingSelector ||
		coding.MembershipColumn != related.MembershipColumn || coding.MembershipColumn != adapter.CodingMembershipColumn {
		return fmt.Errorf("requires the exact Group-owned related target, Coding selector, and occurrence identity")
	}
	if related.RouteID != adapter.RouteID || related.EmptyPolicy != string(adapter.RelatedRecords.EmptyPolicy) ||
		len(adapter.RelatedRecords.Route) == 0 ||
		related.TargetOccurrenceID != adapter.RelatedRecords.Route[len(adapter.RelatedRecords.Route)-1].ToOccurrenceID ||
		related.TargetNodeID != adapter.RelatedRecords.TargetNodeID || related.TargetResourceType != adapter.RelatedRecords.TargetResourceType ||
		!physicalGroupRelatedRouteMatches(related.Route, adapter.RelatedRecords.Route) {
		return fmt.Errorf("related Coding source differs from the exact Group-owned route")
	}
	if coding.OutputColumn == "" || coding.InputTupleVariable == "" || coding.TupleVariable == "" || coding.LabelVariable == "" ||
		coding.MembershipColumn == "" || adapter.CodingSelector != coding.CodingPath || adapter.CodingUnnest == nil ||
		adapter.CodingArrayColumn == "" || adapter.CodingUnnest.Ordinality == "" ||
		adapter.CodingUnnest.EmptyPolicy != adapter.CodingEmptyPolicy ||
		adapter.CodingUnnest.Expression.Kind != PhysicalValueExpression || adapter.CodingUnnest.Expression.Value == nil ||
		adapter.CodingUnnest.Expression.Value.Variable != adapter.RelatedRecords.ItemVariable ||
		!reflect.DeepEqual(adapter.CodingUnnest.Expression.Value.Path, []string{adapter.CodingArrayColumn}) {
		return fmt.Errorf("related Coding selector has no exact typed array and ordinal operation")
	}
	output, ok := outputs[coding.OutputColumn]
	if !ok || output.Internal || output.Kind != "string" || output.Cardinality != "required_one" {
		return fmt.Errorf("output column %q must be a public scalar string", coding.OutputColumn)
	}
	membership, ok := inputs[coding.MembershipColumn]
	if !ok || !membership.Internal || membership.Kind != "object" || membership.Cardinality != "optional_one" || !membership.Nullable {
		return fmt.Errorf("membership column %q must be a hidden nullable Coding tuple object", coding.MembershipColumn)
	}
	occurrence, ok := inputs[coding.SourceOccurrenceKeyColumn]
	if !ok || !occurrence.Internal || occurrence.Kind != "object" || occurrence.Cardinality != "required_one" {
		return fmt.Errorf("occurrence column %q must be the hidden exact related target/Coding ordinal identity", coding.SourceOccurrenceKeyColumn)
	}
	membershipProjection, membershipProjected := stageInputProjection(stage, coding.MembershipColumn)
	occurrenceProjection, occurrenceProjected := stageInputProjection(stage, coding.SourceOccurrenceKeyColumn)
	if !membershipProjected || !membershipProjection.Hidden || membershipProjection.Expression == nil ||
		!occurrenceProjected || !occurrenceProjection.Hidden || occurrenceProjection.Expression == nil {
		return fmt.Errorf("membership and occurrence columns must be projected from the related Group adapter")
	}
	if err := definePhysicalVariable(defined, coding.InputTupleVariable); err != nil {
		return fmt.Errorf("input tuple: %w", err)
	}
	if err := definePhysicalVariable(defined, coding.TupleVariable); err != nil {
		return fmt.Errorf("tuple: %w", err)
	}
	if err := definePhysicalVariable(defined, coding.LabelVariable); err != nil {
		return fmt.Errorf("label: %w", err)
	}
	return nil
}

func PhysicalUnnestExactGroupShapeEqual(left, right PhysicalUnnest) bool {
	if left.Owner.OccurrenceID != right.Owner.OccurrenceID || left.Owner.ResourceType != right.Owner.ResourceType ||
		left.Owner.RootVariable != right.Owner.RootVariable || left.Owner.OwnerVariable != right.Owner.OwnerVariable ||
		len(left.Owner.Route) != len(right.Owner.Route) || left.OutputVariable != right.OutputVariable ||
		left.Ordinality != right.Ordinality || left.HasItemVariable != right.HasItemVariable ||
		!reflect.DeepEqual(left.Ancestors, right.Ancestors) || left.EmptyPolicy != right.EmptyPolicy ||
		left.Expression.Kind != right.Expression.Kind || left.Expression.Cardinality != right.Expression.Cardinality ||
		left.Expression.Extract == nil || right.Expression.Extract == nil {
		return false
	}
	for index := range left.Owner.Route {
		a, b := left.Owner.Route[index], right.Owner.Route[index]
		if a.OccurrenceID != b.OccurrenceID || !reflect.DeepEqual(a.Traversal, b.Traversal) {
			return false
		}
	}
	return reflect.DeepEqual(left.Expression.Extract.Source, right.Expression.Extract.Source) &&
		left.Expression.Extract.ResourceType == right.Expression.Extract.ResourceType &&
		left.Expression.Extract.Selector.CanonicalPath() == right.Expression.Extract.Selector.CanonicalPath()
}

func stageInputRowProjection(stage PhysicalConstructionStage, name string) bool {
	for _, projection := range stage.InputProjections {
		if projection.Name == name && projection.Hidden && projection.Value.Variable == stage.InputRowVariable && reflect.DeepEqual(projection.Value.Path, []string{name}) {
			return true
		}
	}
	return false
}

func stageInputProjection(stage PhysicalConstructionStage, name string) (PhysicalProjection, bool) {
	for _, projection := range stage.InputProjections {
		if projection.Name == name {
			return projection, true
		}
	}
	return PhysicalProjection{}, false
}

func sourceReturnProjection(operations []PhysicalOperation, name string) (PhysicalProjection, bool) {
	for index := len(operations) - 1; index >= 0; index-- {
		operation := operations[index]
		if operation.Kind != PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == name {
				return projection, true
			}
		}
		return PhysicalProjection{}, false
	}
	return PhysicalProjection{}, false
}

func isCodingMembershipProjection(projection PhysicalProjection, unnest PhysicalUnnest) bool {
	if !projection.Hidden || projection.Expression == nil || projection.Expression.Kind != PhysicalObjectExpression || projection.Expression.Object == nil ||
		len(projection.Expression.Object.Fields) != 4 {
		return false
	}
	wanted := []string{"system", "version", "code", "display"}
	for index, field := range projection.Expression.Object.Fields {
		if field.Name != wanted[index] || field.Expression.Kind != PhysicalValueExpression || field.Expression.Value == nil ||
			field.Expression.Value.Variable != unnest.OutputVariable || !reflect.DeepEqual(field.Expression.Value.Path, []string{wanted[index]}) {
			return false
		}
	}
	return true
}

func isExactExpansionOccurrenceProjection(projection PhysicalProjection, unnest PhysicalUnnest, bindVars map[string]any) bool {
	if !projection.Hidden || projection.Expression == nil || projection.Expression.Kind != PhysicalObjectExpression || projection.Expression.Object == nil {
		return false
	}
	wantedNames := []string{"root_id", "occurrence_id"}
	for index := range unnest.Owner.Route {
		wantedNames = append(wantedNames, fmt.Sprintf("edge_%02d_id", index))
	}
	wantedNames = append(wantedNames, "owner_id", "item_present", "ordinal")
	fields := projection.Expression.Object.Fields
	if len(fields) != len(wantedNames) {
		return false
	}
	for index, field := range fields {
		if field.Name != wantedNames[index] {
			return false
		}
		switch field.Name {
		case "occurrence_id":
			if field.Expression.Kind != PhysicalLiteralExpression || field.Expression.Literal == nil {
				return false
			}
			value, ok := bindVars[field.Expression.Literal.BindKey].(string)
			if !ok || value != unnest.Owner.OccurrenceID {
				return false
			}
		default:
			wantVariable, wantPath := "", "_id"
			switch {
			case field.Name == "root_id":
				wantVariable = unnest.Owner.RootVariable
			case field.Name == "owner_id":
				wantVariable = unnest.Owner.OwnerVariable
			case field.Name == "item_present":
				wantVariable, wantPath = unnest.HasItemVariable, ""
			case field.Name == "ordinal":
				wantVariable, wantPath = unnest.Ordinality, ""
			default:
				routeIndex := index - 2
				if routeIndex < 0 || routeIndex >= len(unnest.Owner.Route) {
					return false
				}
				wantVariable = unnest.Owner.Route[routeIndex].Traversal.EdgeVariable
			}
			if field.Expression.Kind != PhysicalValueExpression || field.Expression.Value == nil ||
				field.Expression.Value.Variable != wantVariable ||
				!reflect.DeepEqual(field.Expression.Value.Path, func() []string {
					if wantPath == "" {
						return nil
					}
					return []string{wantPath}
				}()) {
				return false
			}
		}
	}
	return true
}

func validateRootContributorReduction(
	inputColumn string,
	inputMany bool,
	outputColumn string,
	variable string,
	inputColumns, outputColumns []PhysicalStageColumn,
	defined map[string]bool,
) error {
	if inputColumn == "" && outputColumn == "" && variable == "" {
		if inputMany {
			return fmt.Errorf("root contributor cardinality is set without a contributor input")
		}
		return nil
	}
	if inputColumn == "" || outputColumn == "" || !physicalVariablePattern.MatchString(variable) {
		return fmt.Errorf("root contributor input, output, and variable are required together")
	}
	input, ok := physicalStageColumnMap(inputColumns)[inputColumn]
	if !ok || !input.Internal || input.Kind != "string" || input.RootContributorResourceType == "" {
		return fmt.Errorf("input column %q is not a typed hidden root contributor", inputColumn)
	}
	validInput := input.Name == "_key" && (input.Cardinality == "required_one" || input.Cardinality == "optional_one") ||
		input.Name == "__loom_root_contributor_keys" && input.Cardinality == "many"
	if !validInput || inputMany != (input.Cardinality == "many") {
		return fmt.Errorf("input column %q has an unsupported contributor shape", inputColumn)
	}
	output, ok := physicalStageColumnMap(outputColumns)[outputColumn]
	if !ok || !output.Internal || output.Name != "__loom_root_contributor_keys" ||
		output.Kind != "string" || output.Cardinality != "many" ||
		output.RootContributorResourceType != input.RootContributorResourceType {
		return fmt.Errorf("output column %q is not the matching typed contributor set", outputColumn)
	}
	if err := definePhysicalVariable(defined, variable); err != nil {
		return fmt.Errorf("root contributor variable: %w", err)
	}
	return nil
}

func validatePhysicalStageRowValues(
	rowValues []PhysicalStageRowValue,
	inputColumns, outputColumns []PhysicalStageColumn,
	projectedInputs, reservedOutputs map[string]bool,
	defined map[string]bool,
	inputStageID string,
) error {
	inputs, outputs := physicalStageColumnMap(inputColumns), physicalStageColumnMap(outputColumns)
	seenOutputs := make(map[string]bool, len(rowValues))
	for index, rowValue := range rowValues {
		input, ok := inputs[rowValue.InputColumn]
		inputKind, scalar := physicalStageScalarKind(input.Kind)
		if input.Kind == "object" {
			inputKind, scalar = "OBJECT", true
		}
		boundSource := rowValue.FieldSource
		if boundSource != nil {
			if !input.Internal || !validPhysicalGroupFieldSource(boundSource, inputStageID) {
				return fmt.Errorf("row value %d hidden input column %q has no exact source-stage authority", index, rowValue.InputColumn)
			}
		} else if input.Internal {
			return fmt.Errorf("row value %d input column %q is internal without a bound source authority", index, rowValue.InputColumn)
		}
		if !ok || (!input.Internal && boundSource != nil) || !scalar || inputKind != rowValue.InputKind ||
			(rowValue.InputMany != (input.Cardinality == "many")) {
			return fmt.Errorf("row value %d input column %q is not a supported scalar or scalar array", index, rowValue.InputColumn)
		}
		if projectedInputs != nil && !projectedInputs[rowValue.InputColumn] {
			return fmt.Errorf("row value %d input column %q is missing from shape projections", index, rowValue.InputColumn)
		}
		output, ok := outputs[rowValue.Output]
		if !ok || output.Internal || output.Kind != input.Kind || !physicalPathPartPattern.MatchString(rowValue.Output) ||
			seenOutputs[rowValue.Output] || reservedOutputs[rowValue.Output] {
			return fmt.Errorf("row value %d output column %q is missing, mistyped, or collides with another shape output", index, rowValue.Output)
		}
		switch rowValue.Policy {
		case "ALL":
			if output.Cardinality != "many" || output.Nullable {
				return fmt.Errorf("row value %d ALL output must be a non-null array of its scalar type", index)
			}
		case "ONE":
			if output.Cardinality != "optional_one" || !output.Nullable {
				return fmt.Errorf("row value %d ONE output must be a nullable scalar", index)
			}
		default:
			return fmt.Errorf("row value %d has unsupported policy %q", index, rowValue.Policy)
		}
		if defined != nil {
			if err := definePhysicalVariable(defined, rowValue.Variable); err != nil {
				return fmt.Errorf("row value %d: %w", index, err)
			}
		}
		seenOutputs[rowValue.Output] = true
	}
	return nil
}

func validPhysicalGroupFieldSource(source *PhysicalStageGroupFieldSource, inputStageID string) bool {
	if source == nil || inputStageID == "" {
		return false
	}
	switch source.Kind {
	case PhysicalStageGroupFieldSourceCurrentStage:
		if source.RelatedOccurrence != nil || source.SourceStageID != inputStageID || source.SourceShapeID == "" ||
			(source.Role != "ROOT" && source.Role != "OWNER" && source.Role != "ANCESTOR" && source.Role != "CURRENT_ITEM") ||
			source.OccurrenceID == "" || source.OwnerResourceType == "" || source.ResourceType == "" ||
			source.CandidateID == "" || source.CanonicalPath == "" || source.SourceVariable == "" {
			return false
		}
		return source.Role == "ANCESTOR" && source.BoundarySelector != "" || source.Role != "ANCESTOR" && source.BoundarySelector == ""
	case PhysicalStageGroupFieldSourceRelatedOccurrence:
		related := source.RelatedOccurrence
		if related == nil || source.PhysicalStageGroupCurrentStageFieldSource != (PhysicalStageGroupCurrentStageFieldSource{}) ||
			related.SourceStageID != inputStageID || related.SourceShapeID == "" || related.AnchorOccurrenceID == "" ||
			related.AnchorNodeID == "" || related.AnchorResourceType == "" || related.TargetOccurrenceID == "" ||
			related.TargetNodeID == "" || related.TargetResourceType == "" || related.ResourceType == "" ||
			(related.EmptyPolicy != "PRESERVE_PARENT" && related.EmptyPolicy != "EXCLUDE" && related.EmptyPolicy != "ERROR") ||
			related.RouteID == "" || related.Multiplicity != "DISTINCT_TARGET_PER_INPUT" || len(related.Route) == 0 {
			return false
		}
		fromOccurrence, fromNode, fromResource := related.AnchorOccurrenceID, related.AnchorNodeID, related.AnchorResourceType
		for index, hop := range related.Route {
			if hop.StepIndex != index || hop.FromOccurrenceID != fromOccurrence || hop.FromNodeID != fromNode || hop.FromResourceType != fromResource ||
				hop.EdgeID == "" || hop.Relationship == "" || (hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
				(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") || hop.ToOccurrenceID == "" || hop.ToNodeID == "" || hop.ToResourceType == "" {
				return false
			}
			fromOccurrence, fromNode, fromResource = hop.ToOccurrenceID, hop.ToNodeID, hop.ToResourceType
		}
		if fromOccurrence != related.TargetOccurrenceID || fromNode != related.TargetNodeID || fromResource != related.TargetResourceType {
			return false
		}
		if related.CodingSelector == "" {
			return related.CandidateID != "" && related.CanonicalPath != "" && related.CodingEmptyPolicy == "" &&
				related.OrdinalSemantics == "" && related.ProjectionColumn != "" && related.MembershipColumn == ""
		}
		return related.CandidateID == "" && related.CanonicalPath == "" && related.ProjectionColumn == "" &&
			related.MembershipColumn != "" && related.OrdinalSemantics == "ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY" &&
			(related.CodingEmptyPolicy == "GROUP" || related.CodingEmptyPolicy == "EXCLUDE" || related.CodingEmptyPolicy == "ERROR")
	default:
		return false
	}
}

func physicalGroupRelatedRouteMatches(source []PhysicalStageGroupRelatedRouteStep, actual []PhysicalStageRelatedRouteStep) bool {
	if len(source) == 0 || len(source) != len(actual) {
		return false
	}
	for index, route := range source {
		step := actual[index]
		if route.StepIndex != index || route.StepIndex != step.StepIndex ||
			route.FromOccurrenceID != step.FromOccurrenceID || route.ToOccurrenceID != step.ToOccurrenceID ||
			route.EdgeID != step.EdgeID || route.FromNodeID != step.FromNodeID ||
			route.ToNodeID != step.ToNodeID || route.FromResourceType != step.FromResourceType ||
			route.ToResourceType != step.ToResourceType || route.Relationship != step.Relationship ||
			route.StorageDirection != step.StorageDirection || route.MatchMode != step.MatchMode {
			return false
		}
	}
	return true
}

func physicalGroupRelatedReturnSelector(records *PhysicalStageRelatedExpand, output, resourceType, canonicalPath string) bool {
	if records == nil || strings.TrimSpace(canonicalPath) == "" ||
		records.RelatedRecords.Return.Kind != PhysicalObjectExpression || records.RelatedRecords.Return.Object == nil {
		return false
	}
	for _, field := range records.RelatedRecords.Return.Object.Fields {
		if field.Name != output || field.Expression.Kind != PhysicalExtractExpression || field.Expression.Extract == nil ||
			field.Expression.Extract.ResourceType != resourceType ||
			physicalSelectorCanonicalPath(field.Expression.Extract.Selector) != canonicalPath {
			continue
		}
		return true
	}
	return false
}

func physicalSelectorCanonicalPath(selector spec.Selector) string {
	parts := make([]string, 0, len(selector.Steps))
	for _, step := range selector.Steps {
		part := step.Field
		if step.Iterate {
			part += "[]"
		} else if step.Index != nil {
			part += fmt.Sprintf("[%d]", *step.Index)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ".")
}

func validatePhysicalGroupRelatedOccurrenceAdapter(group PhysicalStageGroup, stage PhysicalConstructionStage) error {
	var sources []*PhysicalStageGroupRelatedOccurrenceFieldSource
	for index := range group.Keys {
		if group.Keys[index].FieldSource != nil && group.Keys[index].FieldSource.RelatedOccurrence != nil {
			sources = append(sources, group.Keys[index].FieldSource.RelatedOccurrence)
		}
	}
	for index := range group.RowValues {
		if group.RowValues[index].FieldSource != nil && group.RowValues[index].FieldSource.RelatedOccurrence != nil {
			sources = append(sources, group.RowValues[index].FieldSource.RelatedOccurrence)
		}
	}
	if group.CodingKey != nil && group.CodingKey.RelatedSource != nil {
		sources = append(sources, group.CodingKey.RelatedSource)
	}
	if len(sources) == 0 {
		if group.RelatedOccurrence != nil {
			return fmt.Errorf("unreferenced related route adapter")
		}
		return nil
	}
	adapter := group.RelatedOccurrence
	if adapter == nil || adapter.RouteID == "" || adapter.RelatedRecords.RouteID != adapter.RouteID ||
		adapter.RelatedRecords.Multiplicity != "DISTINCT_TARGET_PER_INPUT" ||
		len(adapter.RelatedRecords.Route) == 0 ||
		adapter.RelatedRecords.RelatedRecords.Unique != true || adapter.RelatedRecords.RelatedRecords.Sort == nil ||
		adapter.RelatedRecords.EmptyPolicy == "" {
		return fmt.Errorf("missing or incomplete exact route adapter")
	}
	projections := make(map[string]PhysicalStageGroupRelatedProjection, len(adapter.ScalarProjections))
	inputColumns := physicalStageColumnMap(stage.InputColumns)
	for _, projection := range adapter.ScalarProjections {
		if projection.InputColumn == "" || projection.ResultField == "" || projection.OwnerResourceType == "" || projection.ValueResourceType == "" ||
			projection.CandidateID == "" || projection.CanonicalPath == "" || projections[projection.InputColumn].InputColumn != "" ||
			!physicalGroupRelatedReturnSelector(&adapter.RelatedRecords, projection.ResultField, projection.OwnerResourceType, projection.CanonicalPath) {
			return fmt.Errorf("scalar route projection %q is not an exact selected target field", projection.InputColumn)
		}
		input, ok := inputColumns[projection.InputColumn]
		inputProjection, projectionFound := stageInputProjection(stage, projection.InputColumn)
		if !ok || !input.Internal || input.Cardinality == "many" || !projectionFound || !inputProjection.Hidden || inputProjection.Value.Variable == "" ||
			inputProjection.Value.Variable != adapter.RelatedRecords.ItemVariable ||
			!reflect.DeepEqual(inputProjection.Value.Path, []string{projection.ResultField}) {
			return fmt.Errorf("scalar route input %q is not projected from its exact related target result", projection.InputColumn)
		}
		projections[projection.InputColumn] = projection
	}
	if adapter.OccurrenceKeyColumn == "" {
		return fmt.Errorf("related Group adapter lacks its exact occurrence key")
	}
	occurrence, occurrenceOK := inputColumns[adapter.OccurrenceKeyColumn]
	occurrenceProjection, occurrenceProjected := stageInputProjection(stage, adapter.OccurrenceKeyColumn)
	if !occurrenceOK || !occurrence.Internal || occurrence.Kind != "object" || occurrence.Cardinality != "required_one" ||
		!occurrenceProjected || !occurrenceProjection.Hidden || occurrenceProjection.Expression == nil {
		return fmt.Errorf("related Group occurrence key is not a hidden required object input")
	}
	codingSources := 0
	for _, source := range sources {
		related := source
		if related.RouteID != adapter.RouteID || related.EmptyPolicy != string(adapter.RelatedRecords.EmptyPolicy) ||
			related.TargetOccurrenceID != adapter.RelatedRecords.Route[len(adapter.RelatedRecords.Route)-1].ToOccurrenceID ||
			related.TargetNodeID != adapter.RelatedRecords.TargetNodeID || related.TargetResourceType != adapter.RelatedRecords.TargetResourceType ||
			!physicalGroupRelatedRouteMatches(related.Route, adapter.RelatedRecords.Route) {
			return fmt.Errorf("bound field does not match the Group-owned related route")
		}
		if related.CodingSelector == "" {
			projection, ok := projections[related.ProjectionColumn]
			if !ok || projection.CandidateID != related.CandidateID || projection.CanonicalPath != related.CanonicalPath || projection.ValueResourceType != related.ResourceType {
				return fmt.Errorf("bound scalar is missing its exact route projection")
			}
			continue
		}
		codingSources++
		wantCodingEmptyPolicy, policyOK := physicalGroupCodingEmptyPolicy(related.CodingEmptyPolicy)
		if adapter.CodingSelector != related.CodingSelector || adapter.CodingArrayColumn == "" || !policyOK ||
			adapter.CodingEmptyPolicy != wantCodingEmptyPolicy ||
			adapter.OrdinalSemantics != related.OrdinalSemantics || adapter.CodingMembershipColumn != related.MembershipColumn ||
			adapter.CodingUnnest == nil || adapter.CodingUnnest.Ordinality == "" ||
			adapter.CodingUnnest.EmptyPolicy != adapter.CodingEmptyPolicy ||
			adapter.CodingUnnest.Expression.Kind != PhysicalValueExpression || adapter.CodingUnnest.Expression.Value == nil ||
			adapter.CodingUnnest.Expression.Value.Variable != adapter.RelatedRecords.ItemVariable ||
			len(adapter.CodingUnnest.Expression.Value.Path) != 1 || adapter.CodingUnnest.Expression.Value.Path[0] != adapter.CodingArrayColumn ||
			!physicalGroupRelatedReturnSelector(&adapter.RelatedRecords, adapter.CodingArrayColumn, related.TargetResourceType, related.CodingSelector) {
			return fmt.Errorf("bound Coding selector lacks its exact typed target projection and ordinal operation")
		}
	}
	if codingSources > 1 {
		return fmt.Errorf("Group supports one Coding identity route source")
	}
	if (adapter.CodingSelector == "") != (adapter.CodingUnnest == nil) {
		return fmt.Errorf("Coding route projection and ordinal operation disagree")
	}
	if adapter.RelatedRecords.EmptyPolicy != PhysicalUnnestPreserveParent && adapter.RelatedRecords.EmptyPolicy != PhysicalUnnestExclude && adapter.RelatedRecords.EmptyPolicy != PhysicalUnnestError {
		return fmt.Errorf("route empty policy %q is unsupported", adapter.RelatedRecords.EmptyPolicy)
	}
	for _, source := range sources {
		if !validPhysicalGroupFieldSource(&PhysicalStageGroupFieldSource{Kind: PhysicalStageGroupFieldSourceRelatedOccurrence, RelatedOccurrence: source}, stage.InputStageID) {
			return fmt.Errorf("related field authority is malformed")
		}
	}
	return nil
}

func physicalGroupCodingEmptyPolicy(policy string) (PhysicalUnnestEmptyPolicy, bool) {
	switch policy {
	case "GROUP":
		return PhysicalUnnestPreserveParent, true
	case "EXCLUDE":
		return PhysicalUnnestExclude, true
	case "ERROR":
		return PhysicalUnnestError, true
	default:
		return "", false
	}
}

func physicalProjectionNameSet(projections []PhysicalProjection) map[string]bool {
	names := make(map[string]bool, len(projections))
	for _, projection := range projections {
		names[projection.Name] = true
	}
	return names
}
