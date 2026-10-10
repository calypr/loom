package published

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/google/uuid"
)

// WithExecutionReadPins holds renewable retention pins for every exact
// published execution while visit resolves its outputs and streams rows.
// Callers must resolve the supplied revisions inside visit so cleanup cannot
// remove a table between metadata lookup and pin acquisition.
func (r *Reader) WithExecutionReadPins(ctx context.Context, executionIDs []string, visit func(context.Context) error) error {
	return r.withExecutionReadPins(ctx, executionIDs, defaultReadPinTTL, defaultReadPinInterval, visit)
}

func (r *Reader) withExecutionReadPins(ctx context.Context, executionIDs []string, ttl, interval time.Duration, visit func(context.Context) error) error {
	if r == nil || r.Catalog == nil {
		return fmt.Errorf("bundle catalog dependency is required")
	}
	if ctx == nil || visit == nil {
		return fmt.Errorf("pin context and visitor are required")
	}
	if ttl <= 0 || interval <= 0 || interval >= ttl {
		return fmt.Errorf("read pin renewal interval must be positive and shorter than its TTL")
	}
	unique := make(map[string]bool, len(executionIDs))
	ids := make([]string, 0, len(executionIDs))
	for _, id := range executionIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("execution read pin requires an exact execution ID")
		}
		if !unique[id] {
			unique[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("at least one exact execution read pin is required")
	}
	sort.Strings(ids)
	pins, ok := r.Catalog.(publication.ExecutionReadPinCatalog)
	if !ok {
		return fmt.Errorf("bundle catalog does not support execution read pins")
	}
	owner := "reader-" + uuid.NewString()
	acquired := make([]string, 0, len(ids))
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer releaseCancel()
		for _, id := range acquired {
			_ = pins.ReleaseExecutionReadPin(releaseCtx, id, owner)
		}
	}()
	for _, id := range ids {
		owned, err := pins.AcquireExecutionReadPin(ctx, id, owner, time.Now().UTC().Add(ttl))
		if err != nil {
			return err
		}
		if !owned {
			return publication.ErrExecutionReadPinLost
		}
		acquired = append(acquired, id)
	}

	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var lost atomic.Bool
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-scanCtx.Done():
				return
			case <-ticker.C:
				for _, id := range ids {
					renewCtx, renewCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					owned, err := pins.RenewExecutionReadPin(renewCtx, id, owner, time.Now().UTC().Add(ttl))
					renewCancel()
					if err != nil || !owned {
						lost.Store(true)
						cancel()
						return
					}
				}
			}
		}
	}()
	err := visit(scanCtx)
	close(stop)
	<-done
	if lost.Load() {
		return publication.ErrExecutionReadPinLost
	}
	return err
}
