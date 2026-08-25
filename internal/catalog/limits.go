package catalog

// ProfileLimits bounds the write-side memory retained while profiling one
// resource file. Catalog discovery is useful only while it remains a bounded
// summary; retaining every value from a high-cardinality field turns an ingest
// into an in-memory copy of the source dataset.
type ProfileLimits struct {
	MaxFields                  int
	MaxDistinctValuesPerField  int
	MaxDistinctValueBytes      int
	MaxPivotColumnsPerField    int
	MaxExtensionValuesPerField int
	MaxShapePlans              int
}

const (
	DefaultMaxProfileFields           = 4096
	DefaultMaxDistinctValuesPerField  = 4096
	DefaultMaxDistinctValueBytes      = 4096
	DefaultMaxPivotColumnsPerField    = 4096
	DefaultMaxExtensionValuesPerField = 4096
	DefaultMaxShapePlans              = 2048
)

// DefaultProfileLimits are deliberately finite. Callers that need a
// different operational envelope must make that choice explicitly.
func DefaultProfileLimits() ProfileLimits {
	return ProfileLimits{
		MaxFields:                  DefaultMaxProfileFields,
		MaxDistinctValuesPerField:  DefaultMaxDistinctValuesPerField,
		MaxDistinctValueBytes:      DefaultMaxDistinctValueBytes,
		MaxPivotColumnsPerField:    DefaultMaxPivotColumnsPerField,
		MaxExtensionValuesPerField: DefaultMaxExtensionValuesPerField,
		MaxShapePlans:              DefaultMaxShapePlans,
	}
}

func (l ProfileLimits) normalized() ProfileLimits {
	defaults := DefaultProfileLimits()
	if l.MaxFields <= 0 {
		l.MaxFields = defaults.MaxFields
	}
	if l.MaxDistinctValuesPerField <= 0 {
		l.MaxDistinctValuesPerField = defaults.MaxDistinctValuesPerField
	}
	if l.MaxDistinctValueBytes <= 0 {
		l.MaxDistinctValueBytes = defaults.MaxDistinctValueBytes
	}
	if l.MaxPivotColumnsPerField <= 0 {
		l.MaxPivotColumnsPerField = defaults.MaxPivotColumnsPerField
	}
	if l.MaxExtensionValuesPerField <= 0 {
		l.MaxExtensionValuesPerField = defaults.MaxExtensionValuesPerField
	}
	if l.MaxShapePlans <= 0 {
		l.MaxShapePlans = defaults.MaxShapePlans
	}
	return l
}

// NormalizeProfileLimits applies the finite defaults to an operational load
// configuration.
func NormalizeProfileLimits(l ProfileLimits) ProfileLimits {
	return l.normalized()
}
