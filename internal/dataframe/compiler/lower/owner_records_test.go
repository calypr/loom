package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestOwnerRecordsLowerToOneTypedArrayProjection(t *testing.T) {
	binding := fhirschema.CorrelatedBinding{
		OwnerPath:   "component[]",
		KeyPath:     "component[].code.coding[]",
		SystemPath:  "system",
		CodePath:    "code",
		ValuePath:   "valueQuantity.value",
		ChoiceArms:  []string{"valueQuantity"},
		LogicalType: "decimal",
		UnitPath:    "valueQuantity.unit",
	}
	plan, err := BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias:        "root",
		ResourceType: "Observation",
		OwnerRecords: []semantic.SemanticOwnerRecords{{
			Name:     "height_records",
			FieldRef: "component",
			Binding:  binding,
			Key:      fhirschema.CorrelatedKey{System: "http://loinc.org", Code: "8302-2"},
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("owner-record plan validation: %v", err)
	}

	var projection *ir.PhysicalExpression
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, candidate := range operation.Return.Projections {
			if candidate.Name == "height_records" {
				projection = candidate.Expression
			}
		}
	}
	if projection == nil || projection.Kind != ir.PhysicalOwnerRecordsExpression || projection.OwnerRecords == nil {
		t.Fatalf("height_records projection = %#v, want OWNER_RECORDS", projection)
	}
	if projection.Cardinality != ir.PhysicalArrayCardinality || projection.NullBehavior != ir.PhysicalEmptyOnNull {
		t.Fatalf("height_records shape = %s/%s, want ARRAY/EMPTY_ON_NULL", projection.Cardinality, projection.NullBehavior)
	}
	correlation := projection.OwnerRecords.Correlation
	if correlation.OwnerSelector.CanonicalPath() != "component[]" || correlation.UnitSelector == nil || correlation.UnitSelector.CanonicalPath() != "valueQuantity.unit" {
		t.Fatalf("owner-record correlation = %#v", correlation)
	}

	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"ownerOrdinal", "matching_codings", "INVALID_CHOICE_ARM", "INVALID_MULTIPLE_VALUES", "ABSENT", "valueQuantity"} {
		if !strings.Contains(rendered.Query, fragment) {
			t.Fatalf("owner-record query is missing %q:\n%s", fragment, rendered.Query)
		}
	}
	if rendered.BindVars[projection.OwnerRecords.OwnerPathBindKey] != "component[]" || rendered.BindVars[projection.OwnerRecords.ChoiceArmBindKey] != "valueQuantity" || rendered.BindVars[projection.OwnerRecords.LogicalTypeBindKey] != "decimal" {
		t.Fatalf("owner-record binds = %#v", rendered.BindVars)
	}
}

func TestOwnerRecordsPlanRejectsMissingMetadataBind(t *testing.T) {
	binding := fhirschema.CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		SystemPath: "system", CodePath: "code", ValuePath: "valueString", LogicalType: "string",
	}
	plan, err := BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation",
		OwnerRecords: []semantic.SemanticOwnerRecords{{Name: "records", FieldRef: "component", Binding: binding, Key: fhirschema.CorrelatedKey{System: "urn:test", Code: "code"}}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var metadataBind string
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Expression != nil && projection.Expression.OwnerRecords != nil {
				metadataBind = projection.Expression.OwnerRecords.LogicalTypeBindKey
			}
		}
	}
	delete(plan.BindVars, metadataBind)
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "is not defined") {
		t.Fatalf("validation error = %v, want undefined bind", err)
	}
}
