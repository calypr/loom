package authoringv2

import (
	"fmt"
	"strings"
)

type RowDefinitionKind string

const (
	RowDefinitionRecords  RowDefinitionKind = "RECORDS"
	RowDefinitionGroups   RowDefinitionKind = "GROUPS"
	RowDefinitionExpanded RowDefinitionKind = "EXPANDED"
)

// RowDefinition is a closed persisted union. Exactly one payload must match
// Kind; future composition semantics require a separate explicit variant.
type RowDefinition struct {
	Kind     RowDefinitionKind `json:"kind"`
	Records  *RecordRows       `json:"records,omitempty"`
	Groups   *GroupedRows      `json:"groups,omitempty"`
	Expanded *ExpandedRows     `json:"expanded,omitempty"`
}

type RecordRows struct{}

type GroupedRows struct {
	Source GroupSource `json:"source"`
}

type GroupSourceKind string

const (
	GroupSourceField    GroupSourceKind = "FIELD"
	GroupSourceExplicit GroupSourceKind = "EXPLICIT"
)

// GroupSource is a closed union between a field key and an immutable group
// revision reference.
type GroupSource struct {
	Kind     GroupSourceKind      `json:"kind"`
	Field    *FieldGroupSource    `json:"field,omitempty"`
	Explicit *ExplicitGroupSource `json:"explicit,omitempty"`
}

type FieldGroupSource struct {
	OccurrenceID     string           `json:"occurrenceId"`
	FieldPath        string           `json:"fieldPath"`
	MissingKeyPolicy MissingKeyPolicy `json:"missingKeyPolicy"`
}

// ExplicitGroupSource references a revision whose declared groups remain
// present even when empty. Its policy applies to root members in no group.
type ExplicitGroupSource struct {
	RevisionID             string                 `json:"revisionId"`
	UnassignedMemberPolicy UnassignedMemberPolicy `json:"unassignedMemberPolicy"`
}

type MissingKeyPolicy string

const (
	MissingKeyError          MissingKeyPolicy = "ERROR"
	MissingKeyExclude        MissingKeyPolicy = "EXCLUDE"
	MissingKeyGroupAsMissing MissingKeyPolicy = "GROUP_AS_MISSING"
)

type UnassignedMemberPolicy string

const (
	UnassignedMemberError             UnassignedMemberPolicy = "ERROR"
	UnassignedMemberExclude           UnassignedMemberPolicy = "EXCLUDE"
	UnassignedMemberGroupAsUnassigned UnassignedMemberPolicy = "GROUP_AS_UNASSIGNED"
)

type ExpandedRows struct {
	OccurrenceID          string                `json:"occurrenceId"`
	ScopePath             string                `json:"scopePath"`
	EmptyCollectionPolicy EmptyCollectionPolicy `json:"emptyCollectionPolicy"`
}

type EmptyCollectionPolicy string

const (
	EmptyCollectionError          EmptyCollectionPolicy = "ERROR"
	EmptyCollectionExclude        EmptyCollectionPolicy = "EXCLUDE"
	EmptyCollectionPreserveParent EmptyCollectionPolicy = "PRESERVE_PARENT"
)

func (r RowDefinition) Validate() error {
	payloads := 0
	if r.Records != nil {
		payloads++
	}
	if r.Groups != nil {
		payloads++
	}
	if r.Expanded != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("rows must contain exactly one payload matching kind")
	}

	switch r.Kind {
	case RowDefinitionRecords:
		if r.Records == nil {
			return fmt.Errorf("rows.records is required for kind %q", r.Kind)
		}
	case RowDefinitionGroups:
		if r.Groups == nil {
			return fmt.Errorf("rows.groups is required for kind %q", r.Kind)
		}
		if err := r.Groups.validate(); err != nil {
			return err
		}
	case RowDefinitionExpanded:
		if r.Expanded == nil {
			return fmt.Errorf("rows.expanded is required for kind %q", r.Kind)
		}
		if err := r.Expanded.validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("rows.kind %q is unsupported", r.Kind)
	}
	return nil
}

