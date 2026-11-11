package arango

type RowVisitor func(map[string]any) error

type CollectionSpec struct {
	Name     string
	Edge     bool
	Truncate bool
	Indexes  [][]string
}

type BootstrapSpec struct {
	Collections []CollectionSpec
	Reporter    func(event string, fields map[string]any)
}
