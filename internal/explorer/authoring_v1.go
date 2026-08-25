package explorer

// This file is the versioned Explorer authoring wire contract.  It is kept in
// the Explorer domain package so HTTP, ETL, and repository bootstrap callers
// share one decoder and one canonical digest implementation.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	ExplorerAuthoringV1APIVersion = "loom.calypr.org/explorer-authoring/v1"
	ExplorerAuthoringV1Kind       = "ExplorerAuthoringBundle"
	ExplorerBuilderV1Kind         = "ExplorerBuilderDocument"
)

// ExplorerAuthoringBundleV1 is the portable authoring artifact.  Intent is
// the only durable browser-facing representation.  The compiled recipe is
// held by CompilationReceipt and never appears in this document.
type ExplorerAuthoringBundleV1 struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Project    string `json:"project"`
	ExplorerID string `json:"explorerId"`
	Title      string `json:"title,omitempty"`
	// Document is accepted as a compatibility input for the original V1
	// single-output packet. Canonical output always uses Documents so one
	// Explorer can retain all of its output-backed tabs.
	Document     ExplorerBuilderDocumentV1   `json:"document"`
	Documents    []ExplorerBuilderDocumentV1 `json:"documents,omitempty"`
	Tabs         []ExplorerTabV1             `json:"tabs,omitempty"`
	IntentDigest string                      `json:"intentDigest,omitempty"`
}

// MarshalJSON emits only the canonical plural Builder shape. Document remains
// a decode-only compatibility field so legacy packets can be migrated without
// leaking the singular form back into current Builder responses.
func (b ExplorerAuthoringBundleV1) MarshalJSON() ([]byte, error) {
	documents := b.AuthoringDocuments()
	if documents == nil {
		documents = []ExplorerBuilderDocumentV1{}
	}
	tabs := b.Tabs
	if tabs == nil {
		tabs = []ExplorerTabV1{}
	}
	return json.Marshal(struct {
		APIVersion   string                      `json:"apiVersion"`
		Kind         string                      `json:"kind"`
		Project      string                      `json:"project"`
		ExplorerID   string                      `json:"explorerId"`
		Title        string                      `json:"title,omitempty"`
		Documents    []ExplorerBuilderDocumentV1 `json:"documents"`
		Tabs         []ExplorerTabV1             `json:"tabs"`
		IntentDigest string                      `json:"intentDigest,omitempty"`
	}{b.APIVersion, b.Kind, b.Project, b.ExplorerID, b.Title, documents, tabs, b.IntentDigest})
}

// ExplorerBuilderDocumentV1 contains authoring intent only.  In particular,
// it has no recipe expression, select, alias, generated public column, or
// physical/storage identity.
type ExplorerBuilderDocumentV1 struct {
	Kind                 string                                   `json:"kind"`
	Output               ExplorerOutputIdentityV1                 `json:"output"`
	BaseNodeID           string                                   `json:"baseNodeId"`
	RowNodeID            string                                   `json:"rowNodeId"`
	RouteEdgeIDs         []string                                 `json:"routeEdgeIds,omitempty"`
	RouteOccurrences     []ExplorerRouteOccurrenceV1              `json:"routeOccurrences,omitempty"`
	CandidateIDs         []string                                 `json:"candidateIds,omitempty"`
	CandidateOccurrences []ExplorerCandidateOccurrenceV1          `json:"candidateOccurrences,omitempty"`
	Presentation         map[string]ExplorerPresentationBindingV1 `json:"presentation,omitempty"`
}

type ExplorerOutputIdentityV1 struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// ExplorerTabV1 describes the visible tab/view layer without exposing recipe
// fields or generated columns. Output-backed tabs may be ordered independently
// of the recipe output order, and outputs not referenced by a tab remain valid
// materialization outputs without becoming visible tabs.
type ExplorerTabV1 struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	OutputID string `json:"outputId"`
	Order    int    `json:"order"`
	Visible  *bool  `json:"visible,omitempty"`
}

