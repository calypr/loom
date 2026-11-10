package ingest

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
	"unicode"
)

const (
	maxAttempts     = 4
	initialBackoff  = 250 * time.Millisecond
	maximumBackoff  = 2 * time.Second
	maxResponseSize = 1 << 20
	maxManifestSize = 1 << 20
	maxFileParts    = 2000
)

// Client streams source files into Loom and validates its durable receipt.
type Client struct {
	baseURL    *url.URL
	bearer     string
	httpClient *http.Client
}

// ManifestFile identifies a canonical repository path. Loom computes its digest
// from the uploaded bytes.
type ManifestFile struct {
	Path string `json:"path"`
}

// Manifest records an immutable repository snapshot and the selected paths.
type Manifest struct {
	Repository string         `json:"repository"`
	Commit     string         `json:"commit"`
	Files      []ManifestFile `json:"files"`
}

// File maps a manifest path to a private local file that is reopened for each
// retry. The bytes are streamed directly into the HTTP request.
type File struct {
	Path      string
	LocalPath string
}

// Request contains one authorized source publication attempt.
type Request struct {
	Project          string
	AuthResourcePath string
	OperationID      string
	Manifest         Manifest
	Files            []File
}

// Receipt is Loom's durable source and ClickHouse publication result.
type Receipt struct {
	OperationID    string `json:"operation_id"`
	SourceRevision int64  `json:"source_revision"`
	ManifestHash   string `json:"manifest_hash"`
	ExecutionID    string `json:"execution_id"`
	State          string `json:"state"`
	Reused         bool   `json:"reused"`
}

// Error is a structured HTTP or transport failure. Its message is safe to log:
// it never includes request headers or the bearer token.
type Error struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
	Retryable  bool
	Attempts   int
	cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return "Loom source publication failed"
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("Loom source publication failed (HTTP %d, %s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("Loom source publication failed (%s): %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// New creates a client for one Loom API base URL. A nil HTTP client uses the
// standard client; request deadlines come from the caller's context.
func New(baseURL, bearerToken string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, errors.New("Loom base URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Loom base URL must be an absolute HTTP or HTTPS URL without credentials, query, or fragment")
	}
	if strings.TrimSpace(bearerToken) == "" || strings.TrimSpace(bearerToken) != bearerToken || strings.ContainsAny(bearerToken, "\r\n") {
		return nil, errors.New("a single-line bearer token is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	} else {
		copy := *httpClient
		httpClient = &copy
	}
	// Never forward the bearer token to a redirect target. The Loom endpoint is
	// configured explicitly, so redirects are transport errors for this client.
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawPath = ""
	return &Client{baseURL: parsed, bearer: bearerToken, httpClient: httpClient}, nil
}

// Publish streams selected files to Loom. Retries reuse the operation ID and
// reopen each local file so no source dataset is copied into memory.
func (c *Client) Publish(ctx context.Context, request Request) (Receipt, error) {
	if c == nil || c.httpClient == nil || c.baseURL == nil {
		return Receipt{}, &Error{Code: "INVALID_CLIENT", Message: "client is not initialized"}
	}
	if err := validateRequest(request); err != nil {
		return Receipt{}, &Error{Code: "INVALID_REQUEST", Message: redactToken(err.Error(), c.bearer), cause: err}
	}
	endpoint, err := c.endpoint(request.Project)
	if err != nil {
		return Receipt{}, &Error{Code: "INVALID_REQUEST", Message: "project path is invalid", cause: err}
	}
	manifest, err := json.Marshal(request.Manifest)
	if err != nil || len(manifest) > maxManifestSize {
		return Receipt{}, &Error{Code: "INVALID_REQUEST", Message: "manifest is invalid or too large", cause: err}
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Receipt{}, err
		}
		receipt, publishErr := c.publishAttempt(ctx, endpoint, request, manifest)
		if publishErr == nil {
			return receipt, nil
		}
		var structured *Error
		if !errors.As(publishErr, &structured) || !structured.Retryable || attempt == maxAttempts {
			if structured != nil {
				structured.Attempts = attempt
			}
			return Receipt{}, publishErr
		}
		structured.Attempts = attempt
		lastErr = structured
		if err := waitForRetry(ctx, retryDelay(attempt)); err != nil {
			return Receipt{}, err
		}
	}
	return Receipt{}, lastErr
}

func (c *Client) endpoint(project string) (string, error) {
	endpoint := *c.baseURL
	basePath := strings.TrimSuffix(endpoint.EscapedPath(), "/")
	if basePath == "/" {
		basePath = ""
	}
	rawPath := basePath + "/api/v1/datasets/" + url.PathEscape(project) + "/sync-and-publish"
	decodedPath, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", err
	}
	endpoint.Path = decodedPath
	endpoint.RawPath = rawPath
	return endpoint.String(), nil
}

