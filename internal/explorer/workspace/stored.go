package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

func (w Workspace) LegacyNilConstructionStepsDigest() (string, error) {
	legacyStepLists := 0
	for _, document := range w.Documents {
		if document.Construction != nil && document.Construction.Steps == nil {
			legacyStepLists++
		}
	}
	if legacyStepLists == 0 {
		return "", nil
	}
	raw, err := CanonicalStoredWorkspaceForMigration(w)
	if err != nil {
		return "", err
	}
	documentsStart, documentsEnd, err := legacyJSONFieldSpan(raw, "documents")
	if err != nil {
		return "", err
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(raw[documentsStart:documentsEnd], &documents); err != nil {
		return "", err
	}
	type replacement struct{ start, end int }
	replacements := make([]replacement, 0, legacyStepLists)
	cursor := documentsStart + 1
	for index, document := range documents {
		relative := bytes.Index(raw[cursor:documentsEnd], document)
		if relative < 0 {
			return "", fmt.Errorf("canonical workspace omitted document %d", index)
		}
		documentStart := cursor + relative
		cursor = documentStart + len(document)
		construction := w.Documents[index].Construction
		if construction == nil || construction.Steps != nil {
			continue
		}
		constructionStart, constructionEnd, err := legacyJSONFieldSpan(document, "construction")
		if err != nil {
			return "", err
		}
		stepsStart, stepsEnd, err := legacyJSONFieldSpan(document[constructionStart:constructionEnd], "steps")
		if err != nil {
			return "", err
		}
		start, end := documentStart+constructionStart+stepsStart, documentStart+constructionStart+stepsEnd
		if !bytes.Equal(raw[start:end], []byte("[]")) {
			return "", fmt.Errorf("canonical workspace omitted an empty construction step list")
		}
		replacements = append(replacements, replacement{start, end})
	}
	for index := len(replacements) - 1; index >= 0; index-- {
		span := replacements[index]
		changed := make([]byte, 0, len(raw)+2)
		changed = append(changed, raw[:span.start]...)
		changed = append(changed, "null"...)
		changed = append(changed, raw[span.end:]...)
		raw = changed
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
