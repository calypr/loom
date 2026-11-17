package catalog

type ProfileLimits struct {
	MaxFields                  int
	MaxDistinctValuesPerField  int
	MaxDistinctValueBytes      int
	MaxPivotColumnsPerField    int
	MaxExtensionValuesPerField int
	MaxShapePlans              int
	MaxRetainedBytes           int
}

const (
	DefaultMaxProfileFields           = 4096
	DefaultMaxDistinctValuesPerField  = 4096
	DefaultMaxDistinctValueBytes      = 4096
	DefaultMaxPivotColumnsPerField    = 4096
	DefaultMaxExtensionValuesPerField = 4096
	DefaultMaxShapePlans              = 2048
	DefaultMaxRetainedBytes           = 64 << 20
)

func DefaultProfileLimits() ProfileLimits {
	return ProfileLimits{
		MaxFields:                  DefaultMaxProfileFields,
		MaxDistinctValuesPerField:  DefaultMaxDistinctValuesPerField,
		MaxDistinctValueBytes:      DefaultMaxDistinctValueBytes,
		MaxPivotColumnsPerField:    DefaultMaxPivotColumnsPerField,
		MaxExtensionValuesPerField: DefaultMaxExtensionValuesPerField,
		MaxShapePlans:              DefaultMaxShapePlans,
		MaxRetainedBytes:           DefaultMaxRetainedBytes,
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
	if l.MaxRetainedBytes <= 0 {
		l.MaxRetainedBytes = defaults.MaxRetainedBytes
	}
	return l
}

func NormalizeProfileLimits(l ProfileLimits) ProfileLimits {
	return l.normalized()
}
