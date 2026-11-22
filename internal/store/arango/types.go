package arango

type RowVisitor func(map[string]any) error

type PersistentIndexSpec struct {
	Fields       []string
	StoredValues []string
	Sparse       bool
}

type CollectionSpec struct {
	Name              string
	Edge              bool
	Truncate          bool
	Indexes           [][]string
	PersistentIndexes []PersistentIndexSpec
}

type BootstrapSpec struct {
	Collections []CollectionSpec
	Reporter    func(event string, fields map[string]any)
}
