package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
)

// WithReadSnapshot executes row queries in one read-only ArangoDB stream
// transaction. Declared collections are strict: AQL that touches an
// undeclared collection fails rather than silently weakening snapshot
// isolation.
func (c *Client) WithReadSnapshot(ctx context.Context, collections []string, fn TransactionFunc) error {
	if fn == nil {
		return fmt.Errorf("Arango read snapshot callback is required")
	}
	collections, err := normalizedReadCollections(collections)
	if err != nil {
		return err
	}
	if len(collections) == 0 {
		return fmt.Errorf("Arango read snapshot requires at least one collection")
	}
	var begun struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	body := struct {
		Collections struct {
			Read []string `json:"read"`
		} `json:"collections"`
		AllowImplicit bool `json:"allowImplicit"`
	}{AllowImplicit: false}
	body.Collections.Read = collections
	if err := c.readTransactionRequest(ctx, http.MethodPost, "/_api/transaction/begin", "", body, &begun); err != nil {
		return fmt.Errorf("begin Arango read snapshot: %w", err)
	}
	txID := strings.TrimSpace(begun.Result.ID)
	if txID == "" {
		return fmt.Errorf("begin Arango read snapshot: response omitted transaction ID")
	}
	queryer := readSnapshotQueryer{client: c, transactionID: txID}
	callbackErr := fn(ctx, queryer)
	if callbackErr != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		abortErr := c.readTransactionRequest(cleanupCtx, http.MethodDelete, "/_api/transaction/"+url.PathEscape(txID), txID, nil, nil)
		if abortErr != nil {
			callbackErr = fmt.Errorf("%w; abort Arango read snapshot: %v", callbackErr, abortErr)
		}
		return callbackErr
	}
	if err := ctx.Err(); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		abortErr := c.readTransactionRequest(cleanupCtx, http.MethodDelete, "/_api/transaction/"+url.PathEscape(txID), txID, nil, nil)
		if abortErr != nil {
			return fmt.Errorf("%w; abort Arango read snapshot: %v", err, abortErr)
		}
		return err
	}
	if err := c.readTransactionRequest(ctx, http.MethodPut, "/_api/transaction/"+url.PathEscape(txID), txID, nil, nil); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		abortErr := c.readTransactionRequest(cleanupCtx, http.MethodDelete, "/_api/transaction/"+url.PathEscape(txID), txID, nil, nil)
		if abortErr != nil {
			return fmt.Errorf("commit Arango read snapshot: %w; abort after commit failure: %v", err, abortErr)
		}
		return fmt.Errorf("commit Arango read snapshot: %w", err)
	}
	return nil
}

// ListReadSnapshotCollections returns the current non-system collections that
// can be declared when a query may use dynamic DOCUMENT(handle) lookups. New
// collections created after this inventory is read remain undeclared and are
// rejected by the strict transaction.
func (c *Client) ListReadSnapshotCollections(ctx context.Context) ([]string, error) {
	collections, err := c.db.Collections(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Arango read snapshot collections: %w", err)
	}
	names := make([]string, 0, len(collections))
	for _, collection := range collections {
		name := collection.Name()
		if strings.HasPrefix(name, "_") {
			continue
		}
		names = append(names, name)
	}
	return normalizedReadCollections(names)
}

func normalizedReadCollections(collections []string) ([]string, error) {
	values := make([]string, 0, len(collections))
	seen := make(map[string]struct{}, len(collections))
	for _, collection := range collections {
		collection = strings.TrimSpace(collection)
		if collection == "" {
			return nil, fmt.Errorf("Arango read snapshot collection is empty")
		}
		if _, ok := seen[collection]; ok {
			continue
		}
		seen[collection] = struct{}{}
		values = append(values, collection)
	}
	sort.Strings(values)
	return values, nil
}

type readSnapshotQueryer struct {
	client        *Client
	transactionID string
}

