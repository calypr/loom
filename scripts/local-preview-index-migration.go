package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/arangodb/go-driver/v2/connection"
	"github.com/arangodb/go-driver/v2/utils"
)

const (
	previewPrefix       = "loom_pivot_preview_"
	databaseName        = "loom_dev"
	collectionName      = "Observation"
	projectLabel        = "loom_dev_cda_fhir"
	generationLabel     = "cda-fhir-v1"
	composeProjectLabel = "loom-dev-6d7df93d6a37"
	maxOwnedIndexes     = 4
)

type indexSpec struct {
	Name         string   `json:"name"`
	ID           string   `json:"id,omitempty"`
	Type         string   `json:"type"`
	Fields       []string `json:"fields"`
	StoredValues []string `json:"storedValues"`
	Sparse       bool     `json:"sparse"`
	Unique       bool     `json:"unique"`
}

type manifest struct {
	SchemaVersion int `json:"schemaVersion"`
	Authorization struct {
		Kind             string `json:"kind"`
		ComposeProject   string `json:"composeProjectLabel"`
		ArangoContainer  string `json:"arangoContainer"`
		APIContainer     string `json:"apiContainer"`
		Database         string `json:"database"`
		Collection       string `json:"collection"`
		Project          string `json:"projectLabel"`
		Generation       string `json:"datasetGeneration"`
		NotSamePlanProof bool   `json:"notCompilerInferredSupersedence"`
	} `json:"authorization"`
	ExpectedOwnedIndexes []indexSpec `json:"expectedOwnedIndexes"`
}

type compilerIndexSpec struct {
	Collection   string   `json:"collection"`
	Name         string   `json:"name"`
	Fields       []string `json:"fields"`
	StoredValues []string `json:"storedValues"`
}

// migrationCollection is the smallest mutation surface needed by the local
// replacement transition. Keeping it narrow makes operation ordering and
// rollback testable without an Arango connection.
type migrationCollection interface {
	Indexes(context.Context) ([]driver.IndexResponse, error)
	EnsurePersistentIndex(context.Context, []string, *driver.CreatePersistentIndexOptions) (driver.IndexResponse, bool, error)
	DeleteIndexByID(context.Context, string) error
}

type indexState struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Fields       []string        `json:"fields"`
	StoredValues []string        `json:"storedValues"`
	Sparse       bool            `json:"sparse"`
	SparseSet    bool            `json:"sparseSet"`
	Unique       bool            `json:"unique"`
	UniqueSet    bool            `json:"uniqueSet"`
	Definition   json.RawMessage `json:"definition"`
}

type report struct {
	SchemaVersion int               `json:"schemaVersion"`
	Action        string            `json:"action"`
	Authorization string            `json:"authorization"`
	Endpoint      string            `json:"endpoint"`
	Database      string            `json:"database"`
	Collection    string            `json:"collection"`
	Project       string            `json:"project"`
	Generation    string            `json:"generation"`
	CompilerSpec  compilerIndexSpec `json:"compilerSpec"`
	Initial       []indexState      `json:"initialIndexes"`
	Final         []indexState      `json:"finalIndexes,omitempty"`
	OldIndexID    string            `json:"oldIndexID,omitempty"`
	NewIndexID    string            `json:"newIndexID,omitempty"`
	Mutations     []string          `json:"mutations"`
	Outcome       string            `json:"outcome"`
	Error         string            `json:"error,omitempty"`
}

func main() {
	var manifestPath, specPath, endpoint string
	var apply, authorized bool
	flag.StringVar(&manifestPath, "manifest", "", "local migration authorization manifest")
	flag.StringVar(&specPath, "compiler-spec", "", "PreviewCoveringIndexSpec JSON emitted by the exact native-shaped compiler request")
	flag.StringVar(&endpoint, "endpoint", "http://arangodb:8529", "Arango endpoint inside the owned local Compose network")
	flag.BoolVar(&apply, "apply", false, "create the candidate and remove the exact authorized legacy index")
	flag.BoolVar(&authorized, "authorize-local-migration", false, "confirm this is the explicitly authorized local verification migration")
	flag.Parse()

	if manifestPath == "" || specPath == "" {
		fatal(errors.New("--manifest and --compiler-spec are required"))
	}
	if apply != authorized {
		fatal(errors.New("--apply and --authorize-local-migration must be supplied together"))
	}
	if err := run(manifestPath, specPath, endpoint, apply); err != nil {
		fatal(err)
	}
}

