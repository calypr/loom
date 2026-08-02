package published

func findColumn(columns []Column, name string) (Column, bool) {
	for _, column := range columns {
		if column.Name == name {
			return column, true
		}
	}
	return Column{}, false
}
