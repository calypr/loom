package main

import _ "embed"

//go:embed fhir_helpers.go.tmpl
var fhirHelpersSource string

func generateFHIRHelpers(path string) error {
	return writeGeneratedGo(path, fhirHelpersSource)
}
