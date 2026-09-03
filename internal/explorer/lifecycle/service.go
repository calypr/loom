package lifecycle

import (
	"fmt"
	"time"

	"github.com/calypr/loom/internal/explorer"
)

// Service is the Explorer application layer. It coordinates persistence and
// deployment adapters while leaving all wire representation to transports.
type Service struct {
	store  *explorer.Service
	config Config
	now    func() time.Time
}

func New(store *explorer.Service, config Config) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("Explorer lifecycle store is required")
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	config.Now = now
	return &Service{store: store, config: config, now: now}, nil
}