func (g GroupedRows) validate() error {
	return g.Source.validate()
}

func (s GroupSource) validate() error {
	payloads := 0
	if s.Field != nil {
		payloads++
	}
	if s.Explicit != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("must contain exactly one payload matching kind")
	}
	switch s.Kind {
	case GroupSourceField:
		if s.Field == nil {
			return fmt.Errorf("field payload is required for kind %q", s.Kind)
		}
		return s.Field.validate()
	case GroupSourceExplicit:
		if s.Explicit == nil {
			return fmt.Errorf("explicit payload is required for kind %q", s.Kind)
		}
		return s.Explicit.validate()
	default:
		return fmt.Errorf("kind %q is unsupported", s.Kind)
	}
}

func (f FieldGroupSource) validate() error {
	if err := requireTrimmedIdentity(f.OccurrenceID, "occurrenceId"); err != nil {
		return fmt.Errorf("field source: %w", err)
	}
	if err := requireTrimmedIdentity(f.FieldPath, "fieldPath"); err != nil {
		return fmt.Errorf("field source: %w", err)
	}
	switch f.MissingKeyPolicy {
	case MissingKeyError, MissingKeyExclude, MissingKeyGroupAsMissing:
		return nil
	default:
		return fmt.Errorf("field source missingKeyPolicy must be ERROR, EXCLUDE, or GROUP_AS_MISSING")
	}
}

func (e ExplicitGroupSource) validate() error {
	if err := requireTrimmedIdentity(e.RevisionID, "revisionId"); err != nil {
		return fmt.Errorf("explicit source: %w", err)
	}
	switch e.UnassignedMemberPolicy {
	case UnassignedMemberError, UnassignedMemberExclude, UnassignedMemberGroupAsUnassigned:
		return nil
	default:
		return fmt.Errorf("explicit source unassignedMemberPolicy must be ERROR, EXCLUDE, or GROUP_AS_UNASSIGNED")
	}
}

func (e ExpandedRows) validate() error {
	if err := requireTrimmedIdentity(e.OccurrenceID, "occurrenceId"); err != nil {
		return fmt.Errorf("rows.expanded: %w", err)
	}
	if err := requireTrimmedIdentity(e.ScopePath, "scopePath"); err != nil {
		return fmt.Errorf("rows.expanded: %w", err)
	}
	switch e.EmptyCollectionPolicy {
	case EmptyCollectionError, EmptyCollectionExclude, EmptyCollectionPreserveParent:
		return nil
	default:
		return fmt.Errorf("rows.expanded.emptyCollectionPolicy must be ERROR, EXCLUDE, or PRESERVE_PARENT")
	}
}

func requireTrimmedIdentity(value, name string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s is required", name)
	}
	if value != trimmed {
		return fmt.Errorf("%s must equal its trimmed value", name)
	}
	return nil
}

// RecordsRowDefinition returns the default row definition for one row per root record.
func RecordsRowDefinition() RowDefinition {
	return RowDefinition{Kind: RowDefinitionRecords, Records: &RecordRows{}}
}

func missingRowDefinition(rows RowDefinition) bool {
	return rows.Kind == "" && rows.Records == nil && rows.Groups == nil && rows.Expanded == nil
}

// migratePreV7MissingRows installs the historical root-record behavior only
// across the semantics-version boundary. It copies the document slice before
// editing and deliberately leaves the version bump to its migration owner.
func migratePreV7MissingRows(workspace Workspace) Workspace {
	if workspace.SemanticsVersion >= CurrentSemanticsVersion {
		return workspace
	}
	needsMigration := false
	for _, document := range workspace.Documents {
		if missingRowDefinition(document.Rows) {
			needsMigration = true
			break
		}
	}
	if !needsMigration {
		return workspace
	}
	workspace.Documents = append([]Document(nil), workspace.Documents...)
	for index := range workspace.Documents {
		if missingRowDefinition(workspace.Documents[index].Rows) {
			workspace.Documents[index].Rows = RecordsRowDefinition()
		}
	}
	return workspace
}
