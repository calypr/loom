package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPublishAuthoringZZNoLeftoverResources(t *testing.T) {
	raw := pubauthTestPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %v", err)
	}
	defer admin.Close()

	var databases int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE left(datname, 15) = 'verify_pubauth_'",
	).Scan(&databases); err != nil {
		t.Fatalf("query database namespace: %v", err)
	}
	if got, want := int64(databases), pubauthDBBaseline.Load()+pubauthLive.Load(); got != want {
		t.Errorf("%d verify_pubauth_* databases remain, baseline+live is %d", got, want)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE left(nspname, 15) = 'verify_pubauth' || '_'",
	).Scan(&schemas); err != nil {
		t.Fatalf("query schema namespace: %v", err)
	}
	if got, want := int64(schemas), pubauthSchemaBaseline.Load(); got != want {
		t.Errorf("%d verify_pubauth_* schemas remain in the service database, baseline is %d", got, want)
	}
}
