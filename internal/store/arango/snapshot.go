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
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
)

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
