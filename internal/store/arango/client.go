package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/arangodb/go-driver/v2/connection"
	"github.com/arangodb/go-driver/v2/utils"
)

type Client struct {
	db     driver.Database
	rawURL string
	dbName string
	client *http.Client
}

// Transaction is the narrow query surface used by stores that need to make
// several collection updates visible as one Arango transaction.
type Transaction interface {
	QueryRows(context.Context, string, int, map[string]interface{}, RowVisitor) error
}

type TransactionCollections struct {
	Read  []string
	Write []string
}

type TransactionFunc func(context.Context, Transaction) error

var bufferPool = sync.Pool{
	New: func() any {
		return &bytes.Buffer{}
	},
}

func Open(ctx context.Context, url, database string) (*Client, error) {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	conn := connection.NewHttpConnection(connection.HttpConfiguration{
		Endpoint:  connection.NewRoundRobinEndpoints([]string{url}),
		Transport: transport,
	})
	client := driver.NewClient(conn)
	exists, err := client.DatabaseExists(ctx, database)
	if err != nil {
		return nil, err
	}
	var db driver.Database
	if exists {
		db, err = client.GetDatabase(ctx, database, nil)
	} else {
		db, err = client.CreateDatabase(ctx, database, nil)
	}
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{Transport: transport}
	return &Client{
		db:     db,
		rawURL: url,
		dbName: database,
		client: httpClient,
	}, nil
}

func (c *Client) Bootstrap(ctx context.Context, spec BootstrapSpec) error {
	total := len(spec.Collections)
	for i, collection := range spec.Collections {
		reportBootstrap(spec, "go_bootstrap_collection_start", map[string]any{
			"backend":    "arango",
			"collection": collection.Name,
			"edge":       collection.Edge,
			"truncate":   collection.Truncate,
			"position":   i + 1,
			"total":      total,
		})
		col, err := c.ensureCollection(ctx, collection.Name, collection.Edge)
		if err != nil {
			return err
		}
		reportBootstrap(spec, "go_bootstrap_collection_ready", map[string]any{
			"backend":    "arango",
			"collection": collection.Name,
			"position":   i + 1,
			"total":      total,
		})
		if collection.Truncate {
			reportBootstrap(spec, "go_bootstrap_collection_truncate_start", map[string]any{
				"backend":    "arango",
				"collection": collection.Name,
				"position":   i + 1,
				"total":      total,
			})
			if err := col.Truncate(ctx); err != nil {
				return err
			}
			reportBootstrap(spec, "go_bootstrap_collection_truncate_complete", map[string]any{
				"backend":    "arango",
				"collection": collection.Name,
				"position":   i + 1,
				"total":      total,
			})
		}
		reportBootstrap(spec, "go_bootstrap_collection_indexes_start", map[string]any{
			"backend":    "arango",
			"collection": collection.Name,
			"indexes":    len(collection.Indexes),
			"position":   i + 1,
			"total":      total,
		})
		if err := ensureIndexes(ctx, col, collection.Indexes); err != nil {
			return err
		}
		reportBootstrap(spec, "go_bootstrap_collection_complete", map[string]any{
			"backend":    "arango",
			"collection": collection.Name,
			"indexes":    len(collection.Indexes),
			"position":   i + 1,
			"total":      total,
		})
	}
	return nil
}

func (c *Client) InsertBatchRaw(ctx context.Context, collection string, docs []json.RawMessage, overwrite bool, writeAPI string) error {
	if len(docs) == 0 {
		return nil
	}
	if writeAPI == "" || writeAPI == "import" {
		return c.insertBatchImportRaw(ctx, collection, docs, overwrite)
	}
	if writeAPI != "document" {
		return fmt.Errorf("unsupported write api %q", writeAPI)
	}
	return c.insertBatchDocumentRaw(ctx, collection, docs, overwrite)
}

func (c *Client) QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit RowVisitor) error {
	return queryRows(ctx, c.db, query, batchSize, bindVars, visit)
}

func (c *Client) WithTransaction(ctx context.Context, collections TransactionCollections, fn TransactionFunc) error {
	if fn == nil {
		return fmt.Errorf("Arango transaction callback is required")
	}
	return c.db.WithTransaction(ctx, driver.TransactionCollections{
		Read:  collections.Read,
		Write: collections.Write,
	}, nil, nil, nil, func(txCtx context.Context, tx driver.Transaction) error {
		return fn(txCtx, transactionClient{queryer: tx})
	})
}

type transactionClient struct {
	queryer driver.DatabaseQuery
}

func (t transactionClient) QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit RowVisitor) error {
	return queryRows(ctx, t.queryer, query, batchSize, bindVars, visit)
}

