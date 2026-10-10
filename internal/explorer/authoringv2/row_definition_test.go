package authoringv2

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestDocumentValidatesAllRowDefinitionVariants(t *testing.T) {
	variants := []struct {
		name string
		rows RowDefinition
	}{
		{
			name: "records",
			rows: RowDefinition{Kind: RowDefinitionRecords, Records: &RecordRows{}},
		},
		{
			name: "field groups",
			rows: RowDefinition{
				Kind: RowDefinitionGroups,
				Groups: &GroupedRows{
					Source: GroupSource{Kind: GroupSourceField, Field: &FieldGroupSource{
						OccurrenceID: RootOccurrenceID, FieldPath: "active", MissingKeyPolicy: MissingKeyGroupAsMissing,
					}},
				},
			},
		},
		{
			name: "explicit groups",
			rows: RowDefinition{
				Kind: RowDefinitionGroups,
				Groups: &GroupedRows{
					Source: GroupSource{Kind: GroupSourceExplicit, Explicit: &ExplicitGroupSource{
						RevisionID: "group-revision-1", UnassignedMemberPolicy: UnassignedMemberGroupAsUnassigned,
					}},
				},
			},
		},
		{
			name: "expanded",
			rows: RowDefinition{
				Kind:     RowDefinitionExpanded,
				Expanded: &ExpandedRows{OccurrenceID: RootOccurrenceID, ScopePath: "name[]", EmptyCollectionPolicy: EmptyCollectionPreserveParent},
			},
		},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			if err := rowDefinitionTestDocument(variant.rows).Validate(); err != nil {
				t.Fatalf("valid row definition rejected: %v", err)
			}
		})
	}
}

func TestRowDefinitionRejectsDiscriminatorPayloadMismatchesAndMultiplePayloads(t *testing.T) {
	validGroups := &GroupedRows{
		Source: GroupSource{Kind: GroupSourceField, Field: &FieldGroupSource{
			OccurrenceID: RootOccurrenceID, FieldPath: "active", MissingKeyPolicy: MissingKeyError,
		}},
	}
	tests := []struct {
		name string
		rows RowDefinition
	}{
		{name: "records discriminator without payload", rows: RowDefinition{Kind: RowDefinitionRecords}},
		{name: "records with groups payload", rows: RowDefinition{Kind: RowDefinitionRecords, Groups: validGroups}},
		{name: "groups without payload", rows: RowDefinition{Kind: RowDefinitionGroups}},
		{name: "expanded discriminator with records payload", rows: RowDefinition{Kind: RowDefinitionExpanded, Records: &RecordRows{}}},
		{name: "multiple matching payloads", rows: RowDefinition{Kind: RowDefinitionGroups, Groups: validGroups, Records: &RecordRows{}}},
		{name: "unknown discriminator", rows: RowDefinition{Kind: RowDefinitionKind("PIPELINE"), Records: &RecordRows{}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.rows.Validate(); err == nil {
				t.Fatal("invalid row-definition union was accepted")
			}
		})
	}
}