func run(manifestPath, specPath, endpoint string, apply bool) error {
	var m manifest
	if err := readJSON(manifestPath, &m); err != nil {
		return fmt.Errorf("read migration manifest: %w", err)
	}
	var candidate compilerIndexSpec
	if err := readJSON(specPath, &candidate); err != nil {
		return fmt.Errorf("read compiler index spec: %w", err)
	}
	if err := validateInputs(m, candidate, endpoint); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	parsed, _ := url.Parse(endpoint)
	conn := connection.NewHttpConnection(connection.HttpConfiguration{
		Endpoint: connection.NewRoundRobinEndpoints([]string{endpoint}),
	})
	client := driver.NewClient(conn)
	databaseExists, err := client.DatabaseExists(ctx, databaseName)
	if err != nil {
		return fmt.Errorf("check existing database: %w", err)
	}
	if !databaseExists {
		return fmt.Errorf("refusing to create missing database %q", databaseName)
	}
	db, err := client.GetDatabase(ctx, databaseName, nil)
	if err != nil {
		return fmt.Errorf("open existing database: %w", err)
	}
	collectionExists, err := db.CollectionExists(ctx, collectionName)
	if err != nil {
		return fmt.Errorf("check existing collection: %w", err)
	}
	if !collectionExists {
		return fmt.Errorf("refusing to create missing collection %q", collectionName)
	}
	collection, err := db.GetCollection(ctx, collectionName, nil)
	if err != nil {
		return fmt.Errorf("open existing collection: %w", err)
	}
	if err := requireProjectGenerationWitness(ctx, db); err != nil {
		return err
	}

	indexes, err := collection.Indexes(ctx)
	if err != nil {
		return fmt.Errorf("read initial index inventory: %w", err)
	}
	initialAll := snapshotAll(indexes)
	initialOwned, err := validateOwnedInventory(indexes, m.ExpectedOwnedIndexes, candidate)
	if err != nil {
		return err
	}
	oldName := m.ExpectedOwnedIndexes[0].Name
	var oldID string
	if index, ok := initialOwned[oldName]; ok {
		oldID = index.ID
	}

	r := report{
		SchemaVersion: 1,
		Action:        map[bool]string{true: "apply", false: "dry-run"}[apply],
		Authorization: "explicit local verification setup; NOT compiler-inferred same-plan supersedence",
		Endpoint:      parsed.Scheme + "://" + parsed.Host,
		Database:      databaseName,
		Collection:    collectionName,
		Project:       projectLabel,
		Generation:    generationLabel,
		CompilerSpec:  candidate,
		Initial:       sortedStates(initialAll),
		OldIndexID:    oldID,
		Mutations:     []string{},
		Outcome:       "planned",
	}

	r, err = transitionLocalIndex(ctx, collection, m.ExpectedOwnedIndexes, candidate, apply, initialAll, initialOwned, r)
	if err != nil {
		return emitFailure(r, err)
	}
	return emit(r)
}

