package authoringv2

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestDocumentValidatesAllRowDefinitionVariants(t *testing.T) {
	variants := []struct {
		name string
		rows ws.RowDefinition
	}{
		{
			name: "records",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionRecords, Records: &ws.RecordRows{}},
		},
		{
			name: "field groups",
			rows: ws.RowDefinition{
				Kind: ws.RowDefinitionGroups,
				Groups: &ws.GroupedRows{
					Source: ws.GroupSource{Kind: ws.GroupSourceField, Field: &ws.FieldGroupSource{
						OccurrenceID: ws.RootOccurrenceID, FieldPath: "active", MissingKeyPolicy: ws.MissingKeyGroupAsMissing,
					}},
				},
			},
		},
		{
			name: "explicit groups",
			rows: ws.RowDefinition{
				Kind: ws.RowDefinitionGroups,
				Groups: &ws.GroupedRows{
					Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{
						RevisionID: "group-revision-1", UnassignedMemberPolicy: ws.UnassignedMemberGroupAsUnassigned,
					}},
				},
			},
		},
		{
			name: "expanded",
			rows: ws.RowDefinition{
				Kind:     ws.RowDefinitionExpanded,
				Expanded: &ws.ExpandedRows{OccurrenceID: ws.RootOccurrenceID, ScopePath: "name[]", EmptyCollectionPolicy: ws.EmptyCollectionPreserveParent},
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
	validGroups := &ws.GroupedRows{
		Source: ws.GroupSource{Kind: ws.GroupSourceField, Field: &ws.FieldGroupSource{
			OccurrenceID: ws.RootOccurrenceID, FieldPath: "active", MissingKeyPolicy: ws.MissingKeyError,
		}},
	}
	tests := []struct {
		name string
		rows ws.RowDefinition
	}{
		{name: "records discriminator without payload", rows: ws.RowDefinition{Kind: ws.RowDefinitionRecords}},
		{name: "records with groups payload", rows: ws.RowDefinition{Kind: ws.RowDefinitionRecords, Groups: validGroups}},
		{name: "groups without payload", rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups}},
		{name: "expanded discriminator with records payload", rows: ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Records: &ws.RecordRows{}}},
		{name: "multiple matching payloads", rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: validGroups, Records: &ws.RecordRows{}}},
		{name: "unknown discriminator", rows: ws.RowDefinition{Kind: ws.RowDefinitionKind("PIPELINE"), Records: &ws.RecordRows{}}},
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
		rows ws.RowDefinition
	}{
		{
			name: "field group missing missing-key policy",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
				Source: ws.GroupSource{Kind: ws.GroupSourceField, Field: &ws.FieldGroupSource{OccurrenceID: ws.RootOccurrenceID, FieldPath: "active"}},
			}},
		},
		{
			name: "field group unknown missing-key policy",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
				Source: ws.GroupSource{Kind: ws.GroupSourceField, Field: &ws.FieldGroupSource{
					OccurrenceID: ws.RootOccurrenceID, FieldPath: "active", MissingKeyPolicy: ws.MissingKeyPolicy("SILENT_DEFAULT"),
				}},
			}},
		},
		{
			name: "explicit group missing unassigned-member policy",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
				Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{RevisionID: "group-revision-1"}},
			}},
		},
		{
			name: "explicit group unknown unassigned-member policy",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
				Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{
					RevisionID: "group-revision-1", UnassignedMemberPolicy: ws.UnassignedMemberPolicy("SILENT_DEFAULT"),
				}},
			}},
		},
		{
			name: "field group missing occurrence",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
				Source: ws.GroupSource{Kind: ws.GroupSourceField, Field: &ws.FieldGroupSource{FieldPath: "active", MissingKeyPolicy: ws.MissingKeyError}},
			}},
		},
		{
			name: "field group missing path",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
				Source: ws.GroupSource{Kind: ws.GroupSourceField, Field: &ws.FieldGroupSource{OccurrenceID: ws.RootOccurrenceID, MissingKeyPolicy: ws.MissingKeyError}},
			}},
		},
		{
			name: "explicit group missing revision identity",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
				Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{UnassignedMemberPolicy: ws.UnassignedMemberError}},
			}},
		},
		{
			name: "field group occurrence has whitespace alias",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
				Source: ws.GroupSource{Kind: ws.GroupSourceField, Field: &ws.FieldGroupSource{
					OccurrenceID: " base", FieldPath: "active", MissingKeyPolicy: ws.MissingKeyError,
				}},
			}},
		},
		{
			name: "field group path has whitespace alias",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
				Source: ws.GroupSource{Kind: ws.GroupSourceField, Field: &ws.FieldGroupSource{
					OccurrenceID: ws.RootOccurrenceID, FieldPath: "active ", MissingKeyPolicy: ws.MissingKeyError,
				}},
			}},
		},
		{
			name: "explicit group revision has whitespace alias",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
				Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{
					RevisionID: " group-revision-1", UnassignedMemberPolicy: ws.UnassignedMemberError,
				}},
			}},
		},
		{
			name: "expanded missing occurrence",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{
				ScopePath: "name[]", EmptyCollectionPolicy: ws.EmptyCollectionError,
			}},
		},
		{
			name: "expanded missing scope path",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{
				OccurrenceID: ws.RootOccurrenceID, EmptyCollectionPolicy: ws.EmptyCollectionError,
			}},
		},
		{
			name: "expanded occurrence has whitespace alias",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{
				OccurrenceID: "base ", ScopePath: "name[]", EmptyCollectionPolicy: ws.EmptyCollectionError,
			}},
		},
		{
			name: "expanded scope path has whitespace alias",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{
				OccurrenceID: ws.RootOccurrenceID, ScopePath: " name[]", EmptyCollectionPolicy: ws.EmptyCollectionError,
			}},
		},
		{
			name: "expanded missing empty-collection policy",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{
				OccurrenceID: ws.RootOccurrenceID, ScopePath: "name[]",
			}},
		},
		{
			name: "expanded unknown empty-collection policy",
			rows: ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{
				OccurrenceID: ws.RootOccurrenceID, ScopePath: "name[]", EmptyCollectionPolicy: ws.EmptyCollectionPolicy("DROP_SILENTLY"),
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
			if _, err := ws.DecodeWorkspace(persistedWorkspaceWithRows(test.rows)); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("cross-arm policy error = %v, want strict unknown-field rejection", err)
			}
		})
	}
}

