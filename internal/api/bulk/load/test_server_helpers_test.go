package load

import (
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
)

type HTTPConfig struct {
	Service       *Service
	Authenticator authscope.Authenticator
	Authorizer    authscope.Authorizer
}

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
	return s, nil
}
