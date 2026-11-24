package row

import (
	"regexp"
	"strings"
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

func SanitizeKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	return validKeyPart.ReplaceAllString(value, "_")
}
