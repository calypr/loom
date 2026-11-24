package ir

func RootPageInsertionIndex(operations []PhysicalOperation) int {
	index := 5
	for index < len(operations) {
		operation := operations[index]
		if operation.Kind == PhysicalExpressionLetOp {

			if index+1 >= len(operations) || operations[index+1].Kind != PhysicalFilterOp {
				break
			}
		} else if operation.Kind != PhysicalFilterOp {
			break
		}
		index++
	}
	return index
}
