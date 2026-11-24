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
	"slices"
	"strings"
	"sync"
	"time"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/arangodb/go-driver/v2/connection"
	"github.com/arangodb/go-driver/v2/utils"
)

type Client struct {
	db             driver.Database
	rawURL         string
	dbName         string
	client         *http.Client
	previewIndexMu sync.Mutex
}

const previewCursorCleanupTimeout = time.Second

type queryMemoryLimitContextKey struct{}
type queryMaxNumberOfPlansContextKey struct{}

func WithQueryMemoryLimit(ctx context.Context, bytes int64) context.Context {
	return context.WithValue(ctx, queryMemoryLimitContextKey{}, bytes)
}

func WithQueryMaxNumberOfPlans(ctx context.Context, maxPlans int) context.Context {
	return context.WithValue(ctx, queryMaxNumberOfPlansContextKey{}, maxPlans)
}

type RowQueryer interface {
	QueryRows(context.Context, string, int, map[string]interface{}, RowVisitor) error
}

type BatchInserter interface {
	InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error
}

type AQLExecutor interface {
	ExecuteAQL(context.Context, string, map[string]interface{}) error
}

type CollectionBootstrapClient interface {
	RowQueryer
	BatchInserter
	AQLExecutor
	CollectionExists(context.Context, string) (bool, error)
	Bootstrap(context.Context, BootstrapSpec) error
}

type RowBatchClient interface {
	RowQueryer
	BatchInserter
}

type TransactionalRowQueryer interface {
	RowQueryer
	WithTransaction(context.Context, TransactionCollections, TransactionFunc) error
}

type TransactionalRowClient interface {
	RowBatchClient
	WithTransaction(context.Context, TransactionCollections, TransactionFunc) error
}

func IsQueryMemoryLimitExceeded(err error) bool {
	if !IsQueryResourceLimitExceeded(err) {
		return false
	}
	ok, arangoErr := shared.IsArangoError(err)
	return ok && strings.Contains(strings.ToLower(arangoErr.ErrorMessage), "memory")
}

func IsQueryResourceLimitExceeded(err error) bool {
	return shared.IsArangoErrorWithErrorNum(err, shared.ErrResourceLimit)
}

func IsQueryOutOfMemory(err error) bool {
	return shared.IsArangoErrorWithErrorNum(err, shared.ErrOutOfMemory)
}

func IsQueryUserAssertion(err error, code string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	ok, arangoErr := shared.IsArangoError(err)
	return ok && arangoErr.ErrorNum == shared.ErrQueryUserAssert && strings.Contains(arangoErr.ErrorMessage, "AQL: "+code)
}

type TransactionCollections struct {
	Read  []string
	Write []string
}

type TransactionFunc func(context.Context, RowQueryer) error

var bufferPool = sync.Pool{
	New: func() any {
		return &bytes.Buffer{}
	},
}

func Open(ctx context.Context, url, database string) (*Client, error) {
	return openDatabase(ctx, url, database, true)
}

func OpenExisting(ctx context.Context, url, database string) (*Client, error) {
	return openDatabase(ctx, url, database, false)
}

func openDatabase(ctx context.Context, url, database string, createIfMissing bool) (*Client, error) {
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
		if !createIfMissing {
			return nil, fmt.Errorf("refusing to create missing database %q", database)
		}
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
			"indexes":    len(collection.Indexes) + len(collection.PersistentIndexes),
			"position":   i + 1,
			"total":      total,
		})
		if err := ensureIndexes(ctx, col, collection.Indexes); err != nil {
			return err
		}
		if err := ensurePersistentIndexSpecs(ctx, col, collection.PersistentIndexes); err != nil {
			return err
		}
		reportBootstrap(spec, "go_bootstrap_collection_complete", map[string]any{
			"backend":    "arango",
			"collection": collection.Name,
			"indexes":    len(collection.Indexes) + len(collection.PersistentIndexes),
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

func (c *Client) QueryRowsWithMaxRuntime(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, maxRuntime time.Duration, visit RowVisitor) error {
	return queryRowsWithMaxRuntime(ctx, c.db, query, batchSize, bindVars, maxRuntime, visit)
}

func (c *Client) CollectionRevision(ctx context.Context, name string) (string, error) {
	if c == nil || c.db == nil || strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("Arango collection revision requires a client and collection")
	}
	collection, err := c.db.GetCollection(ctx, name, nil)
	if err != nil {
		return "", fmt.Errorf("get Arango collection %q for revision: %w", name, err)
	}
	properties, err := collection.Revision(ctx)
	if err != nil {
		return "", fmt.Errorf("read Arango collection %q revision: %w", name, err)
	}
	if properties.Revision == "" {
		return "", fmt.Errorf("Arango collection %q returned an empty revision", name)
	}
	return properties.Revision, nil
}

func (c *Client) CollectionCount(ctx context.Context, name string) (int64, error) {
	if c == nil || c.db == nil || strings.TrimSpace(name) == "" {
		return 0, fmt.Errorf("Arango collection count requires a client and collection")
	}
	collection, err := c.db.GetCollection(ctx, name, nil)
	if err != nil {
		return 0, fmt.Errorf("get Arango collection %q for count: %w", name, err)
	}
	count, err := collection.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("read Arango collection %q count: %w", name, err)
	}
	if count < 0 {
		return 0, fmt.Errorf("Arango collection %q returned a negative count", name)
	}
	return count, nil
}

