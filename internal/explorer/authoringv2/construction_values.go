package authoringv2

import (
	"crypto/sha256"
	"encoding/hex"
)

func stableConstructionID(prefix string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write([]byte(part))
		hash.Write([]byte{0})
	}
	return prefix + "_" + hex.EncodeToString(hash.Sum(nil)[:16])
}

func cloneTableScalar(scalar TableScalar) TableScalar {
	return TableScalar{
		Kind:    scalar.Kind,
		String:  cloneString(scalar.String),
		Integer: cloneInt64(scalar.Integer),
		Decimal: cloneFloat64(scalar.Decimal),
		Boolean: cloneBool(scalar.Boolean),
	}
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
