package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func lowerConstructionCodedGroup(
	plan *ir.PhysicalPlan,
	group recipe.ConstructionCodedGroup,
	declarations []recipe.StageColumn,
	inputIdentity string,
	rootResourceType string,
	usedVariables map[string]bool,
	stepIndex int,
) (ir.PhysicalStageCodedGroup, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	if inputIdentity != "_key" {
		return ir.PhysicalStageCodedGroup{}, nil, nil, fmt.Errorf("CODED_GROUP requires direct root rows identified by _key")
	}
	if err := constructionCodedGroupSourceRowsUnique(plan); err != nil {
		return ir.PhysicalStageCodedGroup{}, nil, nil, err
	}
	if group.Source.ResourceType != rootResourceType {
		return ir.PhysicalStageCodedGroup{}, nil, nil, fmt.Errorf("CODED_GROUP source resource %q does not match the selected root %q", group.Source.ResourceType, rootResourceType)
	}
	if len(plan.Operations) == 0 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return ir.PhysicalStageCodedGroup{}, nil, nil, fmt.Errorf("CODED_GROUP requires a direct root resource scan")
	}
	rootCollectionBindKey := plan.Operations[0].RootScan.CollectionBindKey
	if rootCollectionBindKey == "" || plan.BindVars[rootCollectionBindKey] != rootResourceType {
		return ir.PhysicalStageCodedGroup{}, nil, nil, fmt.Errorf("CODED_GROUP root collection does not match selected root resource %q", rootResourceType)
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return ir.PhysicalStageCodedGroup{}, nil, nil, fmt.Errorf("load generated FHIR schema: %w", err)
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(rootResourceType), group.Source.CodingPath)
	if err != nil {
		return ir.PhysicalStageCodedGroup{}, nil, nil, fmt.Errorf("resolve CODED_GROUP Coding path: %w", err)
	}
	if facts.ResourceType != fhirschema.DefinitionName(rootResourceType) || facts.CanonicalPath != group.Source.CodingPath ||
		facts.FHIRType != "Coding" || facts.Cardinality != fhirschema.RowCardinalityMany || facts.Shape != fhirschema.RowPathArray || facts.Reference {
		return ir.PhysicalStageCodedGroup{}, nil, nil, fmt.Errorf("CODED_GROUP path %q is not an executable generated repeated Coding path on %s", group.Source.CodingPath, rootResourceType)
	}
	paths, err := index.RepeatedCodingPaths(fhirschema.DefinitionName(rootResourceType))
	if err != nil {
		return ir.PhysicalStageCodedGroup{}, nil, nil, fmt.Errorf("enumerate generated Coding paths: %w", err)
	}
	found := false
	for _, path := range paths {
		if path == group.Source.CodingPath {
			found = true
			break
		}
	}
	if !found {
		return ir.PhysicalStageCodedGroup{}, nil, nil, fmt.Errorf("CODED_GROUP path %q is no longer advertised by the generated FHIR schema", group.Source.CodingPath)
	}
	segments, err := parseCodedGroupPath(group.Source.CodingPath)
	if err != nil {
		return ir.PhysicalStageCodedGroup{}, nil, nil, err
	}
	if len(declarations) != 4 {
		return ir.PhysicalStageCodedGroup{}, nil, nil, fmt.Errorf("CODED_GROUP requires exactly four declared output columns")
	}
	outputs := make(map[string]recipe.StageColumn, len(declarations))
	for _, declaration := range declarations {
		outputs[declaration.ID] = declaration
	}
	columnIDs := []string{
		group.SystemOutputColumnID,
		group.VersionOutputColumnID,
		group.CodeOutputColumnID,
		group.DistinctSourceCountOutputColumnID,
	}
	columnNames := make([]string, len(columnIDs))
	for index, id := range columnIDs {
		column, ok := outputs[id]
		if !ok {
			return ir.PhysicalStageCodedGroup{}, nil, nil, fmt.Errorf("CODED_GROUP output column ID %q is missing from its declared schema", id)
		}
		columnNames[index] = column.Name
	}
	constructionIDBindKey := nextTableReshapeBindKey(plan.BindVars, "construction_coded_group_id")
	occurrenceIDBindKey := nextTableReshapeBindKey(plan.BindVars, "construction_coded_group_occurrence")
	codingPathBindKey := nextTableReshapeBindKey(plan.BindVars, "construction_coded_group_path")
	plan.BindVars[constructionIDBindKey] = group.ConstructionID
	plan.BindVars[occurrenceIDBindKey] = group.Source.OccurrenceID
	plan.BindVars[codingPathBindKey] = group.Source.CodingPath
	physical := ir.PhysicalStageCodedGroup{
		RootCollectionBindKey: rootCollectionBindKey,
		ConstructionIDBindKey: constructionIDBindKey,
		OccurrenceIDBindKey:   occurrenceIDBindKey,
		CodingPathBindKey:     codingPathBindKey,
		ResourceType:          rootResourceType,
		SourceIdentityColumn:  inputIdentity,
		SourceRowsUnique:      true,
		CodingPath:            group.Source.CodingPath,
		PathSegments:          segments,
		MissingKeyPolicy:      ir.PhysicalStageGroupMissingKeyPolicy(group.MissingKeyPolicy.Normalized()),
		SystemOutputColumn:    columnNames[0],
		VersionOutputColumn:   columnNames[1],
		CodeOutputColumn:      columnNames[2],
		CountOutputColumn:     columnNames[3],
		SystemVariable:        allocateConstructionVariable(usedVariables, "coded_group_system", stepIndex),
		VersionVariable:       allocateConstructionVariable(usedVariables, "coded_group_version", stepIndex),
		CodeVariable:          allocateConstructionVariable(usedVariables, "coded_group_code", stepIndex),
		CountVariable:         allocateConstructionVariable(usedVariables, "coded_group_count", stepIndex),
		IdentityVariable:      allocateConstructionVariable(usedVariables, "coded_group_identity", stepIndex),
	}
	compiled := make([]CompiledOutputColumn, 0, len(declarations))
	projections := make([]ir.PhysicalProjection, 0, len(declarations)+1)
	metadata := []struct {
		id       string
		name     string
		label    string
		variable string
		kind     string
		cardinal string
		nullable bool
	}{
		{group.SystemOutputColumnID, columnNames[0], outputs[group.SystemOutputColumnID].Label, physical.SystemVariable, string(expression.KindString), string(expression.OptionalOne), true},
		{group.VersionOutputColumnID, columnNames[1], outputs[group.VersionOutputColumnID].Label, physical.VersionVariable, string(expression.KindString), string(expression.OptionalOne), true},
		{group.CodeOutputColumnID, columnNames[2], outputs[group.CodeOutputColumnID].Label, physical.CodeVariable, string(expression.KindString), string(expression.OptionalOne), true},
		{group.DistinctSourceCountOutputColumnID, columnNames[3], outputs[group.DistinctSourceCountOutputColumnID].Label, physical.CountVariable, string(expression.KindInteger), string(expression.RequiredOne), false},
	}
	for _, output := range metadata {
		compiled = append(compiled, CompiledOutputColumn{
			ID: output.id, Name: output.name, Label: constructionFirstNonEmpty(output.label, output.name),
			SemanticPath: "construction_coded_group:" + group.ConstructionID + ":" + group.Source.CodingPath,
			Kind:         output.kind, Cardinality: output.cardinal, Nullable: output.nullable,
		})
		projections = append(projections, ir.PhysicalProjection{Name: output.name, Value: ir.PhysicalValue{Variable: output.variable}})
	}
	projections = append(projections, ir.PhysicalProjection{
		Name: constructionRowID, Hidden: true, Value: ir.PhysicalValue{Variable: physical.IdentityVariable},
	})
	return physical, projections, compiled, nil
}

