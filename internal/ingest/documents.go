package ingest

import (
	"fmt"
	"maps"
	"regexp"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

var validKeyPart = regexp.MustCompile(`[^A-Za-z0-9_\-:.@()+,=;$!*'%]`)

type VertexDocument struct {
	Key              string `json:"_key"`
	ID               string `json:"id"`
	Project          string `json:"project"`
	ProjectID        string `json:"project_id"`
	ResourceType     string `json:"resourceType"`
	AuthResourcePath string `json:"auth_resource_path,omitempty"`
	Payload          any    `json:"payload"`
}

type EdgeDocument struct {
	Key      string `json:"_key"`
	From     string `json:"_from"`
	To       string `json:"_to"`
	Label    string `json:"label"`
	Project  string `json:"project"`
	FromType string `json:"from_type"`
	ToType   string `json:"to_type"`
}

func VertexFromFHIRWithExtra(project, resourceType string, payload, extraArgs map[string]any) (VertexDocument, error) {
	canonicalType, ok := fhirschema.ConcreteResourceType(resourceType)
	if !ok {
		return VertexDocument{}, fmt.Errorf("unsupported FHIR graph resource type %q", resourceType)
	}
	id, ok := payload["id"].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return VertexDocument{}, fmt.Errorf("%s payload missing string id", resourceType)
	}
	payloadCopy := maps.Clone(payload)
	authResourcePath := ""
	if extraArgs != nil {
		maps.Copy(payloadCopy, extraArgs)
		if auth, ok := extraArgs["auth_resource_path"].(string); ok {
			authResourcePath = auth
		}
	}
	return VertexDocument{
		Key:              SanitizeKey(id),
		ID:               id,
		Project:          project,
		ProjectID:        project,
		ResourceType:     canonicalType,
		AuthResourcePath: authResourcePath,
		Payload:          payloadCopy,
	}, nil
}

func SanitizeKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	return validKeyPart.ReplaceAllString(value, "_")
}
