package published

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/projectid"
)

type ExportFormat string

const (
	ExportCSV   ExportFormat = "CSV"
	ExportTSV   ExportFormat = "TSV"
	ExportJSON  ExportFormat = "JSON"
	ExportJSONL ExportFormat = "JSONL"
)

type ExportRequest struct {
	Selector *DataframeSelector `json:"selector,omitempty"`
	Columns  []string
	Filters  []Filter
	Sort     *Sort
	Format   ExportFormat
	Filename string
}

type ExactExportRequest struct {
	ExecutionID        string
	OutputID           string
	Project            string
	DatasetGeneration  string
	ReceiptID          string
	SchemaDigest       string
	Columns            []string
	Filters            []Filter
	Sort               *Sort
	AuthResourcePaths  []string
	Unrestricted       bool
	IncludeRowIdentity bool

	ReaderID           string
	PinExpiresAt       time.Time
	PinRenewalInterval time.Duration
}

type ExactExportResult struct {
	ExecutionID     string
	OutputID        string
	ReaderID        string
	PinExpiresAt    time.Time
	Materialization Materialization
	Rows            int64
}

type ExactExportVisitor func(map[string]any) error

func (r *Reader) StreamExactExport(ctx context.Context, request ExactExportRequest, visit ExactExportVisitor) (ExactExportResult, error) {
	result := ExactExportResult{
		ExecutionID: strings.TrimSpace(request.ExecutionID),
		OutputID:    strings.TrimSpace(request.OutputID),
		ReaderID:    strings.TrimSpace(request.ReaderID),
	}
	if r == nil || r.Catalog == nil {
		return result, fmt.Errorf("bundle catalog dependency is required")
	}
	if visit == nil {
		return result, fmt.Errorf("exact export visitor is required")
	}
	if result.ExecutionID == "" || result.OutputID == "" {
		return result, fmt.Errorf("exact export execution and output are required")
	}
	if result.ReaderID == "" {
		return result, fmt.Errorf("exact export reader id is required")
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.DatasetGeneration) == "" || strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.SchemaDigest) == "" {
		return result, fmt.Errorf("exact export project, generation, receipt, and schema identity are required")
	}
	if err := ValidateTypedFilters(request.Filters); err != nil {
		return result, err
	}

	materialization, err := r.ExactExecutionMaterialization(ctx, result.ExecutionID, result.OutputID)
	if err != nil {
		return result, err
	}
	if err := validateExactExportIdentity(materialization, request); err != nil {
		return result, err
	}
	result.Materialization = materialization

	pins, ok := r.Catalog.(publication.ExecutionReadPinCatalog)
	if !ok {
		return result, fmt.Errorf("bundle catalog does not support execution read pins")
	}
	expires := request.PinExpiresAt
	if expires.IsZero() {
		expires = time.Now().UTC().Add(defaultReadPinTTL)
	}
	if !expires.After(time.Now().UTC()) {
		return result, fmt.Errorf("exact export read pin expiry must be in the future")
	}
	renewInterval := request.PinRenewalInterval
	if renewInterval == 0 {
		renewInterval = defaultReadPinInterval
	}
	if renewInterval < 0 {
		return result, fmt.Errorf("exact export read pin renewal interval must not be negative")
	}

	acquired, err := pins.AcquireExecutionReadPin(ctx, result.ExecutionID, result.ReaderID, expires)
	if err != nil {
		return result, err
	}
	if !acquired {
		return result, publication.ErrExecutionReadPinLost
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer releaseCancel()
		_ = pins.ReleaseExecutionReadPin(releaseCtx, result.ExecutionID, result.ReaderID)
	}()

	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var lost atomic.Bool
	var expiresNanos atomic.Int64
	expiresNanos.Store(expires.UnixNano())
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(renewInterval)
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
				owned, renewErr := pins.RenewExecutionReadPin(renewCtx, result.ExecutionID, result.ReaderID, next)
				renewCancel()
				if renewErr != nil || !owned {
					lost.Store(true)
					cancel()
					return
				}
				expiresNanos.Store(next.UnixNano())
			}
		}
	}()

	streamRequest := StreamRequest{
		Columns:            append([]string(nil), request.Columns...),
		Filters:            append([]Filter(nil), request.Filters...),
		Sort:               cloneSort(request.Sort),
		AuthResourcePaths:  append([]string(nil), request.AuthResourcePaths...),
		Unrestricted:       request.Unrestricted,
		IncludeRowIdentity: request.IncludeRowIdentity,
	}
	err = r.Stream(scanCtx, materialization, streamRequest, func(row map[string]any) error {
		if err := visit(row); err != nil {
			return err
		}
		result.Rows++
		return nil
	})
	close(stop)
	<-done
	result.PinExpiresAt = time.Unix(0, expiresNanos.Load()).UTC()
	if lost.Load() {
		return result, publication.ErrExecutionReadPinLost
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func validateExactExportIdentity(materialization Materialization, request ExactExportRequest) error {
	if projectid.Canonical(materialization.Project) != projectid.Canonical(request.Project) {
		return fmt.Errorf("%w: published export project changed", publication.ErrSelectionSourceIdentityChanged)
	}
	if materialization.DatasetGeneration != strings.TrimSpace(request.DatasetGeneration) {
		return fmt.Errorf("%w: published export generation changed", publication.ErrSelectionSourceIdentityChanged)
	}
	if materialization.ReceiptID != strings.TrimSpace(request.ReceiptID) {
		return fmt.Errorf("%w: published export receipt changed", publication.ErrSelectionSourceIdentityChanged)
	}
	if materialization.SchemaDigest != strings.TrimSpace(request.SchemaDigest) {
		return fmt.Errorf("%w: published export schema changed", publication.ErrSelectionSourceIdentityChanged)
	}
	return nil
}

func cloneSort(value *Sort) *Sort {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func (f ExportFormat) Normalize() ExportFormat {
	return ExportFormat(strings.ToUpper(strings.TrimSpace(string(f))))
}
