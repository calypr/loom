package arango

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// WorkspaceAuditReport contains observations only. No audit method can persist
// documents, rewrite digests, resolve a catalog or open a database.
type WorkspaceAuditReport struct {
	Collections                   []string              `json:"collections"`
	AlreadyCanonicalWorkspaces    int                   `json:"alreadyCanonicalWorkspaces"`
	LegacyTableShapeDocuments     int                   `json:"legacyTableShapeDocuments"`
	DiscoveredTableShapeDocuments int                   `json:"discoveredTableShapeDocuments"`
	ByFeature                     map[string]int        `json:"byFeature"`
	Documents                     int                   `json:"documents"`
	KnownPackets                  int                   `json:"knownPackets"`
	NonWorkspacePackets           int                   `json:"nonWorkspacePackets"`
	AuthoritativeWorkspaces       int                   `json:"authoritativeWorkspaces"`
	DiscoveryCandidates           int                   `json:"discoveryCandidates"`
	ByClass                       map[string]int        `json:"byClass"`
	NilConstructionStepLists      int                   `json:"nilConstructionStepLists"`
	DigestRelations               map[string]int        `json:"digestRelations"`
	Entries                       []WorkspaceAuditEntry `json:"entries"`
}

type WorkspaceAuditEntry struct {
	Collection                  string   `json:"collection"`
	Key                         string   `json:"key"`
	Path                        string   `json:"path"`
	Authoritative               bool     `json:"authoritative"`
	Immutable                   bool     `json:"immutable"`
	SnapshotToken               string   `json:"snapshotToken,omitempty"`
	StoredDigest                string   `json:"storedDigest,omitempty"`
	AlreadyCanonical            bool     `json:"alreadyCanonical"`
	Classification              string   `json:"classification"`
	SemanticsVersion            int      `json:"semanticsVersion"`
	Features                    []string `json:"features"`
	NilConstructionStepLists    int      `json:"nilConstructionStepLists"`
	TableShapeDocuments         int      `json:"tableShapeDocuments"`
	NeedsPinnedCatalog          bool     `json:"needsPinnedCatalog"`
	StrictCurrentError          string   `json:"strictCurrentError,omitempty"`
	DecodeError                 string   `json:"decodeError,omitempty"`
	DecodedCanonicalDigest      string   `json:"decodedCanonicalDigest,omitempty"`
	LegacyNilConstructionDigest string   `json:"legacyNilConstructionDigest,omitempty"`
	DigestRelation              string   `json:"digestRelation"`
}

// These are durable workspace-packet positions, not arbitrary JSON field names.
// Config fields can contain presentation ConfigV2 instead; those are counted as
// checked non-workspace packets. Other copies require manual review before use.
var workspaceAuditSlots = map[string]map[string]string{
	ExplorersCollection:               {"draftConfig": "draftDigest"},
	DraftRevisionsCollection:          {"draftConfig": "draftDigest"},
	RevisionsCollection:               {"config": "intentDigest", "authoringBundle": "intentDigest"},
	CompilationReceiptsCollection:     {"normalizedBundle": "intentDigest", "compiledConfig": "intentDigest"},
	LegacyRepositoryConfigsCollection: {"workspace": "intentDigest"},
	workspaceProjectionCollection:     {"Workspace": "Digest"},
}

