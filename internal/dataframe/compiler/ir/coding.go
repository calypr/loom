package ir

import (
	"fmt"
	"reflect"
	"strings"
)

func validatePhysicalStageCodedGroup(stage PhysicalConstructionStage, coded PhysicalStageCodedGroup, bindVars map[string]any, sourceOperations []PhysicalOperation) error {
	for key, label := range map[string]string{
		coded.RootCollectionBindKey: "root collection",
		coded.ConstructionIDBindKey: "construction ID",
		coded.OccurrenceIDBindKey:   "source occurrence ID",
		coded.CodingPathBindKey:     "Coding path",
	} {
		if err := requireNonEmptyStringBind(bindVars, key); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}
	collection, _ := bindVars[coded.RootCollectionBindKey].(string)
	constructionID, _ := bindVars[coded.ConstructionIDBindKey].(string)
	occurrenceID, _ := bindVars[coded.OccurrenceIDBindKey].(string)
	codingPath, _ := bindVars[coded.CodingPathBindKey].(string)
	if collection != coded.ResourceType || strings.TrimSpace(coded.ResourceType) == "" {
		return fmt.Errorf("root collection does not match the exact root resource type")
	}
	if !coded.SourceRowsUnique {
		return fmt.Errorf("CODED_GROUP requires compiler proof that the direct source has at most one row per root _key")
	}
	if constructionID == "" || occurrenceID != "base" || codingPath != coded.CodingPath || coded.SourceIdentityColumn != "_key" {
		return fmt.Errorf("construction, root occurrence, Coding path, and root identity facts are inconsistent")
	}
	inputIdentity, exists := physicalStageColumnMap(stage.InputColumns)[coded.SourceIdentityColumn]
	if !exists || !inputIdentity.Internal || !inputIdentity.Identity || inputIdentity.Kind != "string" || inputIdentity.Cardinality != "required_one" {
		return fmt.Errorf("source identity column %q is not a required root key", coded.SourceIdentityColumn)
	}
	if len(coded.PathSegments) == 0 {
		return fmt.Errorf("Coding path segments are required")
	}
	var canonical strings.Builder
	for index, segment := range coded.PathSegments {
		if !physicalVariablePattern.MatchString(segment.Name) {
			return fmt.Errorf("Coding path segment %d has an unsafe field name", index)
		}
		if index > 0 {
			canonical.WriteByte('.')
		}
		canonical.WriteString(segment.Name)
		if segment.Repeated {
			canonical.WriteString("[]")
		}
	}
	last := coded.PathSegments[len(coded.PathSegments)-1]
	if !last.Repeated || canonical.String() != coded.CodingPath {
		return fmt.Errorf("Coding path must be the exact generated repeated Coding path")
	}
	switch coded.MissingKeyPolicy {
	case PhysicalStageGroupMissingKeyGroup, PhysicalStageGroupMissingKeyExclude, PhysicalStageGroupMissingKeyError:
	default:
		return fmt.Errorf("missing-key policy %q is unsupported", coded.MissingKeyPolicy)
	}
	variables := []string{coded.SystemVariable, coded.VersionVariable, coded.CodeVariable, coded.CountVariable, coded.IdentityVariable}
	seenVariables := map[string]bool{stage.InputRowVariable: true, stage.OutputRowVariable: true}
	for _, variable := range variables {
		if !physicalVariablePattern.MatchString(variable) || seenVariables[variable] {
			return fmt.Errorf("coded-group output variables must be safe and distinct")
		}
		seenVariables[variable] = true
	}
	outputs := physicalStageColumnMap(stage.OutputColumns)
	for _, columnName := range []string{coded.SystemOutputColumn, coded.VersionOutputColumn, coded.CodeOutputColumn} {
		column, ok := outputs[columnName]
		if !ok || column.Internal || column.Kind != "string" || column.Cardinality != "optional_one" || !column.Nullable {
			return fmt.Errorf("coded-group key output %q must be a nullable scalar string", columnName)
		}
	}
	count, ok := outputs[coded.CountOutputColumn]
	if !ok || count.Internal || count.Kind != "integer" || count.Cardinality != "required_one" || count.Nullable {
		return fmt.Errorf("coded-group source count must be a required integer")
	}
	identity, ok := outputs["__loom_row_id"]
	if !ok || !identity.Internal || !identity.Identity || identity.Kind != "string" || identity.Cardinality != "required_one" {
		return fmt.Errorf("coded-group output requires the hidden stable row identity")
	}
	projectionByName := make(map[string]PhysicalProjection, len(stage.OutputProjections))
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return fmt.Errorf("output projections: %w", err)
	}
	for _, projection := range stage.OutputProjections {
		projectionByName[projection.Name] = projection
	}
	for name, variable := range map[string]string{
		coded.SystemOutputColumn:  coded.SystemVariable,
		coded.VersionOutputColumn: coded.VersionVariable,
		coded.CodeOutputColumn:    coded.CodeVariable,
		coded.CountOutputColumn:   coded.CountVariable,
		"__loom_row_id":           coded.IdentityVariable,
	} {
		projection, ok := projectionByName[name]
		if !ok || projection.Value.Variable != variable || len(projection.Value.Path) != 0 || projection.Expression != nil {
			return fmt.Errorf("coded-group output projection %q does not use its exact value variable", name)
		}
	}
	for _, rowValue := range coded.RowValues {
		projection, ok := projectionByName[rowValue.Output]
		if !ok || projection.Value.Variable != rowValue.Variable || len(projection.Value.Path) != 0 || projection.Expression != nil {
			return fmt.Errorf("coded-group row value output projection %q does not use its exact value variable", rowValue.Output)
		}
	}
	reservedOutputs := map[string]bool{
		coded.SystemOutputColumn: true, coded.VersionOutputColumn: true,
		coded.CodeOutputColumn: true, coded.CountOutputColumn: true, "__loom_row_id": true,
	}
	projectionNames := physicalProjectionNameSet(coded.RowValueProjections)
	if err := validatePhysicalStageRowValues(coded.RowValues, stage.InputColumns, stage.OutputColumns, projectionNames, reservedOutputs, seenVariables, ""); err != nil {
		return fmt.Errorf("row values: %w", err)
	}
	if len(coded.RowValueProjections) != len(projectionNames) {
		return fmt.Errorf("row value source projections contain duplicate columns")
	}
	var sourceReturn *PhysicalReturn
	for index := len(sourceOperations) - 1; index >= 0; index-- {
		if sourceOperations[index].Kind == PhysicalReturnOp && sourceOperations[index].Return != nil {
			sourceReturn = sourceOperations[index].Return
			break
		}
	}
	if len(coded.RowValues) != 0 && sourceReturn == nil {
		return fmt.Errorf("row values require a resolved source projection")
	}
	if sourceReturn != nil {
		sourceProjections := make(map[string]PhysicalProjection, len(sourceReturn.Projections))
		for _, projection := range sourceReturn.Projections {
			sourceProjections[projection.Name] = projection
		}
		expectedInputs := make(map[string]bool, len(coded.RowValues))
		for _, rowValue := range coded.RowValues {
			expectedInputs[rowValue.InputColumn] = true
		}
		if len(expectedInputs) != len(coded.RowValueProjections) {
			return fmt.Errorf("row value source projections do not match requested input columns")
		}
		for _, projection := range coded.RowValueProjections {
			want, ok := sourceProjections[projection.Name]
			if !ok || !expectedInputs[projection.Name] {
				return fmt.Errorf("row value source projection %q is not a requested public input", projection.Name)
			}
			want.Hidden = true
			if !reflect.DeepEqual(projection, want) {
				return fmt.Errorf("row value source projection %q differs from its resolved source", projection.Name)
			}
		}
	}
	return nil
}
