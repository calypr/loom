package ports

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
)

type CapabilityResolver struct {
	Current           func(context.Context, string, string, string) (capability.Snapshot, error)
	Token             func(context.Context, string, string) (capability.Snapshot, error)
	ForCompilation    func(context.Context, string, string) (AuthorizedCapability, error)
	ForExecution      func(context.Context, string, string) (AuthorizedCapability, error)
	Catalog           func(capability.Snapshot, string) ws.CatalogSnapshot
	ValidateReadScope func(authscope.ReadScope, string) error
}

type AuthorizedCapability struct {
	Snapshot capability.Snapshot
	Scope    authscope.ReadScope
}

func (a AuthorizedCapability) Clone() AuthorizedCapability {
	return AuthorizedCapability{Snapshot: a.Snapshot.Clone(), Scope: a.Scope.Clone()}
}