func transitionLocalIndex(ctx context.Context, collection migrationCollection, expected []indexSpec, candidate compilerIndexSpec, apply bool, initialAll map[string]indexState, initialOwned map[string]indexState, r report) (report, error) {
	oldName := expected[0].Name
	oldID := r.OldIndexID
	oldPresent := oldID != ""
	newIndex, newPresent := initialOwned[candidate.Name]
	candidateCreatedByInvocation := false
	if !oldPresent && !newPresent {
		return r, errors.New("neither the exact authorized old index nor the exact candidate index exists")
	}
	if !oldPresent && newPresent {
		if !isCompletedOwnedSet(initialOwned, expected, candidate) {
			return r, errors.New("candidate exists without the exact completed four-index inventory")
		}
		r.NewIndexID = newIndex.ID
		r.Final = sortedStates(initialAll)
		r.Outcome = "already-complete"
		return r, nil
	}
	if !apply {
		r.Outcome = "dry-run-ready"
		r.NewIndexID = newIndex.ID
		return r, nil
	}

	if !newPresent {
		created, wasCreated, createErr := collection.EnsurePersistentIndex(ctx, candidate.Fields, &driver.CreatePersistentIndexOptions{
			Name:         candidate.Name,
			StoredValues: append([]string(nil), candidate.StoredValues...),
			Sparse:       utils.NewType(false),
			Unique:       utils.NewType(false),
		})
		if createErr != nil {
			return r, fmt.Errorf("create candidate index before deleting old index: %w", createErr)
		}
		if !matchesIndex(created, candidate.Name, candidate.Fields, candidate.StoredValues) || created.ID == "" {
			verificationErr := errors.New("Arango returned an unexpected candidate index definition")
			if wasCreated && created.ID != "" {
				verificationErr = errors.Join(verificationErr, rollbackCandidate(ctx, collection, created.ID, candidate))
			}
			return r, verificationErr
		}
		newIndex = toState(created)
		newPresent = true
		candidateCreatedByInvocation = wasCreated
		if wasCreated {
			r.Mutations = append(r.Mutations, "created candidate compiler index")
		}
	}
	r.NewIndexID = newIndex.ID
	rollbackCreatedCandidate := func() error {
		if !candidateCreatedByInvocation {
			return nil
		}
		return rollbackCandidate(ctx, collection, newIndex.ID, candidate)
	}

	// The old ID and every unrelated definition are reread after creation and
	// immediately before deletion. This local migration uses explicit inventory
	// authorization; it is not a compiler replacement/supersedence claim.
	preDeleteIndexes, err := collection.Indexes(ctx)
	if err != nil {
		rollbackErr := rollbackCreatedCandidate()
		return r, errors.Join(fmt.Errorf("reread before legacy deletion: %w", err), rollbackErr)
	}
	preDeleteAll := snapshotAll(preDeleteIndexes)
	preDeleteOwned, err := validateOwnedInventory(preDeleteIndexes, expected, candidate)
	if err == nil {
		oldNow, oldOK := preDeleteOwned[oldName]
		newNow, newOK := preDeleteOwned[candidate.Name]
		if !oldOK || oldNow.ID != oldID || !newOK || newNow.ID != newIndex.ID ||
			!isMigrationStagingSet(preDeleteOwned, expected, candidate) || !sameUntouchedIndexes(initialAll, preDeleteAll, oldID, newIndex.ID) {
			err = errors.New("inventory changed before deletion; exact old ID or unrelated definitions no longer match")
		}
	}
	if err != nil {
		rollbackErr := rollbackCreatedCandidate()
		return r, errors.Join(err, rollbackErr)
	}

	if err := collection.DeleteIndexByID(ctx, oldID); err != nil {
		rollbackErr := restoreOriginalOwnedInventory(ctx, collection, expected, candidate, newIndex.ID, candidateCreatedByInvocation)
		return r, errors.Join(fmt.Errorf("delete exact old index ID %q: %w", oldID, err), rollbackErr)
	}
	r.Mutations = append(r.Mutations, "deleted exact reread old index ID")

	finalIndexes, err := collection.Indexes(ctx)
	if err != nil {
		restoreErr := restoreOriginalOwnedInventory(ctx, collection, expected, candidate, newIndex.ID, candidateCreatedByInvocation)
		return r, errors.Join(fmt.Errorf("read final index inventory after replacement: %w", err), restoreErr)
	}
	finalAll := snapshotAll(finalIndexes)
	finalOwned, err := validateOwnedInventory(finalIndexes, expected, candidate)
	if err == nil && (!isCompletedOwnedSet(finalOwned, expected, candidate) ||
		!sameUntouchedIndexes(initialAll, finalAll, oldID, newIndex.ID)) {
		err = errors.New("final inventory does not contain the exact candidate plus the three preserved owned indexes")
	}
	if err != nil {
		// Restore the authorized old definition first, then remove only the exact
		// candidate ID so a verification failure leaves the original inventory.
		restoreErr := restoreOriginalOwnedInventory(ctx, collection, expected, candidate, newIndex.ID, candidateCreatedByInvocation)
		return r, errors.Join(err, restoreErr)
	}
	r.Final = sortedStates(finalAll)
	r.NewIndexID = finalOwned[candidate.Name].ID
	r.Outcome = "migrated"
	return r, nil
}

