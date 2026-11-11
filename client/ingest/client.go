package ingest

import (
	"bytes"
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
	maxPlanSize     = 1 << 20
	maxFileParts    = 2000
	busyRetryDelay  = 15 * time.Second
	busyRetryLimit  = 5 * time.Minute
)

// Client uploads selected source files into a durable Loom source load, then
// asks Loom to publish the completed load.
type Client struct {
	baseURL           *url.URL
	bearer            string
	httpClient        *http.Client
	busyRetryInterval time.Duration
	busyRetryBudget   time.Duration
}

// ManifestFile identifies a canonical repository path. SHA256 is optional and
// is included only when the caller already has an authoritative digest.
type ManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
}

// Manifest records an immutable repository snapshot and the selected paths.
type Manifest struct {
	Repository string         `json:"repository"`
	Commit     string         `json:"commit"`
	Files      []ManifestFile `json:"files"`
}

// File maps a selected manifest path to an optional local staged file.
// LocalPath may be empty when Request.AcquireFile hydrates that path lazily.
type File struct {
	Path      string
	LocalPath string
}

// Request contains one authorized source publication attempt. AcquireFile is
// called only for files that Loom has not already accepted, and its returned
// cleanup runs after that file's upload finishes (including all retries).
type Request struct {
	Project          string
	AuthResourcePath string
	OperationID      string
	Manifest         Manifest
	ExpectedRevision *int64
	Files            []File
	AcquireFile      func(ctx context.Context, path string) (localPath string, cleanup func(), err error)
}

// SourceFileReceipt records one file accepted by the inactive load generation.
type SourceFileReceipt struct {
	Path         string `json:"path"`
	ResourceType string `json:"resource_type"`
	SHA256       string `json:"sha256"`
	Rows         int64  `json:"rows"`
	State        string `json:"state"`
}

