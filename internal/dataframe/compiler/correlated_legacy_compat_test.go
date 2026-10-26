package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestCorrelatedPhysicalLegacyAQLRemainsByteIdentical(t *testing.T) {
	binding := &fhirschema.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"}
	projection, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{Name: "shared", Columns: []string{"shared"}, Correlation: binding, CorrelationSystem: "urn:study:A", CorrelationCode: "shared"}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	projectionQuery, err := aql.RenderPhysicalPlan(projection)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Filters: []spec.TypedFilter{{
			FieldRef: "Observation.component_code", Selector: "code.coding[].code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals,
			Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: "urn:study:A", Code: "shared"}}}, Correlation: binding,
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	filterQuery, err := aql.RenderPhysicalPlan(filter)
	if err != nil {
		t.Fatal(err)
	}
	projectionSum := sha256.Sum256([]byte(projectionQuery.Query))
	filterSum := sha256.Sum256([]byte(filterQuery.Query))
	projectionHash := hex.EncodeToString(projectionSum[:])
	filterHash := hex.EncodeToString(filterSum[:])
	if projectionHash != "04e466b6c8409b967500151a357b5edf7bb061be45805474efa3bf4b11c33984" {
		t.Fatalf("legacy owner-scope projection AQL changed: sha256=%s", projectionHash)
	}
	if filterHash != "79d08485beae511eb07f5c18a925a3ff16709c37f1f48136b3274bcd2f371fdd" {
		t.Fatalf("legacy owner-scope filter AQL changed: sha256=%s", filterHash)
	}
}
