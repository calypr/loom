package loometl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxResponseBytes = 8 << 20

type RetryPolicy struct {
	MaxAttempts int
	Backoff     func(attempt int) time.Duration
	Sleep       func(context.Context, time.Duration) error
}

func (p RetryPolicy) normalized() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 3
	}
	if p.Backoff == nil {
		p.Backoff = func(attempt int) time.Duration { return time.Duration(attempt) * 250 * time.Millisecond }
	}
	if p.Sleep == nil {
		p.Sleep = func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	return p
}

type ClientConfig struct {
	BaseURL     string
	HTTPClient  *http.Client
	Headers     http.Header
	Retry       RetryPolicy
	GraphQLPath string
}

type Client struct {
	baseURL     *url.URL
	httpClient  *http.Client
	headers     http.Header
	retry       RetryPolicy
	graphqlPath string
}

func NewClient(cfg ClientConfig) (*Client, error) {
	base, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("valid Loom base URL is required")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	graphqlPath := cfg.GraphQLPath
	if graphqlPath == "" {
		graphqlPath = "/graphql/graph"
	}
	return &Client{baseURL: base, httpClient: client, headers: cfg.Headers.Clone(), retry: cfg.Retry.normalized(), graphqlPath: graphqlPath}, nil
}

func (c *Client) CreateOrResumeGeneration(ctx context.Context, project, gitCommit string, input CreateGenerationRequest) (SnapshotGeneration, error) {
	if input.GitCommit == "" {
		input.GitCommit = gitCommit
	}
	var result SnapshotGeneration
	requestID, err := c.doJSON(ctx, http.MethodPost, c.snapshotPath(project, gitCommit), input, &result)
	result.RequestID = requestID
	return result, err
}

func (c *Client) UploadResource(ctx context.Context, project, gitCommit string, source ResourceSource) (SnapshotGeneration, error) {
	requestID := uuid.NewString()
	factory := func() (*http.Request, error) {
		body, err := source.Open(ctx)
		if err != nil {
			return nil, fmt.Errorf("open %s upload: %w", source.ResourceType, err)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.endpoint(c.snapshotPath(project, gitCommit)+"/resources/"+url.PathEscape(source.ResourceType)), body)
		if err != nil {
			_ = body.Close()
			return nil, err
		}
		request.ContentLength = source.Size
		request.Header.Set("Content-Type", "application/fhir+ndjson")
		request.Header.Set("X-Content-SHA256", source.SHA256)
		c.decorate(request, requestID)
		return request, nil
	}
	var result SnapshotGeneration
	responseID, err := c.do(ctx, factory, &result)
	result.RequestID = responseID
	return result, err
}

func (c *Client) FinalizeGeneration(ctx context.Context, project, gitCommit string) (FinalizeGenerationResult, error) {
	var result FinalizeGenerationResult
	requestID, err := c.doJSON(ctx, http.MethodPost, c.snapshotPath(project, gitCommit)+"/finalize", nil, &result)
	result.RequestID = requestID
	result.Generation.RequestID = requestID
	return result, err
}

func (c *Client) GenerationStatus(ctx context.Context, project, gitCommit string) (SnapshotGeneration, error) {
	var result SnapshotGeneration
	requestID, err := c.doJSON(ctx, http.MethodGet, c.snapshotPath(project, gitCommit), nil, &result)
	result.RequestID = requestID
	return result, err
}

func (c *Client) AbortGeneration(ctx context.Context, project, gitCommit string) (SnapshotGeneration, error) {
	var result SnapshotGeneration
	requestID, err := c.doJSON(ctx, http.MethodDelete, c.snapshotPath(project, gitCommit), nil, &result)
	result.RequestID = requestID
	return result, err
}

func (c *Client) CreateRelease(ctx context.Context, project string, input CreateReleaseRequest) (ProjectRelease, error) {
	var result ProjectRelease
	requestID, err := c.doJSON(ctx, http.MethodPost, c.releasesPath(project), input, &result)
	result.RequestID = requestID
	return result, err
}

func (c *Client) ReleaseStatus(ctx context.Context, project, releaseID string) (ProjectRelease, error) {
	var result ProjectRelease
	requestID, err := c.doJSON(ctx, http.MethodGet, c.releasesPath(project)+"/"+url.PathEscape(releaseID), nil, &result)
	result.RequestID = requestID
	return result, err
}

