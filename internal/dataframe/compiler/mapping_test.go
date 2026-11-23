package compiler

func containsBindValue(bindVars map[string]any, want string) bool {
	for _, value := range bindVars {
		if value == want {
			return true
		}
	}
	return false
}
