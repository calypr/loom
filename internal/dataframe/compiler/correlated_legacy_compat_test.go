package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"testing"

	fhirbinding "github.com/calypr/loom/internal/fhir/binding"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestCorrelatedPhysicalScalarSelectorAQLFingerprint(t *testing.T) {
	binding := &fhirbinding.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"}
	projection, err := lower.BuildGenericPhysicalPlanWithPolicy(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Observation", Pivots: []recipeplan.SemanticPivot{{Name: "shared", Columns: []string{"shared"}, Correlation: binding, CorrelationSystem: "urn:study:A", CorrelationCode: "shared"}},
	}}, recipeplan.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	projectionQuery, err := aql.RenderPhysicalPlan(projection)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := lower.BuildGenericPhysicalPlanWithPolicy(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Observation", Filters: []spec.TypedFilter{{
			FieldRef: "Observation.component_code", Selector: "code.coding[].code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals,
			Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: "urn:study:A", Code: "shared"}}}, Correlation: binding,
		}},
	}}, recipeplan.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	filterQuery, err := aql.RenderPhysicalPlan(filter)
	if err != nil {
		t.Fatal(err)
	}
	projectionAQL := restoreSingletonRootLoops(t, projectionQuery.Query, 3)
	filterAQL := restoreSingletonRootLoops(t, filterQuery.Query, 3)
	projectionSum := sha256.Sum256([]byte(projectionAQL))
	filterSum := sha256.Sum256([]byte(filterAQL))
	projectionHash := hex.EncodeToString(projectionSum[:])
	filterHash := hex.EncodeToString(filterSum[:])
	if projectionHash != "9fb070ed419c3d25ca187ad614f9867d381c8fcefadae2ab00e3cb3d3b46ecc9" {
		t.Fatalf("legacy owner-scope projection AQL changed: sha256=%s", projectionHash)
	}
	if filterHash != "9326ffa1a698d806ffd5e0ca0e52139db3309dbcec65caaf3de14367f8ee6d7e" {
		t.Fatalf("legacy owner-scope filter AQL changed: sha256=%s", filterHash)
	}
}

var singletonRootLet = regexp.MustCompile(`(?m)^([\t ]*)LET __root = ([A-Za-z_][A-Za-z0-9_.]*)$`)

func restoreSingletonRootLoops(t *testing.T, query string, want int) string {
	t.Helper()
	matches := singletonRootLet.FindAllStringSubmatchIndex(query, -1)
	if len(matches) != want {
		t.Fatalf("singleton root normalization matched %d bindings, want exactly %d", len(matches), want)
	}
	return singletonRootLet.ReplaceAllString(query, `${1}FOR __root IN [${2}]`)
}
