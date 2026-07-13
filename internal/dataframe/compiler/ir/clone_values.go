package ir

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func clonePhysicalBindValue(value any) any {
	switch value := value.(type) {
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = clonePhysicalBindValue(item)
		}
		return out
	case []string:
		return append([]string(nil), value...)
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = clonePhysicalBindValue(item)
		}
		return out
	default:
		return value
	}
}