func TestRowDefinitionRejectsInvalidPoliciesAndEmptyIdentities(t *testing.T) {
	tests := []struct {
		name string
		rows RowDefinition
	}{
		{
			name: "field group missing missing-key policy",
			rows: RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
				Source: GroupSource{Kind: GroupSourceField, Field: &FieldGroupSource{OccurrenceID: RootOccurrenceID, FieldPath: "active"}},
			}},
		},
		{
			name: "field group unknown missing-key policy",
			rows: RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
				Source: GroupSource{Kind: GroupSourceField, Field: &FieldGroupSource{
					OccurrenceID: RootOccurrenceID, FieldPath: "active", MissingKeyPolicy: MissingKeyPolicy("SILENT_DEFAULT"),
				}},
			}},
		},
		{
			name: "explicit group missing unassigned-member policy",
			rows: RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
				Source: GroupSource{Kind: GroupSourceExplicit, Explicit: &ExplicitGroupSource{RevisionID: "group-revision-1"}},
			}},
		},
		{
			name: "explicit group unknown unassigned-member policy",
			rows: RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
				Source: GroupSource{Kind: GroupSourceExplicit, Explicit: &ExplicitGroupSource{
					RevisionID: "group-revision-1", UnassignedMemberPolicy: UnassignedMemberPolicy("SILENT_DEFAULT"),
				}},
			}},
		},
		{
			name: "field group missing occurrence",
			rows: RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
				Source: GroupSource{Kind: GroupSourceField, Field: &FieldGroupSource{FieldPath: "active", MissingKeyPolicy: MissingKeyError}},
			}},
		},
		{
			name: "field group missing path",
			rows: RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
				Source: GroupSource{Kind: GroupSourceField, Field: &FieldGroupSource{OccurrenceID: RootOccurrenceID, MissingKeyPolicy: MissingKeyError}},
			}},
		},
		{
			name: "explicit group missing revision identity",
			rows: RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
				Source: GroupSource{Kind: GroupSourceExplicit, Explicit: &ExplicitGroupSource{UnassignedMemberPolicy: UnassignedMemberError}},
			}},
		},
		{
			name: "field group occurrence has whitespace alias",
			rows: RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
				Source: GroupSource{Kind: GroupSourceField, Field: &FieldGroupSource{
					OccurrenceID: " base", FieldPath: "active", MissingKeyPolicy: MissingKeyError,
				}},
			}},
		},
		{
			name: "field group path has whitespace alias",
			rows: RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
				Source: GroupSource{Kind: GroupSourceField, Field: &FieldGroupSource{
					OccurrenceID: RootOccurrenceID, FieldPath: "active ", MissingKeyPolicy: MissingKeyError,
				}},
			}},
		},
		{
			name: "explicit group revision has whitespace alias",
			rows: RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
				Source: GroupSource{Kind: GroupSourceExplicit, Explicit: &ExplicitGroupSource{
					RevisionID: " group-revision-1", UnassignedMemberPolicy: UnassignedMemberError,
				}},
			}},
		},
		{
			name: "expanded missing occurrence",
			rows: RowDefinition{Kind: RowDefinitionExpanded, Expanded: &ExpandedRows{
				ScopePath: "name[]", EmptyCollectionPolicy: EmptyCollectionError,
			}},
		},
		{
			name: "expanded missing scope path",
			rows: RowDefinition{Kind: RowDefinitionExpanded, Expanded: &ExpandedRows{
				OccurrenceID: RootOccurrenceID, EmptyCollectionPolicy: EmptyCollectionError,
			}},
		},
		{
			name: "expanded occurrence has whitespace alias",
			rows: RowDefinition{Kind: RowDefinitionExpanded, Expanded: &ExpandedRows{
				OccurrenceID: "base ", ScopePath: "name[]", EmptyCollectionPolicy: EmptyCollectionError,
			}},
		},
		{
			name: "expanded scope path has whitespace alias",
			rows: RowDefinition{Kind: RowDefinitionExpanded, Expanded: &ExpandedRows{
				OccurrenceID: RootOccurrenceID, ScopePath: " name[]", EmptyCollectionPolicy: EmptyCollectionError,
			}},
		},
		{
			name: "expanded missing empty-collection policy",
			rows: RowDefinition{Kind: RowDefinitionExpanded, Expanded: &ExpandedRows{
				OccurrenceID: RootOccurrenceID, ScopePath: "name[]",
			}},
		},
		{
			name: "expanded unknown empty-collection policy",
			rows: RowDefinition{Kind: RowDefinitionExpanded, Expanded: &ExpandedRows{
				OccurrenceID: RootOccurrenceID, ScopePath: "name[]", EmptyCollectionPolicy: EmptyCollectionPolicy("DROP_SILENTLY"),
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.rows.Validate(); err == nil {
				t.Fatal("invalid policy or empty row identity was accepted")
			}
		})
	}
}

func TestDecodeWorkspaceRejectsPoliciesOnTheWrongGroupSourceArm(t *testing.T) {
	tests := []struct {
		name string
		rows string
	}{
		{
			name: "field source cannot carry unassigned-member policy",
			rows: `{"kind":"GROUPS","groups":{"source":{"kind":"FIELD","field":{"occurrenceId":"base","fieldPath":"active","missingKeyPolicy":"ERROR","unassignedMemberPolicy":"EXCLUDE"}}}}`,
		},
		{
			name: "explicit source cannot carry missing-key policy",
			rows: `{"kind":"GROUPS","groups":{"source":{"kind":"EXPLICIT","explicit":{"revisionId":"revision-1","unassignedMemberPolicy":"ERROR","missingKeyPolicy":"EXCLUDE"}}}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeWorkspace(persistedWorkspaceWithRows(test.rows)); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("cross-arm policy error = %v, want strict unknown-field rejection", err)
			}
		})
	}
}

func TestDecodeWorkspaceDefaultsMissingRowsBeforeValidatingPreV8PersistedDocument(t *testing.T) {
	workspace, err := DecodeWorkspace(persistedWorkspaceWithoutRows(6))
	if err != nil {
		t.Fatalf("pre-v8 workspace did not migrate before validation: %v", err)
	}
	if len(workspace.Documents) != 1 || workspace.Documents[0].Rows.Kind != RowDefinitionRecords || workspace.Documents[0].Rows.Records == nil {
		t.Fatalf("migrated row definition = %#v", workspace.Documents)
	}
}

func TestDecodeWorkspaceRejectsMissingRowsAtCurrentSemanticsVersion(t *testing.T) {
	if _, err := DecodeWorkspace(persistedWorkspaceWithoutRows(CurrentSemanticsVersion)); err == nil || !strings.Contains(err.Error(), "rows") {
		t.Fatalf("current-version missing rows error = %v", err)
	}
}

func TestCanonicalRoundTripIncludesRowsAndKeepsDigestStable(t *testing.T) {
	workspace := rowDefinitionTestWorkspace(RowDefinition{Kind: RowDefinitionRecords, Records: &RecordRows{}})
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"rows":{"kind":"RECORDS","records":{}}`) {
		t.Fatalf("canonical JSON omitted the explicit rows payload: %s", raw)
	}
	decoded, err := DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := decoded.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("round-trip digest changed: %s != %s", firstDigest, secondDigest)
	}
}

