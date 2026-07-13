package compiler

import (
	"fmt"
	"strings"
)

func physicalSelectorIdentity(selector Selector) string {
	var b strings.Builder
	for _, step := range selector.Steps {
		b.WriteString(step.Field)
		if step.Iterate {
			b.WriteString("[]")
		}
		if step.Index != nil {
			fmt.Fprintf(&b, "[%d]", *step.Index)
		}
		b.WriteByte('.')
	}
	if selector.Filter != nil {
		b.WriteString("?" + selector.Filter.Field + "=" + selector.Filter.Needle)
	}
	return b.String()
}
