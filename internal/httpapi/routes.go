package httpapi

import (
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
)

func (s *HTTPServer) register() {
	s.app.Use(s.requestIDMiddleware, s.recoveryMiddleware, s.loggingMiddleware, s.authenticationMiddleware)
	s.registerHealthRoutes()
	s.registerGraphQLRoutes()
	s.registerImportRoutes()
}

func (s *HTTPServer) registerHealthRoutes() {
	s.app.Get("/healthz", func(c fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "ok"})
	})
}

func (s *HTTPServer) registerGraphQLRoutes() {
	if s.cfgGraphQLPlaygroundHandler != nil {
		s.app.Get("/graphql", adaptor.HTTPHandlerWithContext(s.cfgGraphQLPlaygroundHandler))
	}
	if s.cfgApolloSandboxHandler != nil {
		s.app.Get("/apollo", adaptor.HTTPHandlerWithContext(s.cfgApolloSandboxHandler))
	}
	if s.cfgGraphQLHandler != nil {
		s.app.Post("/graphql", adaptor.HTTPHandlerWithContext(s.cfgGraphQLHandler))
	}
}

func (s *HTTPServer) registerImportRoutes() {
	api := s.app.Group("/api/v1")
	if s.disableSingleResourceImports {
		api.Post("/imports", func(c fiber.Ctx) error {
			return &apiError{
				Status:  fiber.StatusConflict,
				Code:    "legacy_import_disabled",
				Message: "single-resource imports are disabled while dataset-generation mode is enabled; load a complete dataset generation instead",
			}
		})
		return
	}
	api.Post("/imports", s.createImport)
}
