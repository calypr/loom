package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAddOfficialEdgesUsesStructureDefinitionTargetsAndPaths(t *testing.T) {
	definitionsPath := writeOfficialEdgeDefinitions(t)
	schema := officialEdgeFixtureSchema()

	if err := addOfficialEdges(schema, definitionsPath); err != nil {
		t.Fatal(err)
	}

	got := make(map[string]Link)
	for _, link := range schema.Defs["Observation"].Links {
		got[link.Rel+"|"+refName(link.TargetSchema.Ref)] = link
	}
	want := map[string]string{
		"subject_Device|Device":                           "/subject/reference",
		"subject_Patient|Patient":                         "/subject/reference",
		"dataReference|Group":                             "/dataReference/reference",
		"component_reference|Group":                       "/component/-/reference/reference",
		"medication|Medication":                           "/medication/reference/reference",
		"generic_Device|Device":                           "/generic/reference/reference",
		"generic_Group|Group":                             "/generic/reference/reference",
		"generic_Medication|Medication":                   "/generic/reference/reference",
		"generic_Observation|Observation":                 "/generic/reference/reference",
		"generic_Organization|Organization":               "/generic/reference/reference",
		"generic_Patient|Patient":                         "/generic/reference/reference",
		"identifier_assigner|Organization":                "/identifier/-/assigner/reference",
		"recursive_reference|Patient":                     "/recursive/reference/reference",
		"supportingInformation_Device|Device":             "/supportingInformation/reference",
		"supportingInformation_Group|Group":               "/supportingInformation/reference",
		"supportingInformation_Medication|Medication":     "/supportingInformation/reference",
		"supportingInformation_Observation|Observation":   "/supportingInformation/reference",
		"supportingInformation_Organization|Organization": "/supportingInformation/reference",
		"supportingInformation_Patient|Patient":           "/supportingInformation/reference",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d edge rules, want %d. Rules: %#v", len(got), len(want), got)
	}
	for key, path := range want {
		link, ok := got[key]
		if !ok {
			t.Errorf("missing edge rule %q, got %#v", key, got)
			continue
		}
		if link.TemplatePointers.ID != path {
			t.Errorf("%s path = %q, want %q", key, link.TemplatePointers.ID, path)
		}
		if !reflect.DeepEqual(link.TargetHints.Direction, []string{"outbound"}) {
			t.Errorf("%s direction = %#v", key, link.TargetHints.Direction)
		}
		if len(link.TargetHints.Backref) != 0 {
			t.Errorf("%s unexpectedly creates reverse edges: %#v", key, link.TargetHints.Backref)
		}
	}
	for _, key := range []string{"component_reference|Group", "identifier_assigner|Organization"} {
		if got[key].TargetHints.Multiplicity[0] != "has_many" {
			t.Errorf("%s multiplicity = %#v, want has_many", key, got[key].TargetHints.Multiplicity)
		}
	}
	if _, ok := got["unsupported_Procedure|Procedure"]; ok {
		t.Fatal("edge to a resource outside the generated roots was emitted")
	}
	for _, link := range schema.Defs["Observation"].Links {
		if link.TemplatePointers.ID == "/recursive/next/reference/reference" || strings.HasPrefix(link.TemplatePointers.ID, "/extension/") {
			t.Errorf("edge rule unexpectedly follows unsupported recursive path %q", link.TemplatePointers.ID)
		}
	}
}

func TestOfficialReferenceTargetSuffixIsChosenBeforeScopeFiltering(t *testing.T) {
	profiles := []string{fhirDefinitionBase + "/Patient", fhirDefinitionBase + "/Procedure"}
	got := officialReferenceTargets(profiles, map[string]struct{}{"Patient": {}})
	if !got.multiple || !reflect.DeepEqual(got.types, []string{"Patient"}) {
		t.Fatalf("target set = %#v, want one in-scope target with multiple-source marker", got)
	}
	if label := officialEdgeLabel([]string{"subject"}, "Patient", got.multiple); label != "subject_Patient" {
		t.Fatalf("filtered target label = %q, want subject_Patient", label)
	}
}

func officialEdgeFixtureSchema() *Schema {
	root := func(name string, properties map[string]*Property) *Definition {
		properties["resourceType"] = &Property{Type: "string", Const: name}
		properties["id"] = &Property{Type: "string"}
		properties["meta"] = &Property{Ref: "#/$defs/Meta"}
		return &Definition{Type: "object", Properties: properties}
	}
	return &Schema{ID: fhirDefinitionBase, Defs: map[string]*Definition{
		"Observation": root("Observation", map[string]*Property{
			"subject":               {Ref: "#/$defs/Reference"},
			"dataReference":         {Ref: "#/$defs/Reference"},
			"component":             {Type: "array", Items: &Property{Ref: "#/$defs/ObservationComponent"}},
			"medication":            {Ref: "#/$defs/CodeableReference"},
			"generic":               {Ref: "#/$defs/CodeableReference"},
			"identifier":            {Type: "array", Items: &Property{Ref: "#/$defs/Identifier"}},
			"supportingInformation": {Ref: "#/$defs/Reference"},
			"extension":             {Type: "array", Items: &Property{Ref: "#/$defs/Extension"}},
			"recursive":             {Ref: "#/$defs/Recursive"},
		}),
		"Patient":      root("Patient", map[string]*Property{}),
		"Device":       root("Device", map[string]*Property{}),
		"Group":        root("Group", map[string]*Property{}),
		"Medication":   root("Medication", map[string]*Property{}),
		"Organization": root("Organization", map[string]*Property{}),
		"ObservationComponent": {
			Type: "object", Properties: map[string]*Property{"reference": {Ref: "#/$defs/Reference"}},
		},
		"Identifier": {
			Type: "object", Properties: map[string]*Property{"assigner": {Ref: "#/$defs/Reference"}},
		},
		"CodeableReference": {
			Type: "object", Properties: map[string]*Property{"reference": {Ref: "#/$defs/Reference"}},
		},
		"Reference": {
			Type: "object", Properties: map[string]*Property{"reference": {Type: "string"}},
		},
		"Extension": {
			Type: "object", Properties: map[string]*Property{"valueReference": {Ref: "#/$defs/Reference"}},
		},
		"Recursive": {
			Type: "object", Properties: map[string]*Property{
				"reference": {Ref: "#/$defs/Reference"},
				"next":      {Ref: "#/$defs/Recursive"},
			},
		},
		"Meta": {Type: "object", Properties: map[string]*Property{}},
	}}
}

