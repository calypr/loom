package compilation

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// SourceRowDescriptor identifies the physical output column that preserves a
// root resource identity. It is intentionally transport-neutral so lifecycle
// and publication can build their source-row descriptor without importing the
// authoring compiler or guessing from labels and internal row IDs.
type SourceRowDescriptor struct {
	ResourceType   string
	PhysicalColumn string
}

// ResolveAddressableSourceRow proves that one validated workspace output can
// map rows back to source resources. The proof is deliberately narrow:
// resource-grain output, root occurrence, direct id field, VALUE projection,
// and one emitted physical column with the same source provenance.
func ResolveAddressableSourceRow(workspace authoringv2.Workspace, contract explorer.PublicOutputContract, emitted []explorer.EmittedColumn) (SourceRowDescriptor, error) {
	if err := workspace.Validate(); err != nil {
		return SourceRowDescriptor{}, fmt.Errorf("authoring workspace: %w", err)
	}
	for _, document := range workspace.Documents {
		if document.Output.ID == contract.OutputID {
			return resolveAddressableDocumentSourceRow(document, contract, emitted)
		}
	}
	return SourceRowDescriptor{}, fmt.Errorf("authoring workspace has no output %q", contract.OutputID)
}

func resolveAddressableDocumentSourceRow(document authoringv2.Document, contract explorer.PublicOutputContract, emitted []explorer.EmittedColumn) (SourceRowDescriptor, error) {
	if err := document.Validate(); err != nil {
		return SourceRowDescriptor{}, fmt.Errorf("authoring document: %w", err)
	}
	if strings.TrimSpace(document.RootResourceType) == "" {
		return SourceRowDescriptor{}, fmt.Errorf("root resource type is required")
	}
	if contract.OutputID != document.Output.ID {
		return SourceRowDescriptor{}, fmt.Errorf("output contract %q does not match document %q", contract.OutputID, document.Output.ID)
	}
	if contract.RootResourceType != document.RootResourceType {
		return SourceRowDescriptor{}, fmt.Errorf("output contract root resource type %q does not match document %q", contract.RootResourceType, document.RootResourceType)
	}
	if contract.RowGrain != string(spec.RowGrainResource) {
		return SourceRowDescriptor{}, fmt.Errorf("source row requires resource grain, got %q", contract.RowGrain)
	}
	var source authoringv2.Column
	found := false
	for _, column := range document.Columns {
		if column.OccurrenceID != authoringv2.RootOccurrenceID || column.Source.Kind != authoringv2.SourceField || column.Source.Field == nil {
			continue
		}
		path := strings.Trim(strings.TrimSpace(column.Source.Field.Path), ".")
		mode := strings.ToUpper(strings.TrimSpace(column.Source.Field.ProjectionMode))
		if path != "id" || mode != "VALUE" {
			continue
		}
		if found {
			return SourceRowDescriptor{}, fmt.Errorf("multiple root id VALUE source columns are not addressable")
		}
		source, found = column, true
	}
	if !found {
		return SourceRowDescriptor{}, fmt.Errorf("addressable source row requires root direct id VALUE field")
	}
	var physical string
	for _, column := range emitted {
		if column.OutputID != document.Output.ID || column.OccurrenceID != authoringv2.RootOccurrenceID || column.ProjectionMode != "VALUE" || column.SourceResourceType != document.RootResourceType || strings.Trim(strings.TrimSpace(column.SourcePath), ".") != "id" {
			continue
		}
		if column.PublicColumn == "" || column.PublicColumn == "__loom_row_id" {
			continue
		}
		if physical != "" {
			return SourceRowDescriptor{}, fmt.Errorf("multiple emitted physical columns preserve root id")
		}
		physical = column.PublicColumn
	}
	if physical == "" {
		return SourceRowDescriptor{}, fmt.Errorf("emitted physical columns do not preserve root id source %q", source.Column)
	}
	return SourceRowDescriptor{ResourceType: document.RootResourceType, PhysicalColumn: physical}, nil
}
