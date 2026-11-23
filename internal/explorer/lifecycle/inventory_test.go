package lifecycle

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func builderFullUITestModelState(t *testing.T, stateID, profile, stageID string, steps []map[string]any) json.RawMessage {
	t.Helper()
	stageOperation := "SOURCE_PROJECTION"
	if stageID != recipe.ConstructionSourceProjectionID {
		stageOperation = "FILTER"
	}
	columns := []map[string]any{
		{"id": "model_column_string_required", "classId": "string.required.nonnullable.scalar", "logicalType": "string", "cardinality": "required_one"},
		{"id": "model_column_string_optional", "classId": "string.optional.nullable.scalar", "logicalType": "string", "cardinality": "optional_one"},
		{"id": "model_column_boolean_optional", "classId": "boolean.optional.nullable.scalar", "logicalType": "boolean", "cardinality": "optional_one"},
		{"id": "model_column_integer_optional", "classId": "integer.optional.nullable.scalar", "logicalType": "integer", "cardinality": "optional_one"},
		{"id": "model_column_date_optional", "classId": "date.optional.nullable.scalar", "logicalType": "date", "cardinality": "optional_one"},
	}
	state, err := json.Marshal(map[string]any{
		"stateId": stateID, "seedProfile": profile, "workspacePhase": "workspace-loaded",
		"acceptedWorkspace": map[string]any{
			"rootType":     "Patient",
			"identity":     map[string]any{"outputId": "model_output"},
			"construction": map[string]any{"steps": steps},
		},
		"selectedStage": map[string]any{"id": stageID, "operation": stageOperation},
		"columns":       columns,
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func builderFullUITestRootModelState(t *testing.T, stateID string) json.RawMessage {
	t.Helper()
	state, err := json.Marshal(map[string]any{
		"stateId": stateID, "seedProfile": "empty-root", "workspacePhase": "root-uninitialized",
		"acceptedWorkspace": nil, "selectedStage": nil, "columns": []any{},
		"rootContext": map[string]any{
			"project": "model_project", "explorerId": "model_explorer", "snapshotToken": "model_snapshot",
			"selectionRevisionId": "model_selection_revision", "draftVersion": 1, "draftDigest": "model_digest",
			"catalogSnapshot": nil,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func (r builderFullUIInventoryRow) key() builderFullUIKey {
	return builderFullUIKey{CaseID: r.CaseID, EntryID: r.EntryID, ActionIndex: r.ActionIndex}
}

func readBuilderFullUIInventory(path string) (json.RawMessage, []builderFullUIInventoryRow, []builderFullUIGuardRow, string, error) {
	lines, artifactSHA, err := readBuilderFullUILines(path)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if len(lines) < 2 {
		return nil, nil, nil, "", fmt.Errorf("inventory JSONL requires a header and at least one case or guard row")
	}
	var headerMap map[string]json.RawMessage
	if err := json.Unmarshal(lines[0], &headerMap); err != nil {
		return nil, nil, nil, "", fmt.Errorf("decode inventory header: %w", err)
	}
	if hasRawField(headerMap, "entryId") {
		return nil, nil, nil, "", fmt.Errorf("first inventory JSONL record has entryId; expected header")
	}
	var headerType string
	_ = json.Unmarshal(headerMap["recordType"], &headerType)
	if headerType != "header" {
		return nil, nil, nil, "", fmt.Errorf("inventory first recordType=%q, want header", headerType)
	}
	if schemaVersion, ok := rawIntField(headerMap, "schemaVersion"); !ok || schemaVersion != 1 {
		return nil, nil, nil, "", fmt.Errorf("inventory header schemaVersion=%d present=%t, want 1", schemaVersion, ok)
	}

	rows := make([]builderFullUIInventoryRow, 0, len(lines)-1)
	guards := make([]builderFullUIGuardRow, 0)
	seen := map[string]bool{}
	seenGuards := map[string]bool{}
	for lineIndex, raw := range lines[1:] {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, nil, nil, "", fmt.Errorf("decode inventory row %d: %w", lineIndex+2, err)
		}
		var recordType string
		_ = json.Unmarshal(fields["recordType"], &recordType)
		if recordType == "guard" {
			var guard builderFullUIGuardRow
			if err := json.Unmarshal(raw, &guard); err != nil {
				return nil, nil, nil, "", fmt.Errorf("decode guard row %d: %w", lineIndex+2, err)
			}
			guard.Raw = append(json.RawMessage(nil), raw...)
			if guard.GuardID == "" || guard.EntryID == "" || guard.StateID == "" {
				return nil, nil, nil, "", fmt.Errorf("guard row %d omits guardId, entryId, or stateId", lineIndex+2)
			}
			if version, ok := rawIntField(fields, "schemaVersion"); !ok || version != 1 {
				return nil, nil, nil, "", fmt.Errorf("guard row %d schemaVersion=%d present=%t, want 1", lineIndex+2, version, ok)
			}
			if seenGuards[guard.GuardID] {
				return nil, nil, nil, "", fmt.Errorf("duplicate guardId %q", guard.GuardID)
			}
			seenGuards[guard.GuardID] = true
			guards = append(guards, guard)
			continue
		}
		if recordType != "case" {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d recordType=%q, want case or guard", lineIndex+2, recordType)
		}
		row := builderFullUIInventoryRow{Raw: append(json.RawMessage(nil), raw...)}
		if err := decodeRawField(fields, "caseId", &row.CaseID); err != nil {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d caseId: %w", lineIndex+2, err)
		}
		if err := decodeRawField(fields, "entryId", &row.EntryID); err != nil {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d entryId: %w", lineIndex+2, err)
		}
		if err := decodeRawField(fields, "actionIndex", &row.ActionIndex); err != nil {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d actionIndex: %w", lineIndex+2, err)
		}
		if err := decodeRawField(fields, "requestMode", &row.RequestMode); err != nil {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d requestMode: %w", lineIndex+2, err)
		}
		if version, ok := rawIntField(fields, "schemaVersion"); !ok || version != 1 {
			return nil, nil, nil, "", fmt.Errorf("case row %d schemaVersion=%d present=%t, want 1", lineIndex+2, version, ok)
		}
		row.Request = copyRawField(fields, "request")
		row.Requests = copyRawField(fields, "requests")
		row.Before = copyRawField(fields, "before")
		if stateRefs := copyRawField(fields, "stateRefs"); len(stateRefs) != 0 {
			var refs builderFullUIStateRefs
			if err := json.Unmarshal(stateRefs, &refs); err != nil {
				row.StateResolveError = "decode stateRefs: " + err.Error()
			} else {
				row.StateRefBefore = refs.Before
				if row.StateRefBefore == "" {
					row.StateResolveError = "stateRefs.before is missing"
				}
			}
		} else {
			row.StateResolveError = "stateRefs.before is missing"
		}
		row.Action = copyRawField(fields, "action")
		if action := rawObject(row.Action); action != nil {
			row.ActionRequest = copyRawField(action, "request")
			row.ActionExpectation = copyRawField(action, "expectation")
			row.RequestStep = copyRawField(action, "requestStep")
		}
		row.Seed = copyRawField(fields, "seed")
		row.InputState = copyRawField(fields, "inputState")
		row.ExpectedState = copyRawField(fields, "expectedState")
		row.Assignment = copyRawField(fields, "assignment")
		row.Actions = copyRawField(fields, "actions")
		row.TracePrefix = copyRawField(fields, "tracePrefix")
		row.CapabilityProvenance = copyRawField(fields, "capabilityProvenance")
		row.Expectation = copyRawField(fields, "expectation")
		if len(row.Expectation) == 0 {
			row.Expectation = row.ActionExpectation
		}
		if row.CaseID == "" || row.EntryID == "" || row.ActionIndex < 0 {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d has invalid key %q/%q/%d", lineIndex+2, row.CaseID, row.EntryID, row.ActionIndex)
		}
		if row.RequestMode != "emitted" && row.RequestMode != "blocked" && row.RequestMode != "gap" && row.RequestMode != "local" {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d has unsupported requestMode %q", lineIndex+2, row.RequestMode)
		}
		if seen[row.key().string()] {
			return nil, nil, nil, "", fmt.Errorf("duplicate inventory request key %q/%q/%d", row.CaseID, row.EntryID, row.ActionIndex)
		}
		seen[row.key().string()] = true
		rows = append(rows, row)
	}
	return append(json.RawMessage(nil), lines[0]...), rows, guards, artifactSHA, nil
}

func readBuilderFullUIInventoryManifest(path string, modelHeader json.RawMessage) (map[string]builderFullUIInventoryManifestEntry, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(content)
	manifestSHA := hex.EncodeToString(sum[:])
	if want := builderFullUIInventorySourceSHA(modelHeader); want == "" || want != manifestSHA {
		return nil, "", fmt.Errorf("inventory manifest SHA-256 %q differs from model header %q", manifestSHA, want)
	}
	var manifest builderFullUIInventoryManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, "", fmt.Errorf("decode inventory manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || len(manifest.Affordances) == 0 {
		return nil, "", fmt.Errorf("inventory manifest schemaVersion=%d affordances=%d, want schemaVersion 1 and nonempty affordances", manifest.SchemaVersion, len(manifest.Affordances))
	}
	entries := make(map[string]builderFullUIInventoryManifestEntry, len(manifest.Affordances))
	for index, entry := range manifest.Affordances {
		if strings.TrimSpace(entry.ID) == "" {
			return nil, "", fmt.Errorf("inventory manifest affordances[%d] has no exact id", index)
		}
		if _, duplicate := entries[entry.ID]; duplicate {
			return nil, "", fmt.Errorf("inventory manifest repeats exact entry id %q", entry.ID)
		}
		entries[entry.ID] = entry
	}
	return entries, manifestSHA, nil
}

func readBuilderFullUIModelStates(path, expectedSHA string, modelHeader json.RawMessage) (map[string]json.RawMessage, string, error) {
	lines, artifactSHA, err := readBuilderFullUILines(path)
	if err != nil {
		return nil, "", err
	}
	if expectedSHA == "" || artifactSHA != expectedSHA {
		return nil, "", fmt.Errorf("ModelState artifact SHA-256 %q differs from supplied %q", artifactSHA, expectedSHA)
	}
	var header map[string]json.RawMessage
	if err := json.Unmarshal(modelHeader, &header); err != nil {
		return nil, "", fmt.Errorf("decode model header: %w", err)
	}
	var artifact struct {
		Path          string `json:"path"`
		SchemaVersion int    `json:"schemaVersion"`
		SHA256        string `json:"sha256"`
		StateCount    int    `json:"stateCount"`
	}
	if err := json.Unmarshal(header["stateArtifact"], &artifact); err != nil {
		return nil, "", fmt.Errorf("decode model stateArtifact header binding: %w", err)
	}
	if artifact.SchemaVersion != 1 || artifact.SHA256 != artifactSHA || filepath.Base(artifact.Path) != filepath.Base(path) {
		return nil, "", fmt.Errorf("ModelState artifact binding differs: header=%#v actualSha=%s path=%s", artifact, artifactSHA, path)
	}
	var sidecarHeader struct {
		RecordType    string `json:"recordType"`
		SchemaVersion int    `json:"schemaVersion"`
		StateCount    int    `json:"stateCount"`
		DomainSHA256  string `json:"domainSHA256"`
		ModelSHA256   string `json:"modelSHA256"`
	}
	if err := json.Unmarshal(lines[0], &sidecarHeader); err != nil {
		return nil, "", fmt.Errorf("decode ModelState sidecar header: %w", err)
	}
	var modelBinding struct {
		DomainSHA256 string `json:"domainSHA256"`
		ModelSHA256  string `json:"modelSHA256"`
	}
	if err := json.Unmarshal(modelHeader, &modelBinding); err != nil {
		return nil, "", fmt.Errorf("decode main model domain/model hashes: %w", err)
	}
	if sidecarHeader.RecordType != "header" || sidecarHeader.SchemaVersion != 1 || sidecarHeader.StateCount != artifact.StateCount || sidecarHeader.DomainSHA256 != modelBinding.DomainSHA256 || sidecarHeader.ModelSHA256 != modelBinding.ModelSHA256 {
		return nil, "", fmt.Errorf("ModelState sidecar header differs from model header: sidecar=%#v model=%#v stateArtifact=%#v", sidecarHeader, modelBinding, artifact)
	}
	stateLines := lines[1:]
	if artifact.StateCount != len(stateLines) {
		return nil, "", fmt.Errorf("ModelState stateCount=%d but sidecar has %d state rows", artifact.StateCount, len(stateLines))
	}
	states := make(map[string]json.RawMessage, len(stateLines))
	for index, raw := range stateLines {
		var row builderFullUIModelStateRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, "", fmt.Errorf("decode ModelState row %d: %w", index+2, err)
		}
		if row.RecordType != "state" || row.SchemaVersion != 1 || row.StateID == "" || isJSONNullOrEmpty(row.ModelState) || rawObject(row.ModelState) == nil {
			return nil, "", fmt.Errorf("ModelState row %d has invalid record type/id/value", index+2)
		}
		if _, duplicate := states[row.StateID]; duplicate {
			return nil, "", fmt.Errorf("ModelState artifact repeats stateId %q", row.StateID)
		}
		states[row.StateID] = append(json.RawMessage(nil), row.ModelState...)
	}
	return states, artifactSHA, nil
}

func bindBuilderFullUIGuardStates(rows []builderFullUIGuardRow, states map[string]json.RawMessage) int {
	resolved := 0
	for index := range rows {
		row := &rows[index]
		state, exists := states[row.StateID]
		if !exists {
			row.StateResolveError = fmt.Sprintf("guard stateId %q is missing from the bound ModelState artifact", row.StateID)
			continue
		}
		row.State = append(json.RawMessage(nil), state...)
		resolved++
	}
	return resolved
}

func bindBuilderFullUIModelStates(rows []builderFullUIInventoryRow, states map[string]json.RawMessage) error {
	for index := range rows {
		row := &rows[index]
		if row.StateResolveError != "" {
			continue
		}
		state, exists := states[row.StateRefBefore]
		if !exists {
			row.StateResolveError = fmt.Sprintf("stateRefs.before %q is missing from the bound ModelState artifact", row.StateRefBefore)
			continue
		}
		var stateID struct {
			StateID string `json:"stateId"`
		}
		if err := json.Unmarshal(state, &stateID); err != nil {
			row.StateResolveError = "decode referenced ModelState: " + err.Error()
			continue
		}
		if stateID.StateID != row.StateRefBefore {
			row.StateResolveError = fmt.Sprintf("ModelState stateId %q differs from exact stateRefs.before %q", stateID.StateID, row.StateRefBefore)
			continue
		}
		row.Before = append(json.RawMessage(nil), state...)
	}
	return nil
}

func bindBuilderFullUIInventorySeeds(rows []builderFullUIInventoryRow, manifest map[string]builderFullUIInventoryManifestEntry) error {
	for index := range rows {
		row := &rows[index]
		entry, exists := manifest[row.EntryID]
		if !exists {
			continue
		}
		if entry.ID != row.EntryID {
			return fmt.Errorf("manifest lookup key %q resolved entry id %q", row.EntryID, entry.ID)
		}
		var seed builderFullUIManifestSeed
		if err := json.Unmarshal(entry.Seed, &seed); err != nil {
			row.StateResolveError = "decode exact normalized inventory seed: " + err.Error()
			continue
		}
		if seed.ID == "" || seed.Kind == "" {
			row.StateResolveError = "exact normalized inventory seed omits id or kind"
			continue
		}
		row.Seed = append(json.RawMessage(nil), entry.Seed...)
		row.SeedID, row.SeedKind = seed.ID, seed.Kind
	}
	return nil
}

func rawHeaderCount(header map[string]json.RawMessage, name string) int {
	count, _ := rawIntField(header, name)
	return count
}

func builderFullUIInventorySourceSHA(header json.RawMessage) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(header, &fields); err != nil {
		return ""
	}
	for _, name := range []string{"inventorySha256", "inventorySHA256"} {
		var value string
		if err := json.Unmarshal(fields[name], &value); err == nil && value != "" {
			return value
		}
	}
	return ""
}

func rawObject(raw json.RawMessage) map[string]json.RawMessage {
	var value map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

func readBuilderFullUIObservations(path string) (map[string]builderFullUIUIObservation, string, error) {
	lines, artifactSHA, err := readBuilderFullUILines(path)
	if err != nil {
		return nil, "", err
	}
	observations := make(map[string]builderFullUIUIObservation, len(lines))
	start := 0
	var first map[string]json.RawMessage
	if err := json.Unmarshal(lines[0], &first); err != nil {
		return nil, "", fmt.Errorf("decode UI observation first row: %w", err)
	}
	var firstType string
	_ = json.Unmarshal(first["recordType"], &firstType)
	if firstType == "header" {
		if schemaVersion, ok := rawIntField(first, "schemaVersion"); !ok || schemaVersion != 1 {
			return nil, "", fmt.Errorf("UI observation header schemaVersion=%d present=%t, want 1", schemaVersion, ok)
		}
		start = 1
	}
	for lineIndex, raw := range lines[start:] {
		var ui builderFullUIUIObservation
		if err := json.Unmarshal(raw, &ui); err != nil {
			return nil, "", fmt.Errorf("decode UI observation row %d: %w", lineIndex+start+1, err)
		}
		ui.Raw = append(json.RawMessage(nil), raw...)
		if ui.RecordType != "ui-observation" || ui.SchemaVersion != 1 || ui.CaseID == "" || ui.EntryID == "" || ui.ActionIndex < 0 {
			return nil, "", fmt.Errorf("UI observation row %d has invalid record/key: %#v", lineIndex+start+1, ui)
		}
		key := builderFullUIKey{CaseID: ui.CaseID, EntryID: ui.EntryID, ActionIndex: ui.ActionIndex}.string()
		if _, exists := observations[key]; exists {
			return nil, "", fmt.Errorf("duplicate UI observation key %q/%q/%d", ui.CaseID, ui.EntryID, ui.ActionIndex)
		}
		observations[key] = ui
	}
	return observations, artifactSHA, nil
}

func readBuilderFullUILines(path string) ([][]byte, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, "", err
	}
	if len(content) == 0 {
		return nil, "", fmt.Errorf("artifact %s is empty", path)
	}
	lines := make([][]byte, 0)
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			return nil, "", fmt.Errorf("artifact %s contains a blank line at %d", path, lineNumber)
		}
		if !json.Valid(line) {
			return nil, "", fmt.Errorf("artifact %s line %d is not valid JSON", path, lineNumber)
		}
		lines = append(lines, append([]byte(nil), line...))
	}
	if err := scanner.Err(); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(content)
	return lines, hex.EncodeToString(sum[:]), nil
}

func writeBuilderFullUIObservations(path string, header builderFullUIObservationHeader, rows []builderFullUIObservation) error {
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	if err := encoder.Encode(header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return os.WriteFile(path, content.Bytes(), 0o600)
}