func queryRowsWithMaxRuntime(ctx context.Context, queryer driver.DatabaseQuery, query string, batchSize int, bindVars map[string]interface{}, maxRuntime time.Duration, visit RowVisitor) error {
	if maxRuntime <= 0 {
		return fmt.Errorf("arango query max runtime must be positive")
	}

	maxPlans := 1
	options := &driver.QueryOptions{
		BatchSize: batchSize,
		BindVars:  bindVars,
		Options: driver.QuerySubOptions{
			MaxRuntime:       maxRuntime.Seconds(),
			MaxNumberOfPlans: &maxPlans,
		},
	}
	if memoryLimit, ok := ctx.Value(queryMemoryLimitContextKey{}).(int64); ok {
		if memoryLimit <= 0 {
			return fmt.Errorf("arango query memory limit must be positive")
		}
		options.MemoryLimit = memoryLimit
		if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
				return context.DeadlineExceeded
			}
			if remaining < maxRuntime {
				options.Options.MaxRuntime = remaining.Seconds()
			}
		}
	}
	return queryRowsWithOptions(ctx, queryer, query, options, visit, previewCursorCleanupTimeout)
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
	options := &driver.QueryOptions{BatchSize: batchSize, BindVars: bindVars}
	return queryRowsWithOptions(ctx, queryer, query, options, visit, 0)
}

func queryRowsWithOptions(ctx context.Context, queryer driver.DatabaseQuery, query string, options *driver.QueryOptions, visit RowVisitor, closeTimeout time.Duration) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	cursor, err := queryer.Query(ctx, query, options)
	if err != nil {
		return fmt.Errorf("arango query: %w", err)
	}
	defer func() {
		var closeErr error
		if closeTimeout > 0 {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
			defer cancel()
			closeErr = cursor.CloseWithContext(closeCtx)
		} else {
			closeErr = cursor.Close()
		}
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

type persistentIndexEnsurer interface {
	EnsurePersistentIndex(context.Context, []string, *driver.CreatePersistentIndexOptions) (driver.IndexResponse, bool, error)
}

func ensurePersistentIndexSpecs(ctx context.Context, col persistentIndexEnsurer, indexes []PersistentIndexSpec) error {
	for _, index := range indexes {
		created, _, err := col.EnsurePersistentIndex(ctx, index.Fields, &driver.CreatePersistentIndexOptions{
			StoredValues: append([]string(nil), index.StoredValues...),
			Unique:       utils.NewType(false),
			Sparse:       utils.NewType(index.Sparse),
		})
		if err != nil {
			return err
		}
		if created.Type != driver.IndexType("persistent") || created.RegularIndex == nil ||
			created.Unique == nil || *created.Unique || created.Sparse == nil || *created.Sparse != index.Sparse ||
			!slices.Equal(created.RegularIndex.Fields, index.Fields) ||
			!slices.Equal(created.RegularIndex.StoredValues, index.StoredValues) {
			return fmt.Errorf("Arango persistent index differs from bootstrap definition")
		}
	}
	return nil
}

type rawInsertObserverKey struct{}

func WithRawInsertObserver(ctx context.Context, observe func(int64, time.Duration)) context.Context {
	return context.WithValue(ctx, rawInsertObserverKey{}, observe)
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

	if observe, ok := ctx.Value(rawInsertObserverKey{}).(func(int64, time.Duration)); ok {
		start := time.Now()
		defer func() { observe(req.ContentLength, time.Since(start)) }()
	}
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

	if observe, ok := ctx.Value(rawInsertObserverKey{}).(func(int64, time.Duration)); ok {
		start := time.Now()
		defer func() { observe(req.ContentLength, time.Since(start)) }()
	}
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
