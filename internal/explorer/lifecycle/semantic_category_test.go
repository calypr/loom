package lifecycle

import (
	"context"
	"reflect"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestCodedCategoryChoiceReauthorizesExactSystemVersionFamily(t *testing.T) {
	const (
		project    = "project-a"
		generation = "generation-a"
		explorerID = "medications"
	)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot(project, generation, "snapshot-a", scope)
	snapshot.Nodes = []capability.Node{{ID: "medication", ResourceType: "Medication", RowRootEligible: true}}
	candidate := capability.Candidate{
		ID: "medication-coded-display", NodeID: "medication", ResourceType: "Medication",
		FieldPath: "code.coding[].display", Label: "Medication code display", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 8}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
	}
	codeCandidate := candidate
	codeCandidate.ID = "medication-coded-code"
	codeCandidate.FieldPath = "code.coding[].code"
	codeCandidate.Label = "Medication code"
	snapshot.Candidates = []capability.Candidate{candidate, codeCandidate}
	family := testCodedCategoryFamily("file", "Medication", "Medication.code.coding[]", "https://www.ebi.ac.uk/chembl/compound_name", "v1")

	buildID := catalog.SemanticInventoryBuildID(project, generation)
	contextToken, err := semanticInventoryContextToken(snapshot, explorerID, "Medication", buildID)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := semanticCategoryConstructionChoiceForRoute(snapshot, contextToken, buildID, nil, family, candidate)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	choiceSource, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok || choiceSource.RuleHint != catalog.SemanticRuleHintCodedCategoryV1 || choiceSource.ConceptID != family.FamilyID ||
		choiceSource.BindingID != family.BindingID || choiceSource.SourceKind != family.SourceKind ||
		choiceSource.System != family.System || choiceSource.Version != family.Version || choiceSource.Code != "" {
		t.Fatalf("signed category source = %#v", identity.Source)
	}
	if label := semanticCategoryLabel(family, candidate); label != "Compound name" {
		t.Fatalf("ChEMBL category label = %q", label)
	}
	generic := testCodedCategoryFamily("file", "Specimen", "Specimen.type.coding[]", "https://cda.readthedocs.io/", "")
	if label := semanticCategoryLabel(generic, capability.Candidate{}); label != "Specimen type" {
		t.Fatalf("generic-system field label = %q", label)
	}
	hyphenatedSystem := testCodedCategoryFamily("file", "Specimen", "Specimen.type.coding[]", "https://example.org/my-code-system", "")
	if label := semanticCategoryLabel(hyphenatedSystem, capability.Candidate{}); label != "My code system" {
		t.Fatalf("meaningful system-tail label = %q", label)
	}

	fieldCandidate, ok := semanticCandidateWithFallback(snapshot, semanticInventoryEntryForFamily(family).Observation, candidate)
	if !ok {
		t.Fatal("category display/code fallback candidate was not compiler-proved")
	}
	fieldChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, nil, fieldCandidate)
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := authoringv2.CatalogSnapshot{
		SnapshotToken: snapshot.Token,
		Candidates: []authoringv2.CatalogCandidate{{
			ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath,
			Cardinality: candidate.Cardinality, ConstructionChoice: &fieldChoice,
		}},
	}
	resolved, _, err := resolveSemanticConstructionChoice(
		context.Background(), AuthorizedCapability{Snapshot: snapshot, Scope: scope}, snapshot, catalogSnapshot,
		explorerID, "Medication", contextToken, buildID, buildID,
		map[string]catalog.SemanticInventoryEntry{}, map[string]catalog.SemanticInventoryFamily{semanticChoiceKey(family.FamilyID, family.BindingID): family},
		authoringv2.ConstructionChoiceSelection{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceAll}, identity, choiceSource,
	)
	if err != nil {
		t.Fatalf("reauthorize signed category: %v", err)
	}
	if resolved.Source.Lookup == nil || resolved.Source.Lookup.Binding == nil || resolved.Source.Lookup.Key == nil {
		t.Fatalf("resolved category source = %#v", resolved.Source)
	}
	binding := resolved.Source.Lookup.Binding
	key := resolved.Source.Lookup.Key
	if binding.SelectionScope != fhirschema.CorrelatedSelectionScopeSystemVersion || binding.ValueScope != fhirschema.CorrelatedValueScopeCoding ||
		binding.VersionPath != "version" || binding.CodePath != "code" || binding.ValuePath != "display" ||
		!reflect.DeepEqual(binding.ValueFallback, []string{"code"}) || !binding.FallbackOnBlankString ||
		key.System != family.System || key.Version != family.Version || key.Code != "" || resolved.Source.Lookup.ProjectionMode != string(capability.ConstructionChoiceAll) {
		t.Fatalf("resolved system-version source = %#v", resolved.Source)
	}
}

func TestIdentifierSemanticItemLabelIncludesItsNamespace(t *testing.T) {
	first := semanticIdentifierLabel("https://example.org/identifier/patient")
	second := semanticIdentifierLabel("https://example.org/identifier/provider")
	if first == "" || second == "" || first == second || first != "Identifier · https://example.org/identifier/patient" {
		t.Fatalf("identifier labels = %q / %q", first, second)
	}
}

func testCodedCategoryFamily(sourceKind, resourceType, sourcePath, system, version string) catalog.SemanticInventoryFamily {
	reference := catalog.SemanticInventoryFamilyReference{
		BindingID: "binding-" + resourceType, SourceKind: sourceKind, ResourceType: resourceType,
		SourcePath: sourcePath, System: system, Version: version,
	}
	reference.FamilyID = catalog.SemanticInventoryFamilyID(reference)
	return catalog.SemanticInventoryFamily{
		SemanticInventoryFamilyReference: reference,
		Source:                           catalog.SemanticObservationSource{Type: resourceType, Path: sourcePath},
		KeySelector:                      "code.coding[]",
		Value: catalog.SemanticObservationValue{
			Selector: "code.coding[].display", Type: "string",
			FallbackSelectors: []string{"code.coding[].code"}, FallbackOnBlankString: true,
		},
		LogicalType: "string", SchemaVersion: catalog.SemanticObservationSchemaVersion,
		RuleVersion:  strconv.Itoa(catalog.SemanticObservationRuleVersion),
		Completeness: catalog.SemanticComplete, Status: "SUPPORTED", Population: 2,
		Examples: []string{"CARBOXYAMIDOTRIAZOLE", "another compound"},
	}
}
