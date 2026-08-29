package server

import (
	"context"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
)

func requestIDFromContext(ctx context.Context) string { return httpapi.RequestIDFromContext(ctx) }

func subjectFromContext(ctx context.Context) string {
	principal, _ := authscope.PrincipalFromContext(ctx)
	if principal == nil {
		return ""
	}
	return principal.Subject
}
