package main

import (
	"testing"
)

func TestFHIRRootResourceDefinitionRequiresConcreteResourceShape(t *testing.T) {
	stringType := any("string")
	root := &Definition{Properties: map[string]*Property{
		"resourceType": {Const: "Task"},
		"id":           {Type: stringType},
		"meta":         {Ref: "http://graph-fhir.io/schema/0.0.2/Meta"},
	}}
	if !isFHIRRootResourceDefinition("Task", root) {
		t.Fatal("concrete resource root shape was rejected")
	}

	for testName, testCase := range map[string]struct {
		definitionName string
		definition     *Definition
	}{
		"mismatched resource type": {definitionName: "Task", definition: &Definition{Properties: map[string]*Property{
			"resourceType": {Const: "Patient"},
			"id":           {Type: stringType},
			"meta":         {Ref: "http://graph-fhir.io/schema/0.0.2/Meta"},
		}}},
		"missing metadata root field": {definitionName: "Task", definition: &Definition{Properties: map[string]*Property{
			"resourceType": {Const: "Task"},
			"id":           {Type: stringType},
		}}},
		"non-string id": {definitionName: "Task", definition: &Definition{Properties: map[string]*Property{
			"resourceType": {Const: "Task"},
			"id":           {Type: any("integer")},
			"meta":         {Ref: "http://graph-fhir.io/schema/0.0.2/Meta"},
		}}},
		"abstract Resource placeholder": {definitionName: "Resource", definition: &Definition{Properties: map[string]*Property{
			"resourceType": {Const: "Resource"},
			"id":           {Type: stringType},
			"meta":         {Ref: "http://graph-fhir.io/schema/0.0.2/Meta"},
		}}},
	} {
		t.Run(testName, func(t *testing.T) {
			if isFHIRRootResourceDefinition(testCase.definitionName, testCase.definition) {
				t.Fatal("non-root resource shape was accepted")
			}
		})
	}
}
