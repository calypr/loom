package construction

import (
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

func validateAuthorizedReadScope(scope authscope.ReadScope, expectedDigest string) error {
	return support.ValidateAuthorizedReadScope(scope, expectedDigest)
}

func requireExactIdentity(value, name string) error {
	return support.RequireExactIdentity(value, name)
}

func applyAuthorizedScope(bindings *recipe.RuntimeBindings, authorized ports.AuthorizedCapability, includeAuthResourcePath bool) {
	support.ApplyAuthorizedScope(bindings, authorized, includeAuthResourcePath)
}