// SourceLoadStatus is the durable state returned by begin and status requests.
type SourceLoadStatus struct {
	OperationID  string              `json:"operation_id"`
	Generation   string              `json:"generation"`
	State        string              `json:"state"`
	BaseRevision int64               `json:"base_revision"`
	Files        []SourceFileReceipt `json:"files"`
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

// Issue is a safe, actionable source-row validation diagnostic. Loom omits raw
// resource contents from these fields.
type Issue struct {
	File              string `json:"file,omitempty"`
	Filename          string `json:"filename,omitempty"`
	Row               int64  `json:"row,omitempty"`
	Line              int64  `json:"line,omitempty"`
	ResourceType      string `json:"resourceType,omitempty"`
	ResourceTypeSnake string `json:"resource_type,omitempty"`
	Category          string `json:"category,omitempty"`
	Message           string `json:"message,omitempty"`
	Code              string `json:"code,omitempty"`
}

func (i *Issue) UnmarshalJSON(data []byte) error {
	var wire struct {
		File              string `json:"file"`
		Filename          string `json:"filename"`
		Row               int64  `json:"row"`
		Line              int64  `json:"line"`
		ResourceType      string `json:"resourceType"`
		ResourceTypeSnake string `json:"resource_type"`
		Category          string `json:"category"`
		Message           string `json:"message"`
		Code              string `json:"code"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	i.File = wire.File
	if i.File == "" {
		i.File = wire.Filename
	}
	i.Row = wire.Row
	if i.Row == 0 {
		i.Row = wire.Line
	}
	i.ResourceType = wire.ResourceType
	if i.ResourceType == "" {
		i.ResourceType = wire.ResourceTypeSnake
	}
	i.Category = wire.Category
	i.Message = wire.Message
	i.Code = wire.Code
	return nil
}

// Error is a structured HTTP or transport failure. Its message is safe to log:
// it never includes request headers or the bearer token.
type Error struct {
	StatusCode  int
	Code        string
	Message     string
	RequestID   string
	Issues      []Issue
	TotalErrors int64
	Truncated   bool
	Retryable   bool
	Attempts    int
	cause       error
}

func (e *Error) Error() string {
	if e == nil {
		return "Loom source publication failed"
	}
	var message string
	if e.StatusCode != 0 {
		message = fmt.Sprintf("Loom source publication failed (HTTP %d, %s): %s", e.StatusCode, e.Code, e.Message)
	} else {
		message = fmt.Sprintf("Loom source publication failed (%s): %s", e.Code, e.Message)
	}
	if e.RequestID != "" {
		message += " (request ID: " + e.RequestID + ")"
	}
	for _, issue := range e.Issues {
		message += "\n  - " + issue.publicMessage()
	}
	if e.TotalErrors > int64(len(e.Issues)) {
		message += fmt.Sprintf("\n  %d validation errors total", e.TotalErrors)
	}
	if e.Truncated {
		message += " (issue list truncated)"
	}
	return message
}

func (e *Error) Unwrap() error { return e.cause }

func (i Issue) publicMessage() string {
	file := i.File
	if file == "" {
		file = i.Filename
	}
	var location []string
	if file != "" {
		location = append(location, file)
	}
	if i.Row > 0 {
		location = append(location, fmt.Sprintf("row %d", i.Row))
	}
	if i.ResourceType != "" {
		location = append(location, "resource type "+i.ResourceType)
	}
	if i.Category != "" {
		location = append(location, i.Category)
	}
	if len(location) == 0 {
		if i.Message != "" {
			return i.Message
		}
		return i.Code
	}
	if i.Message != "" {
		return strings.Join(location, ", ") + ": " + i.Message
	}
	return strings.Join(location, ": ")
}

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
	return &Client{
		baseURL: parsed, bearer: bearerToken, httpClient: httpClient,
		busyRetryInterval: busyRetryDelay, busyRetryBudget: busyRetryLimit,
	}, nil
}

// Publish begins or resumes a durable source load, uploads missing files in
// manifest order, and returns only after Loom confirms the final publication.
func (c *Client) Publish(ctx context.Context, request Request) (Receipt, error) {
	if c == nil || c.httpClient == nil || c.baseURL == nil {
		return Receipt{}, &Error{Code: "INVALID_CLIENT", Message: "client is not initialized"}
	}
	if err := validateRequest(request); err != nil {
		return Receipt{}, &Error{Code: "INVALID_REQUEST", Message: redactToken(err.Error(), c.bearer), cause: err}
	}
	endpoint, err := c.endpoint(request.Project, request.OperationID)
	if err != nil {
		return Receipt{}, &Error{Code: "INVALID_REQUEST", Message: "project or operation path is invalid", cause: err}
	}
	plan := sourceLoadPlan{
		Repository: request.Manifest.Repository, Commit: request.Manifest.Commit,
		Files: request.Manifest.Files, AuthResourcePath: request.AuthResourcePath,
		ExpectedRevision: request.ExpectedRevision,
	}
	planBody, err := json.Marshal(plan)
	if err != nil || len(planBody) > maxPlanSize {
		return Receipt{}, &Error{Code: "INVALID_REQUEST", Message: "source load plan is invalid or too large", cause: err}
	}
	if _, err := c.begin(ctx, endpoint, request.OperationID, planBody); err != nil {
		return Receipt{}, err
	}
	status, err := c.loadStatus(ctx, endpoint, request.OperationID)
	if err != nil {
		return Receipt{}, err
	}
	if status.State == "SUPERSEDED" {
		return Receipt{}, &Error{StatusCode: http.StatusConflict, Code: "PUBLICATION_CONFLICT", Message: "this source load was superseded by a newer publication"}
	}
	completed, err := completedFiles(status, request)
	if err != nil {
		return Receipt{}, err
	}
	files := filesByPath(request.Files)
	for _, selected := range request.Manifest.Files {
		if _, ok := completed[selected.Path]; ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return Receipt{}, err
		}
		file := files[selected.Path]
		localPath := file.LocalPath
		var cleanup func()
		if localPath == "" {
			localPath, cleanup, err = request.AcquireFile(ctx, selected.Path)
			if err != nil {
				if cleanup != nil {
					cleanup()
				}
				return Receipt{}, &Error{Code: "LOCAL_FILE_ERROR", Message: fmt.Sprintf("could not prepare %s for Loom: %s", selected.Path, redactToken(err.Error(), c.bearer)), cause: err}
			}
		}
		if err := validateLocalFile(selected.Path, localPath); err != nil {
			if cleanup != nil {
				cleanup()
			}
			return Receipt{}, &Error{Code: "LOCAL_FILE_ERROR", Message: redactToken(err.Error(), c.bearer), cause: err}
		}
		fileReceipt, uploadErr := c.uploadFile(ctx, endpoint, request.OperationID, selected.Path, localPath)
		if cleanup != nil {
			cleanup()
		}
		if uploadErr != nil {
			return Receipt{}, uploadErr
		}
		if err := validateFileReceipt(fileReceipt, selected); err != nil {
			return Receipt{}, err
		}
		completed[selected.Path] = fileReceipt
	}
	return c.publish(ctx, endpoint, request.OperationID)
}

type sourceLoadPlan struct {
	Repository       string         `json:"repository"`
	Commit           string         `json:"commit"`
	Files            []ManifestFile `json:"files"`
	AuthResourcePath string         `json:"auth_resource_path"`
	ExpectedRevision *int64         `json:"expected_revision,omitempty"`
}

func (c *Client) endpoint(project, operationID string) (string, error) {
	endpoint := *c.baseURL
	basePath := strings.TrimSuffix(endpoint.EscapedPath(), "/")
	if basePath == "/" {
		basePath = ""
	}
	rawPath := basePath + "/api/v1/datasets/" + url.PathEscape(project) + "/source-loads/" + url.PathEscape(operationID)
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
	if strings.ContainsAny(request.Project, "/\\") {
		return errors.New("project must be one URL path component")
	}
	if strings.TrimSpace(request.AuthResourcePath) == "" {
		return errors.New("auth resource path is required")
	}
	if strings.TrimSpace(request.OperationID) == "" || request.OperationID != strings.TrimSpace(request.OperationID) || len(request.OperationID) > 200 {
		return errors.New("operation ID must be non-empty, have no surrounding whitespace, and be at most 200 bytes")
	}
	if strings.ContainsAny(request.OperationID, "/\\") {
		return errors.New("operation ID must be one URL path component")
	}
	for _, character := range request.OperationID {
		if unicode.IsControl(character) {
			return errors.New("operation ID cannot contain control characters")
		}
	}
	if strings.TrimSpace(request.Manifest.Repository) == "" || strings.TrimSpace(request.Manifest.Commit) == "" || len(request.Manifest.Files) == 0 || len(request.Manifest.Files) > maxFileParts {
		return fmt.Errorf("manifest requires repository, commit, and between 1 and %d selected files", maxFileParts)
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
	if request.AcquireFile == nil && len(request.Files) != len(request.Manifest.Files) {
		return errors.New("uploaded files must match the manifest paths exactly")
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
	}
	if request.AcquireFile == nil {
		for _, item := range request.Manifest.Files {
			file, ok := findFile(request.Files, item.Path)
			if !ok {
				return fmt.Errorf("manifest file %q is missing from the request", item.Path)
			}
			if strings.TrimSpace(file.LocalPath) == "" {
				return fmt.Errorf("local path for %q is required", item.Path)
			}
		}
	} else {
		for _, item := range request.Manifest.Files {
			if _, exists := filePaths[item.Path]; !exists {
				filePaths[item.Path] = struct{}{}
			}
		}
	}
	if request.ExpectedRevision != nil && *request.ExpectedRevision < 0 {
		return errors.New("expected revision cannot be negative")
	}
	return nil
}

func validateLocalFile(sourcePath, localPath string) error {
	if strings.TrimSpace(localPath) == "" {
		return fmt.Errorf("local path for %q is required", sourcePath)
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("inspect local source file %q: %w", sourcePath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("local source file %q is not a regular file", sourcePath)
	}
	return nil
}

func validateManifestPath(value string) error {
	if strings.Contains(value, "\\") || !strings.HasPrefix(value, "META/") || !strings.HasSuffix(value, ".ndjson") || path.Clean(value) != value {
		return fmt.Errorf("source path %q must be canonical META/<ResourceType>.ndjson", value)
	}
	resource := resourceType(value)
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

func resourceType(sourcePath string) string {
	return strings.TrimSuffix(strings.TrimPrefix(sourcePath, "META/"), ".ndjson")
}

func filesByPath(files []File) map[string]File {
	result := make(map[string]File, len(files))
	for _, file := range files {
		result[file.Path] = file
	}
	return result
}

func findFile(files []File, sourcePath string) (File, bool) {
	for _, file := range files {
		if file.Path == sourcePath {
			return file, true
		}
	}
	return File{}, false
}

func (c *Client) begin(ctx context.Context, endpoint, operationID string, plan []byte) (SourceLoadStatus, error) {
	response, err := c.doJSON(ctx, http.MethodPut, endpoint, operationID, plan)
	if err != nil {
		return SourceLoadStatus{}, err
	}
	status, err := decodeStatus(response)
	if err != nil {
		return SourceLoadStatus{}, err
	}
	return validateStatus(status, operationID)
}

func (c *Client) loadStatus(ctx context.Context, endpoint, operationID string) (SourceLoadStatus, error) {
	response, err := c.doJSON(ctx, http.MethodGet, endpoint, operationID, nil)
	if err != nil {
		return SourceLoadStatus{}, err
	}
	status, err := decodeStatus(response)
	if err != nil {
		return SourceLoadStatus{}, err
	}
	return validateStatus(status, operationID)
}

func (c *Client) publish(ctx context.Context, endpoint, operationID string) (Receipt, error) {
	response, err := c.doJSON(ctx, http.MethodPost, endpoint+"/publish", operationID, nil)
	if err != nil {
		return Receipt{}, err
	}
	return decodeReceipt(response, operationID)
}

func (c *Client) doJSON(ctx context.Context, method, endpoint, operationID string, body []byte) ([]byte, error) {
	var lastErr error
	retries := retryState{}
	attempts := 0
	for {
		attempts++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
		if err != nil {
			return nil, &Error{Code: "INVALID_REQUEST", Message: "could not build HTTP request", cause: err}
		}
		request.Header.Set("Authorization", "Bearer "+c.bearer)
		request.Header.Set("Idempotency-Key", operationID)
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, doErr := c.httpClient.Do(request)
		if doErr != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			lastErr = &Error{Code: "NETWORK_ERROR", Message: "source load transport failed", Retryable: true, cause: doErr}
		} else {
			payload, responseErr := c.readResponse(response)
			if responseErr != nil {
				lastErr = responseErr
			} else {
				return payload, nil
			}
		}
		var structured *Error
		if !errors.As(lastErr, &structured) {
			return nil, lastErr
		}
		structured.Attempts = attempts
		retry, waitErr := retries.wait(ctx, lastErr, c.busyRetryInterval, c.busyRetryBudget)
		if waitErr != nil {
			return nil, waitErr
		}
		if !retry {
			return nil, lastErr
		}
	}
}

func (c *Client) uploadFile(ctx context.Context, endpoint, operationID, sourcePath, localPath string) (SourceFileReceipt, error) {
	resource := resourceType(sourcePath)
	fileEndpoint := endpoint + "/files/" + url.PathEscape(resource)
	var lastErr error
	retries := retryState{}
	attempts := 0
	for {
		attempts++
		if err := ctx.Err(); err != nil {
			return SourceFileReceipt{}, err
		}
		payload, err := c.uploadFileOnce(ctx, fileEndpoint, operationID, sourcePath, localPath)
		if err == nil {
			receipt, decodeErr := decodeFileReceipt(payload)
			if decodeErr != nil {
				return SourceFileReceipt{}, decodeErr
			}
			return receipt, nil
		}
		lastErr = err
		var structured *Error
		if !errors.As(lastErr, &structured) {
			return SourceFileReceipt{}, lastErr
		}
		structured.Attempts = attempts
		retry, waitErr := retries.wait(ctx, lastErr, c.busyRetryInterval, c.busyRetryBudget)
		if waitErr != nil {
			return SourceFileReceipt{}, waitErr
		}
		if !retry {
			return SourceFileReceipt{}, lastErr
		}
	}
}

func (c *Client) uploadFileOnce(ctx context.Context, endpoint, operationID, sourcePath, localPath string) ([]byte, error) {
	local, err := os.Open(localPath)
	if err != nil {
		return nil, &Error{Code: "LOCAL_FILE_ERROR", Message: "could not open a staged source file", cause: &localFileReadError{path: localPath, err: err}}
	}
	defer local.Close()
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, reader)
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, &Error{Code: "INVALID_REQUEST", Message: "could not build file upload request", cause: err}
	}
	request.Header.Set("Authorization", "Bearer "+c.bearer)
	request.Header.Set("Idempotency-Key", operationID)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	writeDone := make(chan error, 1)
	go func() {
		part, writeErr := multipartWriter.CreateFormFile("file", sourcePath)
		if writeErr == nil {
			_, writeErr = io.Copy(part, localFileReader{file: local, path: localPath})
		}
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

	response, doErr := c.httpClient.Do(request)
	if doErr != nil {
		_ = reader.CloseWithError(doErr)
		writeErr := <-writeDone
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var fileErr *localFileReadError
		if errors.As(writeErr, &fileErr) {
			return nil, &Error{Code: "LOCAL_FILE_ERROR", Message: "could not read a staged source file", cause: fileErr}
		}
		return nil, &Error{Code: "NETWORK_ERROR", Message: "source file upload transport failed", Retryable: true, cause: doErr}
	}
	_ = reader.Close()
	writeErr := <-writeDone
	payload, responseErr := c.readResponse(response)
	if responseErr != nil {
		return nil, responseErr
	}
	if writeErr != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var fileErr *localFileReadError
		if errors.As(writeErr, &fileErr) {
			return nil, &Error{Code: "LOCAL_FILE_ERROR", Message: "could not read a staged source file", cause: fileErr}
		}
		return nil, &Error{Code: "NETWORK_ERROR", Message: "source file upload was interrupted", Retryable: true, cause: writeErr}
	}
	return payload, nil
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

func (c *Client) readResponse(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		retryable := response.StatusCode >= 200 && response.StatusCode < 300 || response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests
		return nil, &Error{StatusCode: response.StatusCode, Code: "RESPONSE_READ_ERROR", Message: "could not read Loom response", RequestID: redactToken(response.Header.Get("X-Request-ID"), c.bearer), Retryable: retryable, cause: err}
	}
	if len(body) > maxResponseSize {
		return nil, &Error{StatusCode: response.StatusCode, Code: "RESPONSE_TOO_LARGE", Message: "Loom response exceeded the client limit"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result := responseError(response.StatusCode, body, c.bearer)
		if result.RequestID == "" {
			result.RequestID = redactToken(response.Header.Get("X-Request-ID"), c.bearer)
		}
		return nil, result
	}
	return body, nil
}

func decodeStatus(body []byte) (SourceLoadStatus, error) {
	var status SourceLoadStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return SourceLoadStatus{}, &Error{Code: "INVALID_STATUS", Message: "Loom returned an invalid source load status", cause: err}
	}
	return status, nil
}

func validateStatus(status SourceLoadStatus, operationID string) (SourceLoadStatus, error) {
	if status.OperationID != operationID || strings.TrimSpace(status.Generation) == "" {
		return SourceLoadStatus{}, &Error{Code: "INVALID_STATUS", Message: "Loom source load status does not match the requested operation"}
	}
	switch status.State {
	case "UPLOADING", "READY", "PUBLISH_PENDING", "PUBLISHED", "SUPERSEDED":
	default:
		return SourceLoadStatus{}, &Error{Code: "INVALID_STATUS", Message: "Loom source load status has an unknown state"}
	}
	return status, nil
}

func completedFiles(status SourceLoadStatus, request Request) (map[string]SourceFileReceipt, error) {
	selected := make(map[string]ManifestFile, len(request.Manifest.Files))
	for _, file := range request.Manifest.Files {
		selected[file.Path] = file
	}
	completed := make(map[string]SourceFileReceipt, len(status.Files))
	for _, receipt := range status.Files {
		file, ok := selected[receipt.Path]
		if !ok {
			return nil, &Error{Code: "INVALID_STATUS", Message: "Loom source load contains a file outside the requested manifest"}
		}
		if err := validateFileReceipt(receipt, file); err != nil {
			return nil, err
		}
		if _, exists := completed[receipt.Path]; exists {
			return nil, &Error{Code: "INVALID_STATUS", Message: "Loom source load status contains a duplicate file receipt"}
		}
		completed[receipt.Path] = receipt
	}
	return completed, nil
}

func decodeFileReceipt(body []byte) (SourceFileReceipt, error) {
	var receipt SourceFileReceipt
	if err := json.Unmarshal(body, &receipt); err != nil {
		return SourceFileReceipt{}, &Error{Code: "INVALID_FILE_RECEIPT", Message: "Loom returned an invalid source file receipt", cause: err}
	}
	return receipt, nil
}

func validateFileReceipt(receipt SourceFileReceipt, selected ManifestFile) error {
	if receipt.Path != selected.Path || receipt.ResourceType != resourceType(selected.Path) || receipt.State != "READY" || receipt.Rows < 0 || len(receipt.SHA256) != 64 {
		return &Error{Code: "INVALID_FILE_RECEIPT", Message: "Loom source file receipt does not match the selected file"}
	}
	if _, err := hex.DecodeString(receipt.SHA256); err != nil {
		return &Error{Code: "INVALID_FILE_RECEIPT", Message: "Loom source file receipt digest is invalid"}
	}
	if selected.SHA256 != "" && !strings.EqualFold(receipt.SHA256, selected.SHA256) {
		return &Error{Code: "INVALID_FILE_RECEIPT", Message: "Loom source file receipt digest differs from the selected manifest"}
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

func decodeReceipt(body []byte, operationID string) (Receipt, error) {
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
	if receipt.OperationID != operationID || receipt.SourceRevision <= 0 || receipt.State != "PUBLISHED" || strings.TrimSpace(receipt.ExecutionID) == "" || len(receipt.ManifestHash) != 64 {
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
			Code       string          `json:"code"`
			Message    string          `json:"message"`
			RequestID  string          `json:"requestId"`
			RequestID2 string          `json:"request_id"`
			Retryable  bool            `json:"retryable"`
			Issues     []Issue         `json:"issues"`
			Details    json.RawMessage `json:"details"`
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
	requestID := payload.Error.RequestID
	if requestID == "" {
		requestID = payload.Error.RequestID2
	}
	issues := payload.Error.Issues
	var details struct {
		Issues       []Issue `json:"issues"`
		TotalErrors  int64   `json:"totalErrors"`
		TotalErrors2 int64   `json:"total_errors"`
		Truncated    bool    `json:"truncated"`
	}
	_ = json.Unmarshal(payload.Error.Details, &details)
	if len(issues) == 0 {
		issues = details.Issues
	}
	totalErrors := details.TotalErrors
	if totalErrors == 0 {
		totalErrors = details.TotalErrors2
	}
	truncated := details.Truncated
	retryable := status != http.StatusUnprocessableEntity && (status >= 500 || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || payload.Error.Retryable)
	if code == "PUBLICATION_CONFLICT" || code == "PUBLICATION_LEASE_LOST" {
		retryable = false
	}
	for index := range issues {
		issues[index].File = redactToken(issues[index].File, token)
		issues[index].Filename = redactToken(issues[index].Filename, token)
		issues[index].ResourceType = redactToken(issues[index].ResourceType, token)
		issues[index].Category = redactToken(issues[index].Category, token)
		issues[index].Message = redactToken(issues[index].Message, token)
		issues[index].Code = redactToken(issues[index].Code, token)
	}
	return &Error{
		StatusCode: status, Code: redactToken(code, token), Message: redactToken(message, token),
		RequestID: redactToken(requestID, token), Issues: issues, TotalErrors: totalErrors,
		Truncated: truncated, Retryable: retryable,
	}
}

func redactToken(value, token string) string {
	if token == "" || value == "" {
		return value
	}
	return strings.ReplaceAll(value, token, "[redacted]")
}

type retryState struct {
	ordinaryFailures int
	busyStarted      time.Time
}

func (state *retryState) wait(ctx context.Context, err error, busyInterval, busyBudget time.Duration) (bool, error) {
	var structured *Error
	if !errors.As(err, &structured) {
		return false, nil
	}
	if structured.Code == "PUBLICATION_IN_PROGRESS" && structured.Retryable {
		if busyInterval <= 0 || busyBudget <= 0 {
			return false, nil
		}
		now := time.Now()
		if state.busyStarted.IsZero() {
			state.busyStarted = now
		}
		if now.Sub(state.busyStarted)+busyInterval > busyBudget {
			return false, nil
		}
		if err := waitForRetry(ctx, busyInterval); err != nil {
			return false, err
		}
		return true, nil
	}
	state.ordinaryFailures++
	if !structured.Retryable || state.ordinaryFailures >= maxAttempts {
		return false, nil
	}
	if err := waitForRetry(ctx, retryDelay(state.ordinaryFailures)); err != nil {
		return false, err
	}
	return true, nil
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
