package ir

func StreamingPivotInputProjectsSourceRows(pivot PhysicalGroupedPivot, inputRowVariable string, sourceNames map[string]struct{}) bool {
	if inputRowVariable == "" || len(pivot.InputProjections) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(pivot.InputProjections))
	for _, projection := range pivot.InputProjections {
		if projection.Name == "" || projection.Expression != nil || projection.Presence != nil || projection.PresenceOutput ||
			projection.Value.Variable != inputRowVariable || projection.Value.BindKey != "" ||
			len(projection.Value.Path) != 1 || projection.Value.Path[0] != projection.Name {
			return false
		}
		if _, ok := sourceNames[projection.Name]; !ok {
			return false
		}
		if _, duplicate := seen[projection.Name]; duplicate {
			return false
		}
		seen[projection.Name] = struct{}{}
	}
	required := []string{pivot.CategoryColumn, pivot.ValueColumn, pivot.CategoryPresenceColumn}
	for _, key := range pivot.GroupKeys {
		required = append(required, key.Column)
	}
	for _, name := range required {
		if name == "" {
			return false
		}
		if _, ok := seen[name]; !ok {
			return false
		}
	}
	return true
}