// Route occurrences identify a node occurrence in a route, rather than only
// a resource type.  That distinction is required when a route repeats a
// resource type.
type ExplorerRouteOccurrenceV1 struct {
	ID             string `json:"id"`
	Index          int    `json:"index"`
	NodeID         string `json:"nodeId"`
	IncomingEdgeID string `json:"incomingEdgeId,omitempty"`
}

// Candidate occurrences make candidate resolution unambiguous for repeated
// resource types and for candidates that are valid at more than one node.
type ExplorerCandidateOccurrenceV1 struct {
	CandidateID  string `json:"candidateId"`
	OccurrenceID string `json:"occurrenceId"`
	// ProjectionMode is populated by the V2 adapter. Legacy stored V1
	// documents omit it and use the compiler-proven candidate default.
	ProjectionMode string `json:"projectionMode,omitempty"`
}

// Presentation is keyed by the server-owned emission ID.  No presentation
// object can name a recipe field or generated public column.
type ExplorerPresentationBindingV1 struct {
	Label   string                   `json:"label,omitempty"`
	Visible *bool                    `json:"visible,omitempty"`
	Order   *int                     `json:"order,omitempty"`
	Table   *ExplorerTableBindingV1  `json:"table,omitempty"`
	Filter  *ExplorerFilterBindingV1 `json:"filter,omitempty"`
	Chart   *ExplorerChartBindingV1  `json:"chart,omitempty"`
}

type ExplorerTableBindingV1 struct {
	Pinned bool `json:"pinned,omitempty"`
}

type ExplorerFilterBindingV1 struct {
	Label string `json:"label,omitempty"`
}

type ExplorerChartBindingV1 struct {
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
}

type AuthoringDiagnostic struct {
	Severity  string         `json:"severity"`
	Stage     string         `json:"stage"`
	Code      string         `json:"code"`
	JSONPath  string         `json:"jsonPath"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"requestId,omitempty"`
}

type AuthoringError struct {
	Status     int
	Diagnostic AuthoringDiagnostic
	Cause      error
}

func (e *AuthoringError) Error() string {
	if e == nil {
		return "Explorer authoring error"
	}
	return e.Diagnostic.Code + ": " + e.Diagnostic.Message
}
func (e *AuthoringError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func malformedAuthoring(stage, path, code, message string, cause error) error {
	return &AuthoringError{Status: 400, Cause: cause, Diagnostic: AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, JSONPath: path, Message: message}}
}
func semanticAuthoring(stage, path, code, message string, details map[string]any) error {
	return &AuthoringError{Status: 422, Diagnostic: AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, JSONPath: path, Message: message, Details: details}}
}
func conflictAuthoring(stage, code, message string, details map[string]any) error {
	return &AuthoringError{Status: 409, Diagnostic: AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, Message: message, Details: details}}
}

// DecodeAuthoringBundleV1 rejects unknown fields, duplicate fields, trailing
// JSON, and all recipe-shaped fields.  It is intentionally separate from the
// recipe decoder: accepting a recipe here would recreate the old browser
// compiler protocol.
func DecodeAuthoringBundleV1(raw []byte) (ExplorerAuthoringBundleV1, error) {
	return decodeAuthoringBundleV1(raw, false)
}

// DecodeAuthoringBundleV1ForMigration accepts the same strict wire shape as
// DecodeAuthoringBundleV1, but defers intentDigest verification until the
// server has resolved legacy intent against the current catalog. Stored
// bundles from before candidateOccurrences was canonical may have a digest
// for that legacy shape and must still be repairable by the authoring server.
func DecodeAuthoringBundleV1ForMigration(raw []byte) (ExplorerAuthoringBundleV1, error) {
	return decodeAuthoringBundleV1(raw, true)
}

