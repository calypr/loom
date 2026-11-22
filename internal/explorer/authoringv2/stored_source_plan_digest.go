package authoringv2

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type legacyImportedSourcePlan struct {
	FieldBindings []ImportedSourceFieldBinding       `json:"fieldBindings,omitempty"`
	RuleAdditions []legacyImportedSourceRuleAddition `json:"ruleAdditions,omitempty"`
	Steps         []legacyImportedSourceStep         `json:"steps"`
	Relationships *ImportedRelationshipOverlay       `json:"relationships,omitempty"`
}

type legacyImportedSourceRuleAddition struct {
	Kind         string                    `json:"kind"`
	OccurrenceID string                    `json:"occurrenceId,omitempty"`
	StepID       string                    `json:"stepId"`
	Enabled      *bool                     `json:"enabled,omitempty"`
	Parameters   *ImportedSourceParameters `json:"parameters"`
	MaxColumns   *int                      `json:"maxColumns,omitempty"`
}

type legacyImportedSourceStep struct {
	Parameters   *ImportedSourceParameters `json:"parameters,omitempty"`
	ID           string                    `json:"id"`
	SourceKey    string                    `json:"sourceKey,omitempty"`
	OccurrenceID string                    `json:"occurrenceId,omitempty"`
	MatchMode    RouteMatchMode            `json:"matchMode,omitempty"`
	ParentID     string                    `json:"parentId,omitempty"`
	Kind         string                    `json:"kind"`
	Title        string                    `json:"title"`
	Summary      string                    `json:"summary"`
	Enabled      *bool                     `json:"enabled,omitempty"`
	Editable     bool                      `json:"editable"`
	MaxColumns   *int                      `json:"maxColumns,omitempty"`
	ValueMode    string                    `json:"valueMode,omitempty"`
}

func legacyImportedSourceDigest(raw []byte, workspace Workspace) (string, error) {
	var stored struct {
		Documents []struct {
			ImportedSource json.RawMessage `json:"importedSource"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return "", err
	}
	var sources map[int][]byte
	for index, document := range stored.Documents {
		if len(document.ImportedSource) == 0 {
			continue
		}
		var plan legacyImportedSourcePlan
		if err := strictDecode(document.ImportedSource, &plan); err != nil {
			continue
		}
		hasLegacyFlag := false
		for _, step := range plan.Steps {
			hasLegacyFlag = hasLegacyFlag || step.Enabled != nil
		}
		for _, addition := range plan.RuleAdditions {
			hasLegacyFlag = hasLegacyFlag || addition.Enabled != nil
		}
		if !hasLegacyFlag {
			continue
		}
		encoded, err := json.Marshal(plan)
		if err != nil {
			return "", err
		}
		if sources == nil {
			sources = make(map[int][]byte)
		}
		sources[index] = encoded
	}
	if len(sources) == 0 {
		return "", nil
	}
	canonical, err := CanonicalStoredWorkspaceForMigration(workspace)
	if err != nil {
		return "", err
	}
	start, end, err := legacyJSONFieldSpan(canonical, "documents")
	if err != nil {
		return "", err
	}
	var documents []json.RawMessage
	if err = json.Unmarshal(canonical[start:end], &documents); err != nil {
		return "", err
	}
	var output bytes.Buffer
	output.Write(canonical[:start])
	output.WriteByte('[')
	for index, document := range documents {
		if index > 0 {
			output.WriteByte(',')
		}
		source, ok := sources[index]
		if !ok {
			output.Write(document)
			continue
		}
		from, to, err := legacyJSONFieldSpan(document, "importedSource")
		if err != nil {
			return "", err
		}
		output.Write(document[:from])
		output.Write(source)
		output.Write(document[to:])
	}
	output.WriteByte(']')
	output.Write(canonical[end:])
	return workspaceDigest(output.Bytes()), nil
}

func ConvertStoredSourcePlan(raw []byte, expectedDigest string) (Workspace, bool, error) {
	workspace, err := DecodeCurrentWorkspace(raw)
	if err != nil {
		return Workspace{}, false, err
	}
	currentDigest, err := workspace.Digest()
	if err != nil {
		return Workspace{}, false, err
	}
	if currentDigest == expectedDigest {
		return workspace, false, nil
	}
	legacyDigest, err := legacyImportedSourceDigest(raw, workspace)
	if err != nil {
		return Workspace{}, false, err
	}
	if legacyDigest == "" || legacyDigest != expectedDigest {
		return Workspace{}, false, fmt.Errorf("stored workspace canonical digest mismatch")
	}
	return workspace, true, nil
}