func writeOfficialEdgeDefinitions(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	write := func(name string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", map[string]string{"name": "hl7.fhir.r5.core", "version": "5.0.0"})
	for _, resource := range []string{"Patient", "Device", "Group", "Medication", "Organization"} {
		write("StructureDefinition-"+resource+".json", officialEdgeStructure(resource, "resource", []fhirElementDefinition{{Path: resource}}))
	}
	write("StructureDefinition-Observation.json", officialEdgeStructure("Observation", "resource", []fhirElementDefinition{
		{Path: "Observation"},
		{Path: "Observation.subject", Max: "1", Type: []fhirElementType{{Code: "Reference", TargetProfile: []string{fhirDefinitionBase + "/Patient", fhirDefinitionBase + "/Device"}}}},
		{Path: "Observation.data[x]", Max: "1", Type: []fhirElementType{{Code: "Reference", TargetProfile: []string{fhirDefinitionBase + "/Group"}}}},
		{Path: "Observation.component", Max: "*", Type: []fhirElementType{{Code: "BackboneElement"}}},
		{Path: "Observation.component.reference", Max: "1", Type: []fhirElementType{{Code: "Reference", TargetProfile: []string{fhirDefinitionBase + "/Group"}}}},
		{Path: "Observation.medication", Max: "1", Type: []fhirElementType{{Code: "CodeableReference", TargetProfile: []string{fhirDefinitionBase + "/Medication"}}}},
		{Path: "Observation.generic", Max: "1", Type: []fhirElementType{{Code: "CodeableReference"}}},
		{Path: "Observation.identifier", Max: "*", Type: []fhirElementType{{Code: "Identifier"}}},
		{Path: "Observation.supportingInformation", Max: "1", Type: []fhirElementType{{Code: "Reference", TargetProfile: []string{fhirDefinitionBase + "/Resource"}}}},
		{Path: "Observation.unsupported", Max: "1", Type: []fhirElementType{{Code: "Reference", TargetProfile: []string{fhirDefinitionBase + "/Procedure"}}}},
		{Path: "Observation.extension", Max: "*", Type: []fhirElementType{{Code: "Extension"}}},
		{Path: "Observation.recursive", Max: "1", Type: []fhirElementType{{Code: "Recursive"}}},
	}))
	write("StructureDefinition-Identifier.json", officialEdgeStructure("Identifier", "complex-type", []fhirElementDefinition{
		{Path: "Identifier"},
		{Path: "Identifier.assigner", Max: "1", Type: []fhirElementType{{Code: "Reference", TargetProfile: []string{fhirDefinitionBase + "/Organization"}}}},
	}))
	write("StructureDefinition-CodeableReference.json", officialEdgeStructure("CodeableReference", "complex-type", []fhirElementDefinition{
		{Path: "CodeableReference"},
		{Path: "CodeableReference.reference", Max: "1", Type: []fhirElementType{{Code: "Reference"}}},
	}))
	write("StructureDefinition-Extension.json", officialEdgeStructure("Extension", "complex-type", []fhirElementDefinition{
		{Path: "Extension"},
		{Path: "Extension.value[x]", Max: "1", Type: []fhirElementType{{Code: "Reference"}}},
	}))
	write("StructureDefinition-Recursive.json", officialEdgeStructure("Recursive", "complex-type", []fhirElementDefinition{
		{Path: "Recursive"},
		{Path: "Recursive.reference", Max: "1", Type: []fhirElementType{{Code: "Reference", TargetProfile: []string{fhirDefinitionBase + "/Patient"}}}},
		{Path: "Recursive.next", Max: "1", Type: []fhirElementType{{Code: "Recursive"}}},
	}))
	return directory
}

func officialEdgeStructure(name, kind string, elements []fhirElementDefinition) fhirStructureDefinition {
	definition := fhirStructureDefinition{
		ResourceType: "StructureDefinition",
		URL:          fhirDefinitionBase + "/" + name,
		Version:      "5.0.0",
		Name:         name,
		Type:         name,
		Kind:         kind,
		Derivation:   "specialization",
	}
	definition.Snapshot.Elements = elements
	return definition
}
