package httpapi

import (
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
)

func (s *HTTPServer) register() {
	s.app.Use(s.requestIDMiddleware, s.recoveryMiddleware, s.loggingMiddleware, s.authenticationMiddleware)
	s.registerHealthRoutes()
	s.registerGraphQLRoutes()
	s.registerBulkResourceRoutes()
	s.registerImportRoutes()
	s.registerGenerationRoutes()
	s.registerRawRoutes()
	if s.dataframeExporter != nil {
		s.app.Post("/loom/api/v1/dataframe/export", s.exportDataframe)
	}
}

func (s *HTTPServer) registerRawRoutes() {
	if s.rawExporter != nil {
		s.app.Get("/api/v1/raw", s.dumpRaw)
	}
	s.app.Put("/api/v1/raw", s.loadRaw)
}

func (s *HTTPServer) registerBulkResourceRoutes() {
	s.app.Put("/api/v1/projects/:project/resources/:resourceType", s.bulkResource)
}

func (s *HTTPServer) registerGenerationRoutes() {
	s.app.Post("/api/v1/datasets/:project/generations/:generation", s.createGeneration)
	if s.rawExporter != nil {
		s.app.Get("/api/v1/datasets/:project/generations/:generation/export", s.exportGeneration)
	}
}

func (s *HTTPServer) registerHealthRoutes() {
	s.app.Get("/healthz", func(c fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "ok"})
	})
}

func (s *HTTPServer) registerGraphQLRoutes() {
	if s.cfgGraphQLPlaygroundHandler != nil {
		s.app.Get("/graphql/graph", adaptor.HTTPHandlerWithContext(s.cfgGraphQLPlaygroundHandler))
	}
	if s.cfgApolloSandboxHandler != nil {
		s.app.Get("/apollo", adaptor.HTTPHandlerWithContext(s.cfgApolloSandboxHandler))
	}
	if s.cfgGraphQLHandler != nil {
		s.app.Post("/graphql/graph", adaptor.HTTPHandlerWithContext(s.cfgGraphQLHandler))
		// FHIR dataframe compilation is an Arango-backed read, but it has its
		// own stable endpoint so clients do not accidentally send dataframe
		// documents to the graph or ClickHouse surfaces.
		s.app.Post("/graphql/dataframe", adaptor.HTTPHandlerWithContext(s.cfgGraphQLHandler))
	}
	if s.cfgClickHouseGraphQLHandler != nil {
		s.app.Post("/graphql/flat", adaptor.HTTPHandlerWithContext(s.cfgClickHouseGraphQLHandler))
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
