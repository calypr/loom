package authoring

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxResponseSize = 1 << 20

type Client struct {
	baseURL    *url.URL
	bearer     string
	httpClient *http.Client
}

type PublishRequest struct {
	Project          string
	ExplorerID       string
	AuthResourcePath string
	ReceiptID        string
}

type Error struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
	cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return "Loom authoring publication failed"
	}
	message := e.Message
	if message == "" {
		message = "publication request failed"
	}
	if e.StatusCode != 0 {
		message = fmt.Sprintf("Loom authoring publication failed (HTTP %d): %s", e.StatusCode, message)
	} else {
		message = "Loom authoring publication failed: " + message
	}
	if e.Code != "" {
		message += " code=" + e.Code
	}
	if e.RequestID != "" {
		message += " request_id=" + e.RequestID
	}
	return message
}

func (e *Error) Unwrap() error {
	return e.cause
}

func New(baseURL, bearerToken string, httpClient *http.Client) (*Client, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("Loom URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if strings.TrimSpace(bearerToken) == "" {
		return nil, errors.New("Loom bearer token is required")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	copyClient := *httpClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{baseURL: base, bearer: bearerToken, httpClient: &copyClient}, nil
}

func (c *Client) Publish(ctx context.Context, input PublishRequest) (json.RawMessage, error) {
	if c == nil || c.baseURL == nil {
		return nil, errors.New("Loom authoring client is not configured")
	}
	if input.Project == "" || input.ExplorerID == "" || input.AuthResourcePath == "" || input.ReceiptID == "" {
		return nil, errors.New("project, explorer ID, authorization resource path, and receipt ID are required")
	}
	endpoint, err := url.Parse(c.endpoint(input.Project, input.ExplorerID))
	if err != nil {
		return nil, fmt.Errorf("build Loom publication URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("auth_resource_path", input.AuthResourcePath)
	endpoint.RawQuery = query.Encode()
	body, err := json.Marshal(struct {
		ReceiptID string `json:"receiptId"`
	}{ReceiptID: input.ReceiptID})
	if err != nil {
		return nil, fmt.Errorf("encode Loom publication request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Loom publication request: %w", err)
	}
	req.Header.Set("Authorization", "bearer "+c.bearer)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &Error{Message: "request failed", cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, decodeError(response)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read Loom publication response: %w", err)
	}
	if len(raw) > maxResponseSize {
		return nil, errors.New("Loom publication response exceeds the size limit")
	}
	var result struct {
		ReceiptID  string `json:"receiptId"`
		RevisionID string `json:"revisionId"`
		State      string `json:"state"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode Loom publication receipt: %w", err)
	}
	if result.ReceiptID != input.ReceiptID || strings.TrimSpace(result.RevisionID) == "" || result.State != "ACTIVE" {
		return nil, errors.New("Loom returned a mismatched receipt, missing revision, or inactive publication")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, fmt.Errorf("compact Loom publication response: %w", err)
	}
	return compact.Bytes(), nil
}

func (c *Client) endpoint(project, explorerID string) string {
	escapedPath := strings.TrimRight(c.baseURL.EscapedPath(), "/") +
		"/api/v1/projects/" + url.PathEscape(url.PathEscape(project)) +
		"/explorers/" + url.PathEscape(explorerID) + "/authoring/v2/publish"
	return c.baseURL.Scheme + "://" + c.baseURL.Host + escapedPath
}

func decodeError(response *http.Response) error {
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"requestId"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&envelope); err != nil {
		return &Error{StatusCode: response.StatusCode, Message: "server returned an unreadable error response"}
	}
	message := strings.TrimSpace(envelope.Error.Message)
	if len(message) > 512 {
		message = message[:512]
	}
	return &Error{StatusCode: response.StatusCode, Code: envelope.Error.Code, Message: message, RequestID: envelope.Error.RequestID}
}
