package load

import (
	"fmt"

	"github.com/calypr/loom/internal/authscope"
)

type Handler struct {
	service *Service
	authz   authscope.Authorizer
}

type Config struct {
	Service    *Service
	Authorizer authscope.Authorizer
}

func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Service == nil {
		return nil, fmt.Errorf("load service is required")
	}
	if cfg.Authorizer == nil {
		return nil, fmt.Errorf("load authorizer is required")
	}
	return &Handler{service: cfg.Service, authz: cfg.Authorizer}, nil
}
