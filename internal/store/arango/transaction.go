package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

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

func (q readSnapshotQueryer) closeCursor(id string) {
	if id != "" {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = q.client.readTransactionRequest(cleanupCtx, http.MethodDelete, "/_api/cursor/"+url.PathEscape(id), q.transactionID, nil, nil)
	}
}

type snapshotCursorPage struct {
	Result  []json.RawMessage `json:"result"`
	HasMore bool              `json:"hasMore"`
	ID      string            `json:"id"`
}