func queryRows(ctx context.Context, queryer driver.DatabaseQuery, query string, batchSize int, bindVars map[string]interface{}, visit RowVisitor) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	cursor, err := queryer.Query(ctx, query, &driver.QueryOptions{BatchSize: batchSize, BindVars: bindVars})
	if err != nil {
		return fmt.Errorf("arango query: %w", err)
	}
	defer func() {
		closeErr := cursor.Close()
		if closeErr != nil {
			wrapped := fmt.Errorf("close arango query cursor: %w", closeErr)
			resultErr = errors.Join(resultErr, wrapped)
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			resultErr = err
			return resultErr
		}
		if !cursor.HasMore() {
			return resultErr
		}
		var row map[string]any
		_, err := cursor.ReadDocument(ctx, &row)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				resultErr = ctxErr
				return resultErr
			}
			if shared.IsNoMoreDocuments(err) {
				return resultErr
			}
			resultErr = fmt.Errorf("read arango query cursor: %w", err)
			return resultErr
		}
		if err := ctx.Err(); err != nil {
			resultErr = err
			return resultErr
		}
		if err := visit(row); err != nil {
			resultErr = err
			return resultErr
		}
	}
}

func (c *Client) CollectionExists(ctx context.Context, name string) (bool, error) {
	return c.db.CollectionExists(ctx, name)
}

// ExecuteAQL runs a write/query statement and drains its cursor. It is kept
// deliberately small so durable registries can perform compare-and-swap
// updates without exposing the Arango driver through their public APIs.
func (c *Client) ExecuteAQL(ctx context.Context, query string, bindVars map[string]interface{}) error {
	cursor, err := c.db.Query(ctx, query, &driver.QueryOptions{BatchSize: 32, BindVars: bindVars})
	if err != nil {
		return err
	}
	defer cursor.Close()
	for cursor.HasMore() {
		var discard any
		if _, err := cursor.ReadDocument(ctx, &discard); err != nil {
			if shared.IsNoMoreDocuments(err) {
				return nil
			}
			return err
		}
	}
	return nil
}

func (c *Client) Close(ctx context.Context) error {
	return nil
}

func reportBootstrap(spec BootstrapSpec, event string, fields map[string]any) {
	if spec.Reporter != nil {
		spec.Reporter(event, fields)
	}
}

func (c *Client) ensureCollection(ctx context.Context, name string, edge bool) (driver.Collection, error) {
	exists, err := c.db.CollectionExists(ctx, name)
	if err != nil {
		return nil, err
	}
	if exists {
		return c.db.GetCollection(ctx, name, nil)
	}
	props := &driver.CreateCollectionPropertiesV2{}
	if edge {
		props.Type = utils.NewType(driver.CollectionTypeEdge)
	}
	return c.db.CreateCollectionV2(ctx, name, props)
}

func ensureIndexes(ctx context.Context, col driver.Collection, indexes [][]string) error {
	for _, fields := range indexes {
		_, _, err := col.EnsurePersistentIndex(ctx, fields, &driver.CreatePersistentIndexOptions{
			Unique: utils.NewType(false),
			Sparse: utils.NewType(false),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) insertBatchDocumentRaw(ctx context.Context, collection string, docs []json.RawMessage, overwrite bool) error {
	var url string
	if overwrite {
		url = fmt.Sprintf("%s/_db/%s/_api/document/%s?silent=true&overwriteMode=replace", c.rawURL, c.dbName, collection)
	} else {
		url = fmt.Sprintf("%s/_db/%s/_api/document/%s?silent=true", c.rawURL, c.dbName, collection)
	}

	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	buf.WriteByte('[')
	for i, doc := range docs {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(doc)
	}
	buf.WriteByte(']')
	bodyBytes := buf.Bytes()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		bufferPool.Put(buf)
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	bufferPool.Put(buf)
	if err != nil {
		return err
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http error %d during raw bulk insert to %s: %s", resp.StatusCode, collection, string(body))
	}
	return nil
}

func (c *Client) insertBatchImportRaw(ctx context.Context, collection string, docs []json.RawMessage, overwrite bool) error {
	url := fmt.Sprintf("%s/_db/%s/_api/import?collection=%s&type=documents&complete=true&details=true", c.rawURL, c.dbName, collection)
	if overwrite {
		url += "&onDuplicate=replace"
	}

	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	for i, doc := range docs {
		if i > 0 {
			buf.WriteByte('\n')
		}
		buf.Write(doc)
	}
	bodyBytes := append([]byte(nil), buf.Bytes()...)
	bufferPool.Put(buf)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("http error %d during import to %s: %s", resp.StatusCode, collection, string(body))
	}

	var result struct {
		Error        bool     `json:"error"`
		ErrorMessage string   `json:"errorMessage"`
		Errors       int      `json:"errors"`
		Details      []string `json:"details"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode import response for %s: %w", collection, err)
	}
	if result.Error || result.Errors > 0 {
		msg := result.ErrorMessage
		if msg == "" && len(result.Details) > 0 {
			msg = strings.Join(result.Details, "; ")
		}
		if msg == "" {
			msg = string(body)
		}
		return fmt.Errorf("import errors for %s: %s", collection, msg)
	}
	return nil
}
