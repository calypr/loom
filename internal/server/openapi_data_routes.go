package server

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
