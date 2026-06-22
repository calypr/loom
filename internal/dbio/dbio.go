package dbio

import (
	"context"
	"fmt"
	"strings"

	postgresstore "arangodb-proto/internal/experimental/store/postgres"
	surrealstore "arangodb-proto/internal/experimental/store/surreal"
	"arangodb-proto/internal/store"
	arangostore "arangodb-proto/internal/store/arango"
)

const (
	BackendArango   = "arango"
	BackendPostgres = "postgres"
	BackendSurreal  = "surreal"
)

type ConnectionOptions struct {
	Backend   string
	URL       string
	Namespace string
	Database  string
	Username  string
	Password  string
	AuthToken string
}

func BackendName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return BackendArango
	}
	return name
}

func OpenBackend(ctx context.Context, opts ConnectionOptions) (store.Backend, error) {
	switch BackendName(opts.Backend) {
	case BackendArango:
		return arangostore.Open(ctx, opts.URL, opts.Database)
	case BackendPostgres:
		return postgresstore.Open(ctx, opts.URL, opts.Database, opts.Username, opts.Password)
	case BackendSurreal:
		return surrealstore.Open(ctx, opts.URL, opts.Namespace, opts.Database, opts.Username, opts.Password, opts.AuthToken)
	default:
		return nil, fmt.Errorf("unsupported backend %q", opts.Backend)
	}
}
