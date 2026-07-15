// Package elasticsearch contains the small HTTP surface Loom needs for
// Elasticsearch/OpenSearch publication. It intentionally avoids a generated
// client so the server remains compatible with both products.
package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Options struct {
	URL            string
	Username       string
	Password       string
	HTTPClient     *http.Client
	RequestTimeout time.Duration
	MaxRetries     int
	RetryBase      time.Duration
}

type Client struct {
	baseURL        string
	username       string
	password       string
	httpClient     *http.Client
	requestTimeout time.Duration
	maxRetries     int
	retryBase      time.Duration
}

type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("elasticsearch HTTP %d: %s", e.Status, e.Body) }

type BulkDocument struct {
	ID     string
	Source map[string]any
}

type BulkItem struct {
	ID     string
	Status int
	Error  json.RawMessage
}

type BulkResult struct {
	Errors bool
	Items  []BulkItem
}

func New(opts Options) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.URL), "/")
	if base == "" {
		return nil, fmt.Errorf("elasticsearch URL is required")
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return nil, fmt.Errorf("invalid elasticsearch URL: %w", err)
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		timeout := opts.RequestTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	}
	if opts.RetryBase <= 0 {
		opts.RetryBase = 100 * time.Millisecond
	}
	return &Client{baseURL: base, username: opts.Username, password: opts.Password, httpClient: httpClient, requestTimeout: opts.RequestTimeout, maxRetries: opts.MaxRetries, retryBase: opts.RetryBase}, nil
}

func (c *Client) CreateIndex(ctx context.Context, index string, properties map[string]map[string]any, shards, replicas int) error {
	settings := map[string]any{}
	if shards > 0 {
		settings["number_of_shards"] = shards
	}
	if replicas >= 0 {
		settings["number_of_replicas"] = replicas
	}
	payload := map[string]any{"mappings": map[string]any{"properties": properties}}
	if len(settings) > 0 {
		payload["settings"] = settings
	}
	return c.doJSON(ctx, http.MethodPut, "/"+url.PathEscape(index), payload, nil)
}

func (c *Client) DeleteIndex(ctx context.Context, index string) error {
	return c.doJSON(ctx, http.MethodDelete, "/"+url.PathEscape(index), nil, nil)
}

func (c *Client) Refresh(ctx context.Context, index string) error {
	return c.doJSON(ctx, http.MethodPost, "/"+url.PathEscape(index)+"/_refresh", nil, nil)
}

func (c *Client) Count(ctx context.Context, index string) (int64, error) {
	var response struct {
		Count int64 `json:"count"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/"+url.PathEscape(index)+"/_count", nil, &response); err != nil {
		return 0, err
	}
	return response.Count, nil
}

func (c *Client) AliasIndices(ctx context.Context, alias string) ([]string, error) {
	var response map[string]json.RawMessage
	if err := c.doJSON(ctx, http.MethodGet, "/_alias/"+url.PathEscape(alias), nil, &response); err != nil {
		if httpErr, ok := err.(*HTTPError); ok && httpErr.Status == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	indices := make([]string, 0, len(response))
	for index := range response {
		indices = append(indices, index)
	}
	return indices, nil
}

type AliasAction struct {
	Action string
	Index  string
	Alias  string
}

func (c *Client) SwapAliases(ctx context.Context, actions []AliasAction) error {
	request := map[string]any{"actions": make([]map[string]map[string]string, 0, len(actions))}
	for _, action := range actions {
		if action.Action != "add" && action.Action != "remove" {
			return fmt.Errorf("unsupported alias action %q", action.Action)
		}
		request["actions"] = append(request["actions"].([]map[string]map[string]string), map[string]map[string]string{action.Action: {"index": action.Index, "alias": action.Alias}})
	}
	return c.doJSON(ctx, http.MethodPost, "/_aliases", request, nil)
}

func (c *Client) Bulk(ctx context.Context, index string, documents []BulkDocument) (BulkResult, error) {
	if len(documents) == 0 {
		return BulkResult{}, nil
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, document := range documents {
		if strings.TrimSpace(document.ID) == "" {
			return BulkResult{}, fmt.Errorf("bulk document ID is required")
		}
		if err := encoder.Encode(map[string]any{"index": map[string]string{"_index": index, "_id": document.ID}}); err != nil {
			return BulkResult{}, err
		}
		if err := encoder.Encode(document.Source); err != nil {
			return BulkResult{}, err
		}
	}
	var response struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			ID     string          `json:"_id"`
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"items"`
	}
	if err := c.do(ctx, http.MethodPost, "/_bulk", body.Bytes(), "application/x-ndjson", &response); err != nil {
		return BulkResult{}, err
	}
	result := BulkResult{Errors: response.Errors, Items: make([]BulkItem, 0, len(response.Items))}
	for _, item := range response.Items {
		for _, value := range item {
			result.Items = append(result.Items, BulkItem{ID: value.ID, Status: value.Status, Error: value.Error})
		}
	}
	return result, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, payload, out any) error {
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	return c.do(ctx, method, path, body, "application/json", out)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, contentType string, out any) error {
	attempts := c.maxRetries + 1
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		reqCtx := ctx
		if c.requestTimeout > 0 {
			var cancel context.CancelFunc
			reqCtx, cancel = context.WithTimeout(ctx, c.requestTimeout)
			defer cancel()
		}
		request, err := http.NewRequestWithContext(reqCtx, method, c.baseURL+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		if c.username != "" {
			request.SetBasicAuth(c.username, c.password)
		}
		response, err := c.httpClient.Do(request)
		if err != nil {
			if attempt+1 < attempts {
				if err := sleepRetry(ctx, c.retryBase, attempt); err != nil {
					return err
				}
				continue
			}
			return err
		}
		responseBody, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			if attempt+1 < attempts && retryableStatus(response.StatusCode) {
				if err := sleepRetry(ctx, c.retryBase, attempt); err != nil {
					return err
				}
				continue
			}
			return &HTTPError{Status: response.StatusCode, Body: string(responseBody)}
		}
		if out != nil && len(responseBody) > 0 {
			if err := json.Unmarshal(responseBody, out); err != nil {
				return fmt.Errorf("decode elasticsearch response: %w", err)
			}
		}
		return nil
	}
	return fmt.Errorf("elasticsearch request exhausted retries")
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func sleepRetry(ctx context.Context, base time.Duration, attempt int) error {
	delay := base * time.Duration(1<<min(attempt, 6))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
