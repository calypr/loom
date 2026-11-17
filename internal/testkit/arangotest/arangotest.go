package arangotest

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

const requiredEnv = "LOOM_TEST_REQUIRED"

type Database struct {
	Client *arango.Client
	Name   string
	URL    string
}

func (database *Database) Close(ctx context.Context) error {
	if database == nil || database.Client == nil {
		return nil
	}
	client := database.Client
	database.Client = nil
	return client.Close(ctx)
}

func Required() bool { return strings.TrimSpace(os.Getenv(requiredEnv)) == "1" }

func Owned(t testing.TB, prefix string) *Database {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		missing(t, "LOOM_TEST_ARANGO_URL")
	}
	if strings.TrimSpace(prefix) == "" || strings.TrimSpace(prefix) != prefix {
		t.Fatalf("owned Arango database prefix must be nonblank and trimmed: %q", prefix)
	}
	name := prefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if len(name) > 63 {
		t.Fatalf("owned Arango database name %q exceeds the 63-character limit", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	client, err := arango.Open(ctx, endpoint, name)
	cancel()
	if err != nil {
		t.Fatalf("open owned Arango database %s: %v", name, err)
	}
	owned := &Database{Client: client, Name: name, URL: endpoint}
	t.Logf("ARANGO_TEST_DATABASE_CREATED=%s", name)
	t.Cleanup(func() {
		if err := owned.Close(context.Background()); err != nil {
			t.Errorf("close owned Arango database client %s: %v", name, err)
		}
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		parsed, err := url.Parse(endpoint)
		if err != nil {
			t.Errorf("parse Arango cleanup endpoint: %v", err)
			return
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/_api/database/" + url.PathEscape(name)
		request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, parsed.String(), nil)
		if err != nil {
			t.Errorf("build Arango database cleanup request: %v", err)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Errorf("drop owned Arango database %s: %v", name, err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNotFound {
			t.Errorf("drop owned Arango database %s: HTTP %d", name, response.StatusCode)
			return
		}
		t.Logf("ARANGO_TEST_DATABASE_REMOVED=%s", name)
	})
	return owned
}

func Shared(t testing.TB) *Database {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	name := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_DATABASE"))
	if endpoint == "" && name == "" {
		missing(t, "LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	if endpoint == "" || name == "" {
		t.Fatalf("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must be set together")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := verifyExistingDatabase(ctx, endpoint, name, http.DefaultClient); err != nil {
		t.Fatalf("check shared Arango database %s: %v", name, err)
	}
	client, err := arango.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open shared Arango database %s: %v", name, err)
	}
	shared := &Database{Client: client, Name: name, URL: endpoint}
	t.Cleanup(func() {
		if err := shared.Close(context.Background()); err != nil {
			t.Errorf("close shared Arango database client %s: %v", name, err)
		}
	})
	return shared
}

func verifyExistingDatabase(ctx context.Context, endpoint, name string, client *http.Client) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("invalid Arango URL %q", endpoint)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/_db/" + url.PathEscape(name) + "/_api/database/current"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fmt.Errorf("build shared Arango check: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request existing Arango database %s: %w", name, err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("shared Arango database %s does not exist: HTTP %d", name, response.StatusCode)
	}
	return nil
}

func ClickHouse(t testing.TB) *clickhouse.Client {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_URL"))
	if endpoint == "" {
		missing(t, "LOOM_CLICKHOUSE_URL")
	}
	client, err := clickhouse.New(clickhouse.Options{
		URL:      endpoint,
		Database: strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_DATABASE")),
		Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
		Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatalf("open ClickHouse client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close ClickHouse client: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping ClickHouse: %v", err)
	}
	return client
}

func missing(t testing.TB, names string) {
	t.Helper()
	if Required() {
		t.Fatalf("required integration service configuration is missing: %s", names)
	}
	t.Skipf("set %s to run this integration test", names)
}
