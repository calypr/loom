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

// Profile submits a parameterized AQL query to ArangoDB's cursor endpoint
// with profiling enabled. It returns the first cursor response, which contains
// the complete profile metadata under extra. Callers should use a sufficiently
// large BatchSize when they need to profile a complete result export; normal
// dataframe execution remains on QueryRows and is never profiled implicitly.
func (c *Client) Profile(ctx context.Context, request ProfileRequest) (ProfileResult, error) {
	if strings.TrimSpace(request.Query) == "" {
		return ProfileResult{}, fmt.Errorf("AQL profile query is required")
	}
	if request.Options.Profile == 0 {
		request.Options.Profile = 2
	}
	body, err := json.Marshal(request)
	if err != nil {
		return ProfileResult{}, fmt.Errorf("encode AQL profile request: %w", err)
	}
	endpoint := fmt.Sprintf("%s/_db/%s/_api/cursor", strings.TrimRight(c.rawURL, "/"), url.PathEscape(c.dbName))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ProfileResult{}, fmt.Errorf("create AQL profile request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(httpRequest)
	if err != nil {
		return ProfileResult{}, fmt.Errorf("send AQL profile request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return ProfileResult{}, fmt.Errorf("read AQL profile response: %w", err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return ProfileResult{}, fmt.Errorf("AQL profile HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	result, err := ParseProfileResult(responseBody)
	if err != nil {
		return ProfileResult{}, err
	}
	return result, nil
}