func validateInputs(m manifest, candidate compilerIndexSpec, endpoint string) error {
	if m.SchemaVersion != 1 || m.Authorization.Kind != "local-cda-preview-index-verification-migration" ||
		m.Authorization.ComposeProject != composeProjectLabel ||
		m.Authorization.ArangoContainer != "loom-dev-6d7df93d6a37-arangodb-1" ||
		m.Authorization.APIContainer != "loom-dev-6d7df93d6a37-loom-api-1" ||
		m.Authorization.Database != databaseName || m.Authorization.Collection != collectionName ||
		m.Authorization.Project != projectLabel || m.Authorization.Generation != generationLabel ||
		!m.Authorization.NotSamePlanProof {
		return errors.New("manifest is not the exact authorized local CDA verification migration")
	}
	if len(m.ExpectedOwnedIndexes) != maxOwnedIndexes {
		return fmt.Errorf("manifest must list the exact %d-index starting compiler-owned inventory", maxOwnedIndexes)
	}
	if !sameSpecLists(m.ExpectedOwnedIndexes, expectedInventory()) {
		return errors.New("manifest inventory differs from the exact authorized local four-index snapshot")
	}
	legacy := m.ExpectedOwnedIndexes[0]
	if legacy.Name != fieldOnlyIndexName(collectionName, legacy.Fields) || len(legacy.StoredValues) != 0 {
		return errors.New("authorized legacy index name/hash or empty stored-values definition does not match its exact fields")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Host != "arangodb:8529" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("endpoint must be the owned Compose Arango service with no credentials, path, query, or fragment")
	}
	if !sameCompilerIndexSpec(candidate, expectedCompilerIndexSpec()) {
		return errors.New("compiler spec is not the exact current full-hash stored-parent projection for this local verification case")
	}
	if len(m.ExpectedOwnedIndexes) == 0 || m.ExpectedOwnedIndexes[0].Name != "loom_pivot_preview_46a620d918ffe1d6" {
		return errors.New("manifest does not authorize the exact legacy preview index name")
	}
	for _, expected := range m.ExpectedOwnedIndexes {
		if expected.Type != "persistent" || expected.Sparse || expected.Unique || expected.ID == "" || !strings.HasPrefix(expected.Name, previewPrefix) {
			return fmt.Errorf("invalid expected inventory entry %q", expected.Name)
		}
	}
	return nil
}

func expectedCompilerIndexSpec() compilerIndexSpec {
	spec := compilerIndexSpec{
		Collection:   collectionName,
		Fields:       []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"},
		StoredValues: []string{"payload.valueQuantity"},
	}
	spec.Name = storedValuesIndexName(spec.Collection, spec.Fields, spec.StoredValues)
	return spec
}

func sameCompilerIndexSpec(a, b compilerIndexSpec) bool {
	return a.Collection == b.Collection && a.Name == b.Name &&
		slices.Equal(a.Fields, b.Fields) && slices.Equal(a.StoredValues, b.StoredValues)
}

func requireProjectGenerationWitness(ctx context.Context, db driver.Database) error {
	query := "FOR doc IN @@collection FILTER doc.project == @project AND doc.dataset_generation == @generation LIMIT 1 RETURN {project: doc.project, dataset_generation: doc.dataset_generation}"
	cursor, err := db.Query(ctx, query, &driver.QueryOptions{BindVars: map[string]interface{}{
		"@collection": collectionName,
		"project":     projectLabel,
		"generation":  generationLabel,
	}})
	if err != nil {
		return fmt.Errorf("validate project/generation witness: %w", err)
	}
	defer cursor.Close()
	var witness map[string]interface{}
	if _, err := cursor.ReadDocument(ctx, &witness); err != nil {
		if shared.IsNoMoreDocuments(err) {
			return errors.New("target project/generation has no Observation documents in the selected local database")
		}
		return fmt.Errorf("read project/generation witness: %w", err)
	}
	if witness["project"] != projectLabel || witness["dataset_generation"] != generationLabel {
		return errors.New("selected database returned a mismatched project/generation witness")
	}
	return nil
}

