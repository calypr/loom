package proposal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestColumnPolicyWireRejectsCandidateInjectionAndInvalidLimit(t *testing.T) {
	base := `{"snapshotToken":"snapshot","expectedDraftVersion":1,"expectedDraftDigest":"digest","outputId":"out","columnId":"column","projectionMode":"ALL"}`
	for _, extra := range []string{`,"project":"injected"`, `,"candidateDocument":{}`, `,"candidateNetwork":{}`, `,"candidateWorkspace":{}`, `,"source":{"kind":"FIELD"}`, `,"limit":null`, `,"limit":0`, `,"limit":-1`} {
		var request ColumnPolicyProposalRequest
		if err := json.Unmarshal([]byte(strings.TrimSuffix(base, "}")+extra+"}"), &request); err == nil {
			t.Fatalf("accepted injection %s", extra)
		}
	}
	for _, extra := range []string{`,"column":"other"`, `,"columnId":"other"`, `,"projectionMode":"FIRST"`, `,"source":{}`, `,"title":"rename"`} {
		raw := `{"type":"APPLY_COLUMN_POLICY_PROPOSAL","outputId":"out","proposalId":"receipt"` + extra + `}`
		var command authoringv2.Command
		if err := json.Unmarshal([]byte(raw), &command); err == nil {
			t.Fatalf("Apply accepted non-receipt facts: %s", raw)
		}
	}
}
