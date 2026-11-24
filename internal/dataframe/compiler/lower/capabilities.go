package lower

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func markRootContributorIdentity(schema []CompiledOutputColumn, rootResourceType string) []CompiledOutputColumn {
	if rootResourceType == "" {
		return schema
	}
	marked := cloneCompiledSchema(schema)
	for index := range marked {
		column := &marked[index]
		if column.Name == "_key" && column.Internal && column.Kind == string(expression.KindString) &&
			(column.Cardinality == string(expression.RequiredOne) || column.Cardinality == string(expression.OptionalOne)) {
			column.RootContributorResourceType = rootResourceType
		}
	}
	return marked
}

func hasRootContributorIdentity(schema []CompiledOutputColumn, rootResourceType string) bool {
	_, ok := rootContributorInput(schema, rootResourceType)
	return ok
}

func rootContributorInput(schema []CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	if rootResourceType == "" {
		return CompiledOutputColumn{}, false
	}
	for _, column := range schema {
		if column.Internal && column.RootContributorResourceType == rootResourceType &&
			column.Name == rootContributorSetColumn && column.Kind == string(expression.KindString) &&
			column.Cardinality == string(expression.Many) {
			return column, true
		}
	}
	for _, column := range schema {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType == rootResourceType &&
			column.Kind == string(expression.KindString) &&
			(column.Cardinality == string(expression.RequiredOne) || column.Cardinality == string(expression.OptionalOne)) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func rootContributorSetFromMap(columns map[string]CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	for _, column := range columns {
		if column.Internal && column.RootContributorResourceType == rootResourceType &&
			column.Name == rootContributorSetColumn && column.Kind == string(expression.KindString) &&
			column.Cardinality == string(expression.Many) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func rootContributorInputFromMap(columns map[string]CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	if contributor, ok := rootContributorSetFromMap(columns, rootResourceType); ok {
		return contributor, true
	}
	for _, column := range columns {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType == rootResourceType &&
			column.Kind == string(expression.KindString) &&
			(column.Cardinality == string(expression.RequiredOne) || column.Cardinality == string(expression.OptionalOne)) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func rootContributorSet(schema []CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Internal && column.RootContributorResourceType == rootResourceType &&
			column.Name == rootContributorSetColumn && column.Kind == string(expression.KindString) &&
			column.Cardinality == string(expression.Many) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func rootContributorRootKey(schema []CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType == rootResourceType &&
			column.Kind == string(expression.KindString) && column.Cardinality == string(expression.RequiredOne) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func hasCompilerOwnedRootContributor(schema []CompiledOutputColumn) bool {
	for _, column := range schema {
		if column.RootContributorResourceType == "" {
			continue
		}
		if _, found := rootContributorInput(schema, column.RootContributorResourceType); found {
			return true
		}
	}
	return false
}

func constructionNeedsRetainedRootContributors(steps []recipe.ConstructionStep) bool {
	seenShape := false
	for _, step := range steps {
		switch step.Operation.Kind {
		case recipe.ConstructionGroupOp, recipe.ConstructionPivotOp:
			seenShape = true
		case recipe.ConstructionRelatedSourceOp, recipe.ConstructionRelatedExpandOp, recipe.ConstructionRelatedEligibilityOp:
			if seenShape {
				return true
			}
		}
	}
	return false
}

func constructionPreservesRootContributorProvenance(operation recipe.ConstructionOperationKind) bool {
	switch operation {
	case recipe.ConstructionDeriveOp, recipe.ConstructionFilterOp, recipe.ConstructionPivotOp,
		recipe.ConstructionUnpivotOp, recipe.ConstructionGroupOp, recipe.ConstructionExpandOp,
		recipe.ConstructionRelatedSourceOp, recipe.ConstructionRelatedExpandOp,
		recipe.ConstructionRelatedEligibilityOp, recipe.ConstructionRelatedFieldOp:
		return true
	default:
		return false
	}
}

func constructionCarriesRootContributorValue(operation recipe.ConstructionOperationKind) bool {
	return constructionPreservesRootContributorProvenance(operation)
}

func constructionPreservesLazyRootContributorAvailability(operation recipe.ConstructionOperationKind) bool {
	switch operation {
	case recipe.ConstructionDeriveOp, recipe.ConstructionFilterOp, recipe.ConstructionUnpivotOp,
		recipe.ConstructionExpandOp, recipe.ConstructionRelatedSourceOp, recipe.ConstructionRelatedExpandOp,
		recipe.ConstructionRelatedEligibilityOp, recipe.ConstructionRelatedFieldOp:
		return true
	default:
		return false
	}
}

func stageCapabilities(columns []CompiledOutputColumn, lazyRootContributorAvailable bool) []StageOperationCapability {
	public := publicCompiledSchema(columns)
	numeric, scalar, arrays := 0, 0, 0
	rootKey, rootRowIdentity := false, false
	rootContributorAvailable := lazyRootContributorAvailable || hasCompilerOwnedRootContributor(columns)
	_, activeRelatedRecord := activeRelatedRecordColumn(columns)
	for _, column := range columns {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType != "" &&
			column.Kind == string(expression.KindString) && column.Cardinality == string(expression.RequiredOne) {
			rootKey = true
			rootRowIdentity = column.Identity
		}
	}
	for _, column := range public {
		if column.Cardinality == string(expression.Many) {
			arrays++
			continue
		}
		if _, ok := TableReshapeScalarKind(column.Kind); ok {
			scalar++
		}
		if column.Kind == string(expression.KindInteger) || column.Kind == string(expression.KindDecimal) {
			numeric++
		}
	}
	capability := func(operation recipe.ConstructionOperationKind, supported bool, code, reason string) StageOperationCapability {
		return StageOperationCapability{Operation: operation, Supported: supported, ReasonCode: code, Reason: reason}
	}
	return []StageOperationCapability{
		capability(recipe.ConstructionDeriveOp, numeric > 0, "NO_NUMERIC_COLUMN", "derive requires at least one public scalar numeric column or a numeric literal operand"),
		capability(recipe.ConstructionFilterOp, len(public) > 0, "NO_PUBLIC_COLUMNS", "filter requires a public output column"),
		capability(recipe.ConstructionPivotOp, scalar >= 3, "INSUFFICIENT_SCALAR_COLUMNS", "pivot requires public scalar group, category, and value columns"),
		capability(recipe.ConstructionUnpivotOp, scalar > 0, "NO_COMPATIBLE_UNPIVOT_COLUMNS", "unpivot requires at least one public scalar column; selected columns must have compatible types"),
		capability(recipe.ConstructionGroupOp, len(public) > 0, "NO_PUBLIC_COLUMNS", "group requires at least one public column or row-count input"),
		capability(recipe.ConstructionCodedGroupOp, false, "DIRECT_SOURCE_STAGE_REQUIRED", "coded grouping is available only on the direct source projection stage because transformed rows may omit Coding occurrences"),
		capability(recipe.ConstructionCodedPivotOp, false, "DIRECT_SOURCE_STAGE_REQUIRED", "coded Pivot is available only on the direct source projection stage"),
		capability(recipe.ConstructionExpandOp, arrays > 0, "NO_ARRAY_COLUMNS", "expand requires a public array-valued column"),
		capability(recipe.ConstructionRelatedSourceOp, rootRowIdentity || rootContributorAvailable, "NO_SOURCE_ROW_ANCHOR", "related source requires a root document identity or compiler-proven root contributors to survive this stage"),
		capability(recipe.ConstructionRelatedExpandOp, rootKey || rootContributorAvailable || activeRelatedRecord, "NO_SOURCE_ROW_ANCHOR", "related expansion requires a retained root key, compiler-proven source records, or exact related-record identity"),
		capability(recipe.ConstructionRelatedEligibilityOp, rootKey || rootContributorAvailable || activeRelatedRecord, "NO_SOURCE_ROW_ANCHOR", "related eligibility requires a retained root key, compiler-proven root contributors, or exact related-record identity on this stage"),
		capability(recipe.ConstructionRelatedFieldOp, activeRelatedRecord, "NO_ACTIVE_RELATED_RECORD", "related field requires the exact terminal resource identity to survive this stage"),
	}
}

func codedPivotSourceCapability(columns []CompiledOutputColumn, rootResourceType string, sourceRowsUnique bool) StageOperationCapability {
	capability := StageOperationCapability{
		Operation: recipe.ConstructionCodedPivotOp, ReasonCode: "SOURCE_ROW_NOT_ROOT",
		Reason: "coded Pivot requires direct root rows with a retained root document identity",
	}
	identity, found := schemaColumn(columns, "_key")
	if sourceRowsUnique && found && identity.Internal && identity.Identity && identity.Kind == string(expression.KindString) &&
		identity.Cardinality == string(expression.RequiredOne) && fhirschema.HasResource(rootResourceType) {
		capability.Supported, capability.ReasonCode, capability.Reason = true, "", ""
	}
	return capability
}

const codedPivotSourcePayloadColumn = ir.PhysicalCodedPivotSourcePayloadColumn

func codedGroupSourceCapability(columns []CompiledOutputColumn, rootResourceType string) StageOperationCapability {
	capability := StageOperationCapability{
		Operation:  recipe.ConstructionCodedGroupOp,
		ReasonCode: "SOURCE_ROW_NOT_ROOT",
		Reason:     "coded grouping requires direct root rows with a retained root record identity",
	}
	identity, found := schemaColumn(columns, "_key")
	if !found || !identity.Internal || !identity.Identity {
		return capability
	}
	publicColumns := 0
	for _, column := range columns {
		if !column.Internal {
			publicColumns++
		}
	}
	if publicColumns == 0 {
		capability.ReasonCode = "NO_PUBLIC_SOURCE_COLUMNS"
		capability.Reason = "coded grouping requires at least one public source column on the input stage"
		return capability
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		capability.ReasonCode = "FHIR_SCHEMA_UNAVAILABLE"
		capability.Reason = "generated FHIR Coding metadata is unavailable"
		return capability
	}
	hasCoding, err := index.HasRepeatedCodingPath(fhirschema.DefinitionName(rootResourceType))
	if err != nil {
		capability.ReasonCode = "FHIR_SCHEMA_UNAVAILABLE"
		capability.Reason = "generated FHIR Coding metadata is unavailable for the selected root"
		return capability
	}
	if !hasCoding {
		capability.ReasonCode = "NO_REPEATED_CODING_PATHS"
		capability.Reason = "the selected root has no generated repeated Coding fields"
		return capability
	}
	capability.Supported = true
	capability.ReasonCode = ""
	capability.Reason = ""
	return capability
}

func withConstructionCapability(capabilities []StageOperationCapability, replacement StageOperationCapability) []StageOperationCapability {
	for index := range capabilities {
		if capabilities[index].Operation == replacement.Operation {
			capabilities[index] = replacement
			return capabilities
		}
	}
	return append(capabilities, replacement)
}

func relatedExpandAnchors(schema []CompiledOutputColumn, rootResourceType string, rootContributorsAvailable bool) []CompiledRelatedExpandAnchor {
	anchors := make([]CompiledRelatedExpandAnchor, 0, 3)
	_, retainedRootKey := retainedRootResourceAnchor(schema, rootResourceType)
	if retainedRootKey && rootResourceType != "" {
		anchors = append(anchors, CompiledRelatedExpandAnchor{
			AnchorColumnID: "_key", Kind: "root", ResourceType: rootResourceType,
			Label: "Original " + rootResourceType,
		})
	}
	_, retainedRootContributorSet := rootContributorSet(schema, rootResourceType)
	if !retainedRootKey && (retainedRootContributorSet || rootContributorsAvailable) && rootResourceType != "" {
		anchors = append(anchors, CompiledRelatedExpandAnchor{
			AnchorColumnID: rootContributorSetColumn, Kind: "rootContributors", ResourceType: rootResourceType,
			Label: "Records that make up this row",
		})
	}
	if active, ok := activeRelatedRecordColumn(schema); ok {
		anchors = append(anchors, CompiledRelatedExpandAnchor{
			AnchorColumnID: active.Name, Kind: "activeRelatedRecord",
			NodeID: active.RelatedRecordAnchor.TargetNodeID, ResourceType: active.RelatedRecordAnchor.TargetResourceType,
			Label: "Current related " + active.RelatedRecordAnchor.TargetResourceType,
		})
	}
	return anchors
}

func retainedRootResourceAnchor(schema []CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType == rootResourceType && column.Kind == string(expression.KindString) &&
			column.Cardinality == string(expression.RequiredOne) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func activeRelatedRecordColumn(schema []CompiledOutputColumn) (CompiledOutputColumn, bool) {
	var active CompiledOutputColumn
	for _, column := range schema {
		if column.RelatedRecordAnchor == nil {
			continue
		}
		if active.Name != "" || !column.Internal || column.Identity || column.Kind != string(expression.KindString) ||
			(column.Cardinality != string(expression.RequiredOne) && column.Cardinality != string(expression.OptionalOne)) {
			return CompiledOutputColumn{}, false
		}
		active = column
	}
	return active, active.Name != ""
}

func preservesActiveRelatedRecord(operation ir.PhysicalStageOperationKind) bool {
	switch operation {
	case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedSourceOp, ir.PhysicalStageRelatedFieldOp:
		return true
	default:
		return false
	}
}

func stageInputProjections(schema []CompiledOutputColumn, row string) []ir.PhysicalProjection {
	projections := make([]ir.PhysicalProjection, 0, len(schema))
	for _, column := range schema {
		projections = append(projections, ir.PhysicalProjection{
			Name: column.Name, Hidden: column.Internal,
			Value: ir.PhysicalValue{Variable: row, Path: []string{column.Name}},
		})
	}
	return projections
}

func stagePassThroughProjections(outputs []recipe.StageColumn, input map[string]CompiledOutputColumn, row string) []ir.PhysicalProjection {
	projections := make([]ir.PhysicalProjection, 0, len(outputs))
	for _, output := range outputs {
		prior := input[output.ID]
		projections = append(projections, ir.PhysicalProjection{
			Name: output.Name, Value: ir.PhysicalValue{Variable: row, Path: []string{prior.Name}},
			Presence: &ir.PhysicalProjectionPresence{
				Source: ir.PhysicalValue{Variable: row}, Paths: [][]string{{prior.Name}},
			},
		})
	}
	return projections
}

func stagePassThroughAndDerivedProjections(outputs []recipe.StageColumn, input map[string]CompiledOutputColumn, row, derivedID, derivedName string, derived ir.PhysicalProjection) []ir.PhysicalProjection {
	projections := make([]ir.PhysicalProjection, 0, len(outputs))
	for _, output := range outputs {
		if output.ID == derivedID {
			derived.Name = derivedName
			projections = append(projections, derived)
			continue
		}
		prior := input[output.ID]
		projections = append(projections, ir.PhysicalProjection{
			Name: output.Name, Value: ir.PhysicalValue{Variable: row, Path: []string{prior.Name}},
			Presence: &ir.PhysicalProjectionPresence{
				Source: ir.PhysicalValue{Variable: row}, Paths: [][]string{{prior.Name}},
			},
		})
	}
	return projections
}

func stageProjectionMap(projections []ir.PhysicalProjection) map[string]ir.PhysicalProjection {
	result := make(map[string]ir.PhysicalProjection, len(projections))
	for _, projection := range projections {
		result[projection.Name] = projection
	}
	return result
}

func compiledSchemaByID(schema []CompiledOutputColumn) map[string]CompiledOutputColumn {
	result := make(map[string]CompiledOutputColumn, len(schema))
	for _, column := range schema {
		result[column.ID] = column
	}
	return result
}

func compiledSchemaByName(schema []CompiledOutputColumn) map[string]CompiledOutputColumn {
	result := make(map[string]CompiledOutputColumn, len(schema))
	for _, column := range schema {
		result[column.Name] = column
	}
	return result
}

func publicCompiledSchema(schema []CompiledOutputColumn) []CompiledOutputColumn {
	result := make([]CompiledOutputColumn, 0, len(schema))
	for _, column := range schema {
		if !column.Internal {
			result = append(result, column)
		}
	}
	return result
}

func toPhysicalStageColumns(schema []CompiledOutputColumn) []ir.PhysicalStageColumn {
	result := make([]ir.PhysicalStageColumn, 0, len(schema))
	for _, column := range schema {
		var relatedAnchor *ir.PhysicalStageRelatedRecordAnchor
		if column.RelatedRecordAnchor != nil {
			relatedAnchor = &ir.PhysicalStageRelatedRecordAnchor{
				NodeID: column.RelatedRecordAnchor.TargetNodeID, ResourceType: column.RelatedRecordAnchor.TargetResourceType,
			}
		}
		result = append(result, ir.PhysicalStageColumn{
			ID: column.ID, Name: column.Name, Label: column.Label,
			Kind: column.Kind, Cardinality: column.Cardinality, Nullable: column.Nullable,
			Internal: column.Internal, Identity: column.Identity,
			RelatedRecordAnchor:         relatedAnchor,
			RootContributorResourceType: column.RootContributorResourceType,
			NormalizedUnit:              ir.CloneUnitIdentity(column.NormalizedUnit),
			PresenceCompanionName:       column.PresenceCompanionName,
		})
	}
	return result
}

func constructionIdentitySchema(identity string, output, input []CompiledOutputColumn) CompiledOutputColumn {
	if column, ok := schemaColumn(input, identity); ok {
		column.ID, column.Name = identity, identity
		column.Internal, column.Identity = true, true
		return column
	}
	for _, column := range output {
		if column.Name == identity {
			column.ID, column.Internal, column.Identity = identity, true, true
			return column
		}
	}
	return CompiledOutputColumn{
		ID: identity, Name: identity, Label: identity, SemanticPath: "construction:row_identity",
		Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Internal: true, Identity: true,
	}
}

func constructionGeneratedRowIdentitySchema(identity string) CompiledOutputColumn {
	return CompiledOutputColumn{
		ID: identity, Name: identity, Label: identity, SemanticPath: "construction:row_identity",
		Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Internal: true, Identity: true,
	}
}

func insertProjectionBeforeIdentity(projections []ir.PhysicalProjection, projection ir.PhysicalProjection, identity string) []ir.PhysicalProjection {
	for index, existing := range projections {
		if existing.Name == identity {
			projections = append(projections, ir.PhysicalProjection{})
			copy(projections[index+1:], projections[index:])
			projections[index] = projection
			return projections
		}
	}
	return append(projections, projection)
}

func findProjection(operations []ir.PhysicalOperation, name string) (ir.PhysicalProjection, bool) {
	for _, operation := range operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == name {
				return projection, true
			}
		}
	}
	return ir.PhysicalProjection{}, false
}

func allocateConstructionVariable(used map[string]bool, kind string, index int) string {
	base := fmt.Sprintf("__loom_construction_%s_%d", kind, index+1)
	for suffix := 0; ; suffix++ {
		candidate := base
		if suffix > 0 {
			candidate = fmt.Sprintf("%s_%d", base, suffix)
		}
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func cloneCompiledSchema(schema []CompiledOutputColumn) []CompiledOutputColumn {
	return CloneCompiledOutputSchema(schema)
}
