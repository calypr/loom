// Package graphstore contains Loom's small Arango document adapter.
//
// The upstream jsonschemagraph module exposes these document contracts only
// from an unpublished package path. Keeping the adapter here makes the Loom
// server build reproducibly from the public module graph while preserving the
// existing vertex/edge wire shape.
package graphstore

import (
	"fmt"
	"maps"
	"regexp"
	"strings"

	"github.com/bmeg/jsonschemagraph/model"
)

var validKeyPart = regexp.MustCompile(`[^A-Za-z0-9_\-:.@()+,=;$!*'%]`)

type VertexDocument struct {
	Key              string `json:"_key"`
	ID               string `json:"id"`
	Project          string `json:"project"`
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
		ResourceType:     resourceType,
		AuthResourcePath: authResourcePath,
		Payload:          payloadCopy,
	}, nil
}

func EdgeFromGrip(project, sourceType string, edge *model.Edge) (EdgeDocument, error) {
	if edge == nil {
		return EdgeDocument{}, fmt.Errorf("nil edge")
	}
	if strings.TrimSpace(edge.From) == "" {
		return EdgeDocument{}, fmt.Errorf("edge %q missing source id", edge.Id)
	}
	targetType, targetID := "", edge.To
	if strings.Contains(edge.To, "/") {
		var err error
		targetType, targetID, err = splitFHIRReference(edge.To)
		if err != nil {
			return EdgeDocument{}, fmt.Errorf("edge %q target: %w", edge.Id, err)
		}
	} else {
		var err error
		targetType, err = targetTypeFromLabel(edge.Label)
		if err != nil {
			return EdgeDocument{}, fmt.Errorf("edge %q target type: %w", edge.Id, err)
		}
	}
	return EdgeDocument{
		Key:      SanitizeKey(edge.Id),
		From:     collectionID(sourceType, edge.From),
		To:       collectionID(targetType, targetID),
		Label:    edge.Label,
		Project:  project,
		FromType: sourceType,
		ToType:   targetType,
	}, nil
}

func SanitizeKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	return validKeyPart.ReplaceAllString(value, "_")
}

func splitFHIRReference(ref string) (string, string, error) {
	parts := strings.Split(ref, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("malformed FHIR reference %q", ref)
	}
	return parts[0], parts[1], nil
}

func targetTypeFromLabel(label string) (string, error) {
	parts := strings.Split(label, "_")
	if len(parts) == 0 {
		return "", fmt.Errorf("empty edge label")
	}
	targetType := parts[len(parts)-1]
	if strings.TrimSpace(targetType) == "" {
		return "", fmt.Errorf("malformed edge label %q", label)
	}
	return targetType, nil
}

func collectionID(resourceType, id string) string {
	return SanitizeKey(resourceType) + "/" + SanitizeKey(id)
}