func (c *Client) ActiveRelease(ctx context.Context, project string) (ActiveRelease, error) {
	var result ActiveRelease
	requestID, err := c.doJSON(ctx, http.MethodGet, c.releasesPath(project)+"/active", nil, &result)
	result.RequestID = requestID
	return result, err
}

func (c *Client) ActivateRelease(ctx context.Context, project, releaseID string, input ActivateReleaseRequest) (ActiveRelease, error) {
	var result ActiveRelease
	requestID, err := c.doJSON(ctx, http.MethodPost, c.releasesPath(project)+"/"+url.PathEscape(releaseID)+"/activate", input, &result)
	result.RequestID = requestID
	return result, err
}

func (c *Client) StartMaterialization(ctx context.Context, input MaterializationRequest) (MaterializationExecution, error) {
	if err := input.Selector.Validate(); err != nil {
		return MaterializationExecution{}, err
	}
	variables := map[string]any{"input": map[string]any{"project": input.Project, "generation": input.Generation, "selector": input.Selector}}
	var data struct {
		Execution MaterializationExecution `json:"startDataframeMaterialization"`
	}
	requestID, err := c.graphql(ctx, startMaterializationMutation, variables, &data)
	data.Execution.TransportRequestID = requestID
	return data.Execution, err
}

func (c *Client) MaterializationStatus(ctx context.Context, executionID string) (MaterializationExecution, error) {
	var data struct {
		Execution MaterializationExecution `json:"dataframeRecipeExecution"`
	}
	requestID, err := c.graphql(ctx, materializationStatusQuery, map[string]any{"id": executionID}, &data)
	data.Execution.TransportRequestID = requestID
	if err == nil && data.Execution.ID == "" {
		return MaterializationExecution{}, &APIError{Code: "RECIPE_EXECUTION_NOT_FOUND", Message: "materialization execution was not found", RequestID: requestID}
	}
	return data.Execution, err
}

func (c *Client) snapshotPath(project, gitCommit string) string {
	return "/api/v1/projects/" + url.PathEscape(project) + "/generations/" + url.PathEscape(gitCommit)
}

func (c *Client) releasesPath(project string) string {
	return "/api/v1/projects/" + url.PathEscape(project) + "/releases"
}

func (c *Client) doJSON(ctx context.Context, method, endpointPath string, input any, output any) (string, error) {
	var payload []byte
	var err error
	if input != nil {
		payload, err = json.Marshal(input)
		if err != nil {
			return "", err
		}
	}
	requestID := uuid.NewString()
	factory := func() (*http.Request, error) {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		request, err := http.NewRequestWithContext(ctx, method, c.endpoint(endpointPath), body)
		if err != nil {
			return nil, err
		}
		if payload != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		c.decorate(request, requestID)
		return request, nil
	}
	return c.do(ctx, factory, output)
}

func (c *Client) do(ctx context.Context, factory func() (*http.Request, error), output any) (string, error) {
	var last error
	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		request, err := factory()
		if err != nil {
			return "", err
		}
		response, err := c.httpClient.Do(request)
		if err != nil {
			if request.Body != nil {
				_ = request.Body.Close()
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			last = &TransportError{Cause: err}
		} else {
			responseID := response.Header.Get("X-Request-ID")
			if responseID == "" {
				responseID = request.Header.Get("X-Request-ID")
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
			_ = response.Body.Close()
			if readErr != nil {
				last = &TransportError{Cause: readErr}
			} else if len(body) > maxResponseBytes {
				return responseID, fmt.Errorf("Loom response exceeds %d bytes", maxResponseBytes)
			} else if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
				last = decodeAPIError(response.StatusCode, responseID, body)
			} else if output != nil && len(bytes.TrimSpace(body)) != 0 {
				if err := json.Unmarshal(body, output); err != nil {
					last = &TransportError{Cause: fmt.Errorf("decode Loom response: %w", err)}
				} else {
					return responseID, nil
				}
			} else {
				return responseID, nil
			}
		}
		if !IsRetryable(last) || attempt == c.retry.MaxAttempts {
			return request.Header.Get("X-Request-ID"), last
		}
		if err := c.retry.Sleep(ctx, c.retry.Backoff(attempt)); err != nil {
			return request.Header.Get("X-Request-ID"), err
		}
	}
	return "", last
}

