package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Authoring-plane publication primitives for the ruling setup conditions,
// wired into the live setup path by RunBenchmark (review r4561644037): per
// workload and per arm it calls SetupProofEvidence after authoring and
// gates all timing behind MatchedSetupProof, so the authoring publish, the
// materialization-identity verification, the active-state readback, and the
// pre-timing matched-state proof execute on every timed lane, not only in
// unit tests. They speak only public routes. No arm may reach the
// repository activation posture through them, and activation-style refusals
// are fatal wherever they appear: the benchmark never tolerates, retries,
// or special-cases one.

// ErrActivationRefusal marks an activation-style refusal answered by the
// server (typed 409/503 stances whose message is that a revision was
// retained, or that activation itself failed). The verifier contract is
// fail-closed: such a refusal aborts the run with this error wrapped, so a
// green report can never sit on a tolerated activation gap.
var ErrActivationRefusal = errors.New("benchmark: activation-style refusal (fail-closed, never tolerated)")

// activationRefusalMarkers are the server's own typed codes for
// activation-stage refusals; matching is on codes and the structured
// adapter posture, never on message prose (coordinator ruling 2026-11-13).
// Codes are matched bare so JSON formatting drift cannot hide a refusal
// from the fail-closed posture. PUBLICATION_ACTIVATION_CONFLICT and
// RELEASE_ACTIVATION_FAILED are deterministic contract failures: losing
// them means the harness setup violated a contract, so they are fatal, not
// retried.
var activationRefusalMarkers = []string{
	"GENERATION_ACTIVATION_FAILED",
	"PUBLICATION_ACTIVATION_CONFLICT",
	"RELEASE_ACTIVATION_FAILED",
	"MATERIALIZATION_ACTIVATION_FAILED",
}

// adapterRefusalMarkers together identify the typed named refusal the
// PostgreSQL runtime answers for capabilities with no adapter (postgreSQL
// RuntimeUnavailable: CodeBackendUnavailable + details stage=adapter).
// Both must appear: the code alone could in principle ride an unrelated
// envelope, and the stage detail alone is not an identity.
var adapterRefusalMarkers = []string{
	"BACKEND_UNAVAILABLE",
	`"stage"`,
	`"adapter"`,
}

func activationRefusalIn(body string) bool {
	for _, marker := range activationRefusalMarkers {
		if strings.Contains(body, marker) {
			return true
		}
	}
	for _, marker := range adapterRefusalMarkers {
		if !strings.Contains(body, marker) {
			return false
		}
	}
	return true
}

// PublicationOutput is one materialized output as the authoring publish
// route answers it.
type PublicationOutput struct {
	OutputID          string `json:"outputId"`
	State             string `json:"state"`
	MaterializationID string `json:"materializationId"`
}

// AuthoringPublication is the verified response of the authoring publish
// route: identity fields must all be present, and every output must be
// READY with a materialization identity (condition ii: materialization
// evidence is consumed, never decorative).
type AuthoringPublication struct {
	ReceiptID   string              `json:"receiptId"`
	RevisionID  string              `json:"revisionId"`
	State       string              `json:"state"`
	Outputs     []PublicationOutput `json:"outputs"`
	Diagnostics []any               `json:"diagnostics"`
}

