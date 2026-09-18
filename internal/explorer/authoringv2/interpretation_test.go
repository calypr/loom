package authoringv2

import "testing"

func TestWorkspaceDigestIncludesExactInterpretationReference(t *testing.T) {
	workspace := interpretationWorkspace("revision-a")
	first, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	workspace = interpretationWorkspace("revision-b")
	second, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("interpretation reference did not change workspace digest: %q", first)
	}
}

func TestInterpretationReferenceHasOneCanonicalInlineState(t *testing.T) {
	workspace := interpretationWorkspace("revision-a")
	workspace.Documents[0].Columns[0].Interpretation = &FeatureInterpretation{Kind: "INLINE"}
	if err := workspace.Validate(); err == nil {
		t.Fatal("explicit INLINE interpretation duplicated the nil inline state")
	}
}

func interpretationWorkspace(revisionID string) Workspace {
	return Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Interpretations"},
		Documents: []Document{{
			Kind: Kind, Output: Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"},
			Columns: []Column{{
				Column: "patient_id", Label: "Patient ID", OccurrenceID: RootOccurrenceID,
				Source:         ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE"}},
				Interpretation: &FeatureInterpretation{Kind: FeatureInterpretationPinned, Pinned: &PinnedInterpretation{RevisionID: revisionID}},
			}},
		}},
		Tabs: []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
}