func decodeAuthoringBundleV1(raw []byte, ignoreIntentDigest bool) (ExplorerAuthoringBundleV1, error) {
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return ExplorerAuthoringBundleV1{}, malformedAuthoring("decode", "$", "MALFORMED_AUTHORING_REQUEST", err.Error(), err)
	}
	var bundle ExplorerAuthoringBundleV1
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&bundle); err != nil {
		return ExplorerAuthoringBundleV1{}, malformedAuthoring("decode", "$", "MALFORMED_AUTHORING_REQUEST", err.Error(), err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return ExplorerAuthoringBundleV1{}, malformedAuthoring("decode", "$", "MALFORMED_AUTHORING_REQUEST", err.Error(), err)
	}
	if ignoreIntentDigest {
		bundle.IntentDigest = ""
	}
	validationErr := bundle.Validate()
	if validationErr != nil {
		return ExplorerAuthoringBundleV1{}, validationErr
	}
	return bundle, nil
}

// RejectDuplicateJSONKeys is shared by the versioned HTTP envelope decoder so
// every authoring entry point has the same strict wire behavior.
func RejectDuplicateJSONKeys(raw []byte) error { return rejectDuplicateJSONKeys(raw) }

func (b ExplorerAuthoringBundleV1) Validate() error {
	if b.APIVersion != ExplorerAuthoringV1APIVersion || b.Kind != ExplorerAuthoringV1Kind {
		return malformedAuthoring("protocol", "$.apiVersion", "UNSUPPORTED_AUTHORING_PROTOCOL", "unsupported Explorer authoring protocol or kind", nil)
	}
	if strings.TrimSpace(b.Project) == "" || strings.TrimSpace(b.ExplorerID) == "" {
		return malformedAuthoring("protocol", "$.project", "MISSING_AUTHORING_IDENTITY", "project and explorerId are required", nil)
	}
	documents := b.AuthoringDocuments()
	// An empty document list is the canonical unpublished Explorer model.
	// Preview/publish reject it as non-executable, while Builder reads and
	// canonical local editing may carry it safely.
	if len(b.Documents) > 0 && b.Document.Kind != "" {
		return malformedAuthoring("protocol", "$.document", "DUPLICATE_AUTHORING_DOCUMENTS", "use documents or document, not both", nil)
	}
	seenOutputs := map[string]bool{}
	for i, document := range documents {
		if err := document.validateAt(fmt.Sprintf("$.documents[%d]", i)); err != nil {
			return err
		}
		if seenOutputs[document.Output.ID] {
			return semanticAuthoring("intent", fmt.Sprintf("$.documents[%d].output.id", i), "DUPLICATE_OUTPUT_ID", "output IDs must be unique within an Explorer", nil)
		}
		seenOutputs[document.Output.ID] = true
	}
	seenTabs := map[string]bool{}
	seenOrders := map[int]bool{}
	for i, tab := range b.Tabs {
		path := fmt.Sprintf("$.tabs[%d]", i)
		if !idPattern.MatchString(tab.ID) || strings.TrimSpace(tab.Title) == "" || !idPattern.MatchString(tab.OutputID) {
			return malformedAuthoring("protocol", path, "INVALID_EXPLORER_TAB", "tab id, title, and outputId are required and must be stable identifiers", nil)
		}
		if seenTabs[tab.ID] {
			return semanticAuthoring("intent", path+".id", "DUPLICATE_TAB_ID", "tab IDs must be unique", nil)
		}
		if seenOrders[tab.Order] {
			return semanticAuthoring("intent", path+".order", "DUPLICATE_TAB_ORDER", "tab order values must be unique", nil)
		}
		if !seenOutputs[tab.OutputID] {
			return semanticAuthoring("intent", path+".outputId", "TAB_OUTPUT_MISSING", "tab must reference an output document in the same bundle", nil)
		}
		seenTabs[tab.ID], seenOrders[tab.Order] = true, true
	}
	if b.IntentDigest != "" {
		digest, err := b.DocumentDigest()
		if err != nil {
			return err
		}
		if b.IntentDigest != digest {
			return conflictAuthoring("digest", "INTENT_DIGEST_MISMATCH", "intentDigest does not match the canonical document", map[string]any{"expected": digest, "received": b.IntentDigest})
		}
	}
	return nil
}

