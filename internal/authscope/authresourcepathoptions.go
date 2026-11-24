package authscope

type AuthResourcePathOptions struct {
	Project           string
	DatasetGeneration string
	CursorBatch       int

	AllGenerations bool
}
