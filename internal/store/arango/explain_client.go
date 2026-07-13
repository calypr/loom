package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Explain submits a typed AQL explain request to this client's database. It is
// intentionally separate from QueryRows so compiler tooling can inspect a plan
// without executing a dataframe query.
func (c *Client) Explain(ctx context.Context, request ExplainRequest) (ExplainResult, error) {
	if strings.TrimSpace(request.Query) == "" {
		return ExplainResult{}, fmt.Errorf("AQL explain query is required")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return ExplainResult{}, fmt.Errorf("encode AQL explain request: %w", err)
	}
	endpoint := fmt.Sprintf("%s/_db/%s/_api/explain", strings.TrimRight(c.rawURL, "/"), url.PathEscape(c.dbName))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ExplainResult{}, fmt.Errorf("create AQL explain request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := c.client.Do(httpRequest)
	if err != nil {
		return ExplainResult{}, fmt.Errorf("send AQL explain request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return ExplainResult{}, fmt.Errorf("read AQL explain response: %w", err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return ExplainResult{}, fmt.Errorf("AQL explain HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	result, err := ParseExplainResult(responseBody)
	if err != nil {
		return ExplainResult{}, err
	}
	return result, nil
}
