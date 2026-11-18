package authscope

import (
	"context"
)

func SubjectFromContext(ctx context.Context) string {
	principal, _ := PrincipalFromContext(ctx)
	if principal == nil {
		return ""
	}
	return principal.Subject
}
