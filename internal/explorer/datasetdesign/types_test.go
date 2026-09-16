package datasetdesign

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

func testCatalog() authoringv2.CatalogSnapshot {
	return authoringv2.CatalogSnapshot{
		APIVersion:               authoringv2.APIVersion,
		Kind:                     authoringv2.CatalogKind,
		Project:                  "project-a",
		ExplorerID:               "explorer-a",
		SourceGeneration:         "generation-a",
		AuthorizationScopeDigest: "scope-a",
		SnapshotToken:            "snapshot-a",
		Complete:                 true,
		Nodes: []authoringv2.CatalogNode{
			{ID: "patient-node", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"},
			{ID: "encounter-node", ResourceType: "Encounter", RowRootEligible: true, RowGrain: "resource"},
		},
		Candidates: []authoringv2.CatalogCandidate{
			{ID: "patient-id", NodeID: "patient-node", FieldPath: "id", Label: "Patient ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE"},
			{ID: "patient-gender", NodeID: "patient-node", FieldPath: "gender", Label: "Gender", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE"},
			{ID: "encounter-status", NodeID: "encounter-node", FieldPath: "status", Label: "Status", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE"},
		},
		RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
	}
}

func testDesign() DatasetDesignV1 {
	return DatasetDesignV1{
		Version: Version1,
		Title:   "Patient training table",
		Grain:   RowGrain{Kind: GrainPatient, NodeID: "patient-node"},
		Features: []FeatureDefinition{
			{Key: "patient_id", Label: "Patient ID", Role: RoleIdentifier, MissingMeaning: MissingNotApplicable, Source: FeatureSource{Kind: SourceRootValue, Field: FieldRef{CandidateID: "patient-id"}, Projection: ProjectionValue}},
			{Key: "gender", Label: "Gender", Role: RoleFeature, MissingMeaning: MissingUnknown, Source: FeatureSource{Kind: SourceRootValue, Field: FieldRef{CandidateID: "patient-gender"}, Projection: ProjectionValue}},
		},
	}
}

func testCapabilitySnapshot() capability.Snapshot {
	return capability.Snapshot{
		Identity: capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope-a", SchemaDigest: "schema-a"},
		Policy:   capability.Policy{Route: capability.RoutePolicy{AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: capability.ProjectionPolicy{Modes: []capability.ProjectionMode{capability.ProjectionScalar}}},
		Status:   capability.StatusReady,
		Complete: true,
		Token:    "capability-a",
		Nodes: []capability.Node{
			{ID: "patient-node", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"},
		},
		Candidates: []capability.Candidate{
			{ID: "patient-id", NodeID: "patient-node", ResourceType: "Patient", FieldPath: "id", Label: "Patient ID", LogicalType: "string", Cardinality: "scalar", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
			{ID: "patient-gender", NodeID: "patient-node", ResourceType: "Patient", FieldPath: "gender", Label: "Gender", LogicalType: "string", Cardinality: "scalar", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
		},
	}
}

func TestPatientGenderDesignRoundTripsAndOmitsCompilerVocabulary(t *testing.T) {
	design := testDesign()
	if err := design.ValidateAgainst(testCatalog()); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(design)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, forbidden := range []string{"fieldPath", "expression", "recipe", "aql"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("design leaked compiler vocabulary %q: %s", forbidden, encoded)
		}
	}
	var decoded DatasetDesignV1
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(design, decoded) {
		t.Fatalf("decoded design = %#v, want %#v", decoded, design)
	}
}

func TestDigestTreatsFeatureOrderAsSignificant(t *testing.T) {
	first := testDesign()
	second := testDesign()
	firstDigest, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("same feature order changed digest: %q != %q", firstDigest, secondDigest)
	}
	if !strings.HasPrefix(firstDigest, "sha256:") {
		t.Fatalf("digest = %q", firstDigest)
	}
	canonical, err := first.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	if firstDigest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("digest does not match canonical design: %q", firstDigest)
	}
	reordered := testDesign()
	reordered.Features[0], reordered.Features[1] = reordered.Features[1], reordered.Features[0]
	reorderedDigest, err := reordered.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if reorderedDigest == firstDigest {
		t.Fatal("feature order did not change design identity")
	}
}

func TestDecodeRejectsUnknownVariantsAndRawPaths(t *testing.T) {
	valid := `{"version":1,"title":"Patients","grain":{"kind":"patient","nodeId":"patient-node"},"features":[{"key":"gender","label":"Gender","role":"feature","missingMeaning":"unknown","source":{"kind":"root-value","field":{"candidateId":"patient-gender"},"projection":"VALUE"}}]}`
	for name, raw := range map[string]string{
		"unknown source variant": strings.Replace(valid, `"root-value"`, `"expression"`, 1),
		"raw path":               strings.Replace(valid, `"candidateId":"patient-gender"`, `"candidateId":"patient-gender","path":"gender"`, 1),
		"unknown field":          strings.Replace(valid, `"projection":"VALUE"`, `"projection":"VALUE","recipe":"id"`, 1),
		"duplicate key":          strings.Replace(valid, `"title":"Patients"`, `"title":"Patients","title":"Other"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			var got DatasetDesignV1
			if err := json.Unmarshal([]byte(raw), &got); err == nil {
				t.Fatalf("accepted invalid design: %s", raw)
			}
		})
	}
}

func TestValidateAgainstRejectsInvalidCatalogReferences(t *testing.T) {
	tests := map[string]func(*authoringv2.CatalogSnapshot){
		"missing candidate": func(catalog *authoringv2.CatalogSnapshot) {
			design := testDesign()
			design.Features[0].Source.Field.CandidateID = "missing"
			if err := design.ValidateAgainst(*catalog); err == nil {
				t.Fatal("accepted missing candidate")
			}
		},
		"wrong grain": func(catalog *authoringv2.CatalogSnapshot) {
			design := testDesign()
			design.Grain.NodeID = "encounter-node"
			if err := design.ValidateAgainst(*catalog); err == nil {
				t.Fatal("accepted non-Patient grain")
			}
		},
		"wrong ownership": func(catalog *authoringv2.CatalogSnapshot) {
			design := testDesign()
			design.Features[0].Source.Field.CandidateID = "encounter-status"
			if err := design.ValidateAgainst(*catalog); err == nil {
				t.Fatal("accepted candidate from another node")
			}
		},
		"repeated candidate": func(catalog *authoringv2.CatalogSnapshot) {
			design := testDesign()
			catalog.Candidates[0].Repeated = true
			if err := design.ValidateAgainst(*catalog); err == nil {
				t.Fatal("accepted repeated root value")
			}
		},
	}
	for name, check := range tests {
		t.Run(name, func(t *testing.T) {
			catalog := testCatalog()
			check(&catalog)
		})
	}
}

func TestLoweredPatientGenderCompilesLikeOrdinaryV2(t *testing.T) {
	design := testDesign()
	catalog := testCatalog()
	workspace, err := design.LowerToWorkspace(catalog, LowerOptions{OutputID: "patients"})
	if err != nil {
		t.Fatal(err)
	}
	if workspace.DatasetDesignDigest == "" || len(workspace.DatasetDesign) == 0 {
		t.Fatalf("lowered workspace did not retain design identity: %#v", workspace)
	}
	capabilitySnapshot := testCapabilitySnapshot()
	lowered, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, capabilitySnapshot, explorercompilation.ResolvedInputs{})
	if err != nil {
		t.Fatal(err)
	}
	visible := true
	ordinary := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion,
		Kind:       authoringv2.WorkspaceKind,
		Explorer:   authoringv2.ExplorerMetadata{Title: "Patient training table"},
		Documents: []authoringv2.Document{{
			Kind:             authoringv2.Kind,
			Output:           authoringv2.Output{ID: "patients", Title: "Patient training table"},
			RootResourceType: "Patient",
			Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
			Columns: []authoringv2.Column{
				{Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
				{Column: "gender", Label: "Gender", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "gender", ProjectionMode: "VALUE"}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
			},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients_tab", Title: "Patient training table", OutputID: "patients", Visible: true}},
	}
	ordinaryResult, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "explorer-a", ordinary, capabilitySnapshot, explorercompilation.ResolvedInputs{})
	if err != nil {
		t.Fatal(err)
	}
	loweredBundleDigest, err := lowered.Bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ordinaryBundleDigest, err := ordinaryResult.Bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if loweredBundleDigest != ordinaryBundleDigest {
		t.Fatalf("lowered recipe differs from ordinary V2 recipe: %q != %q", loweredBundleDigest, ordinaryBundleDigest)
	}
	if !reflect.DeepEqual(lowered.OutputContracts, ordinaryResult.OutputContracts) {
		t.Fatalf("lowered output contracts differ:\nlowered=%#v\nordinary=%#v", lowered.OutputContracts, ordinaryResult.OutputContracts)
	}
}