func TestMigrateLosslessDefaultsInstallsRowsIdempotentlyWithoutMutatingInput(t *testing.T) {
	legacy := rowDefinitionTestWorkspace(RowDefinition{})
	legacy.SemanticsVersion = explicitRowsSemanticsVersion - 1
	original := legacy
	first := MigrateLosslessDefaults(legacy, CatalogSnapshot{})
	if first.SemanticsVersion != CurrentSemanticsVersion || first.Documents[0].Rows.Kind != RowDefinitionRecords || first.Documents[0].Rows.Records == nil {
		t.Fatalf("migration did not install records before advancing the version: %#v", first)
	}
	second := MigrateLosslessDefaults(first, CatalogSnapshot{})
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("row migration was not idempotent:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if !reflect.DeepEqual(original, legacy) || legacy.SemanticsVersion != explicitRowsSemanticsVersion-1 || legacy.Documents[0].Rows != (RowDefinition{}) {
		t.Fatal("row migration mutated its input")
	}
}

func TestLegacySourceWireMigrationPreservesRows(t *testing.T) {
	raw := `{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","semanticsVersion":` + strconv.Itoa(CurrentSemanticsVersion) + `,"explorer":{"title":"Persisted"},"documents":[{"kind":"` + Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"rows":{"kind":"EXPANDED","expanded":{"occurrenceId":"base","scopePath":"name[]","emptyCollectionPolicy":"PRESERVE_PARENT"}},"columns":[{"column":"names","label":"Names","occurrenceId":"base","source":{"kind":"field","fieldPath":"name[].family","projectionMode":"FIRST"}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	workspace, err := DecodeWorkspace([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	want := RowDefinition{Kind: RowDefinitionExpanded, Expanded: &ExpandedRows{OccurrenceID: RootOccurrenceID, ScopePath: "name[]", EmptyCollectionPolicy: EmptyCollectionPreserveParent}}
	if !reflect.DeepEqual(workspace.Documents[0].Rows, want) {
		t.Fatalf("legacy source migration changed rows: got %#v want %#v", workspace.Documents[0].Rows, want)
	}
}

func TestCreateTableStartsWithRecordsRows(t *testing.T) {
	workspace, _, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "create-rows", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Documents) != 1 || workspace.Documents[0].Rows.Kind != RowDefinitionRecords || workspace.Documents[0].Rows.Records == nil {
		t.Fatalf("created document rows = %#v", workspace.Documents)
	}
}

func rowDefinitionTestDocument(rows RowDefinition) Document {
	return Document{
		Kind: Kind, Output: Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"}, Rows: rows, Columns: []Column{},
	}
}

func rowDefinitionTestWorkspace(rows RowDefinition) Workspace {
	document := workspaceDocument("patients")
	document.Rows = rows
	return Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, SemanticsVersion: CurrentSemanticsVersion,
		Explorer: ExplorerMetadata{Title: "Builder"}, Documents: []Document{document},
		Tabs: []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
}

func persistedWorkspaceWithoutRows(semanticsVersion int) []byte {
	return []byte(`{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","semanticsVersion":` + strconv.Itoa(semanticsVersion) + `,"explorer":{"title":"Persisted"},"documents":[{"kind":"` + Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`)
}

func persistedWorkspaceWithRows(rows string) []byte {
	return []byte(`{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","semanticsVersion":` + strconv.Itoa(CurrentSemanticsVersion) + `,"explorer":{"title":"Persisted"},"documents":[{"kind":"` + Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"rows":` + rows + `,"columns":[]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`)
}
