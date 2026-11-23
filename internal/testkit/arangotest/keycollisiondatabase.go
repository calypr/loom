package arangotest

import (
	"context"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const KeyCollisionDatabasePrefix = "loom_verify_kcoll_"

func DropKeyCollisionDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	if !strings.HasPrefix(database, KeyCollisionDatabasePrefix) {
		t.Errorf("FB25 refusal to drop %s outside %s", database, KeyCollisionDatabasePrefix)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, endpoint, "_system")
	if err != nil {
		t.Logf("FB25_CLEANUP_WARN: open _system: %v", err)
		return
	}
	defer func() { _ = client.Close(context.Background()) }()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
	if err != nil {
		t.Logf("FB25_CLEANUP_WARN: build drop: %v", err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Logf("FB25_CLEANUP_WARN: drop %s: %v", database, err)
		return
	}
	_ = response.Body.Close()
	if response.StatusCode >= 300 {
		t.Logf("FB25_CLEANUP_WARN: drop %s status %d", database, response.StatusCode)
	}
}