func (q readSnapshotQueryer) QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit RowVisitor) error {
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("Arango snapshot query is required")
	}
	if visit == nil {
		return fmt.Errorf("Arango snapshot row visitor is required")
	}
	if batchSize <= 0 {
		batchSize = 1000
	}
	body := struct {
		Query       string                 `json:"query"`
		BindVars    map[string]interface{} `json:"bindVars,omitempty"`
		BatchSize   int                    `json:"batchSize"`
		MemoryLimit int64                  `json:"memoryLimit,omitempty"`
		Options     struct {
			MaxRuntime       float64 `json:"maxRuntime,omitempty"`
			MaxNumberOfPlans *int    `json:"maxNumberOfPlans,omitempty"`
		} `json:"options,omitempty"`
	}{Query: query, BindVars: bindVars, BatchSize: batchSize}
	if maxPlans, ok := ctx.Value(queryMaxNumberOfPlansContextKey{}).(int); ok && maxPlans > 0 {
		body.Options.MaxNumberOfPlans = &maxPlans
	}
	if memoryLimit, ok := ctx.Value(queryMemoryLimitContextKey{}).(int64); ok && memoryLimit > 0 {
		body.MemoryLimit = memoryLimit
	}
	if deadline, ok := ctx.Deadline(); ok {
		body.Options.MaxRuntime = time.Until(deadline).Seconds()
		if body.Options.MaxRuntime <= 0 {
			return ctx.Err()
		}
	}
	var page snapshotCursorPage
	if err := q.client.readTransactionRequest(ctx, http.MethodPost, "/_api/cursor", q.transactionID, body, &page); err != nil {
		return fmt.Errorf("execute Arango snapshot query: %w", err)
	}
	for {
		for _, raw := range page.Result {
			if err := ctx.Err(); err != nil {
				q.closeCursor(page.ID)
				return err
			}
			var row map[string]interface{}
			if err := json.Unmarshal(raw, &row); err != nil {
				q.closeCursor(page.ID)
				return fmt.Errorf("decode Arango snapshot row: %w", err)
			}
			if err := visit(row); err != nil {
				q.closeCursor(page.ID)
				return err
			}
			if err := ctx.Err(); err != nil {
				q.closeCursor(page.ID)
				return err
			}
		}
		if !page.HasMore {
			return nil
		}
		if page.ID == "" {
			return fmt.Errorf("Arango snapshot cursor has more rows but no cursor ID")
		}
		cursorID := page.ID
		page = snapshotCursorPage{}
		if err := q.client.readTransactionRequest(ctx, http.MethodPut, "/_api/cursor/"+url.PathEscape(cursorID), q.transactionID, nil, &page); err != nil {
			return fmt.Errorf("fetch Arango snapshot cursor: %w", err)
		}
	}
}

// snapshotCursorPage keeps result values raw so arbitrary row schemas can be
// decoded without changing the dataframe's transport types.
type snapshotCursorPage struct {
	Result  []json.RawMessage `json:"result"`
	HasMore bool              `json:"hasMore"`
	ID      string            `json:"id"`
}

func (q readSnapshotQueryer) closeCursor(id string) {
	if id != "" {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = q.client.readTransactionRequest(cleanupCtx, http.MethodDelete, "/_api/cursor/"+url.PathEscape(id), q.transactionID, nil, nil)
	}
}

func (c *Client) readTransactionRequest(ctx context.Context, method, suffix, transactionID string, input, output any) error {
	endpoint := fmt.Sprintf("%s/_db/%s%s", strings.TrimRight(c.rawURL, "/"), url.PathEscape(c.dbName), suffix)
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if transactionID != "" {
		request.Header.Set("x-arango-trx-id", transactionID)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var arangoErr shared.ArangoError
		if json.Unmarshal(responseBody, &arangoErr) == nil && arangoErr.HasError {
			if arangoErr.Code == 0 {
				arangoErr.Code = response.StatusCode
			}
			return arangoErr
		}
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if output != nil && len(responseBody) != 0 {
		if err := json.Unmarshal(responseBody, output); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
