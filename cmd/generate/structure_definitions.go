package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type fhirStructureDefinition struct {
	ResourceType string `json:"resourceType"`
	URL          string `json:"url"`
	Version      string `json:"version"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Type         string `json:"type"`
	Kind         string `json:"kind"`
	Derivation   string `json:"derivation"`
	Abstract     bool   `json:"abstract"`
	Snapshot     struct {
		Elements []fhirElementDefinition `json:"element"`
	} `json:"snapshot"`
}

type fhirElementDefinition struct {
	ID               string                     `json:"id"`
	Path             string                     `json:"path"`
	Short            string                     `json:"short"`
	Definition       string                     `json:"definition"`
	ContentReference string                     `json:"contentReference"`
	Min              int                        `json:"min"`
	Max              string                     `json:"max"`
	Type             []fhirElementType          `json:"type"`
	Binding          fhirElementBinding         `json:"binding"`
	Fixed            map[string]json.RawMessage `json:"fixed"`
}

type fhirElementType struct {
	Code          string   `json:"code"`
	Profile       []string `json:"profile"`
	TargetProfile []string `json:"targetProfile"`
	Extension     []struct {
		URL      string `json:"url"`
		ValueURL string `json:"valueUrl"`
	} `json:"extension"`
}

type fhirElementBinding struct {
	Strength    string `json:"strength"`
	ValueSet    string `json:"valueSet"`
	Description string `json:"description"`
}

const fhirDefinitionBase = "http://hl7.org/fhir/StructureDefinition"

func readStructureDefinitions(directory string) ([]fhirStructureDefinition, string, error) {
	rawManifest, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return nil, "", fmt.Errorf("read FHIR package manifest: %w", err)
	}
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return nil, "", fmt.Errorf("decode FHIR package manifest: %w", err)
	}
	if manifest.Name != "hl7.fhir.r5.core" || manifest.Version != "5.0.0" {
		return nil, "", fmt.Errorf("FHIR package is %s@%s, want hl7.fhir.r5.core@5.0.0", manifest.Name, manifest.Version)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, "", err
	}
	var definitions []fhirStructureDefinition
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "StructureDefinition-") || filepath.Ext(name) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return nil, "", err
		}
		var definition fhirStructureDefinition
		if err := json.Unmarshal(raw, &definition); err != nil {
			return nil, "", fmt.Errorf("decode %s: %w", name, err)
		}
		if definition.ResourceType != "StructureDefinition" || definition.Version != manifest.Version ||
			definition.Derivation != "specialization" || definition.URL != fhirDefinitionBase+"/"+definition.Type {
			return nil, "", fmt.Errorf("unexpected core StructureDefinition metadata in %s", name)
		}
		definitions = append(definitions, definition)
	}
	if len(definitions) == 0 {
		return nil, "", fmt.Errorf("FHIR package %q contains no StructureDefinitions", directory)
	}
	return definitions, manifest.Version, nil
}

func hasDescendantElement(elements []fhirElementDefinition, elementPath string) bool {
	prefix := elementPath + "."
	for _, element := range elements {
		if strings.HasPrefix(element.Path, prefix) {
			return true
		}
	}
	return false
}

func choiceSuffix(elementType fhirElementType) string {
	name := elementType.Code
	for _, extension := range elementType.Extension {
		if extension.URL == "http://hl7.org/fhir/StructureDefinition/structuredefinition-fhir-type" && extension.ValueURL != "" {
			name = extension.ValueURL
		}
	}
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		name = name[slash+1:]
	}
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
