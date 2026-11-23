package output

import (
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"
)

func testAuthoringV2CapabilitySnapshot() capability.Snapshot {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	return capabilityfixtures.AuthoringV2CapabilitySnapshot(capabilityresolver.ScopeDigest(scope))
}