func TestDecodeWorkspaceDefaultsMissingRowsBeforeValidatingPreV8PersistedDocument(t *testing.T) {
	workspace, err := ws.DecodeStoredWorkspaceForMigration(persistedWorkspaceWithoutRows(6))
	if err != nil {
		t.Fatalf("pre-v8 workspace did not migrate before validation: %v", err)
	}
	if len(workspace.Documents) != 1 || workspace.Documents[0].Rows.Kind != ws.RowDefinitionRecords || workspace.Documents[0].Rows.Records == nil {
		t.Fatalf("migrated row definition = %#v", workspace.Documents)
	}
}

func TestDecodeWorkspaceRejectsMissingRowsAtCurrentSemanticsVersion(t *testing.T) {
	if _, err := ws.DecodeWorkspace(persistedWorkspaceWithoutRows(ws.CurrentSemanticsVersion)); err == nil || !strings.Contains(err.Error(), "rows") {
		t.Fatalf("current-version missing rows error = %v", err)
	}
}

func TestCanonicalRoundTripIncludesRowsAndKeepsDigestStable(t *testing.T) {
	workspace := rowDefinitionTestWorkspace(ws.RowDefinition{Kind: ws.RowDefinitionRecords, Records: &ws.RecordRows{}})
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"rows":{"kind":"RECORDS","records":{}}`) {
		t.Fatalf("canonical JSON omitted the explicit rows payload: %s", raw)
	}
	decoded, err := ws.DecodeWorkspace(raw)
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
	legacy := rowDefinitionTestWorkspace(ws.RowDefinition{})
	legacy.SemanticsVersion = explicitRowsSemanticsVersion - 1
	original := legacy
	first := ws.MigrateLosslessDefaults(legacy, ws.CatalogSnapshot{})
	if first.SemanticsVersion != ws.CurrentSemanticsVersion || first.Documents[0].Rows.Kind != ws.RowDefinitionRecords || first.Documents[0].Rows.Records == nil {
		t.Fatalf("migration did not install records before advancing the version: %#v", first)
	}
	second := ws.MigrateLosslessDefaults(first, ws.CatalogSnapshot{})
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("row migration was not idempotent:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if !reflect.DeepEqual(original, legacy) || legacy.SemanticsVersion != explicitRowsSemanticsVersion-1 || legacy.Documents[0].Rows != (ws.RowDefinition{}) {
		t.Fatal("row migration mutated its input")
	}
}

func TestLegacySourceWireMigrationPreservesRows(t *testing.T) {
	raw := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","semanticsVersion":` + strconv.Itoa(ws.CurrentSemanticsVersion) + `,"explorer":{"title":"Persisted"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"rows":{"kind":"EXPANDED","expanded":{"occurrenceId":"base","scopePath":"name[]","emptyCollectionPolicy":"PRESERVE_PARENT"}},"columns":[{"column":"names","label":"Names","occurrenceId":"base","source":{"kind":"field","fieldPath":"name[].family","projectionMode":"FIRST"}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	workspace, err := ws.DecodeStoredWorkspaceForMigration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	want := ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{OccurrenceID: ws.RootOccurrenceID, ScopePath: "name[]", EmptyCollectionPolicy: ws.EmptyCollectionPreserveParent}}
	if !reflect.DeepEqual(workspace.Documents[0].Rows, want) {
		t.Fatalf("legacy source migration changed rows: got %#v want %#v", workspace.Documents[0].Rows, want)
	}
}

func TestCreateTableStartsWithRecordsRows(t *testing.T) {
	workspace, _, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "create-rows", []Command{{Type: CommandCreateTable, Title: "Patients", Payload: &TablePayload{RootNodeID: "patient"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Documents) != 1 || workspace.Documents[0].Rows.Kind != ws.RowDefinitionRecords || workspace.Documents[0].Rows.Records == nil {
		t.Fatalf("created document rows = %#v", workspace.Documents)
	}
}

func rowDefinitionTestDocument(rows ws.RowDefinition) ws.Document {
	return ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Rows: rows, Columns: []ws.Column{},
	}
}

func rowDefinitionTestWorkspace(rows ws.RowDefinition) ws.Workspace {
	document := workspaceDocument("patients")
	document.Rows = rows
	return ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion,
		Explorer: ws.ExplorerMetadata{Title: "Builder"}, Documents: []ws.Document{document},
		Tabs: []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
}

func persistedWorkspaceWithoutRows(semanticsVersion int) []byte {
	return []byte(`{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","semanticsVersion":` + strconv.Itoa(semanticsVersion) + `,"explorer":{"title":"Persisted"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`)
}

func persistedWorkspaceWithRows(rows string) []byte {
	return []byte(`{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","semanticsVersion":` + strconv.Itoa(ws.CurrentSemanticsVersion) + `,"explorer":{"title":"Persisted"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"rows":` + rows + `,"columns":[]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`)
}
