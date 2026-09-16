package published

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/google/uuid"
)

const (
	defaultReadPinTTL      = 2 * time.Minute
	defaultReadPinInterval = 30 * time.Second
)

// SelectionSourceAdapter adapts the exact published reader to Explorer
// lifecycle source resolution without exposing current-pointer lookup.
type SelectionSourceAdapter struct {
	Reader *Reader
}

func (a SelectionSourceAdapter) ResolveSelectionSource(ctx context.Context, project, explorerID, executionID, outputID string) (Materialization, error) {
	if a.Reader == nil {
		return Materialization{}, fmt.Errorf("published reader is required")
	}
	materialization, err := a.Reader.ExactExecutionMaterialization(ctx, executionID, outputID)
	if err != nil {
		return Materialization{}, err
	}
	if materialization.Project != strings.TrimSpace(project) {
		return Materialization{}, fmt.Errorf("%w: published source project changed", publication.ErrSelectionSourceIdentityChanged)
	}
	_ = explorerID
	return materialization, nil
}

func (a SelectionSourceAdapter) StreamSelectionSource(ctx context.Context, materialization Materialization, req StreamRequest, visit func(map[string]any) error) error {
	if a.Reader == nil {
		return fmt.Errorf("published reader is required")
	}
	return a.Reader.StreamExactExecution(ctx, materialization.Revision, materialization.Selector.Output, req, visit)
}

// ExactExecutionMaterialization resolves one stored execution/output without
// consulting a moving project-current pointer. It is the only resolver a
// selection or export may use after persisting source identity.
func (r *Reader) ExactExecutionMaterialization(ctx context.Context, executionID, outputID string) (Materialization, error) {
	if r == nil || r.Catalog == nil {
		return Materialization{}, fmt.Errorf("bundle catalog dependency is required")
	}
	executionID = strings.TrimSpace(executionID)
	outputID = strings.TrimSpace(outputID)
	if executionID == "" || outputID == "" {
		return Materialization{}, fmt.Errorf("execution and output are required")
	}
	execution, err := r.Catalog.GetExecution(ctx, executionID)
	if err != nil {
		return Materialization{}, err
	}
	if !execution.State.Successful() {
		return Materialization{}, fmt.Errorf("execution %q is not published", executionID)
	}
	for _, output := range execution.Outputs {
		if output.Name != outputID {
			continue
		}
		if !output.Queryable() {
			return Materialization{}, fmt.Errorf("published output %q is not queryable", outputID)
		}
		if !output.SourceRow.Valid() || strings.TrimSpace(execution.ReceiptID) == "" || strings.TrimSpace(execution.SchemaDigest) == "" {
			return Materialization{}, publication.ErrSelectionSourceNotAddressable
		}
		selector := output.Selector
		if !selector.Valid() {
			selector = execution.Selector(output.Name)
		}
		materialization := publishedMaterialization(execution, output, output.SourceRow.ResourceType)
		materialization.Selector = selector
		return materialization, nil
	}
	return Materialization{}, publication.ErrBundleNotFound
}

// SourceResourceRef extracts a typed source identity from one output row. It
// deliberately refuses labels, synthetic row numbers, and missing identity
// metadata.
func SourceResourceRef(materialization Materialization, row map[string]any) (string, string, error) {
	if materialization.SourceRow == nil || !materialization.SourceRow.Valid() {
		return "", "", publication.ErrSelectionSourceNotAddressable
	}
	value, ok := row[materialization.SourceRow.IDColumn]
	if !ok || value == nil {
		return "", "", publication.ErrSelectionSourceNotAddressable
	}
	var id string
	switch typed := value.(type) {
	case string:
		id = strings.TrimSpace(typed)
	case []byte:
		id = strings.TrimSpace(string(typed))
	default:
		return "", "", publication.ErrSelectionSourceNotAddressable
	}
	if id == "" {
		return "", "", publication.ErrSelectionSourceNotAddressable
	}
	return materialization.SourceRow.ResourceType, id, nil
}

// StreamExactExecution acquires a renewable reader pin before scanning an
// exact output. Pin loss cancels the ClickHouse query and is surfaced as a
// failure; callers must never return a partial membership as complete.
func (r *Reader) StreamExactExecution(ctx context.Context, executionID, outputID string, req StreamRequest, visit func(map[string]any) error) error {
	materialization, err := r.ExactExecutionMaterialization(ctx, executionID, outputID)
	if err != nil {
		return err
	}
	pins, ok := r.Catalog.(publication.ExecutionReadPinCatalog)
	if !ok {
		return fmt.Errorf("bundle catalog does not support execution read pins")
	}
	owner := "reader-" + uuid.NewString()
	expires := time.Now().UTC().Add(defaultReadPinTTL)
	acquired, err := pins.AcquireExecutionReadPin(ctx, executionID, owner, expires)
	if err != nil {
		return err
	}
	if !acquired {
		return publication.ErrExecutionReadPinLost
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer releaseCancel()
		_ = pins.ReleaseExecutionReadPin(releaseCtx, executionID, owner)
	}()

	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var lost atomic.Bool
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(defaultReadPinInterval)
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-scanCtx.Done():
				return
			case <-ticker.C:
				next := time.Now().UTC().Add(defaultReadPinTTL)
				renewCtx, renewCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				owned, renewErr := pins.RenewExecutionReadPin(renewCtx, executionID, owner, next)
				renewCancel()
				if renewErr != nil || !owned {
					lost.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	err = r.Stream(scanCtx, materialization, req, visit)
	close(stop)
	<-done
	if lost.Load() {
		return publication.ErrExecutionReadPinLost
	}
	return err
}

// ValidateTypedFilters is the boundary check used by selection creation. It
// accepts the existing data-reader filter representation but requires a
// bounded, supported operator/value shape before the reader builds SQL.
func ValidateTypedFilters(filters []Filter) error {
	for _, filter := range filters {
		if strings.TrimSpace(filter.Column) == "" || strings.TrimSpace(filter.Op) == "" {
			return fmt.Errorf("typed filter column and operator are required")
		}
		switch strings.ToUpper(strings.TrimSpace(filter.Op)) {
		case "EXISTS", "IS_NULL":
			if filter.Value != nil {
				return fmt.Errorf("filter %s must not carry a value", filter.Op)
			}
		case "EQ", "NEQ", "IN", "NOT_IN", "LT", "LTE", "GT", "GTE", "CONTAINS", "STARTS_WITH", "ARRAY_CONTAINS", "ARRAY_OVERLAPS":
			if filter.Value == nil {
				return fmt.Errorf("filter %s requires a typed value", filter.Op)
			}
		default:
			return fmt.Errorf("unsupported typed filter operator %q", filter.Op)
		}
	}
	return nil
}
