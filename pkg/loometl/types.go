// Package loometl provides the client-side snapshot-to-release workflow used
// by external ETL jobs. It deliberately depends only on Loom's public HTTP and
// GraphQL contracts.
package loometl

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

type DataframeSelector struct {
	Recipe             string `json:"recipe"`
	TranslationVersion string `json:"translationVersion"`
	Output             string `json:"output"`
}

func (s DataframeSelector) Validate() error {
	for name, value := range map[string]string{"recipe": s.Recipe, "translationVersion": s.TranslationVersion, "output": s.Output} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("%s is required and must not have surrounding whitespace", name)
		}
	}
	return nil
}

func (s DataframeSelector) Key() string {
	return fmt.Sprintf("%d:%s%d:%s%d:%s", len(s.Recipe), s.Recipe, len(s.TranslationVersion), s.TranslationVersion, len(s.Output), s.Output)
}

type DatasetRef struct {
	Project    string `json:"project"`
	Generation string `json:"generation"`
}

type ResourceUpload struct {
	ResourceType string    `json:"resourceType"`
	SHA256       string    `json:"sha256"`
	Size         int64     `json:"size"`
	UploadedAt   time.Time `json:"uploadedAt"`
}

type SnapshotGeneration struct {
	Dataset               DatasetRef       `json:"dataset"`
	GitCommit             string           `json:"gitCommit"`
	State                 string           `json:"state"`
	ExpectedResourceTypes []string         `json:"expectedResourceTypes"`
	Uploads               []ResourceUpload `json:"uploads,omitempty"`
	AuthResourcePath      string           `json:"authResourcePath,omitempty"`
	CreatedAt             time.Time        `json:"createdAt"`
	UpdatedAt             time.Time        `json:"updatedAt"`
	AbortedAt             *time.Time       `json:"abortedAt,omitempty"`
	RequestID             string           `json:"-"`
}

type CreateGenerationRequest struct {
	GitCommit             string   `json:"gitCommit,omitempty"`
	ExpectedResourceTypes []string `json:"expectedResourceTypes"`
	AuthResourcePath      string   `json:"authResourcePath,omitempty"`
}

type FinalizeGenerationResult struct {
	Generation SnapshotGeneration `json:"generation"`
	Load       any                `json:"load,omitempty"`
	RequestID  string             `json:"-"`
}

// ResourceSource must return a fresh body for each attempt. Size and SHA256
// describe the exact bytes returned by Open.
type ResourceSource struct {
	ResourceType string
	SHA256       string
	Size         int64
	Open         func(context.Context) (io.ReadCloser, error)
}

type MaterializationRequest struct {
	Project    string
	Generation string
	Selector   DataframeSelector
}

type ExecutionOutput struct {
	Name           string             `json:"name"`
	Selector       *DataframeSelector `json:"selector,omitempty"`
	State          string             `json:"state"`
	RowCount       *int64             `json:"rowCount,omitempty"`
	Phase          string             `json:"phase,omitempty"`
	Error          string             `json:"error,omitempty"`
	ErrorCode      string             `json:"errorCode,omitempty"`
	ErrorRetryable *bool              `json:"errorRetryable,omitempty"`
}

type MaterializationExecution struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	TranslationVersion string            `json:"translationVersion,omitempty"`
	SourceGeneration   string            `json:"sourceGeneration"`
	State              string            `json:"state"`
	Phase              string            `json:"phase,omitempty"`
	Outputs            []ExecutionOutput `json:"outputs"`
	Error              string            `json:"error,omitempty"`
	ErrorCode          string            `json:"errorCode,omitempty"`
	ErrorRetryable     *bool             `json:"errorRetryable,omitempty"`
	LoomRequestID      string            `json:"requestId,omitempty"`
	TransportRequestID string            `json:"-"`
}

func (e MaterializationExecution) Successful() bool {
	return e.State == "PUBLISHED" || e.State == "READY"
}

func (e MaterializationExecution) Terminal() bool {
	return e.Successful() || e.State == "FAILED"
}

type ReleasePublication struct {
	Selector    DataframeSelector `json:"selector"`
	ExecutionID string            `json:"executionId"`
	Generation  string            `json:"generation"`
	Required    bool              `json:"required"`
	Stale       bool              `json:"stale"`
	VerifiedAt  time.Time         `json:"verifiedAt"`
}

type ContractVerification struct {
	Selector    DataframeSelector `json:"selector"`
	ExecutionID string            `json:"executionId,omitempty"`
	Generation  string            `json:"generation,omitempty"`
	State       string            `json:"state,omitempty"`
	Queryable   bool              `json:"queryable"`
	VerifiedAt  time.Time         `json:"verifiedAt,omitempty"`
	ErrorCode   string            `json:"errorCode,omitempty"`
}

type ProjectRelease struct {
	ID                    string                 `json:"id"`
	Project               string                 `json:"project"`
	GitCommit             string                 `json:"gitCommit"`
	Generation            string                 `json:"generation"`
	Publications          []ReleasePublication   `json:"publications"`
	RequiredVerifications []ContractVerification `json:"requiredVerifications"`
	CreatedAt             time.Time              `json:"createdAt"`
	RequestID             string                 `json:"-"`
}

type CreateReleaseRequest struct {
	Generation        string              `json:"generation"`
	GitCommit         string              `json:"gitCommit,omitempty"`
	OptionalSelectors []DataframeSelector `json:"optionalSelectors,omitempty"`
}

type ActivateReleaseRequest struct {
	ExpectedRevision int64 `json:"expectedRevision"`
}

type ActiveRelease struct {
	Release   ProjectRelease `json:"release"`
	Revision  int64          `json:"revision"`
	RequestID string         `json:"-"`
}

type LoomAPI interface {
	CreateOrResumeGeneration(context.Context, string, string, CreateGenerationRequest) (SnapshotGeneration, error)
	UploadResource(context.Context, string, string, ResourceSource) (SnapshotGeneration, error)
	FinalizeGeneration(context.Context, string, string) (FinalizeGenerationResult, error)
	GenerationStatus(context.Context, string, string) (SnapshotGeneration, error)
	AbortGeneration(context.Context, string, string) (SnapshotGeneration, error)
	StartMaterialization(context.Context, MaterializationRequest) (MaterializationExecution, error)
	MaterializationStatus(context.Context, string) (MaterializationExecution, error)
	CreateRelease(context.Context, string, CreateReleaseRequest) (ProjectRelease, error)
	ReleaseStatus(context.Context, string, string) (ProjectRelease, error)
	ActiveRelease(context.Context, string) (ActiveRelease, error)
	ActivateRelease(context.Context, string, string, ActivateReleaseRequest) (ActiveRelease, error)
}
