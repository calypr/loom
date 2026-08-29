package execution

import (
	"context"
)

type ServiceConfig struct {
	QueryRows func(context.Context, string, int, map[string]any, func(map[string]any) error) error
}

type Service struct {
	queryRows func(context.Context, string, int, map[string]any, func(map[string]any) error) error
}

func NewService(cfg ServiceConfig) *Service {
	return &Service{queryRows: cfg.QueryRows}
}
