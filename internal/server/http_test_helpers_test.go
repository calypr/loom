package server

import (
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/calypr/loom/internal/testkit/httpfixtures"
	"github.com/calypr/loom/internal/testkit/recipetest"
)

type compilerTestRegistry = recipetest.Registry

type testCapabilityStore = capabilityfixtures.Store

func newTestCapabilityStore() *testCapabilityStore {
	return capabilityfixtures.NewStore()
}

type testHTTPResponse = httpfixtures.Response

var requestJSON = httpfixtures.RequestJSON

type testExplorerStore = explorerfixtures.Store

func newTestExplorerStore() *testExplorerStore {
	return explorerfixtures.NewStore()
}

func testAuthoringV2CapabilitySnapshot() capability.Snapshot {
	return capabilityfixtures.AuthoringV2CapabilitySnapshot(capabilityresolver.ScopeDigest(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}))
}