func (d ExplorerBuilderDocumentV1) Validate() error {
	return d.validateAt("$.document")
}

func (d ExplorerBuilderDocumentV1) validateAt(path string) error {
	if d.Kind != ExplorerBuilderV1Kind {
		return malformedAuthoring("protocol", path+".kind", "UNSUPPORTED_DOCUMENT_VERSION", "unsupported builder document kind", nil)
	}
	if strings.TrimSpace(d.Output.ID) == "" {
		return malformedAuthoring("protocol", path+".output.id", "MISSING_OUTPUT_ID", "output.id is required", nil)
	}
	if strings.TrimSpace(d.BaseNodeID) == "" || strings.TrimSpace(d.RowNodeID) == "" {
		return malformedAuthoring("protocol", path+".baseNodeId", "MISSING_ROUTE_NODE", "baseNodeId and rowNodeId are required", nil)
	}
	for i, edge := range d.RouteEdgeIDs {
		if strings.TrimSpace(edge) == "" {
			return malformedAuthoring("protocol", fmt.Sprintf("%s.routeEdgeIds[%d]", path, i), "EMPTY_ROUTE_EDGE_ID", "route edge IDs must not be empty", nil)
		}
	}
	occurrences := map[string]bool{}
	for i, occurrence := range d.RouteOccurrences {
		if strings.TrimSpace(occurrence.ID) == "" || strings.TrimSpace(occurrence.NodeID) == "" {
			return malformedAuthoring("protocol", fmt.Sprintf("%s.routeOccurrences[%d]", path, i), "INVALID_ROUTE_OCCURRENCE", "route occurrence id and nodeId are required", nil)
		}
		if occurrences[occurrence.ID] {
			return semanticAuthoring("intent", fmt.Sprintf("%s.routeOccurrences[%d].id", path, i), "DUPLICATE_ROUTE_OCCURRENCE", "route occurrence IDs must be unique", nil)
		}
		occurrences[occurrence.ID] = true
	}
	candidates := map[string]bool{}
	for i, candidate := range d.CandidateIDs {
		if strings.TrimSpace(candidate) == "" {
			return malformedAuthoring("protocol", fmt.Sprintf("%s.candidateIds[%d]", path, i), "EMPTY_CANDIDATE_ID", "candidate IDs must not be empty", nil)
		}
		if candidates[candidate] {
			return semanticAuthoring("intent", fmt.Sprintf("%s.candidateIds[%d]", path, i), "DUPLICATE_CANDIDATE_ID", "candidate IDs must be unique", nil)
		}
		candidates[candidate] = true
	}
	for i, candidate := range d.CandidateOccurrences {
		if !candidates[candidate.CandidateID] {
			return semanticAuthoring("intent", fmt.Sprintf("%s.candidateOccurrences[%d].candidateId", path, i), "CANDIDATE_REFERENCE_MISSING", "candidate occurrence must reference candidateIds", nil)
		}
		if !occurrences[candidate.OccurrenceID] && candidate.OccurrenceID != "base" {
			return semanticAuthoring("intent", fmt.Sprintf("%s.candidateOccurrences[%d].occurrenceId", path, i), "STALE_ROUTE_OCCURRENCE", "candidate occurrence references an unknown route occurrence", nil)
		}
	}
	for emissionID := range d.Presentation {
		if strings.TrimSpace(emissionID) == "" {
			return malformedAuthoring("protocol", path+".presentation", "EMPTY_EMISSION_ID", "presentation keys must be emission IDs", nil)
		}
	}
	return nil
}

