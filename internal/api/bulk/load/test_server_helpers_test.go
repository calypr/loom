package load

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/api/bulk/dump"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
)

type HTTPConfig struct {
	Service       *Service
	RawExporter   dump.RawExporter
	Authenticator authscope.Authenticator
	Authorizer    authscope.Authorizer
}

type RawDumpRequest = dump.RawDumpRequest

func NewHTTPServer(cfg HTTPConfig) (*httpapi.HTTPServer, error) {
	s, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: cfg.Authenticator, Authorizer: cfg.Authorizer})
	if err != nil {
		return nil, err
	}
	h, err := NewHandler(Config{Service: cfg.Service, Authorizer: cfg.Authorizer})
	if err != nil {
		return nil, err
	}
	h.RegisterRoutes(s.App())
	if cfg.RawExporter != nil {
		dump.NewHandler(dump.Config{RawExporter: cfg.RawExporter}).RegisterRoutes(s.App())
	}
	return s, nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) AuthorizeWrite(context.Context, *authscope.Principal, string, string) error {
	return errors.New("nope")
}
