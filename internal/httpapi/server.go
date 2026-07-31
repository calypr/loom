package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/calypr/loom/internal/authscope"

	"github.com/gofiber/fiber/v3"
)

type HTTPConfig struct {
	Service                  *Service
	Authenticator            authscope.Authenticator
	Authorizer               authscope.Authorizer
	ScopeResolver            *authscope.ScopeResolver
	GraphQLHandler           http.Handler
	ClickHouseGraphQLHandler http.Handler
	GraphQLPlaygroundHandler http.Handler
	ApolloSandboxHandler     http.Handler
	Logger                   *slog.Logger
	BodyLimit                int
	ReadBufferSize           int
	// DisableSingleResourceImports prevents the legacy multipart endpoint from
	// mutating shared graph collections. Generation-aware deployments must use
	// a complete staged bundle loader instead: one uploaded resource file can
	// never safely become an immutable active dataset generation.
	DisableSingleResourceImports bool
	RawExporter                  RawExporter
	DataframeExporter            DataframeExporter
}

type HTTPServer struct {
	app                          *fiber.App
	service                      *Service
	authn                        authscope.Authenticator
	authz                        authscope.Authorizer
	scopeResolver                *authscope.ScopeResolver
	logger                       *slog.Logger
	cfgGraphQLHandler            http.Handler
	cfgClickHouseGraphQLHandler  http.Handler
	cfgGraphQLPlaygroundHandler  http.Handler
	cfgApolloSandboxHandler      http.Handler
	disableSingleResourceImports bool
	rawExporter                  RawExporter
	dataframeExporter            DataframeExporter
}

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string { return e.Message }

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func NewHTTPServer(cfg HTTPConfig) (*HTTPServer, error) {
	if cfg.Service == nil {
		return nil, errors.New("service is required")
	}
	if cfg.Authenticator == nil {
		cfg.Authenticator = authscope.BearerTokenAuthenticator{}
	}
	if cfg.Authorizer == nil {
		cfg.Authorizer = authscope.AllowAllAuthorizer{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.BodyLimit <= 0 {
		cfg.BodyLimit = 1024 * 1024 * 1024
	}
	if cfg.ReadBufferSize <= 0 {
		cfg.ReadBufferSize = 1024 * 1024
	}

	server := &HTTPServer{
		service:                      cfg.Service,
		authn:                        cfg.Authenticator,
		authz:                        cfg.Authorizer,
		scopeResolver:                cfg.ScopeResolver,
		logger:                       cfg.Logger,
		cfgGraphQLHandler:            cfg.GraphQLHandler,
		cfgClickHouseGraphQLHandler:  cfg.ClickHouseGraphQLHandler,
		cfgGraphQLPlaygroundHandler:  cfg.GraphQLPlaygroundHandler,
		cfgApolloSandboxHandler:      cfg.ApolloSandboxHandler,
		disableSingleResourceImports: cfg.DisableSingleResourceImports,
		rawExporter:                  cfg.RawExporter,
		dataframeExporter:            cfg.DataframeExporter,
	}
	app := fiber.New(fiber.Config{
		BodyLimit:         cfg.BodyLimit,
		ReadBufferSize:    cfg.ReadBufferSize,
		StreamRequestBody: true,
		ErrorHandler: func(c fiber.Ctx, err error) error {
			requestID := requestIDFromCtx(c)
			var apiErr *apiError
			if errors.As(err, &apiErr) {
				return c.Status(apiErr.Status).JSON(errorEnvelope{
					Error: errorBody{Code: apiErr.Code, Message: apiErr.Message, RequestID: requestID},
				})
			}
			server.logger.Error("unhandled request error", "request_id", requestID, "path", c.Path(), "error", err.Error())
			return c.Status(fiber.StatusInternalServerError).JSON(errorEnvelope{
				Error: errorBody{Code: "internal_error", Message: "internal server error", RequestID: requestID},
			})
		},
	})
	server.app = app
	server.register()
	return server, nil
}

func (s *HTTPServer) App() *fiber.App {
	return s.app
}
