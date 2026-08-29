package server

import (
	"net/url"
	"strings"

	"github.com/calypr/loom/internal/projectid"
	"github.com/gofiber/fiber/v3"
)

// explorerProjectParam decodes the percent-encoded slash used inside the
// existing /projects/:project route and exposes the canonical public ID to
// the rest of the Explorer HTTP layer.
func explorerProjectParam(c fiber.Ctx) string {
	raw := strings.TrimSpace(c.Params("project"))
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	return projectid.Canonical(raw)
}