func validateRequest(request Request) error {
	if strings.TrimSpace(request.Project) == "" {
		return errors.New("project is required")
	}
	if strings.TrimSpace(request.OperationID) == "" || request.OperationID != strings.TrimSpace(request.OperationID) || len(request.OperationID) > 200 {
		return errors.New("operation ID must be non-empty, have no surrounding whitespace, and be at most 200 bytes")
	}
	for _, character := range request.OperationID {
		if unicode.IsControl(character) {
			return errors.New("operation ID cannot contain control characters")
		}
	}
	if strings.TrimSpace(request.Manifest.Repository) == "" || strings.TrimSpace(request.Manifest.Commit) == "" || len(request.Manifest.Files) == 0 || len(request.Manifest.Files) > maxFileParts {
		return fmt.Errorf("manifest requires repository, commit, and between 1 and %d selected files", maxFileParts)
	}
	if len(request.Files) != len(request.Manifest.Files) {
		return errors.New("uploaded files must match the manifest paths exactly")
	}
	manifestPaths := make(map[string]struct{}, len(request.Manifest.Files))
	for _, item := range request.Manifest.Files {
		if err := validateManifestPath(item.Path); err != nil {
			return err
		}
		if _, exists := manifestPaths[item.Path]; exists {
			return fmt.Errorf("manifest contains duplicate path %q", item.Path)
		}
		manifestPaths[item.Path] = struct{}{}
	}
	filePaths := make(map[string]struct{}, len(request.Files))
	for _, file := range request.Files {
		if err := validateManifestPath(file.Path); err != nil {
			return err
		}
		if _, ok := manifestPaths[file.Path]; !ok {
			return fmt.Errorf("file %q is absent from the manifest", file.Path)
		}
		if _, exists := filePaths[file.Path]; exists {
			return fmt.Errorf("request contains duplicate file %q", file.Path)
		}
		filePaths[file.Path] = struct{}{}
		if strings.TrimSpace(file.LocalPath) == "" {
			return fmt.Errorf("local path for %q is required", file.Path)
		}
		info, err := os.Stat(file.LocalPath)
		if err != nil {
			return fmt.Errorf("inspect local source file %q: %w", file.Path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("local source file %q is not a regular file", file.Path)
		}
	}
	return nil
}

func validateManifestPath(value string) error {
	if strings.Contains(value, "\\") || !strings.HasPrefix(value, "META/") || !strings.HasSuffix(value, ".ndjson") || path.Clean(value) != value {
		return fmt.Errorf("source path %q must be canonical META/<ResourceType>.ndjson", value)
	}
	resource := strings.TrimSuffix(strings.TrimPrefix(value, "META/"), ".ndjson")
	if resource == "" || strings.Contains(resource, "/") || resource == "." || resource == ".." {
		return fmt.Errorf("source path %q must name one resource type", value)
	}
	if resource[0] < 'A' || resource[0] > 'Z' {
		return fmt.Errorf("source path %q has an invalid resource type name", value)
	}
	for _, character := range resource[1:] {
		if !((character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')) {
			return fmt.Errorf("source path %q has an invalid resource type name", value)
		}
	}
	return nil
}

func (c *Client) publishAttempt(ctx context.Context, endpoint string, request Request, manifest []byte) (Receipt, error) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	requestHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, reader)
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return Receipt{}, &Error{Code: "INVALID_REQUEST", Message: "could not build HTTP request", cause: err}
	}
	requestHTTP.Header.Set("Authorization", "Bearer "+c.bearer)
	requestHTTP.Header.Set("Idempotency-Key", request.OperationID)
	requestHTTP.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	writeDone := make(chan error, 1)
	go func() {
		writeErr := writeMultipart(multipartWriter, request, manifest)
		if writeErr == nil {
			writeErr = multipartWriter.Close()
		}
		if writeErr != nil {
			_ = writer.CloseWithError(writeErr)
		} else {
			_ = writer.Close()
		}
		writeDone <- writeErr
	}()

	response, doErr := c.httpClient.Do(requestHTTP)
	if doErr != nil {
		_ = reader.CloseWithError(doErr)
		writeErr := <-writeDone
		if err := ctx.Err(); err != nil {
			return Receipt{}, err
		}
		var fileErr *localFileReadError
		if errors.As(writeErr, &fileErr) {
			return Receipt{}, &Error{Code: "LOCAL_FILE_ERROR", Message: "could not read a staged source file", cause: fileErr}
		}
		return Receipt{}, &Error{Code: "NETWORK_ERROR", Message: "source publish transport failed", Retryable: true, cause: doErr}
	}
	defer response.Body.Close()
	// Closing the reader releases the multipart writer if the server rejected
	// the request before consuming every part.
	_ = reader.Close()
	writeErr := <-writeDone
	if writeErr != nil {
		if err := ctx.Err(); err != nil {
			return Receipt{}, err
		}
		var fileErr *localFileReadError
		if errors.As(writeErr, &fileErr) {
			return Receipt{}, &Error{Code: "LOCAL_FILE_ERROR", Message: "could not read a staged source file", cause: fileErr}
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return Receipt{}, &Error{Code: "NETWORK_ERROR", Message: "source publish upload was interrupted", Retryable: true, cause: writeErr}
		}
	}
	body, bodyErr := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if bodyErr != nil {
		if err := ctx.Err(); err != nil {
			return Receipt{}, err
		}
		return Receipt{}, &Error{StatusCode: response.StatusCode, Code: "RESPONSE_READ_ERROR", Message: "could not read Loom response", Retryable: true, cause: bodyErr}
	}
	if len(body) > maxResponseSize {
		return Receipt{}, &Error{StatusCode: response.StatusCode, Code: "RESPONSE_TOO_LARGE", Message: "Loom response exceeded the client limit"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Receipt{}, responseError(response.StatusCode, body, c.bearer)
	}
	return decodeReceipt(body, request)
}