// ObserveWorkspaceAuditDocument scans one raw database document, retaining
// authoritative packets and separately flagged discoveries in other positions.
// It intentionally does not echo stored workspace contents or credential values.
func (r *WorkspaceAuditReport) ObserveWorkspaceAuditDocument(collection string, raw []byte) error {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("decode audit document in %s: %w", collection, err)
	}
	if document == nil {
		return fmt.Errorf("audit document in %s is not an object", collection)
	}
	r.Documents++
	if r.ByClass == nil {
		r.ByClass = map[string]int{}
	}
	if r.ByFeature == nil {
		r.ByFeature = map[string]int{}
	}
	if r.DigestRelations == nil {
		r.DigestRelations = map[string]int{}
	}
	if !containsAuditString(r.Collections, collection) {
		r.Collections = append(r.Collections, collection)
		sort.Strings(r.Collections)
	}
	key := auditString(document["_key"])
	known := map[string]bool{}
	for field, digestField := range workspaceAuditSlots[collection] {
		packet, present := document[field]
		if !present || string(packet) == "null" {
			continue
		}
		r.KnownPackets++
		object, normalized := auditObject(packet)
		if !auditWorkspaceShape(object) && (field == "config" || field == "compiledConfig") {
			r.NonWorkspacePackets++
			continue
		}
		known["$."+field] = true
		entry := auditWorkspacePacket(collection, key, "$."+field, normalized, object, true)
		entry.StoredDigest = auditString(document[digestField])
		entry.SnapshotToken = auditString(document["snapshotToken"])
		if field == "draftConfig" {
			entry.SnapshotToken = auditString(document["draftSnapshotToken"])
			if entry.SnapshotToken == "" {
				entry.SnapshotToken = auditString(document["snapshotToken"])
			}
		}
		entry.finishDigestRelation()
		r.addWorkspaceAuditEntry(entry)
	}
	var discover func(json.RawMessage, string)
	discover = func(packet json.RawMessage, path string) {
		object, normalized := auditObject(packet)
		if object != nil {
			if !known[path] && auditString(object["apiVersion"]) == authoringv2.APIVersion && auditString(object["kind"]) == authoringv2.WorkspaceKind {
				entry := auditWorkspacePacket(collection, key, path, normalized, object, false)
				entry.finishDigestRelation()
				r.addWorkspaceAuditEntry(entry)
			}
			fields := make([]string, 0, len(object))
			for field := range object {
				fields = append(fields, field)
			}
			sort.Strings(fields)
			for _, field := range fields {
				discover(object[field], path+"["+fmt.Sprintf("%q", field)+"]")
			}
			return
		}
		var items []json.RawMessage
		if json.Unmarshal(packet, &items) == nil {
			for index, item := range items {
				discover(item, fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
	if auditString(document["apiVersion"]) == authoringv2.APIVersion && auditString(document["kind"]) == authoringv2.WorkspaceKind {
		packet := make(map[string]json.RawMessage, len(document))
		for field, value := range document {
			if field != "_key" && field != "_id" && field != "_rev" {
				packet[field] = value
			}
		}
		normalized, err := json.Marshal(packet)
		if err != nil {
			return err
		}
		entry := auditWorkspacePacket(collection, key, "$", normalized, packet, false)
		entry.finishDigestRelation()
		r.addWorkspaceAuditEntry(entry)
	}
	// Match the known-position spelling before descending into each field.
	for field, packet := range document {
		if known["$."+field] {
			continue
		}
		discover(packet, "$."+field)
	}
	sort.Slice(r.Entries, func(i, j int) bool {
		a, b := r.Entries[i], r.Entries[j]
		if a.Collection != b.Collection {
			return a.Collection < b.Collection
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.Path < b.Path
	})
	return nil
}

func auditWorkspaceShape(object map[string]json.RawMessage) bool {
	if object == nil {
		return false
	}
	if auditString(object["kind"]) == authoringv2.WorkspaceKind {
		return true
	}
	_, documents := object["documents"]
	_, explorer := object["explorer"]
	return documents && explorer
}
func auditObject(raw json.RawMessage) (map[string]json.RawMessage, json.RawMessage) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		raw = json.RawMessage(text)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil, raw
	}
	return object, raw
}
func auditString(raw json.RawMessage) string {
	var text string
	_ = json.Unmarshal(raw, &text)
	return text
}
func containsAuditString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func auditWorkspacePacket(collection, key, path string, raw json.RawMessage, object map[string]json.RawMessage, authoritative bool) WorkspaceAuditEntry {
	entry := WorkspaceAuditEntry{Collection: collection, Key: key, Path: path, Authoritative: authoritative, Immutable: collection == DraftRevisionsCollection || collection == RevisionsCollection || collection == CompilationReceiptsCollection, Features: []string{}}
	_ = json.Unmarshal(object["semanticsVersion"], &entry.SemanticsVersion)
	if entry.SemanticsVersion < authoringv2.CurrentSemanticsVersion {
		entry.Features = append(entry.Features, "legacy-semantics")
		entry.NeedsPinnedCatalog = true
	}
	var documents []map[string]json.RawMessage
	_ = json.Unmarshal(object["documents"], &documents)
	for _, document := range documents {
		if tableShape, present := document["tableShape"]; present && strings.TrimSpace(string(tableShape)) != "null" {
			entry.TableShapeDocuments++
			if !containsAuditString(entry.Features, "legacy-table-shape") {
				entry.Features = append(entry.Features, "legacy-table-shape")
			}
		}
		if rows, present := document["rows"]; !present || strings.TrimSpace(string(rows)) == "null" {
			if !containsAuditString(entry.Features, "missing-record-rows") {
				entry.Features = append(entry.Features, "missing-record-rows")
			}
		}
		var construction map[string]json.RawMessage
		if json.Unmarshal(document["construction"], &construction) == nil && construction != nil {
			steps, present := construction["steps"]
			if !present || strings.TrimSpace(string(steps)) == "null" {
				entry.NilConstructionStepLists++
			}
		}
		var columns []map[string]json.RawMessage
		_ = json.Unmarshal(document["columns"], &columns)
		for _, column := range columns {
			source, _ := auditObject(column["source"])
			aggregate, _ := auditObject(source["aggregate"])
			for feature, present := range map[string]bool{
				"legacy-flat-source":        source["fieldPath"] != nil || source["projectionMode"] != nil || source["aggregateOperation"] != nil,
				"legacy-coded-source-kind":  auditString(source["kind"]) == "codingBySystem" || auditString(source["kind"]) == "observationComponentByCode",
				"legacy-aggregate-temporal": aggregate["temporal"] != nil,
			} {
				if present && !containsAuditString(entry.Features, feature) {
					entry.Features = append(entry.Features, feature)
				}
			}
			if source["wherePath"] != nil || source["whereEquals"] != nil || aggregate["where"] != nil {
				entry.NeedsPinnedCatalog = true
				if !containsAuditString(entry.Features, "legacy-contributor") {
					entry.Features = append(entry.Features, "legacy-contributor")
				}
			}
		}
	}
	sort.Strings(entry.Features)
	var direct authoringv2.Workspace
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	strictErr := decoder.Decode(&direct)
	if strictErr == nil {
		strictErr = direct.Validate()
	}
	if strictErr != nil {
		entry.StrictCurrentError = strictErr.Error()
	}
	workspace, err := authoringv2.DecodeStoredWorkspaceForMigration(raw)
	if err != nil {
		entry.Classification = "invalid-or-unsupported"
		if entry.SemanticsVersion > authoringv2.CurrentSemanticsVersion {
			entry.Classification = "future"
		}
		entry.DecodeError = err.Error()
		return entry
	}
	if strictErr != nil {
		entry.Features = append(entry.Features, "persisted-decoder-compatibility")
	}
	entry.DecodedCanonicalDigest, err = workspace.Digest()
	if err != nil {
		entry.DecodeError = err.Error()
		entry.Classification = "invalid-or-unsupported"
		return entry
	}
	canonical, canonicalErr := workspace.CanonicalJSON()
	entry.AlreadyCanonical = canonicalErr == nil && auditNormalizedJSON(raw) == auditNormalizedJSON(canonical)
	sort.Strings(entry.Features)
	entry.LegacyNilConstructionDigest, err = workspace.LegacyNilConstructionStepsDigest()
	if err != nil {
		entry.DecodeError = err.Error()
	}
	switch {
	case entry.SemanticsVersion > authoringv2.CurrentSemanticsVersion:
		entry.Classification = "future"
	case len(entry.Features) > 0 || entry.NilConstructionStepLists > 0:
		entry.Classification = "legacy"
	default:
		entry.Classification = "current"
	}
	return entry
}
func (e *WorkspaceAuditEntry) finishDigestRelation() {
	switch {
	case e.StoredDigest == "":
		e.DigestRelation = "no-stored-digest"
	case e.DecodedCanonicalDigest == "":
		e.DigestRelation = "unresolved"
	case e.StoredDigest == e.DecodedCanonicalDigest:
		e.DigestRelation = "matches-decoded-canonical"
	case e.LegacyNilConstructionDigest != "" && e.StoredDigest == e.LegacyNilConstructionDigest:
		e.DigestRelation = "matches-legacy-nil-construction"
	default:
		e.DigestRelation = "differs-from-decoded-canonical"
	}
}
func (r *WorkspaceAuditReport) addWorkspaceAuditEntry(entry WorkspaceAuditEntry) {
	if entry.Authoritative {
		r.AuthoritativeWorkspaces++
		if r.ByFeature == nil {
			r.ByFeature = map[string]int{}
		}
		for _, feature := range entry.Features {
			r.ByFeature[feature]++
		}
		if entry.AlreadyCanonical {
			r.AlreadyCanonicalWorkspaces++
		}
		r.ByClass[entry.Classification]++
		r.NilConstructionStepLists += entry.NilConstructionStepLists
		r.LegacyTableShapeDocuments += entry.TableShapeDocuments
		r.DigestRelations[entry.DigestRelation]++
	} else {
		r.DiscoveryCandidates++
		r.DiscoveredTableShapeDocuments += entry.TableShapeDocuments
	}
	r.Entries = append(r.Entries, entry)
}

func auditNormalizedJSON(raw []byte) string {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "invalid-json"
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return "invalid-json"
	}
	return string(normalized)
}
