package semanticfixtures

func InventoryComponent(code, version, display string) map[string]any {
	return map[string]any{
		"code": map[string]any{"coding": []any{map[string]any{
			"system": "urn:inventory", "version": version, "code": code, "display": display,
		}}},
		"valueQuantity": map[string]any{"value": 1.0, "unit": "mg"},
	}
}
