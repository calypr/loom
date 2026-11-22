package row

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/calypr/loom/fhir/schema"
)

func normalizeFHIRInteger64(raw []byte, resourceType, project string) (json.RawMessage, map[string]any) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || decoder.InputOffset() < int64(len(bytes.TrimRight(raw, " \t\r\n"))) {
		return nil, nil
	}
	if !normalizeInteger64Properties(payload, schema.Definitions[resourceType].Properties) {
		return nil, nil
	}
	payload["project_id"] = project
	normalized, err := json.Marshal(payload)
	if err != nil {
		return nil, nil
	}
	return normalized, payload
}

func normalizeInteger64Properties(object map[string]any, properties []schema.Property) bool {
	changed := false
	for _, property := range properties {
		value, exists := object[property.Name]
		if !exists {
			continue
		}
		if items, ok := value.([]any); ok {
			for i, item := range items {
				if normalized, ok := normalizeInteger64Value(item, property.ItemInteger64, property.ItemRef, property.ItemProperties); ok {
					items[i] = normalized
					changed = true
				}
			}
			continue
		}
		if normalized, ok := normalizeInteger64Value(value, property.Integer64, property.Ref, property.Properties); ok {
			object[property.Name] = normalized
			changed = true
		}
	}
	return changed
}

func normalizeInteger64Value(value any, integer64 bool, ref string, properties []schema.Property) (any, bool) {
	if number, ok := value.(json.Number); ok && integer64 {
		if n, err := number.Int64(); err == nil {
			return strconv.FormatInt(n, 10), true
		}
	}
	object, ok := value.(map[string]any)
	if !ok {
		return value, false
	}
	if ref == "ResourceList" {
		ref, _ = object["resourceType"].(string)
	}
	if ref != "" {
		properties = schema.Definitions[ref].Properties
	}
	return value, normalizeInteger64Properties(object, properties)
}