// PublishReceipt publishes a reconciled receipt through the authoring
// route (POST explorers/{id}/authoring/v2/publish) on this arm and
// verifies the answer: the receipt identity must round-trip, the revision
// identity must exist, and every output must carry a READY materialization
// identity. Any shortfall is an error before the caller can use the
// receipt for anything.
func (a *Arm) PublishReceipt(ctx context.Context, token, project, explorer, receiptID string) (AuthoringPublication, error) {
	endpoint := a.scoped(a.authoringURL(project, explorer) + "/publish")
	raw, err := json.Marshal(map[string]any{"receiptId": receiptID})
	if err != nil {
		return AuthoringPublication{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return AuthoringPublication{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	a.authed(req, token)
	value, err := doArmRequest(a.Client, req)
	if err != nil {
		return AuthoringPublication{}, fmt.Errorf("benchmark: authoring publish on %s: %w", a.Backend, err)
	}
	encoded, _ := json.Marshal(value)
	var pub AuthoringPublication
	if err := json.Unmarshal(encoded, &pub); err != nil {
		return AuthoringPublication{}, fmt.Errorf("benchmark: authoring publish shape on %s: %w", a.Backend, err)
	}
	if pub.ReceiptID != receiptID {
		return AuthoringPublication{}, fmt.Errorf("benchmark: authoring publish on %s answered receipt %q for reconcile %q", a.Backend, pub.ReceiptID, receiptID)
	}
	if strings.TrimSpace(pub.RevisionID) == "" {
		return AuthoringPublication{}, fmt.Errorf("benchmark: authoring publish on %s omitted the revision identity", a.Backend)
	}
	if strings.TrimSpace(pub.State) == "" {
		return AuthoringPublication{}, fmt.Errorf("benchmark: authoring publish on %s omitted the revision state", a.Backend)
	}
	if len(pub.Outputs) == 0 {
		return AuthoringPublication{}, fmt.Errorf("benchmark: authoring publish on %s answered no materialized outputs", a.Backend)
	}
	for _, out := range pub.Outputs {
		if strings.TrimSpace(out.OutputID) == "" || strings.TrimSpace(out.MaterializationID) == "" {
			return AuthoringPublication{}, fmt.Errorf("benchmark: authoring publish on %s answered output %q without output/materialization identity", a.Backend, out.OutputID)
		}
		if out.State != "READY" {
			return AuthoringPublication{}, fmt.Errorf("benchmark: authoring publish on %s left output %s in state %q, want READY", a.Backend, out.OutputID, out.State)
		}
	}
	return pub, nil
}

// ExplorerActiveState is the matched-state readback: the stable identity
// fields the explorer route answers for a published interactive explorer.
// Timestamps are excluded on purpose; digests are content-derived, so
// cross-arm equality is the setup-equivalence proof.
type ExplorerActiveState struct {
	DraftDigest      string
	ActiveRevisionID string
}

// FetchExplorerActiveState reads the explorer's active runtime projection
// for the matched-state proof.
func (a *Arm) FetchExplorerActiveState(ctx context.Context, token, project, explorerID string) (ExplorerActiveState, error) {
	endpoint := a.scoped(a.BaseURL + "/api/v1/projects/" + url.PathEscape(project) + "/explorers/" + url.PathEscape(explorerID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ExplorerActiveState{}, err
	}
	a.authed(req, token)
	value, err := doArmRequest(a.Client, req)
	if err != nil {
		return ExplorerActiveState{}, fmt.Errorf("benchmark: explorer readback on %s: %w", a.Backend, err)
	}
	raw, _ := json.Marshal(value)
	// The route answers ExplorerStateV1: the draft digest is at
	// draft.digest and the revision identity at active.revisionId.
	var wire struct {
		Draft struct {
			Digest string `json:"digest"`
		} `json:"draft"`
		Active struct {
			RevisionID string `json:"revisionId"`
		} `json:"active"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return ExplorerActiveState{}, fmt.Errorf("benchmark: explorer readback shape on %s: %w", a.Backend, err)
	}
	return ExplorerActiveState{DraftDigest: wire.Draft.Digest, ActiveRevisionID: wire.Active.RevisionID}, nil
}

// SetupProof is everything the pre-timing matched-state proof compares for
// one workload on one arm.
type SetupProof struct {
	DraftDigest      string
	RevisionID       string
	OutputIDs        []string
	Materializations int
	PreviewRows      int
}

// SetupProofEvidence publishes the receipt, reads back the active state,
// and runs one untimed setup preview on this arm. Publish failures
// (including activation-style refusals) abort here; nothing downstream of
// a divergent setup may be timed.
func (a *Arm) SetupProofEvidence(ctx context.Context, token, project, explorerID, receiptID, outputID string) (SetupProof, error) {
	pub, err := a.PublishReceipt(ctx, token, project, explorerID, receiptID)
	if err != nil {
		return SetupProof{}, err
	}
	state, err := a.FetchExplorerActiveState(ctx, token, project, explorerID)
	if err != nil {
		return SetupProof{}, err
	}
	if strings.TrimSpace(state.DraftDigest) == "" {
		return SetupProof{}, fmt.Errorf("benchmark: explorer readback on %s omitted the draft digest", a.Backend)
	}
	preview, err := a.ExecuteOutput(ctx, token, project, explorerID, receiptID, outputID, 1)
	if err != nil {
		return SetupProof{}, fmt.Errorf("benchmark: setup preview on %s: %w", a.Backend, err)
	}
	proof := SetupProof{DraftDigest: state.DraftDigest, RevisionID: state.ActiveRevisionID, PreviewRows: preview.RowCount, Materializations: len(pub.Outputs)}
	for _, out := range pub.Outputs {
		proof.OutputIDs = append(proof.OutputIDs, out.OutputID)
	}
	sort.Strings(proof.OutputIDs)
	return proof, nil
}

// MatchedSetupProof compares the two arms' pre-timing evidence
// (condition i). The draft digest is the content-equivalence proof; the
// output set must match exactly and every arm must carry a materialization
// identity per output; the setup preview rows must agree. Revision ids are
// recorded by the caller but not compared (server-internal identity
// composition may differ across backends).
func MatchedSetupProof(workloadID string, arango, postgres SetupProof) error {
	if arango.DraftDigest == "" || postgres.DraftDigest == "" {
		return fmt.Errorf("benchmark: %s matched-state proof missing draft digest", workloadID)
	}
	if arango.DraftDigest != postgres.DraftDigest {
		return fmt.Errorf("benchmark: %s matched-state proof diverged: draft digest %s != %s", workloadID, arango.DraftDigest, postgres.DraftDigest)
	}
	if len(arango.OutputIDs) != len(postgres.OutputIDs) || arango.Materializations != len(arango.OutputIDs) || postgres.Materializations != len(postgres.OutputIDs) {
		return fmt.Errorf("benchmark: %s matched-state proof output/materialization counts differ: arango %d/%d, postgres %d/%d",
			workloadID, len(arango.OutputIDs), arango.Materializations, len(postgres.OutputIDs), postgres.Materializations)
	}
	for i := range arango.OutputIDs {
		if arango.OutputIDs[i] != postgres.OutputIDs[i] {
			return fmt.Errorf("benchmark: %s matched-state proof output set diverged at %d: %s != %s", workloadID, i, arango.OutputIDs[i], postgres.OutputIDs[i])
		}
	}
	if arango.PreviewRows != postgres.PreviewRows {
		return fmt.Errorf("benchmark: %s matched-state proof setup preview rows differ: arango %d, postgres %d", workloadID, arango.PreviewRows, postgres.PreviewRows)
	}
	return nil
}