func validateOwnedInventory(indexes []driver.IndexResponse, expected []indexSpec, candidate compilerIndexSpec) (map[string]indexState, error) {
	owned := make(map[string]indexState)
	for _, index := range indexes {
		if strings.HasPrefix(index.Name, previewPrefix) {
			if _, duplicate := owned[index.Name]; duplicate {
				return nil, fmt.Errorf("duplicate compiler-owned index name %q", index.Name)
			}
			owned[index.Name] = toState(index)
		}
	}
	expectedNames := make(map[string]indexSpec, len(expected))
	for _, item := range expected {
		expectedNames[item.Name] = item
	}
	allowedCount := len(expected)
	_, hasCandidate := owned[candidate.Name]
	_, hasOld := owned[expected[0].Name]
	if hasCandidate && hasOld {
		allowedCount++
	}
	if len(owned) != allowedCount && !(len(owned) == 0 && len(expected) == 0) {
		return nil, fmt.Errorf("compiler-owned inventory has %d indexes; expected exactly %d known entries", len(owned), allowedCount)
	}
	for name, state := range owned {
		if name == candidate.Name {
			if !matchesState(state, candidate.Name, candidate.Fields, candidate.StoredValues) {
				return nil, fmt.Errorf("candidate index %q has different fields or stored values", name)
			}
			continue
		}
		item, ok := expectedNames[name]
		if !ok {
			return nil, fmt.Errorf("unexpected compiler-owned index %q", name)
		}
		if state.ID != item.ID || !matchesState(state, item.Name, item.Fields, item.StoredValues) {
			return nil, fmt.Errorf("compiler-owned index %q no longer matches the exact authorized inventory tuple", name)
		}
	}
	if _, exists := owned[expected[0].Name]; !exists {
		// Candidate-only completed inventory is checked separately by the caller.
		if _, hasCandidate := owned[candidate.Name]; !hasCandidate {
			return nil, errors.New("exact old index and compiler candidate are both absent")
		}
	}
	for _, item := range expected[1:] {
		if _, exists := owned[item.Name]; !exists {
			return nil, fmt.Errorf("unrelated owned index %q is missing", item.Name)
		}
	}
	return owned, nil
}

func isMigrationStagingSet(owned map[string]indexState, expected []indexSpec, candidate compilerIndexSpec) bool {
	return len(owned) == len(expected)+1 && owned[expected[0].Name].ID != "" && owned[candidate.Name].ID != ""
}

func isCompletedOwnedSet(owned map[string]indexState, expected []indexSpec, candidate compilerIndexSpec) bool {
	if len(owned) != len(expected) || owned[expected[0].Name].ID != "" || owned[candidate.Name].ID == "" {
		return false
	}
	for _, item := range expected[1:] {
		if owned[item.Name].ID != item.ID {
			return false
		}
	}
	return true
}

func snapshotAll(indexes []driver.IndexResponse) map[string]indexState {
	result := make(map[string]indexState, len(indexes))
	for _, index := range indexes {
		result[index.ID] = toState(index)
	}
	return result
}

func sameUntouchedIndexes(before, after map[string]indexState, oldID, newID string) bool {
	for id, state := range before {
		if id == oldID {
			continue
		}
		if current, ok := after[id]; !ok || !sameState(current, state) {
			return false
		}
	}
	for id := range after {
		if id == newID || id == oldID {
			continue
		}
		if _, existed := before[id]; !existed {
			return false
		}
	}
	return true
}

func sameState(a, b indexState) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Type == b.Type &&
		slices.Equal(a.Fields, b.Fields) && slices.Equal(a.StoredValues, b.StoredValues) &&
		a.Sparse == b.Sparse && a.SparseSet == b.SparseSet &&
		a.Unique == b.Unique && a.UniqueSet == b.UniqueSet && bytes.Equal(a.Definition, b.Definition)
}

func toState(index driver.IndexResponse) indexState {
	state := indexState{ID: index.ID, Name: index.Name, Type: string(index.Type)}
	state.Definition, _ = json.Marshal(index)
	if index.RegularIndex != nil {
		state.Fields = append([]string(nil), index.RegularIndex.Fields...)
		state.StoredValues = append([]string(nil), index.RegularIndex.StoredValues...)
	}
	if index.Sparse != nil {
		state.Sparse = *index.Sparse
		state.SparseSet = true
	}
	if index.Unique != nil {
		state.Unique = *index.Unique
		state.UniqueSet = true
	}
	return state
}

func matchesIndex(index driver.IndexResponse, name string, fields, storedValues []string) bool {
	return matchesState(toState(index), name, fields, storedValues)
}

func matchesState(state indexState, name string, fields, storedValues []string) bool {
	return state.Name == name && state.Type == "persistent" && state.ID != "" &&
		state.SparseSet && !state.Sparse && state.UniqueSet && !state.Unique &&
		slices.Equal(state.Fields, fields) && slices.Equal(state.StoredValues, storedValues)
}

