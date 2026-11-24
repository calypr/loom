package lower

import (
	"fmt"
)

type RelatedEligibilityAnchorError struct{ StepID string }

func (e *RelatedEligibilityAnchorError) Error() string {
	return fmt.Sprintf("related eligibility step %q has no compiler-proven exact resource identity", e.StepID)
}