func constructionCodedGroupSourceRowsUnique(plan *ir.PhysicalPlan) error {
	if plan == nil || len(plan.Operations) < 2 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return fmt.Errorf("CODED_GROUP requires a direct root scan with one row per root identity")
	}
	returns := 0
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 || operation.RootScan == nil {
				return fmt.Errorf("CODED_GROUP source must contain exactly one direct root scan")
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp,
			ir.PhysicalSetOp, ir.PhysicalSortOp, ir.PhysicalLimitOp:
			// These operations can remove or annotate rows but cannot add another
			// row for the same root identity.
		case ir.PhysicalReturnOp:
			returns++
			if operation.Return == nil || index != len(plan.Operations)-1 {
				return fmt.Errorf("CODED_GROUP source must have one terminal root projection")
			}
		default:
			return fmt.Errorf("CODED_GROUP cannot stream-count source rows after cardinality-changing operation %q", operation.Kind)
		}
	}
	if returns != 1 {
		return fmt.Errorf("CODED_GROUP source must have one terminal root projection")
	}
	return nil
}

func parseCodedGroupPath(path string) ([]ir.PhysicalCodedGroupPathSegment, error) {
	parts := strings.Split(path, ".")
	segments := make([]ir.PhysicalCodedGroupPathSegment, 0, len(parts))
	for _, part := range parts {
		repeated := strings.HasSuffix(part, "[]")
		name := strings.TrimSuffix(part, "[]")
		if name == "" || strings.ContainsAny(name, "[]") {
			return nil, fmt.Errorf("CODED_GROUP path %q contains an invalid segment", path)
		}
		for index, char := range name {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && char != '_' && (index == 0 || char < '0' || char > '9') {
				return nil, fmt.Errorf("CODED_GROUP path %q contains an invalid field name", path)
			}
		}
		segments = append(segments, ir.PhysicalCodedGroupPathSegment{Name: name, Repeated: repeated})
	}
	if len(segments) == 0 || !segments[len(segments)-1].Repeated {
		return nil, fmt.Errorf("CODED_GROUP path %q must end in a repeated Coding array", path)
	}
	return segments, nil
}
