package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/calypr/loom/internal/authscope"

	"github.com/gofiber/fiber/v3"
)

type HTTPConfig struct {
	Authenticator        authscope.Authenticator
	Authorizer           authscope.Authorizer
	Logger               *slog.Logger
	BodyLimit            int
	ReadBufferSize       int
	CoreReadyCheck       func(context.Context) error
	ClickHouseReadyCheck func(context.Context) error
	ClickHouseEnabled    bool
}

type HTTPServer struct {
	app                  *fiber.App
	authn                authscope.Authenticator
	authz                authscope.Authorizer
	logger               *slog.Logger
	coreReadyCheck       func(context.Context) error
	clickHouseReadyCheck func(context.Context) error
	clickHouseEnabled    bool
	healthMu             sync.Mutex
	lastHealth           time.Time
	lastHealthResult     healthResult
}

type healthResult struct {
	status, core, dataframe string
	httpStatus              int
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func NewHTTPServer(cfg HTTPConfig) (*HTTPServer, error) {
	if cfg.Authenticator == nil {
		cfg.Authenticator = authscope.BearerTokenAuthenticator{}
	}
	if cfg.Authorizer == nil {
		return nil, errors.New("http authorizer is required")
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
		authn:                cfg.Authenticator,
		authz:                cfg.Authorizer,
		logger:               cfg.Logger,
		coreReadyCheck:       cfg.CoreReadyCheck,
		clickHouseReadyCheck: cfg.ClickHouseReadyCheck,
		clickHouseEnabled:    cfg.ClickHouseEnabled,
	}
	app := fiber.New(fiber.Config{
		BodyLimit:         cfg.BodyLimit,
		ReadBufferSize:    cfg.ReadBufferSize,
		StreamRequestBody: true,
		ErrorHandler: func(c fiber.Ctx, err error) error {
			requestID := requestIDFromCtx(c)
			mapped := MapDataframeError(err, requestID)
			if mapped.Cause != nil {
				if mapped.Body.Error.Code == "INTERNAL_ERROR" || mapped.Body.Error.Code == "BACKEND_UNAVAILABLE" {
					server.logger.Error("request failed", "request_id", requestID, "path", c.Path(), "code", mapped.Body.Error.Code, "error", mapped.Cause)
				}
			}
			return c.Status(mapped.Status).JSON(mapped.Body)
		},
	})
	server.app = app
	server.register()
	return server, nil
}

func (s *HTTPServer) App() *fiber.App {
	return s.app
}

func (s *HTTPServer) Logger() *slog.Logger {
	return s.logger
}