func decodeAPIError(status int, responseID string, body []byte) error {
	var envelope struct {
		Error struct {
			Code      string         `json:"code"`
			Message   string         `json:"message"`
			Phase     string         `json:"phase"`
			Output    string         `json:"output"`
			Details   map[string]any `json:"details"`
			Retryable bool           `json:"retryable"`
			RequestID string         `json:"requestId"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Code == "" {
		return &APIError{Status: status, Code: "HTTP_ERROR", Message: strings.TrimSpace(string(body)), RequestID: responseID}
	}
	if envelope.Error.RequestID == "" {
		envelope.Error.RequestID = responseID
	}
	return &APIError{Status: status, Code: envelope.Error.Code, Message: envelope.Error.Message, Phase: envelope.Error.Phase, Output: envelope.Error.Output, Details: envelope.Error.Details, CanRetry: envelope.Error.Retryable, RequestID: envelope.Error.RequestID}
}

func (c *Client) decorate(request *http.Request, requestID string) {
	for name, values := range c.headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	request.Header.Set("X-Request-ID", requestID)
}

func (c *Client) endpoint(endpointPath string) string {
	copy := *c.baseURL
	copy.Path = path.Join(strings.TrimSuffix(c.baseURL.Path, "/"), endpointPath)
	return copy.String()
}

type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type graphqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code      string         `json:"code"`
		Phase     string         `json:"phase"`
		Output    string         `json:"output"`
		Details   map[string]any `json:"details"`
		RequestID string         `json:"requestId"`
		Retryable bool           `json:"retryable"`
	} `json:"extensions"`
}

func (c *Client) graphql(ctx context.Context, query string, variables map[string]any, data any) (string, error) {
	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		var envelope struct {
			Data   json.RawMessage `json:"data"`
			Errors []graphqlError  `json:"errors"`
		}
		requestID, err := c.doJSON(ctx, http.MethodPost, c.graphqlPath, graphqlRequest{Query: query, Variables: variables}, &envelope)
		if err != nil {
			return requestID, err
		}
		if len(envelope.Errors) != 0 {
			first := envelope.Errors[0]
			if first.Extensions.RequestID == "" {
				first.Extensions.RequestID = requestID
			}
			apiErr := &APIError{Status: http.StatusOK, Code: first.Extensions.Code, Message: first.Message, Phase: first.Extensions.Phase, Output: first.Extensions.Output, Details: first.Extensions.Details, CanRetry: first.Extensions.Retryable, RequestID: first.Extensions.RequestID}
			if !apiErr.Retryable() || attempt == c.retry.MaxAttempts {
				return requestID, apiErr
			}
			if err := c.retry.Sleep(ctx, c.retry.Backoff(attempt)); err != nil {
				return requestID, err
			}
			continue
		}
		if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
			return requestID, &APIError{Status: http.StatusOK, Code: "GRAPHQL_EMPTY_RESPONSE", Message: "Loom returned no GraphQL data", RequestID: requestID}
		}
		if err := json.Unmarshal(envelope.Data, data); err != nil {
			return requestID, &TransportError{Cause: fmt.Errorf("decode Loom GraphQL data: %w", err)}
		}
		return requestID, nil
	}
	return "", &APIError{Code: "GRAPHQL_RETRY_EXHAUSTED", Message: "Loom GraphQL retry attempts exhausted"}
}

const executionFields = `
  id
  name
  translationVersion
  sourceGeneration
  state
  phase
  error
  errorCode
  errorRetryable
  requestId
  outputs {
    name
    selector { recipe translationVersion output }
    state
    rowCount
    phase
    error
    errorCode
    errorRetryable
  }`

const startMaterializationMutation = `mutation StartDataframeMaterialization($input: StartDataframeMaterializationInput!) {
  startDataframeMaterialization(input: $input) {` + executionFields + `
  }
}`

const materializationStatusQuery = `query DataframeRecipeExecution($id: ID!) {
  dataframeRecipeExecution(id: $id) {` + executionFields + `
  }
}`

var _ LoomAPI = (*Client)(nil)
