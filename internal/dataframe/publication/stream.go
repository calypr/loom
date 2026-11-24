package publication

import "context"

type OutputStream struct {
	Name      string
	Columns   []LogicalColumn
	SourceRow *SourceRowMetadata
	Stream    func(context.Context, func(map[string]any) error) error
}

type Limits struct {
	BatchRows  int
	BatchBytes int
	Quality    QualityPolicy
}

func (l Limits) normalized() Limits {
	if l.BatchRows <= 0 {
		l.BatchRows = 1000
	}
	if l.BatchBytes <= 0 {
		l.BatchBytes = 4 << 20
	}
	return l
}