func expectedInventory() []indexSpec {
	return []indexSpec{
		{
			Name: "loom_pivot_preview_46a620d918ffe1d6", ID: "Observation/3903331518", Type: "persistent",
			Fields:       []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status", "payload.subject.reference", "payload.valueQuantity.value"},
			StoredValues: []string{}, Sparse: false, Unique: false,
		},
		{
			Name: "loom_pivot_preview_1d784c12d48ef59e", ID: "Observation/4003671588", Type: "persistent",
			Fields:       []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status", "payload.subject.reference"},
			StoredValues: []string{}, Sparse: false, Unique: false,
		},
		{
			Name: "loom_pivot_preview_8ab3637d7bcc1d7e", ID: "Observation/4003678682", Type: "persistent",
			Fields:       []string{"project", "dataset_generation", "payload.status", "auth_resource_path"},
			StoredValues: []string{}, Sparse: false, Unique: false,
		},
		{
			Name: "loom_pivot_preview_601548896a95cbf8", ID: "Observation/4097745091", Type: "persistent",
			Fields:       []string{"project", "dataset_generation", "resourceType", "payload.valueQuantity.code", "auth_resource_path"},
			StoredValues: []string{}, Sparse: false, Unique: false,
		},
	}
}

func sameSpecLists(a, b []indexSpec) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index].Name != b[index].Name || a[index].ID != b[index].ID || a[index].Type != b[index].Type ||
			a[index].Sparse != b[index].Sparse || a[index].Unique != b[index].Unique ||
			!slices.Equal(a[index].Fields, b[index].Fields) || !slices.Equal(a[index].StoredValues, b[index].StoredValues) {
			return false
		}
	}
	return true
}

func rollbackCandidate(ctx context.Context, collection migrationCollection, candidateID string, candidate compilerIndexSpec) error {
	if candidateID == "" {
		return errors.New("cannot roll back candidate without stable ID")
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	indexes, err := collection.Indexes(cleanupCtx)
	if err != nil {
		return fmt.Errorf("rollback candidate reread: %w", err)
	}
	for _, index := range indexes {
		if index.ID != candidateID {
			continue
		}
		if !matchesIndex(index, candidate.Name, candidate.Fields, candidate.StoredValues) {
			return errors.New("rollback candidate ID now names a different index definition; left it untouched")
		}
		if err := collection.DeleteIndexByID(cleanupCtx, candidateID); err != nil {
			return fmt.Errorf("delete exact candidate index ID during rollback: %w", err)
		}
		return nil
	}
	return nil
}

func restoreOriginalOwnedInventory(ctx context.Context, collection migrationCollection, expected []indexSpec, candidate compilerIndexSpec, candidateID string, removeCandidate bool) error {
	old := expected[0]
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	indexes, err := collection.Indexes(cleanupCtx)
	if err != nil {
		return fmt.Errorf("rollback inventory reread: %w", err)
	}
	oldPresent := false
	for _, index := range indexes {
		if index.Name == old.Name {
			if !matchesIndex(index, old.Name, old.Fields, old.StoredValues) {
				return errors.New("cannot restore old index: same name has a conflicting definition")
			}
			oldPresent = true
		}
	}
	if !oldPresent {
		index, _, err := collection.EnsurePersistentIndex(cleanupCtx, old.Fields, &driver.CreatePersistentIndexOptions{
			Name:         old.Name,
			StoredValues: append([]string(nil), old.StoredValues...),
			Sparse:       utils.NewType(false),
			Unique:       utils.NewType(false),
		})
		if err != nil {
			return fmt.Errorf("restore exact old index definition: %w", err)
		}
		if !matchesIndex(index, old.Name, old.Fields, old.StoredValues) {
			return errors.New("restored old index returned a conflicting definition")
		}
	}
	if removeCandidate {
		if err := rollbackCandidate(cleanupCtx, collection, candidateID, candidate); err != nil {
			return fmt.Errorf("remove candidate after restoring old inventory: %w", err)
		}
	}
	return nil
}

func storedValuesIndexName(collection string, fields, storedValues []string) string {
	canonicalStoredValues := append([]string(nil), storedValues...)
	sort.Strings(canonicalStoredValues)
	canonical := collection + "\x00fields\x00" + strings.Join(fields, "\x00") +
		"\x00storedValues\x00" + strings.Join(canonicalStoredValues, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return previewPrefix + hex.EncodeToString(digest[:8])
}

func fieldOnlyIndexName(collection string, fields []string) string {
	digest := sha256.Sum256([]byte(collection + "\x00" + strings.Join(fields, "\x00")))
	return previewPrefix + hex.EncodeToString(digest[:8])
}

func sortedStates(indexes map[string]indexState) []indexState {
	result := make([]indexState, 0, len(indexes))
	for _, state := range indexes {
		result = append(result, state)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func readJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func emitFailure(r report, err error) error {
	r.Outcome = "failed"
	r.Error = err.Error()
	_ = emit(r)
	return err
}

func emit(r report) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(r)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