func (b ExplorerAuthoringBundleV1) CanonicalDocumentJSON() ([]byte, error) {
	validationCopy := b
	validationCopy.IntentDigest = ""
	if err := validationCopy.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Documents []ExplorerBuilderDocumentV1 `json:"documents"`
		Tabs      []ExplorerTabV1             `json:"tabs,omitempty"`
	}{Documents: b.AuthoringDocuments(), Tabs: b.Tabs})
}
func (b ExplorerAuthoringBundleV1) DocumentDigest() (string, error) {
	raw, err := b.CanonicalDocumentJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
func (b ExplorerAuthoringBundleV1) CanonicalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	digest, err := b.DocumentDigest()
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		APIVersion   string                      `json:"apiVersion"`
		Kind         string                      `json:"kind"`
		Project      string                      `json:"project"`
		ExplorerID   string                      `json:"explorerId"`
		Title        string                      `json:"title,omitempty"`
		Documents    []ExplorerBuilderDocumentV1 `json:"documents"`
		Tabs         []ExplorerTabV1             `json:"tabs,omitempty"`
		IntentDigest string                      `json:"intentDigest"`
	}{b.APIVersion, b.Kind, b.Project, b.ExplorerID, b.Title, b.AuthoringDocuments(), b.Tabs, digest})
}

// AuthoringDocuments returns the normalized multi-output view of the bundle.
// The singular Document field is retained only so older V1 callers can be
// read and recompiled while all canonical artifacts use Documents.
func (b ExplorerAuthoringBundleV1) AuthoringDocuments() []ExplorerBuilderDocumentV1 {
	if len(b.Documents) > 0 {
		return append([]ExplorerBuilderDocumentV1(nil), b.Documents...)
	}
	if b.Document.Kind != "" || b.Document.Output.ID != "" || b.Document.BaseNodeID != "" {
		return []ExplorerBuilderDocumentV1{b.Document}
	}
	return nil
}

// AuthoringTabs returns explicit tab order when supplied. A single legacy V1
// document keeps the historical default view; a multi-output bundle defaults
// to one tab per output when the caller omits tabs.
func (b ExplorerAuthoringBundleV1) AuthoringTabs() []ExplorerTabV1 {
	if len(b.Tabs) > 0 {
		return append([]ExplorerTabV1(nil), b.Tabs...)
	}
	documents := b.AuthoringDocuments()
	tabs := make([]ExplorerTabV1, 0, len(documents))
	for i, document := range documents {
		id := document.Output.ID
		if len(documents) == 1 {
			id = "default"
		}
		title := document.Output.Title
		if strings.TrimSpace(title) == "" {
			title = document.Output.ID
		}
		tabs = append(tabs, ExplorerTabV1{ID: id, Title: title, OutputID: document.Output.ID, Order: i})
	}
	return tabs
}

func rejectDuplicateJSONKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var walk func(string) error
	walk = func(path string) error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for dec.More() {
					keyToken, err := dec.Token()
					if err != nil {
						return err
					}
					key := keyToken.(string)
					if seen[key] {
						return fmt.Errorf("duplicate JSON object key %q at %s", key, path)
					}
					seen[key] = true
					if err := walk(path + "." + key); err != nil {
						return err
					}
				}
				_, err = dec.Token()
				return err
			case '[':
				i := 0
				for dec.More() {
					if err := walk(fmt.Sprintf("%s[%d]", path, i)); err != nil {
						return err
					}
					i++
				}
				_, err = dec.Token()
				return err
			}
		}
		return nil
	}
	if err := walk("$"); err != nil {
		return err
	}
	return nil
}

// PresentationEmissionIDs returns sorted keys for deterministic diagnostics
// and test assertions without changing the wire representation.
func (d ExplorerBuilderDocumentV1) PresentationEmissionIDs() []string {
	ids := make([]string, 0, len(d.Presentation))
	for id := range d.Presentation {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