type localFileReadError struct {
	path string
	err  error
}

func (e *localFileReadError) Error() string { return "read staged source file: " + e.err.Error() }
func (e *localFileReadError) Unwrap() error { return e.err }

type localFileReader struct {
	file *os.File
	path string
}

func (r localFileReader) Read(data []byte) (int, error) {
	n, err := r.file.Read(data)
	if err != nil && err != io.EOF {
		return n, &localFileReadError{path: r.path, err: err}
	}
	return n, err
}

func writeMultipart(writer *multipart.Writer, request Request, manifest []byte) error {
	if err := writer.WriteField("manifest", string(manifest)); err != nil {
		return err
	}
	if request.AuthResourcePath != "" {
		if err := writer.WriteField("auth_resource_path", request.AuthResourcePath); err != nil {
			return err
		}
	}
	for _, file := range request.Files {
		local, err := os.Open(file.LocalPath)
		if err != nil {
			return &localFileReadError{path: file.LocalPath, err: err}
		}
		part, err := writer.CreateFormFile("file", path.Base(file.Path))
		if err != nil {
			_ = local.Close()
			return err
		}
		_, copyErr := io.Copy(part, localFileReader{file: local, path: file.LocalPath})
		closeErr := local.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return &localFileReadError{path: file.LocalPath, err: closeErr}
		}
	}
	return nil
}

type wireReceipt struct {
	OperationID    *string `json:"operation_id"`
	SourceRevision *int64  `json:"source_revision"`
	ManifestHash   *string `json:"manifest_hash"`
	ExecutionID    *string `json:"execution_id"`
	State          *string `json:"state"`
	Reused         *bool   `json:"reused"`
}

func decodeReceipt(body []byte, request Request) (Receipt, error) {
	var payload wireReceipt
	if err := json.Unmarshal(body, &payload); err != nil {
		return Receipt{}, &Error{Code: "INVALID_RECEIPT", Message: "Loom returned an invalid publication receipt", cause: err}
	}
	if payload.OperationID == nil || payload.SourceRevision == nil || payload.ManifestHash == nil || payload.ExecutionID == nil || payload.State == nil || payload.Reused == nil {
		return Receipt{}, &Error{Code: "INVALID_RECEIPT", Message: "Loom receipt is missing required fields"}
	}
	receipt := Receipt{
		OperationID: *payload.OperationID, SourceRevision: *payload.SourceRevision,
		ManifestHash: *payload.ManifestHash, ExecutionID: *payload.ExecutionID,
		State: *payload.State, Reused: *payload.Reused,
	}
	if receipt.OperationID != request.OperationID || receipt.SourceRevision <= 0 || receipt.State != "PUBLISHED" || strings.TrimSpace(receipt.ExecutionID) == "" || len(receipt.ManifestHash) != 64 {
		return Receipt{}, &Error{Code: "INVALID_RECEIPT", Message: "Loom receipt does not match the requested publication"}
	}
	if _, err := hex.DecodeString(receipt.ManifestHash); err != nil {
		return Receipt{}, &Error{Code: "INVALID_RECEIPT", Message: "Loom receipt manifest hash is invalid"}
	}
	return receipt, nil
}

func responseError(status int, body []byte, token string) *Error {
	var payload struct {
		Error struct {
			Code       string `json:"code"`
			Message    string `json:"message"`
			RequestID  string `json:"requestId"`
			RequestID2 string `json:"request_id"`
			Retryable  bool   `json:"retryable"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	code := payload.Error.Code
	if code == "" {
		code = http.StatusText(status)
	}
	message := strings.TrimSpace(payload.Error.Message)
	if message == "" {
		message = "Loom rejected the source publication"
	}
	code = redactToken(code, token)
	message = redactToken(message, token)
	requestID := payload.Error.RequestID
	if requestID == "" {
		requestID = payload.Error.RequestID2
	}
	requestID = redactToken(requestID, token)
	return &Error{
		StatusCode: status, Code: code, Message: message, RequestID: requestID,
		Retryable: status >= 500 || payload.Error.Retryable,
	}
}

func redactToken(value, token string) string {
	if token == "" || value == "" {
		return value
	}
	return strings.ReplaceAll(value, token, "[redacted]")
}

func retryDelay(attempt int) time.Duration {
	delay := initialBackoff
	for i := 1; i < attempt && delay < maximumBackoff; i++ {
		delay *= 2
	}
	if delay > maximumBackoff {
		return maximumBackoff
	}
	return delay
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
